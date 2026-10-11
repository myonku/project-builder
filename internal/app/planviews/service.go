package planviews

import (
	"context"
	"log/slog"
	"time"

	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/platform/clock"
	"github.com/myonku/project-builder/internal/platform/idgen"
)

// Store 是本模块对持久化的需求（只读）。定义在消费方，测试用替身实现。
type Store interface {
	Projects(ctx context.Context, scope Scope, filter Filter) ([]ProjectRecord, error)
	Tasks(ctx context.Context, scope Scope, filter Filter) ([]TaskRecord, error)
	ProjectTasks(ctx context.Context, projectID string, filter TaskFilter) ([]TaskRecord, error)
	FindProject(ctx context.Context, projectID, actorID string) (ProjectRecord, error)
	IsActiveGroupMember(ctx context.Context, groupID, userID string) (bool, error)
}

// Service 承载计划视图用例。
//
// 它是**只读**的：没有写路径、没有版本校验、没有幂等键，因此也不持有 idgen 之外的写依赖。
type Service struct {
	repo      Store
	clk       clock.Clock
	logger    *slog.Logger
	timezones TimezoneProvider
}

// Options 是 Service 的构造参数。
type Options struct {
	Store  Store
	Clock  clock.Clock
	Logger *slog.Logger

	// Timezones 提供按用户时区计算"今天"的能力（派生 is_overdue 与逾期筛选）。
	// 由装配点注入 app/users；未注入时回退到 UTC 日历日。
	Timezones TimezoneProvider
}

// TimezoneProvider 按用户 ID 返回其 IANA 时区。
//
// 与 projects 模块声明的是同一个形状：消费方各自声明自己需要的最小能力，
// 装配点注入同一个实现，模块之间不产生依赖边。
type TimezoneProvider interface {
	TimezoneOf(ctx context.Context, userID string) (string, error)
}

// NewService 构造计划视图服务。
func NewService(opts Options) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	clk := opts.Clock
	if clk == nil {
		clk = clock.NewSystem()
	}
	return &Service{repo: opts.Store, clk: clk, logger: logger, timezones: opts.Timezones}
}

// Summary 是计划视图的汇总统计。
//
// 全部由**返回给客户端的同一个切片**算出，因此不存在"统计与列表不一致"的空间。
type Summary struct {
	ProjectCount        int `json:"project_count"`
	TaskCount           int `json:"task_count"`
	InProgressTaskCount int `json:"in_progress_task_count"`
	OverdueTaskCount    int `json:"overdue_task_count"`
	OverallProgress     int `json:"overall_progress"`
}

// ProjectView 是项目在计划视图中的对外投影。
type ProjectView struct {
	Record ProjectRecord
	// MyRole 是当前用户在该项目中的成员角色；空串表示不是项目成员（组内访客）。
	MyRole string
	// Progress 与 OverdueTaskCount 是基于**本视图可见任务**计算的派生字段。
	//
	// 注意与项目详情（GET /projects/{id}）的差异：后者基于项目**全部**任务。
	// 组内访客只能看到已共享任务，若按全部任务计算就会泄漏未共享任务的进度，
	// 因此这里刻意基于可见任务计算。该差异已在契约 4.7 中声明。
	Progress         int
	OverdueTaskCount int
	TaskCount        int
}

// TaskView 是任务在计划视图中的对外投影。
type TaskView struct {
	Record    TaskRecord
	IsOverdue bool
	// CanEdit 直接来自 domain.TaskPermission：与 tasks 模块的写入口使用的是同一个判定。
	CanEdit bool
}

// Personal 返回个人计划视图（接口契约 4.7 GET /plan-views/personal）。
//
// 范围定义：**我是项目成员的项目**及其任务。
//
// 为什么不再单独按 created_by 放宽：项目创建者默认成为持有者，因此"我创建的项目"
// 在正常情形下就是"我是成员的项目"。若额外把 created_by = 我 也算进来，就会出现
// "我把项目移交出去并被移出成员后仍能看到它"的越权读取——而按 domain.ProjectPermission
// 我此时对该项目的权限是 PermNone。宁可以成员关系为准。
func (s *Service) Personal(
	ctx context.Context, actorID string, filter Filter,
) (View, error) {
	today := s.today(ctx, actorID)
	resolved := filter.withToday(today)

	projects, err := s.repo.Projects(ctx, Scope{ActorID: actorID}, resolved)
	if err != nil {
		return View{}, err
	}
	tasks, err := s.repo.Tasks(ctx, Scope{ActorID: actorID}, resolved)
	if err != nil {
		return View{}, err
	}

	return s.assemble(actorID, today, projects, tasks), nil
}

