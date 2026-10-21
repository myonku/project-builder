// Package tasks 实现任务的用例、SQL 访问与 HTTP 端点。
package tasks

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
)

// taskColumns 是 tasks 表的显式列清单。
const taskColumns = `t.id, t.project_id, t.parent_id, t.created_by, t.name, t.description,
	t.status, t.progress, t.priority, t.start_date, t.end_date,
	t.is_milestone, t.is_shared, t.deleted_at, t.created_at, t.updated_at, t.updated_by`

// taskReturningColumns 与 taskColumns 相同，但不带表别名。
//
// INSERT/UPDATE 的 RETURNING 子句没有 FROM 别名，写 t.id 会报
// "missing FROM-clause entry for table t"。
const taskReturningColumns = `id, project_id, parent_id, created_by, name, description,
	status, progress, priority, start_date, end_date,
	is_milestone, is_shared, deleted_at, created_at, updated_at, updated_by`

// TaskRecord 是 tasks 表的行记录。
type TaskRecord struct {
	ID          string
	ProjectID   string
	ParentID    *string
	CreatedBy   string
	Name        string
	Description *string
	Status      string
	Progress    int
	Priority    int
	StartDate   time.Time
	EndDate     time.Time
	IsMilestone bool
	IsShared    bool
	DeletedAt   *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
	// UpdatedBy 是最近一次修改本行的用户（需求文档 5.6：查看任务最后修改人）。
	//
	// 它与 UpdatedAt 跟踪**同一个事件**：两者在同一条写语句里一起推进。
	// 成员关系与附件不触碰 tasks 行，因此两者都不变。
	// 完整的操作历史属于 Audit（activity_logs）阶段；本字段是"当前指针"。
	UpdatedBy string

	// Membership 是当前请求用户在本任务的成员关系；nil 表示不是任务成员。
	Membership *MemberRecord
}

// ToDomain 转换为领域实体。
func (r TaskRecord) ToDomain() domain.Task {
	return domain.Task{
		ID:          r.ID,
		ProjectID:   r.ProjectID,
		ParentID:    r.ParentID,
		CreatedBy:   r.CreatedBy,
		Name:        r.Name,
		Description: r.Description,
		Status:      r.Status,
		Progress:    r.Progress,
		Priority:    r.Priority,
		StartDate:   r.StartDate,
		EndDate:     r.EndDate,
		IsMilestone: r.IsMilestone,
		IsShared:    r.IsShared,
		DeletedAt:   r.DeletedAt,
	}
}

// MemberRecord 是 task_members 的一行，并带上用户展示字段。
type MemberRecord struct {
	ID              string
	TaskID          string
	UserID          string
	Role            string
	CreatedAt       time.Time
	Username        string
	DisplayName     string
	Email           string
	AvatarUpdatedAt *time.Time
}

// ToDomain 转换为领域实体。
func (r MemberRecord) ToDomain() domain.TaskMember {
	return domain.TaskMember{ID: r.ID, TaskID: r.TaskID, UserID: r.UserID, Role: r.Role}
}

// CreateTaskParams 是创建任务所需的字段。
type CreateTaskParams struct {
	ID          string
	ProjectID   string
	ParentID    *string
	CreatedBy   string
	Name        string
	Description *string
	Status      string
	Progress    int
	Priority    int
	StartDate   time.Time
	EndDate     time.Time
	IsMilestone bool
	IsShared    bool
	MemberID    string
	// Audit 是要在同一事务里写下的一条操作记录；nil 表示不记。
	// 由用例层构造（见 apperr.AuditWrite）。
	Audit *apperr.AuditWrite
}

// TaskUpdateParams 是更新任务的字段。
type TaskUpdateParams struct {
	ID              string
	Name            *string
	Description     *string
	DescriptionSet  bool
	Status          *string
	Progress        *int
	Priority        *int
	StartDate       *time.Time
	EndDate         *time.Time
	IsMilestone     *bool
	IsShared        *bool
	ParentID        *string
	ParentIDSet     bool
	ExpectedUpdated time.Time
	// ActorID 是要写入 tasks.updated_by 的用户。
	//
	// 它不是"可选元数据"：写路径必须提供，否则"最后修改人"会停在旧值上，
	// 而这种错误不会有任何报错——字段只是静静地不再更新。
	ActorID string
	// Audit 见 CreateTaskParams 的说明。
	Audit *apperr.AuditWrite
}

// ListFilter 是任务列表筛选条件。
type ListFilter struct {
	ProjectID      string
	UserID         string
	Status         *string
	AssigneeID     *string
	OwnerID        *string
	IsShared       *bool
	IncludeDeleted bool
}

