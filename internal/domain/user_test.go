package domain

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeRegistrationAcceptsValidInput(t *testing.T) {
	t.Parallel()

	got, fields := NormalizeRegistration(RegistrationInput{
		Username:    "  Alice_01  ",
		Email:       "  Alice@Example.COM ",
		DisplayName: "  爱丽丝  ",
		Password:    "correct horse battery",
		Timezone:    "Asia/Shanghai",
	}, 8)

	if len(fields) != 0 {
		t.Fatalf("期望无字段错误，实际为 %v", fields)
	}
	if got.Username != "alice_01" {
		t.Errorf("username = %q，期望归一化为小写 %q", got.Username, "alice_01")
	}
	if got.Email != "alice@example.com" {
		t.Errorf("email = %q，期望归一化为小写 %q", got.Email, "alice@example.com")
	}
	if got.DisplayName != "爱丽丝" {
		t.Errorf("display_name = %q，期望去除首尾空格", got.DisplayName)
	}
	if got.Password != "correct horse battery" {
		t.Errorf("password 不应被改写，实际为 %q", got.Password)
	}
	if got.Timezone != "Asia/Shanghai" {
		t.Errorf("timezone = %q，期望保留", got.Timezone)
	}
}

func TestNormalizeRegistrationDefaultsTimezone(t *testing.T) {
	t.Parallel()

	got, fields := NormalizeRegistration(RegistrationInput{
		Username:    "bob",
		Email:       "bob@example.com",
		DisplayName: "Bob",
		Password:    "password123",
	}, 8)

	if len(fields) != 0 {
		t.Fatalf("期望无字段错误，实际为 %v", fields)
	}
	if got.Timezone != "UTC" {
		t.Fatalf("timezone = %q，期望默认 UTC", got.Timezone)
	}
}

func TestNormalizeRegistrationRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	valid := RegistrationInput{
		Username:    "alice",
		Email:       "alice@example.com",
		DisplayName: "Alice",
		Password:    "password123",
		Timezone:    "UTC",
	}

	tests := []struct {
		name      string
		mutate    func(in RegistrationInput) RegistrationInput
		wantField string
	}{
		{
			name:      "用户名为空",
			mutate:    func(in RegistrationInput) RegistrationInput { in.Username = "   "; return in },
			wantField: "username",
		},
		{
			name:      "用户名过短",
			mutate:    func(in RegistrationInput) RegistrationInput { in.Username = "ab"; return in },
			wantField: "username",
		},
		{
			name:      "用户名过长",
			mutate:    func(in RegistrationInput) RegistrationInput { in.Username = strings.Repeat("a", 65); return in },
			wantField: "username",
		},
		{
			name:      "用户名含非法字符",
			mutate:    func(in RegistrationInput) RegistrationInput { in.Username = "alice.bob"; return in },
			wantField: "username",
		},
		{
			name:      "用户名含中文",
			mutate:    func(in RegistrationInput) RegistrationInput { in.Username = "爱丽丝"; return in },
			wantField: "username",
		},
		{
			name:      "邮箱为空",
			mutate:    func(in RegistrationInput) RegistrationInput { in.Email = ""; return in },
			wantField: "email",
		},
		{
			name:      "邮箱缺少域名点",
			mutate:    func(in RegistrationInput) RegistrationInput { in.Email = "alice@localhost"; return in },
			wantField: "email",
		},
		{
			name:      "邮箱含空格",
			mutate:    func(in RegistrationInput) RegistrationInput { in.Email = "ali ce@example.com"; return in },
			wantField: "email",
		},
		{
			name: "邮箱过长",
			mutate: func(in RegistrationInput) RegistrationInput {
				in.Email = strings.Repeat("a", 320) + "@example.com"
				return in
			},
			wantField: "email",
		},
		{
			name:      "显示名称为空",
			mutate:    func(in RegistrationInput) RegistrationInput { in.DisplayName = "  "; return in },
			wantField: "display_name",
		},
		{
			name:      "显示名称过长",
			mutate:    func(in RegistrationInput) RegistrationInput { in.DisplayName = strings.Repeat("名", 129); return in },
			wantField: "display_name",
		},
		{
			name:      "密码为空",
			mutate:    func(in RegistrationInput) RegistrationInput { in.Password = ""; return in },
			wantField: "password",
		},
		{
			name:      "密码短于策略",
			mutate:    func(in RegistrationInput) RegistrationInput { in.Password = "short"; return in },
			wantField: "password",
		},
		{
			name:      "密码过长",
			mutate:    func(in RegistrationInput) RegistrationInput { in.Password = strings.Repeat("x", 257); return in },
			wantField: "password",
		},
		{
			name:      "时区无效",
			mutate:    func(in RegistrationInput) RegistrationInput { in.Timezone = "Mars/Olympus"; return in },
			wantField: "timezone",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, fields := NormalizeRegistration(tc.mutate(valid), 8)
			if _, ok := fields[tc.wantField]; !ok {
				t.Fatalf("期望字段 %q 报错，实际字段错误为 %v", tc.wantField, fields)
			}
		})
	}
}

func TestNormalizeRegistrationPreservesPasswordWhitespace(t *testing.T) {
	t.Parallel()

	// 前后空格是合法密码字符，不得被 Trim 掉，否则会静默改变用户凭据。
	in := RegistrationInput{
		Username:    "alice",
		Email:       "alice@example.com",
		DisplayName: "Alice",
		Password:    "  spaced password  ",
	}
	got, fields := NormalizeRegistration(in, 8)
	if len(fields) != 0 {
		t.Fatalf("期望无字段错误，实际为 %v", fields)
	}
	if got.Password != in.Password {
		t.Fatalf("password 被改写为 %q，期望保留 %q", got.Password, in.Password)
	}
}

