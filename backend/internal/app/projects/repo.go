// Package projects 实现项目的用例、SQL 访问与 HTTP 端点。
package projects

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
)

// projectColumns 是 projects 表的显式列清单。
const projectColumns = `p.id, p.name, p.description, p.created_by, p.group_id,
	p.status, p.visibility, p.start_date, p.end_date, p.color, p.archived_at,
	p.created_at, p.updated_at, p.progress, p.has_tasks`

// projectReturningColumns 与 projectColumns 相同，但不带表别名。
//
// INSERT/UPDATE 的 RETURNING 子句没有 FROM 别名，写 p.id 会报
// "missing FROM-clause entry for table p"。
const projectReturningColumns = `id, name, description, created_by, group_id,
	status, visibility, start_date, end_date, color, archived_at, created_at, updated_at, progress, has_tasks`

// projectDerivedColumns keeps overdue counts request-timezone dependent.
const projectDerivedColumns = `
 COALESCE((SELECT count(*) FROM tasks t WHERE t.project_id=p.id
 AND t.deleted_at IS NULL AND t.end_date < $1
 AND t.status IN ('not_started','in_progress')),0) AS overdue_task_count`

// ProjectRecord 是 projects 表的行记录（含派生字段与当前用户的项目成员关系）。
type ProjectRecord struct {
	ID          string
	Name        string
	Description *string
	CreatedBy   string
	GroupID     string
	Status      string
	Visibility  string
	StartDate   time.Time
	EndDate     time.Time
	Color       *string
	ArchivedAt  *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time

	Progress         int
	HasTasks         bool
	OverdueTaskCount int

	// Membership 是当前请求用户在本项目的成员关系；nil 表示不是项目成员。
	Membership *MemberRecord
	// ActorSystemRole 是当前请求用户的系统角色（member / admin）。
	//
	// 由 FindProjectForUser 与 ListProjects **两者**都取出（各自的 `JOIN users actor`）。
	// 权限判定需要它才能对系统管理员放行；早期实现各处只传 domain.User{ID: actorID}，
	// 角色恒为空，于是"管理员放行"写了却从未生效——本仓库把这类缺陷
	// （字段被静默留空）视为最危险的一类，见 backend/docs/B05-进度与验证记录.md 5.12。
	ActorSystemRole string
	// GroupMembership 是当前请求用户在项目归属组中的成员关系；nil 表示不是组员。
	// 组内共享判定完全依赖它，因此必须与项目一起取出，避免两次往返产生不一致快照。
	GroupMembership *GroupMemberRecord
	// GroupStatus 是归属组的状态，用于判断组是否可用。
	GroupStatus string
}

// ToDomain 转换为领域实体。
func (r ProjectRecord) ToDomain() domain.Project {
	return domain.Project{
		ID:               r.ID,
		Name:             r.Name,
		Description:      r.Description,
		CreatedBy:        r.CreatedBy,
		GroupID:          r.GroupID,
		Status:           r.Status,
		Visibility:       r.Visibility,
		StartDate:        r.StartDate,
		EndDate:          r.EndDate,
		Color:            r.Color,
		ArchivedAt:       r.ArchivedAt,
		Progress:         r.Progress,
		OverdueTaskCount: r.OverdueTaskCount,
	}
}

// MemberRecord 是 project_members 的一行，并带上用户展示字段。
type MemberRecord struct {
	ID              string
	ProjectID       string
	UserID          string
	Role            string
	CreatedAt       time.Time
	Username        string
	DisplayName     string
	Email           string
	AvatarUpdatedAt *time.Time
}

// ToDomain 转换为领域实体。
func (r MemberRecord) ToDomain() domain.ProjectMember {
	return domain.ProjectMember{
		ID:        r.ID,
		ProjectID: r.ProjectID,
		UserID:    r.UserID,
		Role:      r.Role,
	}
}

// GroupMemberRecord 是 group_members 的领域投影（仅用于权限判定）。
type GroupMemberRecord struct {
	GroupID string
	UserID  string
	Role    string
	Status  string
}

// ToDomain 转换为领域实体。
func (r GroupMemberRecord) ToDomain() domain.GroupMember {
	return domain.GroupMember{
		GroupID: r.GroupID,
		UserID:  r.UserID,
		Role:    r.Role,
		Status:  r.Status,
	}
}

