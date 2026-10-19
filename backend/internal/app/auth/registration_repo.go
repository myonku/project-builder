package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
)

// registrationRequestColumns 是申请表列清单。
//
// password_hash 必须包含在这里（审核通过时要把它原样写入 users），
// 但 HTTP 层的投影**绝不包含它**，见 registration_http.go 的 response 结构。
const registrationRequestColumns = `id, username, email, display_name, password_hash,
	timezone, status, reviewed_by, reviewed_at, rejection_reason, created_at, updated_at`

// RegistrationRequestRecord 是 registration_requests 表的行记录。
type RegistrationRequestRecord struct {
	ID              string
	Username        string
	Email           string
	DisplayName     string
	PasswordHash    string
	Timezone        string
	Status          string
	ReviewedBy      *string
	ReviewedAt      *time.Time
	RejectionReason *string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// CreateRegistrationRequestParams 是写入一条待审核申请的字段。
type CreateRegistrationRequestParams struct {
	ID           string
	Username     string
	Email        string
	DisplayName  string
	PasswordHash string
	Timezone     string
}

// registrationRequestConstraintMap 把申请表的唯一约束翻译为 409。
var registrationRequestConstraintMap = map[string]apperr.ConstraintError{
	"uq_registration_requests_username_lower": {
		Code: domain.CodeConflict, Message: "A registration request already exists",
		Field: "username", Detail: "该用户名已有待审核的注册申请",
	},
	"uq_registration_requests_email_lower": {
		Code: domain.CodeConflict, Message: "A registration request already exists",
		Field: "email", Detail: "该邮箱已有待审核的注册申请",
	},
}

// CreateRegistrationRequest 写入一条 pending 申请。
//
// 这里不写审计：提交申请的是未登录的匿名访客，而 activity_logs.actor_id
// 必须指向一个真实用户。审核通过时才会以管理员为 actor 写 user/created。
func (r *Repository) CreateRegistrationRequest(
	ctx context.Context, in CreateRegistrationRequestParams,
) (RegistrationRequestRecord, error) {
	const query = `
		INSERT INTO registration_requests (
			id, username, email, display_name, password_hash, timezone, status
		) VALUES ($1, $2, $3, $4, $5, $6, 'pending')
		RETURNING ` + registrationRequestColumns

	record, err := scanRegistrationRequest(r.pool.QueryRow(ctx, query,
		in.ID, in.Username, in.Email, in.DisplayName, in.PasswordHash, in.Timezone,
	))
	if err != nil {
		return RegistrationRequestRecord{}, apperr.Translate(err, registrationRequestConstraintMap)
	}
	return record, nil
}

// FindRegistrationRequest 按 id 查询申请（管理员审核页使用）。
func (r *Repository) FindRegistrationRequest(
	ctx context.Context, id string,
) (RegistrationRequestRecord, error) {
	const query = `SELECT ` + registrationRequestColumns + ` FROM registration_requests WHERE id = $1`
	record, err := scanRegistrationRequest(r.pool.QueryRow(ctx, query, id))
	if err != nil {
		return RegistrationRequestRecord{}, translateRegistrationRequestNotFound(err)
	}
	return record, nil
}

// PendingIdentityExists 报告用户名或邮箱是否已被一条 pending 申请占用。
func (r *Repository) PendingIdentityExists(ctx context.Context, identifier string) (bool, error) {
	const query = `
		SELECT 1 FROM registration_requests
		 WHERE status = 'pending'
		   AND (lower(username) = lower($1) OR lower(email) = lower($1))
		 LIMIT 1`
	var one int
	err := r.pool.QueryRow(ctx, query, identifier).Scan(&one)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	default:
		return false, fmt.Errorf("查询待审核注册申请失败: %w", err)
	}
}

