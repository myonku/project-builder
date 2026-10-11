package auth

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/myonku/project-builder/internal/config"
	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/platform"
	"github.com/myonku/project-builder/internal/platform/clock"
	"github.com/myonku/project-builder/internal/platform/idgen"
)

// UserRepository 是本模块对用户的持久化需求。
//
// 定义在消费方，由 internal/app/auth/repo.go 的实现满足，测试用替身实现。
type UserRepository interface {
	CreateUser(ctx context.Context, in NewUser) (UserRecord, error)
	FindByID(ctx context.Context, id string) (UserRecord, error)
	FindByIdentifier(ctx context.Context, identifier string) (UserRecord, error)
	FindByUsername(ctx context.Context, username string) (UserRecord, error)
	TouchLastLogin(ctx context.Context, id string, at time.Time) (UserRecord, error)
	UpdatePasswordHash(ctx context.Context, id, hash string) error
	UpdatePasswordWithCAS(ctx context.Context, id, hash string, expectedUpdated time.Time) (UserRecord, error)
	UpdateSystemRole(ctx context.Context, id, role string) (UserRecord, error)
}

// Service 承载认证用例。
type Service struct {
	users    UserRepository
	store    SessionStore
	hasher   *platform.PasswordHasher
	ids      *idgen.Generator
	clk      clock.Clock
	logger   *slog.Logger
	security config.SecurityConfig
}

// Options 是 Service 的构造参数。
type Options struct {
	Users    UserRepository
	Sessions SessionStore
	Hasher   *platform.PasswordHasher
	IDs      *idgen.Generator
	Clock    clock.Clock
	Logger   *slog.Logger
	Security config.SecurityConfig
}

// NewService 构造认证用例服务。
func NewService(opts Options) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	clk := opts.Clock
	if clk == nil {
		clk = clock.NewSystem()
	}
	hasher := opts.Hasher
	if hasher == nil {
		hasher = platform.NewPasswordHasher()
	}
	return &Service{
		users:    opts.Users,
		store:    opts.Sessions,
		hasher:   hasher,
		ids:      opts.IDs,
		clk:      clk,
		logger:   logger,
		security: opts.Security,
	}
}

// RegisterResult 是注册成功的结果。
type RegisterResult struct {
	User UserRecord
}

// Register 执行注册用例。
//
// 处理顺序（接口文档 5）：字段校验 → 唯一性检查 → 领域规则 → 事务写入。
// 注意：注册不自动登录，与 早期实现行为一致（接口文档 4.2 成功 201）。
func (s *Service) Register(ctx context.Context, in domain.RegistrationInput) (RegisterResult, error) {
	normalized, fields := domain.NormalizeRegistration(in, s.security.PasswordMinLength)
	if len(fields) > 0 {
		return RegisterResult{}, domain.ErrValidation.WithFields(fields)
	}

	// 提前检查以给出精确的字段级冲突提示。
	// 这不是并发保证：最终保证由 uq_users_*_lower 唯一索引提供，
	// 并发注册会在 CreateUser 处被翻译为 409。
	if err := s.ensureIdentityAvailable(ctx, normalized.Username, normalized.Email); err != nil {
		return RegisterResult{}, err
	}

	hash, err := s.hasher.Hash(normalized.Password)
	if err != nil {
		return RegisterResult{}, domain.ErrInternal.WithCause(err)
	}

	id, err := s.ids.UUID()
	if err != nil {
		return RegisterResult{}, domain.ErrInternal.WithCause(err)
	}

	record, err := s.users.CreateUser(ctx, NewUser{
		ID:              id,
		Username:        normalized.Username,
		Email:           normalized.Email,
		DisplayName:     normalized.DisplayName,
		PasswordHash:    hash,
		Timezone:        normalized.Timezone,
		Status:          domain.UserStatusActive,
		SystemRole:      domain.UserSystemRoleMember,
		ReminderEnabled: true,
	})
	if err != nil {
		return RegisterResult{}, err
	}

	// 只记录标识信息，绝不记录密码或摘要。
	s.logger.Info("用户注册成功",
		slog.String("user_id", record.ID),
		slog.String("username", record.Username),
	)
	return RegisterResult{User: record}, nil
}

// ensureIdentityAvailable 检查用户名与邮箱是否已被占用。
//
// 这里必须用 FindByID 而不是 FindByIdentifier：后者服务于登录路径，
// 语义是"查不到即凭据错误"，用它做可用性判断会把"用户名可用"误判为失败。
func (s *Service) ensureIdentityAvailable(ctx context.Context, username, email string) error {
	fields := map[string]string{}

	if id, err := s.findExistingID(ctx, username); err != nil {
		return err
	} else if id != "" {
		fields["username"] = "该用户名已被占用"
	}

	if id, err := s.findExistingID(ctx, email); err != nil {
		return err
	} else if id != "" {
		fields["email"] = "该邮箱已被注册"
	}

	if len(fields) > 0 {
		return domain.ErrConflict.WithFields(fields)
	}
	return nil
}

