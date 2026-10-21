package domain

import (
	"fmt"
	"time"
)

// DateLayout 是接口层与领域层共同使用的业务日期格式（不含时分秒）。
const DateLayout = "2006-01-02"

// TimestampLayout 是瞬时时间的对外格式：UTC ISO 8601，带 Z 后缀。
//
// 必须保留亚秒精度（RFC3339Nano 的 "Z07:00" 布局）：
// PostgreSQL 的 TIMESTAMPTZ 精度到微秒，而客户端的 expected_updated_at
// 是直接回传上一次响应里的 updated_at。若对外只渲染到秒，
// 回传值会丢掉微秒部分，原子 CAS 的等值比较必然失败——
// 表现为"刚读到的版本立刻被判为冲突"（实测踩到过）。
const TimestampLayout = "2006-01-02T15:04:05.999999999Z07:00"

// loadLocation 包装 time.LoadLocation，便于集中校验 IANA 时区标识。
func loadLocation(name string) (*time.Location, error) {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("无效时区 %q: %w", name, err)
	}
	return loc, nil
}

// ValidateDateRange 校验业务日期区间：结束日期不得早于开始日期
// （需求文档 5.3 / 5.4，全局基线约定 6.2）。
//
// 参数是 time.Time（通常来自 DATE 列或 YYYY-MM-DD 解析结果），只比较日历日部分。
func ValidateDateRange(start, end time.Time) error {
	s, e := DayOf(start), DayOf(end)
	if e.Before(s) {
		return ErrInvalidDateRange.WithFields(map[string]string{
			"start_date": "开始日期不能晚于结束日期",
			"end_date":   "结束日期不能早于开始日期",
		})
	}
	return nil
}

// DayOf 把瞬时时间截断到 UTC 日历日，避免时分秒参与日期比较。
//
// 导出是因为任务层级校验需要比较"父任务区间是否覆盖子任务"，
// 该逻辑在 domain 之外也要用同一套日历日语义。
func DayOf(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// Today 返回按指定时区计算的"今天"（UTC 日历日表示）。
//
// 全局基线约定 6.2：日期展示、日期比较和"今天/明天"的判断，
// 使用用户配置的时区转换当前瞬时时间后再计算。
func Today(now time.Time, timezone string) (time.Time, error) {
	loc, err := loadLocation(TimezoneOrDefault(timezone, "UTC"))
	if err != nil {
		return time.Time{}, err
	}
	local := now.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC), nil
}

// IsOverdue 判断任务是否逾期。
//
// 数据库模型文档 3.7 明确：逾期是派生展示字段，不得作为持久化状态。
// 规则：end_date < 今天 且状态属于未结束状态。
func IsOverdue(endDate time.Time, status string, today time.Time) bool {
	switch status {
	case TaskStatusNotStarted, TaskStatusInProgress:
	default:
		return false
	}
	return DayOf(endDate).Before(DayOf(today))
}

// ProgressStatus 按进度推导默认状态。
//
// 需求文档 5.4：0% 对应未开始，1%–99% 对应进行中，100% 对应已完成。
// 注意：该函数只用于"未显式指定状态"的默认推导，用户显式设置的状态优先。
func ProgressStatus(progress int) string {
	switch {
	case progress <= 0:
		return TaskStatusNotStarted
	case progress >= 100:
		return TaskStatusCompleted
	default:
		return TaskStatusInProgress
	}
}

// ValidateProgress 校验进度取值范围。
func ValidateProgress(progress int) error {
	if progress < 0 || progress > 100 {
		return ErrValidation.WithField("progress", "进度必须在 0 到 100 之间")
	}
	return nil
}

// FormatDate 把业务日期格式化为 YYYY-MM-DD。
func FormatDate(t time.Time) string { return t.UTC().Format(DateLayout) }

// FormatTimestamp 把瞬时时间格式化为 UTC ISO 8601。
func FormatTimestamp(t time.Time) string { return t.UTC().Format(TimestampLayout) }

// FormatOptionalTimestamp 把可空时间格式化为 *string（nil 保持 nil）。
func FormatOptionalTimestamp(t *time.Time) *string {
	if t == nil {
		return nil
	}
	formatted := FormatTimestamp(*t)
	return &formatted
}

// ParseDate 解析 YYYY-MM-DD 业务日期。
func ParseDate(raw string) (time.Time, error) {
	t, err := time.Parse(DateLayout, raw)
	if err != nil {
		return time.Time{}, ErrValidation.WithField("date", "日期格式必须为 YYYY-MM-DD")
	}
	return t, nil
}

// AtLocalHour 把某个业务日期与本地小时组合成一个瞬时（UTC）。
//
// 提醒需要"在收件人本地的上午 9 点发"，而任务日期是 DATE（无时区）。
// 这个函数负责把两者拼起来，并处理好夏令时：
// 若该地当天不存在指定小时（春季跳变），取该时刻之后最近的合法瞬时，
// 而不是报错——提醒晚一小时发出去，远好过永远不发。
func AtLocalHour(date time.Time, location *time.Location, hour int) time.Time {
	day := DayOf(date)
	local := time.Date(day.Year(), day.Month(), day.Day(), hour, 0, 0, 0, location)
	return local.UTC()
}
