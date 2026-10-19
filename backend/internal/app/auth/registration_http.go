package auth

import (
	"github.com/gin-gonic/gin"

	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/httpapi"
	"github.com/myonku/project-builder/internal/listquery"
)

// registrationRequestResponse 是对外的申请结构。
//
// 刻意不包含 password_hash：密码摘要只存在于仓储记录中，HTTP 层无法通过
// "忘了排除"把它带出去。
type registrationRequestResponse struct {
	ID              string  `json:"id"`
	Username        string  `json:"username"`
	Email           string  `json:"email"`
	DisplayName     string  `json:"display_name"`
	Timezone        string  `json:"timezone"`
	Status          string  `json:"status"`
	ReviewedBy      *string `json:"reviewed_by"`
	ReviewedAt      *string `json:"reviewed_at"`
	RejectionReason *string `json:"rejection_reason"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
}

func newRegistrationRequestResponse(record RegistrationRequestRecord) registrationRequestResponse {
	var reviewedAt *string
	if record.ReviewedAt != nil {
		formatted := domain.FormatTimestamp(*record.ReviewedAt)
		reviewedAt = &formatted
	}
	return registrationRequestResponse{
		ID:              record.ID,
		Username:        record.Username,
		Email:           record.Email,
		DisplayName:     record.DisplayName,
		Timezone:        record.Timezone,
		Status:          record.Status,
		ReviewedBy:      record.ReviewedBy,
		ReviewedAt:      reviewedAt,
		RejectionReason: record.RejectionReason,
		CreatedAt:       domain.FormatTimestamp(record.CreatedAt),
		UpdatedAt:       domain.FormatTimestamp(record.UpdatedAt),
	}
}

// registrationListRequest 对应 POST /registration-requests/query。
type registrationListRequest struct {
	Status *string `json:"status"`
	listquery.PageRequest
	listquery.SortRequest
}

// registrationReviewRequest 对应 POST /registration-requests/{id}/review。
type registrationReviewRequest struct {
	Status string  `json:"status" validate:"required"`
	Reason *string `json:"reason"`
}

// registrationReviewResponse 是审核结果：通过时 user 非空。
type registrationReviewResponse struct {
	Request registrationRequestResponse `json:"request"`
	User    *PublicUser                 `json:"user,omitempty"`
}

// registerRegistrationRoutes 由 auth.Handler.Register 调用。
func (h *Handler) registerRegistrationRoutes(api *gin.RouterGroup) {
	group := api.Group("/registration-requests")
	group.POST("/query", h.listRegistrationRequests)
	group.POST("/:request_id/review", h.reviewRegistrationRequest)
}

// listRegistrationRequests 处理 POST /registration-requests/query。
func (h *Handler) listRegistrationRequests(c *gin.Context) {
	actor, err := h.LoadActor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	var request registrationListRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}

	page, err := h.service.ListRegistrationRequests(c.Request.Context(), actor,
		ListRegistrationRequestsInput{
			Status: request.Status,
			Page:   request.PageRequest,
			Sort:   request.SortRequest,
		})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	items := make([]registrationRequestResponse, 0, len(page.Items))
	for _, record := range page.Items {
		items = append(items, newRegistrationRequestResponse(record))
	}
	httpapi.RespondList(c, items,
		httpapi.NewPagination(page.Page.Page, page.Page.PageSize, page.Total))
}

// reviewRegistrationRequest 处理 POST /registration-requests/{id}/review。
func (h *Handler) reviewRegistrationRequest(c *gin.Context) {
	actor, err := h.LoadActor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	var request registrationReviewRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}

	result, err := h.service.ReviewRegistrationRequest(c.Request.Context(), actor,
		ReviewRegistrationRequestInput{
			RequestID: c.Param("request_id"),
			Decision:  request.Status,
			Reason:    request.Reason,
		})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	response := registrationReviewResponse{
		Request: newRegistrationRequestResponse(result.Request),
	}
	if result.User != nil {
		user := NewPublicUser(*result.User)
		response.User = &user
	}
	httpapi.RespondOK(c, response)
}