// findExistingID 按用户名或邮箱查找已存在的用户 ID；不存在时返回空串。
//
// 复用 FindByIdentifier 的匹配语义（lower(username) 或 lower(email)），
// 但把"不存在"视为正常结果而不是错误。
func (s *Service) findExistingID(ctx context.Context, identifier string) (string, error) {
	record, err := s.users.FindByIdentifier(ctx, identifier)
	switch {
	case err == nil:
		return record.ID, nil
	case isNotFound(err) || isInvalidCredentials(err):
		return "", nil
	default:
		return "", err
	}
}

// LoginResult 是登录成功的结果。
type LoginResult struct {
	User      UserRecord
	Session   Session
	Token     string
	ExpiresAt time.Time
}

// Login 执行登录用例。
func (s *Service) Login(ctx context.Context, in domain.LoginInput) (LoginResult, error) {
	normalized, fields := domain.NormalizeLogin(in)
	if len(fields) > 0 {
		return LoginResult{}, domain.ErrValidation.WithFields(fields)
	}

	// 统一失败文案：不区分"用户不存在"与"密码错误"，避免账号枚举。
	record, err := s.users.FindByIdentifier(ctx, normalized.Identifier)
	if err != nil {
		if isNotFound(err) || isInvalidCredentials(err) {
			return LoginResult{}, domain.ErrInvalidCredentials
		}
		return LoginResult{}, err
	}

	matched, err := s.hasher.Verify(normalized.Password, record.PasswordHash)
	if err != nil {
		// 摘要无法解析属于数据损坏，记录内部原因但对外仍是凭据错误。
		s.logger.Error("密码摘要无法解析",
			slog.String("user_id", record.ID),
			slog.Any("cause", err),
		)
		return LoginResult{}, domain.ErrInvalidCredentials
	}
	if !matched {
		return LoginResult{}, domain.ErrInvalidCredentials
	}

	// 停用用户不能登录（需求文档 5.1）。凭据正确时才检查状态，
	// 以免通过状态差异推断账号是否存在。
	if !record.ToDomain().IsActive() {
		return LoginResult{}, domain.ErrPermissionDenied.
			WithField("status", "账号已被停用，请联系管理员")
	}

	if !s.store.Available() {
		return LoginResult{}, ErrSessionBackendDisabled
	}

	// 登录成功后按需升级弱参数摘要。
	if s.hasher.NeedsRehash(record.PasswordHash) {
		newHash, hashErr := s.hasher.Hash(normalized.Password)
		if hashErr != nil {
			// 升级失败不应阻断登录，记录后继续。
			s.logger.Warn("密码摘要升级失败，本次登录继续",
				slog.String("user_id", record.ID),
				slog.Any("cause", hashErr),
			)
		} else if updErr := s.users.UpdatePasswordHash(ctx, record.ID, newHash); updErr != nil {
			s.logger.Warn("密码摘要写回失败，本次登录继续",
				slog.String("user_id", record.ID),
				slog.Any("cause", updErr),
			)
		} else {
			record.PasswordHash = newHash
		}
	}

	now := s.clk.Now()
	expiresAt := now.Add(s.security.SessionTTL())

	// 会话令牌使用密码学安全随机源，不使用可预测的 UUIDv7（全局基线约定 4.2）。
	token, err := s.ids.Token()
	if err != nil {
		return LoginResult{}, domain.ErrInternal.WithCause(err)
	}

	session := Session{
		Token:     token,
		UserID:    record.ID,
		CreatedAt: now,
		ExpiresAt: expiresAt,
	}
	if err := s.store.Save(ctx, session); err != nil {
		return LoginResult{}, err
	}

	updated, err := s.users.TouchLastLogin(ctx, record.ID, now)
	if err != nil {
		// 会话已建立，最后登录时间失败不应让用户重新登录一次。
		s.logger.Warn("记录最后登录时间失败",
			slog.String("user_id", record.ID),
			slog.Any("cause", err),
		)
		updated = record
	}

	s.logger.Info("用户登录成功", slog.String("user_id", updated.ID))
	return LoginResult{
		User:      updated,
		Session:   session,
		Token:     token,
		ExpiresAt: expiresAt,
	}, nil
}

// Logout 撤销会话。登出必须幂等：令牌缺失或已失效都返回成功。
func (s *Service) Logout(ctx context.Context, token string) error {
	if err := s.store.Delete(ctx, token); err != nil {
		return err
	}
	return nil
}

