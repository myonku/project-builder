package groups

import (
	"context"
	"log/slog"
	"time"

	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/listquery"
	"github.com/myonku/project-builder/internal/platform/clock"
	"github.com/myonku/project-builder/internal/platform/idgen"
)

// Store 是本模块对持久化的需求。定义在消费方，测试用替身实现。
//
// 命名为 Store 而不是 Repository：本包内已有 Repository 结构体（SQL 实现），
// 两者同名会让所有引用处产生歧义。
type Store interface {
	CountDiscoverGroups(context.Context, string, string) (int, error)
	ListDiscoverGroups(context.Context, string, string, string, int, int) ([]DiscoverGroup, error)
	CountMyJoinRequests(context.Context, string, *string) (int, error)
	ListMyJoinRequests(context.Context, string, *string, string, int, int) ([]MyJoinRequest, error)
	FindGroupForUser(ctx context.Context, groupID, userID string) (GroupRecord, error)
	ListGroups(ctx context.Context, filter ListFilter, orderBy string, limit, offset int) ([]GroupRecord, error)
	CountGroups(ctx context.Context, filter ListFilter) (int, error)
	CreateGroupInTx(ctx context.Context, params CreateGroupParams) (GroupRecord, error)
	UpdateGroup(ctx context.Context, params GroupUpdateParams) (GroupRecord, error)
	// TransferOwnership 在同一事务内改写 groups.owner_id 与目标成员的 manager 角色。
	TransferOwnership(ctx context.Context, params TransferOwnershipParams) (GroupRecord, error)

	FindMember(ctx context.Context, groupID, userID string) (MemberRecord, error)
	ListMembers(ctx context.Context, filter MemberFilter, orderBy string, limit, offset int) ([]MemberRecord, error)
	CountMembers(ctx context.Context, filter MemberFilter) (int, error)
	// AddMember/UpdateMember 的 audit 由用例层构造，在同一事务内落库（《A09》）。
	AddMember(ctx context.Context, id, groupID, userID, role string, audit *apperr.AuditWrite) (MemberRecord, error)
	UpdateMember(ctx context.Context, groupID, userID string, role, status *string, audit *apperr.AuditWrite) (MemberRecord, error)
	CountManagers(ctx context.Context, groupID string) (int, error)

	FindUser(ctx context.Context, userID string) (UserRecord, error)

	FindJoinRequest(ctx context.Context, groupID, userID string) (JoinRequestRecord, error)
	ListJoinRequests(ctx context.Context, groupID string, status *string, orderBy string, limit, offset int) ([]JoinRequestRecord, error)
	CountJoinRequests(ctx context.Context, groupID string, status *string) (int, error)
	CreateJoinRequest(ctx context.Context, id, groupID, userID string, audit *apperr.AuditWrite) (JoinRequestRecord, error)
	ReopenJoinRequest(ctx context.Context, groupID, userID string, audit *apperr.AuditWrite) (JoinRequestRecord, error)
	ReviewJoinRequestInTx(ctx context.Context, groupID, userID, reviewerID, decision, memberID string, audit *apperr.AuditWrite) (JoinRequestRecord, error)
}

// Page 是列表查询结果。
//
// 必须同时返回 Items 与 Total：接口契约 2.3 的 pagination.total 需要真实总数，
// 只返回分页参数会让响应永远报告 total=0。
type Page[T any] struct {
	Items []T
	Total int
	Page  listquery.Pagination
}

// NewPage 构造列表结果。
func NewPage[T any](items []T, total int, pagination listquery.Pagination) Page[T] {
	return Page[T]{Items: items, Total: total, Page: pagination}
}

// Service 承载工作组用例。
type Service struct {
	repo   Store
	ids    *idgen.Generator
	clk    clock.Clock
	logger *slog.Logger
	// audit 是操作记录的写入方：**由仓储在业务事务里调用**（见 apperr.AuditWrite）。
	audit apperr.AuditWriter
}

// Options 是 Service 的构造参数。
type Options struct {
	Store  Store
	IDs    *idgen.Generator
	Clock  clock.Clock
	Logger *slog.Logger
	// Audit 为 nil 时不记操作记录（单元测试或不需要审计的装配）。
	Audit apperr.AuditWriter
}

// NewService 构造工作组用例服务。
func NewService(opts Options) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	clk := opts.Clock
	if clk == nil {
		clk = clock.NewSystem()
	}
	return &Service{
		repo:   opts.Store,
		ids:    opts.IDs,
		clk:    clk,
		logger: logger,
		audit:  opts.Audit,
	}
}

