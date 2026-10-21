package projects

import (
	"errors"
	"log/slog"
	"strings"

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

// Handler 装配项目模块的 HTTP 端点。
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
func (h *Handler) Register(api *gin.RouterGroup) {
	group := api.Group("/projects")

	group.POST("", h.create)
	group.POST("/query", h.list)
	group.GET("/:project_id", h.detail)
	group.PATCH("/:project_id", h.update)
	group.POST("/:project_id/archive", h.archive)

	group.POST("/:project_id/members/query", h.listMembers)
	group.POST("/:project_id/members", h.addMember)
	group.PATCH("/:project_id/members/:user_id", h.changeMemberRole)
	group.DELETE("/:project_id/members/:user_id", h.removeMember)
}

// ---------------------------------------------------------------------------
// 请求与响应结构
// ---------------------------------------------------------------------------

// createRequest 对应接口契约 4.5 POST /projects。
type createRequest struct {
	Name        string       `json:"name" validate:"required"`
	GroupID     string       `json:"group_id" validate:"required"`
	StartDate   httpapi.Date `json:"start_date"`
	EndDate     httpapi.Date `json:"end_date"`
	Description *string      `json:"description"`
	Visibility  *string      `json:"visibility"`
	Color       *string      `json:"color"`
}

// updateRequest 对应 PATCH /projects/{project_id}。
//
// expected_updated_at 必填：契约 2.6 要求更新核心对象提交版本。
type updateRequest struct {
	Name            *string            `json:"name"`
	Description     *string            `json:"description"`
	Status          *string            `json:"status"`
	Visibility      *string            `json:"visibility"`
	StartDate       *httpapi.Date      `json:"start_date"`
	EndDate         *httpapi.Date      `json:"end_date"`
	Color           *string            `json:"color"`
	ExpectedUpdated *httpapi.Timestamp `json:"expected_updated_at" validate:"required"`
}

// archiveRequest 对应 POST /projects/{project_id}/archive。
type archiveRequest struct {
	ExpectedUpdated *httpapi.Timestamp `json:"expected_updated_at" validate:"required"`
}

// queryRequest 对应 POST /projects/query。
type queryRequest struct {
	GroupID         *string `json:"group_id"`
	Status          *string `json:"status"`
	OwnerID         *string `json:"owner_id"`
	Name            *string `json:"name"`
	IncludeArchived *bool   `json:"include_archived"`
	listquery.PageRequest
	listquery.SortRequest
}

// memberQueryRequest 对应 POST /projects/{project_id}/members/query。
type memberQueryRequest struct {
	Role *string `json:"role"`
	listquery.PageRequest
	listquery.SortRequest
}

// addMemberRequest 对应 POST /projects/{project_id}/members。
type addMemberRequest struct {
	UserID string `json:"user_id" validate:"required"`
}

// changeMemberRoleRequest 对应 PATCH /projects/{project_id}/members/{user_id}。
type changeMemberRoleRequest struct {
	Role string `json:"role" validate:"required"`
}

// projectResponse 是项目对外表示（接口契约 3.2）。
type projectResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	GroupID string `json:"group_id"`

	Description *string `json:"description"`
	CreatedBy   string  `json:"created_by"`
	Status      string  `json:"status"`
	Visibility  string  `json:"visibility"`
	StartDate   string  `json:"start_date"`
	EndDate     string  `json:"end_date"`
	Color       *string `json:"color"`
	ArchivedAt  *string `json:"archived_at"`

	// 派生字段：查询时计算，不持久化。
	Progress         int `json:"progress"`
	OverdueTaskCount int `json:"overdue_task_count"`

	// MembershipRole 是当前用户在本项目的角色；非项目成员时为 null。
	MembershipRole *string `json:"membership_role"`

	UpdatedAt string `json:"updated_at"`
}

// memberResponse 是项目成员对外表示。
type memberResponse struct {
	ID              string  `json:"id"`
	ProjectID       string  `json:"project_id"`
	UserID          string  `json:"user_id"`
	Role            string  `json:"role"`
	Username        string  `json:"username"`
	DisplayName     string  `json:"display_name"`
	Email           string  `json:"email"`
	AvatarUpdatedAt *string `json:"avatar_updated_at"`
}

// newProjectResponse 把仓储记录投影为对外结构。
func newProjectResponse(record ProjectRecord) projectResponse {
	var archivedAt *string
	if record.ArchivedAt != nil {
		formatted := domain.FormatTimestamp(*record.ArchivedAt)
		archivedAt = &formatted
	}
	response := projectResponse{
		ID:               record.ID,
		Name:             record.Name,
		GroupID:          record.GroupID,
		Description:      record.Description,
		CreatedBy:        record.CreatedBy,
		Status:           record.Status,
		Visibility:       record.Visibility,
		StartDate:        domain.FormatDate(record.StartDate),
		EndDate:          domain.FormatDate(record.EndDate),
		Color:            record.Color,
		ArchivedAt:       archivedAt,
		Progress:         record.Progress,
		OverdueTaskCount: record.OverdueTaskCount,
		UpdatedAt:        domain.FormatTimestamp(record.UpdatedAt),
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
		ID:              record.ID,
		ProjectID:       record.ProjectID,
		UserID:          record.UserID,
		Role:            record.Role,
		Username:        record.Username,
		DisplayName:     record.DisplayName,
		Email:           record.Email,
		AvatarUpdatedAt: domain.FormatOptionalTimestamp(record.AvatarUpdatedAt),
	}
}

