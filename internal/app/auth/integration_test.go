package auth_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/platform"
	"github.com/myonku/project-builder/internal/testsupport"
)

// 本文件是 Auth 域的集成测试：请求经由真实的 gin 路由、真实的 PostgreSQL 仓储
// 与真实的 Redis 会话存储，不使用任何替身。
//
// 这正是不足之处的对症补强：早期实现的 45 个测试全部使用内存 fake，
// 因此"所有更新端点 500"这类缺陷从未被触碰（见 docs/01-Go服务端架构设计.md 5、8）。

func init() {
	gin.SetMode(gin.TestMode)
}

// request 执行一次 HTTP 请求。
func request(router *gin.Engine, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			panic(err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}

	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

// decode 解析响应体。
func decode(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("响应不是合法 JSON: %v（原文：%s）", err, recorder.Body.String())
	}
	return payload
}

// dataOf 取出 data 字段。
func dataOf(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	payload := decode(t, recorder)
	raw, ok := payload["data"]
	if !ok {
		t.Fatalf("响应缺少 data 字段: %s", recorder.Body.String())
	}
	data, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("data 不是对象: %s", recorder.Body.String())
	}
	return data
}

// errorOf 取出 error 字段。
func errorOf(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	payload := decode(t, recorder)
	raw, ok := payload["error"]
	if !ok {
		t.Fatalf("响应缺少 error 字段: %s", recorder.Body.String())
	}
	errorBody, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("error 不是对象: %s", recorder.Body.String())
	}
	return errorBody
}

// requireStatus 断言状态码。
func requireStatus(t *testing.T, recorder *httptest.ResponseRecorder, want int) {
	t.Helper()

	if recorder.Code != want {
		t.Fatalf("状态码 = %d，期望 %d（响应体：%s）", recorder.Code, want, recorder.Body.String())
	}
}

// requireErrorCode 断言错误码。
func requireErrorCode(t *testing.T, recorder *httptest.ResponseRecorder, want domain.ErrorCode) map[string]any {
	t.Helper()

	errorBody := errorOf(t, recorder)
	if got := errorBody["code"]; got != string(want) {
		t.Fatalf("error.code = %v，期望 %q（响应体：%s）", got, want, recorder.Body.String())
	}
	return errorBody
}

