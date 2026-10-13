package platform

import (
	"strings"
	"testing"
)

// testParams 是低成本参数，仅用于验证摘要逻辑本身。
func testParams() Argon2Params {
	return Argon2Params{
		Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32,
	}
}

func TestHashProducesParseablePHCString(t *testing.T) {
	t.Parallel()

	hasher := NewPasswordHasherWith(testParams())
	hash, err := hasher.Hash("correct horse battery")
	if err != nil {
		t.Fatalf("Hash 返回错误: %v", err)
	}

	// PHC 格式：$argon2id$v=19$m=...,t=...,p=...$<salt>$<hash>
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=8192,t=1,p=1$") {
		t.Fatalf("摘要前缀不符合 PHC 格式: %q", hash)
	}
	if strings.Count(hash, "$") != 5 {
		t.Fatalf("摘要应有 5 个分隔符: %q", hash)
	}
	if strings.Contains(hash, "correct horse battery") {
		t.Fatalf("摘要不得包含明文密码: %q", hash)
	}
}

func TestHashIsSaltedPerCall(t *testing.T) {
	t.Parallel()

	hasher := NewPasswordHasherWith(testParams())

	first, err := hasher.Hash("same password")
	if err != nil {
		t.Fatalf("Hash 返回错误: %v", err)
	}
	second, err := hasher.Hash("same password")
	if err != nil {
		t.Fatalf("Hash 返回错误: %v", err)
	}

	// 相同密码必须产生不同摘要（盐随机），否则彩虹表攻击可行。
	if first == second {
		t.Fatal("相同密码的两次摘要必须不同（必须每次使用新盐）")
	}
}

func TestVerifyAcceptsCorrectPassword(t *testing.T) {
	t.Parallel()

	hasher := NewPasswordHasherWith(testParams())
	hash, err := hasher.Hash("correct horse battery")
	if err != nil {
		t.Fatalf("Hash 返回错误: %v", err)
	}

	tests := []string{
		"correct horse battery",
		"correct horse battery ", // 尾随空格是不同密码
		" correct horse battery", // 前导空格同理
	}
	for _, candidate := range tests[:1] {
		matched, err := hasher.Verify(candidate, hash)
		if err != nil {
			t.Fatalf("Verify 返回错误: %v", err)
		}
		if !matched {
			t.Fatalf("正确密码 %q 必须通过校验", candidate)
		}
	}

	// 空格敏感：这是密码语义的一部分，不得被静默规范化。
	for _, candidate := range tests[1:] {
		matched, err := hasher.Verify(candidate, hash)
		if err != nil {
			t.Fatalf("Verify 返回错误: %v", err)
		}
		if matched {
			t.Fatalf("带有额外空格的密码 %q 不应通过校验", candidate)
		}
	}
}

func TestVerifyRejectsWrongPassword(t *testing.T) {
	t.Parallel()

	hasher := NewPasswordHasherWith(testParams())
	hash, err := hasher.Hash("correct horse battery")
	if err != nil {
		t.Fatalf("Hash 返回错误: %v", err)
	}

	wrong := []string{
		"",
		"wrong",
		"correct horse batter",
		"Correct Horse Battery",
		"correct horse batteryy",
	}
	for _, candidate := range wrong {
		matched, err := hasher.Verify(candidate, hash)
		if err != nil {
			t.Fatalf("Verify(%q) 返回错误: %v", candidate, err)
		}
		if matched {
			t.Errorf("错误密码 %q 不应通过校验", candidate)
		}
	}
}

