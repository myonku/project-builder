package httpapi

import (
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myonku/project-builder/internal/config"
	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/platform/clock"
)

// staleBucketTTL 是空桶的保留时长：超过这段时间没有请求的来源地址会被清理掉。
//
// 没有它，一张按来源地址增长的 map 就是一个慢速内存泄漏
// （每个来源地址只会留下最后一次请求的时间与几个浮点数，但地址数量本身没有上限）。
const staleBucketTTL = 10 * time.Minute

// sweepInterval 是清理扫描的最小间隔，避免每个请求都遍历整张 map。
const sweepInterval = time.Minute

// bucket 是单个来源地址的令牌桶。
type bucket struct {
	tokens float64
	// last 是上一次补充令牌的时刻（由注入时钟给出）。
	last time.Time
}

// RateLimiter 是进程内的令牌桶限流器，按"来源地址"分桶。
//
// 设计边界（全部是有意的，改动前请先看 docs/03 §3.8 与 docs/05 §6 第 37 条）：
//
//  1. **只做全局中间件，不做用户/会话级限流**。按会话限流在登录之前就失效，
//     而登录口令尝试恰恰是最需要挡的场景；按用户限流则要求先解析会话，
//     把一次 Redis 往返压在限流之前，代价与收益不成比例。
//  2. **不信任 `X-Forwarded-For`**。gin 默认的 `ClientIP()` 会读取该头，
//     而它是客户端可伪造的——那样任何人都能靠换一个头绕过限流。
//     这里只使用连接的远端地址；部署在反向代理之后时，
//     所有请求会落到同一个桶（等价于"全局一个桶"，这正是本功能的定位），
//     若将来真的需要按真实客户端 IP 计数，必须显式配置可信代理并改这里，
//     而不是无条件相信请求头。
//  3. **单实例内存态**。本系统是单进程部署（与 scheduler 同理），
//     不引入 Redis 分布式限流：多一个会失败的依赖，换来的是本项目不需要的能力。
//  4. **时间只来自注入时钟**（红线 9）：不调用 time.Now()/time.Since()。
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64
	burst   float64
	clk     clock.Clock
	// lastSweep 是上一次清理扫描的时刻（注入时钟）。
	lastSweep time.Time

	// now 便于测试注入，默认取 clk.Now()。
	// 保留为字段而不是直接调用 clk.Now()，是为了让"推进时间"这件事
	// 在测试里只发生在一个地方。
	now func() time.Time
}

// NewRateLimiter 构造限流器。clk 为 nil 时退回系统时钟。
//
// 返回 nil 表示**不限流**（配置关闭或取值非法）。调用方应容忍 nil：
// RateLimitMiddleware(nil) 是一个纯粹的直通中间件。
func NewRateLimiter(cfg config.RateLimitConfig, clk clock.Clock) *RateLimiter {
	if !cfg.Enabled || cfg.RequestsPerMinute <= 0 || cfg.Burst <= 0 {
		return nil
	}
	if clk == nil {
		clk = clock.NewSystem()
	}
	limiter := &RateLimiter{
		buckets: make(map[string]*bucket),
		rate:    cfg.Rate(),
		burst:   float64(cfg.Burst),
		clk:     clk,
	}
	limiter.now = clk.Now
	limiter.lastSweep = clk.Now()
	return limiter
}

// Middleware 返回限流中间件。
//
// 必须在 ErrorHandler **之后**注册：Abort 只是把错误写进上下文，
// 真正输出统一信封的是 ErrorHandler 的 defer（见 problem.go 的说明）。
func (r *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		allowed, retryAfter := r.Allow(clientKey(c))
		if !allowed {
			// Retry-After 让调用方知道"等多久再试"，而不是盲目重试。
			c.Header("Retry-After", strconv.Itoa(retryAfter))
			Abort(c, domain.ErrRateLimited)
			return
		}
		c.Next()
	}
}

// RateLimitMiddleware 是装配用的便捷形式：limiter 为 nil 时直通。
func RateLimitMiddleware(limiter *RateLimiter) gin.HandlerFunc {
	if limiter == nil {
		return func(c *gin.Context) { c.Next() }
	}
	return limiter.Middleware()
}

// Allow 报告该 key 是否可以放行，以及若被拒绝需要等待的秒数。
func (r *RateLimiter) Allow(key string) (bool, int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.now()
	r.sweepLocked(now)

	b, ok := r.buckets[key]
	if !ok {
		// 新来源从"满桶"开始：一个从未出现过的地址不应因为
		// 上一次运行留下的状态而被立刻拒绝。
		b = &bucket{tokens: r.burst, last: now}
		r.buckets[key] = b
	} else {
		r.refillLocked(b, now)
	}

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, retryAfterSeconds(b.tokens, r.rate)
}

// refillLocked 按流逝的时间补充令牌（在持锁状态下调用）。
func (r *RateLimiter) refillLocked(b *bucket, now time.Time) {
	elapsed := now.Sub(b.last).Seconds()
	if elapsed <= 0 {
		// 时钟未前进（或测试把时钟往回拨）：不补充，也不把 last 往回写。
		return
	}
	b.tokens = math.Min(r.burst, b.tokens+elapsed*r.rate)
	b.last = now
}

// sweepLocked 清理长时间没有请求的来源桶（在持锁状态下调用）。
func (r *RateLimiter) sweepLocked(now time.Time) {
	if now.Sub(r.lastSweep) < sweepInterval {
		return
	}
	r.lastSweep = now
	for key, b := range r.buckets {
		if now.Sub(b.last) > staleBucketTTL {
			delete(r.buckets, key)
		}
	}
}

// Buckets 报告当前保留的来源桶数量（供测试与观测使用）。
func (r *RateLimiter) Buckets() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.buckets)
}

// retryAfterSeconds 返回"再等几秒就有令牌"的上取整值，至少 1 秒。
func retryAfterSeconds(tokens, rate float64) int {
	if rate <= 0 {
		return 1
	}
	missing := 1 - tokens
	seconds := int(math.Ceil(missing / rate))
	if seconds < 1 {
		seconds = 1
	}
	return seconds
}

// clientKey 提取限流的来源地址。
//
// 只取连接的远端 IP，不含端口：端口每次连接都不同，带上它等于按连接限流。
func clientKey(c *gin.Context) string {
	remote := c.Request.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	remote = strings.TrimSpace(remote)
	if remote == "" {
		// 理论上不会发生（HTTP 服务总能拿到远端地址）；
		// 兜底成同一个 key，宁可"严格一点"也不要因为空值而放行全部。
		return "unknown"
	}
	return remote
}
