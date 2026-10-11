package tasks

import (
	"errors"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/httpapi"
	"github.com/myonku/project-builder/internal/listquery"
	"github.com/myonku/project-builder/internal/platform/idgen"
)

// ActorLoader 把一次请求解析为当前用户。实现见 app/auth 的 Handler.LoadActor。
type ActorLoader interface {
	LoadActor(c *gin.Context) (domain.User, error)
}

// Handler 装配任务模块的 HTTP 端点。
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
// 项目下的任务集合端点在 /projects/{project_id}/tasks，
// 单任务端点按接口契约使用 /tasks/{task_id}。两组路径指向同一模块，
// 因此都注册在 api 上。
func (h *Handler) Register(api *gin.RouterGroup) {
	// 项目下的任务集合。
	api.POST("/projects/:project_id/tasks", h.create)
	api.POST("/projects/:project_id/tasks/query", h.list)

	// 单任务。
	task := api.Group("/tasks")
	task.GET("/:task_id", h.detail)
	task.PATCH("/:task_id", h.update)
	task.DELETE("/:task_id", h.delete)
	task.POST("/:task_id/copy", h.copy)
	task.PATCH("/:task_id/dates", h.updateDates)

	task.POST("/:task_id/members/query", h.listMembers)
	task.POST("/:task_id/members", h.addMember)
	task.PATCH("/:task_id/members/:user_id", h.changeMemberRole)
	task.DELETE("/:task_id/members/:user_id", h.removeMember)
}

// ---------------------------------------------------------------------------
// 请求与响应结构
// ---------------------------------------------------------------------------

// createRequest 对应接口契约 4.6 POST /projects/{project_id}/tasks。
type createRequest struct {
	Name        string       `json:"name" validate:"required"`
	StartDate   httpapi.Date `json:"start_date"`
	EndDate     httpapi.Date `json:"end_date"`
	Description *string      `json:"description"`
	ParentID    *string      `json:"parent_id"`
	Priority    *int         `json:"priority"`
	IsMilestone *bool        `json:"is_milestone"`
	IsShared    *bool        `json:"is_shared"`
}

// updateRequest 对应 PATCH /tasks/{task_id}。
//
// 字段集合严格限定为契约 4.6 列出的 9 项。
// expected_updated_at 必填：契约 2.6 要求更新核心对象提交版本。
type updateRequest struct {
	Name            *string            `json:"name"`
	Description     *string            `json:"description"`
	Status          *string            `json:"status"`
	Progress        *int               `json:"progress"`
	Priority        *int               `json:"priority"`
	StartDate       *httpapi.Date      `json:"start_date"`
	EndDate         *httpapi.Date      `json:"end_date"`
	IsMilestone     *bool              `json:"is_milestone"`
	IsShared        *bool              `json:"is_shared"`
	ParentID        *string            `json:"parent_id"`
	ExpectedUpdated *httpapi.Timestamp `json:"expected_updated_at" validate:"required"`
}

// updateDatesRequest 对应 PATCH /tasks/{task_id}/dates（甘特图日期编辑）。
//
// 只包含日期与版本，**不接受**名称/状态/进度等字段。
// Python 版本该端点复用了全量任务更新结构，导致"仅日期"端点能改名称与状态。
type updateDatesRequest struct {
	StartDate       httpapi.Date       `json:"start_date"`
	EndDate         httpapi.Date       `json:"end_date"`
	ExpectedUpdated *httpapi.Timestamp `json:"expected_updated_at" validate:"required"`
}

// deleteRequest 对应 DELETE /tasks/{task_id} 的请求体。
//
// DELETE 携带请求体不是常见做法，但契约 2.6 要求删除也校验版本，
// 因此显式要求 expected_updated_at。
type deleteRequest struct {
	ExpectedUpdated *httpapi.Timestamp `json:"expected_updated_at" validate:"required"`
}

// queryRequest 对应 POST /projects/{project_id}/tasks/query。
type queryRequest struct {
	Status         *string `json:"status"`
	AssigneeID     *string `json:"assignee_id"`
	OwnerID        *string `json:"owner_id"`
	IsShared       *bool   `json:"is_shared"`
	IncludeDeleted *bool   `json:"include_deleted"`
	listquery.PageRequest
	listquery.SortRequest
}

