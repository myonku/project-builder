/**
 * 个人计划（`/plans/personal`，F05 §7.2 第 4 步）。
 *
 * 数据来自 `GET /plan-views/personal`（《A06》§1）：一次拿到
 * `projects` + `tasks` + `summary`，**没有分页**；筛选全部是查询参数（服务端做）。
 *
 * 表格里的"项目行"是**分组头**：服务端把项目和任务分成两个数组返回，
 * 层级关系只有任务侧的 `parent_id`，所以合成一张表是客户端的活（`planRows.ts`）。
 *
 * 抽屉走 `?taskId=`（与项目详情同一套：F01 §5.7 的三处复用），
 * 可编辑性直接用投影里的 `can_edit`（服务端已按归档/只读共享算好）。
 */

import * as React from 'react';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import RefreshIcon from '@mui/icons-material/Refresh';
import Divider from '@mui/material/Divider';
import Stack from '@mui/material/Stack';
import { useNavigate, useSearchParams } from 'react-router';
import PageContainer from '@/components/common/PageContainer';
import PlanFilters, {
  DEFAULT_PLAN_FILTERS,
  buildPlanQuery,
  type PlanFilterForm,
} from '@/components/plan/PlanFilters';
import PlanSummaryTiles from '@/components/plan/PlanSummaryTiles';
import PlanTable from '@/components/plan/PlanTable';
import { buildPlanRows } from '@/components/plan/planRows';
import TaskDetailDrawer from '@/components/task/TaskDetailDrawer';
import { describeApiError } from '@/hooks/useApiError';
import { usePlanView } from '@/hooks/usePlanView';
import { useAuthStore } from '@/stores/auth';
import type { PlanViewQuery } from '@/types/plan';

/** 计划视图的表格高度：页面比列表页多了筛选栏与汇总卡片，所以基准也更大 */
export const PLAN_TABLE_HEIGHT = 'clamp(360px, calc(100dvh - 470px), 860px)';

export default function PersonalPlanView() {
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const currentUserId = useAuthStore((state) => state.user?.id ?? null);
  const userTimeZone = useAuthStore((state) => state.user?.timezone);

  const [form, setForm] = React.useState<PlanFilterForm>(DEFAULT_PLAN_FILTERS);
  const [query, setQuery] = React.useState<PlanViewQuery>({});

  const { payload, loading, error, projectOptions, reload } = usePlanView({
    groupId: null,
    query,
  });

  const rows = React.useMemo(() => buildPlanRows(payload), [payload]);

  const taskId = searchParams.get('taskId');
  const openTask = React.useCallback(
    (nextTaskId: string) => {
      const next = new URLSearchParams(searchParams);
      next.set('taskId', nextTaskId);
      setSearchParams(next, { replace: false });
    },
    [searchParams, setSearchParams],
  );
  const closeTask = React.useCallback(() => {
    const next = new URLSearchParams(searchParams);
    next.delete('taskId');
    setSearchParams(next, { replace: false });
  }, [searchParams, setSearchParams]);

  const apply = React.useCallback(() => {
    setQuery(buildPlanQuery(form, currentUserId));
  }, [currentUserId, form]);

  const reset = React.useCallback(() => {
    setForm(DEFAULT_PLAN_FILTERS);
    setQuery({});
  }, []);

  const activeTask = React.useMemo(
    () => payload?.tasks.find((task) => task.id === taskId) ?? null,
    [payload, taskId],
  );

  return (
    <PageContainer
      title="个人计划"
      breadcrumbs={[{ title: '概览', path: '/' }, { title: '个人计划' }]}
      actions={
        <Button variant="outlined" startIcon={<RefreshIcon />} onClick={reload} disabled={loading}>
          刷新
        </Button>
      }
    >
      <Stack spacing={2}>
        <PlanFilters
          value={form}
          onChange={setForm}
          onApply={apply}
          onReset={reset}
          projectOptions={projectOptions}
          loading={loading}
        />

        {error ? (
          <Alert
            severity={error.isNotFound ? 'info' : 'error'}
            action={
              <Button color="inherit" size="small" onClick={reload}>
                重试
              </Button>
            }
          >
            {describeApiError(error)}
          </Alert>
        ) : null}

        <PlanSummaryTiles summary={payload?.summary} />

        <Divider />

        <Stack sx={{ height: PLAN_TABLE_HEIGHT, width: '100%', scrollbarGutter: 'stable' }}>
          <PlanTable
            rows={rows}
            loading={loading}
            timeZone={userTimeZone}
            activeTaskId={taskId}
            onOpenTask={openTask}
            onOpenProject={(projectId) => navigate(`/projects/${projectId}`)}
            emptyHint="你还没有参与任何项目（个人计划只包含你是项目成员的项目）"
          />
        </Stack>
      </Stack>

      <TaskDetailDrawer
        key={taskId ?? 'none'}
        taskId={taskId}
        canEdit={activeTask?.can_edit ?? false}
        onChanged={reload}
        onClose={closeTask}
      />
    </PageContainer>
  );
}
