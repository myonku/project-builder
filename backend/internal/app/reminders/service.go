package reminders

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/listquery"
	"github.com/myonku/project-builder/internal/platform/clock"
	"github.com/myonku/project-builder/internal/platform/idgen"
	"github.com/myonku/project-builder/internal/platform/mail"
)

// Store 是本模块对持久化的需求。
type Store interface {
	InsertIfAbsent(ctx context.Context, params CreateParams) (Record, bool, error)
	Find(ctx context.Context, reminderID string) (Record, error)
	DispatchItems(ctx context.Context, now time.Time, limit int) ([]DispatchItem, error)
	MarkSent(ctx context.Context, reminderID string, at time.Time) error
	MarkSkipped(ctx context.Context, reminderID, reason string) error
	MarkFailed(ctx context.Context, reminderID, reason string) error
	ResetForRetry(ctx context.Context, reminderID string, scheduledFor time.Time, audit *apperr.AuditWrite) (Record, error)
	List(ctx context.Context, filter ListFilter, orderBy string, limit, offset int) ([]Record, error)
	Count(ctx context.Context, filter ListFilter) (int, error)
	Candidates(ctx context.Context, today time.Time) ([]Candidate, error)
	Recipients(ctx context.Context, taskID, projectID string) ([]Recipient, error)
	TaskRecipient(ctx context.Context, taskID, userID string) (Recipient, Candidate, error)
}

// Options 是 Service 的构造参数。
type Options struct {
	Store           Store
	Sender          mail.Sender
	IDs             *idgen.Generator
	Clock           clock.Clock
	Logger          *slog.Logger
	TaskURLTemplate string
	BatchSize       int
	// Audit 为 nil 时不记操作记录（只有人工重试会写审计，见 auditWith）。
	Audit apperr.AuditWriter
}

// Service 承载提醒用例。
type Service struct {
	repo        Store
	sender      mail.Sender
	ids         *idgen.Generator
	clk         clock.Clock
	logger      *slog.Logger
	urlTemplate string
	batchSize   int
	audit       apperr.AuditWriter
}

// NewService 构造提醒服务。
func NewService(opts Options) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	clk := opts.Clock
	if clk == nil {
		clk = clock.NewSystem()
	}
	sender := opts.Sender
	if sender == nil {
		// 未注入发送器时用 Disabled：它返回 ErrDisabled 而不是 nil，
		// 于是"没配邮件"会如实记成 skipped，而不是伪装成发送成功。
		sender = mail.Disabled{}
	}
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}
	return &Service{
		repo:        opts.Store,
		sender:      sender,
		ids:         opts.IDs,
		clk:         clk,
		logger:      logger,
		urlTemplate: opts.TaskURLTemplate,
		batchSize:   batchSize,
		audit:       opts.Audit,
	}
}

// auditWith 构造一条提醒操作记录。
//
// 只有**人工动作**才入账（目前只有手动重试）：后台派发循环没有操作人，
// 而审计表的 actor_id 是必填外键——把收件人或任务持有者当成操作人会
// 记下"一个人没做过的事"。派发结果本身记在 reminders 表里
// （status / last_error / attempts），通过 GET /reminders 可查。
func (s *Service) auditWith(
	actorID string, action domain.AuditAction, reminderID string, changes map[string]string,
) *apperr.AuditWrite {
	return apperr.NewAuditWrite(s.audit, domain.AuditEntry{
		ActorID:    actorID,
		Action:     action,
		ObjectType: domain.AuditObjectReminder,
		ObjectID:   reminderID,
		Changes:    changes,
	})
}

// Page 是列表查询结果。
type Page[T any] struct {
	Items []T
	Total int
	Page  listquery.Pagination
}