// MemberFilter 是任务成员列表筛选条件。
type MemberFilter struct {
	TaskID string
	Role   *string
}

// Repository 是 tasks 模块的 SQL 访问层。
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository 构造仓储。
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Pool 暴露连接池，供用例层开启事务。
func (r *Repository) Pool() *pgxpool.Pool { return r.pool }

// visibilityCondition 是"当前用户可见的任务"的筛选条件。
//
// 与项目的可见性规则一致，但多一层任务自身的共享开关：
//   - 项目成员可见项目下的全部任务；
//   - 非项目成员只有在"项目组内共享 + 任务已共享"时才可见该任务；
//   - 归档项目仍然可见（只读），因此不在此处排除。
//
// 全部使用独立 EXISTS 子查询，绝不与筛选条件共用 JOIN
// （共用 JOIN 正是 早期实现组合筛选恒为空的根因，见 backend/docs/B01 5.3）。
func visibilityCondition(userParam int) string {
	user := fmt.Sprintf("$%d", userParam)
	return fmt.Sprintf(`(
		EXISTS (SELECT 1 FROM project_members pm
		         WHERE pm.project_id = t.project_id AND pm.user_id = %[1]s)
		OR (
			t.is_shared
			AND EXISTS (
				SELECT 1 FROM projects p
				  JOIN groups g ON g.id = p.group_id
				  JOIN group_members gm ON gm.group_id = g.id
				 WHERE p.id = t.project_id
				   AND p.visibility IN ('group_readonly', 'group_editable')
				   AND g.status = 'active'
				   AND gm.user_id = %[1]s AND gm.status = 'active'
			)
		)
		-- 系统管理员可以看到全部任务（需求文档 4.1）。用与当前行无关的子查询，
		-- 因此列表与计数两个调用方都不需要改动各自的连接子句。
		OR EXISTS (SELECT 1 FROM users au
		            WHERE au.id = %[1]s AND au.system_role = 'admin')
	)`, user)
}

// CreateTaskInTx 在一个事务内创建任务与持有者成员关系。
//
// 需求文档 5.4：创建任务时默认将创建者加入任务成员并设为持有者。
// 单一写路径：tasks.created_by 与 task_members(owner) 必须同生共死。
func (r *Repository) CreateTaskInTx(ctx context.Context, params CreateTaskParams) (TaskRecord, error) {
	var created TaskRecord

	err := apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		const insert = `
			INSERT INTO tasks (
				id, project_id, parent_id, created_by, name, description,
				status, progress, priority, start_date, end_date, is_milestone, is_shared,
				updated_by
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
			RETURNING ` + taskReturningColumns

		if err := tx.QueryRow(ctx, insert,
			params.ID, params.ProjectID, params.ParentID, params.CreatedBy,
			params.Name, params.Description, params.Status, params.Progress,
			params.Priority, params.StartDate, params.EndDate,
			params.IsMilestone, params.IsShared,
			// 创建也是一次"修改"：updated_by 从第一天起就有值，
			// 因此 updated_by 与 updated_at 的不变式在创建路径上同样成立。
			params.CreatedBy,
		).Scan(
			&created.ID, &created.ProjectID, &created.ParentID, &created.CreatedBy,
			&created.Name, &created.Description, &created.Status, &created.Progress,
			&created.Priority, &created.StartDate, &created.EndDate,
			&created.IsMilestone, &created.IsShared, &created.DeletedAt,
			&created.CreatedAt, &created.UpdatedAt, &created.UpdatedBy,
		); err != nil {
			return apperr.Translate(err, constraintMap)
		}

		const insertOwner = `
			INSERT INTO task_members (id, task_id, user_id, role)
			VALUES ($1, $2, $3, 'owner')`

		if _, err := tx.Exec(ctx, insertOwner, params.MemberID, params.ID, params.CreatedBy); err != nil {
			return apperr.Translate(err, constraintMap)
		}
		// 审计与业务在同一个事务里：业务回滚则记录也不存在。
		return params.Audit.Flush(ctx, tx)
	})
	if err != nil {
		return TaskRecord{}, err
	}
	return created, nil
}

