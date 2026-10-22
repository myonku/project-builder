package attachments

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/myonku/project-builder/internal/app/apperr"
	"github.com/myonku/project-builder/internal/config"
	"github.com/myonku/project-builder/internal/domain"
	"github.com/myonku/project-builder/internal/listquery"
	"github.com/myonku/project-builder/internal/platform/clock"
	"github.com/myonku/project-builder/internal/platform/idgen"
	"github.com/myonku/project-builder/internal/platform/storage"
)

// ProjectAccess 是本模块对项目权限判定的需求，由装配点用 app/projects 实现。
//
// 定义成接口而不是直接 import projects：两个业务模块之间不产生依赖边，
// 这也让测试可以注入替身。
type ProjectAccess interface {
	Access(ctx context.Context, projectID, actorID string) (ProjectInfo, domain.Permission, error)
}

// ProjectInfo 是本模块所需的最小项目投影。
type ProjectInfo struct {
	ID string
	// ReadOnly 为真表示项目已归档：附件随之为只读。
	ReadOnly bool
}

// TaskAccess 是本模块对任务权限判定的需求，由装配点用 app/tasks 实现。
type TaskAccess interface {
	Access(ctx context.Context, taskID, actorID string) (TaskInfo, domain.Permission, error)
}

// TaskInfo 是本模块所需的最小任务投影。
//
// 不需要 ReadOnly：任务所在项目归档时，domain.TaskPermission 已经只给 PermRead，
// 因此"能否写"由权限层级本身表达，多一个布尔量只会多一处可能不一致的来源。
type TaskInfo struct {
	ID        string
	ProjectID string
}

// Store 是本模块对持久化的需求。
type Store interface {
	Create(ctx context.Context, params CreateParams) (Record, error)
	Find(ctx context.Context, attachmentID string) (Record, error)
	List(ctx context.Context, filter ListFilter, orderBy string, limit, offset int) ([]Record, error)
	Count(ctx context.Context, filter ListFilter) (int, error)
	SoftDelete(ctx context.Context, attachmentID string, at time.Time, audit *apperr.AuditWrite) (Record, error)
	Purge(ctx context.Context, attachmentID string, audit *apperr.AuditWrite) error
	CountByUploader(ctx context.Context, uploaderID string) (int, error)
}

// Page 是列表查询结果。
type Page[T any] struct {
	Items []T
	Total int
	Page  listquery.Pagination
}

// Service 承载附件用例。
type Service struct {
	repo     Store
	storage  storage.Store
	projects ProjectAccess
	tasks    TaskAccess
	ids      *idgen.Generator
	clk      clock.Clock
	logger   *slog.Logger
	cfg      config.StorageConfig
	// backend 是当前写入使用的对象存储标识，写入 attachments.storage_backend 账本。
	backend string
	// audit 是操作记录的写入方，由仓储在业务事务里调用（见 apperr.AuditWrite）。
	audit apperr.AuditWriter
}

// Options 是 Service 的构造参数。
type Options struct {
	Store    Store
	Storage  storage.Store
	Projects ProjectAccess
	Tasks    TaskAccess
	IDs      *idgen.Generator
	Clock    clock.Clock
	Logger   *slog.Logger
	Config   config.StorageConfig
	// Audit 为 nil 时不记操作记录。
	Audit apperr.AuditWriter
}

// NewService 构造附件服务。
func NewService(opts Options) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	clk := opts.Clock
	if clk == nil {
		clk = clock.NewSystem()
	}
	backend := strings.ToLower(strings.TrimSpace(opts.Config.Backend))
	if backend == "" {
		backend = "local"
	}
	return &Service{
		repo:     opts.Store,
		storage:  opts.Storage,
		projects: opts.Projects,
		tasks:    opts.Tasks,
		ids:      opts.IDs,
		clk:      clk,
		logger:   logger,
		cfg:      opts.Config,
		backend:  backend,
		audit:    opts.Audit,
	}
}

// auditWith 构造一条附件操作记录。
//
// changes 只放元数据（原始文件名、扩展名、大小、所属容器），
// **绝不放文件内容**：附件是用户数据，内容进审计表既无必要也无法清理。
func (s *Service) auditWith(
	actorID string, action domain.AuditAction, attachmentID string, changes map[string]string,
) *apperr.AuditWrite {
	return apperr.NewAuditWrite(s.audit, domain.AuditEntry{
		ActorID:    actorID,
		Action:     action,
		ObjectType: domain.AuditObjectAttachment,
		ObjectID:   attachmentID,
		Changes:    changes,
	})
}

