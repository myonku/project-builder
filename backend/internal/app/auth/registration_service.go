package auth

import (
	"context"
	"log/slog"
	"strings"

	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/listquery"
)

// RegistrationStore 是本模块对注册申请持久化的需求。
//
// 与 UserRepository 分开：既有测试/替身只需要实现用户读写；
// 只有启用"注册审核"的装配才需要注入申请表实现。
type RegistrationStore interface {
	CreateRegistrationRequest(ctx context.Context, in CreateRegistrationRequestParams) (RegistrationRequestRecord, error)
	FindRegistrationRequest(ctx context.Context, id string) (RegistrationRequestRecord, error)
	PendingIdentityExists(ctx context.Context, identifier string) (bool, error)
	ListRegistrationRequests(ctx context.Context, status *string, orderBy string, limit, offset int) ([]RegistrationRequestRecord, error)
	CountRegistrationRequests(ctx context.Context, status *string) (int, error)
	ReviewRegistrationRequestInTx(ctx context.Context, in ReviewRegistrationRequestParams) (RegistrationRequestRecord, *UserRecord, error)
}

// RegistrationRequestPage 是注册申请列表结果。
type RegistrationRequestPage struct {
	Items []RegistrationRequestRecord
	Total int
	Page  listquery.Pagination
}

// RegistrationRequestSort 是审核列表的排序白名单。
var RegistrationRequestSort = listquery.NewSortSpec(
	[]listquery.SortField{
		{Name: "created_at", Column: "created_at"},
		{Name: "updated_at", Column: "updated_at"},
		{Name: "username", Column: "username"},
		{Name: "status", Column: "status"},
	},
	"created_at", true, "registration_requests.id",
)

// ListRegistrationRequestsInput 是审核列表输入。
type ListRegistrationRequestsInput struct {
	// Status 为 nil 时返回全部状态；非 nil 时必须是合法状态。
	Status *string
	Page   listquery.PageRequest
	Sort   listquery.SortRequest
}

// ListRegistrationRequests 返回注册申请列表（仅系统管理员）。
func (s *Service) ListRegistrationRequests(
	ctx context.Context, actor domain.User, in ListRegistrationRequestsInput,
) (RegistrationRequestPage, error) {
	if !domain.CanAdministerUsers(actor) {
		return RegistrationRequestPage{}, domain.ErrPermissionDenied
	}
	if s.registrations == nil {
		return RegistrationRequestPage{}, domain.ErrInternal.WithCause(errNoRegistrationStore)
	}
	if in.Status != nil {
		normalized := strings.TrimSpace(*in.Status)
		if !domain.IsRegistrationStatus(normalized) {
			return RegistrationRequestPage{}, domain.ErrInvalidRequest.
				WithField("status", "申请状态必须是 pending、approved 或 rejected")
		}
		in.Status = &normalized
	}

	pagination, err := in.Page.Pagination()
	if err != nil {
		return RegistrationRequestPage{}, err
	}
	order, err := RegistrationRequestSort.Resolve(in.Sort.Sort)
	if err != nil {
		return RegistrationRequestPage{}, err
	}

	items, err := s.registrations.ListRegistrationRequests(
		ctx, in.Status, RegistrationRequestSort.OrderBy(order),
		pagination.Limit(), pagination.Offset(),
	)
	if err != nil {
		return RegistrationRequestPage{}, err
	}
	total, err := s.registrations.CountRegistrationRequests(ctx, in.Status)
	if err != nil {
		return RegistrationRequestPage{}, err
	}
	return RegistrationRequestPage{Items: items, Total: total, Page: pagination}, nil
}

// ReviewRegistrationRequestInput 是审核输入。
type ReviewRegistrationRequestInput struct {
	RequestID string
	// Decision 只能是 approved 或 rejected；pending 不能作为审核结果。
	Decision string
	Reason   *string
}

// ReviewRegistrationRequestResult 是审核结果：通过时 User 非 nil。
type ReviewRegistrationRequestResult struct {
	Request RegistrationRequestRecord
	User    *UserRecord
}