func TestVerifyHandlesUnicodePasswords(t *testing.T) {
	t.Parallel()

	hasher := NewPasswordHasherWith(testParams())
	password := "密码-🔐-пароль-ß"

	hash, err := hasher.Hash(password)
	if err != nil {
		t.Fatalf("Hash 返回错误: %v", err)
	}

	matched, err := hasher.Verify(password, hash)
	if err != nil {
		t.Fatalf("Verify 返回错误: %v", err)
	}
	if !matched {
		t.Fatal("Unicode 密码必须能通过校验")
	}

	matched, err = hasher.Verify("密码-🔐-пароль-s", hash)
	if err != nil {
		t.Fatalf("Verify 返回错误: %v", err)
	}
	if matched {
		t.Fatal("Unicode 密码的细微差异不应通过校验")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	t.Parallel()

	hasher := NewPasswordHasherWith(testParams())

	malformed := []struct {
		name string
		hash string
	}{
		{name: "空串", hash: ""},
		{name: "缺少段", hash: "$argon2id$v=19$m=8192,t=1,p=1$c2FsdA"},
		{name: "算法不支持", hash: "$argon2i$v=19$m=8192,t=1,p=1$c2FsdA$aGFzaA"},
		{name: "版本不支持", hash: "$argon2id$v=13$m=8192,t=1,p=1$c2FsdA$aGFzaA"},
		{name: "参数段损坏", hash: "$argon2id$v=19$m=abc,t=1,p=1$c2FsdA$aGFzaA"},
		{name: "盐不是 base64", hash: "$argon2id$v=19$m=8192,t=1,p=1$!!!!$aGFzaA"},
		{name: "摘要不是 base64", hash: "$argon2id$v=19$m=8192,t=1,p=1$c2FsdA$!!!!"},
		{name: "盐为空", hash: "$argon2id$v=19$m=8192,t=1,p=1$$aGFzaA"},
		{name: "明文密码", hash: "correct horse battery"},
	}

	for _, tc := range malformed {
		t.Run(tc.name, func(t *testing.T) {
			matched, err := hasher.Verify("correct horse battery", tc.hash)
			if err == nil {
				t.Fatalf("非法摘要必须返回错误（matched=%v）", matched)
			}
			if matched {
				t.Fatal("非法摘要不得报告匹配成功")
			}
		})
	}
}

func TestNeedsRehash(t *testing.T) {
	t.Parallel()

	strong := NewPasswordHasherWith(Argon2Params{
		Memory: 64 * 1024, Iterations: 3, Parallelism: 2, SaltLength: 16, KeyLength: 32,
	})
	weak := NewPasswordHasherWith(Argon2Params{
		Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32,
	})

	strongHash, err := strong.Hash("password123")
	if err != nil {
		t.Fatalf("Hash 返回错误: %v", err)
	}
	weakHash, err := weak.Hash("password123")
	if err != nil {
		t.Fatalf("Hash 返回错误: %v", err)
	}
	// 摘要长度更短的也必须判定为需要升级。
	shortHash, err := NewPasswordHasherWith(Argon2Params{
		Memory: 64 * 1024, Iterations: 3, Parallelism: 2, SaltLength: 16, KeyLength: 16,
	}).Hash("password123")
	if err != nil {
		t.Fatalf("Hash 返回错误: %v", err)
	}

	tests := []struct {
		name   string
		hasher *PasswordHasher
		hash   string
		want   bool
	}{
		{name: "参数相同无需升级", hasher: strong, hash: strongHash, want: false},
		{name: "参数更弱需要升级", hasher: strong, hash: weakHash, want: true},
		{name: "摘要更短需要升级", hasher: strong, hash: shortHash, want: true},
		{name: "非法摘要视为需要升级", hasher: strong, hash: "not-a-hash", want: true},
		{name: "空摘要视为需要升级", hasher: strong, hash: "", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.hasher.NeedsRehash(tc.hash); got != tc.want {
				t.Fatalf("NeedsRehash = %v，期望 %v", got, tc.want)
			}
		})
	}
}

func TestDefaultParamsAreStrongEnough(t *testing.T) {
	t.Parallel()

	params := DefaultArgon2Params()

	// 锁定下限，防止将来把强度调低而无人察觉。
	if params.Memory < 19*1024 {
		t.Errorf("内存参数 = %d KiB，低于 OWASP 对 Argon2id 的建议下限 19456 KiB", params.Memory)
	}
	if params.Iterations < 2 {
		t.Errorf("迭代次数 = %d，应至少为 2", params.Iterations)
	}
	if params.Parallelism < 1 {
		t.Errorf("并行度 = %d，应至少为 1", params.Parallelism)
	}
	if params.SaltLength < 16 {
		t.Errorf("盐长度 = %d 字节，应至少为 16", params.SaltLength)
	}
	if params.KeyLength < 32 {
		t.Errorf("摘要长度 = %d 字节，应至少为 32", params.KeyLength)
	}
}

func TestDefaultHasherRoundTrip(t *testing.T) {
	t.Parallel()

	// 使用真实默认参数做一次完整往返，确保生产参数组合本身可用。
	hasher := NewPasswordHasher()
	hash, err := hasher.Hash("a strong enough password")
	if err != nil {
		t.Fatalf("Hash 返回错误: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("默认参数摘要前缀不符合预期: %q", hash)
	}

	matched, err := hasher.Verify("a strong enough password", hash)
	if err != nil {
		t.Fatalf("Verify 返回错误: %v", err)
	}
	if !matched {
		t.Fatal("默认参数下正确密码必须通过校验")
	}
}
