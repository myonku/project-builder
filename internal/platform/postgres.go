// Package platform 提供外部资源适配器：数据库、Redis、邮件、存储、时间与 ID。
//
// 本包由 cmd/api 在启动时创建并注入，业务模块只依赖接口，不自行创建连接
// （见 docs/01-Go服务端架构设计.md 4.3 依赖方向）。
package platform

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/myonku/project-builder/internal/config"
)

// Postgres 包装 pgxpool 连接池。
//
// 选择显式 SQL 而非 ORM：Python 版本最严重的缺陷正是 ORM 隐式 IO
// （更新后读取过期属性触发同步重载 → MissingGreenlet，所有更新/删除端点 500）。
// Go 版本用 UPDATE ... RETURNING 显式回读，见 docs/01-Go服务端架构设计.md 5.1。
type Postgres struct {
	Pool *pgxpool.Pool
}

// NewPostgres 按配置创建连接池并完成一次连通性验证。
//
// 启动阶段快速失败：数据库不可达时直接返回错误，不进入"能启动但每个请求都 500"的状态。
func NewPostgres(ctx context.Context, cfg config.DatabaseConfig, logger *slog.Logger) (*Postgres, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		// 不回显 DSN，只回显脱敏形式。
		return nil, fmt.Errorf("解析数据库连接串失败（%s）: %w", cfg.RedactedDSN(), err)
	}

	poolCfg.MaxConns = cfg.MaxConns
	poolCfg.MinConns = cfg.MinConns
	poolCfg.MaxConnLifetime = cfg.MaxConnLifetime()
	poolCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout()

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("创建数据库连接池失败（%s）: %w", cfg.RedactedDSN(), err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout())
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("数据库连通性检查失败（%s）: %w", cfg.RedactedDSN(), err)
	}

	logger.Info("数据库连接池已就绪",
		slog.String("dsn", cfg.RedactedDSN()),
		slog.Int("max_conns", int(cfg.MaxConns)),
	)
	return &Postgres{Pool: pool}, nil
}

// Ping 检查数据库可用性，供 /health/ready 使用。
func (p *Postgres) Ping(ctx context.Context) error {
	if p == nil || p.Pool == nil {
		return fmt.Errorf("数据库连接池未初始化")
	}
	return p.Pool.Ping(ctx)
}

// Close 释放连接池。重复调用是安全的。
func (p *Postgres) Close() {
	if p != nil && p.Pool != nil {
		p.Pool.Close()
	}
}

// HealthCheck 返回带超时的健康检查函数，便于在 readiness 中统一处理。
func (p *Postgres) HealthCheck(timeout time.Duration) func(context.Context) error {
	return func(ctx context.Context) error {
		checkCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return p.Ping(checkCtx)
	}
}
