// Package httpapi 承载跨模块的 HTTP 关注点：中间件、统一响应信封与领域错误→HTTP 映射。
//
// 职责边界（docs/01-Go服务端架构设计.md 4.3）：
//   - httpapi 负责绑定、序列化、状态码与错误信封；
//   - 权限与领域规则只能在 domain / app 判定，不得在 handler 里复制；
//   - 未识别错误统一 500 internal_error，绝不向客户端泄漏堆栈、SQL 或物理路径。
package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/myonku/project-builder/internal/domain"
)

// 错误信封与成功信封的稳定性说明：
// 这两个结构是接口文档 2.3 / 2.4 的唯一实现位置，各模块不得自行定义。

// ErrorBody 是 error 字段的内容，对应接口文档 2.4。
type ErrorBody struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Fields    map[string]string `json:"fields,omitempty"`
	RequestID string            `json:"request_id"`
}

// ErrorEnvelope 是统一的错误响应体。
type ErrorEnvelope struct {
	Error ErrorBody `json:"error"`
}

// DataResponse 是单对象与写操作的统一成功响应。
type DataResponse[T any] struct {
	Data T `json:"data"`
}

// Pagination 是列表分页元信息。
type Pagination struct {
	Page     int  `json:"page"`
	PageSize int  `json:"page_size"`
	Total    int  `json:"total"`
	HasNext  bool `json:"has_next"`
}

// NewPagination 构造分页元信息，统一 has_next 推导规则。
//
// 规则：has_next = page*page_size < total。集中在此处实现，
// 避免各模块各写一份导致边界不一致。
func NewPagination(page, pageSize, total int) Pagination {
	hasNext := false
	if pageSize > 0 && page > 0 {
		hasNext = page*pageSize < total
	}
	return Pagination{
		Page:     page,
		PageSize: pageSize,
		Total:    total,
		HasNext:  hasNext,
	}
}

// ListResponse 是列表的统一成功响应。Data 保证序列化为 [] 而不是 null。
type ListResponse[T any] struct {
	Data       []T        `json:"data"`
	Pagination Pagination `json:"pagination"`
}

// NewListResponse 构造列表响应；items 为 nil 时输出空数组。
//
// 契约要求列表的 data 是数组。若直接编码 nil 切片会得到 null，
// 前端需要额外判空，因此在边界处统一归一化。
func NewListResponse[T any](items []T, pagination Pagination) ListResponse[T] {
	if items == nil {
		items = []T{}
	}
	return ListResponse[T]{Data: items, Pagination: pagination}
}

// errorStatus 是领域错误码到 HTTP 状态码的唯一映射表。
//
// 历史缺陷对照：Python 版本只在 API 层为 500 注册了求解器，其余领域错误
// 全部退化为 500（见 docs/01-Go服务端架构设计.md 5.7）。这里用一张显式表格
// 覆盖全部错误码，并由 problem_test.go 断言"每个错误码都有映射"。
var errorStatus = map[domain.ErrorCode]int{
	domain.CodeInvalidRequest:       http.StatusBadRequest,
	domain.CodeInvalidDateRange:     http.StatusBadRequest,
	domain.CodeValidationError:      http.StatusUnprocessableEntity,
	domain.CodeAuthenticationReq:    http.StatusUnauthorized,
	domain.CodeInvalidCredentials:   http.StatusUnauthorized,
	domain.CodePermissionDenied:     http.StatusForbidden,
	domain.CodeResourceNotFound:     http.StatusNotFound,
	domain.CodeUserNotFound:         http.StatusNotFound,
	domain.CodeGroupNotFound:        http.StatusNotFound,
	domain.CodeProjectNotFound:      http.StatusNotFound,
	domain.CodeTaskNotFound:         http.StatusNotFound,
	domain.CodeConflict:             http.StatusConflict,
	domain.CodeDuplicateMember:      http.StatusConflict,
	domain.CodeVersionConflict:      http.StatusConflict,
	domain.CodeFileTooLarge:         http.StatusRequestEntityTooLarge,
	domain.CodeUnsupportedMediaType: http.StatusUnsupportedMediaType,
	domain.CodeRateLimited:          http.StatusTooManyRequests,
	domain.CodeInternalError:        http.StatusInternalServerError,
}

// errorMessage 是错误码到对外英文描述的映射（接口文档 2.4 使用英文 message）。
//
// 领域错误自带的 Message 是面向开发者的中文描述，用于日志；
// 这里的英文文案是对外契约的一部分，改动等于改契约。
var errorMessage = map[domain.ErrorCode]string{
	domain.CodeInvalidRequest:       "The request is invalid.",
	domain.CodeInvalidDateRange:     "The end date must not be earlier than the start date.",
	domain.CodeValidationError:      "One or more fields are invalid.",
	domain.CodeAuthenticationReq:    "Authentication is required.",
	domain.CodeInvalidCredentials:   "The username or password is incorrect.",
	domain.CodePermissionDenied:     "You do not have permission to perform this action.",
	domain.CodeResourceNotFound:     "The requested resource was not found.",
	domain.CodeUserNotFound:         "User not found",
	domain.CodeGroupNotFound:        "Group not found",
	domain.CodeProjectNotFound:      "Project not found",
	domain.CodeTaskNotFound:         "Task not found",
	domain.CodeConflict:             "The request conflicts with the current state.",
	domain.CodeDuplicateMember:      "The member already exists.",
	domain.CodeVersionConflict:      "The resource was modified by someone else. Refresh and retry.",
	domain.CodeFileTooLarge:         "The uploaded file exceeds the size limit.",
	domain.CodeUnsupportedMediaType: "The media type is not supported.",
	domain.CodeRateLimited:          "Too many requests.",
	domain.CodeInternalError:        "An unexpected internal error occurred.",
}

