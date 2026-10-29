// Package reminders dispatches transactionally persisted task reminder plans.
package reminders

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/myonku/project-builder/internal/platform/clock"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
)

// reminderColumns 是 reminders 表的显式列清单。
const reminderColumns = `r.id, r.task_id, r.recipient_id, r.recipient_email, r.kind,
	r.scheduled_for, r.status, r.dedupe_key, r.attempts, r.last_error, r.sent_at,
	r.created_at, r.updated_at, r.cycle,r.plan_version,r.covered_by,r.event_id,r.legacy,
 (r.status='failed' AND NOT r.legacy AND reminder_eligible(r.task_id,r.recipient_id)
 AND NOT EXISTS(SELECT 1 FROM reminder_delivery_guards g WHERE (g.task_id,g.recipient_id,g.kind)=(r.task_id,r.recipient_id,r.kind) AND g.active_attempt IS NOT NULL))`

// reminderReturningColumns 与 reminderColumns 相同，但不带表别名。
const reminderReturningColumns = `id, task_id, recipient_id, recipient_email, kind,
	scheduled_for, status, dedupe_key, attempts, last_error, sent_at,
	created_at, updated_at, cycle,plan_version,covered_by,event_id,legacy,false`

// Record 是 reminders 表的一行。
type Record struct {
	Cycle       int64
	PlanVersion int64
	CoveredBy   *string
	EventID     *string
	Legacy      bool
	CanRetry    bool
	Deliveries  []DeliveryEvidence

	ID             string
	TaskID         string
	RecipientID    string
	RecipientEmail string
	Kind           string
	ScheduledFor   time.Time
	Status         string
	DedupeKey      string
	Attempts       int
	LastError      string
	SentAt         *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ListFilter 是提醒列表的筛选条件。
type ListFilter struct {
	// RecipientID 非空时只看该收件人的提醒（普通用户的作用域）。
	RecipientID *string
	TaskID      *string
	Kind        *string
	Status      *string
}

// Candidate is the current task projection captured under authorization locks.
type Candidate struct {
	TaskID      string
	TaskName    string
	ProjectID   string
	ProjectName string
	Status      string
	Progress    int
	OwnerNames  string
	StartDate   time.Time
	EndDate     time.Time
}

// Recipient 是候选任务的收件人及其时区。
type Recipient struct {
	domain.ReminderRecipient
	// Timezone 用于把"今天"换算到收件人所在时区（需求文档 5.7）。
	Timezone string
}

// Repository 是本模块的 SQL 访问层。
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository 构造仓储。
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Find 按主键查询提醒。
func (r *Repository) Find(ctx context.Context, reminderID string) (Record, error) {
	const query = `SELECT ` + reminderColumns + ` FROM reminders r WHERE r.id = $1`

	record, err := scan(r.pool.QueryRow(ctx, query, reminderID))
	if err != nil {
		if apperr.IsNoRows(err) {
			return Record{}, notFound()
		}
		return Record{}, fmt.Errorf("查询提醒失败: %w", err)
	}
	rows, err := r.pool.Query(ctx, `SELECT id,status,authorized_at,expires_at,sent_at,timezone,snapshot,error FROM reminder_delivery_attempts WHERE reminder_id=$1 ORDER BY authorized_at,id`, reminderID)
	if err != nil {
		return Record{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var ev DeliveryEvidence
		if err = rows.Scan(&ev.ID, &ev.Status, &ev.AuthorizedAt, &ev.ExpiresAt, &ev.SentAt, &ev.Timezone, &ev.Snapshot, &ev.Error); err != nil {
			return Record{}, err
		}
		record.Deliveries = append(record.Deliveries, ev)
	}
	return record, rows.Err()
}

// DispatchItem 是一条待发送提醒及其渲染邮件所需的全部上下文。
//
// 与 Record 分开：渲染需要任务、项目与收件人三张表的信息，
// 一次 JOIN 取回即可，避免在派发循环里对每条提醒再查三次（N+1）。
type DispatchItem struct {
	Record
	Candidate Candidate
	Recipient Recipient
}

// ResetForRetry 把一条提醒重新排入待发送。
//
// 允许从 failed 与 skipped 两种状态重试：前者是发送失败，
// 后者可能是"邮件未启用"这类当时不成立、后来成立的条件
// （例如运维刚刚把 SMTP 配好），此时用户应当能手动补发。
//
// 已发送的（sent）不允许重试：重试就等于重复发送，
// 而"同一提醒不重复发送"是需求里的硬要求。
func (r *Repository) ResetForRetry(ctx context.Context, rid string, at time.Time, audit *apperr.AuditWrite) (Record, error) {
	var record Record
	err := apperr.InTx(clock.WithTime(ctx, at), r.pool, func(ctx context.Context, tx apperr.Querier) error {
		tid, err := lockDeliveryTask(ctx, tx, rid)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `SELECT sync_task_reminders($1,$2,false)`, tid, at); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `SELECT g.task_id FROM reminder_delivery_guards g JOIN reminders r ON
    (g.task_id,g.recipient_id,g.kind)=(r.task_id,r.recipient_id,r.kind) WHERE r.id=$1 FOR UPDATE OF g`, rid); err != nil {
			return err
		}
		current, err := scan(tx.QueryRow(ctx, `SELECT `+reminderColumns+` FROM reminders r WHERE r.id=$1 FOR UPDATE`, rid))
		if err != nil {
			return err
		}
		if !current.CanRetry {
			return domain.ErrConflict.WithField("reminder_id", "该计划已失效、已处理或存在待核实发送，不可重试")
		}
		var cover *string
		if err = tx.QueryRow(ctx, `SELECT reminder_success(r.task_id,r.recipient_id,r.kind,reminder_timezone(u.timezone),$2) FROM reminders r JOIN users u ON u.id=r.recipient_id WHERE r.id=$1`, rid, at).Scan(&cover); err != nil {
			return err
		}
		if cover != nil {
			return domain.ErrConflict.WithField("reminder_id", "当天已发送同类提醒")
		}
		record, err = scan(tx.QueryRow(ctx, `UPDATE reminders SET status='pending',scheduled_for=$2,last_error='',updated_at=$2 WHERE id=$1 RETURNING `+reminderReturningColumns, rid, at))
		if err != nil {
			return err
		}
		return audit.Flush(ctx, tx)
	})
	if apperr.IsNoRows(err) {
		return Record{}, notFound()
	}
	return record, err
}

