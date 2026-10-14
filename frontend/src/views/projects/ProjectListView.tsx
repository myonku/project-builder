/**
 * 项目列表（`/projects`）。
 *
 * 骨架期占位。功能阶段的要点（F01 §5.4、F02 §2.1）：
 * `POST /projects/query` + `DataGrid` 的 `paginationMode="server"` + `rowCount=pagination.total`；
 * 排序走白名单表达式；`include_archived` 由显式筛选开关控制（默认排除归档）。
 */

import PageContainer from '@/components/common/PageContainer';
import PlaceholderContent from '@/components/common/PlaceholderContent';

export default function ProjectListView() {
  return (
    <PageContainer
      title="项目"
      breadcrumbs={[{ title: '概览', path: '/' }, { title: '项目' }]}
    >
      <PlaceholderContent
        description="项目列表：服务器模式分页的 DataGrid + 名称/状态/工作组/是否含归档筛选。"
        endpoints={[
          'POST /projects/query  → Project[] + pagination',
          'POST /projects        → 新建项目',
          'POST /groups/query    → 工作组下拉选项',
        ]}
      />
    </PageContainer>
  );
}
