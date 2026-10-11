package domain

import (
	"fmt"
	"strings"
	"time"
)

// 提醒规则（需求文档 5.7）。本文件只放**纯函数**：类型与状态取值、
// 幂等键构造、工作日推算、收件人资格判定、"是否还需要提醒"的判定。
//
// 与数据库、HTTP、SMTP 无关，因此可以表驱动测试——
// 这里每一条规则都对应需求里的一句话，改动应当先改测试。

// 提醒类型。
const (
	// ReminderKindAssigned 新任务分配。
	ReminderKindAssigned = "task_assigned"
	// ReminderKindStartSoon 任务开始前。
	ReminderKindStartSoon = "task_start_soon"
	// ReminderKindDueSoon 任务到期前。
	ReminderKindDueSoon = "task_due_soon"
	// ReminderKindOverdue 任务延期。
	ReminderKindOverdue = "task_overdue"
	// ReminderKindOwnerChanged 持有者变更。
	ReminderKindOwnerChanged = "task_owner_changed"
)

// ReminderKinds 是全部合法类型（供校验与文档使用）。
var ReminderKinds = []string{
	ReminderKindAssigned,
	ReminderKindStartSoon,
	ReminderKindDueSoon,
	ReminderKindOverdue,
	ReminderKindOwnerChanged,
}

// 提醒状态。
const (
	ReminderStatusPending = "pending"
	ReminderStatusSent    = "sent"
	ReminderStatusFailed  = "failed"
	// ReminderStatusSkipped 表示按规则**不该发**（收件人不合格、任务不再适用、
	// 邮件未启用等）。它与 failed 必须分开：失败要重试并需要人排查，
	// 跳过是预期行为，混在一起会让"失败列表"永远噪声不断。
	ReminderStatusSkipped = "skipped"
)

// ReminderStatuses 是全部合法状态。
var ReminderStatuses = []string{
	ReminderStatusPending, ReminderStatusSent, ReminderStatusFailed, ReminderStatusSkipped,
}

// ReminderSendHour 是提醒在**收件人本地时区**内的发送时刻。
//
// 提醒按工作日计算（需求文档 5.7），但"一天里什么时候发"需求没说。
// 取本地上午 9 点：既不半夜发信，也留出一天的处理时间。
const ReminderSendHour = 9

// ReminderOnceWindow 是"每个任务只提醒一次"的时间窗口标记（需求文档 5.7：延期只提醒一次）。
const ReminderOnceWindow = "once"

// ReminderDateWindow 返回某个业务日期对应的时间窗口标记。
//
// 幂等键必须包含时间窗口：否则"到期前提醒"一辈子只会发一次——
// 用户换了个到期日之后再也收不到提醒，而系统看起来一切正常。
func ReminderDateWindow(date time.Time) string {
	return date.Format(time.DateOnly)
}

// ReminderDedupeKey 构造幂等键。
//
// 需求文档 5.7 的原话是"同一任务、同一收件人、同一提醒类型和同一时间窗口
// 不得重复发送"。这个键就是把那句话写下来：
//
//	<类型>:<任务>:<收件人>:<窗口>
//
// 生成侧据此 INSERT ... ON CONFLICT DO NOTHING，于是"周期性扫描"这类
// 天然会重复运行的机制不需要任何额外去重逻辑。
func ReminderDedupeKey(kind, taskID, recipientID, window string) string {
	return strings.Join([]string{kind, taskID, recipientID, window}, ":")
}

// ReminderTaskURL 把配置里的模板渲染为任务链接。
//
// 模板缺失时返回空串而不是报错：邮件正文少一行链接远好过整封发不出去。
func ReminderTaskURL(template, taskID string) string {
	trimmed := strings.TrimSpace(template)
	if trimmed == "" || taskID == "" {
		return ""
	}
	if !strings.Contains(trimmed, "{task_id}") {
		// 配置里没有占位符时，把任务 id 追加在末尾，至少保证链接不是死路。
		return strings.TrimRight(trimmed, "/") + "/" + taskID
	}
	return strings.ReplaceAll(trimmed, "{task_id}", taskID)
}

