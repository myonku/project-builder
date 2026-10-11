package domain

import (
	"errors"
	"testing"
	"time"
)

// 本文件是测试专用辅助（文件名以 _test.go 结尾，不会进入生产构建）。
// 与测试同包，因此可以直接使用未导出标识符，也不需要绕开导入环。

// mustDate 解析 YYYY-MM-DD 日期，失败即让测试失败。
func mustDate(t *testing.T, raw string) time.Time {
	t.Helper()
	parsed, err := time.Parse(DateLayout, raw)
	if err != nil {
		t.Fatalf("解析日期 %q 失败: %v", raw, err)
	}
	return parsed.UTC()
}

// mustTimestamp 解析 RFC3339 瞬时时间，失败即让测试失败。
func mustTimestamp(t *testing.T, raw string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("解析时间 %q 失败: %v", raw, err)
	}
	return parsed.UTC()
}

// requireDomainError 断言 err 是指定错误码的领域错误，并返回该错误以便进一步断言字段明细。
func requireDomainError(t *testing.T, err error, code ErrorCode) *Error {
	t.Helper()

	var domainErr *Error
	if !errors.As(err, &domainErr) {
		t.Fatalf("期望错误码 %q 的领域错误，实际为 %T: %v", code, err, err)
	}
	if domainErr.Code != code {
		t.Fatalf("错误码 = %q，期望 %q（完整错误：%v）", domainErr.Code, code, domainErr)
	}
	return domainErr
}
