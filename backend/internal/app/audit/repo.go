package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
)

// Record 是一条操作记录（仓储出参 / 列表项）。
type Record struct {
	ID         string
	ActorID    string
	ActorName  string
	Action     string
	ObjectType string
	ObjectID   string
	Changes    map[string]string
	RequestID  string
	CreatedAt  time.Time
	// ActorAvatarUpdatedAt 是操作人**当前**头像版本，用于前端展示操作人头像。
	ActorAvatarUpdatedAt *time.Time
}

// ListFilter 是列表筛选条件。nil 表示不过滤。
//
// 计数与列表**共用**同一套条件（appendFilters），否则会出现
// pagination.total 与实际条目数不符——第二阶段实测过这个缺陷。
type ListFilter struct {
	ObjectType *string
	ObjectID   *string
	ActorID    *string
	Action     *string
	// From/To 是闭区间：created_at >= From 且 created_at <= To。
	From *time.Time
	To   *time.Time
}

// Repository 是审计的 PostgreSQL 实现。
type Repository struct {
	pool *pgxpool.Pool
}

// NewRepository 构造仓储。
func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

// Pool 暴露连接池，供用例层开启事务（多步原子写入）。
func (r *Repository) Pool() *pgxpool.Pool { return r.pool }

// recordColumns 是列表查询用到的列（含操作人显示名）。
const recordColumns = `
	a.id, a.actor_id, u.display_name, a.action, a.object_type,
	a.object_id, a.changes, a.request_id, a.created_at, u.avatar_updated_at`

// constraintMap 把数据库约束翻译为领域错误。
//
// 领域层已经校验过枚举与 UUID，因此这里命中约束说明是**程序缺陷**
// （例如有人加了新动作却忘了登记 CHECK），翻译成 422 而不是 500 只是为了
// 让日志更容易定位；真正拦住它的是 TestAuditEnumWhitelistMatchesDatabaseCheck。
var constraintMap = map[string]apperr.ConstraintError{
	"ck_activity_logs_action": {
		Code: domain.CodeValidationError, Message: "审计动作不在白名单内",
		Field: "action", Detail: "动作取值与数据库 CHECK 约束不一致",
	},
	"ck_activity_logs_object_type": {
		Code: domain.CodeValidationError, Message: "审计对象类型不在白名单内",
		Field: "object_type", Detail: "对象类型与数据库 CHECK 约束不一致",
	},
	"activity_logs_actor_id_fkey": {
		Code: domain.CodeValidationError, Message: "审计记录的操作人不存在",
		Field: "actor_id", Detail: "actor_id 在 users 表中不存在",
	},
}

// Insert 在给定执行器上写入一条记录。
//
// q 由调用方提供：业务写路径传的是**当前事务**，从而保证
// "业务成功 ⇒ 审计存在"（《A09》）。
func (r *Repository) Insert(ctx context.Context, q apperr.Querier, record Record) error {
	changes, err := json.Marshal(record.Changes)
	if err != nil {
		return fmt.Errorf("序列化变更摘要失败: %w", err)
	}

	const query = `
		INSERT INTO activity_logs
			(id, actor_id, action, object_type, object_id, changes, request_id)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7)`

	// 显式转换成 jsonb：让 PostgreSQL 自己校验 JSON 合法性，
	// 而不是依赖驱动的类型推断。
	if _, err := q.Exec(ctx, query,
		record.ID, record.ActorID, record.Action, record.ObjectType,
		record.ObjectID, string(changes), record.RequestID,
	); err != nil {
		return apperr.Translate(err, constraintMap)
	}
	return nil
}

// Count 统计满足条件的记录数量。
func (r *Repository) Count(ctx context.Context, filter ListFilter) (int, error) {
	query := `SELECT count(*) FROM activity_logs a WHERE 1 = 1`
	args := []any{}
	query, args = appendFilters(query, args, filter)

	var total int
	if err := r.pool.QueryRow(ctx, query, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("统计操作记录失败: %w", err)
	}
	return total, nil
}

// List 分页查询记录。
func (r *Repository) List(
	ctx context.Context, filter ListFilter, orderBy string, limit, offset int,
) ([]Record, error) {
	query := `SELECT ` + recordColumns + `
		  FROM activity_logs a
		  JOIN users u ON u.id = a.actor_id
		 WHERE 1 = 1`
	args := []any{}
	query, args = appendFilters(query, args, filter)

	args = append(args, limit, offset)
	query += fmt.Sprintf(" ORDER BY %s LIMIT $%d OFFSET $%d", orderBy, len(args)-1, len(args))

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询操作记录失败: %w", err)
	}
	defer rows.Close()

	var records []Record
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("读取操作记录行失败: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历操作记录失败: %w", err)
	}
	return records, nil
}

// scanRecord 读取一行记录；changes 从 JSONB 反序列化为扁平对象。
func scanRecord(row pgx.Row) (Record, error) {
	var (
		record  Record
		changes []byte
	)
	if err := row.Scan(
		&record.ID, &record.ActorID, &record.ActorName, &record.Action,
		&record.ObjectType, &record.ObjectID, &changes, &record.RequestID,
		&record.CreatedAt, &record.ActorAvatarUpdatedAt,
	); err != nil {
		return Record{}, err
	}
	if len(changes) > 0 {
		if err := json.Unmarshal(changes, &record.Changes); err != nil {
			return Record{}, fmt.Errorf("解析变更摘要失败: %w", err)
		}
	}
	if record.Changes == nil {
		record.Changes = map[string]string{}
	}
	return record, nil
}

// appendFilters 追加筛选条件，由计数与列表共用（避免二者漂移）。
func appendFilters(query string, args []any, filter ListFilter) (string, []any) {
	if filter.ObjectType != nil {
		args = append(args, *filter.ObjectType)
		query += fmt.Sprintf(` AND a.object_type = $%d`, len(args))
	}
	if filter.ObjectID != nil {
		args = append(args, *filter.ObjectID)
		query += fmt.Sprintf(` AND a.object_id = $%d`, len(args))
	}
	if filter.ActorID != nil {
		args = append(args, *filter.ActorID)
		query += fmt.Sprintf(` AND a.actor_id = $%d`, len(args))
	}
	if filter.Action != nil {
		args = append(args, *filter.Action)
		query += fmt.Sprintf(` AND a.action = $%d`, len(args))
	}
	if filter.From != nil {
		args = append(args, *filter.From)
		query += fmt.Sprintf(` AND a.created_at >= $%d`, len(args))
	}
	if filter.To != nil {
		args = append(args, *filter.To)
		query += fmt.Sprintf(` AND a.created_at <= $%d`, len(args))
	}
	return query, args
}
