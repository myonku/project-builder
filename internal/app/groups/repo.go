// Package groups 实现工作组的用例、SQL 访问与 HTTP 端点。
//
// 文件分工与 auth 一致：repo.go（SQL）、service.go（用例与权限）、http.go（路由与绑定）。
package groups

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
)

// groupColumns 是 groups 表的显式列清单，集中一处避免列顺序不一致。
const groupColumns = `g.id, g.name, g.description, g.status, g.owner_id, g.created_at, g.updated_at`

// groupReturningColumns 与 groupColumns 相同，但不带表别名。
//
// INSERT/UPDATE 的 RETURNING 子句里没有 FROM 别名，写 g.id 会报
// "missing FROM-clause entry for table g"。两套常量集中在这里，
// 避免有人为了复用而把别名写进 RETURNING。
const groupReturningColumns = `id, name, description, status, owner_id, created_at, updated_at`

// GroupRecord 是 groups 表的一行（含当前用户在该组内的成员关系）。
type GroupRecord struct {
	ID          string
	Name        string
	Description *string
	Status      string
	OwnerID     string
	CreatedAt   time.Time
	UpdatedAt   time.Time

	// Membership 是当前请求用户在**本组**的成员关系；nil 表示不是成员。
	Membership *MemberRecord

	// ActorSystemRole 是当前请求用户的系统角色（member / admin）。
	//
	// 随查询一并取出，而不是让用例层再查一次用户：权限判定需要它
	// （domain 的组权限函数对系统管理员放行），而用例层手上只有 actorID。
	// 早期实现各处只传 domain.User{ID: actorID}，角色恒为空，
	// 于是"管理员放行"写了却从未生效。
	ActorSystemRole string
}

// ToDomain 转换为领域实体。
func (r GroupRecord) ToDomain() domain.Group {
	return domain.Group{
		ID:          r.ID,
		Name:        r.Name,
		Description: r.Description,
		Status:      r.Status,
		OwnerID:     r.OwnerID,
	}
}

// MemberRecord 是 group_members 的一行，并带上用户的展示字段。
type MemberRecord struct {
	ID          string
	GroupID     string
	UserID      string
	Role        string
	Status      string
	JoinedAt    time.Time
	Username    string
	DisplayName string
	Email       string
}

// ToDomain 转换为领域实体。
func (r MemberRecord) ToDomain() domain.GroupMember {
	return domain.GroupMember{
		ID:      r.ID,
		GroupID: r.GroupID,
		UserID:  r.UserID,
		Role:    r.Role,
		Status:  r.Status,
	}
}

// JoinRequestRecord 是 group_join_requests 的一行，并带上申请人展示字段。
type JoinRequestRecord struct {
	ID          string
	GroupID     string
	UserID      string
	Status      string
	ReviewedBy  *string
	ReviewedAt  *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Username    string
	DisplayName string
	Email       string
}

// ToDomain 转换为领域实体。
func (r JoinRequestRecord) ToDomain() domain.GroupJoinRequest {
	return domain.GroupJoinRequest{
		ID:      r.ID,
		GroupID: r.GroupID,
		UserID:  r.UserID,
		Status:  r.Status,
	}
}

// UserRecord 是校验目标用户存在性与可用性所需的最小字段。
type UserRecord struct {
	ID     string
	Status string
}

// IsActive 报告用户是否可用。
func (u UserRecord) IsActive() bool { return u.Status == domain.UserStatusActive }

// CreateGroupParams 是创建工作组所需的字段。
type CreateGroupParams struct {
	ID          string
	Name        string
	Description *string
	MemberID    string
	OwnerID     string
}

// GroupUpdateParams 是更新工作组的字段。
type GroupUpdateParams struct {
	ID              string
	Name            *string
	Description     *string
	DescriptionSet  bool
	Status          *string
	ExpectedUpdated time.Time
}

// ListFilter 是工作组列表筛选条件。
type ListFilter struct {
	UserID string
	Status *string
}

// MemberFilter 是成员列表筛选条件。
type MemberFilter struct {
	GroupID string
	Role    *string
	Status  *string
}

// Repository 是 groups 模块的 SQL 访问层。
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository 构造仓储。
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Pool 暴露连接池，供用例层开启事务（多步原子写入）。
func (r *Repository) Pool() *pgxpool.Pool { return r.pool }

