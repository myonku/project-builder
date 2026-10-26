package groups

import (
	"github.com/gin-gonic/gin"
	"github.com/myonku/project-builder/internal/httpapi"
	"github.com/myonku/project-builder/internal/listquery"
)

type discoverRequest struct {
	Q string `json:"q"`
	listquery.PageRequest
	listquery.SortRequest
}

func (h *Handler) discover(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	var request discoverRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}
	page, err := h.service.Discover(c.Request.Context(), DiscoverInput{ActorID: actor.ID, Q: request.Q, Page: request.PageRequest, Sort: request.Sort})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	httpapi.RespondList(c, page.Items, httpapi.NewPagination(page.Page.Page, page.Page.PageSize, page.Total))
}
func (h *Handler) myJoinRequests(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	var request joinRequestQueryRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}
	page, err := h.service.MyJoinRequests(c.Request.Context(), MyJoinRequestsInput{ActorID: actor.ID, Status: request.Status, Page: request.PageRequest, Sort: request.Sort})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	httpapi.RespondList(c, page.Items, httpapi.NewPagination(page.Page.Page, page.Page.PageSize, page.Total))
}
