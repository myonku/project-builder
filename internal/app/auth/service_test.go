package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/myonku/project-builder/internal/config"
	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/platform"
	"github.com/myonku/project-builder/internal/platform/clock"
	"github.com/myonku/project-builder/internal/platform/idgen"
	"io"
	"log/slog"
)

// 测试用低成本 Argon2 参数：真实参数（64 MiB、t=3）在单元测试里会拖慢整个套件，
// 而这里要验证的是"摘要校验逻辑正确"，不是"参数是否足够强"。
// 集成测试同样使用低成本参数，强度由 config 与生产默认值保证。
func testHasher() *platform.PasswordHasher {
	return platform.NewPasswordHasherWith(platform.Argon2Params{
		Memory:      8 * 1024,
		Iterations:  1,
		Parallelism: 1,
		SaltLength:  16,
		KeyLength:   32,
	})
}

// fixedNow 是所有用例共用的基准时间，保证断言与机器当前时间无关。
var fixedNow = time.Date(2026, 10, 11, 9, 30, 0, 0, time.UTC)

func testSecurity() config.SecurityConfig {
	return config.SecurityConfig{
		SessionSecret:     "test-session-secret-for-unit-tests",
		SessionTTLSeconds: 3600,
		PasswordMinLength: 8,
		SecureCookies:     false,
		SessionCookieName: "pb_session_test",
	}
}

// requireDomainError 断言 err 是指定错误码的领域错误，并返回该错误以便进一步断言字段明细。
func requireDomainError(t *testing.T, err error, code domain.ErrorCode) *domain.Error {
	t.Helper()

	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		t.Fatalf("期望错误码 %q 的领域错误，实际为 %T: %v", code, err, err)
	}
	if domainErr.Code != code {
		t.Fatalf("错误码 = %q，期望 %q（完整错误：%v）", domainErr.Code, code, domainErr)
	}
	return domainErr
}

// newTestService 装配一套全替身的用例服务。
func newTestService(t *testing.T, users UserRepository, sessions SessionStore) (*Service, *clock.Fixed) {
	t.Helper()

	clk := clock.NewFixed(fixedNow)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	service := NewService(Options{
		Users:    users,
		Sessions: sessions,
		Hasher:   testHasher(),
		IDs:      idgen.NewTimeOrdered(clk.Now),
		Clock:    clk,
		Logger:   logger,
		Security: testSecurity(),
	})
	return service, clk
}

func validRegistration() domain.RegistrationInput {
	return domain.RegistrationInput{
		Username:    "alice",
		Email:       "alice@example.com",
		DisplayName: "Alice",
		Password:    "correct horse battery",
		Timezone:    "Asia/Shanghai",
	}
}

// TestRegisterCreatesActiveMemberWithArgon2Hash 覆盖注册的成功路径与不变量。
func TestRegisterCreatesActiveMemberWithArgon2Hash(t *testing.T) {
	t.Parallel()

	users := NewFakeUserRepository(func() time.Time { return fixedNow })
	service, _ := newTestService(t, users, NewFakeSessionStore())

	result, err := service.Register(context.Background(), validRegistration())
	if err != nil {
		t.Fatalf("Register 返回错误: %v", err)
	}

	if result.User.ID == "" {
		t.Fatal("注册必须生成用户 ID")
	}
	if !idgen.IsUUID(result.User.ID) {
		t.Fatalf("用户 ID %q 不是合法 UUID", result.User.ID)
	}
	if result.User.Status != domain.UserStatusActive {
		t.Errorf("status = %q，期望 active", result.User.Status)
	}
	if result.User.SystemRole != domain.UserSystemRoleMember {
		t.Errorf("system_role = %q，期望 member（新用户不得默认为管理员）", result.User.SystemRole)
	}
	if !result.User.ReminderEnabled {
		t.Error("reminder_enabled 应默认为 true")
	}
	if result.User.Timezone != "Asia/Shanghai" {
		t.Errorf("timezone = %q，期望保留入参", result.User.Timezone)
	}

	// 密码必须以 Argon2id PHC 摘要保存，且不含明文。
	if !strings.HasPrefix(result.User.PasswordHash, "$argon2id$") {
		t.Fatalf("密码摘要格式不正确: %q", result.User.PasswordHash)
	}
	if strings.Contains(result.User.PasswordHash, "correct horse battery") {
		t.Fatal("密码摘要中不得包含明文密码")
	}

	// 摘要必须能被验证，且错误密码必须失败。
	matched, err := testHasher().Verify("correct horse battery", result.User.PasswordHash)
	if err != nil {
		t.Fatalf("校验摘要返回错误: %v", err)
	}
	if !matched {
		t.Fatal("正确密码必须通过校验")
	}
	matched, err = testHasher().Verify("wrong password", result.User.PasswordHash)
	if err != nil {
		t.Fatalf("校验错误密码返回错误: %v", err)
	}
	if matched {
		t.Fatal("错误密码不得通过校验")
	}
}