// db 返回用于自动提交模式的执行器。
// func (r *Repository) db() apperr.Querier { return r.pool }

// CreateGroupInTx 在一个事务内创建工作组的**全部初始状态**。
//
// 单一写路径（docs/02 §3.2.1 保障 1）：groups.owner_id 与
// group_members(owner, manager, active) 必须同生共死，不允许只写其中之一。
// 因此这里把两步放在同一个事务里，而不是让服务层各调一次。
func (r *Repository) CreateGroupInTx(ctx context.Context, params CreateGroupParams) (GroupRecord, error) {
	var created GroupRecord

	err := apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		const insertGroup = `
			INSERT INTO groups (id, name, description, owner_id)
			VALUES ($1, $2, $3, $4)
			RETURNING ` + groupReturningColumns

		if err := tx.QueryRow(ctx, insertGroup,
			params.ID, params.Name, params.Description, params.OwnerID,
		).Scan(&created.ID, &created.Name, &created.Description, &created.Status,
			&created.OwnerID, &created.CreatedAt, &created.UpdatedAt); err != nil {
			return apperr.Translate(err, constraintMap)
		}

		const insertOwnerMember = `
			INSERT INTO group_members (id, group_id, user_id, role, status)
			VALUES ($1, $2, $3, 'manager', 'active')`

		if _, err := tx.Exec(ctx, insertOwnerMember,
			params.MemberID, params.ID, params.OwnerID,
		); err != nil {
			return apperr.Translate(err, constraintMap)
		}

		// 回读刚写入的成员关系，作为"单一写路径成功"的证据。
		const readMember = `
			SELECT id, group_id, user_id, role, status, joined_at
			  FROM group_members
			 WHERE group_id = $1 AND user_id = $2`

		var member MemberRecord
		if err := tx.QueryRow(ctx, readMember, params.ID, params.OwnerID).Scan(
			&member.ID, &member.GroupID, &member.UserID, &member.Role,
			&member.Status, &member.JoinedAt,
		); err != nil {
			return fmt.Errorf("回读所有者成员关系失败: %w", err)
		}
		created.Membership = &member
		return nil
	})
	if err != nil {
		return GroupRecord{}, err
	}
	return created, nil
}

// FindGroupForUser 查询工作组，并附带**当前用户**在该组内的成员关系。
//
// 一次查询同时返回两者：避免"先查组、再查成员"两次往返，
// 也让权限判断始终基于同一快照。
func (r *Repository) FindGroupForUser(ctx context.Context, groupID, userID string) (GroupRecord, error) {
	const query = `
		SELECT ` + groupColumns + `,
		       m.id, m.group_id, m.user_id, m.role, m.status, m.joined_at,
		       COALESCE(actor.system_role, '')
		  FROM groups g
		  LEFT JOIN group_members m ON m.group_id = g.id AND m.user_id = $2
		  LEFT JOIN users actor ON actor.id = $2
		 WHERE g.id = $1`

	var record GroupRecord
	var memberID, memberGroupID, memberUserID, memberRole, memberStatus *string
	var memberJoinedAt *time.Time

	err := r.pool.QueryRow(ctx, query, groupID, userID).Scan(
		&record.ID, &record.Name, &record.Description, &record.Status,
		&record.OwnerID, &record.CreatedAt, &record.UpdatedAt,
		&memberID, &memberGroupID, &memberUserID, &memberRole, &memberStatus, &memberJoinedAt,
		&record.ActorSystemRole,
	)
	if err != nil {
		if apperr.IsNoRows(err) {
			return GroupRecord{}, domain.New(domain.CodeGroupNotFound, "Group not found")
		}
		return GroupRecord{}, fmt.Errorf("查询工作组失败: %w", err)
	}

	if memberID != nil && memberUserID != nil && memberRole != nil && memberStatus != nil {
		record.Membership = &MemberRecord{
			ID:       *memberID,
			GroupID:  derefString(memberGroupID),
			UserID:   *memberUserID,
			Role:     *memberRole,
			Status:   *memberStatus,
			JoinedAt: derefTime(memberJoinedAt),
		}
	}
	return record, nil
}

