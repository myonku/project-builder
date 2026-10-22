/**
 * 组内计划（`/plans/group`，F05 §7.2 第 4 步）。
 *
 * 与个人计划**同一个组件形态**，只有三处不同（《A06》§2）：
 * 1. 路径带 `group_id`：工作组取自 `uiStore.currentGroupId`（跨页面窄状态，F01 §4），
 *    本页自带选择器（工作组管理页还没做，这里先用下拉把链路跑通）；
 * 2. 内容只含**已共享**的项目（`group_readonly` / `group_editable`）与**已共享**任务；
 * 3. `projects[].my_role` 可能是 `null`——表示"我不是这个项目的成员，是通过组内共享看到的"（组内访客）。
 *
 * 还没选工作组时**不发请求**（避免拿一个空 id 去打 404），只给提示。
 */

import * as React from 'react';
import Alert from '@mui/material/Alert';
import Button from '@mui/material/Button';
import Divider from '@mui/material/Divider';
import RefreshIcon from '@mui/icons-material/Refresh';
import MenuItem from '@mui/material/MenuItem';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';
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
import { useGroupOptions } from '@/hooks/useGroupOptions';
import { usePlanView } from '@/hooks/usePlanView';
import { useAuthStore } from '@/stores/auth';
import { useUiStore } from '@/stores/ui';
import type { PlanViewQuery } from '@/types/plan';

const TABLE_HEIGHT = 'clamp(360px, calc(100dvh - 470px), 860px)';

export default function GroupPlanView() {
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const currentUserId = useAuthStore((state) => state.user?.id ?? null);
  const userTimeZone = useAuthStore((state) => state.user?.timezone);
  const currentGroupId = useUiStore((state) => state.currentGroupId);
  const setCurrentGroupId = useUiStore((state) => state.setCurrentGroupId);

  const { options: groupOptions, loading: groupsLoading } = useGroupOptions();

  const [form, setForm] = React.useState<PlanFilterForm>(DEFAULT_PLAN_FILTERS);
  const [query, setQuery] = React.useState<PlanViewQuery>({});

  const { payload, loading, error, projectOptions, reload } = usePlanView({
    groupId: currentGroupId,
    query,
    enabled: currentGroupId !== null,
  });

  const rows = React.useMemo(() => buildPlanRows(payload), [payload]);

  const taskId = currentGroupId ? searchParams.get('taskId') : null;
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

  const groupSlot = (
    <TextField
      select
      size="small"
      label="工作组"
      value={currentGroupId ?? ''}
      onChange={(event) => {
        setCurrentGroupId(event.target.value || null);
        // 换组等于换数据集：清掉已应用的筛选，避免上一组的筛选条件把人筛没了
        setForm(DEFAULT_PLAN_FILTERS);
        setQuery({});
      }}
      disabled={groupsLoading}
      // 宽度交给 PlanFilters 的容器决定（可伸缩），这里撑满即可
      fullWidth
      // 与筛选区里的其它下拉保持同一套观感：标签常驻凹槽、空值也显示占位文案
      slotProps={{
        inputLabel: { shrink: true },
        select: {
          displayEmpty: true,
          renderValue: (selected: unknown) =>
            // 占位文案要短：筛选区第一层的下拉宽度是可伸缩的（最窄约 130px），
            // 写"（请选择工作组）"会被截断成"（请选…"
            groupOptions.find((group) => group.id === selected)?.name ?? '请选择',
        },
      }}
    >
      <MenuItem value="">（请选择工作组）</MenuItem>
      {groupOptions.map((group) => (
        <MenuItem key={group.id} value={group.id}>
          {group.name}
        </MenuItem>
      ))}
    </TextField>
  );

  const noGroup = currentGroupId === null;

  return (
    <PageContainer
      title="组内计划"
      breadcrumbs={[{ title: '计划', path: '/plans/personal' }, { title: '组内计划' }]}
      actions={
        <Button
          variant="outlined"
          startIcon={<RefreshIcon />}
          onClick={reload}
          disabled={loading || noGroup}
        >
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
          loading={loading || noGroup}
          groupSlot={groupSlot}
        />

        {noGroup ? (
          <Alert severity="info" variant="outlined">
            请选择工作组，查看组内已共享的项目与任务。未共享的内容不会出现在这里。
          </Alert>
        ) : null}

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

        <Stack sx={{ height: TABLE_HEIGHT, width: '100%', scrollbarGutter: 'stable' }}>
          <PlanTable
            rows={rows}
            loading={loading}
            timeZone={userTimeZone}
            activeTaskId={taskId}
            onOpenTask={openTask}
            onOpenProject={(projectId) => navigate(`/projects/${projectId}`)}
            emptyHint="这个工作组里还没有共享出来的项目或任务"
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
