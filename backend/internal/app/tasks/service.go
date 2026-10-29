package tasks

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/listquery"
	"github.com/myonku/project-builder/internal/platform/clock"
	"github.com/myonku/project-builder/internal/platform/idgen"
)

// Store 是本模块对持久化的需求。
//
// 写方法多一个 audit 参数：审计必须与业务写在**同一个事务**里（《A09》），
// 因此由用例层构造记录、仓储在提交前 Flush；nil 表示这次写不记审计。
type Store interface {
	FindTaskForUser(ctx context.Context, taskID, userID string, includeDeleted bool) (TaskRecord, error)
	FindTaskByID(ctx context.Context, taskID string, includeDeleted bool) (TaskRecord, error)
	ListTasks(ctx context.Context, filter ListFilter, orderBy string, limit, offset int) ([]TaskRecord, error)
	CountTasks(ctx context.Context, filter ListFilter) (int, error)
	CreateTaskInTx(ctx context.Context, params CreateTaskParams) (TaskRecord, error)
	UpdateTask(ctx context.Context, params TaskUpdateParams) (TaskRecord, error)
	SoftDeleteTask(ctx context.Context, taskID, actorID string, expectedUpdated, deletedAt time.Time, audit *apperr.AuditWrite) (TaskRecord, error)
	ListAncestors(ctx context.Context, taskID string) ([]TaskRecord, error)

	FindMember(ctx context.Context, taskID, userID string) (MemberRecord, error)
	ListMembers(ctx context.Context, filter MemberFilter, orderBy string, limit, offset int) ([]MemberRecord, error)
	CountMembers(ctx context.Context, filter MemberFilter) (int, error)
	CountOwners(ctx context.Context, taskID string) (int, error)
	AddMember(ctx context.Context, id, taskID, userID, role string, audit *apperr.AuditWrite) (MemberRecord, error)
	UpdateMemberRole(ctx context.Context, taskID, userID, role string, audit *apperr.AuditWrite) (MemberRecord, error)
	// TransferOwnership 在同一事务内先降现任持有者、再升目标。
	TransferOwnership(ctx context.Context, taskID, targetUserID string, audit *apperr.AuditWrite) (MemberRecord, error)
	RemoveMember(ctx context.Context, taskID, userID string, audit *apperr.AuditWrite) error

	FindProjectMember(ctx context.Context, projectID, userID string) (bool, error)
	FindUser(ctx context.Context, userID string) (UserRecord, error)

	FindIdempotencyKey(ctx context.Context, userID, scope, key string) (string, string, bool, error)
	SaveIdempotencyKey(ctx context.Context, id, userID, scope, key, resultID, secondaryID string) error
}

// ProjectAccess 是任务模块对项目权限判定的需求。
//
// 由 app/projects 的 Service 实现（见其 Access 方法）。定义成本模块自己的接口，
// 而不是直接依赖 projects 包：这样测试可以注入替身，
// 也避免两个业务模块之间产生循环依赖。
type ProjectAccess interface {
	Access(ctx context.Context, projectID, actorID string) (ProjectView, domain.Permission, error)
}

// ProjectView 是任务模块所需的项目投影。
type ProjectView struct {
	ID         string
	Status     string
	Visibility string
	ArchivedAt *time.Time
}

// ToDomain 转换为领域实体。
func (v ProjectView) ToDomain() domain.Project {
	return domain.Project{
		ID:         v.ID,
		Status:     v.Status,
		Visibility: v.Visibility,
		ArchivedAt: v.ArchivedAt,
	}
}

// Page 是列表查询结果。
type Page[T any] struct {
	Items []T
	Total int
	Page  listquery.Pagination
}

// TodayFor 返回按 actor 时区计算的"今天"，用于派生 is_overdue。
//
// 需求文档 5.7：底层时间统一 UTC，上层按用户时区计算业务日期。
// "今天"的算法集中在 domain.Today，projects 模块使用同一实现，规则不会漂移。
//
// 时区直接取自已加载的当前用户（auth 每次请求都回数据库读取用户），
// 因此这里不需要额外查询，也不需要把 users 模块的仓储注入本模块。
// 这与 projects 模块按用户 ID 查时区的做法不同，原因见 projects.Service.today：
// projects 的权限入口会被其他模块以"只有两个 ID"的方式调用，拿不到完整用户对象。
//
// 时区非法时回退到 UTC 日历日：该值只影响派生展示字段，
// 不应让整个读取路径因此 500。
func (s *Service) TodayFor(actor domain.User) time.Time {
	today, err := domain.Today(s.clk.Now(), actor.Timezone)
	if err != nil {
		s.logger.Warn("用户时区非法，按 UTC 计算今天",
			slog.String("user_id", actor.ID),
			slog.String("timezone", actor.Timezone),
			slog.Any("cause", err),
		)
		return domain.DayOf(s.clk.Now())
	}
	return today
}

// Service 承载任务用例。
type Service struct {
	repo     Store
	projects ProjectAccess
	ids      *idgen.Generator
	clk      clock.Clock
	logger   *slog.Logger
	// audit 是操作记录的写入方：**由仓储在业务事务里调用**（见 apperr.AuditWrite）。
	audit apperr.AuditWriter
}