// PreviousBusinessDay 返回 date 之前最近的一个工作日（周一至周五）。
//
// 需求文档 5.7："项目/任务周期节点默认提前 1 个工作日提醒"。
// 因此到期日是周一的任务，应在上周五提醒——直接减一天会落到周日，
// 那一天没人看邮件，提醒等于没发。
//
// 首期不含法定节假日日历（需求只说"按工作日"），这一点在 docs 中登记为已知简化。
func PreviousBusinessDay(date time.Time) time.Time {
	cursor := DayOf(date).AddDate(0, 0, -1)
	for cursor.Weekday() == time.Saturday || cursor.Weekday() == time.Sunday {
		cursor = cursor.AddDate(0, 0, -1)
	}
	return cursor
}

// IsReminderDayFor 报告 today 是否应触发 date 对应的"提前 1 个工作日"提醒。
func IsReminderDayFor(today, date time.Time) bool {
	return DayOf(today).Equal(PreviousBusinessDay(DayOf(date)))
}

// ReminderRecipient 是收件人判定的输入（取自 users 表的相关列）。
type ReminderRecipient struct {
	UserID          string
	Email           string
	Status          string
	ReminderEnabled bool
}

// CanReceiveReminder 报告该用户是否应当收到提醒。
//
// 需求文档 5.7：无邮箱、已停用、关闭提醒时不发送。
// 三条都是"跳过"而不是"失败"：它们是用户的正常状态，不是系统故障。
func CanReceiveReminder(recipient ReminderRecipient) bool {
	if strings.TrimSpace(recipient.Email) == "" {
		return false
	}
	if recipient.Status != UserStatusActive {
		return false
	}
	return recipient.ReminderEnabled
}

// ReminderTask 是"是否需要提醒"判定的输入。
type ReminderTask struct {
	Status string
	// Deleted 表示任务已被逻辑删除（需求文档 5.7："不再适用时不发送"）。
	Deleted bool
}

// ReminderTaskStillApplies 报告任务是否仍需要提醒。
//
// 需求文档 5.7：任务已取消或不再适用时不发送。完成的任务同理——
// 已经做完的事再提醒就是噪声，而且会让"提醒"这个功能被用户关掉。
func ReminderTaskStillApplies(task ReminderTask) bool {
	if task.Deleted {
		return false
	}
	switch task.Status {
	case TaskStatusCompleted, TaskStatusCancelled:
		return false
	default:
		return true
	}
}

// ReminderKindAppliesToStatus 报告某类提醒对给定任务状态是否有意义。
//
// 与 ReminderTaskStillApplies 分开：前者是"任务还值不值得提醒"（全体类型），
// 这里是"这一类提醒对这个状态还有没有意义"。
func ReminderKindAppliesToStatus(kind, status string) bool {
	if !ReminderTaskStillApplies(ReminderTask{Status: status}) {
		return false
	}
	// 尚未开始的任务不需要"到期前"提醒：它连开始都没开始，
	// 该收的是"开始前"提醒。发出去只会让人以为系统算错了。
	if kind == ReminderKindDueSoon && status == TaskStatusNotStarted {
		return false
	}
	return true
}

// ReminderKindLabel 返回类型的中文说明，用于邮件正文与列表展示。
func ReminderKindLabel(kind string) string {
	switch kind {
	case ReminderKindAssigned:
		return "新任务分配"
	case ReminderKindStartSoon:
		return "任务即将开始"
	case ReminderKindDueSoon:
		return "任务即将到期"
	case ReminderKindOverdue:
		return "任务已延期"
	case ReminderKindOwnerChanged:
		return "任务持有者变更"
	default:
		return kind
	}
}

// ReminderReason 返回邮件正文里的"提醒原因"一句话。
func ReminderReason(kind string, startDate, endDate time.Time) string {
	switch kind {
	case ReminderKindAssigned:
		return "你被加入了该任务"
	case ReminderKindStartSoon:
		return fmt.Sprintf("任务将在 %s 开始", startDate.Format(DateLayout))
	case ReminderKindDueSoon:
		return fmt.Sprintf("任务将在 %s 到期", endDate.Format(DateLayout))
	case ReminderKindOverdue:
		return fmt.Sprintf("任务的到期日 %s 已过", endDate.Format(DateLayout))
	case ReminderKindOwnerChanged:
		return "你已成为该任务的持有者"
	default:
		return "任务有新进展"
	}
}
