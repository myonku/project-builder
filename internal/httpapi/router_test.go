package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myonku/project-builder/internal/config"
	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/platform/clock"
	"github.com/myonku/project-builder/internal/platform/idgen"
)

// testConfig 返回用于 HTTP 层测试的最小配置。
func testConfig() config.Config {
	return config.Config{
		APIVersion: "1",
		Env:        config.EnvTest,
		Server: config.ServerConfig{
			Host:               "127.0.0.1",
			Port:               8080,
			ReadTimeoutSeconds: 15, WriteTimeoutSeconds: 30, ShutdownTimeoutSeconds: 10,
			CORSAllowedOrigins: []string{"http://127.0.0.1:5173"},
		},
		Security: config.SecurityConfig{
			SessionCookieName: "pb_session_test",
			SessionTTLSeconds: 3600,
		},
	}
}

// testRouter 构造一个装配真实的中间件链与健康检查的路由。
func testRouter(t *testing.T, health Health, corsOrigins []string) *gin.Engine {
	t.Helper()

	cfg := testConfig()
	if corsOrigins != nil {
		cfg.Server.CORSAllowedOrigins = corsOrigins
	}

	return Router(Dependencies{
		Config: cfg,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:  clock.NewFixed(time.Date(2026, 10, 11, 9, 30, 0, 0, time.UTC)),
		IDs:    idgen.New(),
		Health: health,
	})
}

// doRequest 执行一次请求并返回响应记录器。
func doRequest(router *gin.Engine, method, path string, body io.Reader, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, body)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// decodeEnvelope 解析错误 / 成功响应体。
func decodeEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("响应不是合法 JSON: %v（原文：%s）", err, recorder.Body.String())
	}
	return payload
}

// requireErrorEnvelope 断言响应是符合契约的统一错误信封。
func requireErrorEnvelope(t *testing.T, recorder *httptest.ResponseRecorder, wantStatus int, wantCode domain.ErrorCode) map[string]any {
	t.Helper()

	if recorder.Code != wantStatus {
		t.Fatalf("状态码 = %d，期望 %d（响应体：%s）", recorder.Code, wantStatus, recorder.Body.String())
	}
	if contentType := recorder.Header().Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Fatalf("Content-Type = %q，期望 application/json", contentType)
	}

	payload := decodeEnvelope(t, recorder)
	rawError, ok := payload["error"]
	if !ok {
		t.Fatalf("错误响应缺少 error 字段: %s", recorder.Body.String())
	}
	errorBody, ok := rawError.(map[string]any)
	if !ok {
		t.Fatalf("error 字段不是对象: %s", recorder.Body.String())
	}

	if got := errorBody["code"]; got != string(wantCode) {
		t.Fatalf("error.code = %v，期望 %q", got, wantCode)
	}
	message, _ := errorBody["message"].(string)
	if message == "" {
		t.Fatal("error.message 不得为空")
	}
	requestID, _ := errorBody["request_id"].(string)
	if requestID == "" {
		t.Fatal("error.request_id 不得为空")
	}
	// 契约要求错误响应带 request_id，且必须与响应头一致。
	if header := recorder.Header().Get(RequestIDHeader); header != requestID {
		t.Fatalf("响应头 %s = %q，与信封中的 request_id %q 不一致", RequestIDHeader, header, requestID)
	}

	return errorBody
}

// TestErrorCodesAllHaveStatusAndMessage 是本包最重要的用例：
// 每个领域错误码都必须同时登记状态码与对外文案。
//
// 历史缺陷对照：Python 版本只为 500 注册了求解器，其余领域错误全部退化为 500。
// 这里用显式断言保证新增错误码时不会漏配映射。
func TestErrorCodesAllHaveStatusAndMessage(t *testing.T) {
	t.Parallel()

	allCodes := []domain.ErrorCode{
		domain.CodeInvalidRequest,
		domain.CodeInvalidDateRange,
		domain.CodeValidationError,
		domain.CodeAuthenticationReq,
		domain.CodeInvalidCredentials,
		domain.CodePermissionDenied,
		domain.CodeResourceNotFound,
		domain.CodeUserNotFound,
		domain.CodeGroupNotFound,
		domain.CodeProjectNotFound,
		domain.CodeTaskNotFound,
		domain.CodeConflict,
		domain.CodeDuplicateMember,
		domain.CodeVersionConflict,
		domain.CodeFileTooLarge,
		domain.CodeUnsupportedMediaType,
		domain.CodeRateLimited,
		domain.CodeInternalError,
	}

	for _, code := range allCodes {
		if _, ok := errorStatus[code]; !ok {
			t.Errorf("错误码 %q 缺少 HTTP 状态码映射", code)
		}
		if _, ok := errorMessage[code]; !ok {
			t.Errorf("错误码 %q 缺少对外文案", code)
		}
	}
}

