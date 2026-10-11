package idgen

import (
	"strings"
	"testing"
	"time"
)

func TestUUIDFormat(t *testing.T) {
	t.Parallel()

	generator := New()

	for i := 0; i < 50; i++ {
		id, err := generator.UUID()
		if err != nil {
			t.Fatalf("UUID 生成失败: %v", err)
		}
		if len(id) != 36 {
			t.Fatalf("UUID 长度 = %d，期望 36: %q", len(id), id)
		}
		if !IsUUID(id) {
			t.Fatalf("UUID 格式不合法: %q", id)
		}
		// 必须是小写带连字符形式（全局基线约定 4.1）。
		if id != strings.ToLower(id) {
			t.Fatalf("UUID 必须为小写: %q", id)
		}
		for _, position := range []int{8, 13, 18, 23} {
			if id[position] != '-' {
				t.Fatalf("UUID 第 %d 位应为连字符: %q", position, id)
			}
		}
	}
}

func TestTimeOrderedUUIDVersionAndVariantBits(t *testing.T) {
	t.Parallel()

	id, err := NewTimeOrdered(func() time.Time {
		return time.Date(2026, 10, 11, 9, 30, 0, 0, time.UTC)
	}).UUID()
	if err != nil {
		t.Fatalf("UUID 生成失败: %v", err)
	}

	// 版本位：第 13 个十六进制字符必须是 '7'（UUIDv7）。
	if id[14] != '7' {
		t.Fatalf("UUIDv7 的版本位应为 7，实际 id = %q", id)
	}
	// 变体位：第 17 个十六进制字符必须是 8/9/a/b 之一。
	switch id[19] {
	case '8', '9', 'a', 'b':
	default:
		t.Fatalf("UUID 的变体位不合法，实际 id = %q", id)
	}
}

func TestTimeOrderedUUIDsAreMonotonic(t *testing.T) {
	t.Parallel()

	// UUIDv7 的核心收益：同一时钟下按生成顺序字典序递增，可直接用于排序。
	base := time.Date(2026, 10, 11, 9, 30, 0, 0, time.UTC)
	current := base
	generator := NewTimeOrdered(func() time.Time { return current })

	var previous string
	for i := 0; i < 20; i++ {
		id, err := generator.UUID()
		if err != nil {
			t.Fatalf("UUID 生成失败: %v", err)
		}
		if previous != "" && id <= previous {
			t.Fatalf("UUIDv7 必须单调递增：%q 未大于 %q（第 %d 次）", id, previous, i)
		}
		previous = id
		current = current.Add(time.Millisecond)
	}
}

func TestTimeOrderedUUIDEncodesTimestamp(t *testing.T) {
	t.Parallel()

	// 前 48 位是毫秒级 Unix 时间戳，因此时间不同必然导致前缀不同。
	early, err := NewTimeOrdered(func() time.Time {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	}).UUID()
	if err != nil {
		t.Fatalf("UUID 生成失败: %v", err)
	}

	late, err := NewTimeOrdered(func() time.Time {
		return time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	}).UUID()
	if err != nil {
		t.Fatalf("UUID 生成失败: %v", err)
	}

	if early[:8] == late[:8] {
		t.Fatalf("不同时间戳应产生不同的 UUID 前缀: %q vs %q", early, late)
	}
	if early >= late {
		t.Fatalf("较早时间生成的 UUID 必须小于较晚时间生成的: %q vs %q", early, late)
	}
}

func TestRandomUUIDIsVersion4(t *testing.T) {
	t.Parallel()

	generator := NewRandom()
	for i := 0; i < 20; i++ {
		id, err := generator.UUID()
		if err != nil {
			t.Fatalf("UUID 生成失败: %v", err)
		}
		if id[14] != '4' {
			t.Fatalf("NewRandom 应生成 UUIDv4，实际 id = %q", id)
		}
		switch id[19] {
		case '8', '9', 'a', 'b':
		default:
			t.Fatalf("UUID 的变体位不合法: %q", id)
		}
	}
}

func TestUUIDsAreUnique(t *testing.T) {
	t.Parallel()

	generator := New()
	seen := make(map[string]bool, 2000)
	for i := 0; i < 2000; i++ {
		id, err := generator.UUID()
		if err != nil {
			t.Fatalf("UUID 生成失败: %v", err)
		}
		if seen[id] {
			t.Fatalf("UUID 重复（第 %d 次）: %q", i, id)
		}
		seen[id] = true
	}
}