// auditWith 构造一条工作组操作记录。
//
// 集中一处构造有三个好处：对象类型不会写错（都是 group）、动作与对象 id 不会各写一遍、
// changes 的组装有统一入口。成员变更也记在**容器**（工作组）上，而不是成员行 id
// （《A09》）。request id 由 context 提供，这里不重复传递。
func (s *Service) auditWith(
	actorID string, action domain.AuditAction, groupID string, changes map[string]string,
) *apperr.AuditWrite {
	return apperr.NewAuditWrite(s.audit, domain.AuditEntry{
		ActorID:    actorID,
		Action:     action,
		ObjectType: domain.AuditObjectGroup,
		ObjectID:   groupID,
		Changes:    changes,
	})
}

// groupChanges 汇总本次更新**实际提供**的字段（未提供的字段不出现）。
//
// 只记新值：契约要求"关键变更内容"，不要求完整前后对照；
// 完整前后值会把更多内容留在只增不删的表里。描述不进审计正文，只留"变过"这一事实。
func groupChanges(in UpdateInput) map[string]string {
	changes := map[string]string{}
	if in.Name != nil {
		changes["name"] = *in.Name
	}
	if in.DescriptionSet {
		changes["description"] = "<由请求提供>"
	}
	if in.Status != nil {
		changes["status"] = *in.Status
	}
	return changes
}

// memberChangeAudit 按领域动作选择要记的动作。
//
// "置为 removed" 与 DELETE 端点都是软移除，统一记 member_removed；
// 恢复活跃没有独立动作，记 member_added（成员关系再次生效）。
func (s *Service) memberChangeAudit(
	in MemberChangeInput, action, currentRole string,
) *apperr.AuditWrite {
	switch action {
	case domain.GroupMemberActionRemove:
		return s.auditWith(in.ActorID, domain.AuditActionMemberRemoved, in.GroupID,
			map[string]string{"user_id": in.UserID})
	case domain.GroupMemberActionRestore:
		return s.auditWith(in.ActorID, domain.AuditActionMemberAdded, in.GroupID,
			map[string]string{"user_id": in.UserID, "role": currentRole})
	default:
		// 提升/降级：此时 in.Role 必定非 nil（见 resolveMemberAction）。
		role := currentRole
		if in.Role != nil {
			role = *in.Role
		}
		return s.auditWith(in.ActorID, domain.AuditActionMemberRoleChanged, in.GroupID,
			map[string]string{"user_id": in.UserID, "role": role})
	}
}

// CreateInput 是创建工作组的用例输入。
type CreateInput struct {
	Name        string
	Description *string
	OwnerID     string
}

// Create 创建工作组的全部初始状态。
//
// 单一写路径（backend/docs/B02 §3.2.1）：groups.owner_id 与所有者的 group_members 行
// 在同一个事务内写入，返回后的组必然"有所有者且所有者是活跃 manager"。
// 客户端不得分别调用两个接口来达成这一点。
func (s *Service) Create(ctx context.Context, in CreateInput) (GroupRecord, error) {
	normalized, fields := domain.NormalizeGroupCreate(domain.GroupMembershipInput{
		Name:        in.Name,
		Description: in.Description,
	})
	if len(fields) > 0 {
		return GroupRecord{}, domain.ErrValidation.WithFields(fields)
	}

	groupID, err := s.ids.UUID()
	if err != nil {
		return GroupRecord{}, domain.ErrInternal.WithCause(err)
	}
	memberID, err := s.ids.UUID()
	if err != nil {
		return GroupRecord{}, domain.ErrInternal.WithCause(err)
	}

	created, err := s.repo.CreateGroupInTx(ctx, CreateGroupParams{
		ID:          groupID,
		Name:        normalized.Name,
		Description: normalized.Description,
		MemberID:    memberID,
		OwnerID:     in.OwnerID,
		Audit: s.auditWith(in.OwnerID, domain.AuditActionCreated, groupID,
			map[string]string{"name": normalized.Name}),
	})
	if err != nil {
		return GroupRecord{}, err
	}

	s.logger.Info("工作组已创建",
		slog.String("group_id", created.ID),
		slog.String("owner_id", in.OwnerID),
	)
	return created, nil
}