// FindTaskForUser 查询任务并附带当前用户的任务成员关系。
//
// includeDeleted 为 false 时排除已逻辑删除的任务。
// **单查与列表必须使用同一套过滤条件**：早期实现列表过滤了 deleted_at，
// 单查没有，导致已删除任务仍可被读取（见 backend/docs/B01 5.5）。
func (r *Repository) FindTaskForUser(ctx context.Context, taskID, userID string, includeDeleted bool) (TaskRecord, error) {
	query := `
		SELECT ` + taskColumns + `,
		       tm.id, tm.task_id, tm.user_id, tm.role
		  FROM tasks t
		  LEFT JOIN task_members tm ON tm.task_id = t.id AND tm.user_id = $2
		 WHERE t.id = $1`
	if !includeDeleted {
		query += ` AND t.deleted_at IS NULL`
	}

	var record TaskRecord
	var memberID, memberTaskID, memberUserID, memberRole *string

	err := r.pool.QueryRow(ctx, query, taskID, userID).Scan(
		&record.ID, &record.ProjectID, &record.ParentID, &record.CreatedBy,
		&record.Name, &record.Description, &record.Status, &record.Progress,
		&record.Priority, &record.StartDate, &record.EndDate,
		&record.IsMilestone, &record.IsShared, &record.DeletedAt,
		&record.CreatedAt, &record.UpdatedAt, &record.UpdatedBy,
		&memberID, &memberTaskID, &memberUserID, &memberRole,
	)
	if err != nil {
		if apperr.IsNoRows(err) {
			return TaskRecord{}, notFound()
		}
		return TaskRecord{}, fmt.Errorf("查询任务失败: %w", err)
	}

	if memberID != nil && memberUserID != nil && memberRole != nil {
		record.Membership = &MemberRecord{
			ID:     *memberID,
			TaskID: derefString(memberTaskID),
			UserID: *memberUserID,
			Role:   *memberRole,
		}
	}
	return record, nil
}

// FindTaskByID 按主键查询任务，不涉及任何用户身份。
//
// 用于父子关系校验等"对象本身的性质"判断：那些判断与调用者是否有权查看父任务无关，
// 也绝不能用空 userID 去套用带身份参数的查询（会触发 22P02）。
func (r *Repository) FindTaskByID(ctx context.Context, taskID string, includeDeleted bool) (TaskRecord, error) {
	query := `SELECT ` + taskColumns + ` FROM tasks t WHERE t.id = $1`
	if !includeDeleted {
		query += ` AND t.deleted_at IS NULL`
	}

	var record TaskRecord
	err := r.pool.QueryRow(ctx, query, taskID).Scan(
		&record.ID, &record.ProjectID, &record.ParentID, &record.CreatedBy,
		&record.Name, &record.Description, &record.Status, &record.Progress,
		&record.Priority, &record.StartDate, &record.EndDate,
		&record.IsMilestone, &record.IsShared, &record.DeletedAt,
		&record.CreatedAt, &record.UpdatedAt, &record.UpdatedBy,
	)
	if err != nil {
		if apperr.IsNoRows(err) {
			return TaskRecord{}, notFound()
		}
		return TaskRecord{}, fmt.Errorf("查询任务失败: %w", err)
	}
	return record, nil
}

// CountTasks 统计符合条件的可见任务数量。
func (r *Repository) CountTasks(ctx context.Context, filter ListFilter) (int, error) {
	query := `
		SELECT count(*)
		  FROM tasks t
		 WHERE t.project_id = $1
		   AND ` + visibilityCondition(2)
	args := []any{filter.ProjectID, filter.UserID}

	// 必须复用与列表完全相同的筛选构造：只加一半条件会让
	// pagination.total 与实际返回的条目数不一致（实测报出 total 偏大）。
	query, args = appendMemberFilters(query, args, filter)
	query, args = appendTaskFilters(query, args, filter)

	var total int
	if err := r.pool.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("统计任务失败: %w", err)
	}
	return total, nil
}

// appendMemberFilters 追加 owner_id / assignee_id 的成员条件。
//
// 每一位成员条件各用**独立的 EXISTS 子查询**：
//
// 反例（早期实现实测缺陷，backend/docs/B01 5.3）：
//
//	JOIN task_members tm ON tm.task_id = t.id
//	WHERE tm.role = 'owner' AND tm.user_id = $owner AND tm.user_id = $assignee
//
// 两个谓词落在同一行上，要求同一个人既是 owner 又是 assignee，结果恒为空
// （实测：单条件各返回 1 条，组合返回 0 条）。
//
// 另一个必须避免的写法是复用 SELECT 中按"当前用户"关联的 tm：
// 用它判断 owner_id 会把"当前用户是持有者"误当成"被筛选的用户是持有者"。
// 每个条件都从 task_members 独立判定，语义才正确。
//
// 该函数由 CountTasks 与 ListTasks 共用，保证 total 与列表结果永远一致。
func appendMemberFilters(query string, args []any, filter ListFilter) (string, []any) {
	if filter.OwnerID != nil {
		args = append(args, *filter.OwnerID)
		query += fmt.Sprintf(` AND EXISTS (
			SELECT 1 FROM task_members mo
			 WHERE mo.task_id = t.id AND mo.role = 'owner' AND mo.user_id = $%d)`, len(args))
	}
	if filter.AssigneeID != nil {
		args = append(args, *filter.AssigneeID)
		query += fmt.Sprintf(` AND EXISTS (
			SELECT 1 FROM task_members ma
			 WHERE ma.task_id = t.id AND ma.user_id = $%d)`, len(args))
	}
	return query, args
}