// groupScopeJoin / groupScopeCondition 定义"当前用户可见的工作组"。
//
// 两种来源：我是该组的成员（**含已被移除的成员关系**，与引入管理员放行前的行为一致
// ——前端需要展示"已停用/已被移除"的状态）；以及**系统管理员**可以查看全部工作组
// （需求文档 4.1：管理所有用户、工作组和系统级数据、处理异常数据）。
//
// 为什么必须是 LEFT JOIN + 显式条件，而不是原来的 INNER JOIN：
// 管理员通常不是组员，INNER JOIN 会让列表对他恒为空——那样"管理员能处理异常数据"
// 就只是文档里的一句话，而不是可用的能力。
//
// 计数与列表**共用**这两个常量，避免二者筛选条件漂移
// （第二阶段实测过 total 与实际条目数不一致的缺陷）。
const groupScopeJoin = `
		  LEFT JOIN group_members m ON m.group_id = g.id AND m.user_id = $1
		  LEFT JOIN users actor ON actor.id = $1`

const groupScopeCondition = `( m.user_id IS NOT NULL OR actor.system_role = 'admin' )`

// CountGroups 统计符合条件的当前用户所属工作组数量。
func (r *Repository) CountGroups(ctx context.Context, filter ListFilter) (int, error) {
	query := `
		SELECT count(*)
		  FROM groups g` + groupScopeJoin + `
		 WHERE ` + groupScopeCondition
	args := []any{filter.UserID}

	if filter.Status != nil {
		args = append(args, *filter.Status)
		query += fmt.Sprintf(" AND g.status = $%d", len(args))
	}

	var total int
	if err := r.pool.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("统计工作组失败: %w", err)
	}
	return total, nil
}

// ListGroups 分页查询当前用户所属工作组。
//
// orderBy 必须来自 listquery 的白名单，绝不拼接用户输入。
func (r *Repository) ListGroups(ctx context.Context, filter ListFilter, orderBy string, limit, offset int) ([]GroupRecord, error) {
	query := `
		SELECT ` + groupColumns + `,
		       m.id, m.group_id, m.user_id, m.role, m.status, m.joined_at,
		       COALESCE(actor.system_role, '')
		  FROM groups g` + groupScopeJoin + `
		 WHERE ` + groupScopeCondition
	args := []any{filter.UserID}

	if filter.Status != nil {
		args = append(args, *filter.Status)
		query += fmt.Sprintf(" AND g.status = $%d", len(args))
	}

	args = append(args, limit, offset)
	query += fmt.Sprintf(" ORDER BY %s LIMIT $%d OFFSET $%d", orderBy, len(args)-1, len(args))

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询工作组列表失败: %w", err)
	}
	defer rows.Close()

	var records []GroupRecord
	for rows.Next() {
		var record GroupRecord
		var member MemberRecord
		var memberID, memberGroupID, memberUserID, memberRole, memberStatus *string
		var memberJoinedAt *time.Time
		if err := rows.Scan(
			&record.ID, &record.Name, &record.Description, &record.Status,
			&record.OwnerID, &record.CreatedAt, &record.UpdatedAt,
			&memberID, &memberGroupID, &memberUserID, &memberRole,
			&memberStatus, &memberJoinedAt,
			&record.ActorSystemRole,
		); err != nil {
			return nil, fmt.Errorf("读取工作组行失败: %w", err)
		}
		// 管理员可能不是组员，因此成员关系是可空的。
		if memberID != nil && memberUserID != nil && memberRole != nil && memberStatus != nil {
			member.ID = *memberID
			member.GroupID = derefString(memberGroupID)
			member.UserID = *memberUserID
			member.Role = *memberRole
			member.Status = *memberStatus
			member.JoinedAt = derefTime(memberJoinedAt)
			record.Membership = &member
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历工作组列表失败: %w", err)
	}
	return records, nil
}

// TransferOwnershipParams 是转移工作组所有权所需的字段。
type TransferOwnershipParams struct {
	GroupID         string
	NewOwnerID      string
	PreviousOwnerID string
	ExpectedUpdated time.Time
}

