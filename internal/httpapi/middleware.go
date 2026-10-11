package httpapi

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/myonku/project-builder/internal/platform/idgen"
)

// requestIDKey 是 request id 在 gin 上下文中的键名，也是响应头名称（小写形式）。
const (
	requestIDContextKey = "dsh.request_id"
	// RequestIDHeader 是请求与响应使用的 request id 头。
	RequestIDHeader = "X-Request-Id"
	// RequestIDLogKey 是 request id 在 slog 中的字段名。
	RequestIDLogKey = "request_id"
)

// RequestID 从上下文读取当前请求的 request id，未设置时返回空串。
func RequestID(c *gin.Context) string {
	if value, ok := c.Get(requestIDContextKey); ok {
		if id, ok := value.(string); ok {
			return id
		}
	}
	return ""
}

// RequestIDMiddleware 为每个请求分配 request id。
//
// 若客户端已带 X-Request-Id 则沿用（便于跨服务串联链路），否则新生成一个。
// 该 id 同时写入：
//   - 响应头，便于前端在报错时反馈；
//   - gin 上下文，供错误信封的 request_id 字段使用；
//   - 访问日志。
func RequestIDMiddleware(ids *idgen.Generator) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID := c.GetHeader(RequestIDHeader)
		if requestID == "" {
			requestID = ids.RequestID()
		}

		c.Set(requestIDContextKey, requestID)
		c.Header(RequestIDHeader, requestID)

		c.Next()
	}
}

// RecoveryMiddleware 捕获 handler 中的 panic，转换为 500 internal_error 信封。
//
// 不使用 gin.Recovery()：它会直接输出纯文本堆栈，既不符合统一错误信封，
// 也会向客户端泄漏内部实现细节（接口文档 2.4 末段明确禁止）。
//
// 为什么这里直接写信封而不交给 ErrorHandler：
// 本中间件位于 ErrorHandler 之外，panic 向外展开时 ErrorHandler 的 defer 已经执行完，
// 因此必须由这里补上信封，否则响应会是"200 空响应体"。
func RecoveryMiddleware(logger *slog.Logger) gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, recovered any) {
		logger.Error("请求处理发生 panic",
			slog.String(RequestIDLogKey, RequestID(c)),
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.Any("panic", recovered),
		)
		_ = c.Error(errInternalPanic{recovered: recovered})
		c.Abort()
		writeErrorEnvelope(c, logger)
	})
}

// errInternalPanic 表示一次被恢复的 panic，Code 固定为 internal_error。
type errInternalPanic struct{ recovered any }

func (e errInternalPanic) Error() string {
	return "panic recovered"
}

// AccessLogMiddleware 输出结构化访问日志。
//
// 只记录方法与路径，不记录请求体或查询串中的可能敏感值；
// 时间由 slog 的 handler 统一按 UTC 输出。
func AccessLogMiddleware(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		attrs := []any{
			slog.String(RequestIDLogKey, RequestID(c)),
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.Int("status", c.Writer.Status()),
			slog.Int("bytes", c.Writer.Size()),
		}
		// 已由 ErrorHandler 记录过的业务错误不在这里重复报错，避免日志噪声。
		if status := c.Writer.Status(); status >= 500 {
			logger.Error("请求完成", attrs...)
			return
		}
		logger.Info("请求完成", attrs...)
	}
}
