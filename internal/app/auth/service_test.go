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
// 历史缺陷对照：早期实现在关闭 Redis 时 session_store 硬依赖导致全站 500。
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

// ---------------------------------------------------------------------------
// 引导管理员（系统初始化时录入一个系统级后台账号）
// ---------------------------------------------------------------------------

func bootstrapInput() BootstrapAdminInput {
	return BootstrapAdminInput{
		Username: "admin",
		Email:    "admin@example.com",
		Password: "bootstrap-password",
	}
}

// TestEnsureAdministratorCreatesAdminAccount 覆盖"账号不存在时创建"。
func TestEnsureAdministratorCreatesAdminAccount(t *testing.T) {
	t.Parallel()

	users := NewFakeUserRepository(func() time.Time { return fixedNow })
	service, _ := newTestService(t, users, NewFakeSessionStore())

	result, err := service.EnsureAdministrator(context.Background(), bootstrapInput())
	if err != nil {
		t.Fatalf("EnsureAdministrator 返回错误: %v", err)
	}
	if !result.Created || result.Promoted {
		t.Fatalf("首次初始化应报告 Created=true / Promoted=false，实际为 %+v", result)
	}

	record, ok := users.Stored(result.UserID)
	if !ok {
		t.Fatal("账号未被创建")
	}
	if record.SystemRole != domain.UserSystemRoleAdmin {
		t.Errorf("system_role = %q，期望 admin", record.SystemRole)
	}
	if record.Status != domain.UserStatusActive {
		t.Errorf("status = %q，期望 active（否则管理员无法登录）", record.Status)
	}
	// 显示名未提供时回退到用户名。
	if record.DisplayName != "admin" {
		t.Errorf("display_name = %q，期望回退为用户名", record.DisplayName)
	}
	// 摘要必须是真实的 Argon2 摘要，且能用给定密码校验通过。
	if !strings.HasPrefix(record.PasswordHash, "$argon2id$") {
		t.Fatalf("密码摘要格式异常: %q", record.PasswordHash)
	}
	matched, err := testHasher().Verify("bootstrap-password", record.PasswordHash)
	if err != nil || !matched {
		t.Fatalf("摘要无法用初始化密码校验: matched=%v err=%v", matched, err)
	}
}

// TestEnsureAdministratorPromotesExistingAccountWithoutTouchingPassword 是本组最关键的一条。
//
// 初始化会在**每次启动**执行，因此"已存在时绝不覆盖密码"必须是硬约束：
// 一旦覆盖，管理员改过的密码会在下次重启时被打回配置里的初始值，
// 而配置里的初始化密码通常早已从环境中移除，结果是所有人都进不去。
func TestEnsureAdministratorPromotesExistingAccountWithoutTouchingPassword(t *testing.T) {
	t.Parallel()

	users := NewFakeUserRepository(func() time.Time { return fixedNow })
	service, _ := newTestService(t, users, NewFakeSessionStore())

	// 先用普通注册路径造一个普通成员，并把它当成"管理员已经改过密码"的账号。
	registered, err := service.Register(context.Background(), validRegistration())
	if err != nil {
		t.Fatalf("Register 返回错误: %v", err)
	}
	originalHash := registered.User.PasswordHash

	result, err := service.EnsureAdministrator(context.Background(), BootstrapAdminInput{
		Username: "ALICE", // 大小写不同也必须定位到同一个账号
		Email:    "ignored@example.com",
		Password: "a-different-bootstrap-password",
	})
	if err != nil {
		t.Fatalf("EnsureAdministrator 返回错误: %v", err)
	}
	if result.Created {
		t.Fatal("账号已存在时不应再次创建")
	}
	if !result.Promoted {
		t.Fatal("账号存在但不是管理员时应报告已提升")
	}
	if result.UserID != registered.User.ID {
		t.Fatalf("提升的账号 = %q，期望 %q", result.UserID, registered.User.ID)
	}

	record, _ := users.Stored(result.UserID)
	if record.SystemRole != domain.UserSystemRoleAdmin {
		t.Errorf("system_role = %q，期望 admin", record.SystemRole)
	}
	if record.PasswordHash != originalHash {
		t.Fatal("初始化**覆盖了既有密码摘要**：这会让每次重启都重置管理员密码")
	}
	// 邮箱同样不得被配置里的值覆盖。
	if record.Email != registered.User.Email {
		t.Errorf("email = %q，期望保持 %q", record.Email, registered.User.Email)
	}
}