// CreateProjectParams 是创建项目所需的字段。
type CreateProjectParams struct {
	ID          string
	Name        string
	Description *string
	CreatedBy   string
	GroupID     string
	Visibility  string
	StartDate   time.Time
	EndDate     time.Time
	Color       *string
	MemberID    string
	// Audit 是要在同一事务里写下的一条操作记录；nil 表示不记。
	// 由用例层构造（见 apperr.AuditWrite）。
	Audit *apperr.AuditWrite
}

// ProjectUpdateParams 是更新项目的字段。
type ProjectUpdateParams struct {
	Progress        *int
	ID              string
	Name            *string
	Description     *string
	DescriptionSet  bool
	Status          *string
	Visibility      *string
	StartDate       *time.Time
	EndDate         *time.Time
	Color           *string
	ColorSet        bool
	ArchivedAt      *time.Time
	ArchivedAtSet   bool
	ExpectedUpdated time.Time
	// Audit 见 CreateProjectParams 的说明。
	Audit *apperr.AuditWrite
	// ShareAudit 是"共享级别变更"这一条独立记录（契约 4.10 的 shared 动作）。
	// 与 Audit 分开是因为它们是两个事实：项目被改了、以及共享范围被改了；
	// 两条记录由同一个 request_id 串起来。nil 表示这次没有共享变更。
	ShareAudit *apperr.AuditWrite
}

// ListFilter 是项目列表筛选条件。
type ListFilter struct {
	UserID  string
	GroupID *string
	Status  *string
	OwnerID *string
	Name    *string
	// IncludeArchived 控制是否包含归档项目。
	// 需求文档 5.3：归档项目默认不出现在活动列表中，但历史数据可查询。
	IncludeArchived bool
	// Today 用于派生逾期任务数的日期比较基准（UTC 日历日）。
	Today time.Time
}

// MemberFilter 是项目成员列表筛选条件。
type MemberFilter struct {
	ProjectID string
	Role      *string
}

// Repository 是 projects 模块的 SQL 访问层。
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository 构造仓储。
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Pool 暴露连接池，供用例层开启事务。
func (r *Repository) Pool() *pgxpool.Pool { return r.pool }

// visibleCondition 是"当前用户可见的项目"的筛选条件。
//
// 三种可见来源（需求文档 4.2 / 4.3）：
//  1. 项目成员（含持有者）；
//  2. 项目归属组的活跃成员，且项目共享级别为 group_readonly / group_editable；
//  3. 系统管理员的全局查看在用例层通过传入不同的 userID 处理，不在此处开后门。
//
// shared 参数传入时使用 $2 作为当前用户，否则使用 $1；这样同一个条件可以
// 同时服务"带 owner 筛选"（参数更多）与"不带筛选"的查询。
func visibleCondition(userParam int) string {
	user := fmt.Sprintf("$%d", userParam)
	return fmt.Sprintf(`(
		EXISTS (SELECT 1 FROM project_members pm
		         WHERE pm.project_id = p.id AND pm.user_id = %[1]s)
		OR (
			p.visibility IN ('group_readonly', 'group_editable')
			AND EXISTS (SELECT 1 FROM groups g
			             JOIN group_members gm ON gm.group_id = g.id
			            WHERE g.id = p.group_id AND g.status = 'active'
			              AND gm.user_id = %[1]s AND gm.status = 'active')
		)
		-- 系统管理员可以看到全部项目（需求文档 4.1）。
		-- 用与当前行无关的子查询而不是往 FROM 里加 JOIN：所有调用方共用同一个条件，
		-- 不必为了一个常量判断去改各自的连接子句。
		OR EXISTS (SELECT 1 FROM users au
		            WHERE au.id = %[1]s AND au.system_role = 'admin')
	)`, user)
}

// ownerCondition 生成"持有者筛选"条件。
//
// 关键点：与"当前用户可见性"无关，必须单独 EXISTS。
// 早期实现把 owner_id 与当前用户 ID 挤进同一个 JOIN 谓词，
// 导致"按持有者筛选"与"可见性"互相污染；这里用独立子查询彻底分开。
func ownerCondition(paramIndex int) string {
	return fmt.Sprintf(`EXISTS (SELECT 1 FROM project_members pm_owner
		WHERE pm_owner.project_id = p.id AND pm_owner.role = 'owner' AND pm_owner.user_id = $%d)`, paramIndex)
}

