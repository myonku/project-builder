// Package planviews 实现计划视图（接口契约 4.7）：个人计划、组内共享计划、项目甘特图。
//
// 设计立场（这一层必须理解，否则会误以为它与 projects/tasks 重复）：
//
//  1. 本模块**只读**：没有事务、没有 CAS、没有写路径，因此也不持有 idgen 与版本校验。
//  2. 权限判定**不在 SQL 里**。SQL 只负责两件事：
//     a) 视图范围（"我的项目" / "本组已共享的项目与任务"），这是视图定义，不是权限规则；
//     b) 机械筛选（状态、优先级、日期区间、成员/持有者、归档与逻辑删除）。
//     逐对象的可见性与可编辑性一律调用 domain 的同一个函数
//     （domain.ProjectPermission / domain.TaskPermission），与 projects、tasks 模块
//     使用的是同一份规则。若在这里再写一套 SQL 可见性谓词，就等于把权限规则复制成两份，
//     而"谁能看到什么"出现两份实现时，迟早会分叉。
//  3. 没有分页。计划视图是一个整体视图（甘特图必须一次拿到全部条目），
//     因此不存在"total 与条目数不符"的空间——统计与列表都由**同一个切片**算出。
//
// 派生字段（progress / overdue_task_count / is_overdue / can_edit）全部通过 domain 的
// 既有函数计算，不在这里重写一遍平均进度或逾期判断。
package planviews

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
)

// projectColumns 是 projects 表在计划视图中的列清单。
const projectColumns = `p.id, p.name, p.description, p.created_by, p.group_id,
	p.status, p.visibility, p.start_date, p.end_date, p.color, p.archived_at,
	p.created_at, p.updated_at`

// projectFactColumns 是权限判定所需的"原始事实"。
//
// 这些列**不是**权限判定本身：它们只是把 domain.ProjectPermission 需要的输入
// （我在项目中的成员关系、组是否可用、我在组中的成员关系、项目持有者）取出来。
// 判定仍然发生在领域层。
const projectFactColumns = `
	pm.role AS actor_role,
	g.status AS group_status,
	gm.role AS group_role,
	gm.status AS group_member_status,
	owner.user_id AS owner_id,
	COALESCE(actor.system_role, '') AS actor_system_role`

// ProjectRecord 是项目在计划视图中的投影。
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

	// ActorRole 是当前用户在该项目中的成员角色；空串表示不是项目成员。
	ActorRole string
	// GroupStatus 是项目归属组的状态；组不存在时为 NULL。
	GroupStatus *string
	// GroupRole / GroupMemberStatus 是当前用户在该组中的成员关系。
	GroupRole         *string
	GroupMemberStatus *string
	// OwnerID 是项目持有者；数据库的部分唯一索引保证最多一个。
	OwnerID *string
	// ActorSystemRole 是当前请求用户的系统角色（member / admin）。
	//
	// 与 projects / groups 模块同样的处理：权限判定需要它才能对系统管理员放行，
	// 而用例层只拿得到 actorID。由查询一并取出，避免又一次"角色被静默留空"
	// ——那会让管理员放行写了却从未生效（docs/06 5.12）。
	ActorSystemRole string
}

// ToDomain 转换为领域实体，供 domain 的权限函数使用。
func (r ProjectRecord) ToDomain() domain.Project {
	return domain.Project{
		ID:         r.ID,
		Name:       r.Name,
		GroupID:    r.GroupID,
		Status:     r.Status,
		Visibility: r.Visibility,
		StartDate:  r.StartDate,
		EndDate:    r.EndDate,
		ArchivedAt: r.ArchivedAt,
	}
}

// projectMember 返回当前用户在该项目中的成员关系；不是成员时返回 nil。
//
// 刻意在这里构造出"成员关系"而不是到处传字符串角色：domain.ProjectPermission
// 的参数形状就是这样，转换放在一处，避免各调用点各写一遍。
func (r ProjectRecord) projectMember(actorID string) *domain.ProjectMember {
	if strings.TrimSpace(r.ActorRole) == "" {
		return nil
	}
	return &domain.ProjectMember{ProjectID: r.ID, UserID: actorID, Role: r.ActorRole}
}

// group 返回项目归属组。
func (r ProjectRecord) group() domain.Group {
	status := ""
	if r.GroupStatus != nil {
		status = *r.GroupStatus
	}
	return domain.Group{ID: r.GroupID, Status: status}
}

