package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/myonku/project-builder/internal/domain"
)

// SessionCookieName 是会话 Cookie 的默认名称；实际值来自配置。
//
// 本文件只负责"Cookie 是否存在"这一层判断。会话有效性由 Redis 会话存储判定
// （服务端设计 5：Session gateway 只检查 Cookie 存在性；SessionStore 负责
// Redis 会话有效性）。这样中间件不依赖 Redis，Redis 不可用时业务端点仍能给出
// 明确错误而不是全部 401。
type SessionGateway struct {
	cookieName string
	// publicPrefixes 是无需会话 Cookie 的路径前缀白名单。
	// 业务 handler 仍必须自行校验当前用户，白名单只表示"不在这里拦截"。
	publicPrefixes []string
	// PublicPathPrefix 是版本前缀（如 /api/v1）。
	// 白名单按"去掉版本前缀"的相对路径匹配，这样升级 API 版本时白名单无需跟着改。
	PublicPathPrefix string
}

// NewSessionGateway 构造会话网关。
func NewSessionGateway(cookieName string) *SessionGateway {
	return &SessionGateway{
		cookieName: cookieName,
		publicPrefixes: []string{
			"/health/check",
			"/health/live",
			"/health/ready",
			"/auth/register",
			"/auth/login",
		},
	}
}

// IsPublic 报告给定请求路径是否在白名单内。
func (g *SessionGateway) IsPublic(path string) bool {
	if g.PublicPathPrefix != "" {
		if path == g.PublicPathPrefix {
			path = "/"
		} else if trimmed, ok := strings.CutPrefix(path, g.PublicPathPrefix+"/"); ok {
			path = "/" + trimmed
		}
	}
	for _, prefix := range g.publicPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// Middleware 在受保护路径上要求会话 Cookie 存在。
//
// 缺失时返回 401 authentication_required，并中止请求，避免走到用例层才失败。
func (g *SessionGateway) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if g.IsPublic(c.Request.URL.Path) {
			c.Next()
			return
		}

		if _, err := c.Cookie(g.cookieName); err != nil {
			Abort(c, domain.ErrAuthenticationRequired)
			return
		}

		c.Next()
	}
}

// SetSessionCookie 写入 HttpOnly 会话 Cookie。
//
// 安全属性：
//   - HttpOnly：禁止脚本读取，降低 XSS 窃取会话的风险；
//   - SameSite=Lax：阻止跨站 POST 携带 Cookie，同时保留普通导航可用性；
//   - Secure：由配置控制（生产必须为 true，本地 HTTP 调试为 false）；
//   - Path=/：整个应用可见。
func SetSessionCookie(c *gin.Context, name, token string, maxAgeSeconds int, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAgeSeconds,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionCookie 清除会话 Cookie。
//
// MaxAge 设为 -1 表示立即删除；必须带上与写入时一致的关键属性，
// 否则部分浏览器不会覆盖原有 Cookie。
func ClearSessionCookie(c *gin.Context, name string, secure bool) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ReadSessionCookie 读取会话 Cookie 值，缺失时返回空串。
func ReadSessionCookie(c *gin.Context, name string) string {
	value, err := c.Cookie(name)
	if err != nil {
		return ""
	}
	return value
}
