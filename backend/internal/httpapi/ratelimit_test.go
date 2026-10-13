package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myonku/project-builder/internal/config"
	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/platform/clock"
	"github.com/myonku/project-builder/internal/platform/idgen"
)

// newTestLimiter 构造一个使用固定时钟的限流器。
//
// 固定时钟是刻意的（红线 9）：令牌补充依赖"现在"，若用真实时钟，
// 断言就会取决于机器快慢与调度抖动。
func newTestLimiter(t *testing.T, clk clock.Clock, perMinute, burst int) *RateLimiter {
	t.Helper()

	limiter := NewRateLimiter(config.RateLimitConfig{
		Enabled:           true,
		RequestsPerMinute: perMinute,
		Burst:             burst,
	}, clk)
	if limiter == nil {
		t.Fatal("限流器未构造成功")
	}
	return limiter
}

// routerWithRateLimit 构造一个开启限流的路由（用 /healthz 作为探测端点：
// 它不依赖数据库与 Redis，因此这里验证的确实只有限流本身）。
func routerWithRateLimit(t *testing.T, clk clock.Clock, perMinute, burst int) *gin.Engine {
	t.Helper()

	cfg := testConfig()
	cfg.RateLimit = config.RateLimitConfig{
		Enabled:           true,
		RequestsPerMinute: perMinute,
		Burst:             burst,
	}
	return Router(Dependencies{
		Config: cfg,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Clock:  clk,
		IDs:    idgen.New(),
	})
}

// doRequestFrom 与 doRequest 相同，但显式指定连接的远端地址。
func doRequestFrom(router *gin.Engine, path, remoteAddr string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.RemoteAddr = remoteAddr
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

// TestRateLimiterAllowsBurstThenRejects 覆盖令牌桶的基本语义。
func TestRateLimiterAllowsBurstThenRejects(t *testing.T) {
	t.Parallel()

	clk := clock.NewFixed(time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC))
	// 60/分钟 = 每秒 1 个令牌；桶容量 3。
	limiter := newTestLimiter(t, clk, 60, 3)

	for i := 1; i <= 3; i++ {
		if allowed, _ := limiter.Allow("10.0.0.1"); !allowed {
			t.Fatalf("第 %d 个请求被拒绝，期望在桶容量内放行", i)
		}
	}

	allowed, retryAfter := limiter.Allow("10.0.0.1")
	if allowed {
		t.Fatal("超出桶容量后仍被放行")
	}
	if retryAfter < 1 {
		t.Fatalf("Retry-After = %d，期望至少 1 秒", retryAfter)
	}
}

// TestRateLimiterRefillsWithInjectedClock 证明补充速率走注入时钟，
// 而不是墙钟：时钟不动，令牌就不回来。
func TestRateLimiterRefillsWithInjectedClock(t *testing.T) {
	t.Parallel()

	clk := clock.NewFixed(time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC))
	limiter := newTestLimiter(t, clk, 60, 1)

	if allowed, _ := limiter.Allow("10.0.0.1"); !allowed {
		t.Fatal("首个请求应被放行")
	}
	if allowed, _ := limiter.Allow("10.0.0.1"); allowed {
		t.Fatal("时钟未前进时不应补充令牌")
	}

	// 秒表前进 1 秒 = 补充 1 个令牌（60/分钟）。
	clk.Advance(time.Second)
	if allowed, _ := limiter.Allow("10.0.0.1"); !allowed {
		t.Fatal("经过 1 秒后应放行 1 个请求")
	}
	if allowed, _ := limiter.Allow("10.0.0.1"); allowed {
		t.Fatal("补给的令牌只应够 1 个请求")
	}
}

// TestRateLimiterIsolatesBucketsPerClient 覆盖分桶：一个来源刷满不应影响另一个。
func TestRateLimiterIsolatesBucketsPerClient(t *testing.T) {
	t.Parallel()

	clk := clock.NewFixed(time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC))
	limiter := newTestLimiter(t, clk, 60, 1)

	if allowed, _ := limiter.Allow("10.0.0.1"); !allowed {
		t.Fatal("第一个来源的首个请求应放行")
	}
	if allowed, _ := limiter.Allow("10.0.0.1"); allowed {
		t.Fatal("第一个来源超出容量后应被拒绝")
	}
	if allowed, _ := limiter.Allow("10.0.0.2"); !allowed {
		t.Fatal("另一个来源不应被第一个来源的额度影响")
	}
}