// Report 是一轮生成/派发的结果，用于日志与测试断言。
type Report struct {
	// Generated 是新插入的提醒数量。
	Generated int
	// SkippedRecipients 是"因不合格而未生成"的收件人次数
	// （无邮箱、已停用、关闭提醒）。这些**不落库**：
	// 为关闭提醒的用户每分钟写一行 skipped 是纯粹的行数增长，
	// 而它回答的是没有人会问的问题。
	SkippedRecipients int
	// Sent / Failed / Skipped 是派发结果。
	Sent    int
	Failed  int
	Skipped int
	// Errors 是失败原因摘要（最多保留若干条，避免日志被刷爆）。
	Errors []string
}

// Generate 扫描候选任务并生成到期的提醒。
//
// 幂等由数据库保证（dedupe_key 唯一 + ON CONFLICT DO NOTHING），
// 因此本方法可以按任意周期反复运行而不会重复发送。
func (s *Service) Generate(ctx context.Context) (Report, error) {
	var report Report

	today := domain.DayOf(s.clk.Now())
	candidates, err := s.repo.Candidates(ctx, today)
	if err != nil {
		return report, err
	}

	for _, candidate := range candidates {
		if !domain.ReminderTaskStillApplies(domain.ReminderTask{Status: candidate.Status}) {
			continue
		}
		recipients, err := s.repo.Recipients(ctx, candidate.TaskID, candidate.ProjectID)
		if err != nil {
			return report, err
		}
		for _, recipient := range recipients {
			if !domain.CanReceiveReminder(recipient.ReminderRecipient) {
				report.SkippedRecipients++
				continue
			}
			generated, err := s.generateForRecipient(ctx, candidate, recipient)
			if err != nil {
				return report, err
			}
			report.Generated += generated
		}
	}

	if report.Generated > 0 || report.SkippedRecipients > 0 {
		s.logger.Info("提醒生成完成",
			slog.Int("generated", report.Generated),
			slog.Int("skipped_recipients", report.SkippedRecipients),
		)
	}
	return report, nil
}

// generateForRecipient 为单个收件人生成所有适用类型的提醒。
//
// "今天"按**收件人所在时区**计算（需求文档 5.7：底层统一 UTC，
// 上层按用户时区计算业务日期）。因此同一个任务对不同时区的成员
// 可能落在不同的提醒日——这是正确行为，而不是需要抹平的差异。
func (s *Service) generateForRecipient(
	ctx context.Context, candidate Candidate, recipient Recipient,
) (int, error) {
	location, err := time.LoadLocation(recipient.Timezone)
	if err != nil {
		// 时区非法时退回 UTC 并记录：一个用户的时区配错不该让整轮生成失败。
		s.logger.Warn("收件人时区非法，按 UTC 计算今天",
			slog.String("user_id", recipient.UserID),
			slog.String("timezone", recipient.Timezone),
		)
		location = time.UTC
	}
	today, err := domain.Today(s.clk.Now(), location.String())
	if err != nil {
		return 0, domain.ErrInternal.WithCause(err)
	}

	type plan struct {
		kind      string
		window    string
		scheduled time.Time
	}
	var plans []plan

	// 时间型提醒：只在"提前 1 个工作日"那一天触发，窗口取目标日期。
	if domain.ReminderKindAppliesToStatus(domain.ReminderKindStartSoon, candidate.Status) &&
		domain.IsReminderDayFor(today, candidate.StartDate) {
		plans = append(plans, plan{
			kind:   domain.ReminderKindStartSoon,
			window: domain.ReminderDateWindow(candidate.StartDate),
			// 发送时刻取**提醒日**（今天）本地 9 点，而不是开始日期本身——
			// 后者在未来，会让提醒一直卡在"计划时间未到"。
			scheduled: domain.AtLocalHour(today, location, domain.ReminderSendHour),
		})
	}
	if domain.ReminderKindAppliesToStatus(domain.ReminderKindDueSoon, candidate.Status) &&
		domain.IsReminderDayFor(today, candidate.EndDate) {
		plans = append(plans, plan{
			kind:      domain.ReminderKindDueSoon,
			window:    domain.ReminderDateWindow(candidate.EndDate),
			scheduled: domain.AtLocalHour(today, location, domain.ReminderSendHour),
		})
	}
	// 延期：到期日已过，且需求要求"每个任务只提醒一次"，因此窗口是固定的 once。
	// scheduled_for 取今天本地零点（已经过去），于是下一轮派发就会发出。
	if domain.ReminderKindAppliesToStatus(domain.ReminderKindOverdue, candidate.Status) &&
		today.After(domain.DayOf(candidate.EndDate)) {
		plans = append(plans, plan{
			kind:      domain.ReminderKindOverdue,
			window:    domain.ReminderOnceWindow,
			scheduled: domain.AtLocalHour(today, location, 0),
		})
	}

	generated := 0
	for _, item := range plans {
		created, err := s.insert(ctx, insertRequest{
			candidate: candidate,
			recipient: recipient,
			kind:      item.kind,
			window:    item.window,
			scheduled: item.scheduled,
		})
		if err != nil {
			return generated, err
		}
		if created {
			generated++
		}
	}
	return generated, nil
}

