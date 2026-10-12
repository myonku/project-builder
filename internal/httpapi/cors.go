package httpapi

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORSMiddleware 按显式来源白名单装配跨域。
//
// 历史缺陷对照：早期实现生产环境使用 allow_origins=["*"] 且允许凭据，
// 这在浏览器规范下既是配置错误，也让任意站点可携带用户 Cookie 发起请求
// （见 docs/01-Go服务端架构设计.md 5 与 docs/03 4.1）。这里改为显式白名单：
//   - 只回显白名单内的 Origin，绝不回显 "*"；
//   - 未命中白名单时不写任何 CORS 头，浏览器自行拒绝；
//   - 白名单为空时视为"不启用跨域"，而不是放开全部。
func CORSMiddleware(allowedOrigins []string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			allowed[origin] = struct{}{}
		}
	}

	exposeHeaders := []string{RequestIDHeader}

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" {
			if _, ok := allowed[origin]; ok {
				headers := c.Writer.Header()
				headers.Set("Access-Control-Allow-Origin", origin)
				// 回显 Origin 时响应随来源变化，必须声明 Vary 以免代理缓存串味。
				headers.Add("Vary", "Origin")
				headers.Set("Access-Control-Allow-Credentials", "true")
				headers.Set("Access-Control-Expose-Headers", strings.Join(exposeHeaders, ", "))
			}
		}

		if c.Request.Method == http.MethodOptions && c.GetHeader("Access-Control-Request-Method") != "" {
			// 预检请求：仅当来源被放行时才补全其余头并提前返回。
			if origin != "" {
				if _, ok := allowed[origin]; ok {
					headers := c.Writer.Header()
					headers.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
					requested := c.GetHeader("Access-Control-Request-Headers")
					if requested == "" {
						requested = "Content-Type, Idempotency-Key, X-Request-Id"
					}
					headers.Set("Access-Control-Allow-Headers", requested)
					headers.Set("Access-Control-Max-Age", "600")
				}
			}
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