// attachmentChanges 汇总附件的元数据。
func attachmentChanges(record Record) map[string]string {
	changes := map[string]string{
		"original_name": record.OriginalName,
		"extension":     record.Extension,
		"size_bytes":    strconv.FormatInt(record.SizeBytes, 10),
	}
	if record.ProjectID != nil {
		changes["project_id"] = *record.ProjectID
	}
	if record.TaskID != nil {
		changes["task_id"] = *record.TaskID
	}
	return changes
}

// derefOrEmpty 把可空字符串转为审计摘要里的取值（空串表示"不属于该容器"）。
func derefOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// MaxFileSize 返回单文件大小上限，供 HTTP 层限制请求体。
func (s *Service) MaxFileSize() int64 { return s.cfg.MaxFileSize }

// UploadInput 是上传附件的用例输入。
//
// Content 由 HTTP 层提供，**流式**传给存储层：不把整个文件读进内存，
// 否则 10 MiB 的上限在并发下会变成内存放大。
type UploadInput struct {
	ActorID string
	// ProjectID 与 TaskID 恰有一个非空。
	ProjectID *string
	TaskID    *string

	FileName string
	MimeType string
	Size     int64
	Content  io.Reader
}

// Upload 保存附件。
//
// 处理顺序（契约 5）：容器存在性与可见性（404）→ 权限（403）→ 配置校验
// （413/415）→ 配额（409）→ 写对象与写行。
//
// 关于"先写对象还是先写行"：先写对象、后写行。若写行失败（例如唯一约束冲突），
// 立即尽力删除刚写入的对象，避免留下无主的字节；反过来先写行的话，
// 行会短暂指向一个不存在的对象，而被别人读到 500。
func (s *Service) Upload(ctx context.Context, in UploadInput) (Record, error) {
	container, err := s.resolveContainer(ctx, in.ProjectID, in.TaskID, in.ActorID)
	if err != nil {
		return Record{}, err
	}
	// 先区分"看不见"与"看得见但不够权限"：
	// 权限判定的返回约定是 PermNone + nil error（而不是错误），
	// 因此调用方必须自己把 PermNone 变成 404，否则会用 403 泄漏对象存在性。
	if container.permission == domain.PermNone {
		if in.ProjectID != nil {
			return Record{}, domain.New(domain.CodeProjectNotFound, "Project not found")
		}
		return Record{}, domain.New(domain.CodeTaskNotFound, "Task not found")
	}
	if !domain.CanWriteAttachment(container.permission, container.readOnly) {
		return Record{}, domain.ErrPermissionDenied
	}

	if err := domain.ValidateAttachmentSize(in.Size, s.cfg.MaxFileSize); err != nil {
		return Record{}, err
	}
	originalName := domain.NormalizeAttachmentName(in.FileName)
	extension := domain.AttachmentExtension(originalName)
	if err := domain.ValidateAttachmentExtension(
		extension, s.cfg.AllowedExtensions, s.cfg.ExtensionsUnrestricted()); err != nil {
		return Record{}, err
	}

	// 配额按**上传者**统计，含已逻辑删除的附件（它们仍占磁盘）。
	count, err := s.repo.CountByUploader(ctx, in.ActorID)
	if err != nil {
		return Record{}, err
	}
	if s.cfg.MaxFilesPerUser > 0 && count >= s.cfg.MaxFilesPerUser {
		return Record{}, domain.AttachmentQuotaError(s.cfg.MaxFilesPerUser)
	}

	id, err := s.ids.UUID()
	if err != nil {
		return Record{}, domain.ErrInternal.WithCause(err)
	}
	key := storedKey(id)

	if err := s.storage.Put(ctx, key, in.Content, in.Size); err != nil {
		return Record{}, domain.ErrInternal.WithCause(err)
	}

	record, err := s.repo.Create(ctx, CreateParams{
		ID:             id,
		ProjectID:      in.ProjectID,
		TaskID:         in.TaskID,
		UploaderID:     in.ActorID,
		OriginalName:   originalName,
		StoredKey:      key,
		Extension:      extension,
		SizeBytes:      in.Size,
		MimeType:       normalizeMime(in.MimeType, extension),
		StorageBackend: s.backend,
		// 上传也入账：只记元数据，不记内容。
		Audit: s.auditWith(in.ActorID, domain.AuditActionCreated, id, map[string]string{
			"original_name": originalName,
			"extension":     extension,
			"size_bytes":    strconv.FormatInt(in.Size, 10),
			"project_id":    derefOrEmpty(in.ProjectID),
			"task_id":       derefOrEmpty(in.TaskID),
		}),
	})
	if err != nil {
		// 尽力清理刚写入的对象：失败只记录，不改变返回给客户端的错误
		// （真正的失败原因是写行，而不是清理）。
		if cleanupErr := s.storage.Delete(ctx, key); cleanupErr != nil {
			s.logger.Warn("上传失败后清理对象失败，可能残留无主字节",
				slog.String("key", key), slog.Any("cause", cleanupErr))
		}
		return Record{}, err
	}

	s.logger.Info("附件已上传",
		slog.String("attachment_id", record.ID),
		slog.String("uploader_id", record.UploaderID),
		slog.Int64("size_bytes", record.SizeBytes),
	)
	return record, nil
}