// distinguishRetryable 区分"不存在"与"状态不允许重试"。
func (r *Repository) distinguishRetryable(ctx context.Context, reminderID string) error {
	record, err := r.Find(ctx, reminderID)
	if err != nil {
		return err
	}
	if record.Status == domain.ReminderStatusSent {
		return domain.ErrConflict.
			WithField("reminder_id", "该提醒已发送成功，重试会导致重复发送")
	}
	return domain.ErrConflict.
		WithField("reminder_id", "该提醒当前状态不允许重试")
}

// Count 统计满足条件的提醒数量。
func (r *Repository) Count(ctx context.Context, filter ListFilter) (int, error) {
	query := `SELECT count(*) FROM reminders r WHERE 1 = 1`
	args := []any{}
	query, args = appendFilters(query, args, filter)

	var total int
	if err := r.pool.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("统计提醒失败: %w", err)
	}
	return total, nil
}

// List 分页查询提醒。
func (r *Repository) List(
	ctx context.Context, filter ListFilter, orderBy string, limit, offset int,
) ([]Record, error) {
	query := `SELECT ` + reminderColumns + ` FROM reminders r WHERE 1 = 1`
	args := []any{}
	query, args = appendFilters(query, args, filter)

	args = append(args, limit, offset)
	query += fmt.Sprintf(" ORDER BY %s LIMIT $%d OFFSET $%d", orderBy, len(args)-1, len(args))

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询提醒列表失败: %w", err)
	}
	defer rows.Close()

	var records []Record
	for rows.Next() {
		record, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("读取提醒行失败: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历提醒列表失败: %w", err)
	}
	return records, nil
}

// appendFilters 追加筛选条件，由计数与列表共用（避免二者漂移）。
func appendFilters(query string, args []any, filter ListFilter) (string, []any) {
	if filter.RecipientID != nil {
		args = append(args, *filter.RecipientID)
		query += fmt.Sprintf(` AND r.recipient_id = $%d`, len(args))
	}
	if filter.TaskID != nil {
		args = append(args, *filter.TaskID)
		query += fmt.Sprintf(` AND r.task_id = $%d`, len(args))
	}
	if filter.Kind != nil {
		args = append(args, *filter.Kind)
		query += fmt.Sprintf(` AND r.kind = $%d`, len(args))
	}
	if filter.Status != nil {
		args = append(args, *filter.Status)
		query += fmt.Sprintf(` AND r.status = $%d`, len(args))
	}
	return query, args
}

// exec 执行一条不返回行的写语句。
func (r *Repository) exec(ctx context.Context, query string, args ...any) error {
	if _, err := r.pool.Exec(ctx, query, args...); err != nil {
		return apperr.Translate(err, constraintMap)
	}
	return nil
}

// scan 读取一行提醒。
func scan(row interface{ Scan(dest ...any) error }) (Record, error) {
	var record Record
	err := row.Scan(
		&record.ID, &record.TaskID, &record.RecipientID, &record.RecipientEmail,
		&record.Kind, &record.ScheduledFor, &record.Status, &record.DedupeKey,
		&record.Attempts, &record.LastError, &record.SentAt,
		&record.CreatedAt, &record.UpdatedAt, &record.Cycle, &record.PlanVersion, &record.CoveredBy, &record.EventID, &record.Legacy, &record.CanRetry,
	)
	if err != nil {
		return Record{}, err
	}
	return record, nil
}

// notFound 返回统一的"提醒不存在"错误。
func notFound() error {
	return domain.New(domain.CodeReminderNotFound, "Reminder not found")
}

// constraintMap 把数据库约束翻译为领域错误。
var constraintMap = map[string]apperr.ConstraintError{
	"fk_reminders_task_id_tasks": {
		Code: domain.CodeTaskNotFound, Message: "Task not found",
		Field: "task_id", Detail: "任务不存在",
	},
	"fk_reminders_recipient_id_users": {
		Code: domain.CodeUserNotFound, Message: "User not found",
		Field: "recipient_id", Detail: "收件人不存在",
	},
	"ck_reminders_status": {
		Code: domain.CodeValidationError, Message: "Invalid reminder status",
		Field: "status", Detail: "提醒状态非法",
	},
}

// DeliveryEvidence contains only evidence belonging to the visible reminder.
type DeliveryEvidence struct {
	ID           string          `json:"id"`
	Status       string          `json:"status"`
	AuthorizedAt time.Time       `json:"authorized_at"`
	ExpiresAt    time.Time       `json:"expires_at"`
	SentAt       *time.Time      `json:"sent_at"`
	Timezone     string          `json:"timezone"`
	Snapshot     json.RawMessage `json:"snapshot"`
	Error        string          `json:"error"`
}
