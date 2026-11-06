package groups

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/listquery"
)

const creationScope = "group_creation_requests.create"

type CreationRequestRecord struct {
	ID, ApplicantID, Name, Reason, Status             string
	Description, ReviewedBy, RejectionReason, GroupID *string
	CreatedAt, UpdatedAt                              time.Time
	ReviewedAt                                        *time.Time
	Username, DisplayName, ApplicantStatus            string
	GroupName, GroupStatus                            *string
	CanAccessGroup                                    bool
}

type SubmitCreationInput struct {
	Name        string
	Description *string
	Reason, Key string
}
type SubmitCreationParams struct {
	SubmitCreationInput
	ID, KeyID, ActorID string
	Audit              *apperr.AuditWrite
}
type CreationFilter struct {
	ActorID     string
	ApplicantID *string
	Status      *string
	Q           string
}
type QueryCreationInput struct {
	CreationFilter
	Page listquery.PageRequest
	Sort string
}
type ReviewCreationInput struct {
	ID, Status      string
	RejectionReason *string
	ExpectedUpdated time.Time
}
type ReviewCreationParams struct {
	ReviewCreationInput
	ActorID, ApplicantID, GroupID, MemberID string
	Audit, GroupAudit                       *apperr.AuditWrite
}

var creationSort = listquery.NewSortSpec([]listquery.SortField{
	{Name: "created_at", Column: "r.created_at"}, {Name: "updated_at", Column: "r.updated_at"}, {Name: "status", Column: "r.status"},
}, "created_at", true, "r.id")

func (s *Service) creationAudit(actorID string, action domain.AuditAction, id string, changes map[string]string) *apperr.AuditWrite {
	return apperr.NewAuditWrite(s.audit, domain.AuditEntry{ActorID: actorID, Action: action, ObjectType: domain.AuditObjectGroupCreationRequest, ObjectID: id, Changes: changes})
}

func (s *Service) SubmitCreation(ctx context.Context, actor domain.User, in SubmitCreationInput) (CreationRequestRecord, bool, error) {
	if !actor.IsActive() {
		return CreationRequestRecord{}, false, domain.ErrPermissionDenied
	}
	normalized, fields := domain.NormalizeGroupCreate(domain.GroupMembershipInput{Name: in.Name, Description: in.Description})
	if fields == nil {
		fields = map[string]string{}
	}
	in.Reason = strings.TrimSpace(in.Reason)
	if n := utf8.RuneCountInString(in.Reason); n < 1 || n > 2000 {
		fields["reason"] = "申请理由须为 1–2000 个字符"
	}
	if n := utf8.RuneCountInString(in.Key); strings.TrimSpace(in.Key) == "" || n > 200 {
		fields["Idempotency-Key"] = "请提供不超过 200 字符的非空幂等键"
	}
	if len(fields) > 0 {
		return CreationRequestRecord{}, false, domain.ErrValidation.WithFields(fields)
	}
	in.Name, in.Description = normalized.Name, normalized.Description
	id, err := s.ids.UUID()
	if err != nil {
		return CreationRequestRecord{}, false, err
	}
	keyID, err := s.ids.UUID()
	if err != nil {
		return CreationRequestRecord{}, false, err
	}
	return s.repo.SubmitCreationRequest(ctx, SubmitCreationParams{SubmitCreationInput: in, ID: id, KeyID: keyID, ActorID: actor.ID,
		Audit: s.creationAudit(actor.ID, domain.AuditActionCreated, id, map[string]string{"name": in.Name, "applicant_id": actor.ID})})
}