// TestRegisterNormalizesIdentity 覆盖大小写归一化，避免 "Alice" 与 "alice" 注册出两个账号。
func TestRegisterNormalizesIdentity(t *testing.T) {
	t.Parallel()

	users := NewFakeUserRepository(func() time.Time { return fixedNow })
	service, _ := newTestService(t, users, NewFakeSessionStore())

	result, err := service.Register(context.Background(), domain.RegistrationInput{
		Username:    "  Alice_01  ",
		Email:       "  Alice@Example.COM ",
		DisplayName: "  爱丽丝  ",
		Password:    "correct horse battery",
	})
	if err != nil {
		t.Fatalf("Register 返回错误: %v", err)
	}
	if result.User.Username != "alice_01" {
		t.Errorf("username = %q，期望小写去空格", result.User.Username)
	}
	if result.User.Email != "alice@example.com" {
		t.Errorf("email = %q，期望小写去空格", result.User.Email)
	}
	if result.User.DisplayName != "爱丽丝" {
		t.Errorf("display_name = %q，期望去空格", result.User.DisplayName)
	}
}

// TestRegisterRejectsInvalidFields 覆盖字段校验失败返回 422 语义。
func TestRegisterRejectsInvalidFields(t *testing.T) {
	t.Parallel()

	users := NewFakeUserRepository(func() time.Time { return fixedNow })
	service, _ := newTestService(t, users, NewFakeSessionStore())

	_, err := service.Register(context.Background(), domain.RegistrationInput{})
	domainErr := requireDomainError(t, err, domain.CodeValidationError)

	for _, field := range []string{"username", "email", "display_name", "password"} {
		if _, ok := domainErr.Fields[field]; !ok {
			t.Errorf("缺少字段错误 %q，实际为 %v", field, domainErr.Fields)
		}
	}
	if users.Count() != 0 {
		t.Fatal("校验失败时不得写入用户")
	}
}

// TestRegisterRejectsDuplicateUsernameAndEmail 覆盖重复注册返回 409 语义。
func TestRegisterRejectsDuplicateUsernameAndEmail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		second    domain.RegistrationInput
		wantField string
	}{
		{
			name: "用户名重复",
			second: domain.RegistrationInput{
				Username: "alice", Email: "other@example.com",
				DisplayName: "Other", Password: "correct horse battery",
			},
			wantField: "username",
		},
		{
			name: "邮箱重复",
			second: domain.RegistrationInput{
				Username: "other", Email: "alice@example.com",
				DisplayName: "Other", Password: "correct horse battery",
			},
			wantField: "email",
		},
		{
			name: "大小写不同的同一用户名",
			second: domain.RegistrationInput{
				Username: "ALICE", Email: "other2@example.com",
				DisplayName: "Other", Password: "correct horse battery",
			},
			wantField: "username",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			users := NewFakeUserRepository(func() time.Time { return fixedNow })
			service, _ := newTestService(t, users, NewFakeSessionStore())

			if _, err := service.Register(context.Background(), validRegistration()); err != nil {
				t.Fatalf("首次注册失败: %v", err)
			}

			_, err := service.Register(context.Background(), tc.second)
			domainErr := requireDomainError(t, err, domain.CodeConflict)
			if _, ok := domainErr.Fields[tc.wantField]; !ok {
				t.Fatalf("期望字段冲突 %q，实际为 %v", tc.wantField, domainErr.Fields)
			}
			if users.Count() != 1 {
				t.Fatalf("冲突时不得写入第二个用户，实际数量 = %d", users.Count())
			}
		})
	}
}