// StatusForCode 返回错误码对应的 HTTP 状态码；未登记时按 500 处理。
func StatusForCode(code domain.ErrorCode) int {
	if status, ok := errorStatus[code]; ok {
		return status
	}
	return http.StatusInternalServerError
}

// MessageForCode 返回错误码对应的对外英文描述；未登记时使用通用兜底文案。
func MessageForCode(code domain.ErrorCode) string {
	if message, ok := errorMessage[code]; ok {
		return message
	}
	return "An unexpected internal error occurred."
}

// Abort 把领域错误写入上下文并终止后续处理，由 ErrorHandler 统一序列化。
//
// 用例层与 handler 都应通过本函数结束请求，保证错误不经过 gin 默认的
// c.AbortWithStatusJSON（那会绕过统一信封）。
func Abort(c *gin.Context, err error) {
	if err == nil {
		return
	}
	_ = c.Error(err)
	c.Abort()
}

// ErrorHandler 是统一的错误处理中间件，必须注册在业务中间件之前。
//
// 它在 c.Next() 之后检查累积的错误：只要有任何错误，就丢弃可能已写入的响应体，
// 改为输出唯一的错误信封。
//
// 实现细节（这是一个必须保留的 defer）：这里的收口逻辑必须放在 defer 中。
// handler 中的 panic 会沿着中间件链向外展开，若收口写在 c.Next() 之后而不在 defer 中，
// panic 会直接跳过这段代码，导致响应既没有信封也没有状态码
// （实测表现：HTTP 200、空响应体）。
func ErrorHandler(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			writeErrorEnvelope(c, logger)
		}()
		c.Next()
	}
}

// writeErrorEnvelope 把上下文中累积的第一个错误写为统一错误信封。
//
// 由 ErrorHandler（普通错误路径）与 RecoveryMiddleware（panic 路径）共用，
// 保证两条路径输出的信封形状完全一致。
func writeErrorEnvelope(c *gin.Context, logger *slog.Logger) {
	if len(c.Errors) == 0 {
		return
	}

	// 只处理第一个错误，其余记为日志，避免错误级联掩盖根因。
	domainErr := domain.AsError(c.Errors[0])
	for _, extra := range c.Errors[1:] {
		logger.Error("请求存在附加错误",
			slog.String("request_id", RequestID(c)),
			slog.String("path", c.FullPath()),
			slog.String("error", extra.Error()),
		)
	}

	status := StatusForCode(domainErr.Code)
	fields := domainErr.Fields
	if fields == nil && status == http.StatusUnprocessableEntity {
		// 校验错误没有字段明细时，至少不要输出 null。
		fields = map[string]string{}
	}

	// 内部错误的原因只进日志，绝不进入响应体。
	if status >= http.StatusInternalServerError {
		logger.Error("请求处理失败",
			slog.String("request_id", RequestID(c)),
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.String("code", string(domainErr.Code)),
			slog.String("reason", domainErr.Message),
			slog.Any("cause", domainErr.Cause),
		)
	} else if logger.Enabled(c.Request.Context(), slog.LevelDebug) {
		logger.Debug("请求被拒绝",
			slog.String("request_id", RequestID(c)),
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.String("code", string(domainErr.Code)),
			slog.Int("status", status),
		)
	}

	c.AbortWithStatusJSON(status, ErrorEnvelope{
		Error: ErrorBody{
			Code:      string(domainErr.Code),
			Message:   MessageForCode(domainErr.Code),
			Fields:    fields,
			RequestID: RequestID(c),
		},
	})
}

// respondOK 写入单对象成功响应。各模块通过本包导出的辅助函数保持一致形状。
func respondOK[T any](c *gin.Context, data T) {
	c.JSON(http.StatusOK, DataResponse[T]{Data: data})
}

// RespondCreated 写入 201 成功响应，用于创建型端点。
func RespondCreated[T any](c *gin.Context, data T) {
	c.JSON(http.StatusCreated, DataResponse[T]{Data: data})
}

// RespondOK 写入 200 单对象成功响应。
func RespondOK[T any](c *gin.Context, data T) {
	respondOK(c, data)
}

// RespondList 写入列表成功响应。
func RespondList[T any](c *gin.Context, items []T, pagination Pagination) {
	c.JSON(http.StatusOK, NewListResponse(items, pagination))
}

// RespondNoContent 写入 204，用于无响应体的删除型端点。
func RespondNoContent(c *gin.Context) {
	c.Status(http.StatusNoContent)
}

// IsNotFound 报告错误是否为"不存在"语义，供用例层组合判断。
func IsNotFound(err error) bool {
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		return false
	}
	switch domainErr.Code {
	case domain.CodeResourceNotFound, domain.CodeUserNotFound,
		domain.CodeGroupNotFound, domain.CodeProjectNotFound, domain.CodeTaskNotFound:
		return true
	default:
		return false
	}
}