// groupMembership 返回当前用户在该项目归属组中的成员关系。
func (r ProjectRecord) groupMembership(actorID string) *domain.GroupMember {
	if r.GroupRole == nil || r.GroupMemberStatus == nil {
		return nil
	}
	return &domain.GroupMember{
		GroupID: r.GroupID, UserID: actorID,
		Role: *r.GroupRole, Status: *r.GroupMemberStatus,
	}
}

// MemberRecord 是任务成员（带展示字段）。
type MemberRecord struct {
	UserID      string `json:"user_id"`
	Role        string `json:"role"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

// TaskRecord 是任务在计划视图中的投影。
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
	// UpdatedBy 是最后修改人；UpdatedByName 是它的显示名（列表里一次性 JOIN 出来，
	// 避免前端为了显示一行名字再发 N 个请求）。
	UpdatedBy     string
	UpdatedByName *string

	// Members 是任务的全部成员（含持有者），按"持有者优先、显示名"排序。
	Members []MemberRecord
}

// ToDomain 转换为领域实体，供 domain.TaskPermission 使用。
func (r TaskRecord) ToDomain() domain.Task {
	return domain.Task{
		ID:          r.ID,
		ProjectID:   r.ProjectID,
		ParentID:    r.ParentID,
		CreatedBy:   r.CreatedBy,
		Name:        r.Name,
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

// Owner 返回任务持有者；没有持有者时返回 nil。
func (r TaskRecord) Owner() *MemberRecord {
	for index := range r.Members {
		if r.Members[index].Role == domain.MemberRoleOwner {
			return &r.Members[index]
		}
	}
	return nil
}

// Scope 描述视图范围。它是**视图定义**，不是权限判定。
type Scope struct {
	// ActorID 决定"我的项目/我的任务"。
	ActorID string
	// GroupID 非空时把范围限定为该工作组（组内计划视图）。
	GroupID *string
}

// ProjectFilter 是项目侧的机械筛选。
type ProjectFilter struct {
	ProjectID *string
	Status    *string
	OwnerID   *string
	// IncludeArchived 为真时把归档项目一并纳入。
	// 默认排除：需求文档 5.3 要求归档项目默认不出现在活动列表中。
	IncludeArchived bool
}

// TaskFilter 是任务侧的机械筛选。
//
// 每个成员条件都会生成**独立的 EXISTS 子查询**，绝不与彼此或与当前用户的成员关系
// 共用 JOIN：共用 JOIN 正是"组合筛选恒为空"的历史缺陷（docs/01 5.3）。
type TaskFilter struct {
	ProjectID *string
	Status    *string
	Priority  *int
	// OwnerID 筛选任务持有者；MemberID 筛选任务成员（含持有者）。
	OwnerID  *string
	MemberID *string
	// DueFrom / DueTo 按任务**结束日期**筛选。
	DueFrom *time.Time
	DueTo   *time.Time
	// Overdue 为真时只返回逾期任务（end_date < today 且状态未结束）。
	Overdue *bool
	// Today 是"今天"，按请求者时区计算后由用例层传入；
	// 只有 Overdue 为真时才会用到它。
	Today time.Time
}

// Repository 是计划视图的只读 SQL 访问层。
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository 构造仓储。
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// membershipJoin 生成"当前用户的项目成员关系"的 JOIN。
//
// 两种范围对 JOIN 类型的要求**不同**，这是本模块最容易搞错的一处：
//
//   - 个人视图（scope.GroupID == nil）的范围就是"我是项目成员的项目"，
//     因此必须是 INNER JOIN：非成员的项目根本不属于这个视图。
//   - 组内视图的范围是"本组已共享的项目"，访客可能**只是组员而不是项目成员**，
//     因此必须是 LEFT JOIN。写成 INNER JOIN 会让组内视图对普通组员恒为空——
//     实测踩到过：界面看起来正常，只是什么都没有。
func membershipJoin(scope Scope) string {
	join := "JOIN"
	if scope.GroupID != nil {
		join = "LEFT JOIN"
	}
	return join + ` project_members pm ON pm.project_id = p.id AND pm.user_id = $1`
}

// Projects 查询视图范围内的项目。
func (r *Repository) Projects(
	ctx context.Context, scope Scope, filter Filter,
) ([]ProjectRecord, error) {
	query := `
		SELECT ` + projectColumns + `,` + projectFactColumns + `
		  FROM projects p
		  ` + membershipJoin(scope) + `
		  LEFT JOIN groups g ON g.id = p.group_id
		  LEFT JOIN group_members gm ON gm.group_id = p.group_id AND gm.user_id = $1
		  LEFT JOIN project_members owner
		         ON owner.project_id = p.id AND owner.role = 'owner'
		  LEFT JOIN users actor ON actor.id = $1
		 WHERE 1 = 1`
	args := []any{scope.ActorID}

	query, args = appendProjectScope(query, args, scope)
	query, args = appendProjectFilters(query, args, filter.Project)
	query += ` ORDER BY p.end_date, p.id`

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询计划视图项目失败: %w", err)
	}
	defer rows.Close()

	var records []ProjectRecord
	for rows.Next() {
		record, err := scanProject(rows)
		if err != nil {
			return nil, fmt.Errorf("读取计划视图项目失败: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历计划视图项目失败: %w", err)
	}
	return records, nil
}

// appendProjectScope 追加视图范围条件。
//
// 组内视图要求项目**已共享**（visibility 为组内只读或可编辑）：
// 需求文档 4.3 明确"项目共享决定项目是否进入组内页面"。
// 未共享的私有项目即使属于该组也不出现。
func appendProjectScope(query string, args []any, scope Scope) (string, []any) {
	if scope.GroupID != nil {
		args = append(args, *scope.GroupID)
		query += fmt.Sprintf(` AND p.group_id = $%d
			AND p.visibility IN ('group_readonly', 'group_editable')`, len(args))
	}
	return query, args
}

// appendProjectFilters 追加项目侧机械筛选。计数与列表共用同一实现（本模块其实没有
// 独立计数查询——统计由同一个切片算出，因此不存在二者不一致的空间）。
func appendProjectFilters(query string, args []any, filter ProjectFilter) (string, []any) {
	if !filter.IncludeArchived {
		// 归档判定与 domain.Project.IsArchived 保持一致：状态或归档时间任一命中。
		query += ` AND p.status <> 'archived' AND p.archived_at IS NULL`
	}
	if filter.ProjectID != nil {
		args = append(args, *filter.ProjectID)
		query += fmt.Sprintf(` AND p.id = $%d`, len(args))
	}
	if filter.Status != nil {
		args = append(args, *filter.Status)
		query += fmt.Sprintf(` AND p.status = $%d`, len(args))
	}
	if filter.OwnerID != nil {
		args = append(args, *filter.OwnerID)
		query += fmt.Sprintf(` AND EXISTS (
			SELECT 1 FROM project_members po
			 WHERE po.project_id = p.id AND po.role = 'owner' AND po.user_id = $%d)`, len(args))
	}
	return query, args
}

// Tasks 查询视图范围内的任务。
//
// 组内视图只返回**已共享任务**：需求文档 4.3 明确"项目已共享但任务未共享时，
// 任务不得出现在组内视图"。个人视图不做这个限制（那是我的项目全貌）。
//
// 项目侧条件与项目列表使用**同一个 filter.Project**：否则会出现
// "项目列表里有这个归档项目、它的任务却不见踪影"的不一致。
func (r *Repository) Tasks(ctx context.Context, scope Scope, filter Filter) ([]TaskRecord, error) {
	query := `
		SELECT ` + taskViewColumns + `
		  FROM tasks t
		  JOIN projects p ON p.id = t.project_id
		  ` + membershipJoin(scope) + `
		  LEFT JOIN groups g ON g.id = p.group_id
		  LEFT JOIN group_members gm ON gm.group_id = p.group_id AND gm.user_id = $1
		  LEFT JOIN project_members owner
		         ON owner.project_id = p.id AND owner.role = 'owner'
		  LEFT JOIN users ub ON ub.id = t.updated_by
		  LEFT JOIN LATERAL (` + taskMembersLateral + `) members ON TRUE
		 WHERE t.deleted_at IS NULL`
	args := []any{scope.ActorID}

	if scope.GroupID != nil {
		args = append(args, *scope.GroupID)
		query += fmt.Sprintf(` AND p.group_id = $%d
			AND p.visibility IN ('group_readonly', 'group_editable')
			AND t.is_shared`, len(args))
	}

	query, args = appendProjectFilters(query, args, filter.Project)
	query, args = appendTaskFilters(query, args, filter.Task)
	query += ` ORDER BY t.start_date, t.end_date, t.id`

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询计划视图任务失败: %w", err)
	}
	defer rows.Close()
	return scanTasks(rows)
}

// ProjectTasks 查询单个项目的任务（甘特图）。
//
// 不做范围筛选：调用方已经通过 domain.ProjectPermission 确认了对该项目的权限，
// 逐任务的可见性随后由 domain.TaskPermission 收敛（非项目成员的组内访客
// 只能看到已共享任务）。
func (r *Repository) ProjectTasks(
	ctx context.Context, projectID string, filter TaskFilter,
) ([]TaskRecord, error) {
	query := `
		SELECT ` + taskViewColumns + `
		  FROM tasks t
		  JOIN projects p ON p.id = t.project_id
		  LEFT JOIN users ub ON ub.id = t.updated_by
		  LEFT JOIN LATERAL (` + taskMembersLateral + `) members ON TRUE
		 WHERE t.project_id = $1 AND t.deleted_at IS NULL`
	args := []any{projectID}

	query, args = appendTaskFilters(query, args, filter)
	query += ` ORDER BY t.start_date, t.end_date, t.id`

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询甘特图任务失败: %w", err)
	}
	defer rows.Close()
	return scanTasks(rows)
}

// FindProject 查询单个项目及其权限判定所需的原始事实。
func (r *Repository) FindProject(
	ctx context.Context, projectID, actorID string,
) (ProjectRecord, error) {
	query := `
		SELECT ` + projectColumns + `,` + projectFactColumns + `
		  FROM projects p
		  LEFT JOIN project_members pm ON pm.project_id = p.id AND pm.user_id = $2
		  LEFT JOIN groups g ON g.id = p.group_id
		  LEFT JOIN group_members gm ON gm.group_id = p.group_id AND gm.user_id = $2
		  LEFT JOIN project_members owner
		         ON owner.project_id = p.id AND owner.role = 'owner'
		  LEFT JOIN users actor ON actor.id = $2
		 WHERE p.id = $1`

	record, err := scanProject(r.pool.QueryRow(ctx, query, projectID, actorID))
	if err != nil {
		return ProjectRecord{}, translateNotFound(err, domain.CodeProjectNotFound, "Project not found")
	}
	return record, nil
}

// CanViewGroupPlan 报告用户是否可以查看该工作组的共享计划。
//
// 除活跃成员外，**系统管理员也放行**（需求文档 4.1）：管理员需要能查看任意组的
// 共享计划来定位与处理异常，否则"管理员兜底"在只读路径上同样是空话。
// 因此这个判断的语义是"可查看"，而不再字面上等于"是成员"。
func (r *Repository) IsActiveGroupMember(ctx context.Context, groupID, userID string) (bool, error) {
	const query = `
		SELECT
			EXISTS (
				SELECT 1 FROM groups g
				  JOIN group_members gm ON gm.group_id = g.id
				 WHERE g.id = $1 AND g.status = 'active'
				   AND gm.user_id = $2 AND gm.status = 'active'
			)
			OR EXISTS (
				SELECT 1 FROM users au
				 WHERE au.id = $2 AND au.system_role = 'admin'
			)`

	var allowed bool
	if err := r.pool.QueryRow(ctx, query, groupID, userID).Scan(&allowed); err != nil {
		return false, fmt.Errorf("查询工作组成员关系失败: %w", err)
	}
	return allowed, nil
}

// 任务投影的列清单与成员聚合。
//
// 成员用 jsonb 聚合而不是第二个数组查询：一次查询返回完整记录，
// 既避免 `= ANY(uuid[])` 的参数编码问题，也避免"两次查询之间数据变化"的不一致。
const taskViewColumns = `t.id, t.project_id, t.parent_id, t.created_by, t.name, t.description,
	t.status, t.progress, t.priority, t.start_date, t.end_date,
	t.is_milestone, t.is_shared, t.deleted_at, t.created_at, t.updated_at,
	t.updated_by, ub.display_name, COALESCE(members.items, '[]'::jsonb)`

const taskMembersLateral = `
	SELECT jsonb_agg(jsonb_build_object(
	           'user_id', tm.user_id,
	           'role', tm.role,
	           'username', mu.username,
	           'display_name', mu.display_name)
	       ORDER BY (tm.role = 'owner') DESC, mu.display_name) AS items
	  FROM task_members tm
	  JOIN users mu ON mu.id = tm.user_id
	 WHERE tm.task_id = t.id`

// scanTasks 读取任务行并解析成员聚合。
func scanTasks(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}) ([]TaskRecord, error) {
	var records []TaskRecord
	for rows.Next() {
		record, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("读取计划视图任务失败: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历计划视图任务失败: %w", err)
	}
	return records, nil
}

// scanTask 读取一行任务。
func scanTask(row interface{ Scan(dest ...any) error }) (TaskRecord, error) {
	var record TaskRecord
	var membersRaw []byte

	err := row.Scan(
		&record.ID, &record.ProjectID, &record.ParentID, &record.CreatedBy,
		&record.Name, &record.Description, &record.Status, &record.Progress,
		&record.Priority, &record.StartDate, &record.EndDate,
		&record.IsMilestone, &record.IsShared, &record.DeletedAt,
		&record.CreatedAt, &record.UpdatedAt,
		&record.UpdatedBy, &record.UpdatedByName, &membersRaw,
	)
	if err != nil {
		return TaskRecord{}, err
	}

	if len(membersRaw) > 0 {
		if err := json.Unmarshal(membersRaw, &record.Members); err != nil {
			return TaskRecord{}, fmt.Errorf("解析任务成员失败: %w", err)
		}
	}
	return record, nil
}

// scanProject 读取一行项目。
func scanProject(row interface{ Scan(dest ...any) error }) (ProjectRecord, error) {
	var record ProjectRecord
	var actorRole *string

	err := row.Scan(
		&record.ID, &record.Name, &record.Description, &record.CreatedBy, &record.GroupID,
		&record.Status, &record.Visibility, &record.StartDate, &record.EndDate,
		&record.Color, &record.ArchivedAt, &record.CreatedAt, &record.UpdatedAt,
		&actorRole, &record.GroupStatus, &record.GroupRole, &record.GroupMemberStatus,
		&record.OwnerID, &record.ActorSystemRole,
	)
	if err != nil {
		return ProjectRecord{}, err
	}
	if actorRole != nil {
		record.ActorRole = *actorRole
	}
	return record, nil
}

// translateNotFound 把 pgx 的"无行"错误转换为域内精确的不存在错误。
func translateNotFound(err error, code domain.ErrorCode, message string) error {
	if apperr.IsNoRows(err) {
		return domain.New(code, message)
	}
	return fmt.Errorf("查询计划视图失败: %w", err)
}

// appendTaskFilters 追加任务侧机械筛选。//
// 每个成员条件一个独立 EXISTS：owner 与 member 同时给出时，
// 必须分别判定（可以命中同一个人的两条关系，也可以命中两个人）。
func appendTaskFilters(query string, args []any, filter TaskFilter) (string, []any) {
	if filter.Status != nil {
		args = append(args, *filter.Status)
		query += fmt.Sprintf(` AND t.status = $%d`, len(args))
	}
	if filter.Priority != nil {
		args = append(args, *filter.Priority)
		query += fmt.Sprintf(` AND t.priority = $%d`, len(args))
	}
	if filter.OwnerID != nil {
		args = append(args, *filter.OwnerID)
		query += fmt.Sprintf(` AND EXISTS (
			SELECT 1 FROM task_members mo
			 WHERE mo.task_id = t.id AND mo.role = 'owner' AND mo.user_id = $%d)`, len(args))
	}
	if filter.MemberID != nil {
		args = append(args, *filter.MemberID)
		query += fmt.Sprintf(` AND EXISTS (
			SELECT 1 FROM task_members mm
			 WHERE mm.task_id = t.id AND mm.user_id = $%d)`, len(args))
	}
	if filter.DueFrom != nil {
		args = append(args, *filter.DueFrom)
		query += fmt.Sprintf(` AND t.end_date >= $%d`, len(args))
	}
	if filter.DueTo != nil {
		args = append(args, *filter.DueTo)
		query += fmt.Sprintf(` AND t.end_date <= $%d`, len(args))
	}
	if filter.Overdue != nil && *filter.Overdue {
		// 逾期是派生状态：end_date < 今天 且状态未结束。
		// 与 domain.IsOverdue 使用同一套判断，SQL 只用于把候选集缩小。
		args = append(args, domain.DayOf(filter.Today))
		query += fmt.Sprintf(` AND t.end_date < $%d
			AND t.status IN ('not_started', 'in_progress')`, len(args))
	}
	return query, args
}
