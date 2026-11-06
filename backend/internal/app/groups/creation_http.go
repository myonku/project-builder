package groups

import (
	"github.com/gin-gonic/gin"
	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/httpapi"
	"github.com/myonku/project-builder/internal/listquery"
	"github.com/myonku/project-builder/internal/platform/idgen"
)

func (h *Handler) registerCreationRequests(api *gin.RouterGroup) {
	api.POST("/group-creation-requests", h.submitCreation)
	api.POST("/me/group-creation-requests/query", h.myCreations)
	api.POST("/group-creation-requests/query", h.allCreations)
	api.GET("/group-creation-requests/:request_id", h.creationDetail)
	api.POST("/group-creation-requests/:request_id/review", h.reviewCreation)
}

type submitCreationRequest struct {
	Name        string  `json:"name" validate:"required"`
	Description *string `json:"description"`
	Reason      string  `json:"reason" validate:"required"`
}
type myCreationQuery struct {
	Status *string `json:"status"`
	listquery.PageRequest
	listquery.SortRequest
}
type creationQuery struct {
	myCreationQuery
	Q           string  `json:"q"`
	ApplicantID *string `json:"applicant_id"`
}
type reviewCreationRequest struct {
	Status          string             `json:"status" validate:"required"`
	RejectionReason *string            `json:"rejection_reason"`
	ExpectedUpdated *httpapi.Timestamp `json:"expected_updated_at" validate:"required"`
}

func creationResponse(r CreationRequestRecord, admin bool) gin.H {
	v := gin.H{"id": r.ID, "name": r.Name, "description": r.Description, "reason": r.Reason, "status": r.Status,
		"created_at": domain.FormatTimestamp(r.CreatedAt), "updated_at": domain.FormatTimestamp(r.UpdatedAt), "reviewed_at": r.ReviewedAt,
		"rejection_reason": r.RejectionReason, "group_id": r.GroupID, "group_name": r.GroupName, "group_status": r.GroupStatus, "can_access_group": r.CanAccessGroup}
	if admin {
		v["applicant_id"] = r.ApplicantID
		v["username"] = r.Username
		v["display_name"] = r.DisplayName
		v["applicant_status"] = r.ApplicantStatus
		v["reviewed_by"] = r.ReviewedBy
	}
	return v
}
func (h *Handler) submitCreation(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	var req submitCreationRequest
	if err := httpapi.BindJSON(c, &req); err != nil {
		httpapi.Abort(c, err)
		return
	}
	result, replay, err := h.service.SubmitCreation(c.Request.Context(), actor, SubmitCreationInput{Name: req.Name, Description: req.Description, Reason: req.Reason, Key: c.GetHeader("Idempotency-Key")})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	if replay {
		httpapi.RespondOK(c, creationResponse(result, false))
	} else {
		httpapi.RespondCreated(c, creationResponse(result, false))
	}
}
func (h *Handler) myCreations(c *gin.Context)  { h.queryCreations(c, true) }
func (h *Handler) allCreations(c *gin.Context) { h.queryCreations(c, false) }
func (h *Handler) queryCreations(c *gin.Context, mine bool) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	if !mine && !actor.IsAdmin() {
		httpapi.Abort(c, domain.ErrPermissionDenied)
		return
	}
	var req creationQuery
	if mine {
		if err := httpapi.BindJSON(c, &req.myCreationQuery); err != nil {
			httpapi.Abort(c, err)
			return
		}
	} else {
		if err := httpapi.BindJSON(c, &req); err != nil {
			httpapi.Abort(c, err)
			return
		}
		if req.ApplicantID != nil && !idgen.IsUUID(*req.ApplicantID) {
			httpapi.Abort(c, domain.ErrInvalidRequest.WithField("applicant_id", "必须是合法 UUID"))
			return
		}
	}
	result, err := h.service.QueryCreations(c.Request.Context(), actor, QueryCreationInput{CreationFilter: CreationFilter{ApplicantID: req.ApplicantID, Status: req.Status, Q: req.Q}, Page: req.PageRequest, Sort: req.Sort}, mine)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	items := make([]gin.H, 0, len(result.Items))
	for _, r := range result.Items {
		items = append(items, creationResponse(r, !mine))
	}
	httpapi.RespondList(c, items, httpapi.NewPagination(result.Page.Page, result.Page.PageSize, result.Total))
}
func (h *Handler) creationDetail(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	id, err := requireUUIDParam(c, "request_id")
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	result, err := h.service.CreationDetail(c.Request.Context(), actor, id)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	httpapi.RespondOK(c, creationResponse(result, actor.IsAdmin()))
}
func (h *Handler) reviewCreation(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	if !actor.IsAdmin() {
		httpapi.Abort(c, domain.ErrPermissionDenied)
		return
	}
	id, err := requireUUIDParam(c, "request_id")
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	var req reviewCreationRequest
	if err := httpapi.BindJSON(c, &req); err != nil {
		httpapi.Abort(c, err)
		return
	}
	result, err := h.service.ReviewCreation(c.Request.Context(), actor, ReviewCreationInput{ID: id, Status: req.Status, RejectionReason: req.RejectionReason, ExpectedUpdated: req.ExpectedUpdated.Time})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	httpapi.RespondOK(c, creationResponse(result, true))
}
