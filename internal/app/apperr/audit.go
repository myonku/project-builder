package apperr

import (
	"context"

	"github.com/myonku/project-builder/internal/domain"
)

// AuditWriter 在**同一个事务**里写入一条操作记录。
//
// 为什么这个端口出现在 apperr（app 层的公共装配面）而不是某个业务模块：
// 审计是横切关注点，七个业务模块都要写它。让每个模块各自定义一份接口、
// 或者让模块之间互相 import，都会形成网状依赖；集中成一个接口后，
// 各模块只依赖它，实现（internal/app/audit）由装配点注入。
//
// 参数 q 是 Querier 而不是连接池：写入必须复用业务事务，
// 因此调用方把当前的 tx 传进来（见 AuditWrite.Flush）。
type AuditWriter interface {
	WriteAudit(ctx context.Context, q Querier, entry domain.AuditEntry) error
}

// AuditWrite 是"这次写操作要记一条审计"的载体。
//
// 用法（仓储层，在业务语句之后、同一事务内）：
//
//	if err := params.Audit.Flush(ctx, tx); err != nil {
//		return err
//	}
//
// 为什么用指针 + nil 判断：审计是**可选**横切能力（单元测试、只读模块不需要它），
// 调用方不必在每个写路径上写 if；nil 接收者是空操作。
type AuditWrite struct {
	Writer AuditWriter
	Entry  domain.AuditEntry
}

// NewAuditWrite 构造审计载体；writer 为 nil 时返回 nil（表示这次写不记审计）。
func NewAuditWrite(writer AuditWriter, entry domain.AuditEntry) *AuditWrite {
	if writer == nil {
		return nil
	}
	return &AuditWrite{Writer: writer, Entry: entry}
}

// Flush 把这条审计写进给定的事务执行器。
//
// nil 接收者或未注入 Writer 都是**空操作**——这是刻意的：
// 是否记审计由装配点决定，而"记了但写不进去"才是错误（由实现返回）。
func (w *AuditWrite) Flush(ctx context.Context, q Querier) error {
	if w == nil || w.Writer == nil {
		return nil
	}
	return w.Writer.WriteAudit(ctx, q, w.Entry)
}
