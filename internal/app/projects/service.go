package projects

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/listquery"
	"github.com/myonku/project-builder/internal/platform/clock"
	"github.com/myonku/project-builder/internal/platform/idgen"
)

// Store 是本模块对持久化的需求。定义在消费方，测试用替身实现。
type Store interface {
	FindProjectForUser(ctx context.Context, projectID, userID string, today time.Time) (ProjectRecord, error)
	ListProjects(ctx context.Context, filter ListFilter, orderBy string, limit, offset int) ([]ProjectRecord, error)
	CountProjects(ctx context.Context, filter ListFilter) (int, error)
	CreateProjectInTx(ctx context.Context, params CreateProjectParams) (ProjectRecord, error)
	UpdateProject(ctx context.Context, params ProjectUpdateParams) (ProjectRecord, error)
	ArchiveProject(ctx context.Context, projectID string, expectedUpdated, archivedAt time.Time) (ProjectRecord, error)

	FindMember(ctx context.Context, projectID, userID string) (MemberRecord, error)
	ListMembers(ctx context.Context, filter MemberFilter, orderBy string, limit, offset int) ([]MemberRecord, error)
	CountMembers(ctx context.Context, filter MemberFilter) (int, error)
	CountOwners(ctx context.Context, projectID string) (int, error)
	AddMember(ctx context.Context, id, projectID, userID, role string) (MemberRecord, error)
	UpdateMemberRole(ctx context.Context, projectID, userID, role string) (MemberRecord, error)
	// TransferOwnership 在同一事务内先降现任持有者、再升目标，保证"至多一个"与"至少一个"。
	TransferOwnership(ctx context.Context, projectID, targetUserID string) (MemberRecord, error)
	RemoveMember(ctx context.Context, projectID, userID string) error

	FindUser(ctx context.Context, userID string) (UserRecord, error)
	FindGroupForUser(ctx context.Context, groupID, userID string) (GroupRecord, error)
}

// Page 是列表查询结果。必须同时返回真实总数（接口契约 2.3 的 pagination.total）。
type Page[T any] struct {
	Items []T
	Total int
	Page  listquery.Pagination
}

// TimezoneProvider 按用户 ID 返回其 IANA 时区。
//
// 由装配点注入（生产环境是 app/users 的服务，测试环境是同一实现或替身）。
// 定义在消费方而不是 provider 侧，是本项目的惯例：模块只声明自己需要的最小能力，
// 因此 projects 不需要 import users，两个业务模块之间不产生依赖边。
type TimezoneProvider interface {
	TimezoneOf(ctx context.Context, userID string) (string, error)
}

// Service 承载项目用例。
type Service struct {
	repo      Store
	timezones TimezoneProvider
	ids       *idgen.Generator
	clk       clock.Clock
	logger    *slog.Logger
}

// Options 是 Service 的构造参数。
type Options struct {
	Store Store
	// Timezones 用于按请求者时区计算"今天"（派生逾期字段）。
	Timezones TimezoneProvider
	IDs       *idgen.Generator
	Clock     clock.Clock
	Logger    *slog.Logger
}

// NewService 构造项目用例服务。
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
		repo:      opts.Store,
		timezones: opts.Timezones,
		ids:       opts.IDs,
		clk:       clk,
		logger:    logger,
	}
}

// CreateInput 是创建项目的用例输入。
type CreateInput struct {
	Name        string
	Description *string
	GroupID     string
	Visibility  *string
	StartDate   time.Time
	EndDate     time.Time
	Color       *string
	CreatorID   string
}

