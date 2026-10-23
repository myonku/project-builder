package attachments

import (
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
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

// Handler 暴露附件端点（接口契约 4.8）。
type Handler struct {
	service     *Service
	actorLoader ActorLoader
}

// NewHandler 构造 HTTP 处理器。
func NewHandler(service *Service, actors ActorLoader) *Handler {
	return &Handler{service: service, actorLoader: actors}
}

// actor 解析当前用户。
func (h *Handler) actor(c *gin.Context) (domain.User, error) {
	if h.actorLoader == nil {
		return domain.User{}, domain.ErrInternal.WithCause(errNoActorLoader)
	}
	return h.actorLoader.LoadActor(c)
}

// errNoActorLoader 表示漏了注入会话解析器（编程错误，快速失败）。
var errNoActorLoader = errors.New("attachments: 未注入 ActorLoader")

// maxMultipartOverhead 是 multipart 分帧本身的开销预留（边界、头部、字段名）。
//
// 请求体上限 = 文件上限 + 该预留：直接把文件上限当请求体上限会把合法文件
// 卡在边界上（实测多出几百字节就会被拒），而客户端看到的是 413，
// 很难联想到"分帧开销"。
const maxMultipartOverhead = 1 << 20

// Register 注册路由。
//
// 上传/列表挂在容器下（项目、任务各一套），下载与删除按附件 id 定位：
// 附件没有独立权限，因此前者的路径本身表达了"归属哪个容器"。
func (h *Handler) Register(group *gin.RouterGroup) {
	group.POST("/me/attachments/query", h.listMine)
	group.POST("/projects/:project_id/attachments", h.uploadToProject)
	group.POST("/projects/:project_id/attachments/query", h.listByProject)
	group.POST("/tasks/:task_id/attachments", h.uploadToTask)
	group.POST("/tasks/:task_id/attachments/query", h.listByTask)
	group.GET("/attachments/:attachment_id/download", h.download)
	group.DELETE("/attachments/:attachment_id", h.softDelete)
	group.POST("/attachments/:attachment_id/purge", h.purge)
}

// uploadRequest 是列表请求体（契约 4.8）。
type listRequest struct {
	IncludeDeleted *bool `json:"include_deleted"`
	DeletedOnly    *bool `json:"deleted_only"`
	listquery.PageRequest
	listquery.SortRequest
}

// uploadToProject 处理 POST /projects/{project_id}/attachments。
func (h *Handler) uploadToProject(c *gin.Context) {
	projectID := c.Param("project_id")
	h.upload(c, &projectID, nil)
}

// uploadToTask 处理 POST /tasks/{task_id}/attachments。
func (h *Handler) uploadToTask(c *gin.Context) {
	taskID := c.Param("task_id")
	h.upload(c, nil, &taskID)
}

// upload 处理 multipart 上传。
//
// 安全要点：
//   - 请求体上限由 http.MaxBytesReader 强制，**不依赖**客户端提供的 Content-Length；
//   - 内存占用由 ParseMultipartForm 的 maxMemory 限制，超出部分落到临时文件；
//   - 文件名只用于展示与扩展名判断，绝不参与存储路径（见 storage 包）。
func (h *Handler) upload(c *gin.Context, projectID, taskID *string) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	limit := h.service.MaxFileSize() + maxMultipartOverhead
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)

	fileHeader, err := c.FormFile("file")
	if err != nil {
		httpapi.Abort(c, translateMultipartError(err, h.service.MaxFileSize()))
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		httpapi.Abort(c, domain.ErrInternal.WithCause(err))
		return
	}
	defer func() { _ = file.Close() }()

	record, err := h.service.Upload(c.Request.Context(), UploadInput{
		ActorID:   actor.ID,
		ProjectID: projectID,
		TaskID:    taskID,
		FileName:  fileHeader.Filename,
		MimeType:  fileHeader.Header.Get("Content-Type"),
		Size:      fileHeader.Size,
		Content:   file,
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	httpapi.RespondCreated(c, attachmentResponse(record))
}

// translateMultipartError 把 multipart 解析错误映射为契约里的错误码。
func translateMultipartError(err error, maxSize int64) error {
	// 请求体超限：Go 1.19+ 提供 MaxBytesError，优先使用它而不是匹配字符串。
	var maxBytes *http.MaxBytesError
	if errors.As(err, &maxBytes) {
		return domain.ErrFileTooLarge.
			WithField("file", "文件不得超过 "+strconv.FormatInt(maxSize, 10)+" 字节")
	}
	if errors.Is(err, multipart.ErrMessageTooLarge) {
		return domain.ErrFileTooLarge
	}
	if errors.Is(err, http.ErrMissingFile) {
		return domain.ErrValidation.WithField("file", "缺少 file 字段（multipart/form-data）")
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return domain.ErrInvalidRequest.WithField("file", "上传内容不完整")
	}
	return domain.ErrInvalidRequest.WithField("file", "无法解析 multipart 请求")
}

// listByProject 处理 POST /projects/{project_id}/attachments/query。
func (h *Handler) listByProject(c *gin.Context) {
	projectID := c.Param("project_id")
	h.list(c, &projectID, nil)
}

// listByTask 处理 POST /tasks/{task_id}/attachments/query。
func (h *Handler) listByTask(c *gin.Context) {
	taskID := c.Param("task_id")
	h.list(c, nil, &taskID)
}

// list 处理附件列表。
func (h *Handler) list(c *gin.Context, projectID, taskID *string) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	var request listRequest
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}
	if request.IncludeDeleted != nil && request.DeletedOnly != nil &&
		*request.IncludeDeleted && *request.DeletedOnly {
		httpapi.Abort(c, domain.ErrValidation.
			WithField("deleted_only", "deleted_only 与 include_deleted 不能同时为真"))
		return
	}

	in := ListInput{
		ActorID:   actor.ID,
		ProjectID: projectID,
		TaskID:    taskID,
		Page:      request.PageRequest,
		Sort:      request.Sort,
	}
	if request.IncludeDeleted != nil {
		in.IncludeDeleted = *request.IncludeDeleted
	}
	if request.DeletedOnly != nil {
		in.DeletedOnly = *request.DeletedOnly
	}

	page, err := h.service.List(c.Request.Context(), in)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	items := make([]gin.H, 0, len(page.Items))
	for _, record := range page.Items {
		items = append(items, attachmentResponse(record))
	}
	httpapi.RespondList(c, items, httpapi.NewPagination(page.Page.Page, page.Page.PageSize, page.Total))
}

