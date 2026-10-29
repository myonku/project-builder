package reminders

import (
	"errors"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/httpapi"
	"github.com/myonku/project-builder/internal/listquery"
)

// ActorLoader 把一次请求解析为当前用户。实现见 app/auth 的 Handler.LoadActor。
type ActorLoader interface {
	LoadActor(c *gin.Context) (domain.User, error)
}

// Handler 暴露提醒端点（接口契约 4.9）。
type Handler struct {
	service     *Service
	actorLoader ActorLoader
}

// NewHandler 构造 HTTP 处理器。
func NewHandler(service *Service, actors ActorLoader) *Handler {
	return &Handler{service: service, actorLoader: actors}
}

// errNoActorLoader 表示漏了注入会话解析器（编程错误，快速失败）。
var errNoActorLoader = errors.New("reminders: 未注入 ActorLoader")

// actor 解析当前用户。
func (h *Handler) actor(c *gin.Context) (domain.User, error) {
	if h.actorLoader == nil {
		return domain.User{}, domain.ErrInternal.WithCause(errNoActorLoader)
	}
	return h.actorLoader.LoadActor(c)
}

// Register 注册路由。
func (h *Handler) Register(group *gin.RouterGroup) {
	group.GET("/reminders", h.list)
	group.GET("/reminders/:reminder_id", h.detail)
	group.POST("/reminders/:reminder_id/retry", h.retry)
}

// listRequest 是提醒列表的请求体。
//
// 用 GET + 查询参数而不是 POST + 请求体：契约 4.9 把提醒列表定义为 GET，
// 因此筛选参数走查询串，未知参数一律 400（契约 2.7 的既有约定）。
//
// Page/Size 用**指针**：nil 表示客户端没提供，交给 listquery 用默认值（1 / 20）。
// 用 int 会把"没提供"解析成 0，从而被当成显式的非法页码拒绝——
// 而契约 2.5 明确要求这两个参数有默认值（2026-10-12 实测到该缺陷并修正）。
type listRequest struct {
	Kind *string `form:"kind"`
	// TaskID 用于"这条任务的提醒都发给谁了"这类排查。
	TaskID *string `form:"task_id"`
	Page   *int    `form:"page"`
	Size   *int    `form:"page_size"`
	Sort   string  `form:"sort"`
}

// list 处理 GET /reminders。
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
	if err := rejectUnknownParams(c, reminderQueryParams); err != nil {
		httpapi.Abort(c, err)
		return
	}

	in := ListInput{
		Actor:  actor,
		Kind:   request.Kind,
		TaskID: request.TaskID,
		Page:   listquery.PageRequest{Page: request.Page, PageSize: request.Size},
		Sort:   request.Sort,
	}
	page, err := h.service.List(c.Request.Context(), in)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	items := make([]gin.H, 0, len(page.Items))
	for _, record := range page.Items {
		items = append(items, reminderResponse(record))
	}
	httpapi.RespondList(c, items,
		httpapi.NewPagination(page.Page.Page, page.Page.PageSize, page.Total))
}

// reminderQueryParams 是列表端点允许的查询参数。
//
// 与 planviews 的既有做法一致：**未知参数一律拒绝**（契约 2.7 对请求体的
// 要求同源）。把 status 拼成 statuss 会被静默忽略，客户端以为筛选生效了、
// 实际拿到全量数据——这类"看起来对了"的错误最难发现。
var reminderQueryParams = []string{"kind", "task_id", "page", "page_size", "sort"}

// rejectUnknownParams 拒绝白名单之外的查询参数。
func rejectUnknownParams(c *gin.Context, allowed []string) error {
	allowedSet := make(map[string]bool, len(allowed))
	for _, name := range allowed {
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
	// 排序后输出，保证错误信息稳定（便于测试与排查）。
	sort.Strings(unknown)
	return domain.ErrInvalidRequest.
		WithField("query", "不支持的查询参数: "+strings.Join(unknown, ", ")+
			"；允许的参数: "+strings.Join(allowed, ", "))
}

// detail 处理 GET /reminders/{reminder_id}。
func (h *Handler) detail(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	record, err := h.service.Detail(c.Request.Context(), actor, c.Param("reminder_id"))
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	httpapi.RespondOK(c, reminderResponse(record))
}

// retry 处理 POST /reminders/{reminder_id}/retry。
func (h *Handler) retry(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	record, err := h.service.Retry(c.Request.Context(), actor, c.Param("reminder_id"))
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	httpapi.RespondOK(c, reminderResponse(record))
}

// reminderResponse 是提醒的对外结构。
//
// 不含 dedupe_key：它是实现细节（幂等机制的载体），客户端不需要也不该依赖它。
func reminderResponse(record Record) gin.H {
	var sentAt *string
	if record.SentAt != nil {
		formatted := domain.FormatTimestamp(*record.SentAt)
		sentAt = &formatted
	}
	return gin.H{
		"id":    record.ID,
		"cycle": record.Cycle, "plan_version": record.PlanVersion, "covered_by": record.CoveredBy, "event_id": record.EventID, "can_retry": record.CanRetry, "delivery_attempts": record.Deliveries,
		"task_id":         record.TaskID,
		"recipient_id":    record.RecipientID,
		"recipient_email": record.RecipientEmail,
		"kind":            record.Kind,
		"kind_label":      domain.ReminderKindLabel(record.Kind),
		"scheduled_for":   domain.FormatTimestamp(record.ScheduledFor),
		"status":          record.Status,
		"attempts":        record.Attempts,
		"last_error":      record.LastError,
		"sent_at":         sentAt,
		"created_at":      domain.FormatTimestamp(record.CreatedAt),
		"updated_at":      domain.FormatTimestamp(record.UpdatedAt),
	}
}
