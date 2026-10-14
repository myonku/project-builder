/**
 * 用户管理（`/admin/users`，仅系统管理员）。
 *
 * 骨架期占位。功能阶段要点：
 * - 列表必须显式传 `include_disabled`，两个口径的 `total` 都与条目数一致（《S02》§5）；
 * - 创建用户**不接受** `system_role`（新账号固定 member），角色变更走 `PATCH /users/{id}`；
 * - 系统至少要保留一个启用中的管理员，停用最后一个会返回 409，需要可读提示。
 */

import PageContainer from '@/components/common/PageContainer';
import PlaceholderContent from '@/components/common/PlaceholderContent';

export default function UserListView() {
  return (
    <PageContainer
      title="用户管理"
      breadcrumbs={[{ title: '概览', path: '/' }, { title: '用户管理' }]}
    >
      <PlaceholderContent
        description="用户列表与账号管理：创建、改资料、停用、重置密码、角色调整。"
        endpoints={[
          'POST /users/query  → User[] + pagination（含 include_disabled）',
          'POST /users        → 新建（不接受 system_role）',
          'PATCH /users/{id}',
          'POST /users/{id}/disable',
          'POST /users/{id}/password',
        ]}
      />
    </PageContainer>
  );
}
