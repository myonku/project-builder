// Package auth 实现注册、登录、会话与当前用户用例。
//
// 模块内文件分工（backend/docs/B01-Go服务端架构设计.md 4.2）：
//   - repo.go    ：本模块的 SQL 访问，显式 pgx 查询，不使用 ORM；
//   - session.go ：Redis 会话存储；
//   - service.go ：用例编排、权限与领域规则调用、事务边界；
//   - http.go    ：路由、绑定、响应。
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
)

// users 表的显式列清单。集中在一处，避免各查询列顺序或字段遗漏不一致。
const userColumns = `id, username, email, display_name, password_hash,
	status, system_role, timezone, reminder_enabled, last_login_at, created_at, updated_at,
	avatar_updated_at, session_version`

// UserRecord 是 users 表的行记录。
//
// 命名遵循"数据库类型不得出现在 HTTP 响应结构中"的边界要求：
// HTTP 层使用自己的响应结构，由 handler 显式投影，不直接序列化本结构。
type UserRecord struct {
	SessionVersion  int
	ID              string
	Username        string
	Email           string
	DisplayName     string
	PasswordHash    string
	Status          string
	SystemRole      string
	Timezone        string
	ReminderEnabled bool
	LastLoginAt     *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
	AvatarUpdatedAt *time.Time
}

// ToDomain 转换为领域实体。
func (r UserRecord) ToDomain() domain.User {
	return domain.User{
		ID:              r.ID,
		Username:        r.Username,
		Email:           r.Email,
		DisplayName:     r.DisplayName,
		PasswordHash:    r.PasswordHash,
		Status:          r.Status,
		SystemRole:      r.SystemRole,
		Timezone:        r.Timezone,
		ReminderEnabled: r.ReminderEnabled,
	}
}

// NewUser 是创建用户所需的字段。
type NewUser struct {
	ID              string
	Username        string
	Email           string
	DisplayName     string
	PasswordHash    string
	Timezone        string
	Status          string
	SystemRole      string
	ReminderEnabled bool
}

// Repository 是 auth 模块的 SQL 访问层。
//
// 只承担持久化职责：不做权限判断、不做状态流转、不校验业务规则
// （见项目计划管理系统-全局基线约定.md 11.4）。
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository 构造仓储。
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// CreateUser 插入用户并回读数据库生成的列。
//
// 历史缺陷对照：早期实现在 UPDATE/INSERT 刷新后读取被标记为过期的 ORM 属性，
// 触发隐式同步 IO 并抛 MissingGreenlet，导致所有写端点 500。
// 这里统一使用 INSERT ... RETURNING，让 updated_at 由数据库真实生成并被显式回读
// （见 backend/docs/B01-Go服务端架构设计.md 5.1）。
func (r *Repository) CreateUser(
	ctx context.Context, in NewUser, audit *apperr.AuditWrite,
) (UserRecord, error) {
	const query = `
		INSERT INTO users (
			id, username, email, display_name, password_hash,
			status, system_role, timezone, reminder_enabled
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING ` + userColumns

	// 审计与插入同事务（《A09》）：插入失败时不会留下"用户已创建"的记录。
	var record UserRecord
	err := apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		created, err := scanUser(tx.QueryRow(ctx, query,
			in.ID, in.Username, in.Email, in.DisplayName, in.PasswordHash,
			in.Status, in.SystemRole, in.Timezone, in.ReminderEnabled,
		))
		if err != nil {
			return translateUserWriteError(err)
		}
		record = created
		return audit.Flush(ctx, tx)
	})
	if err != nil {
		return UserRecord{}, err
	}
	return record, nil
}

// FindByID 按主键查询用户。
func (r *Repository) FindByID(ctx context.Context, id string) (UserRecord, error) {
	const query = `SELECT ` + userColumns + ` FROM users WHERE id = $1`

	record, err := scanUser(r.pool.QueryRow(ctx, query, id))
	if err != nil {
		return UserRecord{}, translateNotFound(err, domain.CodeUserNotFound, "User not found")
	}
	return record, nil
}

