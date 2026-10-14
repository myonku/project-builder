/**
 * 组内计划（`/plans/group`）。
 *
 * 工作组 id 取自 `uiStore.currentGroupId`（跨页面共享的窄状态，F01 §4）；
 * 还没选工作组时提示先选，而不是发一个没有 id 的请求。
 */

import Alert from '@mui/material/Alert';
import PageContainer from '@/components/common/PageContainer';
import PlaceholderContent from '@/components/common/PlaceholderContent';
import { useUiStore } from '@/stores/ui';

export default function GroupPlanView() {
  const currentGroupId = useUiStore((state) => state.currentGroupId);

  return (
    <PageContainer
      title="组内计划"
      breadcrumbs={[{ title: '概览', path: '/' }, { title: '组内计划' }]}
    >
      {currentGroupId ? (
        <PlaceholderContent
          description={`组内计划（当前工作组 ${currentGroupId}）：只看已共享任务，组内只读成员的任务 can_edit 为 false。`}
          endpoints={['GET /plan-views/groups/{id}?…（参数同个人计划）']}
        />
      ) : (
        <Alert severity="info" variant="outlined">
          还没有选择工作组。工作组的选择入口会随「工作组管理」功能一起提供；
          骨架期可直接访问 <code>/admin/groups</code> 查看工作组列表占位页。
        </Alert>
      )}
    </PageContainer>
  );
}
