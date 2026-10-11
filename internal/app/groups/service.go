package groups

import (
	"context"
	"log/slog"
	"time"

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
	FindGroupForUser(ctx context.Context, groupID, userID string) (GroupRecord, error)
	ListGroups(ctx context.Context, filter ListFilter, orderBy string, limit, offset int) ([]GroupRecord, error)
	CountGroups(ctx context.Context, filter ListFilter) (int, error)
	CreateGroupInTx(ctx context.Context, params CreateGroupParams) (GroupRecord, error)
	UpdateGroup(ctx context.Context, params GroupUpdateParams) (GroupRecord, error)

	FindMember(ctx context.Context, groupID, userID string) (MemberRecord, error)
	ListMembers(ctx context.Context, filter MemberFilter, orderBy string, limit, offset int) ([]MemberRecord, error)
	CountMembers(ctx context.Context, filter MemberFilter) (int, error)
	AddMember(ctx context.Context, id, groupID, userID, role string) (MemberRecord, error)
	UpdateMember(ctx context.Context, groupID, userID string, role, status *string) (MemberRecord, error)
	CountManagers(ctx context.Context, groupID string) (int, error)

	FindUser(ctx context.Context, userID string) (UserRecord, error)

	FindJoinRequest(ctx context.Context, groupID, userID string) (JoinRequestRecord, error)
	ListJoinRequests(ctx context.Context, groupID string, status *string, orderBy string, limit, offset int) ([]JoinRequestRecord, error)
	CountJoinRequests(ctx context.Context, groupID string, status *string) (int, error)
	CreateJoinRequest(ctx context.Context, id, groupID, userID string) (JoinRequestRecord, error)
	ReopenJoinRequest(ctx context.Context, groupID, userID string) (JoinRequestRecord, error)
	ReviewJoinRequestInTx(ctx context.Context, groupID, userID, reviewerID, decision, memberID string) (JoinRequestRecord, error)
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
}

// Options 是 Service 的构造参数。
type Options struct {
	Store  Store
	IDs    *idgen.Generator
	Clock  clock.Clock
	Logger *slog.Logger
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
	return &Service{repo: opts.Store, ids: opts.IDs, clk: clk, logger: logger}
}

// CreateInput 是创建工作组的用例输入。
type CreateInput struct {
	Name        string
	Description *string
	OwnerID     string
}

// Create 创建工作组的全部初始状态。
//
// 单一写路径（docs/02 §3.2.1）：groups.owner_id 与所有者的 group_members 行
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
// 非成员返回 404 而不是 403——否则会把"这个组存在"这一信息泄漏给任意登录用户。
//
// 注意所有者被移除成员关系的情况：按 docs/02 §3.2.1，成员关系不得降级/移除所有者，
// 因此这种状态只可能由直接改库产生。此时仍允许所有者读取自己的组，
// 避免把库内异常放大成"所有者看不到自己的组"。
func (s *Service) Detail(ctx context.Context, groupID, actorID string) (GroupRecord, error) {
	record, err := s.repo.FindGroupForUser(ctx, groupID, actorID)
	if err != nil {
		return GroupRecord{}, err
	}

	if record.OwnerID != actorID && !isActiveMember(record.Membership) {
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
}

// Update 修改工作组。
//
// 权限（需求文档 5.2）：所有者、组管理员可改；系统管理员可改。
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

	updated, err := s.repo.UpdateGroup(ctx, GroupUpdateParams{
		ID:              in.GroupID,
		Name:            normalized.Name,
		Description:     normalized.Description,
		DescriptionSet:  in.DescriptionSet,
		Status:          normalized.Status,
		ExpectedUpdated: in.ExpectedUpdated,
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
	if err != nil {
		return Page[MemberRecord]{}, err
	}

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

	member, err := s.repo.AddMember(ctx, memberID, group.ID, in.UserID, role)
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
// 核心不变式（docs/02 §3.2.1 保障 2）：不得降级或移除工作组所有者。
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

	updated, err := s.repo.UpdateMember(ctx, group.ID, in.UserID, in.Role, in.Status)
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
	updated, err := s.repo.UpdateMember(ctx, group.ID, userID, nil, &removed)
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

	created, err := s.repo.CreateJoinRequest(ctx, requestID, group.ID, in.ActorID)
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
	if err != nil {
		return Page[JoinRequestRecord]{}, err
	}

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
		group.ID, in.TargetUser, in.ActorID, in.Decision, memberID)
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
	reopened, err := s.repo.ReopenJoinRequest(ctx, groupID, userID)
	if err != nil {
		return JoinRequestRecord{}, err
	}

	s.logger.Info("加入申请已重新开启",
		slog.String("group_id", groupID),
		slog.String("user_id", userID),
	)
	return reopened, nil
}

// requireAccess 要求 actor 是该组的活跃成员或所有者，返回组记录。
func (s *Service) requireAccess(ctx context.Context, groupID, actorID string) (GroupRecord, error) {
	record, err := s.repo.FindGroupForUser(ctx, groupID, actorID)
	if err != nil {
		return GroupRecord{}, err
	}
	if record.OwnerID != actorID && !isActiveMember(record.Membership) {
		return GroupRecord{}, notFound()
	}
	return record, nil
}

// requireManage 要求 actor 具备成员管理权限，返回组记录。
func (s *Service) requireManage(ctx context.Context, groupID, actorID string) (GroupRecord, error) {
	record, err := s.repo.FindGroupForUser(ctx, groupID, actorID)
	if err != nil {
		return GroupRecord{}, err
	}
	if !domain.CanManageGroupMembers(record.ToDomain(), membershipOf(record), actorID) {
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
	if !domain.CanUpdateGroup(record.ToDomain(), actor, membershipOf(record)) {
		return GroupRecord{}, notFound()
	}
	return record, nil
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
func resolveMemberAction(target domain.GroupMember, role, status *string) (string, error) {
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