// Group 返回组内共享计划视图（接口契约 4.7 GET /plan-views/groups/{group_id}）。
//
// 非本组活跃成员返回 404 group_not_found，而不是 403：与工作组模块一致，
// 不让任意登录用户用状态码差异探测组是否存在。
func (s *Service) Group(
	ctx context.Context, actorID, groupID string, filter Filter,
) (View, error) {
	member, err := s.repo.IsActiveGroupMember(ctx, groupID, actorID)
	if err != nil {
		return View{}, err
	}
	if !member {
		return View{}, groupNotFound()
	}

	today := s.today(ctx, actorID)
	scope := Scope{ActorID: actorID, GroupID: &groupID}
	resolved := filter.withToday(today)

	projects, err := s.repo.Projects(ctx, scope, resolved)
	if err != nil {
		return View{}, err
	}
	tasks, err := s.repo.Tasks(ctx, scope, resolved)
	if err != nil {
		return View{}, err
	}

	view := s.assemble(actorID, today, projects, tasks)
	view.GroupID = groupID
	return view, nil
}

// Gantt 返回项目甘特图（接口契约 4.7 GET /plan-views/projects/{project_id}/gantt）。
func (s *Service) Gantt(ctx context.Context, actorID, projectID string) (Gantt, error) {
	today := s.today(ctx, actorID)

	record, err := s.repo.FindProject(ctx, projectID, actorID)
	if err != nil {
		return Gantt{}, err
	}

	project := record.ToDomain()
	permission := domain.ProjectPermission(
		project,
		domain.User{ID: actorID},
		record.projectMember(actorID),
		record.group(),
		record.groupMembership(actorID),
	)
	if permission == domain.PermNone {
		// 不可见与不存在使用同一个错误码，避免泄漏项目存在性。
		return Gantt{}, projectNotFound()
	}

	tasks, err := s.repo.ProjectTasks(ctx, projectID, TaskFilter{Today: today})
	if err != nil {
		return Gantt{}, err
	}

	visible := make([]TaskView, 0, len(tasks))
	for _, task := range tasks {
		taskPermission := domain.TaskPermission(task.ToDomain(), project, permission)
		if taskPermission == domain.PermNone {
			// 非项目成员的组内访客只能看到已共享任务；
			// 这一步与 tasks 模块列表的可见性完全同源，因为判据是同一个领域函数。
			continue
		}
		visible = append(visible, TaskView{
			Record:    task,
			IsOverdue: domain.IsOverdue(task.EndDate, task.Status, today),
			CanEdit:   taskPermission.AtLeast(domain.PermWrite),
		})
	}

	return Gantt{
		Project: ProjectView{
			Record:    record,
			MyRole:    record.ActorRole,
			Progress:  progressOf(visible),
			TaskCount: len(visible),
			// 逾期数同样基于可见任务。
			OverdueTaskCount: overdueOf(visible, today),
		},
		CanManageProject: domain.CanManageProject(project, domain.User{ID: actorID}, record.projectMember(actorID)),
		CanEditTasks:     domain.CanEditProjectTasks(project, permission),
		Tasks:            visible,
		Summary:          summarize(1, visible),
	}, nil
}

// View 是个人/组内计划视图的返回结构。
type View struct {
	GroupID  string
	Projects []ProjectView
	Tasks    []TaskView
	Summary  Summary
}

// Gantt 是项目甘特图的返回结构。
type Gantt struct {
	Project          ProjectView
	CanManageProject bool
	CanEditTasks     bool
	Tasks            []TaskView
	Summary          Summary
}

// Filter 是计划视图的筛选条件（项目侧与任务侧分开）。
type Filter struct {
	Project ProjectFilter
	Task    TaskFilter
}

// withToday 把筛选条件补上"今天"。
//
// 逾期筛选依赖"今天"，而"今天"必须按请求者时区计算，
// 因此由用例层算好后注入，而不是让仓储去读时钟。
func (f Filter) withToday(today time.Time) Filter {
	resolved := f
	resolved.Task.Today = today
	return resolved
}

