package reminders

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/listquery"
	"github.com/myonku/project-builder/internal/platform/clock"
	"github.com/myonku/project-builder/internal/platform/mail"
)

// Store 是本模块对持久化的需求。
type Store interface {
	BeginDispatch(context.Context) (func(), bool, error)
	Find(context.Context, string) (Record, error)
	DueIDs(context.Context, time.Time, int) ([]string, error)
	Authorize(context.Context, string, time.Time, time.Duration, func(DispatchItem) mail.Message) (*Delivery, error)
	Settle(context.Context, *Delivery, string, time.Time, string) error
	Expire(context.Context, time.Time) error
	ResetForRetry(context.Context, string, time.Time, *apperr.AuditWrite) (Record, error)
	List(context.Context, ListFilter, string, int, int) ([]Record, error)
	Count(context.Context, ListFilter) (int, error)
}

// Options 是 Service 的构造参数。
type Options struct {
	Store           Store
	Sender          mail.Sender
	Clock           clock.Clock
	Logger          *slog.Logger
	TaskURLTemplate string
	BatchSize       int
	SendInterval    time.Duration
	AttemptTimeout  time.Duration
	// Audit 为 nil 时不记操作记录（只有人工重试会写审计，见 auditWith）。
	Audit apperr.AuditWriter
}

// Service 承载提醒用例。
type Service struct {
	repo           Store
	sender         mail.Sender
	clk            clock.Clock
	logger         *slog.Logger
	urlTemplate    string
	batchSize      int
	sendInterval   time.Duration
	attemptTimeout time.Duration
	audit          apperr.AuditWriter
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
		// A disabled sender pauses plans without claiming or sending them.
		sender = mail.Disabled{}
	}
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}
	if opts.AttemptTimeout <= 0 {
		opts.AttemptTimeout = 2 * time.Minute
	}
	if opts.SendInterval < 0 {
		opts.SendInterval = 0
	}
	return &Service{
		repo:         opts.Store,
		sender:       sender,
		clk:          clk,
		logger:       logger,
		urlTemplate:  opts.TaskURLTemplate,
		batchSize:    batchSize,
		sendInterval: opts.SendInterval, attemptTimeout: opts.AttemptTimeout,
		audit: opts.Audit,
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

// Report summarizes actual dispatch results, never task generation.
type Report struct {
	Sent, Unknown, Failed, Skipped int
	Errors                         []string
}

// Dispatch processes persisted due plans only. It never scans business tasks.
func (s *Service) Dispatch(ctx context.Context) (Report, error) {
	var report Report
	release, ok, err := s.repo.BeginDispatch(ctx)
	if err != nil {
		return report, err
	}
	if !ok {
		return report, nil
	}
	defer release()
	if err := s.repo.Expire(ctx, s.clk.Now().UTC()); err != nil {
		return report, err
	}
	if _, disabled := s.sender.(mail.Disabled); disabled {
		return report, nil
	}
	ids, err := s.repo.DueIDs(ctx, s.clk.Now().UTC(), s.batchSize)
	if err != nil {
		return report, err
	}
	for i, id := range ids {
		if i > 0 && s.sendInterval > 0 {
			timer := time.NewTimer(s.sendInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return report, ctx.Err()
			case <-timer.C:
			}
		}
		delivery, err := s.repo.Authorize(ctx, id, s.clk.Now().UTC(), s.attemptTimeout, s.RenderMessage)
		if err != nil {
			return report, err
		}
		if delivery == nil {
			continue
		}
		sendCtx, cancel := context.WithTimeout(ctx, s.attemptTimeout)
		err = s.sender.Send(sendCtx, delivery.Message)
		cancel()
		status, reason := "sent", ""
		if err != nil {
			reason = truncate(err.Error(), 500)
			status = "unknown"
			if mail.DefinitelyNotAccepted(err) {
				status = "failed"
			}
		}
		// Request shutdown cannot prevent recording an already observed SMTP result.
		settleCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		settleErr := s.repo.Settle(settleCtx, delivery, status, s.clk.Now().UTC(), reason)
		stop()
		if settleErr != nil {
			return report, settleErr
		}
		switch status {
		case "sent":
			report.Sent++
		case "failed":
			report.Failed++
		default:
			report.Unknown++
		}
		if err != nil {
			report.appendError(reason)
		}
	}
	return report, nil
}

// render 渲染一封提醒邮件。
//
// 需求文档 5.7 要求邮件"至少包含项目名、任务名、负责人、日期、状态、进度、
// 任务链接和提醒原因"，这里逐项落实——缺项是验收会直接查的。
// RenderMessage renders a supplied snapshot without claiming or sending it.
func (s *Service) RenderMessage(item DispatchItem) mail.Message {
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
