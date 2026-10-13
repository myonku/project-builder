package testsupport

import (
	"context"

	"github.com/myonku/project-builder/internal/app/attachments"
	"github.com/myonku/project-builder/internal/app/projects"
	"github.com/myonku/project-builder/internal/app/tasks"
	"github.com/myonku/project-builder/internal/domain"
)

// ProjectAccessAdapter 把 projects 模块的项目记录适配为 tasks 模块所需的投影。
//
// 与 cmd/api 中的同名适配器职责一致：让两个业务模块互不依赖。
// 放在 testsupport 是为了让测试装配与生产装配使用**同一套**转换语义；
// 若这里与生产实现出现差异，测试就失去了对生产的代表性。
type ProjectAccessAdapter struct {
	Service *projects.Service
}

// Access 实现 tasks.ProjectAccess。
func (a ProjectAccessAdapter) Access(
	ctx context.Context, projectID, actorID string,
) (tasks.ProjectView, domain.Permission, error) {
	record, permission, err := a.Service.AccessFor(ctx, projectID, actorID)
	if err != nil {
		return tasks.ProjectView{}, domain.PermNone, err
	}
	return tasks.ProjectView{
		ID:         record.ID,
		Status:     record.Status,
		Visibility: record.Visibility,
		ArchivedAt: record.ArchivedAt,
	}, permission, nil
}

// AttachmentProjectAccess 把 projects 模块的项目记录适配为 attachments 所需的投影。
//
// 与 cmd/api 中的同名适配器职责一致：让两个业务模块互不依赖。
// 测试装配必须与生产装配使用同一套转换语义，否则测试就失去了对生产的代表性。
type AttachmentProjectAccess struct {
	Service *projects.Service
}

// Access 实现 attachments.ProjectAccess。
func (a AttachmentProjectAccess) Access(
	ctx context.Context, projectID, actorID string,
) (attachments.ProjectInfo, domain.Permission, error) {
	record, permission, err := a.Service.Access(ctx, projectID, actorID)
	if err != nil {
		return attachments.ProjectInfo{}, domain.PermNone, err
	}
	return attachments.ProjectInfo{
		ID:       record.ID,
		ReadOnly: record.ToDomain().IsReadOnly(),
	}, permission, nil
}

// AttachmentTaskAccess 把 tasks 模块的任务记录适配为 attachments 所需的投影。
type AttachmentTaskAccess struct {
	Service *tasks.Service
}

// Access 实现 attachments.TaskAccess。
func (a AttachmentTaskAccess) Access(
	ctx context.Context, taskID, actorID string,
) (attachments.TaskInfo, domain.Permission, error) {
	record, permission, err := a.Service.AccessForAttachment(ctx, taskID, actorID)
	if err != nil {
		return attachments.TaskInfo{}, domain.PermNone, err
	}
	return attachments.TaskInfo{ID: record.ID, ProjectID: record.ProjectID}, permission, nil
}
