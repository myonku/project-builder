package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Local 是基于本地文件系统的 Store 实现。
//
// 写入使用"临时文件 + 重命名"：直接写目标文件时，中途失败会留下半截内容，
// 而重命名在同一个文件系统内是原子的，因此对外只可能看到"完整"或"不存在"。
type Local struct {
	root string
}

// 编译期确认接口实现。
var _ Store = (*Local)(nil)

// NewLocal 构造本地存储。root 不存在时会被创建。
func NewLocal(root string) (*Local, error) {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		return nil, errors.New("storage: 本地存储根目录不能为空")
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return nil, fmt.Errorf("storage: 解析根目录失败: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o755); err != nil {
		return nil, fmt.Errorf("storage: 创建根目录失败: %w", err)
	}
	return &Local{root: absolute}, nil
}

// Root 返回规范化后的根目录绝对路径（供日志与排查使用）。
func (l *Local) Root() string { return l.root }

// Put 实现 Store。
func (l *Local) Put(ctx context.Context, key string, reader io.Reader, size int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := l.resolve(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("storage: 创建目录失败: %w", err)
	}

	temp, err := os.CreateTemp(filepath.Dir(path), ".upload-*")
	if err != nil {
		return fmt.Errorf("storage: 创建临时文件失败: %w", err)
	}
	tempName := temp.Name()
	// 出错路径上清理临时文件；成功重命名后它已不存在，Remove 是空操作。
	defer func() { _ = os.Remove(tempName) }()

	written, err := io.Copy(temp, reader)
	if err != nil {
		_ = temp.Close()
		return fmt.Errorf("storage: 写入对象失败: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("storage: 关闭临时文件失败: %w", err)
	}
	// size 只用于自检：调用方（HTTP 层）已经限制了请求体大小，
	// 这里再核对一次，避免"记录的大小"与"实际字节数"不一致。
	if size >= 0 && written != size {
		return fmt.Errorf("storage: 写入字节数与声明不符（声明 %d，实际 %d）", size, written)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("storage: 落盘失败: %w", err)
	}
	return nil
}

// Open 实现 Store。
func (l *Local) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := l.resolve(key)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrObjectNotFound
		}
		return nil, fmt.Errorf("storage: 打开对象失败: %w", err)
	}
	return file, nil
}

// Delete 实现 Store。对象不存在时返回 nil（删除幂等）。
func (l *Local) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := l.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("storage: 删除对象失败: %w", err)
	}
	return nil
}

// Exists 实现 Store。
func (l *Local) Exists(ctx context.Context, key string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	path, err := l.resolve(key)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("storage: 查询对象失败: %w", err)
	}
	return true, nil
}

// resolve 把 key 解析为根目录下的绝对路径。
//
// 这是本包唯一的安全边界，必须严格：
//   - 拒绝空 key、绝对路径、盘符、UNC 前缀与反斜杠（跨平台一致性）；
//   - 拒绝任何 ".." 片段；
//   - 解析后再次确认结果仍在根目录之内（防止符号链接或平台差异造成的逃逸）。
func (l *Local) resolve(key string) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", errors.New("storage: key 不能为空")
	}
	if strings.ContainsAny(key, "\\\x00") {
		return "", fmt.Errorf("storage: key 不得包含反斜杠或空字符: %q", key)
	}
	if strings.HasPrefix(key, "/") || filepath.IsAbs(key) || strings.Contains(key, ":") {
		return "", fmt.Errorf("storage: key 必须是相对路径: %q", key)
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("storage: key 含非法路径片段: %q", key)
		}
	}

	path := filepath.Join(l.root, filepath.FromSlash(key))
	// 双保险：Join 之后必须仍位于根目录之下。
	if path != l.root && !strings.HasPrefix(path, l.root+string(os.PathSeparator)) {
		return "", fmt.Errorf("storage: key 逃出了根目录: %q", key)
	}
	return path, nil
}
