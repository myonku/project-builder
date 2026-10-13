package auth

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
)

// 本文件是本模块给**装配点**（cmd/api 或测试装配）使用的适配入口。
//
// 使用场景：app/users 的"管理员代建账号"与"管理员重置密码"都是账号级操作，
// 必须复用本模块已有的注册与密码写入逻辑，而不是把归一化、密码强度、
// 唯一性检查与约束翻译复制一份。app/users 只声明自己需要的最小接口
// （users.Accounts），由装配点把 *Service 注入进去，两个业务模块之间
// 因此不产生 import 依赖。
//
// 与 projects.AccessFor 的处理方式一致：适配逻辑本身只做字段搬运与调用转发，
// 如果这里出现业务判断，说明边界划错了。

// CreateAccount 创建一个账号，语义与用户自助注册完全一致。
//
// 存在的唯一理由：让"管理员代建账号"与"自助注册"共用同一套实现。
// 两者若各自实现，密码强度、用户名归一化与唯一性检查迟早会分叉，
// 而这三项都属于安全边界。
//
// actorID 为空表示自助注册（操作人就是新账号本身）；非空表示管理员代建，
// 该 ID 会作为操作人写进操作记录。**操作记录由本方法构造**：新账号的 id
// 只有在这里才知道，调用方拿不到它。
func (s *Service) CreateAccount(
	ctx context.Context, in domain.RegistrationInput, actorID string,
) (domain.User, error) {
	result, err := s.register(ctx, in, actorID)
	if err != nil {
		return domain.User{}, err
	}
	return result.User.ToDomain(), nil
}

// ResetPassword 重置指定用户的密码，要求提交当前版本。
//
// audit 由调用方提供：日志与操作记录都需要同时记录"哪个管理员重置了哪个用户"，
// 而本方法拿不到操作者——调用方（users 端点）才持有当前用户。
//
// 注意：本方法**不**使该用户已有的会话失效。这是**已确认的决策**（2026-10-11）：
// 本系统面向少数人员的内部小组，安全性要求不高，重置后由用户重新登录即可；
// 若确需立即踢下线，可先停用再启用该账号（停用会让既有会话在下一次请求时立即失效）。
// 详见 backend/docs/B05-进度与验证记录.md 7.3。
func (s *Service) ResetPassword(
	ctx context.Context, userID, newPassword string, expectedUpdated time.Time,
	audit *apperr.AuditWrite,
) (domain.User, error) {
	if _, fields := domain.NormalizeNewPassword(newPassword, s.security.PasswordMinLength); len(fields) > 0 {
		return domain.User{}, domain.ErrValidation.WithFields(fields)
	}

	hash, err := s.hasher.Hash(newPassword)
	if err != nil {
		return domain.User{}, domain.ErrInternal.WithCause(err)
	}

	record, err := s.users.UpdatePasswordWithCAS(ctx, userID, hash, expectedUpdated, audit)
	if err != nil {
		return domain.User{}, err
	}
	return record.ToDomain(), nil
}

// BootstrapAdminInput 是系统初始化时录入引导管理员所需的字段。
//
// 全部来自配置（`security.bootstrap_admin_*`），不从 HTTP 进入：
// 本系统面向内部小组，普通用户不需要管理员角色，因此没有分发管理员的需求，
// 只需要在初始化时录入一个系统级后台账号。
type BootstrapAdminInput struct {
	Username    string
	Email       string
	Password    string
	DisplayName string
}

// BootstrapAdminResult 描述初始化实际做了什么，供启动日志使用。
type BootstrapAdminResult struct {
	UserID string
	// Created 为真表示本次新建了账号。
	Created bool
	// Promoted 为真表示账号已存在但被提升为系统管理员。
	Promoted bool
	// Disabled 为真表示目标账号处于停用状态：它无法登录，因此实际上仍不可用。
	// 这种情况**只告警不自动启用**——自动启用会与一次显式的人工停用相抵触，
	// 而"停用管理员"本身必须是有效且可见的决定。
	Disabled bool
}

