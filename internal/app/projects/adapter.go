package projects

import (
	"context"

	"github.com/myonku/project-builder/internal/domain"
)

// AccessFor 返回项目记录与 actor 对该项目的权限。
//
// 这是给**装配点**（cmd/api 或测试装配）使用的适配入口：
// tasks 模块声明自己需要的最小接口（tasks.ProjectAccess），
// 由装配点用一个薄适配器把本方法转成 tasks.ProjectView。
//
// 为什么不直接在 projects 里实现 tasks.ProjectAccess：
// 那会让 projects import tasks（或反向），两个业务模块互相依赖。
// 让装配点承担结构转换是标准做法，也是本项目"业务模块之间不互相依赖"边界的体现。
func (s *Service) AccessFor(ctx context.Context, projectID, actorID string) (ProjectRecord, domain.Permission, error) {
	return s.Access(ctx, projectID, actorID)
}