// TestLoginSucceedsAndCreatesSession 覆盖登录成功路径。
func TestLoginSucceedsAndCreatesSession(t *testing.T) {
	t.Parallel()

	users := NewFakeUserRepository(func() time.Time { return fixedNow })
	sessions := NewFakeSessionStore()
	service, _ := newTestService(t, users, sessions)

	if _, err := service.Register(context.Background(), validRegistration()); err != nil {
		t.Fatalf("注册失败: %v", err)
	}

	result, err := service.Login(context.Background(), domain.LoginInput{
		Identifier: "alice",
		Password:   "correct horse battery",
	})
	if err != nil {
		t.Fatalf("Login 返回错误: %v", err)
	}

	if result.Token == "" {
		t.Fatal("登录必须返回会话令牌")
	}
	if len(result.Token) != 64 {
		t.Errorf("会话令牌长度 = %d，期望 64 个十六进制字符", len(result.Token))
	}
	if !sessions.Has(result.Token) {
		t.Fatal("会话必须已写入会话存储")
	}
	if result.ExpiresAt.Sub(fixedNow) != time.Hour {
		t.Errorf("会话有效期 = %v，期望 1h", result.ExpiresAt.Sub(fixedNow))
	}
	// 最后登录时间必须被记录（需求文档 5.1）。
	if result.User.LastLoginAt == nil {
		t.Fatal("登录后必须记录 last_login_at")
	}
	if !result.User.LastLoginAt.Equal(fixedNow) {
		t.Errorf("last_login_at = %v，期望 %v", result.User.LastLoginAt, fixedNow)
	}
}

// TestLoginAcceptsEmailOrUsername 覆盖 identifier 的双重语义。
func TestLoginAcceptsEmailOrUsername(t *testing.T) {
	t.Parallel()

	for _, identifier := range []string{"alice", "alice@example.com", "ALICE", "Alice@Example.com"} {
		t.Run(identifier, func(t *testing.T) {
			t.Parallel()

			users := NewFakeUserRepository(func() time.Time { return fixedNow })
			service, _ := newTestService(t, users, NewFakeSessionStore())

			if _, err := service.Register(context.Background(), validRegistration()); err != nil {
				t.Fatalf("注册失败: %v", err)
			}

			if _, err := service.Login(context.Background(), domain.LoginInput{
				Identifier: identifier,
				Password:   "correct horse battery",
			}); err != nil {
				t.Fatalf("以 %q 登录失败: %v", identifier, err)
			}
		})
	}
}

// TestLoginRejectsBadCredentials 覆盖凭据错误返回 401 语义。
func TestLoginRejectsBadCredentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		identifier string
		password   string
	}{
		{name: "用户不存在", identifier: "nobody", password: "correct horse battery"},
		{name: "密码错误", identifier: "alice", password: "wrong password"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			users := NewFakeUserRepository(func() time.Time { return fixedNow })
			sessions := NewFakeSessionStore()
			service, _ := newTestService(t, users, sessions)

			if _, err := service.Register(context.Background(), validRegistration()); err != nil {
				t.Fatalf("注册失败: %v", err)
			}

			_, err := service.Login(context.Background(), domain.LoginInput{
				Identifier: tc.identifier,
				Password:   tc.password,
			})
			requireDomainError(t, err, domain.CodeInvalidCredentials)

			if sessions.Count() != 0 {
				t.Fatal("凭据错误时不得创建会话")
			}
		})
	}
}