// ListRegistrationRequests 分页查询申请。
//
// filter.Status 为 nil 时返回全部状态；sort 由调用方先通过白名单校验。
func (r *Repository) ListRegistrationRequests(
	ctx context.Context, status *string, orderBy string, limit, offset int,
) ([]RegistrationRequestRecord, error) {
	query := `
		SELECT ` + registrationRequestColumns + `
		  FROM registration_requests
		 WHERE ($1::text IS NULL OR status = $1)
		 ORDER BY ` + orderBy + `
		 LIMIT $2 OFFSET $3`

	rows, err := r.pool.Query(ctx, query, status, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("查询注册申请失败: %w", err)
	}
	defer rows.Close()

	records := make([]RegistrationRequestRecord, 0, limit)
	for rows.Next() {
		record, err := scanRegistrationRequest(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取注册申请失败: %w", err)
	}
	return records, nil
}

// CountRegistrationRequests 统计同一筛选条件下的申请数。
func (r *Repository) CountRegistrationRequests(ctx context.Context, status *string) (int, error) {
	const query = `SELECT count(*) FROM registration_requests WHERE ($1::text IS NULL OR status = $1)`
	var total int
	if err := r.pool.QueryRow(ctx, query, status).Scan(&total); err != nil {
		return 0, fmt.Errorf("统计注册申请失败: %w", err)
	}
	return total, nil
}

// ReviewRegistrationRequestParams 是审核申请的输入。
type ReviewRegistrationRequestParams struct {
	ID         string
	ReviewerID string
	Decision   string
	Reason     *string
	// NewUserID 只在 decision=approved 时使用。
	NewUserID string
	Audit     *apperr.AuditWrite
}

// ReviewRegistrationRequestInTx 审核申请；通过时在同一事务里创建 users 行。
//
// 并发语义：先 SELECT ... FOR UPDATE，再检查 status=pending；两个管理员同时
// 审核同一条申请时，第二个请求拿到的状态已不是 pending，返回 409 而不是重复建号。
func (r *Repository) ReviewRegistrationRequestInTx(
	ctx context.Context, in ReviewRegistrationRequestParams,
) (RegistrationRequestRecord, *UserRecord, error) {
	var request RegistrationRequestRecord
	var user *UserRecord

	err := apperr.InTx(ctx, r.pool, func(ctx context.Context, tx apperr.Querier) error {
		const selectQuery = `
			SELECT ` + registrationRequestColumns + `
			  FROM registration_requests
			 WHERE id = $1
			 FOR UPDATE`
		current, err := scanRegistrationRequest(tx.QueryRow(ctx, selectQuery, in.ID))
		if err != nil {
			return translateRegistrationRequestNotFound(err)
		}
		if current.Status != domain.RegistrationStatusPending {
			return domain.ErrConflict.WithField("status",
				fmt.Sprintf("申请已处于 %s 状态，不能重复审核", current.Status))
		}

		if in.Decision == domain.RegistrationStatusApproved {
			// 身份可能在这条申请等待审核期间被其他路径占用（例如管理员代建）。
			// 先查一次给出字段级 409；最终仍由 users 的唯一索引兜底。
			const identityQuery = `
				SELECT 1 FROM users
				 WHERE lower(username) = lower($1) OR lower(email) = lower($2)
				 LIMIT 1`
			var one int
			identityErr := tx.QueryRow(ctx, identityQuery, current.Username, current.Email).Scan(&one)
			switch {
			case identityErr == nil:
				return domain.ErrConflict.WithFields(map[string]string{
					"username": "该用户名已被占用",
					"email":    "该邮箱已被注册",
				})
			case !errors.Is(identityErr, pgx.ErrNoRows):
				return fmt.Errorf("检查注册身份是否可用失败: %w", identityErr)
			}

			const insertUser = `
				INSERT INTO users (
					id, username, email, display_name, password_hash,
					status, system_role, timezone, reminder_enabled
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
				RETURNING ` + userColumns
			created, err := scanUser(tx.QueryRow(ctx, insertUser,
				in.NewUserID, current.Username, current.Email, current.DisplayName,
				current.PasswordHash, domain.UserStatusActive, domain.UserSystemRoleMember,
				current.Timezone, true,
			))
			if err != nil {
				return translateUserWriteError(err)
			}
			user = &created
		}

		const updateQuery = `
			UPDATE registration_requests
			   SET status = $2,
			       reviewed_by = $3,
			       reviewed_at = now(),
			       rejection_reason = $4,
			       updated_at = now()
			 WHERE id = $1
			RETURNING ` + registrationRequestColumns
		updated, err := scanRegistrationRequest(
			tx.QueryRow(ctx, updateQuery, in.ID, in.Decision, in.ReviewerID, in.Reason),
		)
		if err != nil {
			return err
		}
		request = updated
		return in.Audit.Flush(ctx, tx)
	})
	if err != nil {
		return RegistrationRequestRecord{}, nil, err
	}
	return request, user, nil
}

// scanRegistrationRequest 扫描一行申请。
func scanRegistrationRequest(row pgx.Row) (RegistrationRequestRecord, error) {
	var record RegistrationRequestRecord
	err := row.Scan(
		&record.ID, &record.Username, &record.Email, &record.DisplayName,
		&record.PasswordHash, &record.Timezone, &record.Status,
		&record.ReviewedBy, &record.ReviewedAt, &record.RejectionReason,
		&record.CreatedAt, &record.UpdatedAt,
	)
	if err != nil {
		return RegistrationRequestRecord{}, err
	}
	return record, nil
}

// 编译期确认接口实现。
var _ RegistrationStore = (*Repository)(nil)

// translateRegistrationRequestNotFound 把 pgx 无行转换为领域错误。
func translateRegistrationRequestNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.New(domain.CodeRegistrationRequestNotFound, "Registration request not found")
	}
	return fmt.Errorf("查询注册申请失败: %w", err)
}