// Detail 返回工作组详情。
//
// 可见性规则：所有者或**活跃**成员可读。
// 非成员仍返回 404；Discover 仅开放启用组的基本资料，不扩大详情读取权限。
//
// 注意所有者被移除成员关系的情况：按 backend/docs/B02 §3.2.1，成员关系不得降级/移除所有者，
// 因此这种状态只可能由直接改库产生。此时仍允许所有者读取自己的组，
// 避免把库内异常放大成"所有者看不到自己的组"。
func (s *Service) Detail(ctx context.Context, groupID, actorID string) (GroupRecord, error) {
	record, err := s.repo.FindGroupForUser(ctx, groupID, actorID)
	if err != nil {
		return GroupRecord{}, err
	}

	if !domain.CanViewGroup(record.ToDomain(), actorOf(record, actorID), membershipOf(record)) {
		return GroupRecord{}, notFound()
	}
	return record, nil
}

// ListInput 是查询工作组的输入。
type ListInput struct {
	ActorID string
	Status  *string
	Page    listquery.PageRequest
	Sort    string
}

// List 查询当前用户所属工作组。
//
// 列表只返回"当前用户是成员"的组（含被停用的组，以便前端展示状态）。
func (s *Service) List(ctx context.Context, in ListInput) (Page[GroupRecord], error) {
	pagination, err := in.Page.Pagination()
	if err != nil {
		return Page[GroupRecord]{}, err
	}

	if in.Status != nil {
		status := *in.Status
		if !statusValid(status) {
			return Page[GroupRecord]{}, domain.ErrInvalidRequest.
				WithField("status", "状态只能是 active 或 disabled")
		}
	}

	order, err := GroupSort.Resolve(in.Sort)
	if err != nil {
		return Page[GroupRecord]{}, err
	}
	orderClause := GroupSort.OrderBy(order)

	filter := ListFilter{UserID: in.ActorID, Status: in.Status}

	total, err := s.repo.CountGroups(ctx, filter)
	if err != nil {
		return Page[GroupRecord]{}, err
	}
	records, err := s.repo.ListGroups(ctx, filter,
		orderClause, pagination.Limit(), pagination.Offset())
	if err != nil {
		return Page[GroupRecord]{}, err
	}
	return NewPage(records, total, pagination), nil
}

// UpdateInput 是修改工作组的用例输入。
type UpdateInput struct {
	GroupID         string
	ActorID         string
	Name            *string
	Description     *string
	DescriptionSet  bool
	Status          *string
	ExpectedUpdated time.Time
	// OwnerID 非 nil 时转移工作组所有权。
	//
	// 为什么放在这里而不是成员角色接口：groups.owner_id 是**工作组自身的治理属性**，
	// 不是 group_members 里的一个角色（backend/docs/B02 §3.2.1 的决策）。成员角色只有
	// member / manager 两种，把"所有者"塞进去会与那份决策冲突。
	OwnerID *string
}

// Update 修改工作组。
//
// 权限（需求文档 5.2）：所有者、组管理员可改；系统管理员可改。
// 但**转移所有权**只有当前所有者与系统管理员可以做：把组交给谁是治理决定，
// 不是日常成员管理。
//
// 版本冲突由数据库 CAS 判定（见 Repository.UpdateGroup）。
func (s *Service) Update(ctx context.Context, actor domain.User, in UpdateInput) (GroupRecord, error) {
	normalized, fields := domain.NormalizeGroupUpdate(domain.GroupUpdateInput{
		Name: in.Name,
		// 仅在显式提供描述时才参与校验。
		Description: descriptionForValidation(in.Description, in.DescriptionSet),
		Status:      in.Status,
	})
	if len(fields) > 0 {
		return GroupRecord{}, domain.ErrValidation.WithFields(fields)
	}

	current, err := s.repo.FindGroupForUser(ctx, in.GroupID, in.ActorID)
	if err != nil {
		return GroupRecord{}, err
	}

	if !domain.CanUpdateGroup(current.ToDomain(), actor, membershipOf(current)) {
		// 有权限要求但无权限：返回 403（对象存在性对组内成员不是秘密）。
		return GroupRecord{}, domain.ErrPermissionDenied
	}

	if in.OwnerID != nil {
		return s.transferOwnership(ctx, actor, current, in)
	}

	updated, err := s.repo.UpdateGroup(ctx, GroupUpdateParams{
		ID:              in.GroupID,
		Name:            normalized.Name,
		Description:     normalized.Description,
		DescriptionSet:  in.DescriptionSet,
		Status:          normalized.Status,
		ExpectedUpdated: in.ExpectedUpdated,
		// 只记本次实际提供的字段，避免把"没改的字段"写进只增不删的表。
		Audit: s.auditWith(in.ActorID, domain.AuditActionUpdated, in.GroupID, groupChanges(in)),
	})
	if err != nil {
		return GroupRecord{}, err
	}

	s.logger.Info("工作组已更新",
		slog.String("group_id", updated.ID),
		slog.String("actor_id", in.ActorID),
	)
	return updated, nil
}

