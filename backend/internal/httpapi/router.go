package httpapi

import (
	"log/slog"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/myonku/project-builder/internal/config"
	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/platform/clock"
	"github.com/myonku/project-builder/internal/platform/idgen"
)

// Health 由 main 注入，用于 /health/ready 的真实依赖检查。
type Health struct {
	PostgresPing func() error
	RedisPing    func() error
	MongoPing    func() error
}

// Registrar 是业务模块向路由注册自身端点的接口。
//
// 各模块实现 Register(api *gin.RouterGroup)，由 Router 统一挂载在 /api/v{version} 之下。
// 这样"版本前缀 + 中间件顺序"只在一处定义，模块无法各自为政。
type Registrar interface {
	Register(api *gin.RouterGroup)
}

// Dependencies 是 Router 所需的全部外部依赖。
//
// 集中为一个结构体是为了让装配点（cmd/api）显式且可读，避免反射式 DI
// （见 backend/docs/B01-Go服务端架构设计.md 4.4）。
type Dependencies struct {
	Config  config.Config
	Logger  *slog.Logger
	Clock   clock.Clock
	IDs     *idgen.Generator
	Health  Health
	Modules []Registrar
}

// Router 把中间件与模块路由装配为一个 http.Handler。
func Router(deps Dependencies) *gin.Engine {
	cfg := deps.Config
	logger := deps.Logger

	// ReleaseMode 下 gin 不再打印调试信息；运行阶段由 APP_ENV 决定。
	if config.IsProduction(cfg.Env) {
		gin.SetMode(gin.ReleaseMode)
	} else {
		gin.SetMode(gin.TestMode)
	}

	engine := gin.New()
	// 关闭自动重定向，避免 /projects 被 301 到 /projects/ 绕过客户端的路径假设。
	engine.RedirectTrailingSlash = false
	// 开启 405 处理：路径存在但方法不被支持时走 engine.NoMethod，
	// 从而返回统一错误信封而不是笼统的 404。
	engine.HandleMethodNotAllowed = true
	// 不使用 gin.Default()：它内置的 Logger 与 Recovery 会输出非结构化日志和裸堆栈文本。
	engine.Use(
		RequestIDMiddleware(deps.IDs),
		AccessLogMiddleware(logger),
		RecoveryMiddleware(logger),
		CORSMiddleware(cfg.Server.CORSAllowedOrigins),
	)
	// ErrorHandler 必须在业务中间件之前注册，才能在 c.Next() 返回后统一收口错误。
	engine.Use(ErrorHandler(logger))

	// 全局限流挂在 ErrorHandler **之后**：限流拒绝走 Abort + ErrorHandler 的统一信封，
	// 而不是自己拼 429 响应体。它同时覆盖未登录端点（登录口令尝试是主要目标）。
	//
	// 只挂在 engine 上、不挂在 /api/v1 组里，是为了让根级 /healthz 也被覆盖；
	// 未命中路由的 404/405 同样受限，这是刻意的（否则可以用无效路径刷请求）。
	engine.Use(RateLimitMiddleware(NewRateLimiter(cfg.RateLimit, deps.Clock)))

	apiPrefix := "/api/v" + cfg.APIVersion

	// 根级存活探针：不依赖任何外部资源，也不带版本前缀，
	// 便于容器与反向代理直接使用。它在会话网关之外，因此无需 Cookie。
	engine.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	api := engine.Group(apiPrefix)

	// 会话网关只挂在版本化路由组上，而不是 engine 全局。
	//
	// 这是一个刻意的选择：挂在全局会让"未命中路由"返回 401 而不是 404，
	// 因为中间件在路由匹配失败（engine.NoRoute）时同样执行，
	// 从而把 404/405 误报成未登录，严重影响前端排查。
	gateway := NewSessionGateway(cfg.Security.SessionCookieName)
	gateway.PublicPathPrefix = apiPrefix
	// 把 Cookie 名写入上下文，业务模块据此读取会话令牌而不各自持有配置。
	api.Use(func(c *gin.Context) {
		c.Set(sessionCookieNameContextKey, cfg.Security.SessionCookieName)
		c.Next()
	})
	api.Use(gateway.Middleware())

	// 健康检查是契约端点，挂在版本前缀之下（与既有路径约定一致）。
	registerHealth(api, deps.Health)

	for _, module := range deps.Modules {
		module.Register(api)
	}

	engine.NoRoute(func(c *gin.Context) {
		Abort(c, domain.ErrResourceNotFound)
	})
	engine.NoMethod(func(c *gin.Context) {
		Abort(c, domain.ErrInvalidRequest.WithField("method", "该方法不被支持"))
	})

	return engine
}

// registerHealth 注册契约要求的三类健康检查。
func registerHealth(api *gin.RouterGroup, health Health) {
	group := api.Group("/health")

	// /health/check 要求 arg 请求头，是最初的传输层冒烟检查。
	// 语义与既有约定保持一致（缺头返回 422/400 而不是 200）。
	group.GET("/check", func(c *gin.Context) {
		if strings.TrimSpace(c.GetHeader("arg")) == "" {
			Abort(c, domain.ErrValidation.WithField("arg", "缺少必需的 arg 请求头"))
			return
		}
		c.JSON(200, gin.H{"status": "ok"})
	})

	// /health/live 不检查业务依赖：进程活着就返回 200。
	group.GET("/live", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	// /health/ready 真实检查数据库与 Redis；任一不可用返回 503。
	group.GET("/ready", func(c *gin.Context) {
		if health.PostgresPing != nil {
			if err := health.PostgresPing(); err != nil {
				c.JSON(503, gin.H{"status": "not_ready", "error": "database_unavailable"})
				return
			}
		}
		if health.RedisPing != nil {
			if err := health.RedisPing(); err != nil {
				c.JSON(503, gin.H{"status": "not_ready", "error": "redis_unavailable"})
				return
			}
		}
		if health.MongoPing != nil {
			if err := health.MongoPing(); err != nil {
				c.JSON(503, gin.H{"status": "not_ready", "error": "object_storage_unavailable"})
				return
			}
		}
		c.JSON(200, gin.H{"status": "ready"})
	})
}