// assemble 把项目与任务收敛为对外视图。
//
// 关键步骤是**逐对象的权限收敛**：SQL 给出的只是候选集，是否可见由 domain 决定。
// 这样"谁能看到什么"在整个系统里只有一份定义（domain 的权限函数），
// 而不是"projects 一套 SQL、tasks 一套 SQL、planviews 再来一套"。
func (s *Service) assemble(
	actorID string, today time.Time, projects []ProjectRecord, tasks []TaskRecord,
) View {
	projectByID := make(map[string]ProjectRecord, len(projects))
	views := make([]ProjectView, 0, len(projects))
	permissions := make(map[string]domain.Permission, len(projects))

	for _, record := range projects {
		project := record.ToDomain()
		permission := domain.ProjectPermission(
			project,
			domain.User{ID: actorID},
			record.projectMember(actorID),
			record.group(),
			record.groupMembership(actorID),
		)
		if permission == domain.PermNone {
			// 范围 SQL 已经与权限规则对齐，走到这里说明两者发生了漂移。
			// 宁可少显示也不要多显示，因此丢弃并记录告警。
			s.logger.Warn("计划视图：项目未通过权限收敛，已从视图中排除",
				slog.String("project_id", record.ID),
				slog.String("actor_id", actorID),
			)
			continue
		}
		projectByID[record.ID] = record
		permissions[record.ID] = permission
		views = append(views, ProjectView{Record: record, MyRole: record.ActorRole})
	}

	// 按项目分组可见任务，用于派生进度与逾期数。
	tasksByProject := map[string][]TaskView{}
	visible := make([]TaskView, 0, len(tasks))
	for _, task := range tasks {
		record, ok := projectByID[task.ProjectID]
		if !ok {
			// 任务所属项目不在项目列表中：视为不一致，丢弃（fail closed）。
			s.logger.Warn("计划视图：任务所属项目不在视图中，已丢弃该任务",
				slog.String("task_id", task.ID),
				slog.String("project_id", task.ProjectID),
				slog.String("actor_id", actorID),
			)
			continue
		}
		project := record.ToDomain()
		taskPermission := domain.TaskPermission(task.ToDomain(), project, permissions[task.ProjectID])
		if taskPermission == domain.PermNone {
			continue
		}
		view := TaskView{
			Record:    task,
			IsOverdue: domain.IsOverdue(task.EndDate, task.Status, today),
			CanEdit:   taskPermission.AtLeast(domain.PermWrite),
		}
		visible = append(visible, view)
		tasksByProject[task.ProjectID] = append(tasksByProject[task.ProjectID], view)
	}

	// 回填派生字段：进度与逾期数基于**该视图内可见的任务**。
	for index := range views {
		projectTasks := tasksByProject[views[index].Record.ID]
		views[index].TaskCount = len(projectTasks)
		views[index].Progress = progressOf(projectTasks)
		views[index].OverdueTaskCount = overdueOf(projectTasks, today)
	}

	return View{
		Projects: views,
		Tasks:    visible,
		Summary:  summarize(len(views), visible),
	}
}

// progressOf 计算一组任务的整体进度。
//
// 直接复用 domain.ComputeProjectProgress：口径（排除已取消、无有效任务为 0）
// 必须与项目详情里的 progress 完全一致，不允许在这里另写一套平均。
func progressOf(tasks []TaskView) int {
	progresses := make([]int, 0, len(tasks))
	statuses := make([]string, 0, len(tasks))
	for _, task := range tasks {
		progresses = append(progresses, task.Record.Progress)
		statuses = append(statuses, task.Record.Status)
	}
	return domain.ComputeProjectProgress(progresses, statuses)
}

// overdueOf 统计一组任务中的逾期数（复用 domain 的同一套派生规则）。
func overdueOf(tasks []TaskView, today time.Time) int {
	endDates := make([]time.Time, 0, len(tasks))
	statuses := make([]string, 0, len(tasks))
	for _, task := range tasks {
		endDates = append(endDates, task.Record.EndDate)
		statuses = append(statuses, task.Record.Status)
	}
	return domain.CountOverdueTasks(endDates, statuses, today)
}