// TestRateLimiterSweepsStaleBuckets 覆盖内存回收：闲置超过 TTL 的来源桶会被清掉。
func TestRateLimiterSweepsStaleBuckets(t *testing.T) {
	t.Parallel()

	clk := clock.NewFixed(time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC))
	limiter := newTestLimiter(t, clk, 60, 5)

	if allowed, _ := limiter.Allow("10.0.0.1"); !allowed {
		t.Fatal("首个请求应放行")
	}
	if limiter.Buckets() != 1 {
		t.Fatalf("来源桶数量 = %d，期望 1", limiter.Buckets())
	}

	// 越过 TTL 与扫描间隔后再来一个**新**来源，触发扫描。
	clk.Advance(staleBucketTTL + sweepInterval + time.Second)
	limiter.Allow("10.0.0.2")

	if got := limiter.Buckets(); got != 1 {
		t.Fatalf("清理后来源桶数量 = %d，期望 1（只剩新来源）", got)
	}
}

// TestRateLimiterDisabledReturnsNil 确认零值配置 = 不限流。
//
// 这条同时说明了集成测试为什么不受限流影响：harness 的配置里
// RateLimit 是零值（见 internal/testsupport/harness.go）。
func TestRateLimiterDisabledReturnsNil(t *testing.T) {
	t.Parallel()

	if limiter := NewRateLimiter(config.RateLimitConfig{}, nil); limiter != nil {
		t.Fatal("零值配置应返回 nil（不限流）")
	}
	if limiter := NewRateLimiter(config.RateLimitConfig{Enabled: true}, nil); limiter != nil {
		t.Fatal("启用但取值非法时应返回 nil，而不是一个永远拒绝请求的限流器")
	}
}

// TestRateLimitMiddlewareReturnsUnifiedEnvelope 覆盖端到端行为：
// 超出限额返回 429 + 统一信封 + Retry-After，而不是 gin 的裸响应。
func TestRateLimitMiddlewareReturnsUnifiedEnvelope(t *testing.T) {
	t.Parallel()

	clk := clock.NewFixed(time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC))
	router := routerWithRateLimit(t, clk, 60, 2)

	for i := 1; i <= 2; i++ {
		recorder := doRequestFrom(router, "/healthz", "192.0.2.10:1234", nil)
		if recorder.Code != http.StatusOK {
			t.Fatalf("第 %d 个请求状态码 = %d，期望 200（响应体：%s）", i, recorder.Code, recorder.Body.String())
		}
	}

	recorder := doRequestFrom(router, "/healthz", "192.0.2.10:5678", nil)
	// 注意端口不同、IP 相同：端口不参与分桶，否则等于按连接限流。
	requireErrorEnvelope(t, recorder, http.StatusTooManyRequests, domain.CodeRateLimited)

	if retryAfter := recorder.Header().Get("Retry-After"); retryAfter == "" {
		t.Fatal("429 响应缺少 Retry-After 头")
	}

	// 另一个来源不受影响。
	other := doRequestFrom(router, "/healthz", "192.0.2.11:1234", nil)
	if other.Code != http.StatusOK {
		t.Fatalf("另一个来源状态码 = %d，期望 200", other.Code)
	}
}

// TestRateLimitDoesNotTrustForwardedFor 锁定"不信任 X-Forwarded-For"这一选择。
//
// 若改为读取该头，任何人都能靠伪造它绕过限流——这条用例会在那时失败。
func TestRateLimitDoesNotTrustForwardedFor(t *testing.T) {
	t.Parallel()

	clk := clock.NewFixed(time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC))
	router := routerWithRateLimit(t, clk, 60, 1)

	first := doRequestFrom(router, "/healthz", "192.0.2.20:1234",
		map[string]string{"X-Forwarded-For": "203.0.113.1"})
	if first.Code != http.StatusOK {
		t.Fatalf("首个请求状态码 = %d，期望 200", first.Code)
	}

	// 同一个连接来源、伪造一个新的 XFF：仍应被拒绝。
	second := doRequestFrom(router, "/healthz", "192.0.2.20:1234",
		map[string]string{"X-Forwarded-For": "203.0.113.2"})
	requireErrorEnvelope(t, second, http.StatusTooManyRequests, domain.CodeRateLimited)
}
