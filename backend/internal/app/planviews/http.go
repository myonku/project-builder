package planviews

import (
	"errors"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/httpapi"
	"github.com/myonku/project-builder/internal/platform/idgen"
)

// ActorLoader 把会话 Cookie 解析为当前用户。实现见 app/auth 的 Handler.LoadActor。
type ActorLoader interface {
	LoadActor(c *gin.Context) (domain.User, error)
}

// Handler 装配计划视图的三个只读端点。
type Handler struct {
	service     *Service
	actorLoader ActorLoader
	logger      *slog.Logger
}

// NewHandler 构造 HTTP 处理器。
func NewHandler(service *Service, actors ActorLoader, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{service: service, actorLoader: actors, logger: logger}
}

// Register 把本模块端点挂载到版本化路由组之下。
//
// 三个端点都是 GET：契约 4.7 明确规定了这三个路径，因此筛选参数走查询字符串。
// 若筛选参数继续增多，按契约 2.5 应改为 POST .../query —— 届时先改契约再改代码。
func (h *Handler) Register(api *gin.RouterGroup) {
	group := api.Group("/plan-views")

	group.GET("/personal", h.personal)
	group.GET("/groups/:group_id", h.group)
	group.GET("/projects/:project_id/gantt", h.gantt)
}

// 允许的查询参数。
//
// 未知参数一律拒绝（与契约 2.7 对请求体的要求同源）：把 status 拼成 statuss
// 会被静默忽略，客户端以为筛选生效了，实际拿到的是全量数据——
// 这类"看起来对了"的错误最难发现。
var (
	personalParams = []string{
		"project_id", "project_status", "project_owner_id",
		"task_status", "task_owner_id", "task_member_id", "priority",
		"due_from", "due_to", "overdue", "include_archived",
	}
	groupParams = personalParams
	// 甘特图是"一个项目的完整画面"，不接受筛选：任何筛选都会让画面不完整。
	ganttParams = []string{}
)

// ---------------------------------------------------------------------------
// 响应结构
// ---------------------------------------------------------------------------

// projectResponse 是项目在计划视图中的对外表示。
type projectResponse struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	GroupID     string  `json:"group_id"`
	Status      string  `json:"status"`
	Visibility  string  `json:"visibility"`
	StartDate   string  `json:"start_date"`
	EndDate     string  `json:"end_date"`
	Color       *string `json:"color"`
	ArchivedAt  *string `json:"archived_at"`
	// IsArchived 与 IsReadOnly 是派生布尔值，省得每个客户端各自判断
	// "status == archived 还是 archived_at != null"。
	IsArchived bool `json:"is_archived"`
	IsReadOnly bool `json:"is_read_only"`

	OwnerID   *string `json:"owner_id"`
	MyRole    *string `json:"my_role"`
	TaskCount int     `json:"task_count"`
	// Progress 与 OverdueTaskCount 基于**本视图可见任务**（见 service 中的说明）。
	Progress         int `json:"progress"`
	OverdueTaskCount int `json:"overdue_task_count"`

	UpdatedAt string `json:"updated_at"`
}

// memberResponse 是任务成员的对外表示。
type memberResponse struct {
	UserID          string  `json:"user_id"`
	Username        string  `json:"username"`
	DisplayName     string  `json:"display_name"`
	Role            string  `json:"role"`
	AvatarUpdatedAt *string `json:"avatar_updated_at"`
}