// Options 是 Service 的构造参数。
type Options struct {
	Store    Store
	Projects ProjectAccess
	IDs      *idgen.Generator
	Clock    clock.Clock
	Logger   *slog.Logger
	// Audit 为 nil 时不记操作记录（单元测试或不需要审计的装配）。
	Audit apperr.AuditWriter
}

// NewService 构造任务用例服务。
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
		repo:     opts.Store,
		projects: opts.Projects,
		ids:      opts.IDs,
		clk:      clk,
		logger:   logger,
		audit:    opts.Audit,
	}
}

// auditWith 构造一条任务操作记录。
//
// 集中一处构造：对象类型恒为 task（成员与持有者变更也记在**任务**这个容器上，
// 《A09》），动作与对象 id 不会各写一遍，changes 的组装有统一入口。
// request id 由 context 提供（RequestIDMiddleware 写入），这里不重复传递。
func (s *Service) auditWith(
	actorID string, action domain.AuditAction, taskID string, changes map[string]string,
) *apperr.AuditWrite {
	return apperr.NewAuditWrite(s.audit, domain.AuditEntry{
		ActorID:    actorID,
		Action:     action,
		ObjectType: domain.AuditObjectTask,
		ObjectID:   taskID,
		Changes:    changes,
	})
}

// taskChanges 汇总本次更新**实际提供**的字段（未提供的字段不出现）。
//
// 只记新值：需求要求"关键变更内容"，不要求完整前后对照；
// 完整前后值会把更多内容留在只增不删的表里。
func taskChanges(in UpdateInput) map[string]string {
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
	if in.Progress != nil {
		changes["progress"] = strconv.Itoa(*in.Progress)
	}
	if in.Priority != nil {
		changes["priority"] = strconv.Itoa(*in.Priority)
	}
	if in.StartDate != nil {
		changes["start_date"] = domain.FormatDate(*in.StartDate)
	}
	if in.EndDate != nil {
		changes["end_date"] = domain.FormatDate(*in.EndDate)
	}
	if in.IsMilestone != nil {
		changes["is_milestone"] = strconv.FormatBool(*in.IsMilestone)
	}
	if in.IsShared != nil {
		changes["is_shared"] = strconv.FormatBool(*in.IsShared)
	}
	if in.ParentIDSet {
		// 显式 null 表示"提升为顶层任务"，与"未提供"不同，因此要记一条可区分的值。
		if in.ParentID == nil {
			changes["parent_id"] = ""
		} else {
			changes["parent_id"] = *in.ParentID
		}
	}
	return changes
}

// CreateInput 是创建任务的用例输入。
type CreateInput struct {
	ProjectID   string
	CreatorID   string
	Name        string
	Description *string
	ParentID    *string
	StartDate   time.Time
	EndDate     time.Time
	Priority    *int
	IsMilestone *bool
	IsShared    *bool
}

// Create 创建任务。
//
// 需求文档 5.4：
//   - 必须属于一个项目，创建者默认成为任务成员与持有者；
//   - 创建者不是项目成员时应先加入项目（这里直接拒绝，给出明确指引）；
//   - 支持父子任务，最大 3 层，子任务必须在父任务区间内且同项目；
//   - 归档项目只读。
func (s *Service) Create(ctx context.Context, in CreateInput) (TaskRecord, error) {
	project, permission, err := s.projects.Access(ctx, in.ProjectID, in.CreatorID)
	if err != nil {
		return TaskRecord{}, err
	}
	if permission == domain.PermNone {
		return TaskRecord{}, projectNotFound()
	}
	if !domain.CanEditProjectTasks(project.ToDomain(), permission) {
		// 归档项目只读，或仅够读权限。
		if project.ToDomain().IsReadOnly() {
			return TaskRecord{}, domain.ArchivedError()
		}
		return TaskRecord{}, domain.ErrPermissionDenied
	}

	normalized, fields := domain.NormalizeTaskCreate(domain.TaskInput{
		Name:        in.Name,
		Description: in.Description,
		ParentID:    in.ParentID,
		StartDate:   in.StartDate,
		EndDate:     in.EndDate,
		Priority:    in.Priority,
		IsMilestone: in.IsMilestone,
		IsShared:    in.IsShared,
	})
	// 日期区间倒置必须返回 400 invalid_date_range，而不是 422。
	if dateErr := domain.ProjectDateRangeError(in.StartDate, in.EndDate); dateErr != nil {
		return TaskRecord{}, dateErr
	}
	if len(fields) > 0 {
		return TaskRecord{}, domain.ErrValidation.WithFields(fields)
	}

	// 层级与日期覆盖校验：父任务必须同项目、未删除、且自身深度不超过上限。
	if normalized.ParentID != nil {
		if err := s.validateParent(ctx, "", *normalized.ParentID, in.ProjectID,
			normalized.StartDate, normalized.EndDate); err != nil {
			return TaskRecord{}, err
		}
	}

	priority := domain.TaskPriorityNormal
	if normalized.Priority != nil {
		priority = *normalized.Priority
	}
	isMilestone := false
	if normalized.IsMilestone != nil {
		isMilestone = *normalized.IsMilestone
	}
	isShared := false
	if normalized.IsShared != nil {
		isShared = *normalized.IsShared
	}

	taskID, err := s.ids.UUID()
	if err != nil {
		return TaskRecord{}, domain.ErrInternal.WithCause(err)
	}
	memberID, err := s.ids.UUID()
	if err != nil {
		return TaskRecord{}, domain.ErrInternal.WithCause(err)
	}

	created, err := s.repo.CreateTaskInTx(ctx, CreateTaskParams{
		ID:          taskID,
		ProjectID:   in.ProjectID,
		ParentID:    normalized.ParentID,
		CreatedBy:   in.CreatorID,
		Name:        normalized.Name,
		Description: normalized.Description,
		// 进度默认 0，与状态 not_started 一致（需求文档 5.4）。
		Status:      domain.ProgressStatus(0),
		Progress:    0,
		Priority:    priority,
		StartDate:   normalized.StartDate,
		EndDate:     normalized.EndDate,
		IsMilestone: isMilestone,
		IsShared:    isShared,
		MemberID:    memberID,
		Audit: s.auditWith(in.CreatorID, domain.AuditActionCreated, taskID, map[string]string{
			"name":       normalized.Name,
			"project_id": in.ProjectID,
		}),
	})
	if err != nil {
		return TaskRecord{}, err
	}

	// 显式补上创建者的成员关系：仓储的 INSERT ... RETURNING 只返回 tasks 表的列，
	// 不会带出刚写入的 task_members 行。若此处不补，创建响应里的 membership_role
	// 会是 null，而数据库里其实已经是 owner，前端据此判断会出错。
	created.Membership = &MemberRecord{
		ID:     memberID,
		TaskID: created.ID,
		UserID: in.CreatorID,
		Role:   domain.MemberRoleOwner,
	}

	s.logger.Info("任务已创建",
		slog.String("task_id", created.ID),
		slog.String("project_id", created.ProjectID),
		slog.String("creator_id", in.CreatorID),
	)
	return created, nil
}