// transferOwnership 转移工作组所有权。
//
// 前置条件（全部在写入前给出明确错误，而不是让数据库约束兜底）：
//   - 只有当前所有者或系统管理员可以发起（组管理员不行）；
//   - 目标必须是该组的**活跃成员**——所有者必须能管理组，而管理权限要求成员关系；
//   - 目标必须是有效用户（未停用），否则组会立刻进入"谁都改不了"的状态；
//   - 目标不能已经是所有者（那是"没有变化"，应返回 409 而不是假装成功）。
//
// 写入在同一事务内完成两件事：groups.owner_id 与目标成员的 manager 角色。
// 两者是同一事实的两种表示（backend/docs/B02 §3.2.1），必须同生共死——
// 否则会出现"是所有者但没有管理者成员行"，或反之。
func (s *Service) transferOwnership(
	ctx context.Context, actor domain.User, current GroupRecord, in UpdateInput,
) (GroupRecord, error) {
	target := *in.OwnerID

	if !domain.CanTransferGroupOwnership(current.ToDomain(), actor) {
		return GroupRecord{}, domain.ErrPermissionDenied
	}
	if target == current.OwnerID {
		return GroupRecord{}, domain.ErrConflict.
			WithField("owner_id", "该用户已经是工作组所有者")
	}

	membership, err := s.repo.FindMember(ctx, current.ID, target)
	switch {
	case err == nil:
	case apperr.IsNotFound(err):
		// 目标不是该组成员：这是请求本身的问题（422 + 字段明细），
		// 而不是"资源不存在"——组是存在且可见的，问题出在目标用户上。
		return GroupRecord{}, domain.ErrValidation.
			WithField("owner_id", "目标用户不是该工作组的成员")
	default:
		return GroupRecord{}, err
	}
	if !isActiveMember(&membership) {
		return GroupRecord{}, domain.ErrValidation.
			WithField("owner_id", "目标用户不是该工作组的活跃成员")
	}

	targetUser, err := s.repo.FindUser(ctx, target)
	if err != nil {
		return GroupRecord{}, err
	}
	if !targetUser.IsActive() {
		return GroupRecord{}, domain.ErrValidation.
			WithField("owner_id", "目标用户已被停用，不能成为工作组所有者")
	}

	updated, err := s.repo.TransferOwnership(ctx, TransferOwnershipParams{
		GroupID:         current.ID,
		NewOwnerID:      target,
		PreviousOwnerID: current.OwnerID,
		ExpectedUpdated: in.ExpectedUpdated,
		// 所有权转移同时改变两个人，因此把前任也记进 changes（契约 4.10）。
		Audit: s.auditWith(in.ActorID, domain.AuditActionOwnerTransferred, current.ID,
			map[string]string{"user_id": target, "previous_owner_id": current.OwnerID}),
	})
	if err != nil {
		return GroupRecord{}, err
	}

	s.logger.Info("工作组所有权已转移",
		slog.String("group_id", updated.ID),
		slog.String("previous_owner_id", current.OwnerID),
		slog.String("new_owner_id", target),
		slog.String("actor_id", in.ActorID),
	)
	return updated, nil
}

// MemberListInput 是查询成员的输入。
type MemberListInput struct {
	GroupID string
	ActorID string
	Role    *string
	Status  *string
	Page    listquery.PageRequest
	Sort    string
}

// ListMembers 查询工作组成员。
func (s *Service) ListMembers(ctx context.Context, in MemberListInput) (Page[MemberRecord], error) {
	pagination, err := in.Page.Pagination()
	if err != nil {
		return Page[MemberRecord]{}, err
	}

	if _, err := s.requireAccess(ctx, in.GroupID, in.ActorID); err != nil {
		return Page[MemberRecord]{}, err
	}

	if in.Role != nil && !contains(domain.MemberRoleManager, domain.MemberRoleMember)(*in.Role) {
		return Page[MemberRecord]{}, domain.ErrInvalidRequest.
			WithField("role", "角色只能是 member 或 manager")
	}
	if in.Status != nil && !contains(domain.MemberStatusActive, domain.MemberStatusRemoved)(*in.Status) {
		return Page[MemberRecord]{}, domain.ErrInvalidRequest.
			WithField("status", "成员状态只能是 active 或 removed")
	}

	order, err := MemberSort.Resolve(in.Sort)
	if err != nil {
		return Page[MemberRecord]{}, err
	}
	orderClause := MemberSort.OrderBy(order)

	filter := MemberFilter{GroupID: in.GroupID, Role: in.Role, Status: in.Status}

	total, err := s.repo.CountMembers(ctx, filter)
	if err != nil {
		return Page[MemberRecord]{}, err
	}
	records, err := s.repo.ListMembers(ctx, filter,
		orderClause, pagination.Limit(), pagination.Offset())
	if err != nil {
		return Page[MemberRecord]{}, err
	}
	return NewPage(records, total, pagination), nil
}