// Download 返回附件的元数据与字节流。
func (s *Service) Download(ctx context.Context, actor domain.User, attachmentID string) (Record, io.ReadCloser, error) {
	record, err := s.findVisible(ctx, actor, attachmentID)
	if err != nil {
		return Record{}, nil, err
	}
	reader, err := s.storage.Open(ctx, record.StoredKey)
	if err != nil {
		if err == storage.ErrObjectNotFound {
			// 有行无对象：数据库是权威，但字节确实丢了。
			// 返回 500（而不是 404）——对客户端来说"这个附件曾经存在但现在读不到"
			// 是服务端的数据一致性问题，而不是"你请求错了"。
			return Record{}, nil, domain.ErrInternal.WithCause(
				fmt.Errorf("附件 %s 的对象缺失: %s", record.ID, record.StoredKey))
		}
		return Record{}, nil, domain.ErrInternal.WithCause(err)
	}
	return record, reader, nil
}

// ListInput 是附件列表的用例输入。
type ListInput struct {
	ActorID   string
	ProjectID *string
	TaskID    *string
	// IncludeDeleted 为真时把已逻辑删除的附件纳入（仍受可见性限制）。
	IncludeDeleted bool
	// DeletedOnly 为真时只看已删除的（"回收站"视角）。
	DeletedOnly bool
	Page        listquery.PageRequest
	Sort        string
}

// List 分页返回附件列表。
func (s *Service) List(ctx context.Context, in ListInput) (Page[Record], error) {
	container, err := s.resolveContainer(ctx, in.ProjectID, in.TaskID, in.ActorID)
	if err != nil {
		return Page[Record]{}, err
	}
	if !domain.CanReadAttachment(container.permission) {
		// 不可读即不可见：与不存在同码。
		return Page[Record]{}, notFound()
	}

	pagination, err := in.Page.Pagination()
	if err != nil {
		return Page[Record]{}, err
	}
	order, err := AttachmentSort.Resolve(in.Sort)
	if err != nil {
		return Page[Record]{}, err
	}
	orderBy := AttachmentSort.OrderBy(order)

	filter := ListFilter{
		ProjectID: in.ProjectID,
		TaskID:    in.TaskID,
		ActorID:   in.ActorID,
		// 逻辑删除的可见性：持有者（PermManage）与系统管理员能看全部，
		// 其他人只能看自己上传的。判定在领域层，这里只把结果传下去。
		// 第二个参数是 uploaderID：列表里每一行的上传者都不同，
		// 因此这里只判断"能否看全部已删除行"（持有者/管理员），
		// 逐行的"是不是自己上传的"由 SQL 条件处理。
		CanSeeAnyDeleted: domain.CanViewDeletedAttachment(
			domain.User{ID: in.ActorID}, "", container.permission),
		IncludeDeleted: in.IncludeDeleted,
		DeletedOnly:    in.DeletedOnly,
	}

	total, err := s.repo.Count(ctx, filter)
	if err != nil {
		return Page[Record]{}, err
	}
	items, err := s.repo.List(ctx, filter, orderBy, pagination.Limit(), pagination.Offset())
	if err != nil {
		return Page[Record]{}, err
	}
	return Page[Record]{Items: items, Total: total, Page: pagination}, nil
}

// DeleteInput 是逻辑删除的用例输入。
type DeleteInput struct {
	ActorID      string
	AttachmentID string
}

