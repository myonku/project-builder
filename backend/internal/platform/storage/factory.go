// 根据配置选择对象存储实现。装配点只调用这里，业务模块不感知 local/gridfs。
package storage

import (
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/myonku/project-builder/internal/config"
)

// NewFromConfig 按 storage.backend 构造 Store。
//
// mongoClient 只在 backend == "gridfs" 时使用；local 模式允许为 nil，
// 因此默认部署不需要启动 MongoDB。
func NewFromConfig(cfg config.StorageConfig, mongoClient *mongo.Client, mongoDatabase string) (Store, error) {
	return newStore(cfg, cfg.GridFSBucket, "attachments", mongoClient, mongoDatabase)
}

// NewAvatarFromConfig 按 storage.backend 构造头像 Store。
//
// 头像与附件使用**两个独立 bucket**（gridfs 模式），但共用同一套 key 不变式；
// local 模式仍落在同一个根目录下，key 前缀 `avatars/` 负责命名空间隔离。
func NewAvatarFromConfig(cfg config.StorageConfig, mongoClient *mongo.Client, mongoDatabase string) (Store, error) {
	return newStore(cfg, cfg.AvatarBucket, "avatars", mongoClient, mongoDatabase)
}

func newStore(
	cfg config.StorageConfig, bucketOverride, defaultBucket string,
	mongoClient *mongo.Client, mongoDatabase string,
) (Store, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Backend)) {
	case "local":
		return NewLocal(cfg.LocalRoot)
	case "gridfs":
		if mongoClient == nil {
			return nil, fmt.Errorf("storage: backend=gridfs 但未提供 MongoDB 客户端")
		}
		bucket := strings.TrimSpace(bucketOverride)
		if bucket == "" {
			bucket = defaultBucket
		}
		return NewGridFS(mongoClient, mongoDatabase, bucket)
	default:
		return nil, fmt.Errorf("storage: 不支持的 backend %q", cfg.Backend)
	}
}
