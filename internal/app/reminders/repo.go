// Package reminders 实现邮件提醒（需求文档 5.7、接口契约 4.9）。
//
// 结构：repo.go（SQL）、service.go（生成与派发）、dispatcher.go（周期循环）、http.go（路由器）。
//
// 三个关键约定：
//
//  1. **提醒是"记录"不是"队列"**。需求要求记录发送结果、失败原因与重试次数，
//     因此这些字段就在同一行上，不另建队列表。
//  2. **幂等靠数据库**。dedupe_key 上的唯一约束 + INSERT ... ON CONFLICT DO NOTHING，
//     让"周期性扫描"这种天然会重复运行的机制不需要任何额外去重逻辑。
//  3. **发送失败不影响用户操作**。生成与派发都在后台循环里进行，
//     HTTP 请求路径上只发生一次轻量的插入（事件型提醒）。
package reminders

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
)

// reminderColumns 是 reminders 表的显式列清单。
const reminderColumns = `r.id, r.task_id, r.recipient_id, r.recipient_email, r.kind,
	r.scheduled_for, r.status, r.dedupe_key, r.attempts, r.last_error, r.sent_at,
	r.created_at, r.updated_at`

// reminderReturningColumns 与 reminderColumns 相同，但不带表别名。
const reminderReturningColumns = `id, task_id, recipient_id, recipient_email, kind,
	scheduled_for, status, dedupe_key, attempts, last_error, sent_at,
	created_at, updated_at`

