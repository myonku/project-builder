/**
 * 个人计划（`/plans/personal`）。
 *
 * 骨架期占位。功能阶段要点：
 * - 无分页，一次拿 `projects` + `tasks` + `summary`；
 * - 查询参数**未知即 400**，因此筛选值必须走 `PlanViewQuery` 的类型约束；
 * - 任务的可编辑性直接用投影里的 `can_edit`（服务端已经算好）。
 */

import PageContainer from '@/components/common/PageContainer';
import PlaceholderContent from '@/components/common/PlaceholderContent';

export default function PersonalPlanView() {
  return (
    <PageContainer
      title="个人计划"
      breadcrumbs={[{ title: '概览', path: '/' }, { title: '个人计划' }]}
    >
      <PlaceholderContent
        description="个人计划：按项目/任务/优先级/截止区间筛选，展示 summary 与任务清单。"
        endpoints={[
          'GET /plan-views/personal?project_id&task_owner_id&task_status&priority&due_from&due_to&overdue…',
        ]}
      />
    </PageContainer>
  );
}
