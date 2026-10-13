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
	// （早期实现的实际缺陷，见 backend/docs/B01-Go服务端架构设计.md 5.7）。
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

// ---------------------------------------------------------------------------
// 用户资料更新（第三阶段 Users 模块）
// ---------------------------------------------------------------------------

// strPtr / boolPtr 是构造"已提供的可选字段"的辅助函数。
func strPtr(value string) *string { return &value }
func boolPtr(value bool) *bool    { return &value }

func TestNormalizeUserProfileUpdateAcceptsValidInput(t *testing.T) {
	t.Parallel()

	got, fields := NormalizeUserProfileUpdate(UserProfileUpdate{
		DisplayName:     strPtr("  爱丽丝  "),
		Email:           strPtr("  Alice@Example.COM "),
		Timezone:        strPtr(" Asia/Shanghai "),
		ReminderEnabled: boolPtr(false),
	})
	if len(fields) != 0 {
		t.Fatalf("期望无字段错误，实际为 %v", fields)
	}
	if *got.DisplayName != "爱丽丝" {
		t.Errorf("display_name = %q，期望去除首尾空格", *got.DisplayName)
	}
	if *got.Email != "alice@example.com" {
		t.Errorf("email = %q，期望归一化为小写", *got.Email)
	}
	if *got.Timezone != "Asia/Shanghai" {
		t.Errorf("timezone = %q，期望去除首尾空格", *got.Timezone)
	}
	if *got.ReminderEnabled {
		t.Error("reminder_enabled 应保留 false，而不是被当成未提供")
	}
}

// TestNormalizeUserProfileUpdateKeepsProvidedFields 锁住红线第 6 条：
// 归一化不得把调用方已提供的字段变成 nil，否则更新会被静默忽略。
func TestNormalizeUserProfileUpdateKeepsProvidedFields(t *testing.T) {
	t.Parallel()

	got, _ := NormalizeUserProfileUpdate(UserProfileUpdate{
		DisplayName:     strPtr("Alice"),
		Email:           strPtr("alice@example.com"),
		Timezone:        strPtr("UTC"),
		ReminderEnabled: boolPtr(true),
		Status:          strPtr(UserStatusActive),
		SystemRole:      strPtr(UserSystemRoleAdmin),
	})

	switch {
	case got.DisplayName == nil:
		t.Error("display_name 被归一化丢弃")
	case got.Email == nil:
		t.Error("email 被归一化丢弃")
	case got.Timezone == nil:
		t.Error("timezone 被归一化丢弃")
	case got.ReminderEnabled == nil:
		t.Error("reminder_enabled 被归一化丢弃")
	case got.Status == nil:
		t.Error("status 被归一化丢弃")
	case got.SystemRole == nil:
		t.Error("system_role 被归一化丢弃")
	}
}

// TestNormalizeUserProfileUpdateLeavesOmittedFieldsNil 确保"未提供"仍是"不修改"。
func TestNormalizeUserProfileUpdateLeavesOmittedFieldsNil(t *testing.T) {
	t.Parallel()

	got, fields := NormalizeUserProfileUpdate(UserProfileUpdate{DisplayName: strPtr("Alice")})
	if len(fields) != 0 {
		t.Fatalf("期望无字段错误，实际为 %v", fields)
	}
	if got.Email != nil || got.Timezone != nil || got.ReminderEnabled != nil ||
		got.Status != nil || got.SystemRole != nil {
		t.Fatalf("未提供的字段必须保持 nil，实际为 %+v", got)
	}
}

func TestNormalizeUserProfileUpdateRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input UserProfileUpdate
		field string
	}{
		{"空白显示名", UserProfileUpdate{DisplayName: strPtr("   ")}, "display_name"},
		{"显示名超长", UserProfileUpdate{DisplayName: strPtr(strings.Repeat("字", 129))}, "display_name"},
		{"空邮箱", UserProfileUpdate{Email: strPtr("")}, "email"},
		{"邮箱格式非法", UserProfileUpdate{Email: strPtr("not-an-email")}, "email"},
		{"邮箱超长", UserProfileUpdate{Email: strPtr(strings.Repeat("a", 320) + "@example.com")}, "email"},
		// 更新时不允许清空时区：注册时"省略或为空"默认 UTC，更新时空值没有业务含义。
		{"空时区", UserProfileUpdate{Timezone: strPtr("")}, "timezone"},
		{"非法时区", UserProfileUpdate{Timezone: strPtr("Mars/Olympus")}, "timezone"},
		{"时区超长", UserProfileUpdate{Timezone: strPtr(strings.Repeat("a", 65))}, "timezone"},
		{"未知状态", UserProfileUpdate{Status: strPtr("deleted")}, "status"},
		{"未知角色", UserProfileUpdate{SystemRole: strPtr("superuser")}, "system_role"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, fields := NormalizeUserProfileUpdate(tc.input)
			if _, ok := fields[tc.field]; !ok {
				t.Fatalf("期望字段 %q 报错，实际为 %v", tc.field, fields)
			}
		})
	}
}

// TestUserProfileUpdateHasChanges 锁住"空更新"必须被拒绝这一契约加强项。
func TestUserProfileUpdateHasChanges(t *testing.T) {
	t.Parallel()

	if (UserProfileUpdate{}).HasChanges() {
		t.Error("空更新不应被视为有变更")
	}
	// 显式设为 false 是一次真实变更，不能与"未提供"混为一谈。
	if !(UserProfileUpdate{ReminderEnabled: boolPtr(false)}).HasChanges() {
		t.Error("显式关闭提醒应被视为有变更")
	}
}

func TestApplyUserProfileUpdateMergesOnlyProvidedFields(t *testing.T) {
	t.Parallel()

	current := User{
		ID: "u1", DisplayName: "Alice", Email: "alice@example.com",
		Status: UserStatusActive, SystemRole: UserSystemRoleAdmin, Timezone: "UTC",
	}
	merged := ApplyUserProfileUpdate(current, UserProfileUpdate{DisplayName: strPtr("Alicia")})

	if merged.DisplayName != "Alicia" {
		t.Errorf("display_name = %q，期望被更新", merged.DisplayName)
	}
	if merged.Email != current.Email || merged.Status != current.Status ||
		merged.SystemRole != current.SystemRole || merged.Timezone != current.Timezone {
		t.Fatalf("未提供的字段不应变化，实际为 %+v", merged)
	}
}

// TestWouldRemoveLastAdmin 覆盖"系统不得失去全部管理入口"这一不变式。
func TestWouldRemoveLastAdmin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		wasActiveAdmin    bool
		willBeActiveAdmin bool
		otherActiveAdmins int
		want              bool
	}{
		{"非管理员不触发", false, false, 0, false},
		{"仍是活跃管理员", true, true, 0, false},
		{"最后一个活跃管理员被降级", true, false, 0, true},
		{"最后一个活跃管理员被停用", true, false, 0, true},
		{"还有其他活跃管理员", true, false, 1, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := WouldRemoveLastAdmin(tc.wasActiveAdmin, tc.willBeActiveAdmin, tc.otherActiveAdmins)
			if got != tc.want {
				t.Fatalf("WouldRemoveLastAdmin(%v, %v, %d) = %v，期望 %v",
					tc.wasActiveAdmin, tc.willBeActiveAdmin, tc.otherActiveAdmins, got, tc.want)
			}
		})
	}
}