// CreateProjectInTx 在一个事务内创建项目与持有者成员关系。
//
// 与工作组一致采用单一写路径：projects.created_by 与
// project_members(owner) 必须同生共死，否则会出现"项目没有持有者"的不可用状态。
func (r *Repository) CreateProjectInTx(ctx context.Context, params CreateProjectParams) (ProjectRecord, error) {
	var created ProjectRecord

	err := apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		const insert = `
			INSERT INTO projects (
				id, name, description, created_by, group_id,
				visibility, start_date, end_date, color
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			RETURNING ` + projectReturningColumns

		if err := tx.QueryRow(ctx, insert,
			params.ID, params.Name, params.Description, params.CreatedBy, params.GroupID,
			params.Visibility, params.StartDate, params.EndDate, params.Color,
		).Scan(
			&created.ID, &created.Name, &created.Description, &created.CreatedBy,
			&created.GroupID, &created.Status, &created.Visibility,
			&created.StartDate, &created.EndDate, &created.Color, &created.ArchivedAt,
			&created.CreatedAt, &created.UpdatedAt, &created.Progress, &created.HasTasks,
		); err != nil {
			return apperr.Translate(err, constraintMap)
		}

		const insertOwner = `
			INSERT INTO project_members (id, project_id, user_id, role)
			VALUES ($1, $2, $3, 'owner')`

		if _, err := tx.Exec(ctx, insertOwner, params.MemberID, params.ID, params.CreatedBy); err != nil {
			return apperr.Translate(err, constraintMap)
		}

		// 审计与业务在同一个事务里：业务回滚则记录也不存在。
		return params.Audit.Flush(ctx, tx)
	})
	if err != nil {
		return ProjectRecord{}, err
	}
	return created, nil
}

// FindProjectForUser 查询项目并附带当前用户的项目成员与组员关系。
func (r *Repository) FindProjectForUser(ctx context.Context, projectID, userID string, today time.Time) (ProjectRecord, error) {
	query := `
		SELECT ` + projectColumns + `,` + projectDerivedColumns + `,
		       pm.id, pm.project_id, pm.user_id, pm.role, pm.created_at,
		       gm.role, gm.status,
		       g.status,
		       COALESCE(actor.system_role, '')
		  FROM projects p
		  JOIN groups g ON g.id = p.group_id
		  LEFT JOIN project_members pm ON pm.project_id = p.id AND pm.user_id = $2
		  LEFT JOIN group_members gm ON gm.group_id = p.group_id AND gm.user_id = $2
		  LEFT JOIN users actor ON actor.id = $2
		 WHERE p.id = $3`

	var record ProjectRecord
	var memberID, memberProjectID, memberUserID, memberRole *string
	var memberCreatedAt *time.Time
	var groupMemberRole, groupMemberStatus *string

	err := r.pool.QueryRow(ctx, query, today, userID, projectID).Scan(
		&record.ID, &record.Name, &record.Description, &record.CreatedBy,
		&record.GroupID, &record.Status, &record.Visibility,
		&record.StartDate, &record.EndDate, &record.Color, &record.ArchivedAt,
		&record.CreatedAt, &record.UpdatedAt, &record.Progress, &record.HasTasks,
		&record.OverdueTaskCount,
		&memberID, &memberProjectID, &memberUserID, &memberRole, &memberCreatedAt,
		&groupMemberRole, &groupMemberStatus,
		&record.GroupStatus,
		&record.ActorSystemRole,
	)
	if err != nil {
		if apperr.IsNoRows(err) {
			return ProjectRecord{}, notFound()
		}
		return ProjectRecord{}, fmt.Errorf("查询项目失败: %w", err)
	}

	if memberID != nil && memberUserID != nil && memberRole != nil {
		record.Membership = &MemberRecord{
			ID:        *memberID,
			ProjectID: derefString(memberProjectID),
			UserID:    *memberUserID,
			Role:      *memberRole,
			CreatedAt: derefTime(memberCreatedAt),
		}
	}
	if groupMemberRole != nil && groupMemberStatus != nil {
		record.GroupMembership = &GroupMemberRecord{
			GroupID: record.GroupID,
			UserID:  userID,
			Role:    *groupMemberRole,
			Status:  *groupMemberStatus,
		}
	}

	return record, nil
}

