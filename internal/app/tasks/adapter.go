package tasks

import (
	"context"

	"github.com/myonku/project-builder/internal/domain"
)

// AccessForAttachment 返回任务记录、其所属项目 ID 与 actor 对该任务的权限。
//
// 这是给**装配点**（cmd/api 或测试装配）使用的适配入口，
// 与 projects.Service.AccessFor 是同一模式：attachments 模块声明自己需要的最小接口
// （attachments.TaskAccess），由装配点用一个薄适配器转成它的投影。
//
// 为什么不直接在 tasks 里实现 attachments.TaskAccess：
// 那会让 tasks import attachments（或反向），业务模块之间互相依赖。
//
// 权限为 PermNone 时返回 404（与其它任务读路径一致，不泄漏任务是否存在）。
func (s *Service) AccessForAttachment(
	ctx context.Context, taskID, actorID string,
) (TaskRecord, domain.Permission, error) {
	record, err := s.repo.FindTaskForUser(ctx, taskID, actorID, false)
	if err != nil {
		return TaskRecord{}, domain.PermNone, err
	}
	project, permission, err := s.projects.Access(ctx, record.ProjectID, actorID)
	if err != nil {
		return TaskRecord{}, domain.PermNone, err
	}
	taskPermission := domain.TaskPermission(record.ToDomain(), project.ToDomain(), permission)
	if taskPermission == domain.PermNone {
		return TaskRecord{}, domain.PermNone, notFound()
	}
	return record, taskPermission, nil
}
