package platform

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/myonku/project-builder/internal/config"
)

// Redis 包装 Redis 客户端。
//
// 边界（全局基线约定 9、服务端设计 2）：Redis 只保存会话、限流与短期锁，
// 不保存核心业务唯一数据。Redis 关闭时应用仍应可启动，由调用方判断 Enabled。
type Redis struct {
	Client  *redis.Client
	Enabled bool
}

// NewRedis 按配置创建 Redis 客户端并完成一次连通性验证。
//
// 当 cfg.Enabled 为 false 时返回 Enabled=false 的空对象且不报错。
//
// 历史缺陷对照：Python 版本在关闭 Redis 时 session_store 仍强制要求资源，
// 导致全站 500（见 backend/服务端推进计划.md 3.3）。
func NewRedis(ctx context.Context, cfg config.RedisConfig, logger *slog.Logger) (*Redis, error) {
	if !cfg.Enabled {
		logger.Warn("Redis 已禁用：会话功能不可用，相关端点将返回 503")
		return &Redis{Enabled: false}, nil
	}

	client := redis.NewClient(&redis.Options{
		Addr:         cfg.Address(),
		Password:     cfg.Password,
		DB:           cfg.DB,
		DialTimeout:  cfg.RedisDialTimeout(),
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	})

	pingCtx, cancel := context.WithTimeout(ctx, cfg.RedisDialTimeout())
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("Redis 连通性检查失败（%s）: %w", cfg.Address(), err)
	}

	logger.Info("Redis 客户端已就绪", slog.String("address", cfg.Address()), slog.Int("db", cfg.DB))
	return &Redis{Client: client, Enabled: true}, nil
}

// Ping 检查 Redis 可用性，供 /health/ready 使用。
func (r *Redis) Ping(ctx context.Context) error {
	if r == nil || !r.Enabled || r.Client == nil {
		return nil // 未启用不算失败
	}
	return r.Client.Ping(ctx).Err()
}

// Close 释放客户端。重复调用与未启用场景都是安全的。
func (r *Redis) Close() error {
	if r == nil || !r.Enabled || r.Client == nil {
		return nil
	}
	return r.Client.Close()
}

// HealthCheck 返回带超时的健康检查函数。
func (r *Redis) HealthCheck(timeout time.Duration) func(context.Context) error {
	return func(ctx context.Context) error {
		checkCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return r.Ping(checkCtx)
	}
}