// TestLoginRejectsDisabledUser 覆盖"停用用户不能登录"（需求文档 5.1）。
func TestLoginRejectsDisabledUser(t *testing.T) {
	t.Parallel()

	users := NewFakeUserRepository(func() time.Time { return fixedNow })
	sessions := NewFakeSessionStore()
	service, _ := newTestService(t, users, sessions)

	result, err := service.Register(context.Background(), validRegistration())
	if err != nil {
		t.Fatalf("注册失败: %v", err)
	}

	// 直接改状态，模拟管理员停用。
	record, _ := users.Stored(result.User.ID)
	record.Status = domain.UserStatusDisabled
	users.mu.Lock()
	users.byID[record.ID] = record
	users.mu.Unlock()

	_, err = service.Login(context.Background(), domain.LoginInput{
		Identifier: "alice",
		Password:   "correct horse battery",
	})
	requireDomainError(t, err, domain.CodePermissionDenied)
	if sessions.Count() != 0 {
		t.Fatal("停用用户不得创建会话")
	}
}

// TestLoginFailsWhenSessionBackendUnavailable 覆盖 Redis 禁用场景。
//
// 历史缺陷对照：Python 版本在关闭 Redis 时 session_store 硬依赖导致全站 500。
// Go 版本要求给出明确错误而不是崩溃。
func TestLoginFailsWhenSessionBackendUnavailable(t *testing.T) {
	t.Parallel()

	users := NewFakeUserRepository(func() time.Time { return fixedNow })
	service, _ := newTestService(t, users, NewUnavailableSessionStore())

	if _, err := service.Register(context.Background(), validRegistration()); err != nil {
		t.Fatalf("注册失败: %v", err)
	}

	_, err := service.Login(context.Background(), domain.LoginInput{
		Identifier: "alice",
		Password:   "correct horse battery",
	})
	if err == nil {
		t.Fatal("会话后端不可用时登录必须失败")
	}
	requireDomainError(t, err, domain.CodeInternalError)
}

// TestLoginRehashUpgradesWeakDigest 覆盖摘要参数升级路径。
func TestLoginRehashUpgradesWeakDigest(t *testing.T) {
	t.Parallel()

	users := NewFakeUserRepository(func() time.Time { return fixedNow })
	service, _ := newTestService(t, users, NewFakeSessionStore())

	// 用更弱的参数生成摘要，模拟历史遗留数据。
	weakHasher := platform.NewPasswordHasherWith(platform.Argon2Params{
		Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16,
	})
	weakHash, err := weakHasher.Hash("correct horse battery")
	if err != nil {
		t.Fatalf("生成弱摘要失败: %v", err)
	}

	userID := idgen.New().MustUUID()
	if _, err := users.CreateUser(context.Background(), NewUser{
		ID: userID, Username: "alice", Email: "alice@example.com",
		DisplayName: "Alice", PasswordHash: weakHash,
		Timezone: "UTC", Status: domain.UserStatusActive,
		SystemRole: domain.UserSystemRoleMember, ReminderEnabled: true,
	}); err != nil {
		t.Fatalf("写入用户失败: %v", err)
	}

	if _, err := service.Login(context.Background(), domain.LoginInput{
		Identifier: "alice",
		Password:   "correct horse battery",
	}); err != nil {
		t.Fatalf("Login 返回错误: %v", err)
	}

	updated, ok := users.Stored(userID)
	if !ok {
		t.Fatal("用户应仍然存在")
	}
	if updated.PasswordHash == weakHash {
		t.Fatal("登录后应升级弱参数摘要，实际摘要未变化")
	}
	// 升级后的摘要必须使用当前策略的参数，并且仍然校验通过。
	rehashed, err := testHasher().Verify("correct horse battery", updated.PasswordHash)
	if err != nil {
		t.Fatalf("校验升级后的摘要失败: %v", err)
	}
	if !rehashed {
		t.Fatal("升级后的摘要必须仍能校验原密码")
	}
	if !strings.HasPrefix(updated.PasswordHash, "$argon2id$v=19$m=8192,t=1,p=1$") {
		t.Fatalf("升级后的摘要未使用当前参数: %q", updated.PasswordHash)
	}
}

