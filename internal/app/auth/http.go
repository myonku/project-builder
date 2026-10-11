package auth

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/httpapi"
)

// Handler 装配本模块的 HTTP 端点。
type Handler struct {
	service *Service
	logger  *slog.Logger
}

// NewHandler 构造 HTTP 处理器。
func NewHandler(service *Service, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{service: service, logger: logger}
}

// Register 把本模块端点挂载到版本化路由组之下。
//
// 路径与接口文档 4.2 逐条对应。
func (h *Handler) Register(api *gin.RouterGroup) {
	group := api.Group("/auth")
	group.POST("/register", h.register)
	group.POST("/login", h.login)
	group.POST("/logout", h.logout)
	group.GET("/me", h.me)
}

// registerRequest 对应接口文档 4.2 POST /auth/register 的字段。
type registerRequest struct {
	Username    string `json:"username"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
	Timezone    string `json:"timezone"`
}

// loginRequest 对应接口文档 4.2 POST /auth/login 的字段。
type loginRequest struct {
	Identifier string `json:"identifier"`
	Password   string `json:"password"`
}

// register 处理注册。
func (h *Handler) register(c *gin.Context) {
	var request registerRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}

	result, err := h.service.Register(c.Request.Context(), domain.RegistrationInput{
		Username:    request.Username,
		Email:       request.Email,
		DisplayName: request.DisplayName,
		Password:    request.Password,
		Timezone:    request.Timezone,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	// 契约 2.1：成功写响应包含对象 id 和最新 updated_at；3.1 要求返回公开用户字段。
	// 注册不建立会话，也不返回会话令牌。
	httpapi.RespondCreated(c, NewPublicUser(result.User))
}

// login 处理登录并写入会话 Cookie。
func (h *Handler) login(c *gin.Context) {
	var request loginRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}

	result, err := h.service.Login(c.Request.Context(), domain.LoginInput{
		Identifier: request.Identifier,
		Password:   request.Password,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.SetSessionCookie(
		c,
		h.service.SessionCookieName(),
		result.Token,
		h.service.SessionCookieMaxAge(),
		h.service.SecureCookies(),
	)
	httpapi.RespondOK(c, NewPublicUser(result.User))
}

// logout 撤销会话并清除 Cookie。
//
// 端点位于会话网关白名单之外，因此到达这里时 Cookie 一定存在；
// 但会话可能已在 Redis 中过期，登出仍必须返回成功（幂等）。
func (h *Handler) logout(c *gin.Context) {
	token := httpapi.ReadSessionCookie(c, h.service.SessionCookieName())
	if err := h.service.Logout(c.Request.Context(), token); err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.ClearSessionCookie(c, h.service.SessionCookieName(), h.service.SecureCookies())
	httpapi.RespondNoContent(c)
}

// me 返回当前会话对应的用户。
//
// 会话网关只保证 Cookie 存在，因此这里必须真正校验 Redis 中的会话，
// 并在用户被停用时立即失效（服务端设计 5）。
func (h *Handler) me(c *gin.Context) {
	token := httpapi.ReadSessionCookie(c, h.service.SessionCookieName())

	record, err := h.service.CurrentUser(c.Request.Context(), token)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	httpapi.RespondOK(c, NewPublicUser(record))
}
