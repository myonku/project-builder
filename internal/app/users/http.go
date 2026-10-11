package users

import (
	"errors"
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/httpapi"
	"github.com/myonku/project-builder/internal/platform/idgen"
)

// ActorLoader 把会话 Cookie 解析为当前用户。实现见 app/auth 的 Handler.LoadActor。
type ActorLoader interface {
	LoadActor(c *gin.Context) (domain.User, error)
}

// Handler 装配用户资料模块的 HTTP 端点。
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
	group := api.Group("/users")

	group.GET("/:user_id", h.detail)
	group.PATCH("/:user_id", h.update)
	group.POST("/:user_id/disable", h.disable)
}

// ---------------------------------------------------------------------------
// 请求与响应结构
// ---------------------------------------------------------------------------

// updateRequest 对应接口契约 4.3 PATCH /users/{user_id}。
//
// expected_updated_at 为**必填**：契约 2.6 要求更新核心对象必须提交版本。
// 字段集合严格等于契约列出的 6 项：username 不可修改（它是登录标识与审计主体），
// 密码不在本端点修改（契约未定义，见 docs/04 4.3 末段）。
type updateRequest struct {
	DisplayName     *string            `json:"display_name"`
	Email           *string            `json:"email"`
	Timezone        *string            `json:"timezone"`
	ReminderEnabled *bool              `json:"reminder_enabled"`
	Status          *string            `json:"status"`
	SystemRole      *string            `json:"system_role"`
	ExpectedUpdated *httpapi.Timestamp `json:"expected_updated_at" validate:"required"`
}

// disableRequest 对应 POST /users/{user_id}/disable。
//
// 停用属于状态变更，按契约 2.6 同样要求版本字段。
type disableRequest struct {
	ExpectedUpdated *httpapi.Timestamp `json:"expected_updated_at" validate:"required"`
}

// profileResponse 是公开用户结构，形状与接口契约 3.1 一致。
//
// 为什么不直接复用 auth.PublicUser：两个业务模块不得互相依赖
// （docs/01-Go服务端架构设计.md 4.3）。重复的代价由集成测试兜住——
// `TestUserProfileShapeMatchesAuthMe` 断言 /auth/me 与 /users/{id} 的
// JSON 字段集合完全相同，任何一侧漂移都会立刻失败。
//
// **password_hash 不在此结构中**：本模块的仓储列清单也不包含它，
// 因此密码摘要不可能从这里泄漏。
type profileResponse struct {
	ID              string  `json:"id"`
	Username        string  `json:"username"`
	Email           string  `json:"email"`
	DisplayName     string  `json:"display_name"`
	Status          string  `json:"status"`
	SystemRole      string  `json:"system_role"`
	Timezone        string  `json:"timezone"`
	ReminderEnabled bool    `json:"reminder_enabled"`
	LastLoginAt     *string `json:"last_login_at"`
	UpdatedAt       string  `json:"updated_at"`
}

// newProfileResponse 把仓储记录投影为对外结构。
func newProfileResponse(record ProfileRecord) profileResponse {
	var lastLogin *string
	if record.LastLoginAt != nil {
		formatted := domain.FormatTimestamp(*record.LastLoginAt)
		lastLogin = &formatted
	}
	return profileResponse{
		ID:              record.ID,
		Username:        record.Username,
		Email:           record.Email,
		DisplayName:     record.DisplayName,
		Status:          record.Status,
		SystemRole:      record.SystemRole,
		Timezone:        record.Timezone,
		ReminderEnabled: record.ReminderEnabled,
		LastLoginAt:     lastLogin,
		UpdatedAt:       domain.FormatTimestamp(record.UpdatedAt),
	}
}

// ---------------------------------------------------------------------------
// 处理器
// ---------------------------------------------------------------------------

// detail 处理 GET /users/{user_id}。
func (h *Handler) detail(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	userID, err := userIDParam(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	record, err := h.service.Detail(c.Request.Context(), actor, userID)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondOK(c, newProfileResponse(record))
}

// update 处理 PATCH /users/{user_id}。
func (h *Handler) update(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	userID, err := userIDParam(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	var request updateRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}

	updated, err := h.service.Update(c.Request.Context(), actor, UpdateInput{
		TargetID: userID,
		Update: domain.UserProfileUpdate{
			DisplayName:     request.DisplayName,
			Email:           request.Email,
			Timezone:        request.Timezone,
			ReminderEnabled: request.ReminderEnabled,
			Status:          request.Status,
			SystemRole:      request.SystemRole,
		},
		ExpectedUpdated: request.ExpectedUpdated.Time,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondOK(c, newProfileResponse(updated))
}

// disable 处理 POST /users/{user_id}/disable。
func (h *Handler) disable(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	userID, err := userIDParam(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	var request disableRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}

	updated, err := h.service.Disable(c.Request.Context(), actor, DisableInput{
		TargetID:        userID,
		ExpectedUpdated: request.ExpectedUpdated.Time,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondOK(c, newProfileResponse(updated))
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
var errNoActorLoader = errors.New("users: 未注入 ActorLoader")

// userIDParam 读取并校验路径参数 user_id。
//
// 非法 UUID 直接 400 而不是交给数据库：否则会触发 22P02 类型错误并退化为 500。
func userIDParam(c *gin.Context) (string, error) {
	value := c.Param("user_id")
	if value == "" {
		return "", domain.ErrInvalidRequest.WithField("user_id", "缺少路径参数")
	}
	if !idgen.IsUUID(value) {
		return "", domain.ErrInvalidRequest.WithField("user_id", "必须是合法的 UUID 字符串")
	}
	return value, nil
}