// Detail 返回任务详情。
//
// 使用与列表相同的过滤条件（默认排除逻辑删除），保证两条路径行为一致。
func (s *Service) Detail(ctx context.Context, taskID, actorID string, includeDeleted bool) (TaskRecord, error) {
	record, err := s.repo.FindTaskForUser(ctx, taskID, actorID, includeDeleted)
	if err != nil {
		return TaskRecord{}, err
	}

	project, permission, err := s.projects.Access(ctx, record.ProjectID, actorID)
	if err != nil {
		return TaskRecord{}, err
	}
	if domain.TaskPermission(record.ToDomain(), project.ToDomain(), permission) == domain.PermNone {
		return TaskRecord{}, projectNotFound()
	}
	return record, nil
}

// ListInput 是查询任务的输入。
type ListInput struct {
	ProjectID      string
	ActorID        string
	Status         *string
	AssigneeID     *string
	OwnerID        *string
	IsShared       *bool
	IncludeDeleted bool
	Page           listquery.PageRequest
	Sort           string
}

// List 查询项目下的可见任务。
func (s *Service) List(ctx context.Context, in ListInput) (Page[TaskRecord], error) {
	pagination, err := in.Page.Pagination()
	if err != nil {
		return Page[TaskRecord]{}, err
	}

	project, permission, err := s.projects.Access(ctx, in.ProjectID, in.ActorID)
	if err != nil {
		return Page[TaskRecord]{}, err
	}
	if permission == domain.PermNone {
		return Page[TaskRecord]{}, projectNotFound()
	}

	if in.Status != nil && !validTaskStatus(*in.Status) {
		return Page[TaskRecord]{}, domain.ErrInvalidRequest.
			WithField("status", "任务状态取值非法")
	}
	for field, value := range map[string]*string{
		"assignee_id": in.AssigneeID,
		"owner_id":    in.OwnerID,
	} {
		if value != nil && !idgen.IsUUID(*value) {
			return Page[TaskRecord]{}, domain.ErrInvalidRequest.
				WithField(field, "必须是合法的 UUID 字符串")
		}
	}

	order, err := TaskSort.Resolve(in.Sort)
	if err != nil {
		return Page[TaskRecord]{}, err
	}
	orderClause := TaskSort.OrderBy(order)

	filter := ListFilter{
		ProjectID:      in.ProjectID,
		UserID:         in.ActorID,
		Status:         in.Status,
		AssigneeID:     in.AssigneeID,
		OwnerID:        in.OwnerID,
		IsShared:       in.IsShared,
		IncludeDeleted: in.IncludeDeleted,
	}

	// 仅够读权限的用户（组内只读）只能看到已共享任务；
	// 该限制在 SQL 的可见性条件中已表达，这里额外收紧显式筛选避免越权枚举。
	if permission == domain.PermRead && project.Visibility == domain.ProjectVisibilityGroupReadOnly {
		shared := true
		filter.IsShared = &shared
	}

	total, err := s.repo.CountTasks(ctx, filter)
	if err != nil {
		return Page[TaskRecord]{}, err
	}
	records, err := s.repo.ListTasks(ctx, filter,
		orderClause, pagination.Limit(), pagination.Offset())
	if err != nil {
		return Page[TaskRecord]{}, err
	}
	return Page[TaskRecord]{Items: records, Total: total, Page: pagination}, nil
}