// AddMemberInput 是添加成员的输入。
type AddMemberInput struct {
	GroupID string
	ActorID string
	UserID  string
	Role    *string
}

// AddMember 由组管理员直接把用户加入工作组。
//
// 目标用户必须是有效用户。重复添加由 group_member_identity 保证返回 409。
func (s *Service) AddMember(ctx context.Context, in AddMemberInput) (MemberRecord, error) {
	group, err := s.requireManage(ctx, in.GroupID, in.ActorID)
	if err != nil {
		return MemberRecord{}, err
	}

	role := domain.MemberRoleMember
	if in.Role != nil {
		role = *in.Role
	}
	if role != domain.MemberRoleMember && role != domain.MemberRoleManager {
		return MemberRecord{}, domain.ErrValidation.
			WithField("role", "角色只能是 member 或 manager")
	}

	target, err := s.repo.FindUser(ctx, in.UserID)
	if err != nil {
		return MemberRecord{}, err
	}
	if !target.IsActive() {
		return MemberRecord{}, domain.ErrValidation.
			WithField("user_id", "目标用户已被停用，不能加入工作组")
	}

	memberID, err := s.ids.UUID()
	if err != nil {
		return MemberRecord{}, domain.ErrInternal.WithCause(err)
	}

	member, err := s.repo.AddMember(ctx, memberID, group.ID, in.UserID, role,
		s.auditWith(in.ActorID, domain.AuditActionMemberAdded, group.ID, map[string]string{
			"user_id": in.UserID,
			"role":    role,
		}))
	if err != nil {
		return MemberRecord{}, err
	}

	s.logger.Info("工作组成员已添加",
		slog.String("group_id", group.ID),
		slog.String("user_id", in.UserID),
		slog.String("actor_id", in.ActorID),
	)
	return member, nil
}

// MemberChangeInput 是修改成员角色或状态的输入。
type MemberChangeInput struct {
	GroupID string
	ActorID string
	UserID  string
	// Role 与 Status 至少提供一个。
	Role   *string
	Status *string
}

// ChangeMember 修改成员角色或状态。
//
// 核心不变式（backend/docs/B02 §3.2.1 保障 2）：不得降级或移除工作组所有者。
// 该判断在 domain.ValidateGroupMemberChange 中完成，此处只负责取出当前状态。
func (s *Service) ChangeMember(ctx context.Context, in MemberChangeInput) (MemberRecord, error) {
	group, err := s.requireManage(ctx, in.GroupID, in.ActorID)
	if err != nil {
		return MemberRecord{}, err
	}

	target, err := s.repo.FindMember(ctx, group.ID, in.UserID)
	if err != nil {
		return MemberRecord{}, err
	}

	// 把"角色或状态的变更"翻译为领域动作，复用同一套不变式校验。
	action, err := resolveMemberAction(target.ToDomain(), in.Role, in.Status)
	if err != nil {
		return MemberRecord{}, err
	}
	if err := domain.ValidateGroupMemberChange(group.ToDomain(), target.ToDomain(), action); err != nil {
		return MemberRecord{}, err
	}

	// 降级或移除管理者时，必须保证组内至少留有一个活跃管理者。
	if group.OwnerID != in.UserID && target.Role == domain.MemberRoleManager &&
		demotesManager(action) {
		managers, err := s.repo.CountManagers(ctx, group.ID)
		if err != nil {
			return MemberRecord{}, err
		}
		// 这里只统计 manager 角色；所有者本身不一定是 manager 行，
		// 因此要求"降级后仍有其它管理者"，由后续集成测试覆盖该边界。
		if managers <= 1 {
			return MemberRecord{}, domain.ErrConflict.
				WithField("user_id", "不能降级组内唯一的管理者")
		}
	}

	updated, err := s.repo.UpdateMember(ctx, group.ID, in.UserID, in.Role, in.Status,
		s.memberChangeAudit(in, action, target.Role))
	if err != nil {
		return MemberRecord{}, err
	}

	s.logger.Info("工作组成员已变更",
		slog.String("group_id", group.ID),
		slog.String("user_id", in.UserID),
		slog.String("actor_id", in.ActorID),
	)
	return updated, nil
}

