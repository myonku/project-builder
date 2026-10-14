/**
 * 操作记录 / 审计（`/audit`）。
 *
 * 骨架期占位。功能阶段要点：
 * - 普通成员只能看到自己可见对象上的记录，"看不到"不等于"没有"；
 * - `changes` 已被服务端脱敏（`"[redacted]"`）与截断，前端不要再解析成结构；
 * - `actor_name` 是操作人**当前**显示名，不是当时的快照。
 */

import PageContainer from '@/components/common/PageContainer';
import PlaceholderContent from '@/components/common/PlaceholderContent';

export default function AuditListView() {
  return (
    <PageContainer
      title="操作记录"
      breadcrumbs={[{ title: '概览', path: '/' }, { title: '操作记录' }]}
    >
      <PlaceholderContent
        description="审计流水：按对象类型/对象/操作人/动作/时间区间筛选，展示变更明细与 request_id。"
        endpoints={['GET /audit?object_type&object_id&actor_id&action&from&to&page&page_size&sort']}
      />
    </PageContainer>
  );
}
