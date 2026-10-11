// Package domain 保存不依赖框架、数据库与 HTTP 的纯业务规则。
//
// 依赖约束（见 docs/01-Go服务端架构设计.md 4.3）：本包只允许依赖标准库与
// internal/platform/clock 的接口类型。规则是纯函数，便于表驱动测试。
package domain

import (
	"errors"
	"fmt"
	"strings"
)

// ErrorCode 是机器可读的错误码，直接映射到 HTTP 契约附录中的错误码。
//
// 历史缺陷对照：Python 版本的领域错误全部是裸 ValueError，API 层只为 500 注册了
// 求解器，导致 409 / 403 / 404 / 400 / 422 全部退化为 500
// （见 docs/01-Go服务端架构设计.md 5.7）。Go 版本把错误码定义在领域层，
// 由 httpapi/problem.go 单点映射为状态码。
type ErrorCode string

// 与 docs/04-HTTP接口契约.md 2.4 错误码表逐条对应。
const (
	CodeInvalidRequest       ErrorCode = "invalid_request"
	CodeInvalidDateRange     ErrorCode = "invalid_date_range"
	CodeValidationError      ErrorCode = "validation_error"
	CodeAuthenticationReq    ErrorCode = "authentication_required"
	CodeInvalidCredentials   ErrorCode = "invalid_credentials"
	CodePermissionDenied     ErrorCode = "permission_denied"
	CodeResourceNotFound     ErrorCode = "resource_not_found"
	CodeUserNotFound         ErrorCode = "user_not_found"
	CodeGroupNotFound        ErrorCode = "group_not_found"
	CodeProjectNotFound      ErrorCode = "project_not_found"
	CodeTaskNotFound         ErrorCode = "task_not_found"
	CodeAttachmentNotFound   ErrorCode = "attachment_not_found"
	CodeReminderNotFound     ErrorCode = "reminder_not_found"
	CodeConflict             ErrorCode = "conflict"
	CodeDuplicateMember      ErrorCode = "duplicate_member"
	CodeVersionConflict      ErrorCode = "version_conflict"
	CodeFileTooLarge         ErrorCode = "file_too_large"
	CodeUnsupportedMediaType ErrorCode = "unsupported_media_type"
	CodeRateLimited          ErrorCode = "rate_limited"
	CodeInternalError        ErrorCode = "internal_error"
)

// Error 是携带语义的领域错误。
//
// 字段说明：
//   - Code：机器可读错误码，客户端据此分支。
//   - Message：面向人的默认描述。HTTP 层可覆盖为对外文案。
//   - Fields：字段级校验明细，对应契约中的 error.fields。
//   - Cause：内部原因，只进日志，绝不返回给客户端（契约 2.4 末段）。
type Error struct {
	Code    ErrorCode
	Message string
	Fields  map[string]string
	Cause   error
}