// RemoveMember 移除成员（软移除，status 置为 removed）。
//
// 需求文档 5.2：成员移除后，个人数据保留；该用户不再看到原组共享数据。
func (s *Service) RemoveMember(ctx context.Context, groupID, actorID, userID string) (MemberRecord, error) {
	group, err := s.requireManage(ctx, groupID, actorID)
	if err != nil {
		return MemberRecord{}, err
	}

	target, err := s.repo.FindMember(ctx, group.ID, userID)
	if err != nil {
		return MemberRecord{}, err
	}

	if err := domain.ValidateGroupMemberChange(group.ToDomain(), target.ToDomain(),
		domain.GroupMemberActionRemove); err != nil {
		return MemberRecord{}, err
	}

	removed := domain.MemberStatusRemoved
	updated, err := s.repo.UpdateMember(ctx, group.ID, userID, nil, &removed,
		s.auditWith(actorID, domain.AuditActionMemberRemoved, group.ID,
			map[string]string{"user_id": userID}))
	if err != nil {
		return MemberRecord{}, err
	}

	s.logger.Info("工作组成员已移除",
		slog.String("group_id", group.ID),
		slog.String("user_id", userID),
		slog.String("actor_id", actorID),
	)
	return updated, nil
}

// JoinRequestInput 是提交加入申请的输入。
type JoinRequestInput struct {
	GroupID string
	ActorID string
}

// RequestJoin 提交加入申请。
//
// 规则（需求文档 5.2）：用户申请加入工作组后，必须由所有者审核通过。
// 重复申请返回 409；已是活跃成员也返回 409。
func (s *Service) RequestJoin(ctx context.Context, in JoinRequestInput) (JoinRequestRecord, error) {
	group, err := s.repo.FindGroupForUser(ctx, in.GroupID, in.ActorID)
	if err != nil {
		return JoinRequestRecord{}, err
	}
	if !group.ToDomain().IsActive() {
		return JoinRequestRecord{}, domain.ErrConflict.
			WithField("status", "工作组已停用，不能申请加入")
	}
	if isActiveMember(group.Membership) {
		return JoinRequestRecord{}, domain.ErrConflict.
			WithField("user_id", "已是该工作组成员")
	}

	// 已存在申请记录时给出精确冲突：待审核 → 重复；已拒绝 → 允许重新申请。
	// 数据库的 group_join_request_identity 不允许同一 (group,user) 有多行，
	// 因此"重新申请"走更新路径而不是插入。
	if existing, findErr := s.repo.FindJoinRequest(ctx, group.ID, in.ActorID); findErr == nil {
		switch existing.Status {
		case domain.JoinRequestPending:
			return JoinRequestRecord{}, domain.ErrConflict.
				WithField("user_id", domain.JoinRequestConflictMessage(existing.ToDomain()))
		case domain.JoinRequestApproved:
			// 已通过但成员关系不是 active：可能是被移除后重新申请，
			// 走与"已拒绝"相同的重新申请路径。
			fallthrough
		case domain.JoinRequestRejected:
			return s.reopenJoinRequest(ctx, group.ID, in.ActorID)
		default:
			return JoinRequestRecord{}, domain.ErrConflict.
				WithField("user_id", domain.JoinRequestConflictMessage(existing.ToDomain()))
		}
	}

	requestID, err := s.ids.UUID()
	if err != nil {
		return JoinRequestRecord{}, domain.ErrInternal.WithCause(err)
	}

	created, err := s.repo.CreateJoinRequest(ctx, requestID, group.ID, in.ActorID,
		s.auditWith(in.ActorID, domain.AuditActionCreated, group.ID, map[string]string{
			"user_id":         in.ActorID,
			"join_request_id": requestID,
		}))
	if err != nil {
		return JoinRequestRecord{}, err
	}

	s.logger.Info("加入申请已提交",
		slog.String("group_id", group.ID),
		slog.String("user_id", in.ActorID),
	)
	return created, nil
}

// JoinRequestListInput 是查询加入申请的输入。
type JoinRequestListInput struct {
	GroupID string
	ActorID string
	Status  *string
	Page    listquery.PageRequest
	Sort    string
}

