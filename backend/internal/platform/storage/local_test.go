package storage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newLocal(t *testing.T) *Local {
	t.Helper()

	local, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal 返回错误: %v", err)
	}
	return local
}

func TestLocalPutOpenDeleteExists(t *testing.T) {
	t.Parallel()

	local := newLocal(t)
	ctx := context.Background()
	const key = "attachments/ab/blob-1"
	payload := []byte("hello attachment")

	if err := local.Put(ctx, key, bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatalf("Put 返回错误: %v", err)
	}

	exists, err := local.Exists(ctx, key)
	if err != nil || !exists {
		t.Fatalf("Exists = %v, %v；期望 true, nil", exists, err)
	}

	reader, err := local.Open(ctx, key)
	if err != nil {
		t.Fatalf("Open 返回错误: %v", err)
	}
	got := make([]byte, len(payload))
	if _, err := reader.Read(got); err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	_ = reader.Close()
	if !bytes.Equal(got, payload) {
		t.Fatalf("读回内容 = %q，期望 %q", got, payload)
	}

	if err := local.Delete(ctx, key); err != nil {
		t.Fatalf("Delete 返回错误: %v", err)
	}
	exists, err = local.Exists(ctx, key)
	if err != nil || exists {
		t.Fatalf("删除后 Exists = %v, %v；期望 false, nil", exists, err)
	}
}

// TestLocalDeleteIsIdempotent 锁住"删除不存在的对象不算错误"。
//
// 用例层的彻底删除依赖这条性质：对象可能已被清理，此时删行仍应成功，
// 否则一次重试就会返回错误，把"已经删掉了"报成失败。
func TestLocalDeleteIsIdempotent(t *testing.T) {
	t.Parallel()

	local := newLocal(t)
	if err := local.Delete(context.Background(), "attachments/none"); err != nil {
		t.Fatalf("删除不存在的对象应返回 nil，实际 %v", err)
	}
}

func TestLocalOpenMissingReturnsErrObjectNotFound(t *testing.T) {
	t.Parallel()

	local := newLocal(t)
	_, err := local.Open(context.Background(), "attachments/missing")
	if !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("Open 缺失对象应返回 ErrObjectNotFound，实际 %v", err)
	}
}

// TestLocalRejectsEscapingKeys 是本包最重要的安全测试。
//
// stored_key 由服务端生成，理论上不会出现这些写法；但一旦哪天有人
// 把用户提供的文件名拼进 key，这里必须拦住，而不是让它写到根目录之外。
func TestLocalRejectsEscapingKeys(t *testing.T) {
	t.Parallel()

	local := newLocal(t)
	ctx := context.Background()

	keys := []string{
		"",
		"   ",
		"../outside",
		"attachments/../../outside",
		"attachments/./x",
		"attachments//x",
		"/etc/passwd",
		`attachments\..\x`,
		"C:/windows/system32",
		"attachments/\x00x",
	}
	for _, key := range keys {
		if err := local.Put(ctx, key, strings.NewReader("x"), 1); err == nil {
			t.Errorf("key %q 应被拒绝，实际写入成功", key)
		}
		if _, err := local.Open(ctx, key); err == nil {
			t.Errorf("key %q 应被拒绝，实际打开成功", key)
		}
		if err := local.Delete(ctx, key); err == nil {
			t.Errorf("key %q 应被拒绝，实际删除成功", key)
		}
	}
}

// TestLocalPutRejectsSizeMismatch 覆盖写入自检。
func TestLocalPutRejectsSizeMismatch(t *testing.T) {
	t.Parallel()

	local := newLocal(t)
	err := local.Put(context.Background(), "attachments/ab/x", strings.NewReader("12345"), 4)
	if err == nil {
		t.Fatal("声明大小与实际不符时应报错")
	}
	// 失败后不得留下目标文件。
	exists, statErr := local.Exists(context.Background(), "attachments/ab/x")
	if statErr != nil {
		t.Fatalf("Exists 返回错误: %v", statErr)
	}
	if exists {
		t.Fatal("写入失败时不应留下目标文件")
	}
}

// TestLocalPutOverwritesAtomically 覆盖覆盖写与临时文件清理。
func TestLocalPutOverwritesAtomically(t *testing.T) {
	t.Parallel()

	local := newLocal(t)
	ctx := context.Background()
	const key = "attachments/ab/same"

	if err := local.Put(ctx, key, strings.NewReader("first"), 5); err != nil {
		t.Fatalf("首次写入失败: %v", err)
	}
	if err := local.Put(ctx, key, strings.NewReader("second"), 6); err != nil {
		t.Fatalf("覆盖写入失败: %v", err)
	}

	reader, err := local.Open(ctx, key)
	if err != nil {
		t.Fatalf("Open 返回错误: %v", err)
	}
	defer func() { _ = reader.Close() }()
	content := make([]byte, 6)
	if _, err := reader.Read(content); err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if string(content) != "second" {
		t.Fatalf("内容 = %q，期望 second", content)
	}

	// 目录里不应残留 .upload-* 临时文件。
	entries, err := os.ReadDir(filepath.Join(local.Root(), "attachments", "ab"))
	if err != nil {
		t.Fatalf("读取目录失败: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".upload-") {
			t.Fatalf("残留了临时文件 %q", entry.Name())
		}
	}
}

func TestNewLocalRejectsEmptyRoot(t *testing.T) {
	t.Parallel()

	if _, err := NewLocal("  "); err == nil {
		t.Fatal("空根目录应被拒绝")
	}
}