// CountProjects 统计符合条件的可见项目数量。
func (r *Repository) CountProjects(ctx context.Context, filter ListFilter) (int, error) {
	// 注意：这里只用到一个参数（当前用户），因此不传 Today。
	// 早期版本传了两个参数但 SQL 只引用 $1，PostgreSQL 无法推断 $2 的类型，
	// 直接报 'could not determine data type of parameter $2'（42P18）。
	query := `SELECT count(*) FROM projects p WHERE ` + visibleCondition(1)
	args := []any{filter.UserID}

	if !filter.IncludeArchived {
		query += ` AND p.archived_at IS NULL AND p.status <> 'archived'`
	}
	query, args = appendProjectFilters(query, args, filter)

	var total int
	if err := r.pool.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("统计项目失败: %w", err)
	}
	return total, nil
}

// ListProjects 分页查询可见项目。
//
// 关键保证：
//   - 分页在 SQL 层完成（LIMIT/OFFSET），不把全量结果载入内存后切片；
//   - orderBy 来自 listquery 白名单，列名硬编码。
func (r *Repository) ListProjects(ctx context.Context, filter ListFilter, orderBy string, limit, offset int) ([]ProjectRecord, error) {
	query := `
		SELECT ` + projectColumns + `,` + projectDerivedColumns + `,
		       pm.id, pm.project_id, pm.user_id, pm.role, pm.created_at,
		       COALESCE(actor.system_role, '')
		  FROM projects p
		  LEFT JOIN project_members pm ON pm.project_id = p.id AND pm.user_id = $2
		  LEFT JOIN users actor ON actor.id = $2
		 WHERE ` + visibleCondition(2)
	args := []any{filter.Today, filter.UserID}

	if !filter.IncludeArchived {
		query += ` AND p.archived_at IS NULL AND p.status <> 'archived'`
	}
	query, args = appendProjectFilters(query, args, filter)

	args = append(args, limit, offset)
	query += fmt.Sprintf(" ORDER BY %s LIMIT $%d OFFSET $%d", orderBy, len(args)-1, len(args))

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询项目列表失败: %w", err)
	}
	defer rows.Close()

	var records []ProjectRecord
	for rows.Next() {
		var record ProjectRecord
		var memberID, memberProjectID, memberUserID, memberRole *string
		var memberCreatedAt *time.Time

		if err := rows.Scan(
			&record.ID, &record.Name, &record.Description, &record.CreatedBy,
			&record.GroupID, &record.Status, &record.Visibility,
			&record.StartDate, &record.EndDate, &record.Color, &record.ArchivedAt,
			&record.CreatedAt, &record.UpdatedAt, &record.Progress, &record.HasTasks,
			&record.OverdueTaskCount,
			&memberID, &memberProjectID, &memberUserID, &memberRole, &memberCreatedAt,
			&record.ActorSystemRole,
		); err != nil {
			return nil, fmt.Errorf("读取项目行失败: %w", err)
		}

		if memberID != nil && memberUserID != nil && memberRole != nil {
			record.Membership = &MemberRecord{
				ID:        *memberID,
				ProjectID: derefString(memberProjectID),
				UserID:    *memberUserID,
				Role:      *memberRole,
				CreatedAt: derefTime(memberCreatedAt),
			}
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历项目列表失败: %w", err)
	}
	return records, nil
}

