package users

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/listquery"
)

// Store 是本模块对持久化的需求。定义在消费方，测试用替身实现。
type Store interface {
	FindProfile(ctx context.Context, userID string) (ProfileRecord, error)
	SharesWorkspace(ctx context.Context, viewerID, targetID string) (bool, error)
	UpdateProfile(ctx context.Context, params UpdateProfileParams) (ProfileRecord, error)
	ListProfiles(ctx context.Context, filter ListFilter, orderBy string, limit, offset int) ([]ProfileRecord, error)
	CountProfiles(ctx context.Context, filter ListFilter) (int, error)
}

// Accounts 是本模块对**账号级操作**的需求，由 app/auth 的用例服务实现。
//
// 为什么委托而不是自己写：
//
//  1. 管理员代建账号必须与用户自助注册**完全一致**（字段归一化、密码强度、
//     唯一性检查、约束冲突翻译）。复制一份实现迟早会分叉，而这几项都属于安全边界。
//  2. password_hash 的写入逻辑集中在 auth，避免两个模块各自维护同一列的写语义。
//
// 版本校验仍由本模块的端点负责：它是一次"对用户资源的版本化写入"，
// 因此通过参数传给 auth，而不是让 auth 去理解 HTTP 契约。
type Accounts interface {
	// CreateAccount 的 actorID 为空表示自助注册；非空表示管理员代建，
	// 由 auth 侧把该 ID 作为操作人写入操作记录（新账号 id 只有那里知道）。
	CreateAccount(ctx context.Context, in domain.RegistrationInput, actorID string) (domain.User, error)
	ResetPassword(ctx context.Context, userID, newPassword string, expectedUpdated time.Time, audit *apperr.AuditWrite) (domain.User, error)
}

// Page 是列表查询结果。必须同时返回真实总数（接口契约 2.3 的 pagination.total）。
type Page[T any] struct {
	Items []T
	Total int
	Page  listquery.Pagination
}

// Service 承载用户资料用例。
type Service struct {
	repo     Store
	accounts Accounts
	logger   *slog.Logger
	// audit 是操作记录的写入方，由仓储在业务事务里调用（见 apperr.AuditWrite）。
	audit apperr.AuditWriter
}

// Options 是 Service 的构造参数。
type Options struct {
	Store Store
	// Accounts 提供账号创建与密码重置，由装配点注入 app/auth 的服务。
	Accounts Accounts
	Logger   *slog.Logger
	// Audit 为 nil 时不记操作记录。
	Audit apperr.AuditWriter
}

// NewService 构造用户资料用例服务。
func NewService(opts Options) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		repo:     opts.Store,
		accounts: opts.Accounts,
		logger:   logger,
		audit:    opts.Audit,
	}
}

// auditWith 构造一条用户操作记录。
//
// object_id 是**被操作的用户**（不是操作人）：审计回答"这个账号上发生过什么"，
// 操作人由 ActorID 单独承载。
func (s *Service) auditWith(
	actorID string, action domain.AuditAction, targetUserID string, changes map[string]string,
) *apperr.AuditWrite {
	return apperr.NewAuditWrite(s.audit, domain.AuditEntry{
		ActorID:    actorID,
		Action:     action,
		ObjectType: domain.AuditObjectUser,
		ObjectID:   targetUserID,
		Changes:    changes,
	})
}

// CreateInput 是"管理员代建账号"的用例输入，字段与自助注册一致。
type CreateInput struct {
	Registration domain.RegistrationInput
}

// Create 由系统管理员创建一个账号（接口契约 4.3 POST /users）。
//
// 委托给 auth 的注册用例，因此行为与自助注册逐条一致：不自动登录、
// 不设置会话 Cookie、status 固定 active、system_role 固定 member、
// reminder_enabled 固定 true。
//
// "管理员不能借创建直接造出第二个管理员"是刻意的：升角色必须走
// PATCH /users/{id}，从而受"系统至少保留一个启用中管理员"的不变式约束，
// 也留下一次可审计的显式变更。
func (s *Service) Create(ctx context.Context, actor domain.User, in CreateInput) (ProfileRecord, error) {
	if !domain.CanAdministerUsers(actor) {
		return ProfileRecord{}, domain.ErrPermissionDenied
	}
	if s.accounts == nil {
		// 装配遗漏属于编程错误：快速失败，而不是返回"创建成功但没有账号"。
		return ProfileRecord{}, domain.ErrInternal.WithCause(errNoAccounts)
	}

	created, err := s.accounts.CreateAccount(ctx, in.Registration, actor.ID)
	if err != nil {
		return ProfileRecord{}, err
	}

	s.logger.Info("管理员创建了账号",
		slog.String("actor_id", actor.ID),
		slog.String("user_id", created.ID),
		slog.String("username", created.Username),
	)

	// 注册用例返回的是领域实体，不含 created_at / updated_at / last_login_at。
	// 回读一次以获得完整的对外表示；这是管理员端点，调用频率极低。
	return s.repo.FindProfile(ctx, created.ID)
}