// EnsureAdministrator 确保配置中指定的账号存在且是系统管理员。
//
// 这是**唯一不依赖 HTTP 的管理员初始化入口**。之所以必须有它：全部管理员端点
// 都要求调用者已是管理员，没有任何 HTTP 路径能造出第一个管理员。
//
// 幂等语义（必须在每次启动时安全地重复执行）：
//
//   - 账号不存在 ⇒ 按给定邮箱/密码/显示名创建，直接置为 admin 与 active；
//   - 账号已存在 ⇒ **只确保 system_role 为 admin**，绝不覆盖密码、邮箱或状态。
//     覆盖密码会让每次重启都把管理员改过的密码打回配置里的初始值，
//     这是一个比"忘记初始化"严重得多的事故。
//
// 因此 BootstrapAdminInput.Password 是一次性的：账号建好之后即可从环境中移除。
func (s *Service) EnsureAdministrator(
	ctx context.Context, in BootstrapAdminInput,
) (BootstrapAdminResult, error) {
	// 复用注册的归一化：用户名/邮箱格式、密码强度、时区默认值只有一份定义，
	// 配置层因此不需要再写一套校验。
	normalized, fields := domain.NormalizeRegistration(domain.RegistrationInput{
		Username:    in.Username,
		Email:       in.Email,
		DisplayName: displayNameOrUsername(in.DisplayName, in.Username),
		Password:    in.Password,
	}, s.security.PasswordMinLength)
	if len(fields) > 0 {
		// 错误里只有字段名与规则说明，不含密码内容。
		return BootstrapAdminResult{}, domain.ErrValidation.WithFields(fields)
	}

	existing, err := s.users.FindByUsername(ctx, normalized.Username)
	switch {
	case err == nil:
		// 已存在：只需要确保角色。
	case isNotFound(err):
		return s.createBootstrapAdmin(ctx, normalized)
	default:
		return BootstrapAdminResult{}, err
	}

	result := BootstrapAdminResult{
		UserID:   existing.ID,
		Disabled: !existing.ToDomain().IsActive(),
	}
	if existing.SystemRole == domain.UserSystemRoleAdmin {
		// 已经是管理员：什么都不做，连 updated_at 都不推进，
		// 这样"重复启动"不会让其他客户端手里的版本失效。
		s.logger.Info("引导管理员已就绪", slog.String("user_id", existing.ID))
		return result, nil
	}

	promoted, err := s.users.UpdateSystemRole(ctx, existing.ID, domain.UserSystemRoleAdmin)
	if err != nil {
		return BootstrapAdminResult{}, err
	}
	result.Promoted = true

	s.logger.Info("引导管理员已提升为系统管理员",
		slog.String("user_id", promoted.ID),
		slog.String("username", promoted.Username),
	)
	if result.Disabled {
		s.logger.Warn("引导管理员账号当前处于停用状态，在启用之前无法登录",
			slog.String("user_id", promoted.ID))
	}
	return result, nil
}

// createBootstrapAdmin 创建引导管理员账号。
func (s *Service) createBootstrapAdmin(
	ctx context.Context, normalized domain.RegistrationInput,
) (BootstrapAdminResult, error) {
	hash, err := s.hasher.Hash(normalized.Password)
	if err != nil {
		return BootstrapAdminResult{}, domain.ErrInternal.WithCause(err)
	}
	id, err := s.ids.UUID()
	if err != nil {
		return BootstrapAdminResult{}, domain.ErrInternal.WithCause(err)
	}

	record, err := s.users.CreateUser(ctx, NewUser{
		ID:              id,
		Username:        normalized.Username,
		Email:           normalized.Email,
		DisplayName:     normalized.DisplayName,
		PasswordHash:    hash,
		Timezone:        normalized.Timezone,
		Status:          domain.UserStatusActive,
		SystemRole:      domain.UserSystemRoleAdmin,
		ReminderEnabled: true,
	}, nil)
	if err != nil {
		return BootstrapAdminResult{}, err
	}

	s.logger.Info("引导管理员已创建",
		slog.String("user_id", record.ID),
		slog.String("username", record.Username),
	)
	// 明确提示这是**一次性**密码：账号已存在时配置里的密码不会再被使用。
	s.logger.Info("引导管理员密码为一次性初始化凭据，账号创建完成后建议从配置中移除")
	return BootstrapAdminResult{UserID: record.ID, Created: true}, nil
}

// displayNameOrUsername 在未提供显示名时回退到用户名。
func displayNameOrUsername(displayName, username string) string {
	if trimmed := strings.TrimSpace(displayName); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(username)
}