// CurrentUser 依据会话令牌解析当前用户。
//
// 每次都回数据库读取用户，而不是把状态快照进会话：
// 这样用户被停用、被改角色后立即生效，不需要额外的会话失效广播。
func (s *Service) CurrentUser(ctx context.Context, token string) (UserRecord, error) {
	if token == "" {
		return UserRecord{}, domain.ErrAuthenticationRequired
	}

	session, err := s.store.Get(ctx, token)
	if err != nil {
		return UserRecord{}, err
	}

	record, err := s.users.FindByID(ctx, session.UserID)
	if err != nil {
		if isNotFound(err) {
			// 会话指向的用户已不存在：视为未登录，并清理这个孤儿会话。
			if delErr := s.store.Delete(ctx, token); delErr != nil {
				s.logger.Warn("清理孤儿会话失败", slog.Any("cause", delErr))
			}
			return UserRecord{}, domain.ErrAuthenticationRequired
		}
		return UserRecord{}, err
	}

	if !record.ToDomain().IsActive() {
		// 停用用户的既有会话立即失效。
		if delErr := s.store.Delete(ctx, token); delErr != nil {
			s.logger.Warn("撤销停用用户会话失败", slog.Any("cause", delErr))
		}
		return UserRecord{}, domain.ErrAuthenticationRequired
	}

	return record, nil
}

// isNotFound 报告错误是否为"对象不存在"。
func isNotFound(err error) bool {
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		return false
	}
	switch domainErr.Code {
	case domain.CodeResourceNotFound, domain.CodeUserNotFound,
		domain.CodeGroupNotFound, domain.CodeProjectNotFound, domain.CodeTaskNotFound:
		return true
	default:
		return false
	}
}

// isInvalidCredentials 报告错误是否为凭据错误。
func isInvalidCredentials(err error) bool {
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		return false
	}
	return domainErr.Code == domain.CodeInvalidCredentials
}

// SessionCookieMaxAge 返回 Cookie 的 Max-Age（秒）。
//
// 与 Redis TTL 使用同一配置值，避免 Cookie 生命周期与真实会话不一致。
func (s *Service) SessionCookieMaxAge() int {
	if s.security.SessionTTLSeconds <= 0 {
		return 0
	}
	return s.security.SessionTTLSeconds
}

// SessionCookieName 返回会话 Cookie 名称。
func (s *Service) SessionCookieName() string { return s.security.SessionCookieName }

// ResolveSession 依据会话令牌解析当前用户的领域表示。
//
// 供其他业务模块的 ActorLoader 使用：它们只需要 domain.User，
// 不应该知道会话存在 Redis、Cookie 叫什么名字。
func (s *Service) ResolveSession(ctx context.Context, token string) (domain.User, error) {
	record, err := s.CurrentUser(ctx, token)
	if err != nil {
		return domain.User{}, err
	}
	return record.ToDomain(), nil
}

// SecureCookies 返回是否要求 HTTPS-only Cookie。
func (s *Service) SecureCookies() bool { return s.security.SecureCookies }

// PublicUser 是用户对外表示，对应接口文档 3.1。
//
// 与数据库行结构刻意分离：password_hash 永远不会出现在这里
// （接口文档 3.1 末句：密码、密码摘要和会话令牌永不返回）。
type PublicUser struct {
	ID              string  `json:"id"`
	Username        string  `json:"username"`
	Email           string  `json:"email"`
	DisplayName     string  `json:"display_name"`
	Status          string  `json:"status"`
	SystemRole      string  `json:"system_role"`
	Timezone        string  `json:"timezone"`
	ReminderEnabled bool    `json:"reminder_enabled"`
	LastLoginAt     *string `json:"last_login_at"`
	UpdatedAt       string  `json:"updated_at"`
}

// NewPublicUser 把数据库行投影为对外结构。
func NewPublicUser(record UserRecord) PublicUser {
	var lastLogin *string
	if record.LastLoginAt != nil {
		formatted := domain.FormatTimestamp(*record.LastLoginAt)
		lastLogin = &formatted
	}
	return PublicUser{
		ID:              record.ID,
		Username:        record.Username,
		Email:           record.Email,
		DisplayName:     record.DisplayName,
		Status:          record.Status,
		SystemRole:      record.SystemRole,
		Timezone:        record.Timezone,
		ReminderEnabled: record.ReminderEnabled,
		LastLoginAt:     lastLogin,
		UpdatedAt:       domain.FormatTimestamp(record.UpdatedAt),
	}
}

// 编译期确认接口实现。
var _ UserRepository = (*Repository)(nil)