// ListTasks 分页查询可见任务。
//
// 组合筛选的正确做法（backend/docs/B01 5.3）：每位成员条件各用一个**别名 JOIN**。
//
// 反例（早期实现实测缺陷）：
//
//	JOIN task_members tm ON tm.task_id = t.id
//	WHERE tm.role = 'owner' AND tm.user_id = $owner AND tm.user_id = $assignee
//
// 两个谓词落在同一行上，要求同一个人既是 owner 又是 assignee，结果恒为空
// （实测：单条件各返回 1 条，组合返回 0 条）。
//
// 正解：owner 与 assignee 各用独立 JOIN，再叠加 DISTINCT 防止 JOIN 行膨胀。
func (r *Repository) ListTasks(ctx context.Context, filter ListFilter, orderBy string, limit, offset int) ([]TaskRecord, error) {
	query := `
		SELECT DISTINCT ` + taskColumns + `,
		       tm.id, tm.task_id, tm.user_id, tm.role
		  FROM tasks t
		  LEFT JOIN task_members tm ON tm.task_id = t.id AND tm.user_id = $2
		 WHERE t.project_id = $1
		   AND ` + visibilityCondition(2)
	args := []any{filter.ProjectID, filter.UserID}

	// 成员筛选与 CountTasks 共用同一实现，避免 total 与列表结果不一致。
	query, args = appendMemberFilters(query, args, filter)

	query, args = appendTaskFilters(query, args, filter)

	args = append(args, limit, offset)
	query += fmt.Sprintf(" ORDER BY %s LIMIT $%d OFFSET $%d", orderBy, len(args)-1, len(args))

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询任务列表失败: %w", err)
	}
	defer rows.Close()

	var records []TaskRecord
	for rows.Next() {
		var record TaskRecord
		var memberID, memberTaskID, memberUserID, memberRole *string

		if err := rows.Scan(
			&record.ID, &record.ProjectID, &record.ParentID, &record.CreatedBy,
			&record.Name, &record.Description, &record.Status, &record.Progress,
			&record.Priority, &record.StartDate, &record.EndDate,
			&record.IsMilestone, &record.IsShared, &record.DeletedAt,
			&record.CreatedAt, &record.UpdatedAt, &record.UpdatedBy,
			&memberID, &memberTaskID, &memberUserID, &memberRole,
		); err != nil {
			return nil, fmt.Errorf("读取任务行失败: %w", err)
		}

		if memberID != nil && memberUserID != nil && memberRole != nil {
			record.Membership = &MemberRecord{
				ID:     *memberID,
				TaskID: derefString(memberTaskID),
				UserID: *memberUserID,
				Role:   *memberRole,
			}
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历任务列表失败: %w", err)
	}
	return records, nil
}