func TestNormalizeRegistrationReportsAllFieldErrorsAtOnce(t *testing.T) {
	t.Parallel()

	// 一次返回全部字段错误，避免前端逐次往返修正。
	_, fields := NormalizeRegistration(RegistrationInput{}, 8)
	want := []string{"username", "email", "display_name", "password"}
	for _, key := range want {
		if _, ok := fields[key]; !ok {
			t.Errorf("缺少字段错误 %q，实际为 %v", key, fields)
		}
	}
}

func TestNormalizeLogin(t *testing.T) {
	t.Parallel()

	t.Run("归一化大小写与空格", func(t *testing.T) {
		t.Parallel()
		got, fields := NormalizeLogin(LoginInput{Identifier: "  Alice@Example.com ", Password: "secret"})
		if len(fields) != 0 {
			t.Fatalf("期望无字段错误，实际为 %v", fields)
		}
		if got.Identifier != "alice@example.com" {
			t.Fatalf("identifier = %q，期望小写去空格", got.Identifier)
		}
	})

	t.Run("缺少凭据", func(t *testing.T) {
		t.Parallel()
		_, fields := NormalizeLogin(LoginInput{})
		for _, key := range []string{"identifier", "password"} {
			if _, ok := fields[key]; !ok {
				t.Errorf("缺少字段错误 %q，实际为 %v", key, fields)
			}
		}
	})
}

func TestLooksLikeEmail(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"alice@example.com": true,
		"alice":             false,
		"alice@":            true, // 仅用于选择查询路径，合法性由校验负责
		"":                  false,
	}
	for input, want := range tests {
		if got := LooksLikeEmail(input); got != want {
			t.Errorf("LooksLikeEmail(%q) = %v，期望 %v", input, got, want)
		}
	}
}

func TestTimezoneOrDefault(t *testing.T) {
	t.Parallel()

	tests := []struct {
		user     string
		fallback string
		want     string
	}{
		{user: "Asia/Shanghai", fallback: "UTC", want: "Asia/Shanghai"},
		{user: "", fallback: "Asia/Shanghai", want: "Asia/Shanghai"},
		{user: "  ", fallback: "Asia/Shanghai", want: "Asia/Shanghai"},
		{user: "", fallback: "", want: "UTC"},
	}
	for _, tc := range tests {
		if got := TimezoneOrDefault(tc.user, tc.fallback); got != tc.want {
			t.Errorf("TimezoneOrDefault(%q, %q) = %q，期望 %q", tc.user, tc.fallback, got, tc.want)
		}
	}
}

func TestErrorWithFieldIsImmutable(t *testing.T) {
	t.Parallel()

	base := ErrValidation
	derived := base.WithField("email", "邮箱格式不正确")

	if len(base.Fields) != 0 {
		t.Fatalf("派生错误改写了共享的基准错误: %v", base.Fields)
	}
	if derived.Fields["email"] != "邮箱格式不正确" {
		t.Fatalf("派生错误缺少字段明细: %v", derived.Fields)
	}
	if derived.Code != CodeValidationError {
		t.Fatalf("错误码 = %q，期望 %q", derived.Code, CodeValidationError)
	}
}

func TestErrorWithFieldsMergesWithoutMutating(t *testing.T) {
	t.Parallel()

	base := ErrValidation.WithField("a", "1")
	derived := base.WithFields(map[string]string{"b": "2"})

	if len(base.Fields) != 1 {
		t.Fatalf("基准错误被改写: %v", base.Fields)
	}
	if derived.Fields["a"] != "1" || derived.Fields["b"] != "2" {
		t.Fatalf("合并结果不正确: %v", derived.Fields)
	}
}

func TestAsErrorNormalizesUnknownErrors(t *testing.T) {
	t.Parallel()

	// 未识别的错误必须归一化为 internal_error，否则 HTTP 层会输出裸 500 文本
	// （Python 版本的实际缺陷，见 docs/01-Go服务端架构设计.md 5.7）。
	cause := errors.New(`pq: relation "users" does not exist`)
	got := AsError(cause)

	if got.Code != CodeInternalError {
		t.Fatalf("错误码 = %q，期望 %q", got.Code, CodeInternalError)
	}
	if !errors.Is(got, cause) {
		t.Fatal("归一化后的错误必须可通过 errors.Is 追溯到原始原因")
	}
}

func TestAsErrorPassesThroughDomainErrors(t *testing.T) {
	t.Parallel()

	original := ErrVersionConflict
	got := AsError(original)
	if got != original {
		t.Fatalf("期望原样返回领域错误，实际为新对象 %v", got)
	}
}

func TestAsErrorNil(t *testing.T) {
	t.Parallel()
	if got := AsError(nil); got != nil {
		t.Fatalf("AsError(nil) = %v，期望 nil", got)
	}
}

func TestErrorIsMatchesByCode(t *testing.T) {
	t.Parallel()

	detail := ErrValidation.WithField("name", "名称不能为空")
	if !errors.Is(detail, ErrValidation) {
		t.Fatal("相同错误码的错误应相互匹配，便于用例层按类别判断")
	}
	if errors.Is(detail, ErrPermissionDenied) {
		t.Fatal("不同错误码不应匹配")
	}
}

func TestErrorDoesNotLeakCauseInMessage(t *testing.T) {
	t.Parallel()

	err := Wrap(CodeInternalError, "邮件发送失败", errors.New("smtp password=hunter2 rejected"))
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("Error() 输出了内部原因，可能泄漏敏感值: %s", err.Error())
	}
}

func TestErrorUnwrapExposesCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("底层原因")
	err := Wrap(CodeInternalError, "包装后的描述", cause)
	if !errors.Is(err, cause) {
		t.Fatal("errors.Is 应能穿透到 Cause")
	}
}