// UpdateProject 以原子 CAS 更新项目并回读最新 updated_at。
//
// CAS 的正确写法（backend/docs/B01 5.2）：版本条件放进 WHERE，而不是先 SELECT 比较再无条件 UPDATE。
//
// 关于"显式提供 vs 未提供"的处理：可空列用"先判断是否提供，再两条语句"而不是
// 单条 CASE WHEN $n。后者需要 13 个参数、且"是否提供"的布尔参数与值参数交错，
// 极易出现位置错配（参数数量对得上、语义却错位），而这类错误不会报错，
// 只会静默地不更新。用两段清晰的分支换取可审查性。
func (r *Repository) UpdateProject(ctx context.Context, params ProjectUpdateParams) (ProjectRecord, error) {
	// SET 子句的静态部分：只更新"提供即覆盖"的列。
	const query = `
		UPDATE projects
		   SET name        = COALESCE($3, name),
		       status      = COALESCE($4, status),
 archived_at = CASE WHEN $4::text IS NOT NULL AND $4 <> 'archived' THEN NULL ELSE archived_at END,
		       visibility  = COALESCE($5, visibility),
		       start_date  = COALESCE($6, start_date),
		       end_date    = COALESCE($7, end_date),
		       description = CASE WHEN $8 THEN $9 ELSE description END,
		       color       = CASE WHEN $10 THEN $11 ELSE color END,
		       progress = COALESCE($12, progress), precise_progress = COALESCE($12::numeric, precise_progress),
		       updated_at  = clock_timestamp()
		 WHERE id = $1 AND updated_at = $2
		RETURNING ` + projectReturningColumns

	var record ProjectRecord
	// 单语句写入也显式开事务：审计必须与业务同事务（《A09》），
	// 而"业务成功但审计缺失"是本次实现里最需要结构性排除的失败模式。
	err := apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		var children bool
		var archived *time.Time
		if err := tx.QueryRow(ctx, `SELECT has_tasks, archived_at FROM projects WHERE id=$1 FOR UPDATE`, params.ID).Scan(&children, &archived); err != nil {
			return err
		}
		if archived != nil && (params.Status == nil || params.Progress != nil) {
			return domain.ArchivedError()
		}
		if children && (params.Progress != nil || (params.Status != nil && archived == nil)) {
			return domain.ErrValidation.WithField("progress", "状态和进度由一级任务汇总，不可手工修改")
		}
		if err := tx.QueryRow(ctx, query,
			params.ID,
			params.ExpectedUpdated,
			params.Name,
			params.Status,
			params.Visibility,
			params.StartDate,
			params.EndDate,
			params.DescriptionSet,
			params.Description,
			params.ColorSet,
			params.Color,
			params.Progress,
		).Scan(
			&record.ID, &record.Name, &record.Description, &record.CreatedBy,
			&record.GroupID, &record.Status, &record.Visibility,
			&record.StartDate, &record.EndDate, &record.Color, &record.ArchivedAt,
			&record.CreatedAt, &record.UpdatedAt, &record.Progress, &record.HasTasks,
		); err != nil {
			// 不在这里做存在性查询：那会在事务开着的时候再借一条连接。
			// 把"没有匹配行"原样上抛，由事务之外的 distinguishMissing 区分 404 与 409。
			return apperr.Translate(err, constraintMap)
		}
		if children && params.Status != nil && archived != nil {
			if _, err := tx.Exec(ctx, `SELECT refresh_project_rollup($1)`, params.ID); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT status, progress, archived_at, updated_at FROM projects WHERE id=$1`, params.ID).Scan(&record.Status, &record.Progress, &record.ArchivedAt, &record.UpdatedAt); err != nil {
				return err
			}
		}
		if err := params.Audit.Flush(ctx, tx); err != nil {
			return err
		}
		return params.ShareAudit.Flush(ctx, tx)
	})
	if err != nil {
		if apperr.IsNoRows(err) {
			return ProjectRecord{}, r.distinguishMissing(ctx, params.ID)
		}
		return ProjectRecord{}, err
	}
	return record, nil
}

// distinguishMissing 区分"项目不存在"与"版本冲突"。
//
// 与其他域一致：先做纯存在性查询，再决定 404 还是 409。
// 全项目只使用一处存在性查询，避免"各写一份、有的传空 UUID 导致 22P02"。
func (r *Repository) distinguishMissing(ctx context.Context, projectID string) error {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM projects WHERE id = $1)`, projectID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("查询项目存在性失败: %w", err)
	}
	if !exists {
		return notFound()
	}
	return domain.ErrVersionConflict
}

// ArchiveProject 归档项目（CAS）。
//
// 归档同时写入 archived_at 并把 status 置为 archived：两者任一都是
// "项目已归档"的判据（见 domain.Project.IsArchived），保持一致可避免歧义。
func (r *Repository) ArchiveProject(
	ctx context.Context, projectID string, expectedUpdated time.Time,
	archivedAt time.Time, audit *apperr.AuditWrite,
) (ProjectRecord, error) {
	const query = `
		UPDATE projects
		   SET status = 'archived', archived_at = $3, updated_at = now()
		 WHERE id = $1 AND updated_at = $2
		RETURNING ` + projectReturningColumns

	var record ProjectRecord
	err := apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		if err := tx.QueryRow(ctx, query, projectID, expectedUpdated, archivedAt).Scan(
			&record.ID, &record.Name, &record.Description, &record.CreatedBy,
			&record.GroupID, &record.Status, &record.Visibility,
			&record.StartDate, &record.EndDate, &record.Color, &record.ArchivedAt,
			&record.CreatedAt, &record.UpdatedAt, &record.Progress, &record.HasTasks,
		); err != nil {
			return apperr.Translate(err, constraintMap)
		}
		return audit.Flush(ctx, tx)
	})
	if err != nil {
		if apperr.IsNoRows(err) {
			return ProjectRecord{}, r.distinguishMissing(ctx, projectID)
		}
		return ProjectRecord{}, err
	}
	return record, nil
}