// ListAncestors 返回任务的祖先链（自底向上），用于层级与循环引用校验。
//
// 使用递归 CTE 而不是在应用层逐级查询：逐级查询在深层级下是 N+1，
// 且无法在一次快照内保证一致性。
func (r *Repository) ListAncestors(ctx context.Context, taskID string) ([]TaskRecord, error) {
	// 注意：taskColumns 带 `t.` 前缀，而递归 CTE 的第二段与最外层 SELECT 里
	// 并没有名为 t 的表别名，直接引用会报 'missing FROM-clause entry for table "t"'。
	// 因此这里显式写出不带前缀的列清单。
	const query = `
		WITH RECURSIVE chain AS (
			SELECT id, project_id, parent_id, created_by, name, description,
			       status, progress, priority, start_date, end_date,
			       is_milestone, is_shared, deleted_at, created_at, updated_at, updated_by,
			       0 AS depth
			  FROM tasks
			 WHERE id = $1
			UNION ALL
			SELECT t.id, t.project_id, t.parent_id, t.created_by, t.name, t.description,
			       t.status, t.progress, t.priority, t.start_date, t.end_date,
			       t.is_milestone, t.is_shared, t.deleted_at, t.created_at, t.updated_at,
			       t.updated_by,
			       c.depth + 1
			  FROM tasks t
			  JOIN chain c ON t.id = c.parent_id
			 WHERE c.depth < 32
		)
		SELECT id, project_id, parent_id, created_by, name, description,
		       status, progress, priority, start_date, end_date,
		       is_milestone, is_shared, deleted_at, created_at, updated_at, updated_by
		  FROM chain ORDER BY depth`

	rows, err := r.pool.Query(ctx, query, taskID)
	if err != nil {
		return nil, fmt.Errorf("查询任务祖先链失败: %w", err)
	}
	defer rows.Close()

	var records []TaskRecord
	for rows.Next() {
		var record TaskRecord
		if err := rows.Scan(
			&record.ID, &record.ProjectID, &record.ParentID, &record.CreatedBy,
			&record.Name, &record.Description, &record.Status, &record.Progress,
			&record.Priority, &record.StartDate, &record.EndDate,
			&record.IsMilestone, &record.IsShared, &record.DeletedAt,
			&record.CreatedAt, &record.UpdatedAt, &record.UpdatedBy,
		); err != nil {
			return nil, fmt.Errorf("读取祖先链行失败: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历祖先链失败: %w", err)
	}
	return records, nil
}

// UpdateTask 以原子 CAS 更新任务并回读最新 updated_at。
//
// updated_by 与 updated_at **在同一条语句里一起推进**：这是本表的一条不变式，
// 由集成测试 `TestTaskUpdatedByAdvancesWithUpdatedAt` 锁住。
//
// 单语句写入也显式开事务：审计必须与业务同事务（《A09》），
// 而"业务成功但审计缺失"是本次实现里最需要结构性排除的失败模式。
func (r *Repository) UpdateTask(ctx context.Context, params TaskUpdateParams) (TaskRecord, error) {
	const query = `
		UPDATE tasks
		   SET name         = COALESCE($3, name),
		       description  = CASE WHEN $4 THEN $5 ELSE description END,
		       status       = COALESCE($6, status),
		       progress     = COALESCE($7, progress),
		       priority     = COALESCE($8, priority),
		       start_date   = COALESCE($9, start_date),
		       end_date     = COALESCE($10, end_date),
		       is_milestone = COALESCE($11, is_milestone),
		       is_shared    = COALESCE($12, is_shared),
		       parent_id    = CASE WHEN $13 THEN $14 ELSE parent_id END,
		       updated_by   = $15,
		       updated_at   = now()
		 WHERE id = $1 AND updated_at = $2
		RETURNING ` + taskReturningColumns

	var record TaskRecord
	err := apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		if err := tx.QueryRow(ctx, query,
			params.ID, params.ExpectedUpdated,
			params.Name, params.DescriptionSet, params.Description,
			params.Status, params.Progress, params.Priority,
			params.StartDate, params.EndDate,
			params.IsMilestone, params.IsShared,
			params.ParentIDSet, params.ParentID,
			params.ActorID,
		).Scan(
			&record.ID, &record.ProjectID, &record.ParentID, &record.CreatedBy,
			&record.Name, &record.Description, &record.Status, &record.Progress,
			&record.Priority, &record.StartDate, &record.EndDate,
			&record.IsMilestone, &record.IsShared, &record.DeletedAt,
			&record.CreatedAt, &record.UpdatedAt, &record.UpdatedBy,
		); err != nil {
			// 不在这里做存在性查询：那会在事务开着的时候再借一条连接。
			// 把"没有匹配行"原样上抛，由事务之外的 distinguishMissing 区分 404 与 409。
			if apperr.IsNoRows(err) {
				return err
			}
			return apperr.Translate(err, constraintMap)
		}
		return params.Audit.Flush(ctx, tx)
	})
	if err != nil {
		if apperr.IsNoRows(err) {
			return TaskRecord{}, r.distinguishMissing(ctx, params.ID)
		}
		return TaskRecord{}, err
	}
	return record, nil
}

// SoftDeleteTask 逻辑删除任务（CAS）。
//
// 需求文档 5.4：已产生提醒或操作记录的任务采用逻辑删除。
// 逻辑删除同样是一次修改，因此 updated_by 与 updated_at 一起推进——
// 谁删的这件事必须能被回答（需求文档 5.8 把删除列为关键操作）。
func (r *Repository) SoftDeleteTask(
	ctx context.Context, taskID, actorID string, expectedUpdated, deletedAt time.Time,
	audit *apperr.AuditWrite,
) (TaskRecord, error) {
	const query = `
		UPDATE tasks
		   SET deleted_at = $3, updated_by = $4, updated_at = now()
		 WHERE id = $1 AND updated_at = $2 AND deleted_at IS NULL
		RETURNING ` + taskReturningColumns

	var record TaskRecord
	err := apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		if err := tx.QueryRow(ctx, query, taskID, expectedUpdated, deletedAt, actorID).Scan(
			&record.ID, &record.ProjectID, &record.ParentID, &record.CreatedBy,
			&record.Name, &record.Description, &record.Status, &record.Progress,
			&record.Priority, &record.StartDate, &record.EndDate,
			&record.IsMilestone, &record.IsShared, &record.DeletedAt,
			&record.CreatedAt, &record.UpdatedAt, &record.UpdatedBy,
		); err != nil {
			// 同 UpdateTask：把"没有匹配行"原样上抛，由事务外区分 404 与 409。
			if apperr.IsNoRows(err) {
				return err
			}
			return apperr.Translate(err, constraintMap)
		}
		return audit.Flush(ctx, tx)
	})
	if err != nil {
		if apperr.IsNoRows(err) {
			return TaskRecord{}, r.distinguishMissing(ctx, taskID)
		}
		return TaskRecord{}, err
	}
	return record, nil
}

