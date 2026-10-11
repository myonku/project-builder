package users

import (
	"context"
	"log/slog"
	"time"

	"github.com/myonku/project-builder/internal/domain"
)

// Store 是本模块对持久化的需求。定义在消费方，测试用替身实现。
type Store interface {
	FindProfile(ctx context.Context, userID string) (ProfileRecord, error)
	SharesWorkspace(ctx context.Context, viewerID, targetID string) (bool, error)
	UpdateProfile(ctx context.Context, params UpdateProfileParams) (ProfileRecord, error)
}

// Service 承载用户资料用例。
type Service struct {
	repo   Store
	logger *slog.Logger
}

// Options 是 Service 的构造参数。
type Options struct {
	Store  Store
	Logger *slog.Logger
}

// NewService 构造用户资料用例服务。
func NewService(opts Options) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: opts.Store, logger: logger}
}

// Detail 返回目标用户的资料（接口契约 4.3 GET /users/{user_id}）。
func (s *Service) Detail(ctx context.Context, actor domain.User, targetID string) (ProfileRecord, error) {
	return s.requireVisible(ctx, actor, targetID)
}

// UpdateInput 是资料更新的用例输入。
//
// Update 使用指针字段：nil 表示"未提供，不修改"。
type UpdateInput struct {
	TargetID        string
	Update          domain.UserProfileUpdate
	ExpectedUpdated time.Time
}

// Update 执行用户资料更新（接口契约 4.3 PATCH /users/{user_id}）。
//
// 处理顺序遵循契约 5：字段校验 → 对象存在性与可见性（404）→ 权限（403）
// → 版本与领域规则 → 事务写入。
func (s *Service) Update(ctx context.Context, actor domain.User, in UpdateInput) (ProfileRecord, error) {
	normalized, fields := domain.NormalizeUserProfileUpdate(in.Update)
	if len(fields) > 0 {
		return ProfileRecord{}, domain.ErrValidation.WithFields(fields)
	}

	// 自检：归一化不得静默丢弃调用方提供的字段。
	// 这类缺陷不报错、不返回 5xx，只是一个字段没生效（docs/06 5.5 第 5 条）。
	assertNotDropped("display_name", in.Update.DisplayName != nil, normalized.DisplayName != nil)
	assertNotDropped("email", in.Update.Email != nil, normalized.Email != nil)
	assertNotDropped("timezone", in.Update.Timezone != nil, normalized.Timezone != nil)
	assertNotDropped("reminder_enabled", in.Update.ReminderEnabled != nil, normalized.ReminderEnabled != nil)
	assertNotDropped("status", in.Update.Status != nil, normalized.Status != nil)
	assertNotDropped("system_role", in.Update.SystemRole != nil, normalized.SystemRole != nil)

	// 只有 expected_updated_at 的请求被拒绝：它不会改变任何数据，
	// 却会推进 updated_at，让其他客户端手里的版本无谓失效。
	if !normalized.HasChanges() {
		return ProfileRecord{}, domain.ErrValidation.
			WithField("body", "至少需要提供一个可修改字段（display_name / email / timezone / reminder_enabled / status / system_role）")
	}

	if _, err := s.requireVisible(ctx, actor, in.TargetID); err != nil {
		return ProfileRecord{}, err
	}

	if !domain.CanUpdateUserProfile(actor, in.TargetID) {
		return ProfileRecord{}, domain.ErrPermissionDenied
	}

	// 状态与系统角色属于管理动作：普通用户即使是本人也不能改，
	// 因此这里给出带 fields 的 403，而不是笼统的"无权"。
	if domain.RequiresUserAdministration(normalized) && !domain.CanAdministerUsers(actor) {
		return ProfileRecord{}, adminOnlyFieldsError(normalized)
	}

	updated, err := s.repo.UpdateProfile(ctx, UpdateProfileParams{
		ID:              in.TargetID,
		ExpectedUpdated: in.ExpectedUpdated,
		DisplayName:     normalized.DisplayName,
		Email:           normalized.Email,
		Timezone:        normalized.Timezone,
		ReminderEnabled: normalized.ReminderEnabled,
		Status:          normalized.Status,
		SystemRole:      normalized.SystemRole,
		// 只有触及状态或角色的写入才需要守住"至少一个启用中管理员"，
		// 避免为一次改显示名的请求加行锁。
		GuardLastAdmin: domain.RequiresUserAdministration(normalized),
	})
	if err != nil {
		return ProfileRecord{}, err
	}

	s.logger.Info("用户资料已更新",
		slog.String("actor_id", actor.ID),
		slog.String("user_id", updated.ID),
		slog.String("status", updated.Status),
		slog.String("system_role", updated.SystemRole),
	)
	return updated, nil
}