// TestEveryDomainErrorConstantIsMapped 通过预置错误值反向确认映射完整性。
func TestEveryDomainErrorConstantIsMapped(t *testing.T) {
	t.Parallel()

	presets := map[string]*domain.Error{
		"ErrInvalidRequest":         domain.ErrInvalidRequest,
		"ErrInvalidDateRange":       domain.ErrInvalidDateRange,
		"ErrValidation":             domain.ErrValidation,
		"ErrAuthenticationRequired": domain.ErrAuthenticationRequired,
		"ErrInvalidCredentials":     domain.ErrInvalidCredentials,
		"ErrPermissionDenied":       domain.ErrPermissionDenied,
		"ErrResourceNotFound":       domain.ErrResourceNotFound,
		"ErrConflict":               domain.ErrConflict,
		"ErrDuplicateMember":        domain.ErrDuplicateMember,
		"ErrVersionConflict":        domain.ErrVersionConflict,
		"ErrInternal":               domain.ErrInternal,
	}

	for name, preset := range presets {
		if _, ok := errorStatus[preset.Code]; !ok {
			t.Errorf("%s (%q) 缺少状态码映射", name, preset.Code)
		}
		if _, ok := errorMessage[preset.Code]; !ok {
			t.Errorf("%s (%q) 缺少对外文案", name, preset.Code)
		}
	}
}

// TestStatusForCodeDefaultsTo500 覆盖未登记错误码的兜底行为。
func TestStatusForCodeDefaultsTo500(t *testing.T) {
	t.Parallel()

	if got := StatusForCode(domain.ErrorCode("something_unregistered")); got != http.StatusInternalServerError {
		t.Fatalf("未登记错误码应兜底为 500，实际 %d", got)
	}
	if got := MessageForCode(domain.ErrorCode("something_unregistered")); got == "" {
		t.Fatal("未登记错误码也必须返回兜底文案")
	}
}

// TestErrorMessagesAreEnglish 断言对外文案是英文（接口文档 2.4 的示例均为英文）。
func TestErrorMessagesAreEnglish(t *testing.T) {
	t.Parallel()

	for code, message := range errorMessage {
		for _, r := range message {
			if r > 0x2E80 { // CJK 起始区
				t.Errorf("错误码 %q 的对外文案包含中文字符: %q", code, message)
				break
			}
		}
		if !strings.HasSuffix(message, ".") && !strings.HasSuffix(message, "found") {
			// 仅作风格提示，不构成契约违反。
			t.Logf("提示：错误码 %q 的文案未以句号结尾: %q", code, message)
		}
	}
}

// TestHealthLiveRequiresNoDependencies 覆盖存活检查。
func TestHealthLiveRequiresNoDependencies(t *testing.T) {
	t.Parallel()

	router := testRouter(t, Health{
		PostgresPing: func() error { t.Fatal("liveness 不得检查数据库"); return nil },
		RedisPing:    func() error { t.Fatal("liveness 不得检查 Redis"); return nil },
	}, nil)

	recorder := doRequest(router, http.MethodGet, "/api/v1/health/live", nil, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200（响应体：%s）", recorder.Code, recorder.Body.String())
	}
}

// TestHealthCheckRequiresArgHeader 覆盖 /health/check 的 arg 头语义。
func TestHealthCheckRequiresArgHeader(t *testing.T) {
	t.Parallel()

	router := testRouter(t, Health{}, nil)

	t.Run("缺少 arg 头被拒绝", func(t *testing.T) {
		recorder := doRequest(router, http.MethodGet, "/api/v1/health/check", nil, nil)
		requireErrorEnvelope(t, recorder, http.StatusUnprocessableEntity, domain.CodeValidationError)
	})

	t.Run("提供 arg 头返回 200", func(t *testing.T) {
		recorder := doRequest(router, http.MethodGet, "/api/v1/health/check", nil,
			map[string]string{"arg": "smoke"})
		if recorder.Code != http.StatusOK {
			t.Fatalf("状态码 = %d，期望 200（响应体：%s）", recorder.Code, recorder.Body.String())
		}
	})
}