// distinguishMissing 区分"任务不存在（或已删除）"与"版本冲突"。
func (r *Repository) distinguishMissing(ctx context.Context, taskID string) error {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM tasks WHERE id = $1 AND deleted_at IS NULL)`,
		taskID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("查询任务存在性失败: %w", err)
	}
	if !exists {
		return notFound()
	}
	return domain.ErrVersionConflict
}

// FindMember 查询指定任务成员。
func (r *Repository) FindMember(ctx context.Context, taskID, userID string) (MemberRecord, error) {
	const query = `
		SELECT tm.id, tm.task_id, tm.user_id, tm.role, tm.created_at,
		       u.username, u.display_name, u.email, u.avatar_updated_at
		  FROM task_members tm
		  JOIN users u ON u.id = tm.user_id
		 WHERE tm.task_id = $1 AND tm.user_id = $2`

	var member MemberRecord
	err := r.pool.QueryRow(ctx, query, taskID, userID).Scan(
		&member.ID, &member.TaskID, &member.UserID, &member.Role, &member.CreatedAt,
		&member.Username, &member.DisplayName, &member.Email, &member.AvatarUpdatedAt,
	)
	if err != nil {
		if apperr.IsNoRows(err) {
			return MemberRecord{}, domain.ErrResourceNotFound.
				WithField("user_id", "该用户不是任务成员")
		}
		return MemberRecord{}, fmt.Errorf("查询任务成员失败: %w", err)
	}
	return member, nil
}

// CountMembers 统计任务成员数量。
func (r *Repository) CountMembers(ctx context.Context, filter MemberFilter) (int, error) {
	query := `SELECT count(*) FROM task_members tm WHERE tm.task_id = $1`
	args := []any{filter.TaskID}

	if filter.Role != nil {
		args = append(args, *filter.Role)
		query += fmt.Sprintf(" AND tm.role = $%d", len(args))
	}

	var total int
	if err := r.pool.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("统计任务成员失败: %w", err)
	}
	return total, nil
}

// CountOwners 统计任务持有者数量。
func (r *Repository) CountOwners(ctx context.Context, taskID string) (int, error) {
	const query = `SELECT count(*) FROM task_members WHERE task_id = $1 AND role = 'owner'`

	var total int
	if err := r.pool.QueryRow(ctx, query, taskID).Scan(&total); err != nil {
		return 0, fmt.Errorf("统计任务持有者失败: %w", err)
	}
	return total, nil
}

// ListMembers 分页查询任务成员。
func (r *Repository) ListMembers(ctx context.Context, filter MemberFilter, orderBy string, limit, offset int) ([]MemberRecord, error) {
	query := `
		SELECT tm.id, tm.task_id, tm.user_id, tm.role, tm.created_at,
		       u.username, u.display_name, u.email, u.avatar_updated_at
		  FROM task_members tm
		  JOIN users u ON u.id = tm.user_id
		 WHERE tm.task_id = $1`
	args := []any{filter.TaskID}

	if filter.Role != nil {
		args = append(args, *filter.Role)
		query += fmt.Sprintf(" AND tm.role = $%d", len(args))
	}

	args = append(args, limit, offset)
	query += fmt.Sprintf(" ORDER BY %s LIMIT $%d OFFSET $%d", orderBy, len(args)-1, len(args))

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询任务成员列表失败: %w", err)
	}
	defer rows.Close()

	var records []MemberRecord
	for rows.Next() {
		var member MemberRecord
		if err := rows.Scan(
			&member.ID, &member.TaskID, &member.UserID, &member.Role, &member.CreatedAt,
			&member.Username, &member.DisplayName, &member.Email, &member.AvatarUpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("读取任务成员行失败: %w", err)
		}
		records = append(records, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历任务成员列表失败: %w", err)
	}
	return records, nil
}

// AddMember 添加任务成员。
func (r *Repository) AddMember(
	ctx context.Context, id, taskID, userID, role string, audit *apperr.AuditWrite,
) (MemberRecord, error) {
	const query = `
		INSERT INTO task_members (id, task_id, user_id, role)
		VALUES ($1, $2, $3, $4)
		RETURNING id, task_id, user_id, role, created_at`

	var member MemberRecord
	err := apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		if err := tx.QueryRow(ctx, query, id, taskID, userID, role).Scan(
			&member.ID, &member.TaskID, &member.UserID, &member.Role, &member.CreatedAt,
		); err != nil {
			return apperr.Translate(err, constraintMap)
		}
		return audit.Flush(ctx, tx)
	})
	if err != nil {
		return MemberRecord{}, err
	}
	return member, nil
}

// UpdateMemberRole 修改任务成员角色并回读。
func (r *Repository) UpdateMemberRole(
	ctx context.Context, taskID, userID, role string, audit *apperr.AuditWrite,
) (MemberRecord, error) {
	const query = `
		UPDATE task_members
		   SET role = $3, updated_at = now()
		 WHERE task_id = $1 AND user_id = $2
		RETURNING id, task_id, user_id, role, created_at`

	var member MemberRecord
	err := apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		if err := tx.QueryRow(ctx, query, taskID, userID, role).Scan(
			&member.ID, &member.TaskID, &member.UserID, &member.Role, &member.CreatedAt,
		); err != nil {
			if apperr.IsNoRows(err) {
				return domain.ErrResourceNotFound.
					WithField("user_id", "该用户不是任务成员")
			}
			return apperr.Translate(err, constraintMap)
		}
		return audit.Flush(ctx, tx)
	})
	if err != nil {
		return MemberRecord{}, err
	}
	return member, nil
}

// RemoveMember 删除任务成员关系。
func (r *Repository) RemoveMember(
	ctx context.Context, taskID, userID string, audit *apperr.AuditWrite,
) error {
	const query = `DELETE FROM task_members WHERE task_id = $1 AND user_id = $2`

	return apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		tag, err := tx.Exec(ctx, query, taskID, userID)
		if err != nil {
			return apperr.Translate(err, constraintMap)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrResourceNotFound.WithField("user_id", "该用户不是任务成员")
		}
		return audit.Flush(ctx, tx)
	})
}

// FindProjectMember 报告用户是否为项目成员。
//
// 需求文档 5.4：任务成员必须属于项目成员，持有者也必须是项目成员。
func (r *Repository) FindProjectMember(ctx context.Context, projectID, userID string) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM project_members
			 WHERE project_id = $1 AND user_id = $2
		)`, projectID, userID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("查询项目成员关系失败: %w", err)
	}
	return exists, nil
}