// sessionCookie 从响应中提取会话 Cookie。
func sessionCookie(t *testing.T, recorder *httptest.ResponseRecorder, name string) string {
	t.Helper()

	for _, cookie := range recorder.Result().Cookies() {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	t.Fatalf("响应未设置会话 Cookie %q（Cookie 头：%v）", name, recorder.Header().Values("Set-Cookie"))
	return ""
}

// registrationBody 返回合法的注册请求体。
func registrationBody(username, email string) map[string]any {
	return map[string]any{
		"username":     username,
		"email":        email,
		"display_name": "Alice",
		"password":     "correct horse battery",
		"timezone":     "Asia/Shanghai",
	}
}

// TestRegisterPersistsUserWithArgon2Digest 验证注册真实落库且摘要不可逆。
func TestRegisterPersistsUserWithArgon2Digest(t *testing.T) {
	harness := testsupport.NewHarness(t)
	ctx := context.Background()

	recorder := request(harness.Router, http.MethodPost, testsupport.URL("/auth/register"),
		registrationBody("alice", "alice@example.com"), nil)
	requireStatus(t, recorder, http.StatusCreated)

	data := dataOf(t, recorder)
	userID, _ := data["id"].(string)
	if userID == "" {
		t.Fatal("注册响应必须包含 id（契约 2.1）")
	}
	if data["updated_at"] == nil || data["updated_at"] == "" {
		t.Fatal("成功写响应必须包含 updated_at（契约 2.1）")
	}

	// 响应绝不能包含摘要或密码。
	body := recorder.Body.String()
	for _, leak := range []string{"password_hash", "correct horse battery", "$argon2id$"} {
		if strings.Contains(body, leak) {
			t.Fatalf("注册响应泄漏了敏感字段 %q: %s", leak, body)
		}
	}

	// 数据库中必须真实存在，且摘要为 Argon2id PHC 格式。
	record, err := harness.Users.FindByID(ctx, userID)
	if err != nil {
		t.Fatalf("按 ID 查询用户失败: %v", err)
	}
	if !strings.HasPrefix(record.PasswordHash, "$argon2id$v=19$") {
		t.Fatalf("密码摘要格式不正确: %q", record.PasswordHash)
	}
	if record.Status != domain.UserStatusActive {
		t.Errorf("status = %q，期望 active", record.Status)
	}
	if record.SystemRole != domain.UserSystemRoleMember {
		t.Errorf("system_role = %q，期望 member", record.SystemRole)
	}
	if record.Timezone != "Asia/Shanghai" {
		t.Errorf("timezone = %q，期望保留入参", record.Timezone)
	}

	// 摘要必须能校验原密码（真实存取后的往返验证）。
	matched, err := harness.Hasher.Verify("correct horse battery", record.PasswordHash)
	if err != nil {
		t.Fatalf("校验摘要失败: %v", err)
	}
	if !matched {
		t.Fatal("数据库中保存的摘要必须能校验原密码")
	}
}

// TestRegisterDoesNotCreateSession 验证注册不自动登录（契约 4.2 的明确要求）。
func TestRegisterDoesNotCreateSession(t *testing.T) {
	harness := testsupport.NewHarness(t)

	recorder := request(harness.Router, http.MethodPost, testsupport.URL("/auth/register"),
		registrationBody("alice", "alice@example.com"), nil)
	requireStatus(t, recorder, http.StatusCreated)

	if len(recorder.Result().Cookies()) != 0 {
		t.Fatalf("注册不应建立会话，实际设置了 Cookie: %v", recorder.Header().Values("Set-Cookie"))
	}
}

// TestRegisterRejectsDuplicateIdentity 验证重复注册返回 409 且字段明细正确。
func TestRegisterRejectsDuplicateIdentity(t *testing.T) {
	harness := testsupport.NewHarness(t)

	first := request(harness.Router, http.MethodPost, testsupport.URL("/auth/register"),
		registrationBody("alice", "alice@example.com"), nil)
	requireStatus(t, first, http.StatusCreated)

	tests := []struct {
		name      string
		body      map[string]any
		wantField string
	}{
		{
			name:      "用户名重复",
			body:      registrationBody("alice", "other@example.com"),
			wantField: "username",
		},
		{
			name:      "邮箱重复",
			body:      registrationBody("other", "alice@example.com"),
			wantField: "email",
		},
		{
			name:      "大小写不同的同一用户名",
			body:      registrationBody("ALICE", "other2@example.com"),
			wantField: "username",
		},
		{
			name:      "大小写不同的同一邮箱",
			body:      registrationBody("other3", "ALICE@EXAMPLE.COM"),
			wantField: "email",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := request(harness.Router, http.MethodPost, testsupport.URL("/auth/register"), tc.body, nil)
			requireStatus(t, recorder, http.StatusConflict)

			errorBody := requireErrorCode(t, recorder, domain.CodeConflict)
			fields, _ := errorBody["fields"].(map[string]any)
			if _, ok := fields[tc.wantField]; !ok {
				t.Fatalf("字段明细缺少 %q，实际为 %v", tc.wantField, fields)
			}
		})
	}

	// 数据库中只应有 1 个用户。
	var count int
	if err := harness.Pool.QueryRow(context.Background(), `SELECT count(*) FROM users`).Scan(&count); err != nil {
		t.Fatalf("统计用户失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("用户数量 = %d，期望 1（重复注册不得写入）", count)
	}
}

// TestConcurrentRegistrationOfSameIdentityIsRejectedByDatabase 验证并发注册的最终保证。
//
// 应用层的"提前检查 + 写入"之间存在竞争窗口，因此必须由数据库唯一索引兜底。
// 该用例同时证明了约束会把冲突翻译为 409 而不是 500。
func TestConcurrentRegistrationOfSameIdentityIsRejectedByDatabase(t *testing.T) {
	harness := testsupport.NewHarness(t)

	const workers = 6
	var waitGroup sync.WaitGroup
	statuses := make([]int, workers)

	waitGroup.Add(workers)
	for i := 0; i < workers; i++ {
		go func(index int) {
			defer waitGroup.Done()
			recorder := request(harness.Router, http.MethodPost, testsupport.URL("/auth/register"),
				registrationBody("alice", "alice@example.com"), nil)
			statuses[index] = recorder.Code
		}(i)
	}
	waitGroup.Wait()

	created, conflicts := 0, 0
	for _, status := range statuses {
		switch status {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		default:
			t.Errorf("并发注册返回了非预期状态码 %d（期望 201 或 409）", status)
		}
	}

	if created != 1 {
		t.Fatalf("成功注册数量 = %d，期望恰好 1", created)
	}
	if conflicts != workers-1 {
		t.Fatalf("冲突数量 = %d，期望 %d（不得出现 500）", conflicts, workers-1)
	}

	var count int
	if err := harness.Pool.QueryRow(context.Background(), `SELECT count(*) FROM users`).Scan(&count); err != nil {
		t.Fatalf("统计用户失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("用户数量 = %d，期望 1", count)
	}
}

// TestLoginCreatesRedisSessionAndMeResolvesIt 覆盖完整的登录 → 会话 → 当前用户链路。
func TestLoginCreatesRedisSessionAndMeResolvesIt(t *testing.T) {
	harness := testsupport.NewHarness(t)

	register := request(harness.Router, http.MethodPost, testsupport.URL("/auth/register"),
		registrationBody("alice", "alice@example.com"), nil)
	requireStatus(t, register, http.StatusCreated)
	registeredID, _ := dataOf(t, register)["id"].(string)

	login := request(harness.Router, http.MethodPost, testsupport.URL("/auth/login"),
		map[string]any{"identifier": "alice", "password": "correct horse battery"}, nil)
	requireStatus(t, login, http.StatusOK)

	token := sessionCookie(t, login, harness.Config.Security.SessionCookieName)
	if token == "" {
		t.Fatal("登录必须返回会话 Cookie")
	}

	// 会话必须真实写入 Redis，且 Cookie 本身不是用户 ID。
	keys, err := harness.Redis.Client.Keys(context.Background(), "pb:session:*").Result()
	if err != nil {
		t.Fatalf("扫描会话键失败: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("Redis 会话数量 = %d，期望 1", len(keys))
	}
	if strings.Contains(token, registeredID) {
		t.Fatal("会话令牌不得由用户 ID 派生")
	}
	ttl, err := harness.Redis.Client.TTL(context.Background(), keys[0]).Result()
	if err != nil {
		t.Fatalf("读取会话 TTL 失败: %v", err)
	}
	if ttl <= 0 {
		t.Fatalf("会话必须有过期时间，实际 TTL = %v", ttl)
	}

	// /auth/me 必须从数据库解析出同一个用户。
	me := request(harness.Router, http.MethodGet, testsupport.URL("/auth/me"), nil,
		map[string]string{"Cookie": harness.Config.Security.SessionCookieName + "=" + token})
	requireStatus(t, me, http.StatusOK)

	meData := dataOf(t, me)
	if got := meData["id"]; got != registeredID {
		t.Fatalf("/auth/me 返回的用户 id = %v，期望 %q", got, registeredID)
	}
	if _, ok := meData["password_hash"]; ok {
		t.Fatal("/auth/me 不得返回密码摘要")
	}
	// 登录后最后登录时间必须被记录（需求文档 5.1）。
	if lastLogin, ok := meData["last_login_at"].(string); !ok || lastLogin == "" {
		t.Fatalf("登录后 last_login_at 必须非空，实际为 %v", meData["last_login_at"])
	}
}

// TestLoginAcceptsIdentifierVariants 覆盖 identifier 为用户名或邮箱。
func TestLoginAcceptsIdentifierVariants(t *testing.T) {
	harness := testsupport.NewHarness(t)

	requireStatus(t, request(harness.Router, http.MethodPost, testsupport.URL("/auth/register"),
		registrationBody("alice", "alice@example.com"), nil), http.StatusCreated)

	for _, identifier := range []string{"alice", "ALICE", "alice@example.com", "ALICE@EXAMPLE.COM"} {
		t.Run(identifier, func(t *testing.T) {
			recorder := request(harness.Router, http.MethodPost, testsupport.URL("/auth/login"),
				map[string]any{"identifier": identifier, "password": "correct horse battery"}, nil)
			requireStatus(t, recorder, http.StatusOK)
		})
	}
}

// TestLoginRejectsBadCredentialsWithoutEnumeration 覆盖凭据错误信息不可用于账号枚举。
func TestLoginRejectsBadCredentialsWithoutEnumeration(t *testing.T) {
	harness := testsupport.NewHarness(t)

	requireStatus(t, request(harness.Router, http.MethodPost, testsupport.URL("/auth/register"),
		registrationBody("alice", "alice@example.com"), nil), http.StatusCreated)

	unknownUser := request(harness.Router, http.MethodPost, testsupport.URL("/auth/login"),
		map[string]any{"identifier": "nobody", "password": "correct horse battery"}, nil)
	requireStatus(t, unknownUser, http.StatusUnauthorized)

	wrongPassword := request(harness.Router, http.MethodPost, testsupport.URL("/auth/login"),
		map[string]any{"identifier": "alice", "password": "wrong password"}, nil)
	requireStatus(t, wrongPassword, http.StatusUnauthorized)

	// 两种情况的错误码与描述必须完全一致，否则可用于判断账号是否存在。
	firstError := requireErrorCode(t, unknownUser, domain.CodeInvalidCredentials)
	secondError := requireErrorCode(t, wrongPassword, domain.CodeInvalidCredentials)
	if firstError["message"] != secondError["message"] {
		t.Fatalf("账号不存在与密码错误的对外描述必须一致：%v vs %v",
			firstError["message"], secondError["message"])
	}
}

// TestDisabledUserCannotLoginAndLosesExistingSession 验证停用语义立即生效。
//
// 需求文档 5.1：停用用户不能登录。
// 会话有效性从数据库实时读取，因此停用后既有会话立即失效，无需额外的会话失效广播。
func TestDisabledUserCannotLoginAndLosesExistingSession(t *testing.T) {
	harness := testsupport.NewHarness(t)
	ctx := context.Background()

	register := request(harness.Router, http.MethodPost, testsupport.URL("/auth/register"),
		registrationBody("alice", "alice@example.com"), nil)
	requireStatus(t, register, http.StatusCreated)
	userID, _ := dataOf(t, register)["id"].(string)

	login := request(harness.Router, http.MethodPost, testsupport.URL("/auth/login"),
		map[string]any{"identifier": "alice", "password": "correct horse battery"}, nil)
	requireStatus(t, login, http.StatusOK)
	token := sessionCookie(t, login, harness.Config.Security.SessionCookieName)

	cookieHeader := map[string]string{"Cookie": harness.Config.Security.SessionCookieName + "=" + token}

	// 停用前 /auth/me 正常。
	beforeDisable := request(harness.Router, http.MethodGet, testsupport.URL("/auth/me"), nil, cookieHeader)
	requireStatus(t, beforeDisable, http.StatusOK)

	// 管理员停用（此处直接改库，Users 管理端点在后续阶段实现）。
	if _, err := harness.Pool.Exec(ctx,
		`UPDATE users SET status = 'disabled', updated_at = now() WHERE id = $1`, userID); err != nil {
		t.Fatalf("停用用户失败: %v", err)
	}

	// 停用后既有会话必须立即失效。
	afterDisable := request(harness.Router, http.MethodGet, testsupport.URL("/auth/me"), nil, cookieHeader)
	requireStatus(t, afterDisable, http.StatusUnauthorized)
	requireErrorCode(t, afterDisable, domain.CodeAuthenticationReq)

	// 停用后不能重新登录。
	relogin := request(harness.Router, http.MethodPost, testsupport.URL("/auth/login"),
		map[string]any{"identifier": "alice", "password": "correct horse battery"}, nil)
	requireStatus(t, relogin, http.StatusForbidden)
	requireErrorCode(t, relogin, domain.CodePermissionDenied)
}

// TestLogoutRevokesRedisSession 验证登出真的删除 Redis 会话，而不只是清 Cookie。
func TestLogoutRevokesRedisSession(t *testing.T) {
	harness := testsupport.NewHarness(t)

	requireStatus(t, request(harness.Router, http.MethodPost, testsupport.URL("/auth/register"),
		registrationBody("alice", "alice@example.com"), nil), http.StatusCreated)

	login := request(harness.Router, http.MethodPost, testsupport.URL("/auth/login"),
		map[string]any{"identifier": "alice", "password": "correct horse battery"}, nil)
	requireStatus(t, login, http.StatusOK)
	token := sessionCookie(t, login, harness.Config.Security.SessionCookieName)

	cookieHeader := map[string]string{"Cookie": harness.Config.Security.SessionCookieName + "=" + token}

	logout := request(harness.Router, http.MethodPost, testsupport.URL("/auth/logout"), nil, cookieHeader)
	requireStatus(t, logout, http.StatusNoContent)

	// Cookie 必须被清除。
	cleared := false
	for _, cookie := range logout.Result().Cookies() {
		if cookie.Name == harness.Config.Security.SessionCookieName && cookie.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("登出必须设置 Max-Age < 0 的 Cookie 以清除会话")
	}

	// 关键断言：即使客户端继续携带旧令牌，也不再有效（Redis 中已被删除）。
	reuse := request(harness.Router, http.MethodGet, testsupport.URL("/auth/me"), nil, cookieHeader)
	requireStatus(t, reuse, http.StatusUnauthorized)

	keys, err := harness.Redis.Client.Keys(context.Background(), "pb:session:*").Result()
	if err != nil {
		t.Fatalf("扫描会话键失败: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("登出后 Redis 中不应残留会话，实际 %d 个", len(keys))
	}
}

// TestSessionCookieHasHttpOnlyAndSameSite 验证 Cookie 安全属性。
func TestSessionCookieHasHttpOnlyAndSameSite(t *testing.T) {
	harness := testsupport.NewHarness(t)

	requireStatus(t, request(harness.Router, http.MethodPost, testsupport.URL("/auth/register"),
		registrationBody("alice", "alice@example.com"), nil), http.StatusCreated)

	login := request(harness.Router, http.MethodPost, testsupport.URL("/auth/login"),
		map[string]any{"identifier": "alice", "password": "correct horse battery"}, nil)
	requireStatus(t, login, http.StatusOK)

	var session *http.Cookie
	for _, cookie := range login.Result().Cookies() {
		if cookie.Name == harness.Config.Security.SessionCookieName {
			session = cookie
		}
	}
	if session == nil {
		t.Fatal("未找到会话 Cookie")
	}
	if !session.HttpOnly {
		t.Error("会话 Cookie 必须是 HttpOnly（防止 XSS 窃取）")
	}
	if session.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v，期望 Lax", session.SameSite)
	}
	if session.Path != "/" {
		t.Errorf("Path = %q，期望 /", session.Path)
	}
}

// TestProtectedEndpointRequiresSessionCookie 验证会话网关与真实会话校验的配合。
func TestProtectedEndpointRequiresSessionCookie(t *testing.T) {
	harness := testsupport.NewHarness(t)

	t.Run("无 Cookie 被网关拦截", func(t *testing.T) {
		recorder := request(harness.Router, http.MethodGet, testsupport.URL("/auth/me"), nil, nil)
		requireStatus(t, recorder, http.StatusUnauthorized)
		requireErrorCode(t, recorder, domain.CodeAuthenticationReq)
	})

	t.Run("伪造 Cookie 通过网关但在会话校验处失败", func(t *testing.T) {
		// 这验证了两层职责分离：网关只查 Cookie 存在性，Redis 决定会话有效性。
		recorder := request(harness.Router, http.MethodGet, testsupport.URL("/auth/me"), nil,
			map[string]string{"Cookie": harness.Config.Security.SessionCookieName + "=forged-token-value"})
		requireStatus(t, recorder, http.StatusUnauthorized)
		requireErrorCode(t, recorder, domain.CodeAuthenticationReq)
	})
}

// TestRegisterValidationErrorsAre422WithFields 验证字段校验错误的契约形状。
func TestRegisterValidationErrorsAre422WithFields(t *testing.T) {
	harness := testsupport.NewHarness(t)

	tests := []struct {
		name      string
		body      map[string]any
		wantField string
	}{
		{
			name:      "缺少用户名",
			body:      map[string]any{"email": "a@example.com", "display_name": "A", "password": "password123"},
			wantField: "username",
		},
		{
			name:      "邮箱格式错误",
			body:      map[string]any{"username": "alice", "email": "not-an-email", "display_name": "A", "password": "password123"},
			wantField: "email",
		},
		{
			name:      "密码过短",
			body:      map[string]any{"username": "alice", "email": "a@example.com", "display_name": "A", "password": "short"},
			wantField: "password",
		},
		{
			name:      "用户名含非法字符",
			body:      map[string]any{"username": "ali ce", "email": "a@example.com", "display_name": "A", "password": "password123"},
			wantField: "username",
		},
		{
			name:      "时区无效",
			body:      map[string]any{"username": "alice", "email": "a@example.com", "display_name": "A", "password": "password123", "timezone": "Mars/Olympus"},
			wantField: "timezone",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			recorder := request(harness.Router, http.MethodPost, testsupport.URL("/auth/register"), tc.body, nil)
			requireStatus(t, recorder, http.StatusUnprocessableEntity)

			errorBody := requireErrorCode(t, recorder, domain.CodeValidationError)
			fields, _ := errorBody["fields"].(map[string]any)
			if _, ok := fields[tc.wantField]; !ok {
				t.Fatalf("字段明细缺少 %q，实际为 %v", tc.wantField, fields)
			}
		})
	}

	// 全部校验失败都不应写入任何用户。
	var count int
	if err := harness.Pool.QueryRow(context.Background(), `SELECT count(*) FROM users`).Scan(&count); err != nil {
		t.Fatalf("统计用户失败: %v", err)
	}
	if count != 0 {
		t.Fatalf("校验失败时不得写入用户，实际数量 = %d", count)
	}
}

// TestMalformedJSONIs400NotInternalError 覆盖请求体解析错误的分级。
func TestMalformedJSONIs400NotInternalError(t *testing.T) {
	harness := testsupport.NewHarness(t)

	tests := []struct {
		name string
		body string
	}{
		{name: "非法 JSON 语法", body: `{"username": `},
		{name: "空请求体", body: ``},
		{name: "字段类型不符", body: `{"username": 123, "email": "a@example.com"}`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, testsupport.URL("/auth/register"),
				strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			harness.Router.ServeHTTP(recorder, req)

			if recorder.Code >= 500 {
				t.Fatalf("请求体解析错误不得返回 5xx，实际 %d（响应体：%s）", recorder.Code, recorder.Body.String())
			}
			// 必须是统一错误信封，而不是框架的纯文本。
			errorOf(t, recorder)
		})
	}
}

// TestUnknownFieldIsRejected 验证未知字段被显式拒绝。
//
// 这条规则直接对应一个历史缺陷：早期实现因校验元数据静默失效，
// 客户端把 expected_updated_at 拼错后会静默退化为 last-write-wins。
// 显式拒绝未知字段让这类错误立刻暴露。
func TestUnknownFieldIsRejected(t *testing.T) {
	harness := testsupport.NewHarness(t)

	body := registrationBody("alice", "alice@example.com")
	body["expected_update_at"] = "2026-10-11T00:00:00Z" // 拼写错误

	recorder := request(harness.Router, http.MethodPost, testsupport.URL("/auth/register"), body, nil)
	requireStatus(t, recorder, http.StatusUnprocessableEntity)
	requireErrorCode(t, recorder, domain.CodeValidationError)
}

// TestHealthEndpointsAgainstRealDependencies 覆盖就绪检查对真实依赖的判定。
func TestHealthEndpointsAgainstRealDependencies(t *testing.T) {
	harness := testsupport.NewHarness(t)

	t.Run("ready 在真实依赖可用时返回 200", func(t *testing.T) {
		recorder := request(harness.Router, http.MethodGet, testsupport.URL("/health/ready"), nil, nil)
		requireStatus(t, recorder, http.StatusOK)
	})

	t.Run("live 不依赖外部资源", func(t *testing.T) {
		recorder := request(harness.Router, http.MethodGet, testsupport.URL("/health/live"), nil, nil)
		requireStatus(t, recorder, http.StatusOK)
	})

	t.Run("check 需要 arg 头", func(t *testing.T) {
		missing := request(harness.Router, http.MethodGet, testsupport.URL("/health/check"), nil, nil)
		requireStatus(t, missing, http.StatusUnprocessableEntity)

		present := request(harness.Router, http.MethodGet, testsupport.URL("/health/check"), nil,
			map[string]string{"arg": "smoke"})
		requireStatus(t, present, http.StatusOK)
	})
}

// TestSessionTTLMatchesConfiguration 验证会话 TTL 与配置一致。
//
// 关键点：服务使用注入的固定时钟（harness.Clock），而 Redis TTL 是相对**真实**时间计算的。
// 因此期望值必须计入固定时钟与真实时间的偏移，否则断言会因注入时钟而失败。
// 这正是"固定时钟"约定带来的必要测试调整（全局基线约定 6.3）。
func TestSessionTTLMatchesConfiguration(t *testing.T) {
	harness := testsupport.NewHarness(t)

	requireStatus(t, request(harness.Router, http.MethodPost, testsupport.URL("/auth/register"),
		registrationBody("alice", "alice@example.com"), nil), http.StatusCreated)
	requireStatus(t, request(harness.Router, http.MethodPost, testsupport.URL("/auth/login"),
		map[string]any{"identifier": "alice", "password": "correct horse battery"}, nil), http.StatusOK)

	keys, err := harness.Redis.Client.Keys(context.Background(), "pb:session:*").Result()
	if err != nil {
		t.Fatalf("扫描会话键失败: %v", err)
	}
	// 归因保护：若上一轮用例留下会话，本用例的断言会失去意义。
	if len(keys) != 1 {
		t.Fatalf("期望恰好 1 个会话键，实际 %d 个: %v", len(keys), keys)
	}

	ttl, err := harness.Redis.Client.TTL(context.Background(), keys[0]).Result()
	if err != nil {
		t.Fatalf("读取 TTL 失败: %v", err)
	}

	// 期望 TTL **恰好等于配置值**。
	//
	// 这里刻意不再补偿"固定时钟与真实时间的差"：会话的 ExpiresAt 由注入时钟算出，
	// 判断过期也走同一个时钟，因此 Redis 里的 TTL 就是一个纯粹的配置量。
	//
	// 早期实现用 time.Until(ExpiresAt)（真实时钟）算 TTL，于是本用例不得不
	// 把时钟差补回去；那等于把这个缺陷写进了断言——而它真正的后果是
	// 固定时钟 + TTL 一旦被真实时间越过，所有登录都会 500。
	expected := harness.Config.Security.SessionTTL()
	drift := ttl - expected
	if drift < 0 {
		drift = -drift
	}
	// 允许 30 秒的执行与调度误差。
	if drift > 30*time.Second {
		t.Fatalf("会话 TTL = %v，期望约 %v（偏差 %v；固定时钟 %v）",
			ttl, expected, drift, harness.Clock.Now())
	}
}

// TestPasswordHashIsUpgradedOnLogin 验证弱参数摘要在真实数据库上被升级。
func TestPasswordHashIsUpgradedOnLogin(t *testing.T) {
	harness := testsupport.NewHarness(t)
	ctx := context.Background()

	// 用**确实更弱**的参数写入用户，模拟历史遗留数据。
	//
	// 不能复用 harness.Hasher：它与 TestArgon2Params 参数相同，
	// NeedsRehash 会正确判定"无需升级"，用例就失去意义。
	weakHasher := platform.NewPasswordHasherWith(platform.Argon2Params{
		Memory: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16,
	})
	weakHash, err := weakHasher.Hash("correct horse battery")
	if err != nil {
		t.Fatalf("生成弱摘要失败: %v", err)
	}
	userID := harness.IDs.MustUUID()
	if _, err := harness.Pool.Exec(ctx,
		`INSERT INTO users (id, username, email, display_name, password_hash)
		 VALUES ($1, 'alice', 'alice@example.com', 'Alice', $2)`, userID, weakHash); err != nil {
		t.Fatalf("写入用户失败: %v", err)
	}

	requireStatus(t, request(harness.Router, http.MethodPost, testsupport.URL("/auth/login"),
		map[string]any{"identifier": "alice", "password": "correct horse battery"}, nil), http.StatusOK)

	var upgraded string
	if err := harness.Pool.QueryRow(ctx,
		`SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&upgraded); err != nil {
		t.Fatalf("读取摘要失败: %v", err)
	}
	if upgraded == weakHash {
		t.Fatal("登录后应在真实数据库中升级弱参数摘要")
	}
	matched, err := harness.Hasher.Verify("correct horse battery", upgraded)
	if err != nil || !matched {
		t.Fatalf("升级后的摘要必须仍能校验原密码（matched=%v, err=%v）", matched, err)
	}
}