// ListInput 是用户列表的用例输入。
type ListInput struct {
	IncludeDisabled bool
	Page            listquery.PageRequest
	Sort            string
}

// List 返回用户列表（接口契约 4.3 POST /users/query）。
//
// 仅系统管理员可用：普通成员能看到的应当是成员列表，而不是全系统账号清单。
func (s *Service) List(ctx context.Context, actor domain.User, in ListInput) (Page[ProfileRecord], error) {
	if !domain.CanAdministerUsers(actor) {
		return Page[ProfileRecord]{}, domain.ErrPermissionDenied
	}

	page, err := in.Page.Pagination()
	if err != nil {
		return Page[ProfileRecord]{}, err
	}
	order, err := UserSort.Resolve(in.Sort)
	if err != nil {
		return Page[ProfileRecord]{}, err
	}

	filter := ListFilter{IncludeDisabled: in.IncludeDisabled}

	total, err := s.repo.CountProfiles(ctx, filter)
	if err != nil {
		return Page[ProfileRecord]{}, err
	}
	items, err := s.repo.ListProfiles(ctx, filter, UserSort.OrderBy(order), page.Limit(), page.Offset())
	if err != nil {
		return Page[ProfileRecord]{}, err
	}
	return Page[ProfileRecord]{Items: items, Total: total, Page: page}, nil
}

// ResetPasswordInput 是重置密码的用例输入。
type ResetPasswordInput struct {
	TargetID        string
	NewPassword     string
	ExpectedUpdated time.Time
}

// ResetPassword 由系统管理员重置指定用户的密码
// （接口契约 4.3 POST /users/{user_id}/password）。
//
// 校验顺序：管理员权限（403）→ 目标存在性（404）→ auth 的密码强度与 CAS 写入
// （422 / 409）。先判权限再查对象，因此非管理员无论目标是否存在都得到同一个 403，
// 这个端点不会变成"用户是否存在"的探测接口。
//
// **已知限制**：本操作不使该用户已有的会话失效。原因与替代方案见
// backend/docs/B05-进度与验证记录.md 7.4 第 1 项。
func (s *Service) ResetPassword(
	ctx context.Context, actor domain.User, in ResetPasswordInput,
) (ProfileRecord, error) {
	if !domain.CanAdministerUsers(actor) {
		return ProfileRecord{}, domain.ErrPermissionDenied
	}
	if s.accounts == nil {
		return ProfileRecord{}, domain.ErrInternal.WithCause(errNoAccounts)
	}

	// 先确认目标存在，让 404 与版本冲突彻底分开。
	if _, err := s.repo.FindProfile(ctx, in.TargetID); err != nil {
		return ProfileRecord{}, err
	}

	// 密码本身绝不进 changes：只记"哪个管理员重置了哪个账号"。
	_, err := s.accounts.ResetPassword(ctx, in.TargetID, in.NewPassword, in.ExpectedUpdated,
		s.auditWith(actor.ID, domain.AuditActionPasswordReset, in.TargetID, nil))
	if err != nil {
		return ProfileRecord{}, err
	}

	// 只记录"哪个管理员重置了哪个用户的密码"，绝不记录密码本身或摘要。
	s.logger.Info("用户密码已被管理员重置",
		slog.String("actor_id", actor.ID),
		slog.String("user_id", in.TargetID),
	)

	return s.repo.FindProfile(ctx, in.TargetID)
}

// errNoAccounts 表示装配遗漏。
var errNoAccounts = errors.New("users: 未注入 Accounts（账号级操作）")

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
	// 这类缺陷不报错、不返回 5xx，只是一个字段没生效（backend/docs/B05 5.5 第 5 条）。
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
		Audit: s.auditWith(actor.ID, domain.AuditActionUpdated, in.TargetID,
			profileChanges(in.Update)),
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
		Audit: s.auditWith(actor.ID, domain.AuditActionDisabled, in.TargetID,
			map[string]string{"status": domain.UserStatusDisabled}),
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

// profileChanges 汇总本次资料更新**实际提供**的字段（未提供的字段不出现）。
//
// 只记新值：邮箱与显示名都属于"要知道改成了什么"的字段，
// 而密码与摘要从不经过这里（密码重置是另一条路径，且只记动作）。
func profileChanges(update domain.UserProfileUpdate) map[string]string {
	changes := map[string]string{}
	if update.DisplayName != nil {
		changes["display_name"] = *update.DisplayName
	}
	if update.Email != nil {
		changes["email"] = *update.Email
	}
	if update.Timezone != nil {
		changes["timezone"] = *update.Timezone
	}
	if update.ReminderEnabled != nil {
		changes["reminder_enabled"] = strconv.FormatBool(*update.ReminderEnabled)
	}
	if update.Status != nil {
		changes["status"] = *update.Status
	}
	if update.SystemRole != nil {
		changes["system_role"] = *update.SystemRole
	}
	return changes
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