// Create 创建项目与持有者成员关系。
//
// 需求文档 5.3：新项目默认为私有、未开始，创建者为持有者和成员。
// 创建者必须是归属工作组的活跃成员——否则会创建出"自己看不见的项目"。
func (s *Service) Create(ctx context.Context, in CreateInput) (ProjectRecord, error) {
	normalized, fields := domain.NormalizeProjectCreate(domain.ProjectInput{
		Name:        in.Name,
		Description: in.Description,
		GroupID:     in.GroupID,
		StartDate:   in.StartDate,
		EndDate:     in.EndDate,
		Visibility:  in.Visibility,
		Color:       in.Color,
	})
	// 日期区间倒置必须返回契约规定的 400 invalid_date_range，而不是 422。
	if dateErr := domain.ProjectDateRangeError(in.StartDate, in.EndDate); dateErr != nil {
		return ProjectRecord{}, dateErr
	}
	if len(fields) > 0 {
		return ProjectRecord{}, domain.ErrValidation.WithFields(fields)
	}

	// 归属组必须存在、可用，且创建者是活跃成员。
	group, err := s.repo.FindGroupForUser(ctx, normalized.GroupID, in.CreatorID)
	if err != nil {
		if isNotFound(err) {
			return ProjectRecord{}, domain.ErrValidation.
				WithField("group_id", "归属工作组不存在或不可见")
		}
		return ProjectRecord{}, err
	}
	if !group.ToDomain().IsActive() {
		return ProjectRecord{}, domain.ErrConflict.
			WithField("group_id", "归属工作组已停用，不能创建项目")
	}
	if group.OwnerID != in.CreatorID && !isActiveGroupMember(group.Membership) {
		return ProjectRecord{}, domain.ErrPermissionDenied.
			WithField("group_id", "创建者必须是归属工作组的活跃成员")
	}

	visibility := domain.ProjectVisibilityPrivate
	if normalized.Visibility != nil {
		visibility = *normalized.Visibility
	}

	projectID, err := s.ids.UUID()
	if err != nil {
		return ProjectRecord{}, domain.ErrInternal.WithCause(err)
	}
	memberID, err := s.ids.UUID()
	if err != nil {
		return ProjectRecord{}, domain.ErrInternal.WithCause(err)
	}

	created, err := s.repo.CreateProjectInTx(ctx, CreateProjectParams{
		ID:          projectID,
		Name:        normalized.Name,
		Description: normalized.Description,
		CreatedBy:   in.CreatorID,
		GroupID:     normalized.GroupID,
		Visibility:  visibility,
		StartDate:   normalized.StartDate,
		EndDate:     normalized.EndDate,
		Color:       normalized.Color,
		MemberID:    memberID,
	})
	if err != nil {
		return ProjectRecord{}, err
	}

	// 显式补上创建者的成员关系：仓储的 INSERT ... RETURNING 只返回 projects 表的列，
	// 不会带出刚写入的 project_members 行。若此处不补，创建响应里的 membership_role
	// 会是 null，而数据库里其实已经是 owner，前端据此判断会出错。
	// 这是"单一写路径"在返回值上的收尾，不是第二处业务判断。
	created.Membership = &MemberRecord{
		ID:        memberID,
		ProjectID: created.ID,
		UserID:    in.CreatorID,
		Role:      domain.MemberRoleOwner,
	}

	s.logger.Info("项目已创建",
		slog.String("project_id", created.ID),
		slog.String("group_id", created.GroupID),
		slog.String("creator_id", in.CreatorID),
	)
	return created, nil
}

// Detail 返回项目详情。
//
// 可见性：项目成员，或归属组的活跃成员且共享级别非私有。
// 不可见返回 404 而不是 403——私有项目的存在性不应泄漏给任意登录用户。
func (s *Service) Detail(ctx context.Context, projectID, actorID string) (ProjectRecord, error) {
	record, err := s.repo.FindProjectForUser(ctx, projectID, actorID, s.today(ctx, actorID))
	if err != nil {
		return ProjectRecord{}, err
	}
	if permissionOf(record, actorID) == domain.PermNone {
		return ProjectRecord{}, notFound()
	}
	return record, nil
}

// Access 返回 actor 对项目的权限，供其他模块（tasks）在编排时复用。
//
// 这样任务模块不需要自己复制一遍"项目成员 / 组内共享 / 归档只读"的判定。
func (s *Service) Access(ctx context.Context, projectID, actorID string) (ProjectRecord, domain.Permission, error) {
	record, err := s.repo.FindProjectForUser(ctx, projectID, actorID, s.today(ctx, actorID))
	if err != nil {
		return ProjectRecord{}, domain.PermNone, err
	}
	return record, permissionOf(record, actorID), nil
}

