package groups

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

// Handler 装配工作组模块的 HTTP 端点。
type Handler struct {
	service *Service
	// actorLoader 由 main 注入，用于把会话 Cookie 解析为当前用户。
	actorLoader ActorLoader
	logger      *slog.Logger
}

// ActorLoader 把会话令牌解析为当前用户。
//
// 定义成接口而不是直接依赖 auth 模块：httpapi 与各业务模块都不应硬编码
// "当前用户从哪来"，这样测试可以注入固定用户，未来换鉴权方式也只改装配点。
type ActorLoader interface {
	LoadActor(c *gin.Context) (domain.User, error)
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
	group := api.Group("/groups")

	group.POST("", h.create)
	group.POST("/query", h.list)
	group.GET("/:group_id", h.detail)
	group.PATCH("/:group_id", h.update)

	group.POST("/:group_id/members/query", h.listMembers)
	group.POST("/:group_id/members", h.addMember)
	group.PATCH("/:group_id/members/:user_id", h.changeMember)
	group.DELETE("/:group_id/members/:user_id", h.removeMember)

	group.POST("/:group_id/join-requests", h.requestJoin)
	group.POST("/:group_id/join-requests/query", h.listJoinRequests)
	group.POST("/:group_id/join-requests/:user_id/review", h.reviewJoinRequest)
}

// ---------------------------------------------------------------------------
// 请求与响应结构
// ---------------------------------------------------------------------------

// createRequest 对应接口契约 4.4 POST /groups。
type createRequest struct {
	Name        string  `json:"name" validate:"required"`
	Description *string `json:"description"`
}

// updateRequest 对应接口契约 4.4 PATCH /groups/{group_id}。
//
// expected_updated_at 为**必填**：契约 2.6 要求更新核心对象必须提交版本。
// 早期实现把它设为可选，省略即跳过校验，使版本协议静默退化为
// last-write-wins（见 docs/06 5.3 的防回归对照）。
type updateRequest struct {
	Name            *string            `json:"name"`
	Description     *string            `json:"description"`
	Status          *string            `json:"status"`
	OwnerID         *string            `json:"owner_id"`
	ExpectedUpdated *httpapi.Timestamp `json:"expected_updated_at" validate:"required"`
}

// queryRequest 对应接口契约 4.4 POST /groups/query。
type queryRequest struct {
	Status *string `json:"status"`
	listquery.PageRequest
	listquery.SortRequest
}

// memberQueryRequest 对应 POST /groups/{group_id}/members/query。
type memberQueryRequest struct {
	Role   *string `json:"role"`
	Status *string `json:"status"`
	listquery.PageRequest
	listquery.SortRequest
}

// joinRequestQueryRequest 对应 POST /groups/{group_id}/join-requests/query。
type joinRequestQueryRequest struct {
	Status *string `json:"status"`
	listquery.PageRequest
	listquery.SortRequest
}

// addMemberRequest 对应 POST /groups/{group_id}/members。
type addMemberRequest struct {
	UserID string  `json:"user_id" validate:"required"`
	Role   *string `json:"role"`
}

// changeMemberRequest 对应 PATCH /groups/{group_id}/members/{user_id}。
type changeMemberRequest struct {
	Role   *string `json:"role"`
	Status *string `json:"status"`
}

// reviewRequest 对应 POST /groups/{group_id}/join-requests/{user_id}/review。
type reviewRequest struct {
	Status string `json:"status" validate:"required"`
}

// groupResponse 是工作组对外表示。
type groupResponse struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Status      string  `json:"status"`
	OwnerID     string  `json:"owner_id"`
	UpdatedAt   string  `json:"updated_at"`
	// MembershipRole 是当前用户在本组的角色；非成员时为 null。
	// 前端据此决定是否显示管理入口，但**权限判定仍在服务端**。
	MembershipRole  *string `json:"membership_role"`
	MembershipState *string `json:"membership_status"`
}

