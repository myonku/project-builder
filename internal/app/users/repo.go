// Package users 实现用户资料的查询与维护用例（接口契约 4.3）。
//
// 模块内文件分工（docs/01-Go服务端架构设计.md 4.2）：
//   - repo.go    ：本模块的 SQL 访问，显式 pgx 查询，不使用 ORM；
//   - service.go ：用例编排、权限判定、事务边界；
//   - http.go    ：路由、绑定、响应。
//
// 与 app/auth 的分工：auth 负责"谁能成为当前用户"（注册、登录、会话），
// users 负责"已登录用户如何查看与维护用户资料"。两者都读写 users 表，
// 但列投影不同——本模块**只投影资料列，永不读取 password_hash**，
// 因此本模块的响应在结构上不可能泄漏密码摘要。
package users

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
)

// profileColumns 是 users 表的资料投影，**刻意不包含 password_hash**。
//
// 把摘要列排除在列清单之外，而不是"查出来但记得别返回"，
// 是让"不泄漏密码摘要"成为结构性保证而不是纪律要求。
const profileColumns = `id, username, email, display_name, status, system_role,
	timezone, reminder_enabled, last_login_at, created_at, updated_at`

// ProfileRecord 是 users 表的资料行记录。
//
// 命名遵循"数据库类型不得出现在 HTTP 响应结构中"的边界要求：
// HTTP 层使用自己的响应结构，由 handler 显式投影。
type ProfileRecord struct {
	ID              string
	Username        string
	Email           string
	DisplayName     string
	Status          string
	SystemRole      string
	Timezone        string
	ReminderEnabled bool
	LastLoginAt     *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ToDomain 转换为领域实体。密码摘要留空：本模块不持有它，也不需要使用它。
func (r ProfileRecord) ToDomain() domain.User {
	return domain.User{
		ID:              r.ID,
		Username:        r.Username,
		Email:           r.Email,
		DisplayName:     r.DisplayName,
		Status:          r.Status,
		SystemRole:      r.SystemRole,
		Timezone:        r.Timezone,
		ReminderEnabled: r.ReminderEnabled,
	}
}

// UpdateProfileParams 是资料更新的写入参数。
//
// 指针字段为 nil 表示"不修改"；在 SQL 中由 COALESCE 实现，
// 因此"未提供"与"提供空值"不会互相污染（空值由领域层在校验阶段拒绝）。
type UpdateProfileParams struct {
	ID              string
	ExpectedUpdated time.Time

	DisplayName     *string
	Email           *string
	Timezone        *string
	ReminderEnabled *bool
	Status          *string
	SystemRole      *string

	// GuardLastAdmin 为真时，在**同一事务**内复查"写入后系统仍至少保留一个
	// 启用中的管理员"（见 ensureAdminRetained）。
	//
	// 该复查必须与写入同事务：先读后写在并发下两个请求都能通过，
	// 结果就是两个管理员互相降级、系统失去全部管理入口。
	GuardLastAdmin bool
}

// Repository 是本模块的 SQL 访问层。
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository 构造仓储。
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// FindProfile 按主键查询用户资料。
func (r *Repository) FindProfile(ctx context.Context, userID string) (ProfileRecord, error) {
	const query = `SELECT ` + profileColumns + ` FROM users WHERE id = $1`

	record, err := scanProfile(r.pool.QueryRow(ctx, query, userID))
	if err != nil {
		return ProfileRecord{}, translateNotFound(err)
	}
	return record, nil
}

// ListFilter 是用户列表的筛选条件。
type ListFilter struct {
	// IncludeDisabled 为真时同时返回已停用账号。
	//
	// 默认排除停用账号，但它是**显式可查**的而不是被隐藏：停用是数据保留
	// 而不是删除（需求文档 4.2 第 6 条），因此管理列表必须能查出来。
	IncludeDisabled bool
}

// where 生成筛选条件，供计数与列表查询**共用**。
//
// 必须共用：第二阶段实测过"计数查询漏掉列表的筛选条件"，
// 表现为 pagination.total 与实际条目数不符——接口看起来正常，翻页却会错。
func (f ListFilter) where() string {
	if f.IncludeDisabled {
		return ""
	}
	return ` WHERE status = 'active'`
}

// CountProfiles 统计满足筛选条件的用户数。
func (r *Repository) CountProfiles(ctx context.Context, filter ListFilter) (int, error) {
	query := `SELECT count(*) FROM users` + filter.where()

	var total int
	if err := r.pool.QueryRow(ctx, query).Scan(&total); err != nil {
		return 0, fmt.Errorf("统计用户数失败: %w", err)
	}
	return total, nil
}

// ListProfiles 分页查询用户资料。
//
// orderBy 由 listquery 的白名单生成，列名永不来自用户输入；
// LIMIT/OFFSET 下推到 SQL，不全量载入内存后切片（契约 2.5）。
func (r *Repository) ListProfiles(
	ctx context.Context, filter ListFilter, orderBy string, limit, offset int,
) ([]ProfileRecord, error) {
	query := `SELECT ` + profileColumns + ` FROM users` + filter.where() +
		` ORDER BY ` + orderBy + ` LIMIT $1 OFFSET $2`

	rows, err := r.pool.Query(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("查询用户列表失败: %w", err)
	}
	defer rows.Close()

	var records []ProfileRecord
	for rows.Next() {
		record, err := scanProfile(rows)
		if err != nil {
			return nil, fmt.Errorf("读取用户列表失败: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历用户列表失败: %w", err)
	}
	return records, nil
}

// SharesWorkspace 报告两个用户是否同属一个工作组或同一个项目。
//
// 这是接口契约 4.3 中"可见性"的数据库侧判定：
// 同组成员或同项目成员的用户名、显示名与邮箱本来就能从成员列表读到，
// 因此允许互看资料不额外暴露信息；反过来，无交集的用户必须看不到彼此。
//
// 两个 EXISTS 各自独立，**不共用一个 JOIN**：与任务模块的
// owner_id + assignee_id 组合筛选同理，把两个条件压在同一个 JOIN 行上
// 会要求"两个用户在同一个组且同一个项目才可见"，得到恒为假的结果。
func (r *Repository) SharesWorkspace(ctx context.Context, viewerID, targetID string) (bool, error) {
	const query = `
		SELECT
			EXISTS (
				SELECT 1
				  FROM group_members mine
				  JOIN group_members theirs ON theirs.group_id = mine.group_id
				 WHERE mine.user_id = $1 AND mine.status = 'active'
				   AND theirs.user_id = $2 AND theirs.status = 'active'
			)
			OR EXISTS (
				SELECT 1
				  FROM project_members mine
				  JOIN project_members theirs ON theirs.project_id = mine.project_id
				 WHERE mine.user_id = $1 AND theirs.user_id = $2
			)`

	var shared bool
	if err := r.pool.QueryRow(ctx, query, viewerID, targetID).Scan(&shared); err != nil {
		return false, fmt.Errorf("查询用户协作范围失败: %w", err)
	}
	return shared, nil
}

// UpdateProfile 以原子 CAS 更新用户资料并回读最新 updated_at。
//
// CAS 的正确写法（docs/01 5.2）：版本条件放进 WHERE，而不是先 SELECT 比较再无条件 UPDATE。
//
// 列全部用 COALESCE：这些列在数据库中都是 NOT NULL，因此"未提供（NULL）"
// 与"提供了合法取值"可以被 SQL 无歧义地区分，不需要为"是否提供"再传一个布尔参数。
func (r *Repository) UpdateProfile(ctx context.Context, params UpdateProfileParams) (ProfileRecord, error) {
	if !params.GuardLastAdmin {
		return r.updateProfile(ctx, r.pool, params)
	}

	// 需要守住"至少一个启用中管理员"时，复查与写入必须在同一事务内完成。
	var record ProfileRecord
	err := apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		if err := r.ensureAdminRetained(ctx, tx, params); err != nil {
			return err
		}
		updated, err := r.updateProfile(ctx, tx, params)
		if err != nil {
			return err
		}
		record = updated
		return nil
	})
	if err != nil {
		return ProfileRecord{}, err
	}
	return record, nil
}