// ListJoinRequests 查询加入申请。仅所有者、组管理员或系统管理员可查。
func (s *Service) ListJoinRequests(ctx context.Context, actor domain.User, in JoinRequestListInput) (Page[JoinRequestRecord], error) {
	pagination, err := in.Page.Pagination()
	if err != nil {
		return Page[JoinRequestRecord]{}, err
	}

	group, err := s.requireReviewer(ctx, actor, in.GroupID, in.ActorID)
	if err != nil {
		return Page[JoinRequestRecord]{}, err
	}

	if in.Status != nil && !contains(domain.JoinRequestPending, domain.JoinRequestApproved,
		domain.JoinRequestRejected)(*in.Status) {
		return Page[JoinRequestRecord]{}, domain.ErrInvalidRequest.
			WithField("status", "申请状态只能是 pending、approved 或 rejected")
	}

	order, err := JoinRequestSort.Resolve(in.Sort)
	if err != nil {
		return Page[JoinRequestRecord]{}, err
	}
	orderClause := JoinRequestSort.OrderBy(order)

	total, err := s.repo.CountJoinRequests(ctx, group.ID, in.Status)
	if err != nil {
		return Page[JoinRequestRecord]{}, err
	}
	records, err := s.repo.ListJoinRequests(ctx, group.ID, in.Status,
		orderClause, pagination.Limit(), pagination.Offset())
	if err != nil {
		return Page[JoinRequestRecord]{}, err
	}
	return NewPage(records, total, pagination), nil
}

// ReviewInput 是审核加入申请的输入。
type ReviewInput struct {
	GroupID    string
	ActorID    string
	TargetUser string
	Decision   string
}

// ReviewJoinRequest 审核加入申请。
//
// 通过时在同一事务内写入成员关系；重复审核返回 409（数据库 CAS + 领域校验双重保证）。
func (s *Service) ReviewJoinRequest(ctx context.Context, actor domain.User, in ReviewInput) (JoinRequestRecord, error) {
	group, err := s.requireReviewer(ctx, actor, in.GroupID, in.ActorID)
	if err != nil {
		return JoinRequestRecord{}, err
	}

	current, err := s.repo.FindJoinRequest(ctx, group.ID, in.TargetUser)
	if err != nil {
		return JoinRequestRecord{}, err
	}
	if err := domain.ValidateJoinRequestDecision(current.ToDomain(), in.Decision); err != nil {
		return JoinRequestRecord{}, err
	}

	memberID, err := s.ids.UUID()
	if err != nil {
		return JoinRequestRecord{}, domain.ErrInternal.WithCause(err)
	}

	reviewed, err := s.repo.ReviewJoinRequestInTx(ctx,
		group.ID, in.TargetUser, in.ActorID, in.Decision, memberID,
		s.auditWith(in.ActorID, domain.AuditActionJoinRequestReviewed, group.ID, map[string]string{
			"user_id":  in.TargetUser,
			"decision": in.Decision,
		}))
	if err != nil {
		return JoinRequestRecord{}, err
	}

	s.logger.Info("加入申请已审核",
		slog.String("group_id", group.ID),
		slog.String("user_id", in.TargetUser),
		slog.String("decision", in.Decision),
		slog.String("actor_id", in.ActorID),
	)
	return reviewed, nil
}

// reopenJoinRequest 把已拒绝（或被移除后重新申请）的申请重新置为 pending。
//
// 为什么不是插入新行：group_join_request_identity 对 (group_id, user_id) 唯一，
// 保留单行并翻转状态既满足"可重新申请"，又不破坏审计所需的唯一性。
func (s *Service) reopenJoinRequest(ctx context.Context, groupID, userID string) (JoinRequestRecord, error) {
	// 重新开启也是一次"申请已提交"，因此与首次提交记同一动作。
	reopened, err := s.repo.ReopenJoinRequest(ctx, groupID, userID,
		s.auditWith(userID, domain.AuditActionCreated, groupID,
			map[string]string{"user_id": userID}))
	if err != nil {
		return JoinRequestRecord{}, err
	}

	s.logger.Info("加入申请已重新开启",
		slog.String("group_id", groupID),
		slog.String("user_id", userID),
	)
	return reopened, nil
}