// TransferOwnership 在同一事务内转移工作组所有权。
//
// 写两处，因为它们是同一事实的两种表示（docs/02 §3.2.1）：
//  1. groups.owner_id —— 权威来源；
//  2. group_members(new_owner) 的 role='manager' + status='active' —— 权限判定的投影。
//     所有者必须是组管理员，否则"所有者"会看不到自己也管不了组。
//
// 原所有者保留成员身份，但**降为普通成员**：所有权转移意味着交出管理权，
// 与项目/任务的持有者转移保持一致。新所有者之后可以再把他设为 manager
// （那是一次显式的、可审计的操作）。
//
// 版本条件放在 groups 的 CAS 上：与其它写路径一致，避免"先读后写"。
func (r *Repository) TransferOwnership(
	ctx context.Context, params TransferOwnershipParams,
) (GroupRecord, error) {
	var updated GroupRecord

	err := apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		const updateOwner = `
			UPDATE groups
			   SET owner_id = $3, updated_at = now()
			 WHERE id = $1 AND updated_at = $2
			RETURNING ` + groupReturningColumns

		err := tx.QueryRow(ctx, updateOwner,
			params.GroupID, params.ExpectedUpdated, params.NewOwnerID,
		).Scan(
			&updated.ID, &updated.Name, &updated.Description, &updated.Status,
			&updated.OwnerID, &updated.CreatedAt, &updated.UpdatedAt,
		)
		if err != nil {
			if apperr.IsNoRows(err) {
				return r.distinguishMissing(ctx, params.GroupID)
			}
			return apperr.Translate(err, constraintMap)
		}

		// 目标成为组管理员（成员行已由用例层确认为活跃成员）。
		const promote = `
			UPDATE group_members
			   SET role = 'manager', status = 'active', updated_at = now()
			 WHERE group_id = $1 AND user_id = $2`
		if _, err := tx.Exec(ctx, promote, params.GroupID, params.NewOwnerID); err != nil {
			return apperr.Translate(err, constraintMap)
		}

		// 原所有者降为普通成员：所有权转移就是交出管理权。
		const demote = `
			UPDATE group_members
			   SET role = 'member', updated_at = now()
			 WHERE group_id = $1 AND user_id = $2 AND role = 'manager'`
		if _, err := tx.Exec(ctx, demote, params.GroupID, params.PreviousOwnerID); err != nil {
			return apperr.Translate(err, constraintMap)
		}
		return nil
	})
	if err != nil {
		return GroupRecord{}, err
	}

	// 回读一次，把新所有者在该组的成员关系一并带上，保证响应与查询路径同形。
	record, err := r.FindGroupForUser(ctx, updated.ID, params.NewOwnerID)
	if err != nil {
		return GroupRecord{}, err
	}
	return record, nil
}

// UpdateGroup 以原子 CAS 更新工作组并回读最新 updated_at。
//
// 版本冲突的正确做法（docs/01 5.2）：把 expected_updated_at 放进 WHERE 子句，
// 而不是先 SELECT 比较再无条件 UPDATE——后者在并发下两个请求都能通过。
func (r *Repository) UpdateGroup(ctx context.Context, params GroupUpdateParams) (GroupRecord, error) {
	const query = `
		UPDATE groups
		   SET name        = COALESCE($3, name),
		       description = CASE WHEN $4 THEN $5 ELSE description END,
		       status      = COALESCE($6, status),
		       updated_at  = now()
		 WHERE id = $1 AND updated_at = $2
		RETURNING ` + groupReturningColumns

	var record GroupRecord
	err := r.pool.QueryRow(ctx, query,
		params.ID, params.ExpectedUpdated,
		params.Name, params.DescriptionSet, params.Description, params.Status,
	).Scan(&record.ID, &record.Name, &record.Description, &record.Status,
		&record.OwnerID, &record.CreatedAt, &record.UpdatedAt)

	if err != nil {
		if apperr.IsNoRows(err) {
			// 无行有两种可能：对象不存在，或版本不匹配。必须补一次存在性查询区分。
			return GroupRecord{}, r.distinguishMissing(ctx, params.ID)
		}
		return GroupRecord{}, apperr.Translate(err, constraintMap)
	}
	return record, nil
}