// TestHealthReadyReportsDependencyState 覆盖就绪检查的三态。
func TestHealthReadyReportsDependencyState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		health     Health
		wantStatus int
		wantError  string
	}{
		{
			name:       "全部可用",
			health:     Health{PostgresPing: func() error { return nil }, RedisPing: func() error { return nil }},
			wantStatus: http.StatusOK,
		},
		{
			name: "数据库不可用",
			health: Health{
				PostgresPing: func() error { return errUnavailable },
				RedisPing:    func() error { return nil },
			},
			wantStatus: http.StatusServiceUnavailable,
			wantError:  "database_unavailable",
		},
		{
			name: "Redis 不可用",
			health: Health{
				PostgresPing: func() error { return nil },
				RedisPing:    func() error { return errUnavailable },
			},
			wantStatus: http.StatusServiceUnavailable,
			wantError:  "redis_unavailable",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			router := testRouter(t, tc.health, nil)
			recorder := doRequest(router, http.MethodGet, "/api/v1/health/ready", nil, nil)

			if recorder.Code != tc.wantStatus {
				t.Fatalf("状态码 = %d，期望 %d（响应体：%s）", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			payload := decodeEnvelope(t, recorder)
			if tc.wantError != "" {
				if payload["error"] != tc.wantError {
					t.Fatalf("error = %v，期望 %q", payload["error"], tc.wantError)
				}
			} else if payload["status"] != "ready" {
				t.Fatalf("status = %v，期望 ready", payload["status"])
			}
		})
	}
}

var errUnavailable = errString("dependency unavailable")

type errString string

func (e errString) Error() string { return string(e) }

// TestRootLivenessProbe 覆盖根级 /healthz。
func TestRootLivenessProbe(t *testing.T) {
	t.Parallel()

	router := testRouter(t, Health{}, nil)
	recorder := doRequest(router, http.MethodGet, "/healthz", nil, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", recorder.Code)
	}
}

// TestNoRouteReturnsErrorEnvelope 覆盖未命中路由的错误信封。
func TestNoRouteReturnsErrorEnvelope(t *testing.T) {
	t.Parallel()

	router := testRouter(t, Health{}, nil)
	recorder := doRequest(router, http.MethodGet, "/api/v1/does-not-exist", nil, nil)
	requireErrorEnvelope(t, recorder, http.StatusNotFound, domain.CodeResourceNotFound)
}

// TestNoMethodReturnsErrorEnvelope 覆盖方法不被支持时的错误信封。
func TestNoMethodReturnsErrorEnvelope(t *testing.T) {
	t.Parallel()

	router := testRouter(t, Health{}, nil)
	// /api/v1/health/live 只注册了 GET。
	recorder := doRequest(router, http.MethodDelete, "/api/v1/health/live", nil, nil)
	requireErrorEnvelope(t, recorder, http.StatusBadRequest, domain.CodeInvalidRequest)
}

// TestRequestIDIsGeneratedAndEchoed 覆盖请求标识的生成与回显。
func TestRequestIDIsGeneratedAndEchoed(t *testing.T) {
	t.Parallel()

	router := testRouter(t, Health{}, nil)

	t.Run("未提供时自动生成", func(t *testing.T) {
		recorder := doRequest(router, http.MethodGet, "/api/v1/health/live", nil, nil)
		if got := recorder.Header().Get(RequestIDHeader); got == "" {
			t.Fatal("响应必须包含 request id 头")
		}
	})

	t.Run("已提供时沿用", func(t *testing.T) {
		recorder := doRequest(router, http.MethodGet, "/api/v1/health/live", nil,
			map[string]string{RequestIDHeader: "client-supplied-id"})
		if got := recorder.Header().Get(RequestIDHeader); got != "client-supplied-id" {
			t.Fatalf("request id = %q，期望沿用客户端提供的值", got)
		}
	})
}

// TestSessionGatewayBlocksProtectedRoutes 覆盖会话网关的拦截行为。
func TestSessionGatewayBlocksProtectedRoutes(t *testing.T) {
	t.Parallel()

	// 注册一个受保护端点（不在白名单内），验证无 Cookie 时被网关拦截。
	cfg := testConfig()
	router := Router(Dependencies{
		Config: cfg,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		IDs:    idgen.New(),
		Health: Health{},
		Modules: []Registrar{registrarFunc(func(api *gin.RouterGroup) {
			api.GET("/auth/me", func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"data": "should-not-reach"})
			})
		})},
	})

	// 受保护路径：无 Cookie 必须 401。
	recorder := doRequest(router, http.MethodGet, "/api/v1/auth/me", nil, nil)
	requireErrorEnvelope(t, recorder, http.StatusUnauthorized, domain.CodeAuthenticationReq)

	// 携带 Cookie 时应放行（会话有效性由端点自行校验）。
	recorder = doRequest(router, http.MethodGet, "/api/v1/auth/me", nil,
		map[string]string{"Cookie": cfg.Security.SessionCookieName + "=any-token"})
	if recorder.Code != http.StatusOK {
		t.Fatalf("携带 Cookie 时应通过网关，状态码 = %d（响应体：%s）", recorder.Code, recorder.Body.String())
	}
}