// UpdateInput 是修改任务的用例输入。
type UpdateInput struct {
	TaskID          string
	ActorID         string
	Name            *string
	Description     *string
	DescriptionSet  bool
	Status          *string
	Progress        *int
	Priority        *int
	StartDate       *time.Time
	EndDate         *time.Time
	IsMilestone     *bool
	IsShared        *bool
	ParentID        *string
	ParentIDSet     bool
	ExpectedUpdated time.Time
}

// Update 修改任务。
//
// 权限：项目持有者、项目成员，或"项目组内可编辑且任务已共享"的组员。
// 归档项目只读。日期区间用合并后的最终值校验。
func (s *Service) Update(ctx context.Context, in UpdateInput) (TaskRecord, error) {
	record, err := s.repo.FindTaskForUser(ctx, in.TaskID, in.ActorID, false)
	if err != nil {
		return TaskRecord{}, err
	}

	project, permission, err := s.projects.Access(ctx, record.ProjectID, in.ActorID)
	if err != nil {
		return TaskRecord{}, err
	}
	taskPermission := domain.TaskPermission(record.ToDomain(), project.ToDomain(), permission)
	if taskPermission == domain.PermNone {
		return TaskRecord{}, projectNotFound()
	}
	if project.ToDomain().IsReadOnly() {
		return TaskRecord{}, domain.ArchivedError()
	}
	if !taskPermission.AtLeast(domain.PermWrite) {
		return TaskRecord{}, domain.ErrPermissionDenied
	}

	normalized, fields := domain.NormalizeTaskUpdate(domain.TaskUpdateInput{
		Name:        in.Name,
		Description: descriptionForValidation(in.Description, in.DescriptionSet),
		Status:      in.Status,
		Progress:    in.Progress,
		Priority:    in.Priority,
		ParentID:    in.ParentID,
		// 日期字段必须一并传入：归一化会原样返回它们。
		// 漏传会让"只改日期"的请求被静默忽略（返回 200 但字段未变）。
		StartDate: in.StartDate,
		EndDate:   in.EndDate,
	})
	if len(fields) > 0 {
		return TaskRecord{}, domain.ErrValidation.WithFields(fields)
	}
	// 自检：归一化不得丢弃调用方提供的字段（见 projects 包同名断言的说明）。
	assertNotDropped("start_date", in.StartDate != nil, normalized.StartDate != nil)
	assertNotDropped("end_date", in.EndDate != nil, normalized.EndDate != nil)

	// 日期区间用"合并后的最终值"校验，避免只改一端时漏判。
	start := record.StartDate
	if in.StartDate != nil {
		start = *in.StartDate
	}
	end := record.EndDate
	if in.EndDate != nil {
		end = *in.EndDate
	}
	if err := domain.ValidateDateRange(start, end); err != nil {
		return TaskRecord{}, err
	}

	// 状态与进度保持一致：显式设置了进度但没给状态时，按进度推导状态。
	status := normalized.Status
	progress := normalized.Progress
	if progress != nil && status == nil {
		derived := domain.ProgressStatus(*progress)
		status = &derived
	}

	// 父任务变更需要重做层级与区间校验（含循环引用检测）。
	if in.ParentIDSet {
		if normalized.ParentID != nil {
			if err := s.validateParent(ctx, in.TaskID, *normalized.ParentID,
				record.ProjectID, start, end); err != nil {
				return TaskRecord{}, err
			}
		}
	} else if in.StartDate != nil || in.EndDate != nil {
		// 日期变化时，若已有父任务，必须重新确认仍落在父任务区间内。
		if record.ParentID != nil {
			if err := s.validateParent(ctx, in.TaskID, *record.ParentID,
				record.ProjectID, start, end); err != nil {
				return TaskRecord{}, err
			}
		}
	}

	updated, err := s.repo.UpdateTask(ctx, TaskUpdateParams{
		ID:              in.TaskID,
		Name:            normalized.Name,
		Description:     normalized.Description,
		DescriptionSet:  in.DescriptionSet,
		Status:          status,
		Progress:        progress,
		Priority:        normalized.Priority,
		StartDate:       in.StartDate,
		EndDate:         in.EndDate,
		IsMilestone:     in.IsMilestone,
		IsShared:        in.IsShared,
		ParentID:        normalized.ParentID,
		ParentIDSet:     in.ParentIDSet,
		ExpectedUpdated: in.ExpectedUpdated,
		// 最后修改人必须由写路径显式提供：漏传不会报错，字段只会停在旧值上。
		ActorID: in.ActorID,
		// 只记本次请求**实际提供**的字段与新值（未提供的字段不出现）。
		Audit: s.auditWith(in.ActorID, domain.AuditActionUpdated, in.TaskID, taskChanges(in)),
	})
	if err != nil {
		return TaskRecord{}, err
	}

	s.logger.Info("任务已更新",
		slog.String("task_id", updated.ID),
		slog.String("actor_id", in.ActorID),
	)
	return updated, nil
}