// ListInput 是查询项目的输入。
type ListInput struct {
	ActorID         string
	GroupID         *string
	Status          *string
	OwnerID         *string
	Name            *string
	IncludeArchived bool
	Page            listquery.PageRequest
	Sort            string
}

// List 查询可见项目。
func (s *Service) List(ctx context.Context, in ListInput) (Page[ProjectRecord], error) {
	pagination, err := in.Page.Pagination()
	if err != nil {
		return Page[ProjectRecord]{}, err
	}

	if in.Status != nil && !validProjectStatus(*in.Status) {
		return Page[ProjectRecord]{}, domain.ErrInvalidRequest.
			WithField("status", "项目状态取值非法")
	}
	if in.OwnerID != nil && !idgen.IsUUID(*in.OwnerID) {
		return Page[ProjectRecord]{}, domain.ErrInvalidRequest.
			WithField("owner_id", "必须是合法的 UUID 字符串")
	}
	if in.GroupID != nil && !idgen.IsUUID(*in.GroupID) {
		return Page[ProjectRecord]{}, domain.ErrInvalidRequest.
			WithField("group_id", "必须是合法的 UUID 字符串")
	}

	order, err := ProjectSort.Resolve(in.Sort)
	if err != nil {
		return Page[ProjectRecord]{}, err
	}
	orderClause := ProjectSort.OrderBy(order)
	if err != nil {
		return Page[ProjectRecord]{}, err
	}

	filter := ListFilter{
		UserID:          in.ActorID,
		GroupID:         in.GroupID,
		Status:          in.Status,
		OwnerID:         in.OwnerID,
		Name:            in.Name,
		IncludeArchived: in.IncludeArchived,
		Today:           s.today(ctx, in.ActorID),
	}

	total, err := s.repo.CountProjects(ctx, filter)
	if err != nil {
		return Page[ProjectRecord]{}, err
	}
	records, err := s.repo.ListProjects(ctx, filter,
		orderClause, pagination.Limit(), pagination.Offset())
	if err != nil {
		return Page[ProjectRecord]{}, err
	}
	return Page[ProjectRecord]{Items: records, Total: total, Page: pagination}, nil
}

// UpdateInput 是修改项目的用例输入。
type UpdateInput struct {
	ProjectID       string
	ActorID         string
	Name            *string
	Description     *string
	DescriptionSet  bool
	Status          *string
	Visibility      *string
	StartDate       *time.Time
	EndDate         *time.Time
	Color           *string
	ColorSet        bool
	ExpectedUpdated time.Time
}