// TestSessionGatewayAllowsPublicRoutes 覆盖白名单：公开路径不应被网关拦截。
//
// 这里断言"未被网关拦截"，而不是"业务成功"：/health/live 本身是公开端点。
func TestSessionGatewayAllowsPublicRoutes(t *testing.T) {
	t.Parallel()

	router := testRouter(t, Health{}, nil)

	for _, path := range []string{"/api/v1/health/live", "/api/v1/health/check", "/api/v1/health/ready"} {
		t.Run(path, func(t *testing.T) {
			recorder := doRequest(router, http.MethodGet, path, nil, map[string]string{"arg": "x"})
			if recorder.Code == http.StatusUnauthorized {
				t.Fatalf("公开路径 %s 不应被会话网关拦截", path)
			}
		})
	}
}

// TestSessionGatewayIsPublicPrefixMatching 覆盖白名单匹配规则（含版本前缀剥离）。
func TestSessionGatewayIsPublicPrefixMatching(t *testing.T) {
	t.Parallel()

	gateway := NewSessionGateway("pb_session")
	gateway.PublicPathPrefix = "/api/v1"

	tests := map[string]bool{
		"/api/v1/auth/login":       true,
		"/api/v1/auth/register":    true,
		"/api/v1/health/ready":     true,
		"/api/v1/auth/me":          false,
		"/api/v1/auth/logout":      false,
		"/api/v1/projects":         false,
		"/api/v1/auth/login/extra": true, // 前缀匹配
		"/api/v1/auth/loginish":    false,
		"/healthz":                 false, // 无版本前缀，不匹配白名单
		"/api/v2/auth/login":       false, // 其他版本不命中
	}

	for path, want := range tests {
		if got := gateway.IsPublic(path); got != want {
			t.Errorf("IsPublic(%q) = %v，期望 %v", path, got, want)
		}
	}
}

// TestRecoveryMiddlewareConvertsPanicToEnvelope 覆盖 panic 恢复。
//
// 历史缺陷对照：gin.Recovery() 会输出纯文本堆栈，既不符合统一信封，
// 也向客户端泄漏内部实现细节（接口文档 2.4 明确禁止）。
func TestRecoveryMiddlewareConvertsPanicToEnvelope(t *testing.T) {
	t.Parallel()

	cfg := testConfig()
	engine := Router(Dependencies{
		Config: cfg,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		IDs:    idgen.New(),
		Health: Health{},
		Modules: []Registrar{registrarFunc(func(api *gin.RouterGroup) {
			api.GET("/panic", func(_ *gin.Context) {
				panic("internal detail: connection string postgres://user:secret@host/db")
			})
		})},
	})

	// 携带会话 Cookie 以通过会话网关，确保请求真正到达会 panic 的处理函数。
	recorder := doRequest(engine, http.MethodGet, "/api/v1/panic", nil,
		map[string]string{"Cookie": cfg.Security.SessionCookieName + "=any-token"})
	body := recorder.Body.String()

	requireErrorEnvelope(t, recorder, http.StatusInternalServerError, domain.CodeInternalError)

	// 响应体绝不能包含 panic 内容或堆栈痕迹。
	for _, leak := range []string{"secret", "postgres://", "goroutine", "panic", ".go:"} {
		if strings.Contains(body, leak) {
			t.Fatalf("响应体泄漏了内部细节 %q: %s", leak, body)
		}
	}
}

// registrarFunc 把函数适配为 Registrar。
type registrarFunc func(api *gin.RouterGroup)

func (f registrarFunc) Register(api *gin.RouterGroup) { f(api) }