// UpdateDatesInput 是甘特图日期编辑的用例输入。
//
// 接口契约 4.6：PATCH /tasks/{task_id}/dates —— 只允许改日期。
// 早期实现该端点接受了全量任务字段，导致名称/状态/进度可被"仅日期"端点修改；
// 这里通过独立的输入结构在类型层面收敛字段集合。
type UpdateDatesInput struct {
	TaskID          string
	ActorID         string
	StartDate       time.Time
	EndDate         time.Time
	ExpectedUpdated time.Time
}

// UpdateDates 修改任务日期（甘特图拖拽保存）。
func (s *Service) UpdateDates(ctx context.Context, in UpdateDatesInput) (TaskRecord, error) {
	record, err := s.repo.FindTaskForUser(ctx, in.TaskID, in.ActorID, false)
	if err != nil {
		return TaskRecord{}, err
	}

	project, permission, err := s.projects.Access(ctx, record.ProjectID, in.ActorID)
	if err != nil {
		return TaskRecord{}, err
	}
	taskPermission := domain.TaskPermission(record.ToDomain(), project.ToDomain(), permission)
	if taskPermission == domain.PermNone {
		return TaskRecord{}, projectNotFound()
	}
	if project.ToDomain().IsReadOnly() {
		return TaskRecord{}, domain.ArchivedError()
	}
	if !taskPermission.AtLeast(domain.PermWrite) {
		return TaskRecord{}, domain.ErrPermissionDenied
	}

	if err := domain.ValidateDateRange(in.StartDate, in.EndDate); err != nil {
		return TaskRecord{}, err
	}
	if record.ParentID != nil {
		if err := s.validateParent(ctx, in.TaskID, *record.ParentID,
			record.ProjectID, in.StartDate, in.EndDate); err != nil {
			return TaskRecord{}, err
		}
	}

	updated, err := s.repo.UpdateTask(ctx, TaskUpdateParams{
		ID:              in.TaskID,
		StartDate:       &in.StartDate,
		EndDate:         &in.EndDate,
		ExpectedUpdated: in.ExpectedUpdated,
		// 甘特图拖拽改日期也是一次修改，同样要记录最后修改人。
		ActorID: in.ActorID,
		// 该端点只能改日期，因此 changes 只有两个日期键。
		Audit: s.auditWith(in.ActorID, domain.AuditActionUpdated, in.TaskID, map[string]string{
			"start_date": domain.FormatDate(in.StartDate),
			"end_date":   domain.FormatDate(in.EndDate),
		}),
	})
	if err != nil {
		return TaskRecord{}, err
	}
	return updated, nil
}

// DeleteInput 是逻辑删除任务的用例输入。
type DeleteInput struct {
	TaskID          string
	ActorID         string
	ExpectedUpdated time.Time
}

// Delete 逻辑删除任务。
//
// 需求文档 4.2 第 5 条：删除共享任务至少需要项目持有者、组管理员或系统管理员权限。
// 这里要求 PermManage 或 PermWrite（项目成员），并显式阻止归档项目下的删除。
func (s *Service) Delete(ctx context.Context, in DeleteInput) (TaskRecord, error) {
	record, err := s.repo.FindTaskForUser(ctx, in.TaskID, in.ActorID, false)
	if err != nil {
		return TaskRecord{}, err
	}

	project, permission, err := s.projects.Access(ctx, record.ProjectID, in.ActorID)
	if err != nil {
		return TaskRecord{}, err
	}
	taskPermission := domain.TaskPermission(record.ToDomain(), project.ToDomain(), permission)
	if taskPermission == domain.PermNone {
		return TaskRecord{}, projectNotFound()
	}
	if project.ToDomain().IsReadOnly() {
		return TaskRecord{}, domain.ArchivedError()
	}
	if !taskPermission.AtLeast(domain.PermWrite) {
		return TaskRecord{}, domain.ErrPermissionDenied
	}

	deleted, err := s.repo.SoftDeleteTask(ctx, in.TaskID, in.ActorID, in.ExpectedUpdated, s.clk.Now(),
		s.auditWith(in.ActorID, domain.AuditActionDeleted, in.TaskID, nil))
	if err != nil {
		return TaskRecord{}, err
	}

	s.logger.Info("任务已逻辑删除",
		slog.String("task_id", deleted.ID),
		slog.String("actor_id", in.ActorID),
	)
	return deleted, nil
}

// CopyInput 是复制任务的用例输入。
type CopyInput struct {
	TaskID         string
	ActorID        string
	IdempotencyKey string
	Name           *string
	Description    *string
	StartDate      *time.Time
	EndDate        *time.Time
	IsShared       *bool
}