// Update 修改项目。
//
// 权限：仅项目持有者可改（需求文档 4.3：组内可编辑允许修改共享任务，
// 但**不能**修改项目共享权限）。系统管理员可改。
//
// 归档项目只读：Python 版本此处有缺陷，可继续修改归档项目。
func (s *Service) Update(ctx context.Context, actor domain.User, in UpdateInput) (ProjectRecord, error) {
	normalized, fields := domain.NormalizeProjectUpdate(domain.ProjectUpdateInput{
		Name:        in.Name,
		Description: descriptionForValidation(in.Description, in.DescriptionSet),
		Status:      in.Status,
		Visibility:  in.Visibility,
		Color:       in.Color,
		// 日期字段必须一并传入：NormalizeProjectUpdate 会原样返回它们。
		// 早期版本漏传了这两个字段，导致"只改 end_date"的请求被静默忽略——
		// 返回 200、updated_at 推进、但 end_date 没有变化。这是最难发现的一类缺陷：
		// 没有报错、没有 5xx，只有一个字段没生效。
		StartDate: in.StartDate,
		EndDate:   in.EndDate,
	})
	if len(fields) > 0 {
		return ProjectRecord{}, domain.ErrValidation.WithFields(fields)
	}
	// 自检：归一化不得丢失调用方提供的字段。
	// 这类"字段被静默吞掉"的缺陷不会触发编译错误，只能靠断言拦住。
	assertNotDropped("start_date", in.StartDate != nil, normalized.StartDate != nil)
	assertNotDropped("end_date", in.EndDate != nil, normalized.EndDate != nil)

	current, err := s.repo.FindProjectForUser(ctx, in.ProjectID, in.ActorID, s.today(ctx, in.ActorID))
	if err != nil {
		return ProjectRecord{}, err
	}
	if !domain.CanManageProject(current.ToDomain(), actor, membershipOf(current)) {
		return ProjectRecord{}, domain.ErrPermissionDenied
	}

	// 日期区间必须用"合并后的最终值"校验：
	// 只改 end_date 时拿请求体里为空的 start_date 去比较会漏掉冲突。
	start := current.StartDate
	if normalized.StartDate != nil {
		start = *normalized.StartDate
	}
	end := current.EndDate
	if normalized.EndDate != nil {
		end = *normalized.EndDate
	}
	if err := domain.ValidateProjectDateRange(start, end); err != nil {
		var domainErr *domain.Error
		if errors.As(err, &domainErr) {
			return ProjectRecord{}, domainErr
		}
		return ProjectRecord{}, err
	}

	// 归档项目不可再编辑（除解除归档外）。
	if current.ToDomain().IsReadOnly() && !isUnarchive(normalized.Status) {
		return ProjectRecord{}, domain.ArchivedError()
	}

	updated, err := s.repo.UpdateProject(ctx, ProjectUpdateParams{
		ID:              in.ProjectID,
		Name:            normalized.Name,
		Description:     normalized.Description,
		DescriptionSet:  in.DescriptionSet,
		Status:          normalized.Status,
		Visibility:      normalized.Visibility,
		StartDate:       normalized.StartDate,
		EndDate:         normalized.EndDate,
		Color:           normalized.Color,
		ColorSet:        in.ColorSet,
		ExpectedUpdated: in.ExpectedUpdated,
	})
	if err != nil {
		return ProjectRecord{}, err
	}

	s.logger.Info("项目已更新",
		slog.String("project_id", updated.ID),
		slog.String("actor_id", in.ActorID),
	)
	return updated, nil
}

// ArchiveInput 是归档项目的用例输入。
type ArchiveInput struct {
	ProjectID       string
	ActorID         string
	ExpectedUpdated time.Time
}

// Archive 归档项目。
//
// 需求文档 5.3 + 接口契约 4.5：owner 或管理员归档项目。
// 归档后项目只读（由 domain.CanEditProjectTasks / IsReadOnly 保证）。
func (s *Service) Archive(ctx context.Context, actor domain.User, in ArchiveInput) (ProjectRecord, error) {
	current, err := s.repo.FindProjectForUser(ctx, in.ProjectID, in.ActorID, s.today(ctx, in.ActorID))
	if err != nil {
		return ProjectRecord{}, err
	}
	if !domain.CanManageProject(current.ToDomain(), actor, membershipOf(current)) {
		return ProjectRecord{}, domain.ErrPermissionDenied
	}
	if current.ToDomain().IsArchived() {
		return ProjectRecord{}, domain.ErrConflict.
			WithField("project_id", "项目已归档")
	}

	archived, err := s.repo.ArchiveProject(ctx, in.ProjectID, in.ExpectedUpdated, s.clk.Now())
	if err != nil {
		return ProjectRecord{}, err
	}

	s.logger.Info("项目已归档",
		slog.String("project_id", archived.ID),
		slog.String("actor_id", in.ActorID),
	)
	return archived, nil
}

// MemberListInput 是查询项目成员的输入。
type MemberListInput struct {
	ProjectID string
	ActorID   string
	Role      *string
	Page      listquery.PageRequest
	Sort      string
}