// summarize 计算汇总统计。
func summarize(projectCount int, tasks []TaskView) Summary {
	inProgress := 0
	overdue := 0
	for _, task := range tasks {
		if task.Record.Status == domain.TaskStatusInProgress {
			inProgress++
		}
		if task.IsOverdue {
			overdue++
		}
	}
	return Summary{
		ProjectCount:        projectCount,
		TaskCount:           len(tasks),
		InProgressTaskCount: inProgress,
		OverdueTaskCount:    overdue,
		OverallProgress:     progressOf(tasks),
	}
}

// today 返回按请求者时区计算的"今天"。
//
// 与 projects 模块同一套做法与同一条降级理由：读取失败时回退到 UTC 日历日并告警，
// 因为该值只影响派生展示字段与逾期筛选，不应让整个视图 500。
func (s *Service) today(ctx context.Context, actorID string) time.Time {
	fallback := domain.DayOf(s.clk.Now())
	if s.timezones == nil {
		return fallback
	}
	timezone, err := s.timezones.TimezoneOf(ctx, actorID)
	if err != nil {
		s.logger.Warn("读取用户时区失败，本次按 UTC 计算今天",
			slog.String("user_id", actorID), slog.Any("cause", err))
		return fallback
	}
	today, err := domain.Today(s.clk.Now(), timezone)
	if err != nil {
		s.logger.Warn("用户时区非法，本次按 UTC 计算今天",
			slog.String("user_id", actorID), slog.String("timezone", timezone), slog.Any("cause", err))
		return fallback
	}
	return today
}

// ValidateFilter 校验筛选参数，返回契约规定的 400/422 错误。
//
// 全部按"查询参数"处理，因此取值非法一律 400 invalid_request：
// 与 projects/tasks 的列表筛选保持一致（契约 2.5 把"未知筛选/排序字段"定为 400）。
func ValidateFilter(filter Filter) error {
	fields := map[string]string{}

	checkUUID := func(value *string, name string) {
		if value != nil && !idgen.IsUUID(*value) {
			fields[name] = "必须是合法的 UUID 字符串"
		}
	}
	checkUUID(filter.Project.ProjectID, "project_id")
	checkUUID(filter.Project.OwnerID, "project_owner_id")
	checkUUID(filter.Task.ProjectID, "project_id")
	checkUUID(filter.Task.OwnerID, "task_owner_id")
	checkUUID(filter.Task.MemberID, "task_member_id")

	if filter.Project.Status != nil && !contains(filter.Project.Statuses(), *filter.Project.Status) {
		fields["project_status"] = "项目状态取值非法"
	}
	if filter.Task.Status != nil && !contains(domain.TaskStatuses, *filter.Task.Status) {
		fields["task_status"] = "任务状态取值非法"
	}
	if filter.Task.Priority != nil {
		if err := domain.ValidateTaskPriority(*filter.Task.Priority); err != nil {
			fields["priority"] = "优先级取值非法（只能是 0 低 / 1 普通 / 2 高 / 3 紧急）"
		}
	}
	// 时间范围倒置是请求本身的问题，按契约 2.4 使用 400 invalid_date_range。
	if filter.Task.DueFrom != nil && filter.Task.DueTo != nil {
		if domain.DayOf(*filter.Task.DueTo).Before(domain.DayOf(*filter.Task.DueFrom)) {
			return domain.ErrInvalidDateRange.WithFields(map[string]string{
				"due_from": "开始日期不能晚于结束日期",
				"due_to":   "结束日期不能早于开始日期",
			})
		}
	}

	if len(fields) > 0 {
		return domain.ErrInvalidRequest.WithFields(fields)
	}
	return nil
}

// Statuses 返回项目状态的合法取值集合。
//
// 直接复用 domain 的白名单：新增项目状态时，筛选与写入会同时生效，
// 不会出现"能写不能筛"的漂移。
func (f ProjectFilter) Statuses() []string { return domain.ProjectStatuses }

// contains 报告白名单中是否含该取值。
func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// groupNotFound 返回组内视图的"组不存在或不可见"。
func groupNotFound() error {
	return domain.New(domain.CodeGroupNotFound, "Group not found")
}

// projectNotFound 返回甘特图的"项目不存在或不可见"。
func projectNotFound() error {
	return domain.New(domain.CodeProjectNotFound, "Project not found")
}