// TestUserProfilePermissions 覆盖可见性与修改权的判定矩阵。
func TestUserProfilePermissions(t *testing.T) {
	t.Parallel()

	actor := User{ID: "actor", Status: UserStatusActive, SystemRole: UserSystemRoleMember}
	admin := User{ID: "admin", Status: UserStatusActive, SystemRole: UserSystemRoleAdmin}

	tests := []struct {
		name           string
		actor          User
		target         string
		sharesWorkpace bool
		wantRead       bool
		wantUpdate     bool
	}{
		{"本人", actor, "actor", false, true, true},
		{"管理员", admin, "someone", false, true, true},
		{"同组同项目的普通成员", actor, "someone", true, true, false},
		{"无关用户", actor, "someone", false, false, false},
		{"目标为空", actor, "", false, false, false},
		{"未登录", User{}, "someone", true, false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanReadUserProfile(tc.actor, tc.target, tc.sharesWorkpace); got != tc.wantRead {
				t.Errorf("CanReadUserProfile = %v，期望 %v", got, tc.wantRead)
			}
			if got := CanUpdateUserProfile(tc.actor, tc.target); got != tc.wantUpdate {
				t.Errorf("CanUpdateUserProfile = %v，期望 %v", got, tc.wantUpdate)
			}
		})
	}
}

// TestRequiresUserAdministration 确保"普通用户改自己的状态/角色"被识别为需要管理员权限。
func TestRequiresUserAdministration(t *testing.T) {
	t.Parallel()

	if RequiresUserAdministration(UserProfileUpdate{DisplayName: strPtr("Alice")}) {
		t.Error("仅修改显示名不应要求管理员权限")
	}
	if !RequiresUserAdministration(UserProfileUpdate{Status: strPtr(UserStatusDisabled)}) {
		t.Error("修改状态必须要求管理员权限")
	}
	if !RequiresUserAdministration(UserProfileUpdate{SystemRole: strPtr(UserSystemRoleAdmin)}) {
		t.Error("修改系统角色必须要求管理员权限")
	}
}

// ---------------------------------------------------------------------------
// 密码规则（注册与管理员重置密码共用）
// ---------------------------------------------------------------------------

func TestNormalizeNewPasswordAcceptsValidInput(t *testing.T) {
	t.Parallel()

	// 前后空格是合法密码字符：不做 Trim。
	const password = "  correct horse battery  "
	got, fields := NormalizeNewPassword(password, 8)
	if len(fields) != 0 {
		t.Fatalf("期望无字段错误，实际为 %v", fields)
	}
	if got != password {
		t.Fatalf("密码被改写为 %q，期望原样保留（不得 Trim）", got)
	}
}

func TestNormalizeNewPasswordRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		password string
		min      int
	}{
		{"空密码", "", 8},
		{"短于最小长度", "short", 8},
		{"超过 256 个码点", strings.Repeat("密", 257), 8},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, fields := NormalizeNewPassword(tc.password, tc.min)
			// 字段名必须是契约中的 new_password：返回 password 会让前端拿到的
			// fields 键与请求体对不上。
			if _, ok := fields["new_password"]; !ok {
				t.Fatalf("期望字段 new_password 报错，实际为 %v", fields)
			}
		})
	}
}

// TestPasswordRuleIsSharedBetweenRegistrationAndReset 锁住"密码强度只有一套标准"。
//
// 注册与管理员重置密码若各自实现校验，迟早会分叉：管理员可能设出一个
// 用户自助注册时不被接受的密码（或反之），而密码强度属于安全边界。
func TestPasswordRuleIsSharedBetweenRegistrationAndReset(t *testing.T) {
	t.Parallel()

	candidates := []string{
		"",
		"short",
		"12345678",
		"correct horse battery",
		strings.Repeat("密", 257),
		"  spaced  ",
	}

	for _, password := range candidates {
		_, registrationFields := NormalizeRegistration(RegistrationInput{
			Username: "alice", Email: "alice@example.com",
			DisplayName: "Alice", Password: password,
		}, 8)
		_, resetFields := NormalizeNewPassword(password, 8)

		rejectedByRegistration := registrationFields["password"] != ""
		rejectedByReset := resetFields["new_password"] != ""
		if rejectedByRegistration != rejectedByReset {
			t.Errorf("密码 %q 的判定不一致：注册拒绝=%v，重置拒绝=%v",
				password, rejectedByRegistration, rejectedByReset)
		}
	}
}