// TransferOwnership 把任务持有者身份转移给目标成员。
//
// 与项目完全同一套做法，理由也相同：持有者唯一，所以"设为持有者"必然是转移；
// 必须在同一事务内**先降现任、再升目标**——
// 部分唯一索引 `task_single_owner` 在语句边界检查，顺序反过来会立刻撞上"第二个 owner"，
// 而先降现任的中间态是"没有 owner"，绝不能提交。
func (r *Repository) TransferOwnership(
	ctx context.Context, taskID, targetUserID string, audit *apperr.AuditWrite,
) (MemberRecord, error) {
	var updated MemberRecord

	err := apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		const demote = `
			UPDATE task_members
			   SET role = 'member', updated_at = now()
			 WHERE task_id = $1 AND role = 'owner' AND user_id <> $2`

		if _, err := tx.Exec(ctx, demote, taskID, targetUserID); err != nil {
			return apperr.Translate(err, constraintMap)
		}

		const promote = `
			UPDATE task_members
			   SET role = 'owner', updated_at = now()
			 WHERE task_id = $1 AND user_id = $2
			RETURNING id, task_id, user_id, role, created_at`

		var member MemberRecord
		err := tx.QueryRow(ctx, promote, taskID, targetUserID).Scan(
			&member.ID, &member.TaskID, &member.UserID, &member.Role, &member.CreatedAt,
		)
		if err != nil {
			if apperr.IsNoRows(err) {
				return domain.ErrResourceNotFound.
					WithField("user_id", "该用户不是任务成员")
			}
			return apperr.Translate(err, constraintMap)
		}
		updated = member
		return audit.Flush(ctx, tx)
	})
	if err != nil {
		return MemberRecord{}, err
	}
	return updated, nil
}

// FindUser 查询用户的最小信息。
func (r *Repository) FindUser(ctx context.Context, userID string) (UserRecord, error) {
	const query = `SELECT id, status FROM users WHERE id = $1`

	var record UserRecord
	if err := r.pool.QueryRow(ctx, query, userID).Scan(&record.ID, &record.Status); err != nil {
		if apperr.IsNoRows(err) {
			return UserRecord{}, domain.New(domain.CodeUserNotFound, "User not found")
		}
		return UserRecord{}, fmt.Errorf("查询用户失败: %w", err)
	}
	return record, nil
}