// Delete 逻辑删除附件（接口契约 4.8）。
//
// 删除后字节仍在磁盘上，直到被彻底删除；期间只有上传者、容器持有者与
// 系统管理员能看到它（用户确认的规则）。
func (s *Service) Delete(ctx context.Context, in DeleteInput) (Record, error) {
	record, err := s.repo.Find(ctx, in.AttachmentID)
	if err != nil {
		return Record{}, err
	}
	container, err := s.containerOf(ctx, record, in.ActorID)
	if err != nil {
		return Record{}, err
	}
	if container.permission == domain.PermNone {
		// 不可见与不存在同码，不泄漏附件是否存在。
		return Record{}, notFound()
	}
	if !domain.CanWriteAttachment(container.permission, container.readOnly) {
		return Record{}, domain.ErrPermissionDenied
	}
	if record.IsDeleted() {
		return Record{}, domain.ErrConflict.
			WithField("attachment_id", "该附件已被逻辑删除")
	}

	deleted, err := s.repo.SoftDelete(ctx, in.AttachmentID, s.clk.Now(),
		s.auditWith(in.ActorID, domain.AuditActionDeleted, in.AttachmentID, nil))
	if err != nil {
		return Record{}, err
	}
	s.logger.Info("附件已逻辑删除",
		slog.String("attachment_id", deleted.ID),
		slog.String("actor_id", in.ActorID),
	)
	return deleted, nil
}

// Purge 彻底删除已逻辑删除的附件：先删对象，再删行。
//
// 顺序的理由：两种失败模式里，"对象没了但行还在"是**可修复**的
// （再调一次即可成功，因为删除不存在的对象是幂等的），
// 而"行没了但对象还在"是**不可见**的——没有任何记录指向那堆字节，
// 它会一直占着磁盘且无人知道。因此宁可留下前者。
//
// 只能对**已逻辑删除**的附件执行：彻底删除是不可逆的，
// 必须先经过一次显式的逻辑删除，避免误操作直接销毁文件。
func (s *Service) Purge(ctx context.Context, actor domain.User, attachmentID string) error {
	record, err := s.findPurgeable(ctx, actor, attachmentID)
	if err != nil {
		return err
	}

	if err := s.storage.Delete(ctx, record.StoredKey); err != nil {
		return domain.ErrInternal.WithCause(err)
	}
	// 彻底删除也入账：它是不可逆动作，更需要留下"谁在什么时候删了什么"。
	if err := s.repo.Purge(ctx, attachmentID,
		s.auditWith(actor.ID, domain.AuditActionPurged, attachmentID,
			attachmentChanges(record))); err != nil {
		return err
	}

	s.logger.Info("附件已被彻底删除",
		slog.String("attachment_id", record.ID),
		slog.String("actor_id", actor.ID),
	)
	return nil
}

// findPurgeable 取出附件并校验"可以彻底删除"。
func (s *Service) findPurgeable(ctx context.Context, actor domain.User, attachmentID string) (Record, error) {
	record, err := s.repo.Find(ctx, attachmentID)
	if err != nil {
		return Record{}, err
	}
	if !record.IsDeleted() {
		// 未逻辑删除的附件不允许直接彻底删除。
		return Record{}, domain.ErrConflict.
			WithField("attachment_id", "请先逻辑删除该附件，再执行彻底删除")
	}

	container, err := s.containerOf(ctx, record, actor.ID)
	if err != nil {
		return Record{}, err
	}
	// 持有者与系统管理员要看得到全部已删除附件；其他人只能处理自己上传的。
	visible := domain.CanViewDeletedAttachment(actor, record.UploaderID, container.permission)
	if container.permission == domain.PermNone || !visible {
		return Record{}, notFound()
	}
	return record, nil
}

// findVisible 取出附件并校验 actor 能不能看到它（下载路径）。
func (s *Service) findVisible(ctx context.Context, actor domain.User, attachmentID string) (Record, error) {
	record, err := s.repo.Find(ctx, attachmentID)
	if err != nil {
		return Record{}, err
	}
	container, err := s.containerOf(ctx, record, actor.ID)
	if err != nil {
		return Record{}, err
	}
	if !domain.CanReadAttachment(container.permission) {
		return Record{}, notFound()
	}
	if record.IsDeleted() &&
		!domain.CanViewDeletedAttachment(actor, record.UploaderID, container.permission) {
		// 已逻辑删除且调用者既不是上传者也不是持有者/管理员：与不存在同码。
		return Record{}, notFound()
	}
	return record, nil
}