// taskResponse 是任务在计划视图中的对外表示。
type taskResponse struct {
	HasChildren bool    `json:"has_children"`
	ID          string  `json:"id"`
	ProjectID   string  `json:"project_id"`
	ParentID    *string `json:"parent_id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Status      string  `json:"status"`
	Progress    int     `json:"progress"`
	Priority    int     `json:"priority"`
	StartDate   string  `json:"start_date"`
	EndDate     string  `json:"end_date"`
	IsMilestone bool    `json:"is_milestone"`
	IsShared    bool    `json:"is_shared"`
	IsOverdue   bool    `json:"is_overdue"`
	// CanEdit 是"当前用户能否修改该任务"的唯一权威回答。
	// 前端据此决定是否允许拖拽改日期，但服务端写入口仍会独立校验一次。
	CanEdit bool `json:"can_edit"`

	Owner   *memberResponse  `json:"owner"`
	Members []memberResponse `json:"members"`

	// UpdatedBy 与 UpdatedByName 满足需求文档 5.6"查看任务最后修改人和最后修改时间"。
	UpdatedBy     string  `json:"updated_by"`
	UpdatedByName *string `json:"updated_by_name"`
	UpdatedAt     string  `json:"updated_at"`
}

// summaryResponse 是计划视图的汇总统计。
type summaryResponse struct {
	ProjectCount        int `json:"project_count"`
	TaskCount           int `json:"task_count"`
	InProgressTaskCount int `json:"in_progress_task_count"`
	OverdueTaskCount    int `json:"overdue_task_count"`
	OverallProgress     int `json:"overall_progress"`
}

// viewResponse 是个人/组内计划视图的响应。
type viewResponse struct {
	GroupID  *string           `json:"group_id"`
	Projects []projectResponse `json:"projects"`
	Tasks    []taskResponse    `json:"tasks"`
	Summary  summaryResponse   `json:"summary"`
}

// ganttResponse 是项目甘特图的响应。
type ganttResponse struct {
	Project projectResponse `json:"project"`
	// CanManageProject / CanEditTasks 让前端在进入编辑态之前就知道边界：
	// 归档项目与组内只读共享都不允许写。
	CanManageProject bool            `json:"can_manage_project"`
	CanEditTasks     bool            `json:"can_edit_tasks"`
	Tasks            []taskResponse  `json:"tasks"`
	Summary          summaryResponse `json:"summary"`
}

// newProjectResponse 把内部投影转为对外结构。
func newProjectResponse(view ProjectView) projectResponse {
	record := view.Record
	project := record.ToDomain()

	response := projectResponse{
		ID:               record.ID,
		Name:             record.Name,
		Description:      record.Description,
		GroupID:          record.GroupID,
		Status:           record.Status,
		Visibility:       record.Visibility,
		StartDate:        domain.FormatDate(record.StartDate),
		EndDate:          domain.FormatDate(record.EndDate),
		Color:            record.Color,
		IsArchived:       project.IsArchived(),
		IsReadOnly:       project.IsReadOnly(),
		OwnerID:          record.OwnerID,
		TaskCount:        view.TaskCount,
		Progress:         view.Progress,
		OverdueTaskCount: view.OverdueTaskCount,
		UpdatedAt:        domain.FormatTimestamp(record.UpdatedAt),
	}
	if record.ArchivedAt != nil {
		formatted := domain.FormatTimestamp(*record.ArchivedAt)
		response.ArchivedAt = &formatted
	}
	if strings.TrimSpace(view.MyRole) != "" {
		role := view.MyRole
		response.MyRole = &role
	}
	return response
}

// newTaskResponse 把内部投影转为对外结构。
func newTaskResponse(view TaskView) taskResponse {
	record := view.Record

	response := taskResponse{
		HasChildren:   record.HasChildren,
		ID:            record.ID,
		ProjectID:     record.ProjectID,
		ParentID:      record.ParentID,
		Name:          record.Name,
		Description:   record.Description,
		Status:        record.Status,
		Progress:      record.Progress,
		Priority:      record.Priority,
		StartDate:     domain.FormatDate(record.StartDate),
		EndDate:       domain.FormatDate(record.EndDate),
		IsMilestone:   record.IsMilestone,
		IsShared:      record.IsShared,
		IsOverdue:     view.IsOverdue,
		CanEdit:       view.CanEdit,
		Members:       make([]memberResponse, 0, len(record.Members)),
		UpdatedBy:     record.UpdatedBy,
		UpdatedByName: record.UpdatedByName,
		UpdatedAt:     domain.FormatTimestamp(record.UpdatedAt),
	}

	for _, member := range record.Members {
		response.Members = append(response.Members, memberResponse{
			UserID:          member.UserID,
			Username:        member.Username,
			DisplayName:     member.DisplayName,
			Role:            member.Role,
			AvatarUpdatedAt: domain.FormatOptionalTimestamp(member.AvatarUpdatedAt),
		})
	}
	if owner := record.Owner(); owner != nil {
		response.Owner = &memberResponse{
			UserID:          owner.UserID,
			Username:        owner.Username,
			DisplayName:     owner.DisplayName,
			Role:            owner.Role,
			AvatarUpdatedAt: domain.FormatOptionalTimestamp(owner.AvatarUpdatedAt),
		}
	}
	return response
}

// newSummaryResponse 转换汇总统计。
func newSummaryResponse(summary Summary) summaryResponse {
	return summaryResponse{
		ProjectCount:        summary.ProjectCount,
		TaskCount:           summary.TaskCount,
		InProgressTaskCount: summary.InProgressTaskCount,
		OverdueTaskCount:    summary.OverdueTaskCount,
		OverallProgress:     summary.OverallProgress,
	}
}

// ---------------------------------------------------------------------------
// 处理器
// ---------------------------------------------------------------------------

// personal 处理 GET /plan-views/personal。
func (h *Handler) personal(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	filter, err := parseFilter(c, personalParams)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	view, err := h.service.Personal(c.Request.Context(), actor.ID, filter)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondOK(c, newViewResponse(view))
}

// group 处理 GET /plan-views/groups/{group_id}。
func (h *Handler) group(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	groupID, err := requireUUIDParam(c, "group_id")
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	filter, err := parseFilter(c, groupParams)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	view, err := h.service.Group(c.Request.Context(), actor.ID, groupID, filter)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondOK(c, newViewResponse(view))
}

// gantt 处理 GET /plan-views/projects/{project_id}/gantt。
func (h *Handler) gantt(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	projectID, err := requireUUIDParam(c, "project_id")
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	if err := rejectUnknownParams(c, ganttParams); err != nil {
		httpapi.Abort(c, err)
		return
	}

	view, err := h.service.Gantt(c.Request.Context(), actor.ID, projectID)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	tasks := make([]taskResponse, 0, len(view.Tasks))
	for _, task := range view.Tasks {
		tasks = append(tasks, newTaskResponse(task))
	}

	httpapi.RespondOK(c, ganttResponse{
		Project:          newProjectResponse(view.Project),
		CanManageProject: view.CanManageProject,
		CanEditTasks:     view.CanEditTasks,
		Tasks:            tasks,
		Summary:          newSummaryResponse(view.Summary),
	})
}

// newViewResponse 转换个人/组内视图。
func newViewResponse(view View) viewResponse {
	projects := make([]projectResponse, 0, len(view.Projects))
	for _, project := range view.Projects {
		projects = append(projects, newProjectResponse(project))
	}
	tasks := make([]taskResponse, 0, len(view.Tasks))
	for _, task := range view.Tasks {
		tasks = append(tasks, newTaskResponse(task))
	}

	response := viewResponse{
		Projects: projects,
		Tasks:    tasks,
		Summary:  newSummaryResponse(view.Summary),
	}
	if strings.TrimSpace(view.GroupID) != "" {
		groupID := view.GroupID
		response.GroupID = &groupID
	}
	return response
}

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

// actor 解析当前用户。
func (h *Handler) actor(c *gin.Context) (domain.User, error) {
	if h.actorLoader == nil {
		return domain.User{}, domain.ErrInternal.WithCause(errNoActorLoader)
	}
	return h.actorLoader.LoadActor(c)
}

// errNoActorLoader 表示装配遗漏。
var errNoActorLoader = errors.New("planviews: 未注入 ActorLoader")

// parseFilter 解析并校验查询参数。
func parseFilter(c *gin.Context, allowed []string) (Filter, error) {
	if err := rejectUnknownParams(c, allowed); err != nil {
		return Filter{}, err
	}

	filter := Filter{}
	fields := map[string]string{}

	// 把解析失败收集到一处，一次性返回全部字段错误（契约：不要逐次往返修正）。
	parseUUID := func(name string, target **string) {
		value := c.Query(name)
		if value == "" {
			return
		}
		if !idgen.IsUUID(value) {
			fields[name] = "必须是合法的 UUID 字符串"
			return
		}
		*target = &value
	}
	parseDate := func(name string, target **time.Time) {
		value := c.Query(name)
		if value == "" {
			return
		}
		parsed, err := domain.ParseDate(value)
		if err != nil {
			fields[name] = "日期格式必须为 YYYY-MM-DD"
			return
		}
		*target = &parsed
	}
	parseBool := func(name string) *bool {
		value := c.Query(name)
		if value == "" {
			return nil
		}
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			fields[name] = "布尔值只能是 true 或 false"
			return nil
		}
		return &parsed
	}

	parseUUID("project_id", &filter.Project.ProjectID)
	parseUUID("project_owner_id", &filter.Project.OwnerID)
	parseUUID("task_owner_id", &filter.Task.OwnerID)
	parseUUID("task_member_id", &filter.Task.MemberID)
	parseDate("due_from", &filter.Task.DueFrom)
	parseDate("due_to", &filter.Task.DueTo)
	filter.Task.Overdue = parseBool("overdue")
	if includeArchived := parseBool("include_archived"); includeArchived != nil {
		filter.Project.IncludeArchived = *includeArchived
	}

	if value := c.Query("project_status"); value != "" {
		filter.Project.Status = &value
	}
	if value := c.Query("task_status"); value != "" {
		filter.Task.Status = &value
	}
	if value := c.Query("priority"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			fields["priority"] = "优先级必须是整数"
		} else {
			filter.Task.Priority = &parsed
		}
	}

	if len(fields) > 0 {
		return Filter{}, domain.ErrInvalidRequest.WithFields(fields)
	}

	// 与项目列表一致的第二层校验：取值范围的合法性集中在 service 里，
	// 避免 http 层与 service 层各写一套白名单。
	if err := ValidateFilter(filter); err != nil {
		return Filter{}, err
	}
	return filter, nil
}

// rejectUnknownParams 拒绝契约未定义的查询参数。
//
// 理由与契约 2.7（拒绝未知请求体字段）完全相同：把 `task_status` 写成 `status`
// 会被静默忽略，客户端以为筛选生效、实际拿到全量数据，而接口没有任何提示。
func rejectUnknownParams(c *gin.Context, allowed []string) error {
	allowedSet := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = true
	}

	query := c.Request.URL.Query()
	var unknown []string
	for name := range query {
		if !allowedSet[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	// 排序后输出，保证错误信息稳定（便于测试与排查）。
	sort.Strings(unknown)

	hint := "本端点不接受查询参数"
	if len(allowed) > 0 {
		sorted := append([]string(nil), allowed...)
		sort.Strings(sorted)
		hint = "可用参数：" + strings.Join(sorted, ", ")
	}
	fields := map[string]string{}
	for _, name := range unknown {
		fields[name] = "契约未定义的查询参数。" + hint
	}
	return domain.ErrInvalidRequest.WithFields(fields)
}

// requireUUIDParam 校验路径参数是合法 UUID。
//
// 非法 UUID 直接 400 而不是交给数据库：否则会触发 22P02 类型错误并退化为 500。
func requireUUIDParam(c *gin.Context, name string) (string, error) {
	value := c.Param(name)
	if value == "" {
		return "", domain.ErrInvalidRequest.WithField(name, "缺少路径参数")
	}
	if !idgen.IsUUID(value) {
		return "", domain.ErrInvalidRequest.WithField(name, "必须是合法的 UUID 字符串")
	}
	return value, nil
}