// FindMember 查询指定项目成员。
func (r *Repository) FindMember(ctx context.Context, projectID, userID string) (MemberRecord, error) {
	const query = `
		SELECT pm.id, pm.project_id, pm.user_id, pm.role, pm.created_at,
		       u.username, u.display_name, u.email, u.avatar_updated_at
		  FROM project_members pm
		  JOIN users u ON u.id = pm.user_id
		 WHERE pm.project_id = $1 AND pm.user_id = $2`

	var member MemberRecord
	err := r.pool.QueryRow(ctx, query, projectID, userID).Scan(
		&member.ID, &member.ProjectID, &member.UserID, &member.Role, &member.CreatedAt,
		&member.Username, &member.DisplayName, &member.Email, &member.AvatarUpdatedAt,
	)
	if err != nil {
		if apperr.IsNoRows(err) {
			return MemberRecord{}, domain.ErrResourceNotFound.
				WithField("user_id", "该用户不是项目成员")
		}
		return MemberRecord{}, fmt.Errorf("查询项目成员失败: %w", err)
	}
	return member, nil
}

// CountMembers 统计项目成员数量。
func (r *Repository) CountMembers(ctx context.Context, filter MemberFilter) (int, error) {
	query := `SELECT count(*) FROM project_members pm WHERE pm.project_id = $1`
	args := []any{filter.ProjectID}

	if filter.Role != nil {
		args = append(args, *filter.Role)
		query += fmt.Sprintf(" AND pm.role = $%d", len(args))
	}

	var total int
	if err := r.pool.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("统计项目成员失败: %w", err)
	}
	return total, nil
}

// CountOwners 统计项目的持有者数量。
//
// 用途：判断"移除/降级"是否会破坏"至少一个持有者"。
func (r *Repository) CountOwners(ctx context.Context, projectID string) (int, error) {
	const query = `SELECT count(*) FROM project_members WHERE project_id = $1 AND role = 'owner'`

	var total int
	if err := r.pool.QueryRow(ctx, query, projectID).Scan(&total); err != nil {
		return 0, fmt.Errorf("统计项目持有者失败: %w", err)
	}
	return total, nil
}

// ListMembers 分页查询项目成员。
func (r *Repository) ListMembers(ctx context.Context, filter MemberFilter, orderBy string, limit, offset int) ([]MemberRecord, error) {
	query := `
		SELECT pm.id, pm.project_id, pm.user_id, pm.role, pm.created_at,
		       u.username, u.display_name, u.email, u.avatar_updated_at
		  FROM project_members pm
		  JOIN users u ON u.id = pm.user_id
		 WHERE pm.project_id = $1`
	args := []any{filter.ProjectID}

	if filter.Role != nil {
		args = append(args, *filter.Role)
		query += fmt.Sprintf(" AND pm.role = $%d", len(args))
	}

	args = append(args, limit, offset)
	query += fmt.Sprintf(" ORDER BY %s LIMIT $%d OFFSET $%d", orderBy, len(args)-1, len(args))

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询项目成员列表失败: %w", err)
	}
	defer rows.Close()

	var records []MemberRecord
	for rows.Next() {
		var member MemberRecord
		if err := rows.Scan(
			&member.ID, &member.ProjectID, &member.UserID, &member.Role, &member.CreatedAt,
			&member.Username, &member.DisplayName, &member.Email, &member.AvatarUpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("读取项目成员行失败: %w", err)
		}
		records = append(records, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历项目成员列表失败: %w", err)
	}
	return records, nil
}