// download 处理 GET /attachments/{attachment_id}/download。
//
// 下载**重新校验权限**（契约 4.8）：下载链接不携带任何凭证，
// 权限完全由当前会话决定，因此不能只在上传时检查一次。
func (h *Handler) download(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	record, reader, err := h.service.Download(c.Request.Context(), actor, c.Param("attachment_id"))
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	defer func() { _ = reader.Close() }()

	c.Header("Content-Type", contentDispositionType(record.MimeType))
	c.Header("Content-Length", strconv.FormatInt(record.SizeBytes, 10))
	c.Header("Content-Disposition", contentDisposition(record.OriginalName))
	// 附件内容不应被浏览器缓存到共享缓存里：权限是按会话判定的。
	c.Header("Cache-Control", "private, no-store")
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, reader); err != nil {
		// 响应头已经发出，无法再改状态码；只能记录。
		_ = c.Error(err)
	}
}

// softDelete 处理 DELETE /attachments/{attachment_id}。
func (h *Handler) softDelete(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	record, err := h.service.Delete(c.Request.Context(), DeleteInput{
		ActorID:      actor.ID,
		AttachmentID: c.Param("attachment_id"),
	})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	httpapi.RespondOK(c, attachmentResponse(record))
}

// purge 处理 POST /attachments/{attachment_id}/purge（彻底删除）。
func (h *Handler) purge(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}

	if err := h.service.Purge(c.Request.Context(), actor, c.Param("attachment_id")); err != nil {
		httpapi.Abort(c, err)
		return
	}
	httpapi.RespondNoContent(c)
}