// insertRequest 是一次插入所需的字段。
type insertRequest struct {
	candidate Candidate
	recipient Recipient
	kind      string
	window    string
	scheduled time.Time
}

// insert 插入一条提醒（幂等）。
func (s *Service) insert(ctx context.Context, request insertRequest) (bool, error) {
	id, err := s.ids.UUID()
	if err != nil {
		return false, domain.ErrInternal.WithCause(err)
	}
	_, created, err := s.repo.InsertIfAbsent(ctx, CreateParams{
		ID:             id,
		TaskID:         request.candidate.TaskID,
		RecipientID:    request.recipient.UserID,
		RecipientEmail: request.recipient.Email,
		Kind:           request.kind,
		ScheduledFor:   request.scheduled.UTC(),
		DedupeKey: domain.ReminderDedupeKey(
			request.kind, request.candidate.TaskID, request.recipient.UserID, request.window),
	})
	if err != nil {
		return false, err
	}
	return created, nil
}

// RecordEvent 记录一条**事件型**提醒（新任务分配、持有者变更）。
//
// 由 tasks 模块在事务提交后调用。它只写一行，不发送——
// 需求文档 §9 要求"邮件发送不得阻塞用户操作"，因此发送留给后台派发循环。
//
// 返回的错误由调用方**记录日志而不是让请求失败**：任务操作本身已经成功，
// 因为一封提醒没排上队就回滚它，是把次要功能的失败放大成主要功能的失败。
func (s *Service) RecordEvent(ctx context.Context, taskID, userID, kind string) error {
	recipient, candidate, err := s.repo.TaskRecipient(ctx, taskID, userID)
	if err != nil {
		return err
	}
	if !domain.ReminderTaskStillApplies(domain.ReminderTask{Status: candidate.Status}) {
		return nil
	}
	if !domain.CanReceiveReminder(recipient.ReminderRecipient) {
		return nil
	}

	// 事件型提醒的窗口：持有者变更按"日期"去重（同一天内多次变更只提醒一次），
	// 新任务分配按"一次"去重（同一个人被重复加入同一条任务只提醒一次）。
	window := domain.ReminderOnceWindow
	if kind == domain.ReminderKindOwnerChanged {
		window = domain.ReminderDateWindow(domain.DayOf(s.clk.Now()))
	}

	_, err = s.insert(ctx, insertRequest{
		candidate: candidate,
		recipient: recipient,
		kind:      kind,
		window:    window,
		scheduled: s.clk.Now().UTC(),
	})
	return err
}