// memberResponse 是工作组成员对外表示。
type memberResponse struct {
	ID          string `json:"id"`
	GroupID     string `json:"group_id"`
	UserID      string `json:"user_id"`
	Role        string `json:"role"`
	Status      string `json:"status"`
	JoinedAt    string `json:"joined_at"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

// joinRequestResponse 是加入申请对外表示。
type joinRequestResponse struct {
	ID          string  `json:"id"`
	GroupID     string  `json:"group_id"`
	UserID      string  `json:"user_id"`
	Status      string  `json:"status"`
	ReviewedBy  *string `json:"reviewed_by"`
	ReviewedAt  *string `json:"reviewed_at"`
	Username    string  `json:"username"`
	DisplayName string  `json:"display_name"`
	Email       string  `json:"email"`
}

// newGroupResponse 把仓储记录投影为对外结构。
func newGroupResponse(record GroupRecord) groupResponse {
	response := groupResponse{
		ID:          record.ID,
		Name:        record.Name,
		Description: record.Description,
		Status:      record.Status,
		OwnerID:     record.OwnerID,
		UpdatedAt:   domain.FormatTimestamp(record.UpdatedAt),
	}
	if record.Membership != nil {
		role := record.Membership.Role
		status := record.Membership.Status
		response.MembershipRole = &role
		response.MembershipState = &status
	}
	return response
}

// newMemberResponse 把成员记录投影为对外结构。
func newMemberResponse(record MemberRecord) memberResponse {
	return memberResponse{
		ID:          record.ID,
		GroupID:     record.GroupID,
		UserID:      record.UserID,
		Role:        record.Role,
		Status:      record.Status,
		JoinedAt:    domain.FormatTimestamp(record.JoinedAt),
		Username:    record.Username,
		DisplayName: record.DisplayName,
		Email:       record.Email,
	}
}

// newJoinRequestResponse 把申请记录投影为对外结构。
func newJoinRequestResponse(record JoinRequestRecord) joinRequestResponse {
	var reviewedAt *string
	if record.ReviewedAt != nil {
		formatted := domain.FormatTimestamp(*record.ReviewedAt)
		reviewedAt = &formatted
	}
	return joinRequestResponse{
		ID:          record.ID,
		GroupID:     record.GroupID,
		UserID:      record.UserID,
		Status:      record.Status,
		ReviewedBy:  record.ReviewedBy,
		ReviewedAt:  reviewedAt,
		Username:    record.Username,
		DisplayName: record.DisplayName,
		Email:       record.Email,
	}
}

// ---------------------------------------------------------------------------
// 处理器
// ---------------------------------------------------------------------------

// create 处理 POST /groups。
//
// 创建者自动成为 owner 与 manager；该初始状态在仓储的单一事务内写入。
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
		OwnerID:     actor.ID,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	// 契约 2.1：成功写响应包含 id 与最新 updated_at。
	httpapi.RespondCreated(c, newGroupResponse(created))
}

// detail 处理 GET /groups/{group_id}。
func (h *Handler) detail(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	groupID, err := groupIDParam(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	record, err := h.service.Detail(c.Request.Context(), groupID, actor.ID)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondOK(c, newGroupResponse(record))
}

// list 处理 POST /groups/query。
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

	page, err := h.service.List(c.Request.Context(), ListInput{
		ActorID: actor.ID,
		Status:  request.Status,
		Page:    request.PageRequest,
		Sort:    request.Sort,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	items := make([]groupResponse, 0, len(page.Items))
	for _, record := range page.Items {
		items = append(items, newGroupResponse(record))
	}
	httpapi.RespondList(c, items, httpapi.NewPagination(page.Page.Page, page.Page.PageSize, page.Total))
}

// update 处理 PATCH /groups/{group_id}。
func (h *Handler) update(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	groupID, err := groupIDParam(c)
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

	updated, err := h.service.Update(c.Request.Context(), actor, UpdateInput{
		GroupID: groupID,
		ActorID: actor.ID,
		Name:    request.Name,
		// 仅在请求体显式包含 description 时才视为"修改"，
		// 否则会把"未提供"当成"清空描述"。
		// 显式传空字符串表示清空：必须写成 SQL NULL 而不是 ''，
		// 否则"清空"与"未设置"在库里成为两种状态，详情响应也会返回 "" 而不是 null。
		Description:     emptyToNil(request.Description),
		DescriptionSet:  httpapi.HasKey(body, "description"),
		Status:          request.Status,
		OwnerID:         request.OwnerID,
		ExpectedUpdated: request.ExpectedUpdated.Time,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondOK(c, newGroupResponse(updated))
}

// listMembers 处理 POST /groups/{group_id}/members/query。
func (h *Handler) listMembers(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	groupID, err := groupIDParam(c)
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
		GroupID: groupID,
		ActorID: actor.ID,
		Role:    request.Role,
		Status:  request.Status,
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

// addMember 处理 POST /groups/{group_id}/members。
func (h *Handler) addMember(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	groupID, err := groupIDParam(c)
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
		GroupID: groupID,
		ActorID: actor.ID,
		UserID:  request.UserID,
		Role:    request.Role,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondCreated(c, newMemberResponse(member))
}

// changeMember 处理 PATCH /groups/{group_id}/members/{user_id}。
func (h *Handler) changeMember(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	groupID, err := groupIDParam(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	userID, err := userIDParam(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	var request changeMemberRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}

	member, err := h.service.ChangeMember(c.Request.Context(), MemberChangeInput{
		GroupID: groupID,
		ActorID: actor.ID,
		UserID:  userID,
		Role:    request.Role,
		Status:  request.Status,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondOK(c, newMemberResponse(member))
}

// removeMember 处理 DELETE /groups/{group_id}/members/{user_id}。
func (h *Handler) removeMember(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	groupID, err := groupIDParam(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	userID, err := userIDParam(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	if _, err := h.service.RemoveMember(c.Request.Context(), groupID, actor.ID, userID); err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondNoContent(c)
}

// requestJoin 处理 POST /groups/{group_id}/join-requests。
func (h *Handler) requestJoin(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	groupID, err := groupIDParam(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	request, err := h.service.RequestJoin(c.Request.Context(), JoinRequestInput{
		GroupID: groupID,
		ActorID: actor.ID,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondCreated(c, newJoinRequestResponse(request))
}

// listJoinRequests 处理 POST /groups/{group_id}/join-requests/query。
func (h *Handler) listJoinRequests(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	groupID, err := groupIDParam(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	var request joinRequestQueryRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}

	page, err := h.service.ListJoinRequests(c.Request.Context(), actor, JoinRequestListInput{
		GroupID: groupID,
		ActorID: actor.ID,
		Status:  request.Status,
		Page:    request.PageRequest,
		Sort:    request.Sort,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	items := make([]joinRequestResponse, 0, len(page.Items))
	for _, record := range page.Items {
		items = append(items, newJoinRequestResponse(record))
	}
	httpapi.RespondList(c, items, httpapi.NewPagination(page.Page.Page, page.Page.PageSize, page.Total))
}

// reviewJoinRequest 处理 POST /groups/{group_id}/join-requests/{user_id}/review。
func (h *Handler) reviewJoinRequest(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	groupID, err := groupIDParam(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	userID, err := userIDParam(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	var request reviewRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}

	reviewed, err := h.service.ReviewJoinRequest(c.Request.Context(), actor, ReviewInput{
		GroupID:    groupID,
		ActorID:    actor.ID,
		TargetUser: userID,
		Decision:   request.Status,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondOK(c, newJoinRequestResponse(reviewed))
}

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

// actor 解析当前用户。
func (h *Handler) actor(c *gin.Context) (domain.User, error) {
	if h.actorLoader == nil {
		// 装配遗漏属于编程错误：快速失败而不是降级为匿名。
		return domain.User{}, domain.ErrInternal.WithCause(errNoActorLoader)
	}
	return h.actorLoader.LoadActor(c)
}

// errNoActorLoader 表示装配遗漏。
var errNoActorLoader = errors.New("groups: 未注入 ActorLoader")

// emptyToNil 把空字符串可选字段归一化为 nil。
//
// 可空文本字段在数据库中只有两种状态：有值或 NULL。
// 客户端用空字符串表达"清空"时必须翻译成 NULL，否则会引入第三种状态 ""，
// 既有歧义，也会让详情响应与创建时的表现不一致（实测踩到过）。
func emptyToNil(value *string) *string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	return value
}

// groupIDParam 读取并校验路径参数 group_id。
func groupIDParam(c *gin.Context) (string, error) {
	return requireUUIDParam(c, "group_id")
}

// userIDParam 读取并校验路径参数 user_id。
func userIDParam(c *gin.Context) (string, error) {
	return requireUUIDParam(c, "user_id")
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
		return "", domain.ErrInvalidRequest.
			WithField(name, "必须是合法的 UUID 字符串")
	}
	return value, nil
}
