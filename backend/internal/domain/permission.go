package domain

// 本文件集中定义跨模块共用的稳定机器值与权限基线。
//
// 约定（需求文档 4.x、接口文档 3.x）：
//   - 枚举值使用稳定英文机器值，数据库以 CHECK 约束保护；
//   - 权限与领域规则只能在 domain / app 判定，不得在 handler 里复制；
//   - "组内可见"不等于"可编辑"。

// 项目状态。
const (
	ProjectStatusNotStarted = "not_started"
	ProjectStatusInProgress = "in_progress"
	ProjectStatusPaused     = "paused"
	ProjectStatusCompleted  = "completed"
	ProjectStatusArchived   = "archived"
	ProjectStatusCancelled  = "cancelled"
)

// 项目共享级别。
const (
	ProjectVisibilityPrivate       = "private"
	ProjectVisibilityGroupReadOnly = "group_readonly"
	ProjectVisibilityGroupEditable = "group_editable"
)

// 任务状态。注意：逾期（overdue）不是持久化状态，见数据库模型文档 3.7。
const (
	TaskStatusNotStarted = "not_started"
	TaskStatusInProgress = "in_progress"
	TaskStatusCompleted  = "completed"
	TaskStatusCancelled  = "cancelled"
)

// 成员角色。
const (
	MemberRoleOwner   = "owner"
	MemberRoleMember  = "member"
	MemberRoleManager = "manager"
)

// 成员状态。
const (
	MemberStatusActive  = "active"
	MemberStatusRemoved = "removed"
)

// 工作组状态。
const (
	GroupStatusActive   = "active"
	GroupStatusDisabled = "disabled"
)

// 加入申请状态。
const (
	JoinRequestPending  = "pending"
	JoinRequestApproved = "approved"
	JoinRequestRejected = "rejected"
)

// TaskStatuses / ProjectStatuses 等白名单用于排序与筛选字段校验，
// 以及确保不把中文文案写进数据库。
var (
	ProjectStatuses = []string{
		ProjectStatusNotStarted, ProjectStatusInProgress, ProjectStatusPaused,
		ProjectStatusCompleted, ProjectStatusArchived, ProjectStatusCancelled,
	}
	ProjectVisibilities = []string{
		ProjectVisibilityPrivate, ProjectVisibilityGroupReadOnly, ProjectVisibilityGroupEditable,
	}
	TaskStatuses = []string{
		TaskStatusNotStarted, TaskStatusInProgress, TaskStatusCompleted, TaskStatusCancelled,
	}
	MemberRoles    = []string{MemberRoleOwner, MemberRoleMember}
	MemberStatuses = []string{MemberStatusActive, MemberStatusRemoved}
	// UserStatuses / UserSystemRoles 与 ck_users_user_status /
	// ck_users_user_system_role 两个 CHECK 约束一一对应（backend/docs/B02 3.1）。
	UserStatuses    = []string{UserStatusActive, UserStatusDisabled}
	UserSystemRoles = []string{UserSystemRoleMember, UserSystemRoleAdmin}
)

// 任务层级上限（需求文档 5.4：父子任务最大 3 层）。
const TaskMaxDepth = 3

// Permission 是"某个主体对某个对象可以做什么"的抽象。
//
// Phase 1 只落地用户自身的权限；项目/任务/组的判定在后续阶段实现，
// 此处先固定调用约定，避免后续在各 handler 里各自为政。
type Permission int

const (
	// PermNone 无任何权限。
	PermNone Permission = iota
	// PermRead 可读。
	PermRead
	// PermWrite 可写。
	PermWrite
	// PermManage 可管理（含成员与权限变更）。
	PermManage
)

// String 返回权限的可读名称，仅用于日志。
func (p Permission) String() string {
	switch p {
	case PermNone:
		return "none"
	case PermRead:
		return "read"
	case PermWrite:
		return "write"
	case PermManage:
		return "manage"
	default:
		return "unknown"
	}
}

// AtLeast 报告 p 是否达到 required 要求的级别。
func (p Permission) AtLeast(required Permission) bool { return p >= required }

// CanManageProjectVisibility 报告某个共享级别是否允许组内成员直接修改项目共享设置。
//
// 需求文档 4.3：组内可编辑允许修改共享任务，但不能修改项目共享权限。
// 因此共享级别本身永远不授予 PermManage。
func CanManageProjectVisibility(visibility string) bool { return false }

// CanReadUserProfile 报告 actor 是否可以读取目标用户的资料。
//
// sharesWorkspace 由仓储计算（请求者与目标用户是否同属一个工作组或同一项目），
// 因为该判断需要 JOIN 才能完成；而"本人 / 系统管理员"是纯权限语义，留在领域层。
//
// 把这个布尔量作为参数而不是在领域层查库，是为了让规则保持纯函数、可表驱动测试。
func CanReadUserProfile(actor User, targetUserID string, sharesWorkspace bool) bool {
	if actor.ID == "" || targetUserID == "" {
		return false
	}
	if actor.IsAdmin() || actor.ID == targetUserID {
		return true
	}
	return sharesWorkspace
}

// CanUpdateUserProfile 报告 actor 是否可以修改目标用户的资料。
//
// 仅本人与系统管理员。同组成员只获得读取权限，不获得修改权限——
// 否则任何人都能改同事的邮箱，而邮箱是登录标识。
func CanUpdateUserProfile(actor User, targetUserID string) bool {
	if actor.ID == "" || targetUserID == "" {
		return false
	}
	return actor.IsAdmin() || actor.ID == targetUserID
}

// RequiresUserAdministration 报告给定更新是否触及只有系统管理员才能修改的字段。
//
// 与 CanUpdateUserProfile 分开是为了给出准确的错误语义：
// "本人改自己的状态"不是无权访问，而是该字段不对普通用户开放，
// 因此需要 403 + fields 说明，而不是笼统的 403。
func RequiresUserAdministration(update UserProfileUpdate) bool {
	return update.Status != nil || update.SystemRole != nil
}

// ProjectVisibleToGroup 报告给定共享级别是否让项目进入组内视图。
func ProjectVisibleToGroup(visibility string) bool {
	return visibility == ProjectVisibilityGroupReadOnly || visibility == ProjectVisibilityGroupEditable
}

// SelfPermission 返回用户对自身资源的权限。
//
// 规则：本人可读写自身资料；但"修改系统角色 / 停用用户"属于系统管理员权限，
// 必须显式检查 User.IsAdmin()，不得由此函数授权。
func SelfPermission(actorID, targetUserID string) Permission {
	if actorID == "" || actorID != targetUserID {
		return PermNone
	}
	return PermWrite
}

// CanAdministerUsers 报告主体是否可以执行用户管理动作（停用、改系统角色）。
func CanAdministerUsers(actor User) bool { return actor.IsAdmin() }

// CanReadAuditLog 报告主体是否可以查看全局操作记录。
//
// 只有系统管理员：需求文档 §4.1 把"查看全局操作记录"列为系统管理员的职责，
// 组管理员不扩权（人工决策 2026-10-12，见 backend/docs/B05 7.3 第 7 项）。
// 与其它"管理员放行"一样，这里的判定必须由用例层显式调用，
// 不得在 handler 里复制（backend/docs/B01 §5.9）。
func CanReadAuditLog(actor User) bool { return actor.IsAdmin() }