// FindByIdentifier 按用户名或邮箱查询用户。
//
// 使用 lower() 比较，与 uq_users_username_lower / uq_users_email_lower
// 两个唯一索引使用同一表达式，因此该查询可以走索引且与约束语义一致。
func (r *Repository) FindByIdentifier(ctx context.Context, identifier string) (UserRecord, error) {
	const query = `
		SELECT ` + userColumns + `
		FROM users
		WHERE lower(username) = lower($1) OR lower(email) = lower($1)
		LIMIT 1`

	record, err := scanUser(r.pool.QueryRow(ctx, query, identifier))
	if err != nil {
		return UserRecord{}, translateNotFound(err, domain.CodeInvalidCredentials, "Invalid credentials")
	}
	return record, nil
}

// FindByUsername 按用户名查询用户（不区分大小写）。
//
// 与 FindByIdentifier 的区别：后者同时匹配邮箱，服务于登录路径；
// 引导管理员初始化必须**只按用户名**定位目标账号，否则当一个用户名恰好等于
// 另一个账号的邮箱时，会把错误的账号提升为管理员。
//
// 使用 lower() 比较，与 uq_users_username_lower 使用同一表达式，
// 因此该查询可以走索引且与约束语义一致。
func (r *Repository) FindByUsername(ctx context.Context, username string) (UserRecord, error) {
	const query = `
		SELECT ` + userColumns + `
		FROM users
		WHERE lower(username) = lower($1)
		LIMIT 1`

	record, err := scanUser(r.pool.QueryRow(ctx, query, username))
	if err != nil {
		return UserRecord{}, translateNotFound(err, domain.CodeUserNotFound, "User not found")
	}
	return record, nil
}

// UpdateSystemRole 设置用户的系统角色并回读。
//
// 只改 system_role 与 updated_at：引导管理员初始化必须能重复执行，
// 且**绝不能**触碰密码摘要（否则每次重启都会把管理员改过的密码打回配置里的值）。
func (r *Repository) UpdateSystemRole(ctx context.Context, id, role string) (UserRecord, error) {
	const query = `
		UPDATE users
		   SET system_role = $2,
		       updated_at = now()
		 WHERE id = $1
		RETURNING ` + userColumns

	record, err := scanUser(r.pool.QueryRow(ctx, query, id, role))
	if err != nil {
		return UserRecord{}, translateNotFound(err, domain.CodeUserNotFound, "User not found")
	}
	return record, nil
}

// TouchLastLogin 记录最后登录时间并回读最新 updated_at。
//
// 使用 UPDATE ... RETURNING 显式回读，不依赖内存中可能过期的对象状态。
func (r *Repository) TouchLastLogin(ctx context.Context, id string, at time.Time) (UserRecord, error) {
	const query = `
		UPDATE users
		   SET last_login_at = $2,
		       updated_at = now()
		 WHERE id = $1
		RETURNING ` + userColumns

	record, err := scanUser(r.pool.QueryRow(ctx, query, id, at))
	if err != nil {
		return UserRecord{}, translateNotFound(err, domain.CodeUserNotFound, "User not found")
	}
	return record, nil
}