// container 描述附件所属容器的权限上下文。
//
// 刻意**不含**"调用者是不是管理员"：领域层的权限函数已经对管理员放行
// （管理员得到 PermManage），因此管理员在下面的每个判断里都自动通过。
// 多加一个字段只会多一处可能被静默留空的地方——那正是 backend/docs/B05 5.12 的根因。
type container struct {
	permission domain.Permission
	readOnly   bool
}

// containerOf 根据附件自身的归属解析容器权限。
func (s *Service) containerOf(ctx context.Context, record Record, actorID string) (container, error) {
	return s.resolveContainer(ctx, record.ProjectID, record.TaskID, actorID)
}

// resolveContainer 解析"附件挂在哪个容器上"以及调用者对该容器的权限。
//
// 项目与任务恰有一个非空（数据库 CHECK 保证），因此这里最多走一条分支。
func (s *Service) resolveContainer(
	ctx context.Context, projectID, taskID *string, actorID string,
) (container, error) {
	switch {
	case projectID != nil && taskID != nil:
		// 只可能来自直接改库：契约与 CHECK 都不允许。
		return container{}, domain.ErrInternal.WithCause(
			fmt.Errorf("附件同时归属项目与任务"))
	case projectID != nil:
		if s.projects == nil {
			return container{}, domain.ErrInternal.WithCause(errNoProjectAccess)
		}
		info, permission, err := s.projects.Access(ctx, *projectID, actorID)
		if err != nil {
			return container{}, err
		}
		return container{permission: permission, readOnly: info.ReadOnly}, nil
	case taskID != nil:
		if s.tasks == nil {
			return container{}, domain.ErrInternal.WithCause(errNoTaskAccess)
		}
		_, permission, err := s.tasks.Access(ctx, *taskID, actorID)
		if err != nil {
			return container{}, err
		}
		// 任务权限已经包含"归档项目只读"（domain.TaskPermission 只给 PermRead），
		// 因此 readOnly 传 false，由权限层级表达。
		return container{permission: permission}, nil
	default:
		return container{}, domain.ErrInvalidRequest.
			WithField("project_id", "必须指定附件归属的项目或任务")
	}
}

// storedKey 生成存储标识。
//
// 规则（backend/docs/B02 §5.2 的规划要点）：**绝不使用用户提供的文件名**。
// 这里用 UUIDv7：时间有序、全局唯一，且与数据库主键一致，便于对照排查。
// 前缀按前两位分片，避免单个目录下堆积过多文件（本地文件系统的常见退化）。
func storedKey(id string) string {
	shard := strings.ReplaceAll(id, "-", "")[:2]
	return path.Join("attachments", shard, id)
}

// normalizeMime 规范化 MIME。
//
// 客户端可能不提供 Content-Type；此时按扩展名给一个保守的兜底，
// 而不是留空——下载时必须回一个明确的类型，否则浏览器会猜。
func normalizeMime(raw, extension string) string {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	// multipart 里常见的默认值，对单个文件没有意义。
	if trimmed == "" || trimmed == "application/octet-stream" {
		if guessed := mimeByExtension(extension); guessed != "" {
			return guessed
		}
		return "application/octet-stream"
	}
	if len(trimmed) > 160 {
		return trimmed[:160]
	}
	return trimmed
}

// mimeByExtension 是一张很小的兜底表。
//
// 刻意不引入第三方 MIME 库：这里只服务"客户端没给类型"的场景，
// 给出一个比 octet-stream 更有用的默认值即可，不追求完备。
func mimeByExtension(extension string) string {
	switch extension {
	case ".txt", ".log", ".md":
		return "text/plain; charset=utf-8"
	case ".csv":
		return "text/csv; charset=utf-8"
	case ".json":
		return "application/json"
	case ".pdf":
		return "application/pdf"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".zip":
		return "application/zip"
	case ".doc":
		return "application/msword"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xls":
		return "application/vnd.ms-excel"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".ppt":
		return "application/vnd.ms-powerpoint"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	default:
		return ""
	}
}

// 装配遗漏属于编程错误，快速失败而不是静默降级。
var (
	errNoProjectAccess = fmt.Errorf("attachments: 未注入 ProjectAccess")
	errNoTaskAccess    = fmt.Errorf("attachments: 未注入 TaskAccess")
)