// updateProfile 执行实际的 CAS 写入。db 可以是连接池，也可以是事务。
func (r *Repository) updateProfile(ctx context.Context, db apperr.Querier, params UpdateProfileParams) (ProfileRecord, error) {
	const query = `
		UPDATE users
		   SET display_name     = COALESCE($3, display_name),
		       email            = COALESCE($4, email),
		       timezone         = COALESCE($5, timezone),
		       reminder_enabled = COALESCE($6, reminder_enabled),
		       status           = COALESCE($7, status),
		       system_role      = COALESCE($8, system_role),
		       updated_at       = now()
		 WHERE id = $1 AND updated_at = $2
		RETURNING ` + profileColumns

	row := db.QueryRow(ctx, query,
		params.ID, params.ExpectedUpdated,
		params.DisplayName, params.Email, params.Timezone,
		params.ReminderEnabled, params.Status, params.SystemRole,
	)

	record, err := scanProfile(row)
	if err != nil {
		if apperr.IsNoRows(err) {
			return ProfileRecord{}, r.distinguishMissing(ctx, db, params.ID)
		}
		return ProfileRecord{}, apperr.Translate(err, profileConstraintMap)
	}
	return record, nil
}

// ensureAdminRetained 在事务内复查"更新后系统仍至少保留一个启用中的管理员"。
//
// 实现方式（为什么不直接 count）：
//
//	SELECT ... FOR UPDATE 会锁定当前全部启用中的管理员行，并发的事务必须排队。
//	PostgreSQL 不允许聚合函数与 FOR UPDATE 同时使用，因此这里取出主键后在应用侧计数；
//	 并按 id 排序加锁，避免两个并发管理操作以不同顺序锁行而互相死锁。
//	当局势在等待期间发生变化（例如另一个事务已经把某人降级），
//	READ COMMITTED 会重新求值 WHERE 并跳过已不匹配的行，
//	因此排队结束后读到的是**提交后**的状态，判定不会是过期快照。
func (r *Repository) ensureAdminRetained(ctx context.Context, tx apperr.Querier, params UpdateProfileParams) error {
	const query = `
		SELECT id FROM users
		 WHERE system_role = 'admin' AND status = 'active'
		 ORDER BY id
		   FOR UPDATE`

	rows, err := tx.Query(ctx, query)
	if err != nil {
		return fmt.Errorf("锁定管理员行失败: %w", err)
	}
	defer rows.Close()

	var activeAdmins []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return fmt.Errorf("读取管理员行失败: %w", err)
		}
		activeAdmins = append(activeAdmins, id)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历管理员行失败: %w", err)
	}

	wasActiveAdmin := false
	for _, id := range activeAdmins {
		if id == params.ID {
			wasActiveAdmin = true
			break
		}
	}

	// 合并后的取值：目标用户当前是启用中的管理员，因此只有被显式改掉才会失去该身份。
	willBeActiveAdmin := wasActiveAdmin
	if params.Status != nil && *params.Status != domain.UserStatusActive {
		willBeActiveAdmin = false
	}
	if params.SystemRole != nil && *params.SystemRole != domain.UserSystemRoleAdmin {
		willBeActiveAdmin = false
	}

	otherActiveAdmins := len(activeAdmins)
	if wasActiveAdmin {
		otherActiveAdmins--
	}

	if !domain.WouldRemoveLastAdmin(wasActiveAdmin, willBeActiveAdmin, otherActiveAdmins) {
		return nil
	}

	// 指出究竟是哪一个字段触发了拒绝，前端才能给出可操作的提示。
	field := "status"
	if params.Status == nil || *params.Status == domain.UserStatusActive {
		field = "system_role"
	}
	return domain.ErrConflict.WithField(field,
		"系统必须至少保留一个启用中的系统管理员，否则将失去全部管理入口")
}