// ---------------------------------------------------------------------------
// 处理器
// ---------------------------------------------------------------------------

// create 处理 POST /projects。
func (h *Handler) create(c *gin.Context) {
	actor, err := h.actor(c)
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
		Name:        request.Name,
		Description: request.Description,
		GroupID:     request.GroupID,
		Visibility:  request.Visibility,
		StartDate:   request.StartDate.Time,
		EndDate:     request.EndDate.Time,
		Color:       request.Color,
		CreatorID:   actor.ID,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondCreated(c, newProjectResponse(created))
}

// detail 处理 GET /projects/{project_id}。
func (h *Handler) detail(c *gin.Context) {
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

	record, err := h.service.Detail(c.Request.Context(), projectID, actor.ID)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondOK(c, newProjectResponse(record))
}

// list 处理 POST /projects/query。
func (h *Handler) list(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	var request queryRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}

	includeArchived := false
	if request.IncludeArchived != nil {
		includeArchived = *request.IncludeArchived
	}

	page, err := h.service.List(c.Request.Context(), ListInput{
		ActorID:         actor.ID,
		GroupID:         request.GroupID,
		Status:          request.Status,
		OwnerID:         request.OwnerID,
		Name:            request.Name,
		IncludeArchived: includeArchived,
		Page:            request.PageRequest,
		Sort:            request.Sort,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	items := make([]projectResponse, 0, len(page.Items))
	for _, record := range page.Items {
		items = append(items, newProjectResponse(record))
	}
	httpapi.RespondList(c, items, httpapi.NewPagination(page.Page.Page, page.Page.PageSize, page.Total))
}

// update 处理 PATCH /projects/{project_id}。
func (h *Handler) update(c *gin.Context) {
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

	var request updateRequest
	body, err := httpapi.BindJSONWithBody(c, &request)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	input := UpdateInput{
		ProjectID: projectID,
		ActorID:   actor.ID,
		Name:      request.Name,
		// 显式传空字符串表示清空：必须写成 SQL NULL 而不是 ''。
		Description:     emptyToNil(request.Description),
		DescriptionSet:  httpapi.HasKey(body, "description"),
		Status:          request.Status,
		Visibility:      request.Visibility,
		Color:           emptyToNil(request.Color),
		ColorSet:        httpapi.HasKey(body, "color"),
		ExpectedUpdated: request.ExpectedUpdated.Time,
	}
	if request.StartDate != nil {
		input.StartDate = &request.StartDate.Time
	}
	if request.EndDate != nil {
		input.EndDate = &request.EndDate.Time
	}

	updated, err := h.service.Update(c.Request.Context(), actor, input)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondOK(c, newProjectResponse(updated))
}

// archive 处理 POST /projects/{project_id}/archive。
func (h *Handler) archive(c *gin.Context) {
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

	var request archiveRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}

	archived, err := h.service.Archive(c.Request.Context(), actor, ArchiveInput{
		ProjectID:       projectID,
		ActorID:         actor.ID,
		ExpectedUpdated: request.ExpectedUpdated.Time,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondOK(c, newProjectResponse(archived))
}

// listMembers 处理 POST /projects/{project_id}/members/query。
func (h *Handler) listMembers(c *gin.Context) {
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

	var request memberQueryRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}

	page, err := h.service.ListMembers(c.Request.Context(), MemberListInput{
		ProjectID: projectID,
		ActorID:   actor.ID,
		Role:      request.Role,
		Page:      request.PageRequest,
		Sort:      request.Sort,
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

// addMember 处理 POST /projects/{project_id}/members。
func (h *Handler) addMember(c *gin.Context) {
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

	var request addMemberRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}

	member, err := h.service.AddMember(c.Request.Context(), AddMemberInput{
		ProjectID: projectID,
		ActorID:   actor.ID,
		UserID:    request.UserID,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondCreated(c, newMemberResponse(member))
}

// changeMemberRole 处理 PATCH /projects/{project_id}/members/{user_id}。
func (h *Handler) changeMemberRole(c *gin.Context) {
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
		ProjectID: projectID,
		ActorID:   actor.ID,
		UserID:    userID,
		Role:      request.Role,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondOK(c, newMemberResponse(member))
}

// removeMember 处理 DELETE /projects/{project_id}/members/{user_id}。
func (h *Handler) removeMember(c *gin.Context) {
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
	userID, err := requireUUIDParam(c, "user_id")
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	if err := h.service.RemoveMember(c.Request.Context(), projectID, actor.ID, userID); err != nil {
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
var errNoActorLoader = errors.New("projects: 未注入 ActorLoader")

// emptyToNil 把空字符串可选字段归一化为 nil（见 groups 包同名函数的说明）。
func emptyToNil(value *string) *string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	return value
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
