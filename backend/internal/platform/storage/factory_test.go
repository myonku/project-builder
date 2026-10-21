package storage

import (
	"testing"

	"github.com/myonku/project-builder/internal/config"
)

func TestAvatarFactoryLocalReusesRoot(t *testing.T) {
	root := t.TempDir()
	store, err := NewAvatarFromConfig(config.StorageConfig{
		Backend:   "local",
		LocalRoot: root,
	}, nil, "")
	if err != nil {
		t.Fatalf("构造本地头像存储失败: %v", err)
	}
	local, ok := store.(*Local)
	if !ok {
		t.Fatalf("期望 *Local，实际 %T", store)
	}
	if local.Root() == "" {
		t.Fatal("本地头像存储根目录不能为空")
	}
}

func TestAvatarFactoryGridFSRequiresClient(t *testing.T) {
	_, err := NewAvatarFromConfig(config.StorageConfig{
		Backend:      "gridfs",
		AvatarBucket: "avatars",
	}, nil, "worklane")
	if err == nil {
		t.Fatal("gridfs 模式缺少 MongoDB 客户端时必须报错")
	}
}