func (s *Service) QueryCreations(ctx context.Context, actor domain.User, in QueryCreationInput, mine bool) (Page[CreationRequestRecord], error) {
	if !mine && !actor.IsAdmin() {
		return Page[CreationRequestRecord]{}, domain.ErrPermissionDenied
	}
	in.ActorID = actor.ID
	if mine {
		in.ApplicantID = &actor.ID
		in.Q = ""
	}
	in.Q = strings.TrimSpace(in.Q)
	if utf8.RuneCountInString(in.Q) > 160 {
		return Page[CreationRequestRecord]{}, domain.ErrInvalidRequest.WithField("q", "搜索内容不超过 160 字符")
	}
	if in.Status != nil && *in.Status != "pending" && *in.Status != "approved" && *in.Status != "rejected" {
		return Page[CreationRequestRecord]{}, domain.ErrInvalidRequest.WithField("status", "申请状态不合法")
	}
	pagination, err := in.Page.Pagination()
	if err != nil {
		return Page[CreationRequestRecord]{}, err
	}
	order, err := creationSort.Resolve(in.Sort)
	if err != nil {
		return Page[CreationRequestRecord]{}, err
	}
	rows, total, err := s.repo.QueryCreationRequests(ctx, in.CreationFilter, creationSort.OrderBy(order), pagination.Limit(), pagination.Offset())
	if err != nil {
		return Page[CreationRequestRecord]{}, err
	}
	return NewPage(rows, total, pagination), nil
}

func (s *Service) CreationDetail(ctx context.Context, actor domain.User, id string) (CreationRequestRecord, error) {
	rec, err := s.repo.FindCreationRequest(ctx, id, actor.ID)
	if err != nil {
		return CreationRequestRecord{}, err
	}
	if !actor.IsAdmin() && rec.ApplicantID != actor.ID {
		return CreationRequestRecord{}, domain.ErrResourceNotFound
	}
	return rec, nil
}

func (s *Service) ReviewCreation(ctx context.Context, actor domain.User, in ReviewCreationInput) (CreationRequestRecord, error) {
	if !actor.IsActive() || !actor.IsAdmin() {
		return CreationRequestRecord{}, domain.ErrPermissionDenied
	}
	fields := map[string]string{}
	reason := ""
	if in.RejectionReason != nil {
		reason = strings.TrimSpace(*in.RejectionReason)
	}
	if in.Status != "approved" && in.Status != "rejected" {
		fields["status"] = "审核结果只能是 approved 或 rejected"
	}
	if in.Status == "rejected" && (reason == "" || utf8.RuneCountInString(reason) > 2000) {
		fields["rejection_reason"] = "拒绝理由须为 1–2000 个字符"
	}
	if in.Status == "approved" && reason != "" {
		fields["rejection_reason"] = "通过时不能提供拒绝理由"
	}
	if in.ExpectedUpdated.IsZero() {
		fields["expected_updated_at"] = "请提供申请版本"
	}
	if len(fields) > 0 {
		return CreationRequestRecord{}, domain.ErrValidation.WithFields(fields)
	}
	if in.Status == "rejected" {
		in.RejectionReason = &reason
	} else {
		in.RejectionReason = nil
	}
	rec, err := s.CreationDetail(ctx, actor, in.ID)
	if err != nil {
		return CreationRequestRecord{}, err
	}
	groupID, err := s.ids.UUID()
	if err != nil {
		return CreationRequestRecord{}, err
	}
	memberID, err := s.ids.UUID()
	if err != nil {
		return CreationRequestRecord{}, err
	}
	return s.repo.ReviewCreationRequest(ctx, ReviewCreationParams{ReviewCreationInput: in, ActorID: actor.ID, ApplicantID: rec.ApplicantID, GroupID: groupID, MemberID: memberID,
		Audit:      s.creationAudit(actor.ID, domain.AuditActionGroupCreationReviewed, in.ID, map[string]string{"applicant_id": rec.ApplicantID, "decision": in.Status, "group_id": groupIDIfApproved(in.Status, groupID)}),
		GroupAudit: s.auditWith(actor.ID, domain.AuditActionCreated, groupID, map[string]string{"name": rec.Name, "applicant_id": rec.ApplicantID, "request_id": in.ID}),
	})
}
func groupIDIfApproved(status, id string) string {
	if status == "approved" {
		return id
	}
	return ""
}