// ListMembers 查询项目成员。
func (s *Service) ListMembers(ctx context.Context, in MemberListInput) (Page[MemberRecord], error) {
	pagination, err := in.Page.Pagination()
	if err != nil {
		return Page[MemberRecord]{}, err
	}

	if _, err := s.requireVisible(ctx, in.ProjectID, in.ActorID); err != nil {
		return Page[MemberRecord]{}, err
	}
	if in.Role != nil && *in.Role != domain.MemberRoleOwner && *in.Role != domain.MemberRoleMember {
		return Page[MemberRecord]{}, domain.ErrInvalidRequest.
			WithField("role", "角色只能是 owner 或 member")
	}

	order, err := MemberSort.Resolve(in.Sort)
	if err != nil {
		return Page[MemberRecord]{}, err
	}
	orderClause := MemberSort.OrderBy(order)
	if err != nil {
		return Page[MemberRecord]{}, err
	}

	filter := MemberFilter{ProjectID: in.ProjectID, Role: in.Role}
	total, err := s.repo.CountMembers(ctx, filter)
	if err != nil {
		return Page[MemberRecord]{}, err
	}
	records, err := s.repo.ListMembers(ctx, filter,
		orderClause, pagination.Limit(), pagination.Offset())
	if err != nil {
		return Page[MemberRecord]{}, err
	}
	return Page[MemberRecord]{Items: records, Total: total, Page: pagination}, nil
}

// AddMemberInput 是添加项目成员的输入。
type AddMemberInput struct {
	ProjectID string
	ActorID   string
	UserID    string
}

// AddMember 添加项目成员。
//
// 需求文档 5.3：项目成员可以来自不同工作组。
// 接口契约 4.5：目标用户必须是有效用户，允许来自项目归属组以外的其他工作组。
func (s *Service) AddMember(ctx context.Context, in AddMemberInput) (MemberRecord, error) {
	project, err := s.requireManage(ctx, in.ProjectID, in.ActorID)
	if err != nil {
		return MemberRecord{}, err
	}
	if project.ToDomain().IsReadOnly() {
		return MemberRecord{}, domain.ArchivedError()
	}

	target, err := s.repo.FindUser(ctx, in.UserID)
	if err != nil {
		return MemberRecord{}, err
	}
	if !target.IsActive() {
		return MemberRecord{}, domain.ErrValidation.
			WithField("user_id", "目标用户已被停用，不能加入项目")
	}

	memberID, err := s.ids.UUID()
	if err != nil {
		return MemberRecord{}, domain.ErrInternal.WithCause(err)
	}

	// 新成员默认是 member；持有者通过 PATCH 单独提升，
	// 避免"添加即持有者"与部分唯一索引冲突。
	member, err := s.repo.AddMember(ctx, memberID, project.ID, in.UserID, domain.MemberRoleMember)
	if err != nil {
		return MemberRecord{}, err
	}

	s.logger.Info("项目成员已添加",
		slog.String("project_id", project.ID),
		slog.String("user_id", in.UserID),
		slog.String("actor_id", in.ActorID),
	)
	return member, nil
}

// ChangeMemberInput 是修改项目成员角色的输入。
type ChangeMemberInput struct {
	ProjectID string
	ActorID   string
	UserID    string
	Role      string
}