// AddMember 添加项目成员。
//
// 成员可以来自项目归属组以外的其他工作组（需求文档 5.3）；
// 数据库层对此没有组归属复合外键，只有一个指向 users 的外键。
func (r *Repository) AddMember(
	ctx context.Context, id, projectID, userID, role string, audit *apperr.AuditWrite,
) (MemberRecord, error) {
	const query = `
		INSERT INTO project_members (id, project_id, user_id, role)
		VALUES ($1, $2, $3, $4)
		RETURNING id, project_id, user_id, role, created_at`

	var member MemberRecord
	err := apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		if err := tx.QueryRow(ctx, query, id, projectID, userID, role).Scan(
			&member.ID, &member.ProjectID, &member.UserID, &member.Role, &member.CreatedAt,
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

// TransferOwnership 把项目持有者身份转移给目标成员。
//
// 必须在**同一个事务**内完成"先降现任、再升目标"：
//
//   - 部分唯一索引 `project_single_owner` 在**语句边界**检查，因此顺序不能反——
//     先升目标会立刻撞上"第二个 owner"，先降现任则中间态是"没有 owner"，
//     这个中间态绝不能提交（否则项目会处于无人可管理的状态）。
//   - 降级条件带 `user_id <> $2`：目标本身就是现任持有者时不该自我降级
//     （服务层在此之前已用 domain 规则拒绝这种情况，这里只是让 SQL 自身也成立）。
//
// 两条语句合起来保证不变式"任何时刻至多一个 owner"，事务保证"至少一个"。
func (r *Repository) TransferOwnership(
	ctx context.Context, projectID, targetUserID string, audit *apperr.AuditWrite,
) (MemberRecord, error) {
	var updated MemberRecord

	err := apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		const demote = `
			UPDATE project_members
			   SET role = 'member', updated_at = now()
			 WHERE project_id = $1 AND role = 'owner' AND user_id <> $2`

		if _, err := tx.Exec(ctx, demote, projectID, targetUserID); err != nil {
			return apperr.Translate(err, constraintMap)
		}

		const promote = `
			UPDATE project_members
			   SET role = 'owner', updated_at = now()
			 WHERE project_id = $1 AND user_id = $2
			RETURNING id, project_id, user_id, role, created_at`

		member, err := scanMember(tx.QueryRow(ctx, promote, projectID, targetUserID))
		if err != nil {
			if apperr.IsNoRows(err) {
				return domain.ErrResourceNotFound.
					WithField("user_id", "该用户不是项目成员")
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

// scanMember 读取一行项目成员。
func scanMember(row pgx.Row) (MemberRecord, error) {
	var member MemberRecord
	err := row.Scan(&member.ID, &member.ProjectID, &member.UserID, &member.Role, &member.CreatedAt)
	if err != nil {
		return MemberRecord{}, err
	}
	return member, nil
}

// UpdateMemberRole 修改项目成员角色并回读。
func (r *Repository) UpdateMemberRole(
	ctx context.Context, projectID, userID, role string, audit *apperr.AuditWrite,
) (MemberRecord, error) {
	const query = `
		UPDATE project_members
		   SET role = $3, updated_at = now()
		 WHERE project_id = $1 AND user_id = $2
		RETURNING id, project_id, user_id, role, created_at`

	var member MemberRecord
	err := apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		if err := tx.QueryRow(ctx, query, projectID, userID, role).Scan(
			&member.ID, &member.ProjectID, &member.UserID, &member.Role, &member.CreatedAt,
		); err != nil {
			return apperr.Translate(err, constraintMap)
		}
		return audit.Flush(ctx, tx)
	})
	if err != nil {
		if apperr.IsNoRows(err) {
			return MemberRecord{}, domain.ErrResourceNotFound.
				WithField("user_id", "该用户不是项目成员")
		}
		return MemberRecord{}, err
	}
	return member, nil
}

// RemoveMember 物理删除项目成员关系。
//
// 与工作组不同：项目成员关系没有 status 列，"移除"就是删除该行。
// 删除唯一持有者被 domain.ValidateProjectMemberChange 阻止（应用层），
// 这里不做判断以保持仓储只负责持久化。
func (r *Repository) RemoveMember(
	ctx context.Context, projectID, userID string, audit *apperr.AuditWrite,
) error {
	const query = `DELETE FROM project_members WHERE project_id = $1 AND user_id = $2`

	return apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		tag, err := tx.Exec(ctx, query, projectID, userID)
		if err != nil {
			return apperr.Translate(err, constraintMap)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrResourceNotFound.WithField("user_id", "该用户不是项目成员")
		}
		return audit.Flush(ctx, tx)
	})
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

// FindGroupForUser 查询工作组与当前用户的组员关系，用于创建项目时的归属校验。
//
// 需求文档 5.3：创建项目时必须选择一个归属组，且创建者应是该组的活跃成员。
func (r *Repository) FindGroupForUser(ctx context.Context, groupID, userID string) (GroupRecord, error) {
	const query = `
		SELECT g.id, g.status, g.owner_id, gm.role, gm.status
		  FROM groups g
		  LEFT JOIN group_members gm ON gm.group_id = g.id AND gm.user_id = $2
		 WHERE g.id = $1`

	var record GroupRecord
	var role, status *string
	err := r.pool.QueryRow(ctx, query, groupID, userID).Scan(
		&record.ID, &record.Status, &record.OwnerID, &role, &status,
	)
	if err != nil {
		if apperr.IsNoRows(err) {
			return GroupRecord{}, domain.New(domain.CodeGroupNotFound, "Group not found")
		}
		return GroupRecord{}, fmt.Errorf("查询工作组失败: %w", err)
	}
	if role != nil && status != nil {
		record.Membership = &GroupMemberRecord{
			GroupID: record.ID, UserID: userID, Role: *role, Status: *status,
		}
	}
	return record, nil
}

// GroupRecord 是 groups 表在权限判定中所需的最小投影。
type GroupRecord struct {
	ID         string
	Status     string
	OwnerID    string
	Membership *GroupMemberRecord
}

// ToDomain 转换为领域实体。
func (r GroupRecord) ToDomain() domain.Group {
	return domain.Group{ID: r.ID, Status: r.Status, OwnerID: r.OwnerID}
}

// UserRecord 是 users 表的最小投影。
type UserRecord struct {
	ID     string
	Status string
}

// IsActive 报告用户是否可用。
func (u UserRecord) IsActive() bool { return u.Status == domain.UserStatusActive }

// appendProjectFilters 追加项目列表的筛选条件。
//
// 每个筛选条件都用独立 EXISTS 子查询，绝不共用同一个 JOIN：
// 共用 JOIN 会让多个条件落在同一行上，产生"单条件有结果、组合条件恒为空"的缺陷
// （早期实现的实测缺陷，见 backend/docs/B01 5.3）。
func appendProjectFilters(query string, args []any, filter ListFilter) (string, []any) {
	if filter.GroupID != nil {
		args = append(args, *filter.GroupID)
		query += fmt.Sprintf(" AND p.group_id = $%d", len(args))
	}
	if filter.Status != nil {
		args = append(args, *filter.Status)
		query += fmt.Sprintf(" AND p.status = $%d", len(args))
	}
	if filter.OwnerID != nil {
		args = append(args, *filter.OwnerID)
		query += " AND " + ownerCondition(len(args))
	}
	if filter.Name != nil {
		args = append(args, "%"+escapeLike(*filter.Name)+"%")
		query += fmt.Sprintf(" AND p.name ILIKE $%d", len(args))
	}
	return query, args
}

// escapeLike 转义 LIKE 模式中的通配符，使名称搜索按字面量匹配。
//
// 未转义时用户输入 "%" 会匹配全部记录，既不符合预期也构成一次轻量的信息放大。
// 反斜杠必须先转义，否则会把后面刚插入的转义符再转义一遍。
func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, "%", `\%`)
	value = strings.ReplaceAll(value, "_", `\_`)
	return value
}