// TestCORSMiddlewareRejectsUnknownOrigin 覆盖 CORS 白名单。
func TestCORSMiddlewareRejectsUnknownOrigin(t *testing.T) {
	t.Parallel()

	router := testRouter(t, Health{}, []string{"https://plan.example.com"})

	t.Run("白名单来源被回显", func(t *testing.T) {
		recorder := doRequest(router, http.MethodGet, "/api/v1/health/live", nil,
			map[string]string{"Origin": "https://plan.example.com"})
		if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://plan.example.com" {
			t.Fatalf("Access-Control-Allow-Origin = %q，期望回显白名单来源", got)
		}
		if got := recorder.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Fatalf("Access-Control-Allow-Credentials = %q，期望 true", got)
		}
	})

	t.Run("非白名单来源不写 CORS 头", func(t *testing.T) {
		recorder := doRequest(router, http.MethodGet, "/api/v1/health/live", nil,
			map[string]string{"Origin": "https://evil.example.com"})
		if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("非白名单来源不得获得 CORS 许可，实际为 %q", got)
		}
	})

	t.Run("绝不回显通配符", func(t *testing.T) {
		recorder := doRequest(router, http.MethodGet, "/api/v1/health/live", nil,
			map[string]string{"Origin": "https://plan.example.com"})
		if got := recorder.Header().Get("Access-Control-Allow-Origin"); got == "*" {
			t.Fatal("带凭据的 CORS 不得回显通配符")
		}
	})
}

// TestCORSPreflight 覆盖预检请求。
func TestCORSPreflight(t *testing.T) {
	t.Parallel()

	router := testRouter(t, Health{}, []string{"https://plan.example.com"})

	recorder := doRequest(router, http.MethodOptions, "/api/v1/auth/login", nil, map[string]string{
		"Origin":                        "https://plan.example.com",
		"Access-Control-Request-Method": "POST",
	})
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("预检状态码 = %d，期望 204", recorder.Code)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, "POST") {
		t.Fatalf("Access-Control-Allow-Methods = %q，应包含 POST", got)
	}
}

// TestPaginationHasNext 覆盖 has_next 推导规则。
func TestPaginationHasNext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		page, pageSize, total int
		want                  bool
	}{
		{page: 1, pageSize: 20, total: 0, want: false},
		{page: 1, pageSize: 20, total: 20, want: false},
		{page: 1, pageSize: 20, total: 21, want: true},
		{page: 2, pageSize: 20, total: 40, want: false},
		{page: 2, pageSize: 20, total: 41, want: true},
		// 防御性：非法分页不得报告 has_next。
		{page: 0, pageSize: 20, total: 100, want: false},
		{page: 1, pageSize: 0, total: 100, want: false},
	}

	for _, tc := range tests {
		got := NewPagination(tc.page, tc.pageSize, tc.total)
		if got.HasNext != tc.want {
			t.Errorf("NewPagination(%d, %d, %d).HasNext = %v，期望 %v",
				tc.page, tc.pageSize, tc.total, got.HasNext, tc.want)
		}
		if got.Page != tc.page || got.PageSize != tc.pageSize || got.Total != tc.total {
			t.Errorf("分页字段未原样回填: %+v", got)
		}
	}
}

// TestListResponseDataIsNeverNull 覆盖列表 data 的空数组语义。
func TestListResponseDataIsNeverNull(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	RespondList(context, []string(nil), NewPagination(1, 20, 0))

	body := recorder.Body.String()
	if strings.Contains(body, `"data":null`) {
		t.Fatalf("列表 data 不得为 null: %s", body)
	}
	if !strings.Contains(body, `"data":[]`) {
		t.Fatalf("空列表应序列化为 []: %s", body)
	}
}

// TestAbortWithNilIsNoop 覆盖 Abort(nil) 的防御行为。
func TestAbortWithNilIsNoop(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	Abort(context, nil)

	if len(context.Errors) != 0 {
		t.Fatalf("Abort(nil) 不应写入错误，实际 %d 条", len(context.Errors))
	}
}

// TestIsNotFound 覆盖不存在语义判定。
func TestIsNotFound(t *testing.T) {
	t.Parallel()

	tests := map[error]bool{
		domain.ErrResourceNotFound:                  true,
		domain.New(domain.CodeUserNotFound, "x"):    true,
		domain.New(domain.CodeProjectNotFound, "x"): true,
		domain.New(domain.CodeTaskNotFound, "x"):    true,
		domain.New(domain.CodeGroupNotFound, "x"):   true,
		domain.ErrConflict:                          false,
		domain.ErrPermissionDenied:                  false,
		errUnavailable:                              false,
	}

	for err, want := range tests {
		if got := IsNotFound(err); got != want {
			t.Errorf("IsNotFound(%v) = %v，期望 %v", err, got, want)
		}
	}
}