// attachmentResponse 是附件的对外结构。
//
// 刻意**不含 stored_key**：存储布局是实现细节，客户端一律用 id 下载。
// 也不含 download_url：URL 由客户端按契约拼接，避免服务端拼出与部署前缀不符的地址。
func attachmentResponse(record Record) gin.H {
	var deletedAt *string
	if record.DeletedAt != nil {
		formatted := domain.FormatTimestamp(*record.DeletedAt)
		deletedAt = &formatted
	}
	return gin.H{
		"id":            record.ID,
		"project_id":    record.ProjectID,
		"task_id":       record.TaskID,
		"uploader_id":   record.UploaderID,
		"original_name": record.OriginalName,
		"extension":     record.Extension,
		"size_bytes":    record.SizeBytes,
		"mime_type":     record.MimeType,
		"deleted_at":    deletedAt,
		"created_at":    domain.FormatTimestamp(record.CreatedAt),
		"updated_at":    domain.FormatTimestamp(record.UpdatedAt),
	}
}

// contentDispositionType 兜底 MIME：记录为空时回 octet-stream。
func contentDispositionType(mimeType string) string {
	if strings.TrimSpace(mimeType) == "" {
		return "application/octet-stream"
	}
	return mimeType
}

// contentDisposition 生成 Content-Disposition。
//
// 必须同时给出 filename 与 filename*：前者兼容老客户端，后者（RFC 5987）
// 才能正确表达中文等非 ASCII 文件名。只给 filename 时中文名会变成乱码或被截断。
func contentDisposition(name string) string {
	ascii := make([]rune, 0, len(name))
	for _, r := range name {
		if r < 0x80 && r != '"' && r != '\\' {
			ascii = append(ascii, r)
			continue
		}
		ascii = append(ascii, '_')
	}
	encoded := url.PathEscape(name)
	// PathEscape 会把空格编码为 %20，符合 RFC 5987；但引号要额外处理。
	encoded = strings.ReplaceAll(encoded, `"`, "%22")
	return `attachment; filename="` + string(ascii) + `"; filename*=UTF-8''` + encoded
}

// listMine exposes the authenticated uploader's cross-container, permission-filtered list.
func (h *Handler) listMine(c *gin.Context) {
	actor, err := h.actor(c)
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	var request struct {
		listRequest
		OriginalName  string  `json:"original_name"`
		ContainerType string  `json:"container_type"`
		ProjectID     *string `json:"project_id"`
	}
	if err := httpapi.BindJSON(c, &request); err != nil {
		httpapi.Abort(c, err)
		return
	}
	filter := MineFilter{OriginalName: request.OriginalName, ContainerType: request.ContainerType, ProjectID: request.ProjectID}
	if request.IncludeDeleted != nil {
		filter.IncludeDeleted = *request.IncludeDeleted
	}
	if request.DeletedOnly != nil {
		filter.DeletedOnly = *request.DeletedOnly
	}
	page, err := h.service.ListMine(c.Request.Context(), MineInput{Actor: actor, MineFilter: filter, Page: request.PageRequest, Sort: request.Sort})
	if err != nil {
		httpapi.Abort(c, err)
		return
	}
	items := make([]gin.H, 0, len(page.Items))
	for _, item := range page.Items {
		response := attachmentResponse(item.Record)
		response["source"] = item.Source
		response["permissions"] = item.Permissions
		items = append(items, response)
	}
	httpapi.RespondList(c, items, httpapi.NewPagination(page.Page.Page, page.Page.PageSize, page.Total))
}
