package groups

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/listquery"
)

// These projections deliberately exclude member, owner and applicant identities.
type DiscoverGroup struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	Description      *string `json:"description"`
	Status           string  `json:"status"`
	MembershipStatus *string `json:"membership_status"`
	RequestStatus    *string `json:"request_status"`
	CanReview        bool    `json:"can_review"`
}
type MyJoinRequest struct {
	ID               string     `json:"id"`
	GroupID          string     `json:"group_id"`
	GroupName        string     `json:"group_name"`
	GroupStatus      string     `json:"group_status"`
	MembershipStatus *string    `json:"membership_status"`
	Status           string     `json:"status"`
	ReviewedAt       *time.Time `json:"reviewed_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

var discoverSort = listquery.NewSortSpec([]listquery.SortField{{Name: "name", Column: "g.name"}, {Name: "created_at", Column: "g.created_at"}}, "name", false, "g.id")
var myJoinSort = listquery.NewSortSpec([]listquery.SortField{{Name: "created_at", Column: "r.created_at"}, {Name: "updated_at", Column: "r.updated_at"}, {Name: "status", Column: "r.status"}}, "updated_at", true, "r.id")

type DiscoverInput struct {
	ActorID string
	Q       string
	Page    listquery.PageRequest
	Sort    string
}
type MyJoinRequestsInput struct {
	ActorID string
	Status  *string
	Page    listquery.PageRequest
	Sort    string
}

func (s *Service) Discover(ctx context.Context, in DiscoverInput) (Page[DiscoverGroup], error) {
	p, err := in.Page.Pagination()
	if err != nil {
		return Page[DiscoverGroup]{}, err
	}
	q := strings.TrimSpace(in.Q)
	if utf8.RuneCountInString(q) > 160 {
		return Page[DiscoverGroup]{}, domain.ErrInvalidRequest.WithField("q", "搜索内容不超过 160 字符")
	}
	order, err := discoverSort.Resolve(in.Sort)
	if err != nil {
		return Page[DiscoverGroup]{}, err
	}
	total, err := s.repo.CountDiscoverGroups(ctx, in.ActorID, q)
	if err != nil {
		return Page[DiscoverGroup]{}, err
	}
	rows, err := s.repo.ListDiscoverGroups(ctx, in.ActorID, q, discoverSort.OrderBy(order), p.Limit(), p.Offset())
	if err != nil {
		return Page[DiscoverGroup]{}, err
	}
	return NewPage(rows, total, p), nil
}
func (s *Service) MyJoinRequests(ctx context.Context, in MyJoinRequestsInput) (Page[MyJoinRequest], error) {
	p, err := in.Page.Pagination()
	if err != nil {
		return Page[MyJoinRequest]{}, err
	}
	if in.Status != nil && *in.Status != "pending" && *in.Status != "approved" && *in.Status != "rejected" {
		return Page[MyJoinRequest]{}, domain.ErrInvalidRequest.WithField("status", "申请状态只能是 pending、approved 或 rejected")
	}
	order, err := myJoinSort.Resolve(in.Sort)
	if err != nil {
		return Page[MyJoinRequest]{}, err
	}
	total, err := s.repo.CountMyJoinRequests(ctx, in.ActorID, in.Status)
	if err != nil {
		return Page[MyJoinRequest]{}, err
	}
	rows, err := s.repo.ListMyJoinRequests(ctx, in.ActorID, in.Status, myJoinSort.OrderBy(order), p.Limit(), p.Offset())
	if err != nil {
		return Page[MyJoinRequest]{}, err
	}
	return NewPage(rows, total, p), nil
}