// DisableInput 是停用用户的用例输入。
type DisableInput struct {
	TargetID        string
	ExpectedUpdated time.Time
}

// Disable 停用用户（接口契约 4.3 POST /users/{user_id}/disable）。
//
// 停用是**状态变更而非删除**：users 行保留，历史数据与操作记录不受影响，
// 也不会因其历史对象上的 ON DELETE RESTRICT 外键而失败（需求文档 5.1）。
// 停用后该用户的既有会话在下一次请求时立即失效：会话有效性每次请求
// 都回数据库读取用户状态（契约 2.2），不需要会话失效广播。
func (s *Service) Disable(ctx context.Context, actor domain.User, in DisableInput) (ProfileRecord, error) {
	// 仅系统管理员。先判权限再查对象：非管理员无论目标是否存在都得到同一个 403，
	// 因此这个端点不会变成"用户是否存在"的探测接口。
	if !domain.CanAdministerUsers(actor) {
		return ProfileRecord{}, domain.ErrPermissionDenied
	}

	current, err := s.repo.FindProfile(ctx, in.TargetID)
	if err != nil {
		return ProfileRecord{}, err
	}

	// 已经是停用状态时拒绝，而不是"成功但没有变更"。
	// 理由与拒绝空更新相同：不让一次无变更的请求推进 updated_at。
	if current.Status == domain.UserStatusDisabled {
		return ProfileRecord{}, domain.ErrConflict.
			WithField("status", "该用户已处于停用状态")
	}

	disabled := domain.UserStatusDisabled
	updated, err := s.repo.UpdateProfile(ctx, UpdateProfileParams{
		ID:              in.TargetID,
		ExpectedUpdated: in.ExpectedUpdated,
		Status:          &disabled,
		GuardLastAdmin:  true,
	})
	if err != nil {
		return ProfileRecord{}, err
	}

	s.logger.Info("用户已被停用",
		slog.String("actor_id", actor.ID),
		slog.String("user_id", updated.ID),
	)
	return updated, nil
}

// TimezoneOf 返回用户用于计算业务日期的 IANA 时区。
//
// 这是给**装配点**使用的适配入口：projects / tasks 各自声明自己需要的最小接口，
// 由装配点把本方法注入它们。这样"用户时区从哪里来"只由本模块回答，
// 而两个消费方不必互相依赖，也不必知道 users 表的结构。
func (s *Service) TimezoneOf(ctx context.Context, userID string) (string, error) {
	record, err := s.repo.FindProfile(ctx, userID)
	if err != nil {
		return "", err
	}
	return domain.TimezoneOrDefault(record.Timezone, "UTC"), nil
}

// requireVisible 返回目标用户资料；用户不存在或对 actor 不可见时返回 user_not_found。
//
// 不可见与不存在使用**同一个**错误码：若不可见返回 403，任意登录用户就能用
// 状态码差异枚举出系统里有哪些账号。这与工作组模块的处理方式一致。
func (s *Service) requireVisible(ctx context.Context, actor domain.User, targetID string) (ProfileRecord, error) {
	record, err := s.repo.FindProfile(ctx, targetID)
	if err != nil {
		return ProfileRecord{}, err
	}

	// 本人与系统管理员无需再查协作范围，省一次查询。
	if actor.IsAdmin() || actor.ID == targetID {
		return record, nil
	}

	shared, err := s.repo.SharesWorkspace(ctx, actor.ID, targetID)
	if err != nil {
		return ProfileRecord{}, err
	}
	if !domain.CanReadUserProfile(actor, targetID, shared) {
		return ProfileRecord{}, notFound()
	}
	return record, nil
}

// adminOnlyFieldsError 构造"普通用户触碰管理员字段"的 403，并逐字段说明原因。
func adminOnlyFieldsError(update domain.UserProfileUpdate) error {
	fields := map[string]string{}
	if update.Status != nil {
		fields["status"] = "只有系统管理员可以修改用户状态"
	}
	if update.SystemRole != nil {
		fields["system_role"] = "只有系统管理员可以修改系统角色"
	}
	return domain.ErrPermissionDenied.WithFields(fields)
}

// assertNotDropped 断言归一化步骤没有静默丢弃调用方提供的字段。
//
// panic 是刻意的：它只可能在开发/测试期被触发（属于编程错误），
// 生产环境不会因为用户输入而进入该分支。
func assertNotDropped(field string, provided, survived bool) {
	if provided && !survived {
		panic("users: 归一化丢弃了已提供的字段 " + field + "，请检查 NormalizeUserProfileUpdate 的调用参数")
	}
}

// 编译期确认接口实现。
var _ Store = (*Repository)(nil)
