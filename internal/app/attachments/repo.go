// Package attachments 实现项目/任务附件（接口契约 4.8）。
//
// 模块内文件分工：repo.go（SQL）、service.go（用例与权限编排）、http.go（multipart 与路由）。
//
// 两条边界值得先说明：
//
//  1. **对象存储只按 key 读写字节**（internal/platform/storage）。本模块负责生成 key、
//     记录元数据、判定权限；存储层不知道附件、项目、任务的存在。
//     这样将来换对象存储或 MongoDB 实现时，数据库里的 stored_key 可以一字不改地沿用。
//  2. **附件没有独立权限**，完全继承所在项目/任务：可读性、可写性、归档只读
//     都通过 domain 的权限函数判定，不在这里另写一套。
package attachments

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
)

// attachmentColumns 是 attachments 表的显式列清单。
//
// 含 stored_key：仓储需要它来操作对象存储，但**对外结构不暴露它**
// （存储布局属于实现细节，客户端用 id 下载）。
const attachmentColumns = `a.id, a.project_id, a.task_id, a.uploader_id,
	a.original_name, a.stored_key, a.extension, a.size_bytes, a.mime_type,
	a.deleted_at, a.created_at, a.updated_at`

// attachmentReturningColumns 与 attachmentColumns 相同，但不带表别名
// （INSERT/UPDATE 的 RETURNING 没有 FROM 别名）。
const attachmentReturningColumns = `id, project_id, task_id, uploader_id,
	original_name, stored_key, extension, size_bytes, mime_type,
	deleted_at, created_at, updated_at`

// Record 是 attachments 表的一行。
type Record struct {
	ID           string
	ProjectID    *string
	TaskID       *string
	UploaderID   string
	OriginalName string
	StoredKey    string
	Extension    string
	SizeBytes    int64
	MimeType     string
	DeletedAt    *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// IsDeleted 报告附件是否已被逻辑删除。
func (r Record) IsDeleted() bool { return r.DeletedAt != nil }

// CreateParams 是插入附件所需的字段。
type CreateParams struct {
	ID           string
	ProjectID    *string
	TaskID       *string
	UploaderID   string
	OriginalName string
	StoredKey    string
	Extension    string
	SizeBytes    int64
	MimeType     string
}

// ListFilter 是附件列表的筛选条件。
type ListFilter struct {
	// ProjectID 与 TaskID 恰有一个非空（与数据库的 CHECK 约束一致）。
	ProjectID *string
	TaskID    *string

	// ActorID 用于判断"已逻辑删除的附件里，哪些行对他可见"。
	ActorID string
	// IncludeDeleted 为真时把已逻辑删除的附件纳入候选集；
	// 具体某一行是否可见仍由下面的布尔量与 uploader_id 决定。
	IncludeDeleted bool
	// CanSeeAnyDeleted 为真表示调用者能看**所有**已删除附件
	// （容器持有者或系统管理员）。
	CanSeeAnyDeleted bool
	// DeletedOnly 为真时只返回已逻辑删除的附件（"回收站"视角）。
	DeletedOnly bool
}

// Repository 是本模块的 SQL 访问层。
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository 构造仓储。
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Create 插入附件并回读数据库生成的列。
func (r *Repository) Create(ctx context.Context, params CreateParams) (Record, error) {
	const query = `
		INSERT INTO attachments (
			id, project_id, task_id, uploader_id,
			original_name, stored_key, extension, size_bytes, mime_type
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING ` + attachmentReturningColumns

	record, err := scan(r.pool.QueryRow(ctx, query,
		params.ID, params.ProjectID, params.TaskID, params.UploaderID,
		params.OriginalName, params.StoredKey, params.Extension,
		params.SizeBytes, params.MimeType,
	))
	if err != nil {
		return Record{}, apperr.Translate(err, constraintMap)
	}
	return record, nil
}

// Find 按主键查询附件（**含已逻辑删除的行**）。
//
// 是否允许调用者看到它由用例层决定：仓储只负责取行，
// 权限判定不在这一层（见 docs/01 4.4 的分层约定）。
func (r *Repository) Find(ctx context.Context, attachmentID string) (Record, error) {
	const query = `SELECT ` + attachmentColumns + ` FROM attachments a WHERE a.id = $1`

	record, err := scan(r.pool.QueryRow(ctx, query, attachmentID))
	if err != nil {
		if apperr.IsNoRows(err) {
			return Record{}, notFound()
		}
		return Record{}, fmt.Errorf("查询附件失败: %w", err)
	}
	return record, nil
}

// CountByUploader 统计该用户关联的附件数量（**含已逻辑删除的**）。
//
// 为什么把已删除的也算进配额：它们仍然占用磁盘，彻底删除才是回收点。
// 若不计数，用户可以通过"上传 + 逻辑删除"无限占用存储，
// 而配额的意义正是限制占用。
func (r *Repository) CountByUploader(ctx context.Context, uploaderID string) (int, error) {
	const query = `SELECT count(*) FROM attachments WHERE uploader_id = $1`

	var total int
	if err := r.pool.QueryRow(ctx, query, uploaderID).Scan(&total); err != nil {
		return 0, fmt.Errorf("统计附件数量失败: %w", err)
	}
	return total, nil
}

// appendContainerScope 追加"归属哪个项目/任务"的条件。
func appendContainerScope(query string, args []any, filter ListFilter) (string, []any) {
	if filter.ProjectID != nil {
		args = append(args, *filter.ProjectID)
		query += fmt.Sprintf(` AND a.project_id = $%d`, len(args))
	}
	if filter.TaskID != nil {
		args = append(args, *filter.TaskID)
		query += fmt.Sprintf(` AND a.task_id = $%d`, len(args))
	}
	return query, args
}

// appendDeletedScope 追加"逻辑删除可见性"条件，**由计数与列表共用**。
//
// 两种视角：
//   - 默认：只看未删除的（deleted_at IS NULL）；
//   - DeletedOnly：只看已删除的，且只包含调用者有权看到的那部分
//     （上传者本人、容器持有者、系统管理员）。
//
// 计数与列表必须共用它，否则会出现"total 说 5 条、列表只给 2 条"的不一致
// ——第二阶段实测过这一类缺陷。
func appendDeletedScope(query string, args []any, filter ListFilter) (string, []any) {
	if filter.DeletedOnly {
		args = append(args, filter.ActorID)
		uploaderParam := len(args)
		args = append(args, filter.CanSeeAnyDeleted)
		anyParam := len(args)
		return query + fmt.Sprintf(
			` AND a.deleted_at IS NOT NULL AND (a.uploader_id = $%d OR $%d)`,
			uploaderParam, anyParam), args
	}
	if !filter.IncludeDeleted {
		return query + ` AND a.deleted_at IS NULL`, args
	}

	args = append(args, filter.ActorID)
	uploaderParam := len(args)
	args = append(args, filter.CanSeeAnyDeleted)
	anyParam := len(args)
	return query + fmt.Sprintf(
		` AND (a.deleted_at IS NULL OR a.uploader_id = $%d OR $%d)`,
		uploaderParam, anyParam), args
}

// Count 统计满足条件的附件数量。
func (r *Repository) Count(ctx context.Context, filter ListFilter) (int, error) {
	query := `SELECT count(*) FROM attachments a WHERE 1 = 1`
	args := []any{}

	query, args = appendContainerScope(query, args, filter)
	query, args = appendDeletedScope(query, args, filter)

	var total int
	if err := r.pool.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("统计附件失败: %w", err)
	}
	return total, nil
}