// constraintMap 把本模块涉及的数据库约束名映射为领域错误。
var constraintMap = map[string]apperr.ConstraintError{
	"project_member_identity": {
		Code: domain.CodeDuplicateMember, Message: "该成员已存在",
		Field: "user_id", Detail: "该用户已是项目成员",
	},
	"project_single_owner": {
		Code: domain.CodeConflict, Message: "项目只能有一个持有者",
		Field: "role", Detail: "项目只能有一个持有者",
	},
	"ck_projects_project_status": {
		Code: domain.CodeValidationError, Message: "status 取值非法",
		Field: "status", Detail: "项目状态取值非法",
	},
	"ck_projects_project_visibility": {
		Code: domain.CodeValidationError, Message: "visibility 取值非法",
		Field: "visibility", Detail: "共享级别取值非法",
	},
	"ck_projects_project_date_range": {
		Code: domain.CodeInvalidDateRange, Message: "结束日期不得早于开始日期",
		Field: "end_date", Detail: "结束日期不得早于开始日期",
	},
	"fk_projects_group_id_groups": {
		Code: domain.CodeGroupNotFound, Message: "Group not found",
		Field: "group_id", Detail: "归属工作组不存在",
	},
	"fk_projects_created_by_users": {
		Code: domain.CodeUserNotFound, Message: "User not found",
		Field: "created_by", Detail: "创建者用户不存在",
	},
	"fk_project_members_user_id_users": {
		Code: domain.CodeUserNotFound, Message: "User not found",
		Field: "user_id", Detail: "目标用户不存在",
	},
}

// notFound 返回统一的"项目不存在或不可见"错误。
func notFound() error { return domain.New(domain.CodeProjectNotFound, "Project not found") }

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func derefTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}
