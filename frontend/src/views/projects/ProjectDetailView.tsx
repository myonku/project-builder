/**
 * 项目详情（`/projects/:projectId`）。
 *
 * 骨架期占位。功能阶段要点：
 * - 归档项目（`status === 'archived' || archived_at !== null`）**提前禁用**编辑入口（F01 §6 第 7 条）；
 * - 任务详情用 `TaskDetailDrawer`，以 `?taskId=` 承载，三处页面复用（F01 §5.7）；
 * - 成员管理里"设为持有者"是**转移**，不提供"改为普通成员"按钮（F01 §6 第 9 条）。
 */

import PageContainer from '@/components/common/PageContainer';
import PlaceholderContent from '@/components/common/PlaceholderContent';

export default function ProjectDetailView() {
  return (
    <PageContainer
      title="项目详情"
      breadcrumbs={[
        { title: '概览', path: '/' },
        { title: '项目', path: '/projects' },
        { title: '详情' },
      ]}
    >
      <PlaceholderContent
        description="项目详情：基本信息 + 任务列表 + 成员管理 + 附件 + 甘特图入口。"
        endpoints={[
          'GET /projects/{id}',
          'PATCH /projects/{id}（expected_updated_at 必填）',
          'POST /projects/{id}/archive',
          'POST /projects/{id}/tasks/query',
          'POST /projects/{id}/members/query',
          'POST /projects/{id}/attachments/query',
          'GET /plan-views/projects/{id}/gantt',
        ]}
      />
    </PageContainer>
  );
}