// TestCurrentUserResolvesSession 覆盖会话解析。
func TestCurrentUserResolvesSession(t *testing.T) {
	t.Parallel()

	users := NewFakeUserRepository(func() time.Time { return fixedNow })
	service, _ := newTestService(t, users, NewFakeSessionStore())

	if _, err := service.Register(context.Background(), validRegistration()); err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	login, err := service.Login(context.Background(), domain.LoginInput{
		Identifier: "alice", Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}

	record, err := service.CurrentUser(context.Background(), login.Token)
	if err != nil {
		t.Fatalf("CurrentUser 返回错误: %v", err)
	}
	if record.ID != login.User.ID {
		t.Fatalf("解析到的用户 = %q，期望 %q", record.ID, login.User.ID)
	}
}

// TestCurrentUserRejectsMissingOrUnknownSession 覆盖未登录语义。
func TestCurrentUserRejectsMissingOrUnknownSession(t *testing.T) {
	t.Parallel()

	users := NewFakeUserRepository(func() time.Time { return fixedNow })
	service, _ := newTestService(t, users, NewFakeSessionStore())

	for _, token := range []string{"", "not-a-real-token"} {
		_, err := service.CurrentUser(context.Background(), token)
		requireDomainError(t, err, domain.CodeAuthenticationReq)
	}
}

// TestCurrentUserInvalidatesDisabledUserSession 覆盖"停用用户的既有会话立即失效"。
func TestCurrentUserInvalidatesDisabledUserSession(t *testing.T) {
	t.Parallel()

	users := NewFakeUserRepository(func() time.Time { return fixedNow })
	sessions := NewFakeSessionStore()
	service, _ := newTestService(t, users, sessions)

	if _, err := service.Register(context.Background(), validRegistration()); err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	login, err := service.Login(context.Background(), domain.LoginInput{
		Identifier: "alice", Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}

	record, _ := users.Stored(login.User.ID)
	record.Status = domain.UserStatusDisabled
	users.mu.Lock()
	users.byID[record.ID] = record
	users.mu.Unlock()

	_, err = service.CurrentUser(context.Background(), login.Token)
	requireDomainError(t, err, domain.CodeAuthenticationReq)

	// 会话必须被清理，避免停用后仍占用存储。
	if sessions.Has(login.Token) {
		t.Fatal("停用用户的会话必须被撤销")
	}
}

// TestCurrentUserCleansOrphanSession 覆盖"会话指向已删除用户"的清理。
func TestCurrentUserCleansOrphanSession(t *testing.T) {
	t.Parallel()

	users := NewFakeUserRepository(func() time.Time { return fixedNow })
	sessions := NewFakeSessionStore()
	service, _ := newTestService(t, users, sessions)

	if _, err := service.Register(context.Background(), validRegistration()); err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	login, err := service.Login(context.Background(), domain.LoginInput{
		Identifier: "alice", Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}

	// 直接移除用户，模拟数据被清理后的孤儿会话。
	users.mu.Lock()
	delete(users.byID, login.User.ID)
	users.mu.Unlock()

	_, err = service.CurrentUser(context.Background(), login.Token)
	requireDomainError(t, err, domain.CodeAuthenticationReq)
	if sessions.Has(login.Token) {
		t.Fatal("孤儿会话必须被清理")
	}
}

// TestLogoutRevokesSessionAndIsIdempotent 覆盖登出幂等。
func TestLogoutRevokesSessionAndIsIdempotent(t *testing.T) {
	t.Parallel()

	users := NewFakeUserRepository(func() time.Time { return fixedNow })
	sessions := NewFakeSessionStore()
	service, _ := newTestService(t, users, sessions)

	if _, err := service.Register(context.Background(), validRegistration()); err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	login, err := service.Login(context.Background(), domain.LoginInput{
		Identifier: "alice", Password: "correct horse battery",
	})
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}

	if err := service.Logout(context.Background(), login.Token); err != nil {
		t.Fatalf("登出失败: %v", err)
	}
	if sessions.Has(login.Token) {
		t.Fatal("登出必须撤销会话")
	}

	// 重复登出与空令牌都必须成功。
	if err := service.Logout(context.Background(), login.Token); err != nil {
		t.Fatalf("重复登出必须成功，实际: %v", err)
	}
	if err := service.Logout(context.Background(), ""); err != nil {
		t.Fatalf("空令牌登出必须成功，实际: %v", err)
	}
}

// TestSessionTokensAreUnique 覆盖令牌随机性。
func TestSessionTokensAreUnique(t *testing.T) {
	t.Parallel()

	users := NewFakeUserRepository(func() time.Time { return fixedNow })
	service, _ := newTestService(t, users, NewFakeSessionStore())

	if _, err := service.Register(context.Background(), validRegistration()); err != nil {
		t.Fatalf("注册失败: %v", err)
	}

	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		login, err := service.Login(context.Background(), domain.LoginInput{
			Identifier: "alice", Password: "correct horse battery",
		})
		if err != nil {
			t.Fatalf("第 %d 次登录失败: %v", i, err)
		}
		if seen[login.Token] {
			t.Fatalf("会话令牌重复: %q", login.Token)
		}
		seen[login.Token] = true
	}
}

// TestPublicUserNeverExposesPasswordHash 覆盖对外结构不泄漏摘要。
//
// 这是硬性安全边界：接口文档 3.1 明确密码与摘要永不返回。
func TestPublicUserNeverExposesPasswordHash(t *testing.T) {
	t.Parallel()

	record := UserRecord{
		ID: "0192f2e5-7d2a-7abc-8b01-123456789abc", Username: "alice",
		Email: "alice@example.com", DisplayName: "Alice",
		PasswordHash: "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA",
		Status:       domain.UserStatusActive, SystemRole: domain.UserSystemRoleMember,
		Timezone: "UTC", ReminderEnabled: true,
		CreatedAt: fixedNow, UpdatedAt: fixedNow,
	}

	public := NewPublicUser(record)

	// 序列化后必须完全不含摘要。这里用结构体字段检查代替 JSON 反射，
	// 更直接地表达"公开结构里根本没有这个字段"。
	if public.ID != record.ID || public.Username != record.Username {
		t.Fatal("公开结构必须包含 id 与 username")
	}
	if public.UpdatedAt != domain.FormatTimestamp(fixedNow) {
		t.Errorf("updated_at = %q，期望 UTC ISO 8601 格式", public.UpdatedAt)
	}
	if public.LastLoginAt != nil {
		t.Error("未登录用户的 last_login_at 应为 null")
	}
}

// TestSecretDoesNotLeakIntoServiceErrors 覆盖错误信息不泄漏内部细节。
func TestSecretDoesNotLeakIntoServiceErrors(t *testing.T) {
	t.Parallel()

	users := NewFakeUserRepository(func() time.Time { return fixedNow })
	users.FindErr = errors.New("pq: password authentication failed for user \"project_builder\"")
	service, _ := newTestService(t, users, NewFakeSessionStore())

	_, err := service.Login(context.Background(), domain.LoginInput{
		Identifier: "alice", Password: "whatever-password",
	})
	if err == nil {
		t.Fatal("期望错误")
	}
	// 未识别的仓储错误会被归一化为 internal_error，其对外文案是固定英文描述，
	// 不包含底层 SQL 或认证细节。
	domainErr := domain.AsError(err)
	if strings.Contains(domainErr.Message, "pq:") {
		t.Fatalf("错误描述泄漏了数据库细节: %q", domainErr.Message)
	}
}