// distinguishMissing 区分"用户不存在"与"版本冲突"。
//
// 纯存在性查询，不复用带身份参数的查询：传入空 UUID 会触发 22P02 并退化为 500
// （第二阶段实测踩到过）。db 与写入同源，保证在事务中看到的是同一快照。
func (r *Repository) distinguishMissing(ctx context.Context, db apperr.Querier, userID string) error {
	var exists bool
	err := db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, userID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("查询用户存在性失败: %w", err)
	}
	if !exists {
		return notFound()
	}
	return domain.ErrVersionConflict
}

// scanProfile 从一行结果读取用户资料。
func scanProfile(row pgx.Row) (ProfileRecord, error) {
	var record ProfileRecord
	err := row.Scan(
		&record.ID,
		&record.Username,
		&record.Email,
		&record.DisplayName,
		&record.Status,
		&record.SystemRole,
		&record.Timezone,
		&record.ReminderEnabled,
		&record.LastLoginAt,
		&record.CreatedAt,
		&record.UpdatedAt,
	)
	if err != nil {
		return ProfileRecord{}, err
	}
	return record, nil
}

// notFound 返回域内精确的"用户不存在"，避免前端把 404 一概当成路由写错。
func notFound() error {
	return domain.New(domain.CodeUserNotFound, "User not found")
}

// translateNotFound 把 pgx 的"无行"错误转换为领域错误。
func translateNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound()
	}
	return fmt.Errorf("查询用户失败: %w", err)
}

// profileConstraintMap 把 users 表的唯一约束翻译为 409 冲突。
//
// 邮箱变更与注册走同一组唯一索引（uq_users_email_lower / uq_users_email），
// 因此这里的映射必须与 auth 模块保持一致；漏登记会退化为 500。
var profileConstraintMap = map[string]apperr.ConstraintError{
	"uq_users_email_lower": {
		Code: domain.CodeConflict, Message: "Email already registered",
		Field: "email", Detail: "该邮箱已被注册",
	},
	"uq_users_email": {
		Code: domain.CodeConflict, Message: "Email already registered",
		Field: "email", Detail: "该邮箱已被注册",
	},
}
