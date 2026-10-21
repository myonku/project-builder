package storage_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/myonku/project-builder/internal/platform/storage"
)

func TestGridFSStoreRoundTrip(t *testing.T) {
	uri := os.Getenv("TEST_MONGO_URI")
	if uri == "" {
		t.Skip("TEST_MONGO_URI 未设置；跳过 GridFS 集成测试")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	client, err := mongo.Connect(options.Client().
		ApplyURI(uri).
		SetServerSelectionTimeout(3 * time.Second))
	if err != nil {
		t.Fatalf("连接 MongoDB 失败: %v", err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	if err := client.Ping(ctx, nil); err != nil {
		t.Skipf("MongoDB 不可达，跳过: %v", err)
	}

	bucketName := "attachments_test_" + time.Now().Format("150405.000000000")
	store, err := storage.NewGridFS(client, os.Getenv("TEST_MONGO_DATABASE"), bucketName)
	if err != nil {
		t.Fatalf("构造 GridFS 失败: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_ = client.Database(os.Getenv("TEST_MONGO_DATABASE")).Collection(bucketName + ".files").Drop(cleanupCtx)
		_ = client.Database(os.Getenv("TEST_MONGO_DATABASE")).Collection(bucketName + ".chunks").Drop(cleanupCtx)
	})

	key := "attachments/ab/00000000-0000-0000-0000-000000000001"
	first := []byte("hello gridfs")
	if err := store.Put(ctx, key, bytes.NewReader(first), int64(len(first))); err != nil {
		t.Fatalf("Put 失败: %v", err)
	}
	exists, err := store.Exists(ctx, key)
	if err != nil || !exists {
		t.Fatalf("Exists = %v, %v；期望 true", exists, err)
	}
	got, err := readAll(ctx, store, key)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	if !bytes.Equal(got, first) {
		t.Fatalf("读取内容 = %q，期望 %q", got, first)
	}

	second := []byte("overwritten")
	if err := store.Put(ctx, key, bytes.NewReader(second), int64(len(second))); err != nil {
		t.Fatalf("覆盖 Put 失败: %v", err)
	}
	got, err = readAll(ctx, store, key)
	if err != nil {
		t.Fatalf("覆盖后 Open 失败: %v", err)
	}
	if !bytes.Equal(got, second) {
		t.Fatalf("覆盖后内容 = %q，期望 %q", got, second)
	}

	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}
	exists, err = store.Exists(ctx, key)
	if err != nil || exists {
		t.Fatalf("删除后 Exists = %v, %v；期望 false", exists, err)
	}
	if _, err := store.Open(ctx, key); !errors.Is(err, storage.ErrObjectNotFound) {
		t.Fatalf("删除后 Open 错误 = %v，期望 ErrObjectNotFound", err)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatalf("重复 Delete 应幂等，实际 %v", err)
	}
}

func TestGridFSPutRejectsSizeMismatch(t *testing.T) {
	uri := os.Getenv("TEST_MONGO_URI")
	if uri == "" {
		t.Skip("TEST_MONGO_URI 未设置；跳过 GridFS 集成测试")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetServerSelectionTimeout(3 * time.Second))
	if err != nil {
		t.Fatalf("连接 MongoDB 失败: %v", err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	if err := client.Ping(ctx, nil); err != nil {
		t.Skipf("MongoDB 不可达，跳过: %v", err)
	}
	bucketName := "attachments_test_" + time.Now().Format("150405.000000000")
	store, err := storage.NewGridFS(client, os.Getenv("TEST_MONGO_DATABASE"), bucketName)
	if err != nil {
		t.Fatalf("构造 GridFS 失败: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		_ = client.Database(os.Getenv("TEST_MONGO_DATABASE")).Collection(bucketName + ".files").Drop(cleanupCtx)
		_ = client.Database(os.Getenv("TEST_MONGO_DATABASE")).Collection(bucketName + ".chunks").Drop(cleanupCtx)
	})
	err = store.Put(ctx, "attachments/ab/mismatch", bytes.NewReader([]byte("abc")), 99)
	if err == nil {
		t.Fatal("size 不匹配时应返回错误")
	}
	exists, existsErr := store.Exists(ctx, "attachments/ab/mismatch")
	if existsErr != nil {
		t.Fatalf("Exists 失败: %v", existsErr)
	}
	if exists {
		t.Fatal("size 不匹配时不应留下对象")
	}
}

func readAll(ctx context.Context, store storage.Store, key string) ([]byte, error) {
	reader, err := store.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}
