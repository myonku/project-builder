// Package apperr 把 PostgreSQL 错误翻译为领域错误，并提供事务/查询的公共抽象。
//
// 为什么必须集中：数据库约束是并发写入的最终保证，但它抛出的是 SQLSTATE 与约束名。
// 如果各仓储各自判断，必然出现"同一个约束在一处翻译成 409、在另一处漏掉变成 500"。
// Python 版本的 `review_join_request` 就漏掉了对 IntegrityError 的捕获，实测返回 500。
package apperr

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/myonku/project-builder/internal/domain"
)

// 常用 SQLSTATE。
const (
	sqlStateUniqueViolation     = "23505"
	sqlStateForeignKeyViolation = "23503"
	sqlStateCheckViolation      = "23514"
	sqlStateNotNullViolation    = "23502"
)

// Querier 是 pgxpool.Pool 与 pgx.Tx 的公共能力子集。
//
// 仓储的方法都基于它实现，因此同一段 SQL 既能在自动提交模式下执行，
// 也能在显式事务中执行，不需要写两份。
type Querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// 编译期确认两个实现都满足接口。
var (
	_ Querier = (*pgxpool.Pool)(nil)
	_ Querier = (pgx.Tx)(nil)
)

// TxFunc 是在事务中执行的函数体。
type TxFunc func(ctx context.Context, tx Querier) error

// InTx 在一个事务中执行 fn，失败即回滚。
//
// 用途：需要原子性的多步写入，例如"创建工作组 + 把所有者写入 group_members"
// （docs/02 §3.2.1 保障 1：单一写路径）。
func InTx(ctx context.Context, pool *pgxpool.Pool, fn TxFunc) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("开启事务失败: %w", err)
	}
	// 回滚在已提交后是空操作，因此无需区分提交与否。
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("提交事务失败: %w", err)
	}
	return nil
}

// ConstraintError 描述某个数据库约束对应的领域错误。
type ConstraintError struct {
	Code    domain.ErrorCode
	Message string
	// Field 与 Detail 用于填充错误信封的 fields，可选。
	Field  string
	Detail string
}

// ToDomain 把 ConstraintError 转换为领域错误。
func (c ConstraintError) ToDomain(cause error) *domain.Error {
	err := domain.New(c.Code, c.Message).WithCause(cause)
	if c.Field != "" {
		err = err.WithField(c.Field, c.Detail)
	}
	return err
}

// Translate 把数据库错误按约束名翻译为领域错误。
//
// mappings 的键是约束名（唯一约束、外键、CHECK 名均可）。
// 未登记的约束按 SQLSTATE 兜底：唯一冲突 → 409 conflict；CHECK/非空 → 422 validation_error；
// 外键冲突 → 409 conflict。这样"漏登记"不会退化为 500，只会在错误码上不够精确。
func Translate(err error, mappings map[string]ConstraintError) error {
	if err == nil {
		return nil
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return fmt.Errorf("数据库操作失败: %w", err)
	}

	if mapped, ok := mappings[pgErr.ConstraintName]; ok {
		return mapped.ToDomain(err)
	}

	switch pgErr.Code {
	case sqlStateUniqueViolation:
		return domain.ErrConflict.WithCause(
			fmt.Errorf("唯一约束冲突 %s: %w", pgErr.ConstraintName, err))
	case sqlStateCheckViolation:
		return domain.ErrValidation.WithCause(
			fmt.Errorf("字段取值不满足约束 %s: %w", pgErr.ConstraintName, err))
	case sqlStateNotNullViolation:
		return domain.ErrValidation.WithCause(
			fmt.Errorf("必填字段缺失 %s: %w", pgErr.ConstraintName, err))
	case sqlStateForeignKeyViolation:
		return domain.ErrConflict.WithCause(
			fmt.Errorf("外键约束冲突 %s: %w", pgErr.ConstraintName, err))
	default:
		return fmt.Errorf("数据库操作失败: %w", err)
	}
}

// TranslateWrite 是 Translate 的便捷形式，用于没有约束映射表的简单写入。
func TranslateWrite(err error) error { return Translate(err, nil) }

// IsNoRows 报告错误是否为 pgx 的"无结果行"。
func IsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// IsNotFound 报告错误是否为"对象不存在"语义。
func IsNotFound(err error) bool {
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		return false
	}
	switch domainErr.Code {
	case domain.CodeResourceNotFound, domain.CodeUserNotFound,
		domain.CodeGroupNotFound, domain.CodeProjectNotFound, domain.CodeTaskNotFound:
		return true
	default:
		return false
	}
}