// Dispatch 发送所有到期的待发送提醒。
//
// 每条提醒在**发送前重新判定一次**：收件人可能已停用或关闭提醒、
// 任务可能已完成或被取消、邮件可能被运维关掉。生成时不落库的
// "不合格收件人"在这里以 skipped 落库——因为这一行确实产生过，
// 而且"为什么没发出去"正是需求要求可查询的内容。
func (s *Service) Dispatch(ctx context.Context) (Report, error) {
	var report Report

	items, err := s.repo.DispatchItems(ctx, s.clk.Now().UTC(), s.batchSize)
	if err != nil {
		return report, err
	}

	for _, item := range items {
		if reason := s.skipReason(item); reason != "" {
			if err := s.repo.MarkSkipped(ctx, item.ID, reason); err != nil {
				return report, err
			}
			report.Skipped++
			continue
		}

		message := s.render(item)
		if err := s.sender.Send(ctx, message); err != nil {
			// "邮件功能未启用"是**配置状态**，不是这条提醒的失败：
			// 记成 failed 会让失败列表永远噪声不断，而 failed 的语义是
			// "需要人排查"。领域层对 skipped 的定义里本来就写了这一项。
			if errors.Is(err, mail.ErrDisabled) {
				if err := s.repo.MarkSkipped(ctx, item.ID,
					"邮件功能未启用，提醒未发送："+truncate(err.Error(), 200)); err != nil {
					return report, err
				}
				report.Skipped++
				continue
			}
			// 失败原因截断后落库：邮件服务器的错误可能很长（含多行诊断），
			// 全文写进 last_error 会让列表接口返回巨大的字段。
			if err := s.repo.MarkFailed(ctx, item.ID, truncate(err.Error(), 500)); err != nil {
				return report, err
			}
			report.Failed++
			report.appendError(err.Error())
			continue
		}

		if err := s.repo.MarkSent(ctx, item.ID, s.clk.Now().UTC()); err != nil {
			return report, err
		}
		report.Sent++
	}

	if report.Sent > 0 || report.Failed > 0 || report.Skipped > 0 {
		s.logger.Info("提醒派发完成",
			slog.Int("sent", report.Sent),
			slog.Int("failed", report.Failed),
			slog.Int("skipped", report.Skipped),
		)
	}
	return report, nil
}

// skipReason 返回"不该发送"的原因；空串表示可以发送。
func (s *Service) skipReason(item DispatchItem) string {
	if !domain.ReminderTaskStillApplies(domain.ReminderTask{
		Status: item.Candidate.Status,
	}) {
		return "任务已完成、已取消或已被删除"
	}
	if !domain.CanReceiveReminder(item.Recipient.ReminderRecipient) {
		return "收件人没有邮箱、已停用或已关闭邮件提醒"
	}
	return ""
}

// render 渲染一封提醒邮件。
//
// 需求文档 5.7 要求邮件"至少包含项目名、任务名、负责人、日期、状态、进度、
// 任务链接和提醒原因"，这里逐项落实——缺项是验收会直接查的。
func (s *Service) render(item DispatchItem) mail.Message {
	candidate := item.Candidate
	context := strings.Builder{}

	fmt.Fprintf(&context, "提醒原因：%s\n\n",
		domain.ReminderReason(item.Kind, candidate.StartDate, candidate.EndDate))
	fmt.Fprintf(&context, "项目：%s\n", candidate.ProjectName)
	fmt.Fprintf(&context, "任务：%s\n", candidate.TaskName)
	if candidate.OwnerNames != "" {
		fmt.Fprintf(&context, "负责人：%s\n", candidate.OwnerNames)
	}
	fmt.Fprintf(&context, "计划周期：%s ~ %s\n",
		candidate.StartDate.Format(domain.DateLayout), candidate.EndDate.Format(domain.DateLayout))
	fmt.Fprintf(&context, "状态：%s\n", taskStatusLabel(candidate.Status))
	fmt.Fprintf(&context, "进度：%d%%\n", candidate.Progress)
	if link := domain.ReminderTaskURL(s.urlTemplate, candidate.ProjectID, candidate.TaskID); link != "" {
		fmt.Fprintf(&context, "任务链接：%s\n", link)
	}

	subject := fmt.Sprintf("【%s】%s", domain.ReminderKindLabel(item.Kind), candidate.TaskName)

	return mail.Message{
		To:      item.RecipientEmail,
		Subject: subject,
		Body: fmt.Sprintf(`%s

--
本邮件由项目计划管理系统自动发送，请勿直接回复。
如需停止接收提醒，可在个人设置中关闭邮件提醒。`, context.String()),
	}
}