// New 构造一个领域错误。
func New(code ErrorCode, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Newf 构造一个带格式化描述的领域错误。
func Newf(code ErrorCode, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Wrap 包装底层错误并附加领域语义。Cause 只用于日志。
func Wrap(code ErrorCode, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Cause: cause}
}

// WithField 附加一个字段级错误，返回新的错误对象（不可变风格，避免共享 map 被改写）。
func (e *Error) WithField(field, detail string) *Error {
	fields := make(map[string]string, len(e.Fields)+1)
	for k, v := range e.Fields {
		fields[k] = v
	}
	fields[field] = detail
	return &Error{Code: e.Code, Message: e.Message, Fields: fields, Cause: e.Cause}
}

// WithFields 批量附加字段级错误。
func (e *Error) WithFields(fields map[string]string) *Error {
	if len(fields) == 0 {
		return e
	}
	merged := make(map[string]string, len(e.Fields)+len(fields))
	for k, v := range e.Fields {
		merged[k] = v
	}
	for k, v := range fields {
		merged[k] = v
	}
	return &Error{Code: e.Code, Message: e.Message, Fields: merged, Cause: e.Cause}
}

// WithCause 附加内部原因。
func (e *Error) WithCause(cause error) *Error {
	return &Error{Code: e.Code, Message: e.Message, Fields: e.Fields, Cause: cause}
}

// Error 实现 error 接口。输出不含 Cause，避免内部细节意外进入日志以外的路径。
func (e *Error) Error() string {
	if len(e.Fields) == 0 {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	parts := make([]string, 0, len(e.Fields))
	for k, v := range e.Fields {
		parts = append(parts, k+"="+v)
	}
	return fmt.Sprintf("%s: %s (%s)", e.Code, e.Message, strings.Join(parts, ", "))
}

// Unwrap 支持 errors.Is / errors.As 穿透到内部原因。
func (e *Error) Unwrap() error { return e.Cause }

// Is 让相同错误码的错误相互匹配，便于在测试与用例层用 errors.Is 判断类别。
func (e *Error) Is(target error) bool {
	var other *Error
	if !errors.As(target, &other) {
		return false
	}
	return e.Code == other.Code
}

// 预置错误值。用例层直接返回它们，或用 New/Newf 派生更具体的描述。

var (
	// ErrInvalidRequest 请求本身不合法（分页越界、未知筛选或排序字段等）。
	ErrInvalidRequest = New(CodeInvalidRequest, "请求参数无效")
	// ErrInvalidDateRange 日期区间不合法（结束日期早于开始日期）。
	ErrInvalidDateRange = New(CodeInvalidDateRange, "结束日期不得早于开始日期")
	// ErrValidation 字段级校验失败。
	ErrValidation = New(CodeValidationError, "字段校验失败")
	// ErrAuthenticationRequired 未登录或会话失效。
	ErrAuthenticationRequired = New(CodeAuthenticationReq, "需要登录")
	// ErrInvalidCredentials 凭据错误。
	ErrInvalidCredentials = New(CodeInvalidCredentials, "用户名或密码错误")
	// ErrPermissionDenied 已登录但无权操作。
	ErrPermissionDenied = New(CodePermissionDenied, "当前用户无权执行该操作")
	// ErrResourceNotFound 对象不存在或对当前用户不可见。
	ErrResourceNotFound = New(CodeResourceNotFound, "对象不存在或不可见")
	// ErrConflict 状态、重复或并发冲突的通用形式。
	ErrConflict = New(CodeConflict, "操作与当前状态冲突")
	// ErrDuplicateMember 重复成员关系。
	ErrDuplicateMember = New(CodeDuplicateMember, "该成员已存在")
	// ErrVersionConflict 乐观并发校验失败。
	ErrVersionConflict = New(CodeVersionConflict, "数据已被其他人修改，请刷新后重试")
	// ErrFileTooLarge 上传内容超过 storage.max_file_size（413）。
	ErrFileTooLarge = New(CodeFileTooLarge, "文件超过大小上限")
	// ErrUnsupportedMediaType 文件扩展名不在 storage.allowed_extensions 白名单内（415）。
	ErrUnsupportedMediaType = New(CodeUnsupportedMediaType, "不支持的文件类型")
	// ErrInternal 未识别的服务端错误。
	ErrInternal = New(CodeInternalError, "服务端内部错误")
)

// AsError 把任意 error 归一化为 *Error。未识别的错误映射为 internal_error，
// 保证 HTTP 层永远不会因为未注册的错误类型而输出裸 500 文本或泄漏堆栈。
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var domainErr *Error
	if errors.As(err, &domainErr) {
		return domainErr
	}
	return ErrInternal.WithCause(err)
}

// AsErrorAs 等价于 errors.As(err, **Error)，用于只判断"是否领域错误"的场景。
//
// 命名刻意区分 AsError（归一化，总是返回值）与本函数（类型断言，可能失败）。
func AsErrorAs(err error, target **Error) bool {
	return errors.As(err, target)
}

// MergeErrorFields 把 err 的字段级明细合并进 fields，并返回是否发生了合并。
//
// 用途：字段校验函数（如 ValidateDateRange、ValidateProgress）各自返回带 fields 的
// 领域错误，调用方需要把多组明细汇总成一次 422 响应，而不是逐次往返。
func MergeErrorFields(err error, fields map[string]string) bool {
	var domainErr *Error
	if !errors.As(err, &domainErr) || len(domainErr.Fields) == 0 {
		return false
	}
	for key, value := range domainErr.Fields {
		fields[key] = value
	}
	return true
}

// PreferDateRange 在 fields 中优先识别日期区间错误。
//
// 契约（docs/04 HTTP接口契约 2.4）把"结束日期早于开始日期"单列为
// 400 invalid_date_range，而不是笼统的 422 validation_error。
// 因此聚合型校验函数必须区分这两类错误，不能把日期区间问题
// 混进普通的字段校验集合里——否则客户端的错误码分支会失效。
//
// 返回 dateErr 非 nil 时，调用方应直接返回它（可附带已收集的其它 fields），
// 而不是返回 422。
func PreferDateRange(fields map[string]string, candidates ...error) *Error {
	for _, candidate := range candidates {
		var domainErr *Error
		if !errors.As(candidate, &domainErr) {
			continue
		}
		if domainErr.Code != CodeInvalidDateRange {
			continue
		}
		return domainErr.WithFields(fields)
	}
	return nil
}