// List 分页查询附件。
func (r *Repository) List(
	ctx context.Context, filter ListFilter, orderBy string, limit, offset int,
) ([]Record, error) {
	query := `SELECT ` + attachmentColumns + ` FROM attachments a WHERE 1 = 1`
	args := []any{}

	query, args = appendContainerScope(query, args, filter)
	query, args = appendDeletedScope(query, args, filter)

	args = append(args, limit, offset)
	query += fmt.Sprintf(" ORDER BY %s LIMIT $%d OFFSET $%d", orderBy, len(args)-1, len(args))

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询附件列表失败: %w", err)
	}
	defer rows.Close()

	var records []Record
	for rows.Next() {
		record, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("读取附件行失败: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历附件失败: %w", err)
	}
	return records, nil
}

// SoftDelete 逻辑删除附件（幂等地拒绝重复删除）。
//
// 条件 `deleted_at IS NULL` 让"重复删除"落到 0 行，从而能被区分出来，
// 而不是把一次无变化的请求报成成功。
func (r *Repository) SoftDelete(ctx context.Context, attachmentID string, at time.Time) (Record, error) {
	const query = `
		UPDATE attachments
		   SET deleted_at = $2, updated_at = now()
		 WHERE id = $1 AND deleted_at IS NULL
		RETURNING ` + attachmentReturningColumns

	record, err := scan(r.pool.QueryRow(ctx, query, attachmentID, at))
	if err != nil {
		if apperr.IsNoRows(err) {
			return Record{}, r.distinguishMissing(ctx, attachmentID)
		}
		return Record{}, apperr.Translate(err, constraintMap)
	}
	return record, nil
}

// Purge 物理删除附件行。
//
// 只删行：对象存储里的字节由用例层负责清理，因为"先删字节还是先删行"
// 需要与存储的错误语义一起考虑（见 service.Purge 的说明）。
func (r *Repository) Purge(ctx context.Context, attachmentID string) error {
	const query = `DELETE FROM attachments WHERE id = $1`

	tag, err := r.pool.Exec(ctx, query, attachmentID)
	if err != nil {
		return apperr.Translate(err, constraintMap)
	}
	if tag.RowsAffected() == 0 {
		return notFound()
	}
	return nil
}

// distinguishMissing 区分"附件不存在"与"状态冲突"。
func (r *Repository) distinguishMissing(ctx context.Context, attachmentID string) error {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM attachments WHERE id = $1)`, attachmentID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("查询附件存在性失败: %w", err)
	}
	if !exists {
		return notFound()
	}
	return domain.ErrConflict.WithField("attachment_id", "该附件已被逻辑删除")
}

// scan 读取一行附件。
func scan(row interface{ Scan(dest ...any) error }) (Record, error) {
	var record Record
	err := row.Scan(
		&record.ID, &record.ProjectID, &record.TaskID, &record.UploaderID,
		&record.OriginalName, &record.StoredKey, &record.Extension,
		&record.SizeBytes, &record.MimeType,
		&record.DeletedAt, &record.CreatedAt, &record.UpdatedAt,
	)
	if err != nil {
		return Record{}, err
	}
	return record, nil
}

// notFound 返回统一的"附件不存在"错误。
func notFound() error {
	return domain.New(domain.CodeAttachmentNotFound, "Attachment not found")
}

// constraintMap 把数据库约束翻译为领域错误。
var constraintMap = map[string]apperr.ConstraintError{
	"uq_attachments_stored_key": {
		Code: domain.CodeConflict, Message: "Attachment key conflict",
		Field: "attachment_id", Detail: "存储标识冲突，请重试",
	},
	"ck_attachments_single_container": {
		Code: domain.CodeValidationError, Message: "Attachment must belong to exactly one container",
		Field: "attachment_id", Detail: "附件必须且只能属于一个项目或一个任务",
	},
}
