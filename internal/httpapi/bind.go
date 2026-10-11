package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"

	"github.com/myonku/project-builder/internal/domain"
)

// BindJSON 解析并校验 JSON 请求体，把框架绑定错误统一转换为领域错误。
//
// 历史缺陷对照：Python 版本使用 lihil/msgspec，校验元数据对 JSON body 静默失效
// （page_size=9999 与 page=0 均通过），且未注册求解器的异常直接以纯文本 500 返回。
// 这里把三类失败分别映射到契约规定的错误码：
//   - 请求体不是合法 JSON / 类型不符 → 400 invalid_request；
//   - 字段约束不满足（必填、长度、范围） → 422 validation_error，并带 fields；
//   - 请求体过大 → 413 file_too_large 不适用，改用 400 并说明原因。
func BindJSON(c *gin.Context, target any) error {
	_, err := BindJSONWithBody(c, target)
	return err
}

// BindJSONWithBody 与 BindJSON 相同，但额外返回原始请求体。
//
// 需要原始报文的场景：区分"字段省略"与"字段显式为 null"
// （例如 tasks 的 parent_id：省略=不修改，null=置为顶层任务）。
// 该区分无法仅凭解码后的 Go 值判断，因此必须保留字节。
func BindJSONWithBody(c *gin.Context, target any) ([]byte, error) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, domain.ErrInvalidRequest.WithField("body", "读取请求体失败")
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, domain.ErrInvalidRequest.WithField("body", "请求体不能为空")
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	// 拒绝未知字段：契约未定义的字段静默丢弃会让客户端以为设置生效了，
	// 例如把 expected_updated_at 拼错成 expected_update_at 会静默退化为 last-write-wins。
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(target); err != nil {
		return body, decodeError(err)
	}

	// 确保请求体只有一个 JSON 值，多余内容视为请求不合法。
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return body, domain.ErrInvalidRequest.WithField("body", "请求体必须是单个 JSON 对象")
	}

	if err := validateStruct(target); err != nil {
		return body, err
	}
	return body, nil
}

// decodeError 把 JSON 解码错误翻译为领域错误。
func decodeError(err error) error {
	var syntaxErr *json.SyntaxError
	var typeErr *json.UnmarshalTypeError

	switch {
	case errors.Is(err, io.EOF):
		return domain.ErrInvalidRequest.WithField("body", "请求体不能为空")
	case errors.As(err, &syntaxErr):
		return domain.ErrInvalidRequest.WithField("body",
			fmt.Sprintf("JSON 语法错误（偏移 %d）", syntaxErr.Offset))
	case errors.As(err, &typeErr):
		field := typeErr.Field
		if field == "" {
			field = "body"
		}
		return domain.ErrInvalidRequest.WithField(field,
			fmt.Sprintf("字段类型应为 %s", typeErr.Type.String()))
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		unknown := strings.Trim(strings.TrimPrefix(err.Error(), "json: unknown field "), `"`)
		return domain.ErrValidation.WithField(unknown, "请求包含契约未定义的字段")
	default:
		return domain.ErrInvalidRequest.WithField("body", "请求体无法解析")
	}
}

// validatorInstance 是包级复用的校验器。
//
// validator.Validate 是并发安全的（内部缓存带锁），复用可避免每次请求反射构建校验器。
var validatorInstance = validator.New(validator.WithRequiredStructEnabled())

// validateStruct 执行结构体标签校验，并把结果转换为带 fields 的领域错误。
func validateStruct(target any) error {
	err := validatorInstance.Struct(target)
	if err == nil {
		return nil
	}

	var validationErrs validator.ValidationErrors
	if !errors.As(err, &validationErrs) {
		return domain.ErrInternal.WithCause(err)
	}

	fields := make(map[string]string, len(validationErrs))
	for _, fieldErr := range validationErrs {
		name := jsonFieldName(target, fieldErr.StructField())
		fields[name] = describeValidation(fieldErr)
	}
	return domain.ErrValidation.WithFields(fields)
}

// jsonFieldName 返回字段的 JSON 名，未标注时退化为 Go 字段名。
func jsonFieldName(target any, structField string) string {
	field, ok := reflect.TypeOf(target).Elem().FieldByName(structField)
	if !ok {
		return structField
	}
	tag := field.Tag.Get("json")
	if tag == "" {
		return structField
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" || name == "-" {
		return structField
	}
	return name
}

// describeValidation 把校验标签翻译为中文说明。
func describeValidation(fieldErr validator.FieldError) string {
	switch fieldErr.Tag() {
	case "required":
		return "该字段为必填项"
	case "email":
		return "邮箱格式不正确"
	case "uuid":
		return "必须是合法的 UUID"
	case "min":
		return fmt.Sprintf("长度或取值不得小于 %s", fieldErr.Param())
	case "max":
		return fmt.Sprintf("长度或取值不得大于 %s", fieldErr.Param())
	case "gte":
		return fmt.Sprintf("取值不得小于 %s", fieldErr.Param())
	case "lte":
		return fmt.Sprintf("取值不得大于 %s", fieldErr.Param())
	case "oneof":
		return fmt.Sprintf("取值必须是以下之一：%s", fieldErr.Param())
	case "datetime":
		return fmt.Sprintf("时间格式必须为 %s", fieldErr.Param())
	default:
		return fmt.Sprintf("不满足校验规则 %s", fieldErr.Tag())
	}
}
