// Package audit 实现操作记录（审计）的写入与查询。
//
// 两个角色：
//
//  1. **写入方**：实现 apperr.AuditWriter，由各业务模块在**自己的事务里**调用
//     （docs/04 §4.10 的"同事务写入"）。业务成功 ⇒ 审计一定存在；
//     审计写入失败 ⇒ 整个业务操作回滚。
//  2. **查询方**：GET /api/v1/audit，只有系统管理员可访问。
//
// 为什么审计不做成"提交后异步写"：那样一旦写入失败，业务已经提交，
// 只能留下"操作发生过但记录缺失"且无法回滚的空洞；审计是需求文档 §5.8 的
// 交付要求，不是尽力而为的日志（对比提醒派发）。
package audit

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/listquery"
	"github.com/myonku/project-builder/internal/platform/idgen"
	"github.com/myonku/project-builder/internal/platform/requestid"
)

// Page 是列表查询结果。必须同时返回真实总数（接口契约 2.3 的 pagination.total）。
type Page[T any] struct {
	Items []T
	Total int
	Page  listquery.Pagination
}

// Store 是审计仓储的契约（由消费方定义，实现见 repo.go）。
type Store interface {
	// Insert 在给定执行器上写入一条记录：q 既可以是连接池，也可以是业务事务。
	Insert(ctx context.Context, q apperr.Querier, record Record) error
	Count(ctx context.Context, filter ListFilter) (int, error)
	List(ctx context.Context, filter ListFilter, orderBy string, limit, offset int) ([]Record, error)
}

// Service 承载审计的写入与查询用例。
type Service struct {
	repo   Store
	ids    *idgen.Generator
	logger *slog.Logger
}

// Options 是 Service 的构造参数。
type Options struct {
	Store  Store
	IDs    *idgen.Generator
	Logger *slog.Logger
}

// NewService 构造审计服务。
func NewService(opts Options) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: opts.Store, ids: opts.IDs, logger: logger}
}

// WriteAudit 实现 apperr.AuditWriter：在**调用方的事务**里写一条操作记录。
//
// 参数 q 必须是业务语句所在的那个事务执行器——这是"审计与业务同生共死"的
// 全部实现要点。传入连接池就等于回到"提交后写"，那是被刻意否决的做法
// （docs/05 §6 第 38 条）。
func (s *Service) WriteAudit(ctx context.Context, q apperr.Querier, entry domain.AuditEntry) error {
	record, err := s.buildRecord(ctx, entry)
	if err != nil {
		return err
	}
	if err := s.repo.Insert(ctx, q, record); err != nil {
		// 审计写不进去就不允许业务落库：这是刻意的失败模式，
		// 宁可让用户看到一次失败的操作，也不要留下无记录的状态变更。
		return fmt.Errorf("写入操作记录失败: %w", err)
	}
	return nil
}

// buildRecord 校验并归一化一条待写入的记录。
func (s *Service) buildRecord(ctx context.Context, entry domain.AuditEntry) (Record, error) {
	if err := domain.ValidateAuditEntry(entry); err != nil {
		return Record{}, err
	}
	if !idgen.IsUUID(entry.ActorID) {
		return Record{}, domain.ErrValidation.WithField("actor_id", "不是合法的 UUID")
	}
	if !idgen.IsUUID(entry.ObjectID) {
		return Record{}, domain.ErrValidation.WithField("object_id", "不是合法的 UUID")
	}
	if s.ids == nil {
		return Record{}, domain.ErrInternal.WithCause(fmt.Errorf("audit: 未注入 id 生成器"))
	}
	id, err := s.ids.UUID()
	if err != nil {
		return Record{}, domain.ErrInternal.WithCause(err)
	}

	// request id 从 context 取（由 RequestIDMiddleware 写入）；
	// 显式传入的值优先，便于后台任务与测试指定。
	requestID := entry.RequestID
	if requestID == "" {
		requestID = requestid.From(ctx)
	}

	return Record{
		ID:         id,
		ActorID:    entry.ActorID,
		Action:     string(entry.Action),
		ObjectType: string(entry.ObjectType),
		ObjectID:   entry.ObjectID,
		Changes:    domain.SanitizeAuditChanges(entry.Changes),
		RequestID:  requestID,
	}, nil
}

// ListInput 是审计列表的用例输入。
type ListInput struct {
	Actor domain.User
	// 全部筛选都是可选的；nil 表示不过滤。
	ObjectType *string
	ObjectID   *string
	ActorID    *string
	Action     *string
	From       *time.Time
	To         *time.Time
	Page       listquery.PageRequest
	Sort       string
}

// List 分页返回操作记录。
//
// 只有系统管理员可以查看（需求文档 §4.1"查看全局操作记录"；
// 人工决策 2026-10-12：组管理员不扩权）。
func (s *Service) List(ctx context.Context, in ListInput) (Page[Record], error) {
	if !domain.CanReadAuditLog(in.Actor) {
		return Page[Record]{}, domain.ErrPermissionDenied
	}

	pagination, err := in.Page.Pagination()
	if err != nil {
		return Page[Record]{}, err
	}
	order, err := AuditSort.Resolve(in.Sort)
	if err != nil {
		return Page[Record]{}, err
	}

	filter, err := buildFilter(in)
	if err != nil {
		return Page[Record]{}, err
	}

	total, err := s.repo.Count(ctx, filter)
	if err != nil {
		return Page[Record]{}, err
	}
	items, err := s.repo.List(ctx, filter, AuditSort.OrderBy(order),
		pagination.Limit(), pagination.Offset())
	if err != nil {
		return Page[Record]{}, err
	}
	return Page[Record]{Items: items, Total: total, Page: pagination}, nil
}

// buildFilter 校验并组装筛选条件。
//
// 枚举、UUID、时间范围都在这里拦下并给出字段级错误：让数据库去报 CHECK 失败
// 只会得到一个 500，而"未知动作"是客户端拼错了参数，应当明确告诉它。
func buildFilter(in ListInput) (ListFilter, error) {
	filter := ListFilter{
		ObjectType: in.ObjectType,
		ObjectID:   in.ObjectID,
		ActorID:    in.ActorID,
		Action:     in.Action,
		From:       in.From,
		To:         in.To,
	}

	if filter.ObjectType != nil && !domain.IsAuditObjectType(*filter.ObjectType) {
		return ListFilter{}, domain.ErrInvalidRequest.
			WithField("object_type", fmt.Sprintf("未知取值 %q", *filter.ObjectType))
	}
	if filter.Action != nil && !domain.IsAuditAction(*filter.Action) {
		return ListFilter{}, domain.ErrInvalidRequest.
			WithField("action", fmt.Sprintf("未知取值 %q", *filter.Action))
	}
	if filter.ObjectID != nil && !idgen.IsUUID(*filter.ObjectID) {
		return ListFilter{}, domain.ErrInvalidRequest.WithField("object_id", "不是合法的 UUID")
	}
	if filter.ActorID != nil && !idgen.IsUUID(*filter.ActorID) {
		return ListFilter{}, domain.ErrInvalidRequest.WithField("actor_id", "不是合法的 UUID")
	}
	if filter.From != nil && filter.To != nil && filter.To.Before(*filter.From) {
		return ListFilter{}, domain.ErrInvalidDateRange.
			WithField("to", "结束时间不得早于开始时间")
	}
	return filter, nil
}