// UserRecord 是 users 表的最小投影。
type UserRecord struct {
	ID     string
	Status string
}

// IsActive 报告用户是否可用。
func (u UserRecord) IsActive() bool { return u.Status == domain.UserStatusActive }

// FindIdempotencyKey 查询幂等键记录。
//
// 返回 (resultID, found, error)：found 为 false 表示该 key 尚未使用。
func (r *Repository) FindIdempotencyKey(ctx context.Context, userID, scope, key string) (string, string, bool, error) {
	const query = `
		SELECT result_id, COALESCE(result_secondary_id::text, '')
		  FROM idempotency_keys
		 WHERE user_id = $1 AND scope = $2 AND idempotency_key = $3`

	var resultID, secondaryID string
	err := r.pool.QueryRow(ctx, query, userID, scope, key).Scan(&resultID, &secondaryID)
	if err != nil {
		if apperr.IsNoRows(err) {
			return "", "", false, nil
		}
		return "", "", false, fmt.Errorf("查询幂等键失败: %w", err)
	}
	return resultID, secondaryID, true, nil
}

// SaveIdempotencyKey 持久化幂等键。
//
// 唯一约束 UNIQUE(user_id, scope, idempotency_key) 是"不产生重复副作用"的最终保证：
// 并发提交同一 key 时，一个请求成功、另一个得到唯一冲突并被翻译为 409 冲突语义。
func (r *Repository) SaveIdempotencyKey(ctx context.Context, id, userID, scope, key, resultID, secondaryID string) error {
	const query = `
		INSERT INTO idempotency_keys (id, user_id, scope, idempotency_key, result_id, result_secondary_id)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, '')::uuid)`

	if _, err := r.pool.Exec(ctx, query, id, userID, scope, key, resultID, secondaryID); err != nil {
		return apperr.Translate(err, constraintMap)
	}
	return nil
}

// appendTaskFilters 追加任务列表的筛选条件。
func appendTaskFilters(query string, args []any, filter ListFilter) (string, []any) {
	if !filter.IncludeDeleted {
		query += ` AND t.deleted_at IS NULL`
	}
	if filter.Status != nil {
		args = append(args, *filter.Status)
		query += fmt.Sprintf(" AND t.status = $%d", len(args))
	}
	if filter.IsShared != nil {
		args = append(args, *filter.IsShared)
		query += fmt.Sprintf(" AND t.is_shared = $%d", len(args))
	}
	return query, args
}

// constraintMap 把本模块涉及的数据库约束名映射为领域错误。
var constraintMap = map[string]apperr.ConstraintError{
	"task_member_identity": {
		Code: domain.CodeDuplicateMember, Message: "该成员已存在",
		Field: "user_id", Detail: "该用户已是任务成员",
	},
	"task_single_owner": {
		Code: domain.CodeConflict, Message: "任务只能有一个持有者",
		Field: "role", Detail: "任务只能有一个持有者",
	},
	"ck_tasks_task_status": {
		Code: domain.CodeValidationError, Message: "status 取值非法",
		Field: "status", Detail: "任务状态取值非法",
	},
	"ck_tasks_task_progress": {
		Code: domain.CodeValidationError, Message: "progress 取值非法",
		Field: "progress", Detail: "进度必须在 0 到 100 之间",
	},
	"ck_tasks_task_date_range": {
		Code: domain.CodeInvalidDateRange, Message: "结束日期不得早于开始日期",
		Field: "end_date", Detail: "结束日期不得早于开始日期",
	},
	"fk_tasks_project_id_projects": {
		Code: domain.CodeProjectNotFound, Message: "Project not found",
		Field: "project_id", Detail: "所属项目不存在",
	},
	"fk_tasks_parent_id_tasks": {
		Code: domain.CodeTaskNotFound, Message: "Task not found",
		Field: "parent_id", Detail: "父任务不存在",
	},
	"fk_task_members_user_id_users": {
		Code: domain.CodeUserNotFound, Message: "User not found",
		Field: "user_id", Detail: "目标用户不存在",
	},
	"idempotency_key_identity": {
		Code: domain.CodeConflict, Message: "重复的幂等键请求",
		Field: "Idempotency-Key", Detail: "同一幂等键已产生过结果",
	},
}

// notFound 返回统一的"任务不存在或不可见"错误。
func notFound() error { return domain.New(domain.CodeTaskNotFound, "Task not found") }

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
