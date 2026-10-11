// Command api 是服务端唯一入口：读取配置、装配依赖、启动 HTTP、优雅关闭。
//
// 装配策略（docs/01-Go服务端架构设计.md 4.4）：显式构造每一个依赖，
// 不使用反射式依赖注入，也不引入工厂层。所有外部资源在启动时创建一次，
// 通过结构体注入到各业务模块。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/myonku/project-builder/internal/app/auth"
	"github.com/myonku/project-builder/internal/config"
	"github.com/myonku/project-builder/internal/httpapi"
	"github.com/myonku/project-builder/internal/platform"
	"github.com/myonku/project-builder/internal/platform/clock"
	"github.com/myonku/project-builder/internal/platform/idgen"
)

func main() {
	if err := run(); err != nil {
		// 启动失败必须快速失败并给出脱敏后的原因（全局基线约定 7.4）。
		fmt.Fprintf(os.Stderr, "服务启动失败: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	logger := newLogger()
	slog.SetDefault(logger)

	cfg, err := config.Load(os.Getenv("APP_CONFIG_FILE"), os.Getenv("APP_ENV_FILE"))
	if err != nil {
		return err
	}

	// 启动摘要：只输出脱敏后的字段与"是否已配置"，绝不输出敏感值。
	logger.Info("配置已加载", cfg.Summary()...)

	// 根 context：收到 SIGINT/SIGTERM 时取消，用于中断启动过程与后台任务。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	clk := clock.NewSystem()
	ids := idgen.NewTimeOrdered(clk.Now)

	postgres, err := platform.NewPostgres(ctx, cfg.Database, logger)
	if err != nil {
		return err
	}
	defer postgres.Close()

	redisClient, err := platform.NewRedis(ctx, cfg.Redis, logger)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := redisClient.Close(); closeErr != nil {
			logger.Warn("关闭 Redis 客户端失败", slog.Any("cause", closeErr))
		}
	}()

	// Redis 客户端的 *redis.Client 在未启用时为 nil，会话存储据此报告不可用，
	// 而不是让整个应用启动失败（历史缺陷对照：Python 版本此处在关闭 Redis 时全站 500）。
	var redisRaw = redisClient.Client

	sessionStore := auth.NewRedisSessionStore(redisRaw, redisClient.Enabled)
	if !sessionStore.Available() {
		logger.Warn("会话后端不可用：认证相关端点将返回错误，服务其余部分仍可运行")
	}

	authService := auth.NewService(auth.Options{
		Users:    auth.NewRepository(postgres.Pool),
		Sessions: sessionStore,
		Hasher:   platform.NewPasswordHasher(),
		IDs:      ids,
		Clock:    clk,
		Logger:   logger,
		Security: cfg.Security,
	})

	engine := httpapi.Router(httpapi.Dependencies{
		Config: cfg,
		Logger: logger,
		Clock:  clk,
		IDs:    ids,
		Health: httpapi.Health{
			PostgresPing: func() error {
				pingCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				return postgres.Ping(pingCtx)
			},
			RedisPing: func() error {
				if !redisClient.Enabled {
					return nil
				}
				pingCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				return redisClient.Ping(pingCtx)
			},
		},
		Modules: []httpapi.Registrar{
			auth.NewHandler(authService, logger),
		},
	})

	server := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:      engine,
		ReadTimeout:  cfg.Server.ReadTimeout(),
		WriteTimeout: cfg.Server.WriteTimeout(),
		// IdleTimeout 略大于写超时，保证连接复用但不会长期挂住。
		IdleTimeout: cfg.Server.WriteTimeout() + 30*time.Second,
	}

	// 在独立 goroutine 中监听，主流程等待关闭信号。
	serveErr := make(chan error, 1)
	go func() {
		logger.Info("HTTP 服务已启动",
			slog.String("address", server.Addr),
			slog.String("api_prefix", "/api/v"+cfg.APIVersion),
		)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- fmt.Errorf("HTTP 服务异常退出: %w", err)
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		logger.Info("收到关闭信号，开始优雅关闭")
	}

	// 优雅关闭：给在途请求一个明确的预算，超时后强制结束。
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout())
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("优雅关闭超时: %w", err)
	}
	logger.Info("服务已关闭")
	return nil
}

// newLogger 构造结构化日志器。
//
// 时间统一由 slog 以 UTC 输出（全局基线约定 12）；
// 日志中不得写入密码、令牌、完整邮件内容或附件内容。
func newLogger() *slog.Logger {
	level := slog.LevelInfo
	switch os.Getenv("LOG_LEVEL") {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			// 统一把时间改写为 UTC，避免不同机器的本地时区混进日志。
			if attr.Key == slog.TimeKey {
				if t, ok := attr.Value.Any().(time.Time); ok {
					return slog.Time(slog.TimeKey, t.UTC())
				}
			}
			return attr
		},
	})
	return slog.New(handler)
}