// copyRequest 对应 POST /tasks/{task_id}/copy。
type copyRequest struct {
	Name        *string       `json:"name"`
	Description *string       `json:"description"`
	StartDate   *httpapi.Date `json:"start_date"`
	EndDate     *httpapi.Date `json:"end_date"`
	IsShared    *bool         `json:"is_shared"`
}

// memberQueryRequest 对应 POST /tasks/{task_id}/members/query。
type memberQueryRequest struct {
	Role *string `json:"role"`
	listquery.PageRequest
	listquery.SortRequest
}

// addMemberRequest 对应 POST /tasks/{task_id}/members。
type addMemberRequest struct {
	UserID string `json:"user_id" validate:"required"`
}

// changeMemberRoleRequest 对应 PATCH /tasks/{task_id}/members/{user_id}。
type changeMemberRoleRequest struct {
	Role string `json:"role" validate:"required"`
}

// taskResponse 是任务对外表示（接口契约 3.3）。
type taskResponse struct {
	ID          string  `json:"id"`
	ProjectID   string  `json:"project_id"`
	ParentID    *string `json:"parent_id"`
	CreatedBy   string  `json:"created_by"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Status      string  `json:"status"`
	Progress    int     `json:"progress"`
	Priority    int     `json:"priority"`
	StartDate   string  `json:"start_date"`
	EndDate     string  `json:"end_date"`
	IsMilestone bool    `json:"is_milestone"`
	IsShared    bool    `json:"is_shared"`

	// IsOverdue 是派生展示字段，不持久化（需求文档 5.4）。
	IsOverdue bool `json:"is_overdue"`

	DeletedAt *string `json:"deleted_at"`

	// MembershipRole 是当前用户在本任务的成员角色；非任务成员时为 null。
	MembershipRole *string `json:"membership_role"`

	// UpdatedBy 是最近一次修改本任务的用户（需求文档 5.6）。
	//
	// 与 updated_at 一起推进：两者描述同一个事件。这里只给出 ID；
	// 展示用的显示名由计划视图（plan-views）在列表查询里一并 JOIN 出来，
	// 因为那是一次查询返回整个视图，而单任务响应没有必要为此多查一次用户表。
	UpdatedBy string `json:"updated_by"`

	UpdatedAt string `json:"updated_at"`
}

// memberResponse 是任务成员对外表示。
type memberResponse struct {
	ID          string `json:"id"`
	TaskID      string `json:"task_id"`
	UserID      string `json:"user_id"`
	Role        string `json:"role"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

// newTaskResponse 把仓储记录投影为对外结构。
//
// today 由调用方按**请求者时区**计算后传入（Service.TodayFor）：
// 逾期是派生展示字段，其判定基准必须跟随用户时区，而不是服务器 UTC 日历日
// （需求文档 5.7）。
func newTaskResponse(record TaskRecord, today time.Time) taskResponse {
	var deletedAt *string
	if record.DeletedAt != nil {
		formatted := domain.FormatTimestamp(*record.DeletedAt)
		deletedAt = &formatted
	}
	response := taskResponse{
		ID:          record.ID,
		ProjectID:   record.ProjectID,
		ParentID:    record.ParentID,
		CreatedBy:   record.CreatedBy,
		Name:        record.Name,
		Description: record.Description,
		Status:      record.Status,
		Progress:    record.Progress,
		Priority:    record.Priority,
		StartDate:   domain.FormatDate(record.StartDate),
		EndDate:     domain.FormatDate(record.EndDate),
		IsMilestone: record.IsMilestone,
		IsShared:    record.IsShared,
		IsOverdue:   domain.IsOverdue(record.EndDate, record.Status, today),
		DeletedAt:   deletedAt,
		UpdatedBy:   record.UpdatedBy,
		UpdatedAt:   domain.FormatTimestamp(record.UpdatedAt),
	}
	if record.Membership != nil {
		role := record.Membership.Role
		response.MembershipRole = &role
	}
	return response
}

// newMemberResponse 把成员记录投影为对外结构。
func newMemberResponse(record MemberRecord) memberResponse {
	return memberResponse{
		ID:          record.ID,
		TaskID:      record.TaskID,
		UserID:      record.UserID,
		Role:        record.Role,
		Username:    record.Username,
		DisplayName: record.DisplayName,
		Email:       record.Email,
	}
}

// ---------------------------------------------------------------------------
// 处理器
// ---------------------------------------------------------------------------

// create 处理 POST /projects/{project_id}/tasks。
func (h *Handler) create(c *gin.Context) {
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

	var request createRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}

	created, err := h.service.Create(c.Request.Context(), CreateInput{
		ProjectID:   projectID,
		CreatorID:   actor.ID,
		Name:        request.Name,
		Description: request.Description,
		ParentID:    request.ParentID,
		StartDate:   request.StartDate.Time,
		EndDate:     request.EndDate.Time,
		Priority:    request.Priority,
		IsMilestone: request.IsMilestone,
		IsShared:    request.IsShared,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondCreated(c, newTaskResponse(created, h.service.TodayFor(actor)))
}

// detail 处理 GET /tasks/{task_id}。
func (h *Handler) detail(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	taskID, err := requireUUIDParam(c, "task_id")
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	record, err := h.service.Detail(c.Request.Context(), taskID, actor.ID, false)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondOK(c, newTaskResponse(record, h.service.TodayFor(actor)))
}

// list 处理 POST /projects/{project_id}/tasks/query。
func (h *Handler) list(c *gin.Context) {
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

	var request queryRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}

	includeDeleted := false
	if request.IncludeDeleted != nil {
		includeDeleted = *request.IncludeDeleted
	}

	page, err := h.service.List(c.Request.Context(), ListInput{
		ProjectID:      projectID,
		ActorID:        actor.ID,
		Status:         request.Status,
		AssigneeID:     request.AssigneeID,
		OwnerID:        request.OwnerID,
		IsShared:       request.IsShared,
		IncludeDeleted: includeDeleted,
		Page:           request.PageRequest,
		Sort:           request.Sort,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	today := h.service.TodayFor(actor)
	items := make([]taskResponse, 0, len(page.Items))
	for _, record := range page.Items {
		items = append(items, newTaskResponse(record, today))
	}
	httpapi.RespondList(c, items, httpapi.NewPagination(page.Page.Page, page.Page.PageSize, page.Total))
}

// update 处理 PATCH /tasks/{task_id}。
func (h *Handler) update(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	taskID, err := requireUUIDParam(c, "task_id")
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	var request updateRequest
	body, err := httpapi.BindJSONWithBody(c, &request)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	input := UpdateInput{
		TaskID:          taskID,
		ActorID:         actor.ID,
		Name:            request.Name,
		Description:     request.Description,
		DescriptionSet:  httpapi.HasKey(body, "description"),
		Status:          request.Status,
		Progress:        request.Progress,
		Priority:        request.Priority,
		IsMilestone:     request.IsMilestone,
		IsShared:        request.IsShared,
		ExpectedUpdated: request.ExpectedUpdated.Time,
	}
	if request.StartDate != nil {
		input.StartDate = &request.StartDate.Time
	}
	if request.EndDate != nil {
		input.EndDate = &request.EndDate.Time
	}
	// parent_id 的语义：显式 null 表示"提升为顶层任务"，省略表示"不修改"。
	// 该区分无法仅凭解码后的值判断，必须借助原始报文。
	if httpapi.HasKey(body, "parent_id") {
		input.ParentIDSet = true
		input.ParentID = request.ParentID
	}

	updated, err := h.service.Update(c.Request.Context(), input)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondOK(c, newTaskResponse(updated, h.service.TodayFor(actor)))
}

// updateDates 处理 PATCH /tasks/{task_id}/dates。
func (h *Handler) updateDates(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	taskID, err := requireUUIDParam(c, "task_id")
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	var request updateDatesRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}

	updated, err := h.service.UpdateDates(c.Request.Context(), UpdateDatesInput{
		TaskID:          taskID,
		ActorID:         actor.ID,
		StartDate:       request.StartDate.Time,
		EndDate:         request.EndDate.Time,
		ExpectedUpdated: request.ExpectedUpdated.Time,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondOK(c, newTaskResponse(updated, h.service.TodayFor(actor)))
}

// delete 处理 DELETE /tasks/{task_id}（逻辑删除）。
func (h *Handler) delete(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	taskID, err := requireUUIDParam(c, "task_id")
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	var request deleteRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}

	if _, err := h.service.Delete(c.Request.Context(), DeleteInput{
		TaskID:          taskID,
		ActorID:         actor.ID,
		ExpectedUpdated: request.ExpectedUpdated.Time,
	}); err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondNoContent(c)
}

// copy 处理 POST /tasks/{task_id}/copy。
//
// 幂等语义（契约 2.6）：
//   - 首次请求创建任务并返回 201；
//   - 重复请求返回既有任务与 200，**不再创建**第二个任务。
//
// 2xx 掩盖不了副作用，因此状态码必须区分：201 表示"确实创建了"，
// 200 表示"重放，未产生新对象"。
func (h *Handler) copy(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	taskID, err := requireUUIDParam(c, "task_id")
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	var request copyRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}

	input := CopyInput{
		TaskID:         taskID,
		ActorID:        actor.ID,
		IdempotencyKey: c.GetHeader("Idempotency-Key"),
		Name:           request.Name,
		Description:    request.Description,
		IsShared:       request.IsShared,
	}
	if request.StartDate != nil {
		input.StartDate = &request.StartDate.Time
	}
	if request.EndDate != nil {
		input.EndDate = &request.EndDate.Time
	}

	created, replayed, err := h.service.Copy(c.Request.Context(), input)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	response := newTaskResponse(created, h.service.TodayFor(actor))
	if replayed {
		httpapi.RespondOK(c, response)
		return
	}
	httpapi.RespondCreated(c, response)
}

// listMembers 处理 POST /tasks/{task_id}/members/query。
func (h *Handler) listMembers(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	taskID, err := requireUUIDParam(c, "task_id")
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	var request memberQueryRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}

	page, err := h.service.ListMembers(c.Request.Context(), MemberListInput{
		TaskID:  taskID,
		ActorID: actor.ID,
		Role:    request.Role,
		Page:    request.PageRequest,
		Sort:    request.Sort,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	items := make([]memberResponse, 0, len(page.Items))
	for _, record := range page.Items {
		items = append(items, newMemberResponse(record))
	}
	httpapi.RespondList(c, items, httpapi.NewPagination(page.Page.Page, page.Page.PageSize, page.Total))
}

// addMember 处理 POST /tasks/{task_id}/members。
func (h *Handler) addMember(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	taskID, err := requireUUIDParam(c, "task_id")
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	var request addMemberRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}

	member, err := h.service.AddMember(c.Request.Context(), AddMemberInput{
		TaskID:  taskID,
		ActorID: actor.ID,
		UserID:  request.UserID,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondCreated(c, newMemberResponse(member))
}

// changeMemberRole 处理 PATCH /tasks/{task_id}/members/{user_id}。
func (h *Handler) changeMemberRole(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	taskID, err := requireUUIDParam(c, "task_id")
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	userID, err := requireUUIDParam(c, "user_id")
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	var request changeMemberRoleRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}

	member, err := h.service.ChangeMemberRole(c.Request.Context(), ChangeMemberInput{
		TaskID:  taskID,
		ActorID: actor.ID,
		UserID:  userID,
		Role:    request.Role,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondOK(c, newMemberResponse(member))
}

// removeMember 处理 DELETE /tasks/{task_id}/members/{user_id}。
func (h *Handler) removeMember(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	taskID, err := requireUUIDParam(c, "task_id")
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	userID, err := requireUUIDParam(c, "user_id")
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	if err := h.service.RemoveMember(c.Request.Context(), taskID, actor.ID, userID); err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondNoContent(c)
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
var errNoActorLoader = errors.New("tasks: 未注入 ActorLoader")

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