// ChangeMemberRole 修改项目成员角色。
//
// 核心不变式：项目最多一个 owner（数据库部分唯一索引保证），
// 且不得降级唯一 owner（domain.ValidateProjectMemberChange 保证）。
func (s *Service) ChangeMemberRole(ctx context.Context, in ChangeMemberInput) (MemberRecord, error) {
	project, err := s.requireManage(ctx, in.ProjectID, in.ActorID)
	if err != nil {
		return MemberRecord{}, err
	}
	if project.ToDomain().IsReadOnly() {
		return MemberRecord{}, domain.ArchivedError()
	}

	target, err := s.repo.FindMember(ctx, project.ID, in.UserID)
	if err != nil {
		return MemberRecord{}, err
	}

	action := domain.GroupMemberActionDemote
	if in.Role == domain.MemberRoleOwner {
		// 持有者唯一，因此"设为持有者"就是**转移**：在同一事务内先降现任、再升目标。
		action = domain.GroupMemberActionTransfer
	} else if in.Role != domain.MemberRoleMember {
		return MemberRecord{}, domain.ErrValidation.
			WithField("role", "角色只能是 owner 或 member")
	}

	ownerCount, err := s.repo.CountOwners(ctx, project.ID)
	if err != nil {
		return MemberRecord{}, err
	}
	if err := domain.ValidateProjectMemberChange(target.ToDomain(), action, ownerCount); err != nil {
		return MemberRecord{}, err
	}

	// 转移目标必须是**有效用户**：把持有者交给一个已停用账号，会让项目立刻进入
	// "谁都改不了"的状态——持有者是唯一能改项目的人，而停用账号无法登录。
	// 这与 AddMember 对目标用户的校验是同一个理由。
	if action == domain.GroupMemberActionTransfer {
		owner, err := s.repo.FindUser(ctx, in.UserID)
		if err != nil {
			return MemberRecord{}, err
		}
		if !owner.IsActive() {
			return MemberRecord{}, domain.ErrValidation.
				WithField("user_id", "目标用户已被停用，不能成为项目持有者")
		}
	}

	var updated MemberRecord
	if action == domain.GroupMemberActionTransfer {
		updated, err = s.repo.TransferOwnership(ctx, project.ID, in.UserID)
	} else {
		updated, err = s.repo.UpdateMemberRole(ctx, project.ID, in.UserID, in.Role)
	}
	if err != nil {
		return MemberRecord{}, err
	}

	s.logger.Info("项目成员角色已变更",
		slog.String("project_id", project.ID),
		slog.String("user_id", in.UserID),
		slog.String("role", in.Role),
		slog.Bool("ownership_transferred", action == domain.GroupMemberActionTransfer),
		slog.String("actor_id", in.ActorID),
	)
	return updated, nil
}

// RemoveMember 移除项目成员。
//
// 接口契约 4.5：owner 移除成员，不得移除唯一 owner。
func (s *Service) RemoveMember(ctx context.Context, projectID, actorID, userID string) error {
	project, err := s.requireManage(ctx, projectID, actorID)
	if err != nil {
		return err
	}
	if project.ToDomain().IsReadOnly() {
		return domain.ArchivedError()
	}

	target, err := s.repo.FindMember(ctx, project.ID, userID)
	if err != nil {
		return err
	}

	ownerCount, err := s.repo.CountOwners(ctx, project.ID)
	if err != nil {
		return err
	}
	if err := domain.ValidateProjectMemberChange(target.ToDomain(),
		domain.GroupMemberActionRemove, ownerCount); err != nil {
		return err
	}

	if err := s.repo.RemoveMember(ctx, project.ID, userID); err != nil {
		return err
	}

	s.logger.Info("项目成员已移除",
		slog.String("project_id", project.ID),
		slog.String("user_id", userID),
		slog.String("actor_id", actorID),
	)
	return nil
}

// requireVisible 要求项目对 actor 可见。
func (s *Service) requireVisible(ctx context.Context, projectID, actorID string) (ProjectRecord, error) {
	record, err := s.repo.FindProjectForUser(ctx, projectID, actorID, s.today(ctx, actorID))
	if err != nil {
		return ProjectRecord{}, err
	}
	if permissionOf(record, actorID) == domain.PermNone {
		return ProjectRecord{}, notFound()
	}
	return record, nil
}

// requireManage 要求 actor 是项目持有者。
func (s *Service) requireManage(ctx context.Context, projectID, actorID string) (ProjectRecord, error) {
	record, err := s.repo.FindProjectForUser(ctx, projectID, actorID, s.today(ctx, actorID))
	if err != nil {
		return ProjectRecord{}, err
	}
	if !domain.CanManageProject(record.ToDomain(), actorOf(record, actorID), membershipOf(record)) {
		return ProjectRecord{}, notFound()
	}
	return record, nil
}