// distinguishMissing 区分"工作组不存在"与"版本冲突"。
//
// CAS 更新返回 0 行有两种原因，必须补一次存在性查询才能给出正确状态码：
// 直接返回 version_conflict 会把"组已被删除"误报为并发冲突。
//
// 注意这里查询的是**纯存在性**，不涉及任何用户身份：
// 早期实现复用了"按用户查询组"的方法并传入空 userID，
// 结果触发 'invalid input syntax for type uuid: ""'（22P02）并退化为 500。
func (r *Repository) distinguishMissing(ctx context.Context, groupID string) error {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM groups WHERE id = $1)`, groupID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("查询工作组存在性失败: %w", err)
	}
	if !exists {
		return domain.New(domain.CodeGroupNotFound, "Group not found")
	}
	return domain.ErrVersionConflict
}

// FindMember 查询指定成员关系。
func (r *Repository) FindMember(ctx context.Context, groupID, userID string) (MemberRecord, error) {
	const query = `
		SELECT m.id, m.group_id, m.user_id, m.role, m.status, m.joined_at,
		       u.username, u.display_name, u.email
		  FROM group_members m
		  JOIN users u ON u.id = m.user_id
		 WHERE m.group_id = $1 AND m.user_id = $2`

	var member MemberRecord
	err := r.pool.QueryRow(ctx, query, groupID, userID).Scan(
		&member.ID, &member.GroupID, &member.UserID, &member.Role, &member.Status,
		&member.JoinedAt, &member.Username, &member.DisplayName, &member.Email,
	)
	if err != nil {
		if apperr.IsNoRows(err) {
			return MemberRecord{}, domain.New(domain.CodeUserNotFound,
				"该用户不是工作组成员")
		}
		return MemberRecord{}, fmt.Errorf("查询工作组成员失败: %w", err)
	}
	return member, nil
}

// CountMembers 统计符合条件的成员数量。
func (r *Repository) CountMembers(ctx context.Context, filter MemberFilter) (int, error) {
	query := `SELECT count(*) FROM group_members m WHERE m.group_id = $1`
	args := []any{filter.GroupID}

	if filter.Role != nil {
		args = append(args, *filter.Role)
		query += fmt.Sprintf(" AND m.role = $%d", len(args))
	}
	if filter.Status != nil {
		args = append(args, *filter.Status)
		query += fmt.Sprintf(" AND m.status = $%d", len(args))
	}

	var total int
	if err := r.pool.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("统计工作组成员失败: %w", err)
	}
	return total, nil
}

// ListMembers 分页查询工作组成员（含用户展示字段）。
func (r *Repository) ListMembers(ctx context.Context, filter MemberFilter, orderBy string, limit, offset int) ([]MemberRecord, error) {
	query := `
		SELECT m.id, m.group_id, m.user_id, m.role, m.status, m.joined_at,
		       u.username, u.display_name, u.email
		  FROM group_members m
		  JOIN users u ON u.id = m.user_id
		 WHERE m.group_id = $1`
	args := []any{filter.GroupID}

	if filter.Role != nil {
		args = append(args, *filter.Role)
		query += fmt.Sprintf(" AND m.role = $%d", len(args))
	}
	if filter.Status != nil {
		args = append(args, *filter.Status)
		query += fmt.Sprintf(" AND m.status = $%d", len(args))
	}

	args = append(args, limit, offset)
	query += fmt.Sprintf(" ORDER BY %s LIMIT $%d OFFSET $%d", orderBy, len(args)-1, len(args))

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询工作组成员列表失败: %w", err)
	}
	defer rows.Close()

	var records []MemberRecord
	for rows.Next() {
		var member MemberRecord
		if err := rows.Scan(
			&member.ID, &member.GroupID, &member.UserID, &member.Role, &member.Status,
			&member.JoinedAt, &member.Username, &member.DisplayName, &member.Email,
		); err != nil {
			return nil, fmt.Errorf("读取工作组成员行失败: %w", err)
		}
		records = append(records, member)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历工作组成员列表失败: %w", err)
	}
	return records, nil
}

// FindUser 查询用户的最小信息，用于"目标用户必须是有效用户"的校验。
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

// AddMember 添加成员关系。
func (r *Repository) AddMember(ctx context.Context, id, groupID, userID, role string) (MemberRecord, error) {
	const query = `
		INSERT INTO group_members (id, group_id, user_id, role, status)
		VALUES ($1, $2, $3, $4, 'active')
		RETURNING id, group_id, user_id, role, status, joined_at`

	var member MemberRecord
	err := r.pool.QueryRow(ctx, query, id, groupID, userID, role).Scan(
		&member.ID, &member.GroupID, &member.UserID, &member.Role, &member.Status, &member.JoinedAt,
	)
	if err != nil {
		return MemberRecord{}, apperr.Translate(err, constraintMap)
	}
	return member, nil
}

// UpdateMember 修改成员角色或状态并回读。
func (r *Repository) UpdateMember(ctx context.Context, groupID, userID string, role, status *string) (MemberRecord, error) {
	const query = `
		UPDATE group_members
		   SET role       = COALESCE($3, role),
		       status     = COALESCE($4, status),
		       updated_at = now()
		 WHERE group_id = $1 AND user_id = $2
		RETURNING id, group_id, user_id, role, status, joined_at`

	var member MemberRecord
	err := r.pool.QueryRow(ctx, query, groupID, userID, role, status).Scan(
		&member.ID, &member.GroupID, &member.UserID, &member.Role, &member.Status, &member.JoinedAt,
	)
	if err != nil {
		if apperr.IsNoRows(err) {
			return MemberRecord{}, domain.New(domain.CodeUserNotFound, "该用户不是工作组成员")
		}
		return MemberRecord{}, apperr.Translate(err, constraintMap)
	}
	return member, nil
}

// CountManagers 统计组内的活跃管理者数量（含所有者）。
//
// 用途：判断"移除/降级"是否会破坏"至少有一个可管理成员"。
func (r *Repository) CountManagers(ctx context.Context, groupID string) (int, error) {
	const query = `
		SELECT count(*) FROM group_members
		 WHERE group_id = $1 AND role = 'manager' AND status = 'active'`

	var total int
	if err := r.pool.QueryRow(ctx, query, groupID).Scan(&total); err != nil {
		return 0, fmt.Errorf("统计组管理员失败: %w", err)
	}
	return total, nil
}

// FindJoinRequest 查询指定用户的加入申请。
func (r *Repository) FindJoinRequest(ctx context.Context, groupID, userID string) (JoinRequestRecord, error) {
	const query = `
		SELECT r.id, r.group_id, r.user_id, r.status, r.reviewed_by, r.reviewed_at,
		       r.created_at, r.updated_at, u.username, u.display_name, u.email
		  FROM group_join_requests r
		  JOIN users u ON u.id = r.user_id
		 WHERE r.group_id = $1 AND r.user_id = $2`

	return scanJoinRequest(r.pool.QueryRow(ctx, query, groupID, userID))
}

// CreateJoinRequest 创建加入申请。
func (r *Repository) CreateJoinRequest(ctx context.Context, id, groupID, userID string) (JoinRequestRecord, error) {
	const insert = `
		INSERT INTO group_join_requests (id, group_id, user_id, status)
		VALUES ($1, $2, $3, 'pending')
		RETURNING id`

	if _, err := r.pool.Exec(ctx, insert, id, groupID, userID); err != nil {
		// group_join_request_identity 冲突：说明已存在一条申请记录。
		// 这里必须翻译为 409 而不是 500（Python 版本漏掉了这个捕获）。
		return JoinRequestRecord{}, apperr.Translate(err, constraintMap)
	}
	return r.FindJoinRequest(ctx, groupID, userID)
}

// CountJoinRequests 统计符合条件的申请数量。
func (r *Repository) CountJoinRequests(ctx context.Context, groupID string, status *string) (int, error) {
	query := `SELECT count(*) FROM group_join_requests WHERE group_id = $1`
	args := []any{groupID}

	if status != nil {
		args = append(args, *status)
		query += fmt.Sprintf(" AND status = $%d", len(args))
	}

	var total int
	if err := r.pool.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("统计加入申请失败: %w", err)
	}
	return total, nil
}

// ListJoinRequests 分页查询加入申请。
func (r *Repository) ListJoinRequests(ctx context.Context, groupID string, status *string, orderBy string, limit, offset int) ([]JoinRequestRecord, error) {
	query := `
		SELECT r.id, r.group_id, r.user_id, r.status, r.reviewed_by, r.reviewed_at,
		       r.created_at, r.updated_at, u.username, u.display_name, u.email
		  FROM group_join_requests r
		  JOIN users u ON u.id = r.user_id
		 WHERE r.group_id = $1`
	args := []any{groupID}

	if status != nil {
		args = append(args, *status)
		query += fmt.Sprintf(" AND r.status = $%d", len(args))
	}

	args = append(args, limit, offset)
	query += fmt.Sprintf(" ORDER BY %s LIMIT $%d OFFSET $%d", orderBy, len(args)-1, len(args))

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询加入申请列表失败: %w", err)
	}
	defer rows.Close()

	var records []JoinRequestRecord
	for rows.Next() {
		record, err := scanJoinRequestRow(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历加入申请列表失败: %w", err)
	}
	return records, nil
}

// ReviewJoinRequestInTx 审核加入申请，并在通过时写入成员关系。
//
// 两步必须在同一事务内：审核为 approved 却没能写入成员关系，
// 会让用户看到"已通过"但访问不了组内计划。
func (r *Repository) ReviewJoinRequestInTx(
	ctx context.Context,
	groupID, userID, reviewerID, decision, memberID string,
) (JoinRequestRecord, error) {
	var reviewed JoinRequestRecord

	err := apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		// 只允许从 pending 迁移，避免重复审核（并发下两个请求同时通过）。
		const update = `
			UPDATE group_join_requests
			   SET status = $3, reviewed_by = $4, reviewed_at = now(), updated_at = now()
			 WHERE group_id = $1 AND user_id = $2 AND status = 'pending'
			RETURNING id`

		var id string
		if err := tx.QueryRow(ctx, update, groupID, userID, decision, reviewerID).Scan(&id); err != nil {
			if apperr.IsNoRows(err) {
				// 无行：申请不存在，或状态已不是 pending。补一次查询区分。
				const lookup = `SELECT status FROM group_join_requests WHERE group_id = $1 AND user_id = $2`
				var status string
				if lookupErr := tx.QueryRow(ctx, lookup, groupID, userID).Scan(&status); lookupErr != nil {
					if apperr.IsNoRows(lookupErr) {
						return domain.ErrResourceNotFound.WithField("user_id", "加入申请不存在")
					}
					return fmt.Errorf("查询加入申请状态失败: %w", lookupErr)
				}
				return domain.ErrConflict.WithField("status",
					fmt.Sprintf("申请已处于 %s 状态，不能重复审核", status))
			}
			return apperr.Translate(err, constraintMap)
		}

		if decision == domain.JoinRequestApproved {
			// 已是活跃成员时不重复插入：group_member_identity 会拒绝，
			// 这里用 DO UPDATE 把"重新申请并通过"处理成幂等恢复。
			const upsertMember = `
				INSERT INTO group_members (id, group_id, user_id, role, status)
				VALUES ($1, $2, $3, 'member', 'active')
				ON CONFLICT ON CONSTRAINT group_member_identity
				DO UPDATE SET status = 'active', updated_at = now()`

			if _, err := tx.Exec(ctx, upsertMember, memberID, groupID, userID); err != nil {
				return apperr.Translate(err, constraintMap)
			}
		}

		record, err := scanJoinRequest(tx.QueryRow(ctx, `
			SELECT r.id, r.group_id, r.user_id, r.status, r.reviewed_by, r.reviewed_at,
			       r.created_at, r.updated_at, u.username, u.display_name, u.email
			  FROM group_join_requests r
			  JOIN users u ON u.id = r.user_id
			 WHERE r.group_id = $1 AND r.user_id = $2`, groupID, userID))
		if err != nil {
			return err
		}
		reviewed = record
		return nil
	})
	if err != nil {
		return JoinRequestRecord{}, err
	}
	return reviewed, nil
}

// ReopenJoinRequest 把已结束的加入申请重新置为 pending。
//
// 为什么不是插入新行：group_join_request_identity 对 (group_id, user_id) 唯一，
// 保留单行并翻转状态既满足"被拒绝后可重新申请"（需求文档 5.2），
// 又不破坏唯一性约束。只在当前状态不是 pending 时生效，避免并发重复开启。
func (r *Repository) ReopenJoinRequest(ctx context.Context, groupID, userID string) (JoinRequestRecord, error) {
	const update = `
		UPDATE group_join_requests
		   SET status = 'pending', reviewed_by = NULL, reviewed_at = NULL, updated_at = now()
		 WHERE group_id = $1 AND user_id = $2 AND status <> 'pending'
		RETURNING id`

	var id string
	if err := r.pool.QueryRow(ctx, update, groupID, userID).Scan(&id); err != nil {
		if apperr.IsNoRows(err) {
			// 无行：申请不存在，或已经是 pending（并发下另一请求刚开启）。
			existing, lookupErr := r.FindJoinRequest(ctx, groupID, userID)
			if lookupErr != nil {
				return JoinRequestRecord{}, lookupErr
			}
			if existing.Status == domain.JoinRequestPending {
				return JoinRequestRecord{}, domain.ErrConflict.
					WithField("user_id", "已存在待审核的加入申请")
			}
			return JoinRequestRecord{}, domain.ErrConflict.
				WithField("user_id", "加入申请状态冲突")
		}
		return JoinRequestRecord{}, apperr.Translate(err, constraintMap)
	}
	return r.FindJoinRequest(ctx, groupID, userID)
}

// constraintMap 把本模块涉及的数据库约束名映射为领域错误。
//
// 键必须与 internal/database/migrations/000001_init.up.sql 中的约束名逐字一致；
// 约束名写错会导致翻译失效并退化为兜底错误码。
var constraintMap = map[string]apperr.ConstraintError{
	"group_member_identity": {
		Code: domain.CodeDuplicateMember, Message: "该成员已存在",
		Field: "user_id", Detail: "该用户已是工作组成员",
	},
	"group_join_request_identity": {
		Code: domain.CodeConflict, Message: "Join request already exists",
		Field: "user_id", Detail: "已存在加入申请记录",
	},
	"ck_groups_group_status": {
		Code: domain.CodeValidationError, Message: "status 取值非法",
		Field: "status", Detail: "状态只能是 active 或 disabled",
	},
	"ck_group_members_group_member_role": {
		Code: domain.CodeValidationError, Message: "role 取值非法",
		Field: "role", Detail: "角色只能是 member 或 manager",
	},
	"ck_group_members_group_member_status": {
		Code: domain.CodeValidationError, Message: "status 取值非法",
		Field: "status", Detail: "成员状态只能是 active 或 removed",
	},
	"fk_groups_owner_id_users": {
		Code: domain.CodeUserNotFound, Message: "User not found",
		Field: "owner_id", Detail: "所有者用户不存在",
	},
	"fk_group_members_user_id_users": {
		Code: domain.CodeUserNotFound, Message: "User not found",
		Field: "user_id", Detail: "目标用户不存在",
	},
}

// scanJoinRequest 从单行结果读取加入申请。
func scanJoinRequest(row pgx.Row) (JoinRequestRecord, error) {
	var record JoinRequestRecord
	err := row.Scan(
		&record.ID, &record.GroupID, &record.UserID, &record.Status,
		&record.ReviewedBy, &record.ReviewedAt, &record.CreatedAt, &record.UpdatedAt,
		&record.Username, &record.DisplayName, &record.Email,
	)
	if err != nil {
		if apperr.IsNoRows(err) {
			return JoinRequestRecord{}, domain.ErrResourceNotFound.
				WithField("user_id", "加入申请不存在")
		}
		return JoinRequestRecord{}, fmt.Errorf("查询加入申请失败: %w", err)
	}
	return record, nil
}

// scanJoinRequestRow 从多行结果读取加入申请。
func scanJoinRequestRow(rows pgx.Rows) (JoinRequestRecord, error) {
	var record JoinRequestRecord
	err := rows.Scan(
		&record.ID, &record.GroupID, &record.UserID, &record.Status,
		&record.ReviewedBy, &record.ReviewedAt, &record.CreatedAt, &record.UpdatedAt,
		&record.Username, &record.DisplayName, &record.Email,
	)
	if err != nil {
		return JoinRequestRecord{}, fmt.Errorf("读取加入申请行失败: %w", err)
	}
	return record, nil
}

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
