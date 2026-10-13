// Package storage 定义附件对象存储的最低层接口与本地文件系统实现。
//
// 定位（backend/docs/B01-Go服务端架构设计.md 3）：这一层刻意做得**很薄**，
// 作用与数据库客户端类似——只按 key 做基本 CRUD，不含任何业务语义：
//
//   - 不决定 key 怎么生成（由用例层生成，且绝不使用用户提供的文件名）；
//   - 不记录任何元数据（文件名、大小、MIME、归属、权限都在数据库里）；
//   - 不知道附件、项目、任务的任何概念。
//
// 这样做的理由是**可迁移性**：初版只用本地文件系统，但真实数据里
// "文件"往往是最难迁移的部分。把边界压到"按 key 读写字节"，
// 将来换成对象存储或基于 MongoDB 的实现时，只需要再实现这四个方法，
// 数据库里的 stored_key 一字不改即可沿用。
//
// 因此：**任何业务规则都不得下沉到本包**。需要判断"谁能读"、
// "该存到哪个 key"，那是用例层的职责。
package storage

import (
	"context"
	"errors"
	"io"
)

// ErrObjectNotFound 表示对象在底层存储中不存在。
//
// 与数据库的"行不存在"是两回事：数据库是权威，存储只是字节的存放处。
// 两者不一致（有行无对象）时由用例层决定如何处置。
var ErrObjectNotFound = errors.New("storage: 对象不存在")

// Store 是最低层的对象存储。
//
// key 是**相对路径风格的不透明标识**（如 "attachments/ab/<uuid>"）：
// 实现不得假定它有层级含义，本地实现会把它当作相对路径，
// 但必须拒绝任何逃出根目录的写法。
type Store interface {
	// Put 写入对象。已存在时覆盖（同一 key 的重复写入是幂等的）。
	//
	// size 是预期字节数；实现可以用它做前置校验，但不得据此跳过读取。
	Put(ctx context.Context, key string, reader io.Reader, size int64) error
	// Open 打开对象用于读取。对象不存在时返回 ErrObjectNotFound。
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete 删除对象。对象不存在时返回 nil——
	// 删除是幂等的，调用方不需要先查是否存在。
	Delete(ctx context.Context, key string) error
	// Exists 报告对象是否存在。
	Exists(ctx context.Context, key string) (bool, error)
}