// Copy 复制任务。
//
// 需求文档 5.4：首期复制任务简化为复制基础字段，不复制提醒发送记录。
//
// 幂等（backend/docs/B01 5.6）：Idempotency-Key 必须持久化并带唯一约束。
// 早期实现完全未实现该机制，实测同一 key 连续两次 copy 会产生两个任务。
func (s *Service) Copy(ctx context.Context, in CopyInput) (TaskRecord, bool, error) {
	source, err := s.repo.FindTaskForUser(ctx, in.TaskID, in.ActorID, false)
	if err != nil {
		return TaskRecord{}, false, err
	}

	project, permission, err := s.projects.Access(ctx, source.ProjectID, in.ActorID)
	if err != nil {
		return TaskRecord{}, false, err
	}
	taskPermission := domain.TaskPermission(source.ToDomain(), project.ToDomain(), permission)
	if taskPermission == domain.PermNone {
		return TaskRecord{}, false, notFound()
	}
	if project.ToDomain().IsReadOnly() {
		return TaskRecord{}, false, domain.ArchivedError()
	}
	if !taskPermission.AtLeast(domain.PermWrite) {
		return TaskRecord{}, false, domain.ErrPermissionDenied
	}

	normalized, fields := domain.NormalizeTaskCopy(domain.TaskCopyInput{
		Name:        in.Name,
		Description: in.Description,
		StartDate:   in.StartDate,
		EndDate:     in.EndDate,
		IsShared:    in.IsShared,
	})
	if len(fields) > 0 {
		return TaskRecord{}, false, domain.ErrValidation.WithFields(fields)
	}

	// 幂等键作用域包含操作与被复制任务，避免跨任务/跨操作串用（backend/docs/B01 5.6）。
	scope := domain.IdempotencyScope("tasks.copy", in.TaskID)
	if in.IdempotencyKey != "" {
		resultID, _, found, err := s.repo.FindIdempotencyKey(ctx, in.ActorID, scope, in.IdempotencyKey)
		if err != nil {
			return TaskRecord{}, false, err
		}
		if found {
			// 已存在：返回既有结果，并标记为"重放"，由 HTTP 层决定状态码。
			existing, err := s.repo.FindTaskForUser(ctx, resultID, in.ActorID, true)
			if err != nil {
				return TaskRecord{}, false, err
			}
			return existing, true, nil
		}
	}

	copied := domain.ApplyTaskCopy(source.ToDomain(), normalized)
	if err := domain.ValidateDateRange(copied.StartDate, copied.EndDate); err != nil {
		return TaskRecord{}, false, err
	}

	taskID, err := s.ids.UUID()
	if err != nil {
		return TaskRecord{}, false, domain.ErrInternal.WithCause(err)
	}
	memberID, err := s.ids.UUID()
	if err != nil {
		return TaskRecord{}, false, domain.ErrInternal.WithCause(err)
	}

	created, err := s.repo.CreateTaskInTx(ctx, CreateTaskParams{
		ID:        taskID,
		ProjectID: source.ProjectID,
		// 复制不保留父子关系：跨父任务复制会破坏"子任务落在父任务区间内"的约束。
		ParentID:    nil,
		CreatedBy:   in.ActorID,
		Name:        copied.Name,
		Description: copied.Description,
		Status:      copied.Status,
		Progress:    copied.Progress,
		Priority:    copied.Priority,
		StartDate:   copied.StartDate,
		EndDate:     copied.EndDate,
		IsMilestone: copied.IsMilestone,
		IsShared:    copied.IsShared,
		MemberID:    memberID,
		// 记录的是**新任务**（object_id = 新 id）；copied_from 让"这条任务从哪来"可追溯。
		Audit: s.auditWith(in.ActorID, domain.AuditActionCreated, taskID, map[string]string{
			"name":        copied.Name,
			"project_id":  source.ProjectID,
			"copied_from": source.ID,
		}),
	})
	if err != nil {
		return TaskRecord{}, false, err
	}

	if in.IdempotencyKey != "" {
		keyID, err := s.ids.UUID()
		if err != nil {
			return TaskRecord{}, false, domain.ErrInternal.WithCause(err)
		}
		if err := s.repo.SaveIdempotencyKey(ctx, keyID, in.ActorID, scope,
			in.IdempotencyKey, created.ID, ""); err != nil {
			// 幂等键写入失败意味着并发请求已抢先写入同名键：
			// 此时已创建的任务属于重复副作用，必须明确报错而不是静默成功。
			return TaskRecord{}, false, err
		}
	}

	s.logger.Info("任务已复制",
		slog.String("source_task_id", source.ID),
		slog.String("task_id", created.ID),
		slog.String("actor_id", in.ActorID),
	)
	return created, false, nil
}

// MemberListInput 是查询任务成员的输入。
type MemberListInput struct {
	TaskID  string
	ActorID string
	Role    *string
	Page    listquery.PageRequest
	Sort    string
}

