// Mongo 包装 MongoDB 7 客户端。
//
// 定位：**只承载附件/头像的字节（GridFS）**，不保存任何业务事实。
// PostgreSQL 仍是唯一业务事实源；Mongo 不可用时应由装配层决定是快速失败
// （gridfs 模式）还是完全不连接（local 模式）。
package platform

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/myonku/project-builder/internal/config"
)

// Mongo 包装 MongoDB 客户端；Client 只在需要 GridFS 时创建。
type Mongo struct {
	Client *mongo.Client
}

// NewMongo 连接 MongoDB 并完成一次 ping。
//
// URI 可能含凭据，错误信息**不输出 URI 原文**，只输出数据库名。
func NewMongo(ctx context.Context, cfg config.MongoConfig, logger *slog.Logger) (*Mongo, error) {
	client, err := mongo.Connect(options.Client().
		ApplyURI(cfg.URI).
		SetServerSelectionTimeout(cfg.ServerSelectionTimeout()).
		SetMaxPoolSize(uint64(cfg.MaxPoolSize)))
	if err != nil {
		return nil, fmt.Errorf("创建 MongoDB 客户端失败（database=%s）: %w", cfg.Database, err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout())
	defer cancel()
	if err := client.Ping(pingCtx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("MongoDB 连通性检查失败（database=%s）: %w", cfg.Database, err)
	}

	logger.Info("MongoDB 已连接",
		slog.String("database", cfg.Database),
		slog.Int("max_pool_size", cfg.MaxPoolSize),
	)
	return &Mongo{Client: client}, nil
}

// Ping 检查 MongoDB 可用性，供 /health/ready 使用。
func (m *Mongo) Ping(ctx context.Context) error {
	if m == nil || m.Client == nil {
		return fmt.Errorf("MongoDB 客户端未初始化")
	}
	return m.Client.Ping(ctx, nil)
}

// Close 断开 Mongo 客户端。重复调用是安全的。
func (m *Mongo) Close(ctx context.Context) error {
	if m == nil || m.Client == nil {
		return nil
	}
	return m.Client.Disconnect(ctx)
}

// MongoHealthCheck 返回带超时的健康检查函数。
func (m *Mongo) HealthCheck(timeout time.Duration) func(context.Context) error {
	return func(ctx context.Context) error {
		checkCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return m.Ping(checkCtx)
	}
}