// today 返回按请求者时区计算的"今天"。
//
// 需求文档 5.7：底层时间统一 UTC，上层按用户时区计算业务日期。
// 用户时区由装配点注入的 TimezoneProvider 提供（生产环境是 app/users）。
//
// 为什么不直接把 actor 时区从 HTTP 层传下来：本服务的权限判定入口
// AccessFor 会被其他模块以"只有两个 ID"的方式调用（见 adapter.go），
// 无法保证调用方手上总有完整的用户对象。以用户 ID 查时区是唯一在两种
// 调用方式下都成立的做法。
//
// 读取失败时回退到 UTC 日历日并记录告警，而不是让整个请求失败：
// 这个值只影响派生展示字段（is_overdue / overdue_task_count），
// 让一次列表请求因为一个展示值而 500 是更差的结果。
func (s *Service) today(ctx context.Context, actorID string) time.Time {
	fallback := domain.DayOf(s.clk.Now())
	if s.timezones == nil {
		return fallback
	}

	timezone, err := s.timezones.TimezoneOf(ctx, actorID)
	if err != nil {
		s.logger.Warn("读取用户时区失败，本次按 UTC 计算今天",
			slog.String("user_id", actorID),
			slog.Any("cause", err),
		)
		return fallback
	}

	today, err := domain.Today(s.clk.Now(), timezone)
	if err != nil {
		s.logger.Warn("用户时区非法，本次按 UTC 计算今天",
			slog.String("user_id", actorID),
			slog.String("timezone", timezone),
			slog.Any("cause", err),
		)
		return fallback
	}
	return today
}

// permissionOf 计算 actor 对项目记录的权限。
func permissionOf(record ProjectRecord, actorID string) domain.Permission {
	var groupMembership *domain.GroupMember
	if record.GroupMembership != nil {
		member := record.GroupMembership.ToDomain()
		groupMembership = &member
	}
	group := domain.Group{ID: record.GroupID, Status: record.GroupStatus}
	// actor 必须带上真实的 SystemRole：domain 的权限函数据此对系统管理员放行。
	// 无法在这里凭 actorID 重建角色，因此角色由查询一并取出（record.ActorSystemRole）。
	return domain.ProjectPermission(record.ToDomain(), actorOf(record, actorID),
		membershipOf(record), group, groupMembership)
}

// actorOf 由记录中的系统角色重建领域用户。
//
// 与 groups 模块的同名函数同理：权限入口大多只拿得到 actorID，而管理员放行需要
// 系统角色。查询时已把它取出，这里只做重建，不额外查库。
func actorOf(record ProjectRecord, actorID string) domain.User {
	return domain.User{ID: actorID, SystemRole: record.ActorSystemRole}
}

// membershipOf 从项目记录取出领域成员关系，非成员时返回 nil。
func membershipOf(record ProjectRecord) *domain.ProjectMember {
	if record.Membership == nil {
		return nil
	}
	member := record.Membership.ToDomain()
	return &member
}

// isActiveGroupMember 报告组员关系是否活跃。
func isActiveGroupMember(member *GroupMemberRecord) bool {
	return member != nil && member.Status == domain.MemberStatusActive
}

// validProjectStatus 报告项目状态是否合法。
func validProjectStatus(status string) bool {
	for _, allowed := range domain.ProjectStatuses {
		if status == allowed {
			return true
		}
	}
	return false
}

// isUnarchive 报告目标状态是否为"解除归档"。
func isUnarchive(status *string) bool {
	return status != nil && *status != domain.ProjectStatusArchived
}

// descriptionForValidation 在未显式提供描述时返回 nil。
func descriptionForValidation(description *string, set bool) *string {
	if !set {
		return nil
	}
	return description
}

// isNotFound 报告错误是否为"对象不存在"语义。
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

// assertNotDropped 断言归一化步骤没有静默丢弃调用方提供的字段。
//
// 为什么需要它：字段校验/归一化函数接收的是"部分字段"的结构体，
// 如果调用方漏传某个字段，归一化会把它当成"未提供"并返回 nil，
// 于是更新请求被静默忽略——没有报错、没有 5xx，只有一个字段没生效。
// 这类缺陷无法靠编译期发现，只能靠显式断言拦住。
//
// panic 是刻意的：它只可能在开发/测试期被触发（属于编程错误），
// 生产环境不会因为用户输入而进入该分支。
func assertNotDropped(field string, provided, survived bool) {
	if provided && !survived {
		panic("projects: 归一化丢弃了已提供的字段 " + field + "，请检查 NormalizeProjectUpdate 的调用参数")
	}
}