// TestEnsureAdministratorIsIdempotent 覆盖"已经是管理员时什么都不做"。
//
// 不只是"不报错"：连 updated_at 都不应推进，否则每次重启都会让其他客户端
// 手里的版本无谓失效。
func TestEnsureAdministratorIsIdempotent(t *testing.T) {
	t.Parallel()

	users := NewFakeUserRepository(func() time.Time { return fixedNow })
	service, _ := newTestService(t, users, NewFakeSessionStore())

	first, err := service.EnsureAdministrator(context.Background(), bootstrapInput())
	if err != nil {
		t.Fatalf("首次初始化失败: %v", err)
	}
	before, _ := users.Stored(first.UserID)

	second, err := service.EnsureAdministrator(context.Background(), bootstrapInput())
	if err != nil {
		t.Fatalf("重复初始化失败: %v", err)
	}
	if second.Created || second.Promoted {
		t.Fatalf("重复初始化不应产生变更，实际为 %+v", second)
	}
	if second.UserID != first.UserID {
		t.Fatalf("重复初始化指向了不同账号：%q vs %q", second.UserID, first.UserID)
	}

	after, _ := users.Stored(first.UserID)
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("重复初始化推进了 updated_at（%v → %v）", before.UpdatedAt, after.UpdatedAt)
	}
}

// TestEnsureAdministratorReportsDisabledAccount 覆盖"账号被停用"的处置。
//
// 只报告不自动启用：自动启用会与一次显式的人工停用相抵触。
func TestEnsureAdministratorReportsDisabledAccount(t *testing.T) {
	t.Parallel()

	users := NewFakeUserRepository(func() time.Time { return fixedNow })
	service, _ := newTestService(t, users, NewFakeSessionStore())

	created, err := service.EnsureAdministrator(context.Background(), bootstrapInput())
	if err != nil {
		t.Fatalf("首次初始化失败: %v", err)
	}
	record, _ := users.Stored(created.UserID)
	record.Status = domain.UserStatusDisabled
	record.SystemRole = domain.UserSystemRoleMember
	users.Put(record)

	result, err := service.EnsureAdministrator(context.Background(), bootstrapInput())
	if err != nil {
		t.Fatalf("重新初始化失败: %v", err)
	}
	if !result.Disabled {
		t.Error("停用账号必须被报告出来，否则运维不知道管理员实际上无法登录")
	}
	if !result.Promoted {
		t.Error("角色不是 admin 时应被提升，即使账号处于停用状态")
	}
	after, _ := users.Stored(created.UserID)
	if after.Status != domain.UserStatusDisabled {
		t.Error("初始化不应擅自把账号改为启用：那会与显式的人工停用相抵触")
	}
}

// TestEnsureAdministratorRejectsInvalidInput 覆盖配置写错时的快速失败。
//
// 错误里只能有字段名与规则说明，绝不能出现密码内容。
func TestEnsureAdministratorRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input BootstrapAdminInput
		field string
	}{
		{
			name:  "用户名过短",
			input: BootstrapAdminInput{Username: "ab", Email: "a@example.com", Password: "bootstrap-password"},
			field: "username",
		},
		{
			name:  "邮箱格式非法",
			input: BootstrapAdminInput{Username: "admin", Email: "not-an-email", Password: "bootstrap-password"},
			field: "email",
		},
		{
			name:  "密码过短",
			input: BootstrapAdminInput{Username: "admin", Email: "a@example.com", Password: "short"},
			field: "password",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			users := NewFakeUserRepository(func() time.Time { return fixedNow })
			service, _ := newTestService(t, users, NewFakeSessionStore())

			_, err := service.EnsureAdministrator(context.Background(), tc.input)
			domainErr := requireDomainError(t, err, domain.CodeValidationError)
			if _, ok := domainErr.Fields[tc.field]; !ok {
				t.Fatalf("期望字段 %q 报错，实际为 %v", tc.field, domainErr.Fields)
			}
			if strings.Contains(err.Error(), tc.input.Password) {
				t.Fatalf("错误信息泄漏了密码内容: %v", err)
			}
			if users.Count() != 0 {
				t.Fatal("校验失败时不应创建任何账号")
			}
		})
	}
}
