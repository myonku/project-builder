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
	switch strings.ToLower(strings.TrimSpace(cfg.Backend)) {
	case "local":
		return NewLocal(cfg.LocalRoot)
	case "gridfs":
		if mongoClient == nil {
			return nil, fmt.Errorf("storage: backend=gridfs 但未提供 MongoDB 客户端")
		}
		bucket := strings.TrimSpace(cfg.GridFSBucket)
		if bucket == "" {
			bucket = "attachments"
		}
		return NewGridFS(mongoClient, mongoDatabase, bucket)
	default:
		return nil, fmt.Errorf("storage: 不支持的 backend %q", cfg.Backend)
	}
}
