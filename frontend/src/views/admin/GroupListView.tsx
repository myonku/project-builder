/**
 * 工作组管理（`/admin/groups`，仅系统管理员）。
 *
 * 骨架期占位。功能阶段要点：
 * - 工作组**没有** `owner` 成员角色：所有者由 `groups.owner_id` 表达，
 *   转移所有权走 `PATCH /groups/{id}` 的 `owner_id`（同事务改写 manager 行）；
 * - 成员是**软移除**（`status: 'removed'`），与项目/任务成员"s移除即删行"不同；
 * - 工作组写接口返回的用户字段是空串，展示用户信息要用成员列表或 `GET /users/{id}`。
 */

import PageContainer from '@/components/common/PageContainer';
import PlaceholderContent from '@/components/common/PlaceholderContent';

export default function GroupListView() {
  return (
    <PageContainer
      title="工作组管理"
      breadcrumbs={[{ title: '概览', path: '/' }, { title: '工作组管理' }]}
    >
      <PlaceholderContent
        description="工作组列表、成员管理与加入申请审核；也是「当前工作组」的选择入口。"
        endpoints={[
          'POST /groups/query',
          'GET /groups/{id}',
          'PATCH /groups/{id}（含 owner_id 转移所有权）',
          'POST /groups/{id}/members/query',
          'POST /groups/{id}/join-requests/query',
          'POST /groups/{id}/join-requests/{user_id}/review',
        ]}
      />
    </PageContainer>
  );
}