func TestTokenIsSecureRandom(t *testing.T) {
	t.Parallel()

	generator := New()
	seen := make(map[string]bool, 500)

	for i := 0; i < 500; i++ {
		token, err := generator.Token()
		if err != nil {
			t.Fatalf("令牌生成失败: %v", err)
		}
		// 64 个小写十六进制字符 = 256 位熵。
		if len(token) != 64 {
			t.Fatalf("令牌长度 = %d，期望 64: %q", len(token), token)
		}
		if token != strings.ToLower(token) {
			t.Fatalf("令牌必须为小写十六进制: %q", token)
		}
		for _, r := range token {
			isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')
			if !isHex {
				t.Fatalf("令牌包含非十六进制字符 %q: %q", r, token)
			}
		}
		if seen[token] {
			t.Fatalf("令牌重复（第 %d 次）: %q", i, token)
		}
		seen[token] = true
	}
}

func TestTokenIsNotDerivedFromTime(t *testing.T) {
	t.Parallel()

	// 全局基线约定 4.2：安全令牌不得使用可预测的 UUIDv7。
	// 固定时钟下连续生成的两个令牌必须不同，证明随机性来自随机源而非时间。
	fixed := func() time.Time { return time.Date(2026, 10, 11, 9, 30, 0, 0, time.UTC) }
	generator := NewTimeOrdered(fixed)

	first, err := generator.Token()
	if err != nil {
		t.Fatalf("令牌生成失败: %v", err)
	}
	second, err := generator.Token()
	if err != nil {
		t.Fatalf("令牌生成失败: %v", err)
	}
	if first == second {
		t.Fatal("固定时钟下令牌不得重复，说明令牌来自时间而非随机源")
	}
}

func TestRequestID(t *testing.T) {
	t.Parallel()

	generator := New()
	seen := make(map[string]bool, 500)

	for i := 0; i < 500; i++ {
		requestID := generator.RequestID()
		if len(requestID) != 32 {
			t.Fatalf("request id 长度 = %d，期望 32: %q", len(requestID), requestID)
		}
		if !strings.Contains("0123456789abcdef", string(requestID[0])) {
			t.Fatalf("request id 必须为小写十六进制: %q", requestID)
		}
		if seen[requestID] {
			t.Fatalf("request id 重复: %q", requestID)
		}
		seen[requestID] = true
	}
}

func TestMustUUID(t *testing.T) {
	t.Parallel()

	if id := New().MustUUID(); !IsUUID(id) {
		t.Fatalf("MustUUID 返回了非法 UUID: %q", id)
	}
}

func TestIsUUID(t *testing.T) {
	t.Parallel()

	valid := "0192f2e5-7d2a-7abc-8b01-123456789abc"
	if !IsUUID(valid) {
		t.Fatalf("IsUUID(%q) 应为 true", valid)
	}

	invalid := []string{
		"",
		"0192f2e57d2a7abc8b01123456789abc",    // 缺少连字符
		"0192f2e5-7d2a-7abc-8b01-123456789ab", // 过短
		"0192f2e5-7d2a-7abc-8b01-123456789abcd",
		"0192f2e5_7d2a_7abc_8b01_123456789abc", // 下划线分隔
		"g192f2e5-7d2a-7abc-8b01-123456789abc", // 非十六进制字符
		"0192f2e5-7d2a-7abc-8b01-123456789ABC", // 大写仍算合法（十六进制）
	}
	// 最后一项实际上是合法的十六进制 UUID 形式，单独断言。
	for _, candidate := range invalid[:len(invalid)-1] {
		if IsUUID(candidate) {
			t.Errorf("IsUUID(%q) 应为 false", candidate)
		}
	}
	if !IsUUID(invalid[len(invalid)-1]) {
		t.Errorf("IsUUID 应接受大写十六进制：%q", invalid[len(invalid)-1])
	}
}

func TestNewUsesSystemClock(t *testing.T) {
	t.Parallel()

	generator := New()
	if generator.now == nil {
		t.Fatal("New 必须配置默认时钟")
	}
	if generator.random {
		t.Fatal("New 必须生成时间有序 UUID，而不是随机 UUID")
	}
	// 默认时钟必须返回 UTC。
	if got := generator.now(); got.Location() != time.UTC {
		t.Fatalf("默认时钟必须返回 UTC，实际时区 %v", got.Location())
	}
}

func TestNewTimeOrderedWithNilClockFallsBack(t *testing.T) {
	t.Parallel()

	generator := NewTimeOrdered(nil)
	if generator.now == nil {
		t.Fatal("NewTimeOrdered(nil) 必须提供默认时钟")
	}
	if id, err := generator.UUID(); err != nil || !IsUUID(id) {
		t.Fatalf("NewTimeOrdered(nil) 应可正常生成 UUID，实际 id=%q err=%v", id, err)
	}
}
