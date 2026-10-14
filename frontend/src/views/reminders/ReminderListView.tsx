/**
 * 提醒列表（`/reminders`）。
 *
 * 骨架期占位。功能阶段要点：
 * - 查询串**没有 `status` 参数**，传它会 400；
 * - 只有 `failed` / `skipped` 可重试（`pending` / `sent` 返回 409）；
 * - `last_error` 成功时是空串而不是 null。
 */

import PageContainer from '@/components/common/PageContainer';
import PlaceholderContent from '@/components/common/PlaceholderContent';

export default function ReminderListView() {
  return (
    <PageContainer
      title="邮件提醒"
      breadcrumbs={[{ title: '概览', path: '/' }, { title: '邮件提醒' }]}
    >
      <PlaceholderContent
        description="提醒记录：按类型与任务筛选，展示发送状态、尝试次数与失败原因，并支持重试。"
        endpoints={[
          'GET /reminders?kind&task_id&page&page_size&sort',
          'GET /reminders/{id}',
          'POST /reminders/{id}/retry',
        ]}
      />
    </PageContainer>
  );
}
