// GridFS 是基于 MongoDB GridFS 的 Store 实现。
//
// key 同时作为 GridFS 的 filename 与 fileID（字符串），因此每个 stored_key
// 在 bucket 内恰好对应一个文件：Put 先删除旧文件再上传，避免同名多版本堆积。
// 该层仍然只做按 key 读写，不关心附件/项目/权限。
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// GridFS 是基于 MongoDB GridFS 的 Store。
type GridFS struct {
	bucket *mongo.GridFSBucket
	name   string
}

// 编译期确认接口实现。
var _ Store = (*GridFS)(nil)

// NewGridFS 构造 GridFS 存储。bucketName 不能为空。
func NewGridFS(client *mongo.Client, database, bucketName string) (*GridFS, error) {
	if client == nil {
		return nil, errors.New("storage: MongoDB 客户端不能为空")
	}
	if database == "" {
		return nil, errors.New("storage: MongoDB 数据库名不能为空")
	}
	if bucketName == "" {
		return nil, errors.New("storage: GridFS bucket 名不能为空")
	}
	bucket := client.Database(database).GridFSBucket(options.GridFSBucket().SetName(bucketName))
	return &GridFS{bucket: bucket, name: bucketName}, nil
}

// BucketName 返回 bucket 名（供日志/迁移命令使用）。
func (g *GridFS) BucketName() string { return g.name }

// Put 实现 Store：同一 key 覆盖。
func (g *GridFS) Put(ctx context.Context, key string, reader io.Reader, size int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if key == "" {
		return errors.New("storage: key 不能为空")
	}
	// 先清理旧对象，保证同 key 只有一个文件（GridFS 默认允许同名多版本）。
	if err := g.Delete(ctx, key); err != nil {
		return err
	}

	stream, err := g.bucket.OpenUploadStreamWithID(ctx, key, key)
	if err != nil {
		return fmt.Errorf("storage: 创建 GridFS 上传流失败: %w", err)
	}
	written, copyErr := io.Copy(stream, reader)
	if copyErr != nil {
		_ = stream.Abort()
		return fmt.Errorf("storage: 写入 GridFS 对象失败: %w", copyErr)
	}
	if size >= 0 && written != size {
		_ = stream.Abort()
		return fmt.Errorf("storage: 写入字节数与声明不符（声明 %d，实际 %d）", size, written)
	}
	if err := stream.Close(); err != nil {
		return fmt.Errorf("storage: 关闭 GridFS 上传流失败: %w", err)
	}
	return nil
}

// Open 实现 Store。
func (g *GridFS) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stream, err := g.bucket.OpenDownloadStream(ctx, key)
	if err != nil {
		if errors.Is(err, mongo.ErrFileNotFound) {
			return nil, ErrObjectNotFound
		}
		return nil, fmt.Errorf("storage: 打开 GridFS 对象失败: %w", err)
	}
	return stream, nil
}

// Delete 实现 Store；对象不存在时返回 nil（删除幂等）。
func (g *GridFS) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := g.bucket.Delete(ctx, key); err != nil && !errors.Is(err, mongo.ErrFileNotFound) {
		return fmt.Errorf("storage: 删除 GridFS 对象失败: %w", err)
	}
	return nil
}

// Exists 实现 Store。
func (g *GridFS) Exists(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	count, err := g.bucket.GetFilesCollection().CountDocuments(ctx, bson.M{"_id": key})
	if err != nil {
		return false, fmt.Errorf("storage: 查询 GridFS 对象失败: %w", err)
	}
	return count > 0, nil
}
