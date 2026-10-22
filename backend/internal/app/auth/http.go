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

// LoadActor 实现其他业务模块所需的 ActorLoader 契约。
//
// 放在 auth 模块里而不是让各模块自己读 Redis：会话的存储位置与 Cookie 名
// 属于认证模块的内部知识，其他模块只应得到"当前用户是谁"。
//
// 返回值刻意使用 domain.User，因此这里不依赖任何业务模块（避免反向依赖）。
func (h *Handler) LoadActor(c *gin.Context) (domain.User, error) {
	token := httpapi.ReadSessionCookie(c, httpapi.SessionCookieName(c))
	return h.service.ResolveSession(c.Request.Context(), token)
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
	h.registerPasswordResetRoutes(group)

	// 注册申请审核：仅系统管理员；权限在 service 层再次校验。
	h.registerRegistrationRoutes(api)
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

	// 生产配置下 result.Pending 为真：只返回申请，不创建用户、不建会话。
	// 测试/冒烟配置关闭审核时保留原来的"直接创建 active 用户"响应。
	if result.Pending {
		httpapi.RespondCreated(c, newRegistrationRequestResponse(result.Request))
		return
	}
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