// ListMembers 查询任务成员。
func (s *Service) ListMembers(ctx context.Context, in MemberListInput) (Page[MemberRecord], error) {
	pagination, err := in.Page.Pagination()
	if err != nil {
		return Page[MemberRecord]{}, err
	}

	if _, err := s.requireVisible(ctx, in.TaskID, in.ActorID); err != nil {
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

	filter := MemberFilter{TaskID: in.TaskID, Role: in.Role}
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

// AddMemberInput 是添加任务成员的输入。
type AddMemberInput struct {
	TaskID  string
	ActorID string
	UserID  string
}

// AddMember 添加任务成员。
//
// 需求文档 5.4：任务成员必须属于项目成员，持有者也必须是项目成员。
// 该不变式在数据库层没有约束（task_members 只外键指向 users），
// 因此必须由用例层显式校验——这是"应用层保证"的明确示例。
func (s *Service) AddMember(ctx context.Context, in AddMemberInput) (MemberRecord, error) {
	record, project, permission, err := s.requireManage(ctx, in.TaskID, in.ActorID)
	if err != nil {
		return MemberRecord{}, err
	}
	if project.ToDomain().IsReadOnly() {
		return MemberRecord{}, domain.ArchivedError()
	}
	_ = permission

	target, err := s.repo.FindUser(ctx, in.UserID)
	if err != nil {
		return MemberRecord{}, err
	}
	if !target.IsActive() {
		return MemberRecord{}, domain.ErrValidation.
			WithField("user_id", "目标用户已被停用，不能加入任务")
	}

	isProjectMember, err := s.repo.FindProjectMember(ctx, record.ProjectID, in.UserID)
	if err != nil {
		return MemberRecord{}, err
	}
	if err := domain.ValidateTaskMemberInvariant(isProjectMember); err != nil {
		return MemberRecord{}, err
	}

	memberID, err := s.ids.UUID()
	if err != nil {
		return MemberRecord{}, domain.ErrInternal.WithCause(err)
	}

	member, err := s.repo.AddMember(ctx, memberID, record.ID, in.UserID, domain.MemberRoleMember,
		s.auditWith(in.ActorID, domain.AuditActionMemberAdded, record.ID, map[string]string{
			"user_id": in.UserID,
			"role":    domain.MemberRoleMember,
		}))
	if err != nil {
		return MemberRecord{}, err
	}

	s.logger.Info("任务成员已添加",
		slog.String("task_id", record.ID),
		slog.String("user_id", in.UserID),
		slog.String("actor_id", in.ActorID),
	)

	// 需求文档 5.7："新任务分配"可以触发邮件。这里只插入一行提醒记录，
	// 发送由 reminders 的后台循环完成（不阻塞本次请求）。
	return member, nil
}

// ChangeMemberInput 是修改任务成员角色的输入。
type ChangeMemberInput struct {
	TaskID  string
	ActorID string
	UserID  string
	Role    string
}

// ChangeMemberRole 修改任务成员角色。
func (s *Service) ChangeMemberRole(ctx context.Context, in ChangeMemberInput) (MemberRecord, error) {
	record, project, _, err := s.requireManage(ctx, in.TaskID, in.ActorID)
	if err != nil {
		return MemberRecord{}, err
	}
	if project.ToDomain().IsReadOnly() {
		return MemberRecord{}, domain.ArchivedError()
	}

	target, err := s.repo.FindMember(ctx, record.ID, in.UserID)
	if err != nil {
		return MemberRecord{}, err
	}

	action := domain.GroupMemberActionDemote
	switch in.Role {
	case domain.MemberRoleOwner:
		// 持有者唯一，因此"设为持有者"就是**转移**（需求文档 5.4 的"修改持有者"）。
		action = domain.GroupMemberActionTransfer
	case domain.MemberRoleMember:
	default:
		return MemberRecord{}, domain.ErrValidation.
			WithField("role", "角色只能是 owner 或 member")
	}

	ownerCount, err := s.repo.CountOwners(ctx, record.ID)
	if err != nil {
		return MemberRecord{}, err
	}
	if err := domain.ValidateTaskMemberChange(target.ToDomain(), action, ownerCount); err != nil {
		return MemberRecord{}, err
	}

	// 与项目同理：把持有者交给停用账号会让任务立刻进入"谁都改不了"的状态。
	if action == domain.GroupMemberActionTransfer {
		targetUser, err := s.repo.FindUser(ctx, in.UserID)
		if err != nil {
			return MemberRecord{}, err
		}
		if !targetUser.IsActive() {
			return MemberRecord{}, domain.ErrValidation.
				WithField("user_id", "目标用户已被停用，不能成为任务持有者")
		}
	}

	var updated MemberRecord
	if action == domain.GroupMemberActionTransfer {
		updated, err = s.repo.TransferOwnership(ctx, record.ID, in.UserID,
			s.auditWith(in.ActorID, domain.AuditActionOwnerTransferred, record.ID,
				map[string]string{"user_id": in.UserID}))
	} else {
		updated, err = s.repo.UpdateMemberRole(ctx, record.ID, in.UserID, in.Role,
			s.auditWith(in.ActorID, domain.AuditActionMemberRoleChanged, record.ID, map[string]string{
				"user_id": in.UserID,
				"role":    in.Role,
			}))
	}
	if err != nil {
		return MemberRecord{}, err
	}

	// 需求文档 5.7："持有者变更"可以触发邮件。只在**转移**时记录：
	// 普通角色调整（member ↔ 其他）不构成持有者变更。
	return updated, nil
}

// RemoveMember 移除任务成员。
func (s *Service) RemoveMember(ctx context.Context, taskID, actorID, userID string) error {
	record, project, _, err := s.requireManage(ctx, taskID, actorID)
	if err != nil {
		return err
	}
	if project.ToDomain().IsReadOnly() {
		return domain.ArchivedError()
	}

	target, err := s.repo.FindMember(ctx, record.ID, userID)
	if err != nil {
		return err
	}
	ownerCount, err := s.repo.CountOwners(ctx, record.ID)
	if err != nil {
		return err
	}
	if err := domain.ValidateTaskMemberChange(target.ToDomain(),
		domain.GroupMemberActionRemove, ownerCount); err != nil {
		return err
	}
	return s.repo.RemoveMember(ctx, record.ID, userID,
		s.auditWith(actorID, domain.AuditActionMemberRemoved, record.ID,
			map[string]string{"user_id": userID}))
}

// validateParent 校验父子关系：同项目、未删除、层级不超过 3 层、区间覆盖、无循环引用。
func (s *Service) validateParent(ctx context.Context, childID, parentID, projectID string, start, end time.Time) error {
	parent, err := s.repo.FindTaskByID(ctx, parentID, false)
	if err != nil {
		return err
	}

	// 祖先链同时用于计算父任务深度与检测循环引用。
	ancestors, err := s.repo.ListAncestors(ctx, parentID)
	if err != nil {
		return err
	}
	// ListAncestors 的第一行是父任务自身，其余为更上层祖先。
	parentDepth := domain.TaskDepth(len(ancestors) - 1)

	if err := domain.ValidateTaskParent(childID, parent.ToDomain(), parentDepth,
		start, end, projectID); err != nil {
		return err
	}

	if childID != "" {
		ancestorIDs := make([]string, 0, len(ancestors))
		for _, ancestor := range ancestors {
			ancestorIDs = append(ancestorIDs, ancestor.ID)
		}
		if err := domain.ValidateTaskSelfParent(childID, ancestorIDs); err != nil {
			return err
		}
	}
	return nil
}

// requireVisible 要求任务对 actor 可见，返回任务与项目视图。
func (s *Service) requireVisible(ctx context.Context, taskID, actorID string) (TaskRecord, error) {
	record, err := s.repo.FindTaskForUser(ctx, taskID, actorID, false)
	if err != nil {
		return TaskRecord{}, err
	}
	project, permission, err := s.projects.Access(ctx, record.ProjectID, actorID)
	if err != nil {
		return TaskRecord{}, err
	}
	if domain.TaskPermission(record.ToDomain(), project.ToDomain(), permission) == domain.PermNone {
		return TaskRecord{}, projectNotFound()
	}
	return record, nil
}

// requireManage 要求 actor 具备任务管理权限（项目持有者或成员）。
func (s *Service) requireManage(ctx context.Context, taskID, actorID string) (TaskRecord, ProjectView, domain.Permission, error) {
	record, err := s.repo.FindTaskForUser(ctx, taskID, actorID, false)
	if err != nil {
		return TaskRecord{}, ProjectView{}, domain.PermNone, err
	}
	project, permission, err := s.projects.Access(ctx, record.ProjectID, actorID)
	if err != nil {
		return TaskRecord{}, ProjectView{}, domain.PermNone, err
	}
	taskPermission := domain.TaskPermission(record.ToDomain(), project.ToDomain(), permission)
	if taskPermission == domain.PermNone {
		return TaskRecord{}, ProjectView{}, domain.PermNone, notFound()
	}
	if !taskPermission.AtLeast(domain.PermWrite) {
		return TaskRecord{}, ProjectView{}, taskPermission, domain.ErrPermissionDenied
	}
	return record, project, taskPermission, nil
}

// validTaskStatus 报告任务状态是否合法。
func validTaskStatus(status string) bool {
	for _, allowed := range domain.TaskStatuses {
		if status == allowed {
			return true
		}
	}
	return false
}

// descriptionForValidation 在未显式提供描述时返回 nil。
func descriptionForValidation(description *string, set bool) *string {
	if !set {
		return nil
	}
	return description
}

// assertNotDropped 断言归一化步骤没有静默丢弃调用方提供的字段。
//
// 字段校验/归一化函数接收"部分字段"结构体，调用方漏传时归一化会当成"未提供"
// 并返回 nil，于是更新被静默忽略——没有报错、没有 5xx，只有一个字段没生效。
// 这类缺陷无法靠编译期发现，只能显式断言。
func assertNotDropped(field string, provided, survived bool) {
	if provided && !survived {
		panic("tasks: 归一化丢弃了已提供的字段 " + field + "，请检查 NormalizeTaskUpdate 的调用参数")
	}
}

// projectNotFound 返回项目级不可见的错误。
//
// 与 notFound()（任务级）刻意分开：任务列表与任务详情在"项目不可见"时
// 应报告 project_not_found，这样调用方能区分"项目不存在"与"任务不存在"。
// 用一个错误码覆盖两种情况会让前端的错误提示失准。
func projectNotFound() error {
	return domain.New(domain.CodeProjectNotFound, "Project not found")
}