// ReviewRegistrationRequest 审核一条注册申请（仅系统管理员）。
//
// 通过时在同一个事务里创建 active 用户；拒绝时只落申请状态。
// 审核通过会写一条 user/created 审计（actor 是管理员）；拒绝暂不写审计
// （activity_logs 目前没有 registration_request 对象类型）。
func (s *Service) ReviewRegistrationRequest(
	ctx context.Context, actor domain.User, in ReviewRegistrationRequestInput,
) (ReviewRegistrationRequestResult, error) {
	if !domain.CanAdministerUsers(actor) {
		return ReviewRegistrationRequestResult{}, domain.ErrPermissionDenied
	}
	if s.registrations == nil {
		return ReviewRegistrationRequestResult{}, domain.ErrInternal.WithCause(errNoRegistrationStore)
	}

	decision := strings.TrimSpace(in.Decision)
	switch decision {
	case domain.RegistrationStatusApproved, domain.RegistrationStatusRejected:
	default:
		return ReviewRegistrationRequestResult{}, domain.ErrInvalidRequest.
			WithField("status", "审核结果必须是 approved 或 rejected")
	}

	reason := ""
	if in.Reason != nil {
		reason = strings.TrimSpace(*in.Reason)
	}
	if len([]rune(reason)) > domain.RegistrationReviewReasonMaxLength {
		return ReviewRegistrationRequestResult{}, domain.ErrValidation.
			WithField("reason", "拒绝原因不得超过 2000 个字符")
	}
	var reasonPtr *string
	if decision == domain.RegistrationStatusRejected && reason != "" {
		reasonPtr = &reason
	}

	// 先读一次申请（只用于构造审计信息与快速失败）；真正的状态迁移在
	// ReviewRegistrationRequestInTx 里用 FOR UPDATE 重新校验。
	current, err := s.registrations.FindRegistrationRequest(ctx, in.RequestID)
	if err != nil {
		return ReviewRegistrationRequestResult{}, err
	}
	if current.Status != domain.RegistrationStatusPending {
		return ReviewRegistrationRequestResult{}, domain.ErrConflict.
			WithField("status", "申请已审核，不能重复审核")
	}

	var newUserID string
	var audit *apperr.AuditWrite
	if decision == domain.RegistrationStatusApproved {
		if s.ids == nil {
			return ReviewRegistrationRequestResult{}, domain.ErrInternal.
				WithCause(errNoIDGenerator)
		}
		newUserID, err = s.ids.UUID()
		if err != nil {
			return ReviewRegistrationRequestResult{}, domain.ErrInternal.WithCause(err)
		}
		audit = s.auditUserCreated(actor.ID, newUserID, current.Username, current.Email,
			map[string]string{"source": "registration_approved"})
	}

	request, user, err := s.registrations.ReviewRegistrationRequestInTx(ctx,
		ReviewRegistrationRequestParams{
			ID:         in.RequestID,
			ReviewerID: actor.ID,
			Decision:   decision,
			Reason:     reasonPtr,
			NewUserID:  newUserID,
			Audit:      audit,
		})
	if err != nil {
		return ReviewRegistrationRequestResult{}, err
	}

	s.logger.Info("注册申请已审核",
		slog.String("request_id", request.ID),
		slog.String("status", request.Status),
		slog.String("reviewer_id", actor.ID),
	)
	return ReviewRegistrationRequestResult{Request: request, User: user}, nil
}

// registerPending 创建一条待审核申请，而不是 users 行。
func (s *Service) registerPending(ctx context.Context, in domain.RegistrationInput) (RegisterResult, error) {
	if s.registrations == nil {
		return RegisterResult{}, domain.ErrInternal.WithCause(errNoRegistrationStore)
	}
	normalized, fields := domain.NormalizeRegistration(in, s.security.PasswordMinLength)
	if len(fields) > 0 {
		return RegisterResult{}, domain.ErrValidation.WithFields(fields)
	}
	if err := s.ensureIdentityAvailable(ctx, normalized.Username, normalized.Email); err != nil {
		return RegisterResult{}, err
	}
	for _, identifier := range []string{normalized.Username, normalized.Email} {
		exists, err := s.registrations.PendingIdentityExists(ctx, identifier)
		if err != nil {
			return RegisterResult{}, err
		}
		if exists {
			field := "username"
			if identifier == normalized.Email {
				field = "email"
			}
			return RegisterResult{}, domain.ErrConflict.
				WithField(field, "该用户名或邮箱已有待审核的注册申请")
		}
	}

	hash, err := s.hasher.Hash(normalized.Password)
	if err != nil {
		return RegisterResult{}, domain.ErrInternal.WithCause(err)
	}
	id, err := s.ids.UUID()
	if err != nil {
		return RegisterResult{}, domain.ErrInternal.WithCause(err)
	}
	request, err := s.registrations.CreateRegistrationRequest(ctx, CreateRegistrationRequestParams{
		ID:           id,
		Username:     normalized.Username,
		Email:        normalized.Email,
		DisplayName:  normalized.DisplayName,
		PasswordHash: hash,
		Timezone:     normalized.Timezone,
	})
	if err != nil {
		return RegisterResult{}, err
	}
	s.logger.Info("注册申请已提交",
		slog.String("request_id", request.ID),
		slog.String("username", request.Username),
	)
	return RegisterResult{Request: request, Pending: true}, nil
}

var (
	errNoRegistrationStore = errString("auth: 未注入注册申请仓储")
	errNoIDGenerator       = errString("auth: 未注入 ID 生成器")
)

type errString string

func (e errString) Error() string { return string(e) }