// requireAccess 要求 actor 是该组的活跃成员、所有者，或系统管理员，返回组记录。
func (s *Service) requireAccess(ctx context.Context, groupID, actorID string) (GroupRecord, error) {
	record, err := s.repo.FindGroupForUser(ctx, groupID, actorID)
	if err != nil {
		return GroupRecord{}, err
	}
	if !domain.CanViewGroup(record.ToDomain(), actorOf(record, actorID), membershipOf(record)) {
		return GroupRecord{}, notFound()
	}
	return record, nil
}

// requireManage 要求 actor 具备成员管理权限（所有者、组管理员或系统管理员），返回组记录。
func (s *Service) requireManage(ctx context.Context, groupID, actorID string) (GroupRecord, error) {
	record, err := s.repo.FindGroupForUser(ctx, groupID, actorID)
	if err != nil {
		return GroupRecord{}, err
	}
	if !domain.CanManageGroupMembers(record.ToDomain(), actorOf(record, actorID), membershipOf(record)) {
		return GroupRecord{}, notFound()
	}
	return record, nil
}

// requireReviewer 要求 actor 可以审核申请（所有者、组管理员或系统管理员）。
func (s *Service) requireReviewer(ctx context.Context, actor domain.User, groupID, actorID string) (GroupRecord, error) {
	record, err := s.repo.FindGroupForUser(ctx, groupID, actorID)
	if err != nil {
		return GroupRecord{}, err
	}
	// 这里直接用调用方传入的 actor：它来自会话解析，已经带有真实的 SystemRole。
	if !domain.CanUpdateGroup(record.ToDomain(), actor, membershipOf(record)) {
		return GroupRecord{}, notFound()
	}
	return record, nil
}

// actorOf 由记录中的系统角色重建领域用户。
//
// 存在的理由：本模块的权限入口大多只拿得到 actorID，而"系统管理员放行"
// 需要知道调用者的系统角色。查询时已把它一并取出（record.ActorSystemRole），
// 因此这里不需要额外查库。
//
// 必须走这一处，不要在别处写 domain.User{ID: actorID}——那样 SystemRole 恒为空，
// 管理员放行会静默失效（这正是引入本函数前的实际缺陷）。
func actorOf(record GroupRecord, actorID string) domain.User {
	return domain.User{ID: actorID, SystemRole: record.ActorSystemRole}
}

// notFound 返回统一的"工作组不存在或不可见"错误。
//
// 一律 404：非成员不应能区分"组不存在"与"组存在但自己无权"。
func notFound() error { return domain.New(domain.CodeGroupNotFound, "Group not found") }

// isActiveMember 报告成员关系是否为活跃状态。
func isActiveMember(member *MemberRecord) bool {
	return member != nil && member.Status == domain.MemberStatusActive
}

// membershipOf 从组记录取出领域成员关系，非成员时返回 nil。
func membershipOf(record GroupRecord) *domain.GroupMember {
	if record.Membership == nil {
		return nil
	}
	member := record.Membership.ToDomain()
	return &member
}

// statusValid 报告工作组状态是否合法。
func statusValid(status string) bool {
	return status == domain.GroupStatusActive || status == domain.GroupStatusDisabled
}

// descriptionForValidation 在未显式提供描述时返回 nil，避免把"未修改"误判为空值。
func descriptionForValidation(description *string, set bool) *string {
	if !set {
		return nil
	}
	return description
}

// contains 构造一个"取值必须属于集合"的判定函数。
func contains(allowed ...string) func(string) bool {
	return func(value string) bool {
		for _, candidate := range allowed {
			if candidate == value {
				return true
			}
		}
		return false
	}
}

// resolveMemberAction 把角色/状态的变更翻译为领域动作。
func resolveMemberAction(_ domain.GroupMember, role, status *string) (string, error) {
	if role != nil {
		switch *role {
		case domain.MemberRoleManager:
			return domain.GroupMemberActionPromote, nil
		case domain.MemberRoleMember:
			return domain.GroupMemberActionDemote, nil
		default:
			return "", domain.ErrValidation.WithField("role", "角色只能是 member 或 manager")
		}
	}
	if status != nil {
		switch *status {
		case domain.MemberStatusRemoved:
			return domain.GroupMemberActionRemove, nil
		case domain.MemberStatusActive:
			return domain.GroupMemberActionRestore, nil
		default:
			return "", domain.ErrValidation.
				WithField("status", "成员状态只能是 active 或 removed")
		}
	}
	return "", domain.ErrValidation.WithField("role", "必须提供 role 或 status")
}

// demotesManager 报告该动作是否会让目标失去管理者身份。
func demotesManager(action string) bool {
	return action == domain.GroupMemberActionDemote || action == domain.GroupMemberActionRemove
}