// ListInput 是提醒列表的用例输入。
type ListInput struct {
	Actor domain.User
	Kind  *string
	// TaskID / OnlyMine 用于收窄范围。
	TaskID *string
	Page   listquery.PageRequest
	Sort   string
}

// List 分页返回提醒列表。
//
// 作用域：普通用户只看得到**发给自己的**提醒。
// 系统管理员看全部——"处理异常数据"需要能看到"谁没收到、为什么没收到"
// （backend/docs/B01 §5.9 的管理员放行规则）。
func (s *Service) List(ctx context.Context, in ListInput) (Page[Record], error) {
	pagination, err := in.Page.Pagination()
	if err != nil {
		return Page[Record]{}, err
	}
	order, err := ReminderSort.Resolve(in.Sort)
	if err != nil {
		return Page[Record]{}, err
	}

	filter := ListFilter{Kind: in.Kind, TaskID: in.TaskID}
	if !in.Actor.IsAdmin() {
		recipientID := in.Actor.ID
		filter.RecipientID = &recipientID
	}

	total, err := s.repo.Count(ctx, filter)
	if err != nil {
		return Page[Record]{}, err
	}
	items, err := s.repo.List(ctx, filter, ReminderSort.OrderBy(order),
		pagination.Limit(), pagination.Offset())
	if err != nil {
		return Page[Record]{}, err
	}
	return Page[Record]{Items: items, Total: total, Page: pagination}, nil
}

// Detail 返回一条提醒。
func (s *Service) Detail(ctx context.Context, actor domain.User, reminderID string) (Record, error) {
	record, err := s.repo.Find(ctx, reminderID)
	if err != nil {
		return Record{}, err
	}
	// 不是自己的、也不是管理员：与不存在同码，不泄漏"这个 id 是否存在"。
	if !actor.IsAdmin() && record.RecipientID != actor.ID {
		return Record{}, notFound()
	}
	return record, nil
}

// Retry 手动重发一条失败的提醒。
func (s *Service) Retry(ctx context.Context, actor domain.User, reminderID string) (Record, error) {
	record, err := s.Detail(ctx, actor, reminderID)
	if err != nil {
		return Record{}, err
	}
	if record.Status == domain.ReminderStatusPending {
		return Record{}, domain.ErrConflict.
			WithField("reminder_id", "该提醒仍在待发送队列中，无需重试")
	}

	// 计划时间用**注入的时钟**而不是数据库 now()：否则测试里的固定时钟
	// 与库时间分叉，重试后的提醒会因为"计划时间在未来"而永远发不出去。
	updated, err := s.repo.ResetForRetry(ctx, record.ID, s.clk.Now().UTC(),
		s.auditWith(actor.ID, domain.AuditActionUpdated, record.ID, map[string]string{
			"status": domain.ReminderStatusPending,
			"reason": "manual_retry",
		}))
	if err != nil {
		return Record{}, err
	}
	s.logger.Info("提醒已重新排入待发送",
		slog.String("reminder_id", updated.ID),
		slog.String("actor_id", actor.ID),
	)
	return updated, nil
}

// truncate 按字节截断，但保证不切碎多字节字符。
func truncate(raw string, limit int) string {
	if len(raw) <= limit {
		return raw
	}
	runes := []rune(raw)
	if len(runes) <= limit {
		return raw
	}
	return string(runes[:limit]) + "…"
}

// taskStatusLabel 返回任务状态的中文说明。
func taskStatusLabel(status string) string {
	switch status {
	case domain.TaskStatusNotStarted:
		return "未开始"
	case domain.TaskStatusInProgress:
		return "进行中"
	case domain.TaskStatusCompleted:
		return "已完成"
	case domain.TaskStatusCancelled:
		return "已取消"
	default:
		return status
	}
}

// appendError 记录一条失败原因（上限 5 条）。
func (r *Report) appendError(reason string) {
	const limit = 5
	if len(r.Errors) < limit {
		r.Errors = append(r.Errors, reason)
	}
}
