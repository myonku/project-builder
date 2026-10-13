package httpapi

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/myonku/project-builder/internal/domain"
)

// Timestamp 是瞬时时间的 JSON 表示，格式为 UTC ISO 8601（带 Z 或明确偏移）。
//
// 为什么自定义而不是直接用 time.Time：
// time.Time 的默认解码错误文案是 Go 内部的（如 `parsing time "x" as ...`），
// 既不面向接口使用者，也无法放进取证用的 fields 字段。这里统一转成
// 422 validation_error + 明确的中文说明，与其他字段校验保持一致。
type Timestamp struct {
	time.Time
}

// UnmarshalJSON 实现 json.Unmarshaler。
func (t *Timestamp) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return domain.ErrValidation.
			WithField("timestamp", "时间必须是 ISO 8601 字符串")
	}
	parsed, err := time.Parse(domain.TimestampLayout, raw)
	if err != nil {
		// 兼容不带小数秒的写法（time.RFC3339 是其子集）。
		parsed, err = time.Parse(time.RFC3339, raw)
	}
	if err != nil {
		return domain.ErrValidation.
			WithField("timestamp", "时间必须是 ISO 8601 格式，例如 2026-09-30T04:00:00Z")
	}
	t.Time = parsed.UTC()
	return nil
}

// MarshalJSON 实现 json.Marshaler，始终输出 UTC。
func (t Timestamp) MarshalJSON() ([]byte, error) {
	return json.Marshal(domain.FormatTimestamp(t.Time))
}

// Date 是业务日期的 JSON 表示，格式为 YYYY-MM-DD（自然日，不含时分秒）。
type Date struct {
	time.Time
}

// UnmarshalJSON 实现 json.Unmarshaler。
func (d *Date) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return domain.ErrValidation.WithField("date", "日期必须是 YYYY-MM-DD 字符串")
	}
	parsed, err := time.Parse(domain.DateLayout, raw)
	if err != nil {
		return domain.ErrValidation.
			WithField("date", "日期格式必须为 YYYY-MM-DD，例如 2026-10-01")
	}
	d.Time = parsed.UTC()
	return nil
}

// MarshalJSON 实现 json.Marshaler。
func (d Date) MarshalJSON() ([]byte, error) {
	return json.Marshal(domain.FormatDate(d.Time))
}

// IsNull reports whether the raw JSON value for key in body is the literal null.
//
// 用途：区分"字段省略"与"字段显式为 null"。两者在 *string / *int 上都会得到 nil，
// 但语义不同——例如 tasks 的 parent_id 需要"省略=不修改"与"null=置为顶层任务"。
//
// 实现方式：在已解码的原始报文上做一次流式扫描，避免为了区分而定义额外包装类型。
// 缺键、格式非法都返回 false（视为"未提供"）。
func IsNull(body []byte, key string) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()

	token, err := decoder.Token()
	if err != nil {
		return false
	}
	// 期望对象起始符 '{'
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return false
	}

	for decoder.More() {
		nameToken, err := decoder.Token()
		if err != nil {
			return false
		}
		name, ok := nameToken.(string)
		if !ok {
			return false
		}

		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return false
		}
		if name == key {
			return string(bytes.TrimSpace(raw)) == "null"
		}
	}
	return false
}

// HasKey reports whether body contains the given top-level key.
//
// 用于区分"未提供"与"提供了空值"（例如 description = ""）。
func HasKey(body []byte, key string) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()

	token, err := decoder.Token()
	if err != nil {
		return false
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return false
	}

	for decoder.More() {
		nameToken, err := decoder.Token()
		if err != nil {
			return false
		}
		name, ok := nameToken.(string)
		if !ok {
			return false
		}

		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return false
		}
		if name == key {
			return true
		}
	}
	return false
}