// UpdatePasswordHash 更新密码摘要，用于登录后按需升级弱参数摘要。
func (r *Repository) UpdatePasswordHash(ctx context.Context, id, hash, expectedHash string) error {
	const query = `UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1 AND password_hash = $3`
	tag, err := r.pool.Exec(ctx, query, id, hash, expectedHash)
	if err != nil {
		return fmt.Errorf("更新密码摘要失败: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.New(domain.CodeUserNotFound, "User not found")
	}
	return nil
}

// UpdatePasswordWithCAS 以原子 CAS 更新密码摘要并回读最新 updated_at。
//
// 用途：管理员重置密码。版本条件必须放进 WHERE 而不是先读后写
// （见 backend/docs/B01-Go服务端架构设计.md 5.2）——先读后写在并发下会让两个重置请求
// 都"成功"，此时后写入的那个静默覆盖前一个，而两边都认为自己的版本是最新的。
func (r *Repository) UpdatePasswordWithCAS(
	ctx context.Context, id, hash string, expectedUpdated time.Time, audit *apperr.AuditWrite,
) (UserRecord, error) {
	const query = `
		UPDATE users
		   SET password_hash = $2, session_version = session_version + 1,
		       updated_at = now()
		 WHERE id = $1 AND updated_at = $3
		RETURNING ` + userColumns

	var record UserRecord
	err := apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		updated, err := scanUser(tx.QueryRow(ctx, query, id, hash, expectedUpdated))
		if err != nil {
			return translateUserWriteError(err)
		}
		record = updated
		if _, err := tx.Exec(ctx, `UPDATE password_reset_tokens SET consumed_at=now(), updated_at=now() WHERE user_id=$1 AND consumed_at IS NULL`, id); err != nil {
			return err
		}
		return audit.Flush(ctx, tx)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserRecord{}, r.distinguishMissing(ctx, id)
		}
		return UserRecord{}, err
	}
	return record, nil
}

// distinguishMissing 区分"用户不存在"与"版本冲突"。
//
// 纯存在性查询，不复用带身份参数的查询：传入空 UUID 会触发 22P02 并退化为 500
// （第二阶段实测踩到过）。全模块只保留这一处存在性判断，避免各写一份。
func (r *Repository) distinguishMissing(ctx context.Context, id string) error {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, id).Scan(&exists)
	if err != nil {
		return fmt.Errorf("查询用户存在性失败: %w", err)
	}
	if !exists {
		return domain.New(domain.CodeUserNotFound, "User not found")
	}
	return domain.ErrVersionConflict
}

// scanUser 从一行结果读取用户记录。
func scanUser(row pgx.Row) (UserRecord, error) {
	var record UserRecord
	err := row.Scan(
		&record.ID,
		&record.Username,
		&record.Email,
		&record.DisplayName,
		&record.PasswordHash,
		&record.Status,
		&record.SystemRole,
		&record.Timezone,
		&record.ReminderEnabled,
		&record.LastLoginAt,
		&record.CreatedAt,
		&record.UpdatedAt,
		&record.AvatarUpdatedAt,
		&record.SessionVersion,
	)
	if err != nil {
		return UserRecord{}, err
	}
	return record, nil
}

// translateNotFound 把 pgx 的"无行"错误转换为领域错误。
func translateNotFound(err error, code domain.ErrorCode, message string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.New(code, message)
	}
	return fmt.Errorf("查询用户失败: %w", err)
}

// 唯一约束名 → 领域错误码。数据库约束是并发重复写入的最终保证，
// 必须把约束冲突翻译成 409 而不是 500。
var userUniqueConstraints = map[string]struct {
	code    domain.ErrorCode
	message string
	field   string
	detail  string
}{
	"uq_users_username_lower": {domain.CodeConflict, "Username already taken", "username", "该用户名已被占用"},
	"uq_users_username":       {domain.CodeConflict, "Username already taken", "username", "该用户名已被占用"},
	"uq_users_email_lower":    {domain.CodeConflict, "Email already registered", "email", "该邮箱已被注册"},
	"uq_users_email":          {domain.CodeConflict, "Email already registered", "email", "该邮箱已被注册"},
}

// translateUserWriteError 把数据库写入错误翻译为领域错误。
func translateUserWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
		if mapped, ok := userUniqueConstraints[pgErr.ConstraintName]; ok {
			return domain.New(mapped.code, mapped.message).
				WithField(mapped.field, mapped.detail).
				WithCause(err)
		}
		// 未登记的唯一约束：仍然按冲突处理，但保留约束名便于排查。
		return domain.ErrConflict.WithCause(fmt.Errorf("唯一约束冲突 %s: %w", pgErr.ConstraintName, err))
	}
	return fmt.Errorf("写入用户失败: %w", err)
}
