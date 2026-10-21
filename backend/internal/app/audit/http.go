package audit

import (
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/httpapi"
	"github.com/myonku/project-builder/internal/listquery"
)

// ActorLoader 把一次请求解析为当前用户。实现见 app/auth 的 Handler.LoadActor。
type ActorLoader interface {
	LoadActor(c *gin.Context) (domain.User, error)
}

// Handler 暴露审计端点（接口契约 4.10）。
type Handler struct {
	service     *Service
	actorLoader ActorLoader
}

// NewHandler 构造 HTTP 处理器。
func NewHandler(service *Service, actors ActorLoader) *Handler {
	return &Handler{service: service, actorLoader: actors}
}

// errNoActorLoader 表示漏了注入会话解析器（编程错误，快速失败）。
var errNoActorLoader = errors.New("audit: 未注入 ActorLoader")

// actor 解析当前用户。
func (h *Handler) actor(c *gin.Context) (domain.User, error) {
	if h.actorLoader == nil {
		return domain.User{}, domain.ErrInternal.WithCause(errNoActorLoader)
	}
	return h.actorLoader.LoadActor(c)
}

// Register 注册路由。
func (h *Handler) Register(group *gin.RouterGroup) {
	group.GET("/audit", h.list)
}

// listRequest 是审计列表的请求体（GET + 查询参数）。
//
// Page/Size 用**指针**：nil 表示客户端没提供，交给 listquery 用默认值（1 / 20）。
// 用 int 的话"没提供"会解析成 0，从而被当成显式的非法页码拒绝——
// 契约 2.5 明确要求 page/page_size 有默认值。
type listRequest struct {
	ObjectType *string `form:"object_type"`
	ObjectID   *string `form:"object_id"`
	ActorID    *string `form:"actor_id"`
	Action     *string `form:"action"`
	From       *string `form:"from"`
	To         *string `form:"to"`
	Page       *int    `form:"page"`
	Size       *int    `form:"page_size"`
	Sort       string  `form:"sort"`
}

// auditQueryParams 是列表端点允许的查询参数。
//
// 与其它列表端点同规：**未知参数一律拒绝**（契约 2.7）。把 object_type
// 拼成 objectType 会被静默忽略，客户端以为筛选生效了、实际拿到全量记录。
var auditQueryParams = []string{
	"object_type", "object_id", "actor_id", "action",
	"from", "to", "page", "page_size", "sort",
}

// list 处理 GET /audit。
func (h *Handler) list(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	var request listRequest
	if err := c.ShouldBindQuery(&request); err != nil {
		httpapi.Abort(c, domain.ErrInvalidRequest.WithField("query", "查询参数无法解析"))
		return
	}
	if err := rejectUnknownParams(c); err != nil {
		httpapi.Abort(c, err)
		return
	}

	from, err := parseTimeParam(request.From, "from")
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	to, err := parseTimeParam(request.To, "to")
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	page, err := h.service.List(c.Request.Context(), ListInput{
		Actor:      actor,
		ObjectType: request.ObjectType,
		ObjectID:   request.ObjectID,
		ActorID:    request.ActorID,
		Action:     request.Action,
		From:       from,
		To:         to,
		Page:       listquery.PageRequest{Page: request.Page, PageSize: request.Size},
		Sort:       request.Sort,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	items := make([]gin.H, 0, len(page.Items))
	for _, record := range page.Items {
		items = append(items, recordResponse(record))
	}
	httpapi.RespondList(c, items,
		httpapi.NewPagination(page.Page.Page, page.Page.PageSize, page.Total))
}

// parseTimeParam 解析 RFC3339 时间参数；空值返回 nil（不过滤）。
//
// 用 RFC3339 而不是自定义格式：客户端与 curl 都天然会写它，
// 而审计的时间范围是排查手段，不是业务字段。
func parseTimeParam(raw *string, field string) (*time.Time, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(*raw))
	if err != nil {
		return nil, domain.ErrInvalidRequest.
			WithField(field, "必须是 RFC3339 时间（如 2026-10-12T00:00:00Z）")
	}
	utc := parsed.UTC()
	return &utc, nil
}

// rejectUnknownParams 拒绝白名单之外的查询参数。
func rejectUnknownParams(c *gin.Context) error {
	allowedSet := make(map[string]bool, len(auditQueryParams))
	for _, name := range auditQueryParams {
		allowedSet[name] = true
	}

	var unknown []string
	for name := range c.Request.URL.Query() {
		if !allowedSet[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return domain.ErrInvalidRequest.
		WithField("query", "不支持的查询参数: "+strings.Join(unknown, ", ")+
			"；允许的参数: "+strings.Join(auditQueryParams, ", "))
}

// recordResponse 是操作记录的对外结构。
//
// 不含任何"内部指针"字段：这张表本身就是给管理员看的，
// 因此列出的字段与表里的列一一对应，不做额外加工。
func recordResponse(record Record) gin.H {
	return gin.H{
		"id":                      record.ID,
		"actor_id":                record.ActorID,
		"actor_name":              record.ActorName,
		"actor_avatar_updated_at": domain.FormatOptionalTimestamp(record.ActorAvatarUpdatedAt),
		"action":                  record.Action,
		"object_type":             record.ObjectType,
		"object_id":               record.ObjectID,
		"changes":                 record.Changes,
		"request_id":              record.RequestID,
		"created_at":              domain.FormatTimestamp(record.CreatedAt),
	}
}