// Record 是 reminders 表的一行。
type Record struct {
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

// CreateParams 是插入一条提醒所需的字段。
type CreateParams struct {
	ID             string
	TaskID         string
	RecipientID    string
	RecipientEmail string
	Kind           string
	ScheduledFor   time.Time
	DedupeKey      string
}

// ListFilter 是提醒列表的筛选条件。
type ListFilter struct {
	// RecipientID 非空时只看该收件人的提醒（普通用户的作用域）。
	RecipientID *string
	TaskID      *string
	Kind        *string
	Status      *string
}

// Candidate 是一个"可能需要在今天生成提醒"的任务。
//
// 仓储只负责把候选集取回来（含邮件正文所需的展示字段），
// 哪一天该发、发给谁由领域函数与用例层判定——
// 权限与业务规则不写在 SQL 里，这是本仓库的一贯分工。
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

// InsertIfAbsent 插入提醒；幂等键已存在时不插入、也不报错。
//
// 返回值 created 表示"这次真的插入了"。调用方据此统计生成量：
// 把"已存在"当成成功会让日志无法区分"扫描没找到"与"全都生成过了"。
func (r *Repository) InsertIfAbsent(
	ctx context.Context, params CreateParams,
) (Record, bool, error) {
	const query = `
		INSERT INTO reminders (
			id, task_id, recipient_id, recipient_email, kind, scheduled_for, dedupe_key
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (dedupe_key) DO NOTHING
		RETURNING ` + reminderReturningColumns

	record, err := scan(r.pool.QueryRow(ctx, query,
		params.ID, params.TaskID, params.RecipientID, params.RecipientEmail,
		params.Kind, params.ScheduledFor, params.DedupeKey,
	))
	if err != nil {
		if apperr.IsNoRows(err) {
			// ON CONFLICT DO NOTHING 命中：该提醒已存在。
			return Record{}, false, nil
		}
		return Record{}, false, apperr.Translate(err, constraintMap)
	}
	return record, true, nil
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
	return record, nil
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

// DispatchItems 取出"计划时间已到且仍待发送"的提醒，并带上渲染所需上下文。
//
// 用 SKIP LOCKED：多实例部署时同一条提醒不会被两个进程同时取走发出。
// 单实例下它没有额外开销，却省掉了"将来上第二个实例"时的重复发送事故。
//
// 收件人用 **JOIN**（而不是 LEFT JOIN）：收件人不可能不存在
// （外键 RESTRICT 保证），若真查不到说明数据被外部改坏了，
// 这时宁可让这条提醒留在 pending 里被反复看到，也不要静默跳过。
func (r *Repository) DispatchItems(
	ctx context.Context, now time.Time, limit int,
) ([]DispatchItem, error) {
	const query = `
		SELECT ` + reminderColumns + `,
		       t.id, t.name, p.id, p.name, t.status, t.progress,
		       COALESCE((
		           SELECT string_agg(COALESCE(NULLIF(owner.display_name, ''), owner.username), '、'
		                             ORDER BY owner.username)
		             FROM task_members tm
		             JOIN users owner ON owner.id = tm.user_id
		            WHERE tm.task_id = t.id AND tm.role = 'owner'
		       ), '') AS owner_names,
		       t.start_date, t.end_date,
		       u.id, u.email, u.status, u.reminder_enabled, u.timezone
		  FROM reminders r
		  JOIN tasks t ON t.id = r.task_id
		  JOIN projects p ON p.id = t.project_id
		  JOIN users u ON u.id = r.recipient_id
		 WHERE r.status = 'pending' AND r.scheduled_for <= $1
		 ORDER BY r.scheduled_for, r.id
		 LIMIT $2
		 FOR UPDATE OF r SKIP LOCKED`

	rows, err := r.pool.Query(ctx, query, now, limit)
	if err != nil {
		return nil, fmt.Errorf("查询待发送提醒失败: %w", err)
	}
	defer rows.Close()

	var items []DispatchItem
	for rows.Next() {
		var item DispatchItem
		if err := rows.Scan(
			&item.ID, &item.TaskID, &item.RecipientID, &item.RecipientEmail,
			&item.Kind, &item.ScheduledFor, &item.Status, &item.DedupeKey,
			&item.Attempts, &item.LastError, &item.SentAt,
			&item.CreatedAt, &item.UpdatedAt,
			&item.Candidate.TaskID, &item.Candidate.TaskName,
			&item.Candidate.ProjectID, &item.Candidate.ProjectName,
			&item.Candidate.Status, &item.Candidate.Progress, &item.Candidate.OwnerNames,
			&item.Candidate.StartDate, &item.Candidate.EndDate,
			&item.Recipient.UserID, &item.Recipient.Email,
			&item.Recipient.Status, &item.Recipient.ReminderEnabled, &item.Recipient.Timezone,
		); err != nil {
			return nil, fmt.Errorf("读取待发送提醒失败: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历待发送提醒失败: %w", err)
	}
	return items, nil
}

// MarkSent 记录发送成功。
func (r *Repository) MarkSent(ctx context.Context, reminderID string, at time.Time) error {
	const query = `
		UPDATE reminders
		   SET status = 'sent', sent_at = $2, attempts = attempts + 1,
		       last_error = '', updated_at = now()
		 WHERE id = $1`

	return r.exec(ctx, query, reminderID, at)
}

// MarkSkipped 记录"按规则不该发"。
//
// attempts 也要 +1：跳过同样是"本轮已处理"，否则它会在每个扫描周期里
// 被反复取出，日志被同一个跳过原因刷满。
func (r *Repository) MarkSkipped(ctx context.Context, reminderID, reason string) error {
	const query = `
		UPDATE reminders
		   SET status = 'skipped', attempts = attempts + 1,
		       last_error = $2, updated_at = now()
		 WHERE id = $1`

	return r.exec(ctx, query, reminderID, reason)
}

// MarkFailed 记录发送失败与原因。
func (r *Repository) MarkFailed(ctx context.Context, reminderID, reason string) error {
	const query = `
		UPDATE reminders
		   SET status = 'failed', attempts = attempts + 1,
		       last_error = $2, updated_at = now()
		 WHERE id = $1`

	return r.exec(ctx, query, reminderID, reason)
}

// ResetForRetry 把一条提醒重新排入待发送。
//
// 允许从 failed 与 skipped 两种状态重试：前者是发送失败，
// 后者可能是"邮件未启用"这类当时不成立、后来成立的条件
// （例如运维刚刚把 SMTP 配好），此时用户应当能手动补发。
//
// 已发送的（sent）不允许重试：重试就等于重复发送，
// 而"同一提醒不重复发送"是需求里的硬要求。
func (r *Repository) ResetForRetry(
	ctx context.Context, reminderID string, scheduledFor time.Time,
) (Record, error) {
	const query = `
		UPDATE reminders
		   SET status = 'pending', scheduled_for = $2,
		       last_error = '', updated_at = now()
		 WHERE id = $1 AND status IN ('failed', 'skipped')
		RETURNING ` + reminderReturningColumns

	record, err := scan(r.pool.QueryRow(ctx, query, reminderID, scheduledFor))
	if err != nil {
		if apperr.IsNoRows(err) {
			return Record{}, r.distinguishRetryable(ctx, reminderID)
		}
		return Record{}, apperr.Translate(err, constraintMap)
	}
	return record, nil
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

// Candidates 取回可能需要在 today 前后触发提醒的任务。
//
// 候选集的两条边界，都是**有意的简化**（已登记在 docs/06）：
//   - 时间型提醒只看 [today-1, today+4] 内的日期：覆盖周末与调休造成的偏移，
//     更远的日期下一轮扫描自然会进入窗口；
//   - 逾期提醒只取"还没有任何逾期提醒行"的任务：需求要求"延期只提醒一次"，
//     因此用一次存在性判断就能把候选集压到很小，不必设时间上限。
func (r *Repository) Candidates(ctx context.Context, today time.Time) ([]Candidate, error) {
	const query = `
		SELECT t.id, t.name, p.id, p.name, t.status, t.progress,
		       COALESCE((
		           SELECT string_agg(COALESCE(NULLIF(u.display_name, ''), u.username), '、'
		                             ORDER BY u.username)
		             FROM task_members tm
		             JOIN users u ON u.id = tm.user_id
		            WHERE tm.task_id = t.id AND tm.role = 'owner'
		       ), '') AS owner_names,
		       t.start_date, t.end_date
		  FROM tasks t
		  JOIN projects p ON p.id = t.project_id
		 WHERE t.deleted_at IS NULL
		   AND t.status NOT IN ('completed', 'cancelled')
		   AND (
		       t.start_date BETWEEN $1::date - 1 AND $1::date + 4
		    OR t.end_date   BETWEEN $1::date - 1 AND $1::date + 4
		    OR (
		        t.end_date < $1::date
		        AND NOT EXISTS (
		            SELECT 1 FROM reminders r
		             WHERE r.task_id = t.id AND r.kind = 'task_overdue'
		        )
		    )
		   )
		 ORDER BY t.end_date, t.id`

	rows, err := r.pool.Query(ctx, query, today)
	if err != nil {
		return nil, fmt.Errorf("查询提醒候选任务失败: %w", err)
	}
	defer rows.Close()

	var candidates []Candidate
	for rows.Next() {
		var candidate Candidate
		if err := rows.Scan(
			&candidate.TaskID, &candidate.TaskName, &candidate.ProjectID, &candidate.ProjectName,
			&candidate.Status, &candidate.Progress, &candidate.OwnerNames,
			&candidate.StartDate, &candidate.EndDate,
		); err != nil {
			return nil, fmt.Errorf("读取候选任务失败: %w", err)
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历候选任务失败: %w", err)
	}
	return candidates, nil
}

// Recipients 返回该任务的默认收件人（需求文档 5.7）：
// 任务成员（含任务持有者）与**项目持有者**。
//
// 用 UNION 而不是 JOIN：需求给的是"并集"语义，写成 JOIN 会在
// "一个人既是任务成员又是项目持有者"时产生重复行，
// 而每行都会去插一条提醒——最终被 dedupe_key 挡住，但白跑一趟。
func (r *Repository) Recipients(ctx context.Context, taskID, projectID string) ([]Recipient, error) {
	const query = `
		SELECT u.id, u.email, u.status, u.reminder_enabled, u.timezone
		  FROM users u
		 WHERE u.id IN (
		     SELECT tm.user_id FROM task_members tm WHERE tm.task_id = $1
		     UNION
		     SELECT pm.user_id FROM project_members pm
		      WHERE pm.project_id = $2 AND pm.role = 'owner'
		 )
		 ORDER BY u.id`

	rows, err := r.pool.Query(ctx, query, taskID, projectID)
	if err != nil {
		return nil, fmt.Errorf("查询提醒收件人失败: %w", err)
	}
	defer rows.Close()

	var recipients []Recipient
	for rows.Next() {
		var recipient Recipient
		if err := rows.Scan(
			&recipient.UserID, &recipient.Email, &recipient.Status,
			&recipient.ReminderEnabled, &recipient.Timezone,
		); err != nil {
			return nil, fmt.Errorf("读取收件人失败: %w", err)
		}
		recipients = append(recipients, recipient)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历收件人失败: %w", err)
	}
	return recipients, nil
}

// TaskRecipient 返回"任务 + 指定用户"的收件信息，供事件型提醒使用。
//
// 与 Recipients 分开是因为事件型提醒只针对**一个**具体用户
// （新加入的成员、新的持有者），不需要把整个收件人集合取回来，
// 也不必在插入时再过滤一遍。
func (r *Repository) TaskRecipient(
	ctx context.Context, taskID, userID string,
) (Recipient, Candidate, error) {
	const query = `
		SELECT u.id, u.email, u.status, u.reminder_enabled, u.timezone,
		       t.id, t.name, p.id, p.name, t.status, t.progress,
		       COALESCE((
		           SELECT string_agg(COALESCE(NULLIF(owner.display_name, ''), owner.username), '、'
		                             ORDER BY owner.username)
		             FROM task_members tm
		             JOIN users owner ON owner.id = tm.user_id
		            WHERE tm.task_id = t.id AND tm.role = 'owner'
		       ), '') AS owner_names,
		       t.start_date, t.end_date
		  FROM tasks t
		  JOIN projects p ON p.id = t.project_id
		  JOIN users u ON u.id = $2
		 WHERE t.id = $1 AND t.deleted_at IS NULL`

	var recipient Recipient
	var candidate Candidate
	err := r.pool.QueryRow(ctx, query, taskID, userID).Scan(
		&recipient.UserID, &recipient.Email, &recipient.Status,
		&recipient.ReminderEnabled, &recipient.Timezone,
		&candidate.TaskID, &candidate.TaskName, &candidate.ProjectID, &candidate.ProjectName,
		&candidate.Status, &candidate.Progress, &candidate.OwnerNames,
		&candidate.StartDate, &candidate.EndDate,
	)
	if err != nil {
		if apperr.IsNoRows(err) {
			return Recipient{}, Candidate{}, domain.New(domain.CodeTaskNotFound, "Task not found")
		}
		return Recipient{}, Candidate{}, fmt.Errorf("查询提醒收件信息失败: %w", err)
	}
	return recipient, candidate, nil
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
		&record.CreatedAt, &record.UpdatedAt,
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
