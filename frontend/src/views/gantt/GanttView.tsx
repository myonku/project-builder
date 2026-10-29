/**
 * 视图 / 甘特图（`/gantt`，P03 日期与进度编辑）。
 *
 * 两类图放在**同一个一级路由**里，用页内 Tab 切换（与 F05 记录的方案一致）：
 * - **项目视图**：选定一个项目，展示项目周期与任务层级/进度；
 * - **成员视图**：选定工作组，按任务成员拆分泳道，展示成员的工作分布与进度。
 *
 * 图形编辑默认锁定，页面控制器串行管理草稿、确认、保存及权威刷新。
 * 时间尺度（日/周/月）是纯客户端展示参数，服务端不提供（《A06》§3 备注）。
 *
 * URL 参数：`view=members`、`projectId`、`groupId`、`memberId`、`scale=day|month`、
 * `taskId`（任务抽屉）。默认 `view=projects`、`scale=week`，因此默认值不写进地址栏。
 */

import * as React from 'react';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Chip from '@mui/material/Chip';
import MenuItem from '@mui/material/MenuItem';
import Stack from '@mui/material/Stack';
import Tab from '@mui/material/Tab';
import Tabs from '@mui/material/Tabs';
import TextField from '@mui/material/TextField';
import ToggleButton from '@mui/material/ToggleButton';
import ToggleButtonGroup from '@mui/material/ToggleButtonGroup';
import Typography from '@mui/material/Typography';
import RefreshIcon from '@mui/icons-material/Refresh';
import RestartAltIcon from '@mui/icons-material/RestartAlt';
import SearchIcon from '@mui/icons-material/Search';
import { useNavigate, useSearchParams } from 'react-router';
import PageContainer from '@/components/common/PageContainer';
import { filterItemSx, filterRowSx } from '@/components/common/filterLayout';
import GanttChart from '@/components/gantt/GanttChart';
import GanttEditDialog from '@/components/gantt/GanttEditDialog';
import { useGanttEdit } from '@/hooks/useGanttEdit';
import {
  buildMemberGanttRows,
  buildProjectGanttRows,
  collectGanttMembers,
  filterCollapsedRows,
  GANTT_SCALE_META,
  type GanttScale,
} from '@/components/gantt/ganttModel';
import TaskDetailDrawer from '@/components/task/TaskDetailDrawer';
import { describeApiError, useApiError } from '@/hooks/useApiError';
import { useGroupOptions } from '@/hooks/useGroupOptions';
import { usePlanView } from '@/hooks/usePlanView';
import { useProjectGantt } from '@/hooks/useProjectGantt';
import { useProjectOptions } from '@/hooks/useProjectOptions';
import { useAuthStore } from '@/stores/auth';
import { useUiStore } from '@/stores/ui';
import type { PlanViewQuery } from '@/types/plan';
import { todayIn } from '@/utils/datetime';

type GanttViewMode = 'projects' | 'members';

const GANTT_PLAN_QUERY: PlanViewQuery = {};
const SCALE_VALUES: GanttScale[] = ['day', 'week', 'month'];

function parseScale(value: string | null): GanttScale {
  return SCALE_VALUES.includes(value as GanttScale) ? (value as GanttScale) : 'week';
}

export default function GanttView() {
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const userTimeZone = useAuthStore((state) => state.user?.timezone);
  const currentGroupId = useUiStore((state) => state.currentGroupId);
  const setCurrentGroupId = useUiStore((state) => state.setCurrentGroupId);
  const { handle } = useApiError();
  const interactionBusy = React.useRef(false);

  const view: GanttViewMode = searchParams.get('view') === 'members' ? 'members' : 'projects';
  const scale = parseScale(searchParams.get('scale'));
  const projectParam = searchParams.get('projectId') ?? '';
  const projectNameParam = searchParams.get('projectName') ?? '';
  const groupParam = searchParams.get('groupId') ?? '';
  const memberParam = searchParams.get('memberId') ?? '';
  const taskId = searchParams.get('taskId');

  /** 项目名称是"主动查询"：输入框只改草稿，点查询/回车才写 URL 并重新拉选项 */
  const [projectNameDraft, setProjectNameDraft] = React.useState(projectNameParam);
  const [collapsedIds, setCollapsedIds] = React.useState<Set<string>>(() => new Set());

  const patchParams = React.useCallback(
    (patch: Record<string, string | null>, replace = false) => {
      if (interactionBusy.current) return;
      const next = new URLSearchParams(searchParams);
      for (const [key, value] of Object.entries(patch)) {
        if (value === null || value === '') {
          next.delete(key);
        } else {
          next.set(key, value);
        }
      }
      setSearchParams(next, { replace });
    },
    [searchParams, setSearchParams],
  );

  const {
    options: projectOptions,
    loading: projectsLoading,
    error: projectsError,
    reload: reloadProjects,
  } = useProjectOptions(view === 'projects', projectNameParam);

  // URL 是名称筛选的权威值：后退/外链进入时把草稿同步回来
  React.useEffect(() => {
    setProjectNameDraft(projectNameParam);
  }, [projectNameParam]);
  const {
    options: groupOptions,
    loading: groupsLoading,
    error: groupsError,
    reload: reloadGroups,
  } = useGroupOptions(view === 'members');

  const effectiveProjectId = React.useMemo(() => {
    if (projectParam && projectOptions.some((project) => project.id === projectParam)) {
      return projectParam;
    }
    return projectOptions[0]?.id ?? '';
  }, [projectParam, projectOptions]);

  const effectiveGroupId = React.useMemo(() => {
    if (groupParam && groupOptions.some((group) => group.id === groupParam)) {
      return groupParam;
    }
    if (currentGroupId && groupOptions.some((group) => group.id === currentGroupId)) {
      return currentGroupId;
    }
    return groupOptions[0]?.id ?? '';
  }, [groupParam, groupOptions, currentGroupId]);

  // 首次进入时把默认项目/工作组写进 URL，刷新与分享才能复现同一张图
  React.useEffect(() => {
    if (view !== 'projects' || projectsLoading || !effectiveProjectId || projectParam === effectiveProjectId) {
      return;
    }
    patchParams({ projectId: effectiveProjectId }, true);
  }, [view, projectsLoading, effectiveProjectId, projectParam, patchParams]);

  React.useEffect(() => {
    if (view !== 'members' || groupsLoading || !effectiveGroupId || groupParam === effectiveGroupId) {
      return;
    }
    setCurrentGroupId(effectiveGroupId);
    patchParams({ groupId: effectiveGroupId }, true);
  }, [view, groupsLoading, effectiveGroupId, groupParam, patchParams, setCurrentGroupId]);

  const {
    payload: projectPayload,
    loading: projectLoading,
    error: projectError,
    reload: reloadProject,
    reloadAsync: reloadProjectAsync,
  } = useProjectGantt(view === 'projects' && effectiveProjectId ? effectiveProjectId : null);

  const {
    payload: planPayload,
    loading: planLoading,
    error: planError,
    reload: reloadPlan,
    reloadAsync: reloadPlanAsync,
  } = usePlanView({
    groupId: effectiveGroupId || null,
    query: GANTT_PLAN_QUERY,
    enabled: view === 'members' && Boolean(effectiveGroupId),
  });

  const memberOptions = React.useMemo(
    () => collectGanttMembers(planPayload?.tasks ?? []),
    [planPayload],
  );

  // URL 里的成员已不在当前组时清掉，避免页面看起来"没有任务"。
  // 注意必须等 `planPayload` 到达后再判断：首帧 `planLoading` 还是 false、
  // `memberOptions` 也为空，如果这时就清，会把 URL 里刚带进来的 memberId 误删。
  React.useEffect(() => {
    if (
      view !== 'members' ||
      !memberParam ||
      planLoading ||
      !planPayload ||
      memberOptions.length === 0 ||
      memberOptions.some((member) => member.id === memberParam)
    ) {
      return;
    }
    patchParams({ memberId: null }, true);
  }, [view, memberParam, planLoading, planPayload, memberOptions, patchParams]);

  const rows = React.useMemo(() => {
    if (view === 'projects') {
      return projectPayload
        ? buildProjectGanttRows(projectPayload.project, projectPayload.tasks)
        : [];
    }
    return buildMemberGanttRows(planPayload?.tasks ?? [], memberParam || null);
  }, [view, projectPayload, planPayload, memberParam]);

  const visibleRows = React.useMemo(
    () => filterCollapsedRows(rows, collapsedIds),
    [rows, collapsedIds],
  );

  const dataKey =
    view === 'projects'
      ? `project:${projectPayload?.project.id ?? ''}:${scale}`
      : `group:${effectiveGroupId}:${memberParam}:${scale}`;

  React.useEffect(() => {
    setCollapsedIds(new Set());
  }, [dataKey]);

  const toggleRow = React.useCallback((rowId: string) => {
    if (interactionBusy.current) return;
    setCollapsedIds((previous) => {
      const next = new Set(previous);
      if (next.has(rowId)) {
        next.delete(rowId);
      } else {
        next.add(rowId);
      }
      return next;
    });
  }, []);

  const openTask = React.useCallback(
    (nextTaskId: string) => patchParams({ taskId: nextTaskId }),
    [patchParams],
  );
  const closeTask = React.useCallback(
    () => patchParams({ taskId: null }, true),
    [patchParams],
  );

  const applyProjectName = React.useCallback(() => {
    const name = projectNameDraft.trim();
    // 名称变化后让 effectiveProjectId 重新命中结果集；当前项目若仍在结果里会保留
    patchParams({ projectName: name || null, taskId: null });
  }, [projectNameDraft, patchParams]);

  const resetProjectName = React.useCallback(() => {
    setProjectNameDraft('');
    patchParams({ projectName: null, taskId: null });
  }, [patchParams]);

  const summary = view === 'projects' ? projectPayload?.summary : planPayload?.summary;
  const loading = view === 'projects' ? projectLoading : planLoading;
  const dataError = view === 'projects' ? projectError : planError;
  const optionsError = view === 'projects' ? projectsError : groupsError;
  const reload = view === 'projects' ? reloadProject : reloadPlan;
  const today = todayIn(userTimeZone);
  const editableAvailable = !loading && !dataError && !optionsError && rows.some((row) => row.kind === 'task' && row.task.can_edit);
  const editKey = `${view}:${effectiveProjectId}:${projectNameParam}:${effectiveGroupId}:${memberParam}:${scale}`;
  const edit = useGanttEdit(editKey, editableAvailable, view === 'projects' ? reloadProjectAsync : reloadPlanAsync);
  interactionBusy.current = edit.busy;
  React.useEffect(() => { if (taskId && edit.enabled && !edit.busy) edit.lock(); }, [taskId, edit.enabled, edit.busy, edit.lock]);
  const openTaskFromChart = (id: string) => { if (!edit.busy) { edit.lock(); openTask(id); } };

  React.useEffect(() => {
    if (dataError) {
      handle(dataError, { silent: true });
    }
  }, [dataError, handle]);

  const activeTask = React.useMemo(() => {
    if (!taskId) {
      return null;
    }
    const tasks = view === 'projects' ? (projectPayload?.tasks ?? []) : (planPayload?.tasks ?? []);
    return tasks.find((task) => task.id === taskId) ?? null;
  }, [taskId, view, projectPayload, planPayload]);

  const editTaskPath = React.useMemo(() => {
    if (!edit.draft) return '';
    const tasks = view === 'projects' ? projectPayload?.tasks ?? [] : planPayload?.tasks ?? [];
    const names = [edit.draft.task.name];
    const seen = new Set([edit.draft.task.id]);
    let parentId = edit.draft.task.parent_id;
    while (parentId && !seen.has(parentId)) {
      seen.add(parentId);
      const parent = tasks.find(task => task.id === parentId);
      if (!parent) break; // Never fetch or reveal a hidden ancestor for the label.
      names.unshift(parent.name); parentId = parent.parent_id;
    }
    return names.join(' / ');
  }, [edit.draft, view, projectPayload, planPayload]);

  const noProject = view === 'projects' && !projectsLoading && projectOptions.length === 0;
  const noGroup = view === 'members' && !groupsLoading && groupOptions.length === 0;

  return (
    <PageContainer
      title="视图"
      breadcrumbs={[{ title: '工作台', path: '/projects' }, { title: '视图' }]}
      actions={
        <Button
          variant="outlined"
          startIcon={<RefreshIcon />}
          onClick={() => { edit.lock(); reload(); }}
          disabled={edit.busy || loading || noProject || noGroup}
        >
          刷新
        </Button>
      }
    >
      <Stack spacing={2}>
        <Box
          component="form"
          onSubmit={(event) => {
            event.preventDefault();
            if (view === 'projects') {
              applyProjectName();
            }
          }}
          sx={{ pb: 2, borderBottom: 1, borderColor: 'divider' }}
        >
          <Box component="fieldset" disabled={edit.busy} sx={{ border: 0, p: 0, m: 0, minWidth: 0 }}>
          <Stack spacing={1.5}>
            <Tabs
              value={view}
              onChange={(_event, next: GanttViewMode) =>
                patchParams({ view: next === 'members' ? 'members' : null, memberId: null, taskId: null })
              }
              aria-label="甘特图类型"
            >
              <Tab value="projects" label="项目视图" disabled={edit.busy} />
              <Tab value="members" label="成员视图" disabled={edit.busy} />
            </Tabs>

            <Box sx={filterRowSx(false)}>
              {view === 'projects' ? (
                <>
                  <TextField
                    size="small"
                    label="项目名称"
                    disabled={edit.busy}
                    placeholder="包含匹配"
                    value={projectNameDraft}
                    onChange={(event) => setProjectNameDraft(event.target.value)}
                    sx={filterItemSx(200, 380)}
                  />
                  <TextField
                    select
                    size="small"
                    label="项目"
                    value={effectiveProjectId}
                    onChange={(event) => patchParams({ projectId: event.target.value, taskId: null })}
                    disabled={edit.busy || projectsLoading || projectOptions.length === 0}
                    sx={filterItemSx(220, 360)}
                    slotProps={{
                      inputLabel: { shrink: true },
                      select: {
                        displayEmpty: true,
                        renderValue: (selected: unknown) =>
                          projectOptions.find((project) => project.id === selected)?.name ??
                          (projectsLoading ? '加载中…' : '暂无项目'),
                      },
                    }}
                  >
                    {projectOptions.map((project) => (
                      <MenuItem key={project.id} value={project.id}>
                        {project.name}
                      </MenuItem>
                    ))}
                  </TextField>
                </>
              ) : (
                <>
                  <TextField
                    select
                    size="small"
                    label="工作组"
                    value={effectiveGroupId}
                    onChange={(event) => {
                      const nextGroupId = event.target.value;
                      setCurrentGroupId(nextGroupId || null);
                      patchParams({ groupId: nextGroupId, memberId: null, taskId: null });
                    }}
                    disabled={edit.busy || groupsLoading || groupOptions.length === 0}
                    sx={filterItemSx(180, 300)}
                    slotProps={{
                      inputLabel: { shrink: true },
                      select: {
                        displayEmpty: true,
                        renderValue: (selected: unknown) =>
                          groupOptions.find((group) => group.id === selected)?.name ??
                          (groupsLoading ? '加载中…' : '暂无工作组'),
                      },
                    }}
                  >
                    {groupOptions.map((group) => (
                      <MenuItem key={group.id} value={group.id}>
                        {group.name}
                      </MenuItem>
                    ))}
                  </TextField>

                  <TextField
                    select
                    size="small"
                    label="成员"
                    value={memberParam}
                    onChange={(event) => patchParams({ memberId: event.target.value || null, taskId: null })}
                    disabled={edit.busy || planLoading || memberOptions.length === 0}
                    sx={filterItemSx(160, 260)}
                    slotProps={{
                      inputLabel: { shrink: true },
                      select: {
                        displayEmpty: true,
                        renderValue: (selected: unknown) =>
                          memberOptions.find((member) => member.id === selected)?.display_name ??
                          '全部成员',
                      },
                    }}
                  >
                    <MenuItem value="">全部成员</MenuItem>
                    {memberOptions.map((member) => (
                      <MenuItem key={member.id} value={member.id}>
                        {member.display_name}
                        {member.username ? `（${member.username}）` : ''}
                      </MenuItem>
                    ))}
                  </TextField>
                </>
              )}

              <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, ml: 'auto' }}>
                <Typography variant="caption" color="text.secondary">
                  时间尺度
                </Typography>
                <ToggleButtonGroup
                  exclusive
                  disabled={edit.busy}
                  size="small"
                  value={scale}
                  onChange={(_event, next: GanttScale | null) => {
                    if (next) {
                      patchParams({ scale: next === 'week' ? null : next });
                    }
                  }}
                  aria-label="时间尺度"
                >
                  {SCALE_VALUES.map((value) => (
                    <ToggleButton key={value} value={value} sx={{ px: 1.5 }}>
                      {GANTT_SCALE_META[value].label}
                    </ToggleButton>
                  ))}
                </ToggleButtonGroup>
              </Box>
            </Box>

            {view === 'projects' ? (
              <Box sx={filterRowSx(true)}>
                <Button type="submit" variant="contained" startIcon={<SearchIcon />}>
                  查询
                </Button>
                <Button
                  type="button"
                  variant="outlined"
                  startIcon={<RestartAltIcon />}
                  onClick={resetProjectName}
                >
                  重置
                </Button>
              </Box>
            ) : null}
          </Stack>
          </Box>
        </Box>

        <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
          <ToggleButton value="edit" selected={edit.enabled} aria-pressed={edit.enabled} data-gantt-edit-toggle
            size="small" disabled={edit.busy || !editableAvailable}
            onClick={() => { if (taskId) closeTask(); edit.toggle(); }}>允许编辑</ToggleButton>
          <Typography variant="body2" color="text.secondary">{edit.enabled ? '已启用编辑模式' : '只读模式'}{!editableAvailable && !loading ? ' · 当前没有可编辑任务或数据不可用' : ''}</Typography>
        </Stack>
        {edit.message ? <Alert severity={edit.message.startsWith('修改已保存') ? 'success' : 'info'}>{edit.message}</Alert> : null}
        <Alert severity="info" variant="outlined" icon={false}>
          点击任务条或左侧任务名查看详情；开启“允许编辑”后，条外手柄改日期，条内竖柄改进度，主体拖动平移。短条和触屏使用行内编辑入口；每笔修改均需确认。
        </Alert>

        {optionsError ? (
          <Alert
            severity="error"
            action={
              <Button
                color="inherit"
                size="small"
                onClick={view === 'projects' ? reloadProjects : reloadGroups}
              >
                重试
              </Button>
            }
          >
            {describeApiError(optionsError)}
          </Alert>
        ) : null}

        {dataError ? (
          <Alert
            severity={dataError.isNotFound ? 'info' : 'error'}
            action={
              <Button color="inherit" size="small" onClick={reload}>
                重试
              </Button>
            }
          >
            {describeApiError(dataError)}
          </Alert>
        ) : null}

        {noProject ? (
          <Alert severity="info" variant="outlined">
            {projectNameParam
              ? `没有名称包含「${projectNameParam}」的可查看项目。可以清空名称后重新查询。`
              : '当前没有可查看的项目。项目甘特图只列出你有读权限的项目。'}
          </Alert>
        ) : null}

        {noGroup ? (
          <Alert severity="info" variant="outlined">
            当前没有可查看的工作组。成员视图按工作组的已共享任务展示。
          </Alert>
        ) : null}

        <Stack direction="row" spacing={1} sx={{ flexWrap: 'wrap', alignItems: 'center', rowGap: 1 }}>
          <Chip size="small" label={`任务 ${summary?.task_count ?? 0}`} />
          <Chip size="small" label={`进行中 ${summary?.in_progress_task_count ?? 0}`} color="primary" variant="outlined" />
          <Chip
            size="small"
            label={`逾期 ${summary?.overdue_task_count ?? 0}`}
            color={(summary?.overdue_task_count ?? 0) > 0 ? 'error' : 'default'}
            variant="outlined"
          />
          <Chip size="small" label={`总体进度 ${summary?.overall_progress ?? 0}%`} />
          <Typography variant="caption" color="text.secondary" sx={{ ml: 1 }}>
            灰=未开始　蓝=进行中　绿=已完成　红框=逾期　◆=里程碑
          </Typography>
        </Stack>

        <GanttChart
          edit={edit}
          rows={visibleRows}
          scale={scale}
          labelHeader={view === 'projects' ? '项目 / 任务' : '成员 / 任务'}
          today={today}
          collapsedIds={collapsedIds}
          onToggleRow={toggleRow}
          onOpenTask={openTaskFromChart}
          onOpenProject={
            view === 'projects' ? (projectId) => { if (!edit.busy) { edit.lock(); navigate(`/projects/${projectId}`); } } : undefined
          }
          activeTaskId={taskId}
          emptyHint={
            view === 'projects'
              ? noProject
                ? projectNameParam
                  ? '没有匹配的项目'
                  : '暂无可查看的项目'
                : '这个项目还没有可见任务'
              : '这个工作组还没有已共享的任务'
          }
        />

        <Box sx={{ pt: 0.5, pb: { xs: 2, md: 3 } }}>
          <Typography variant="caption" color="text.secondary" component="p">
            {view === 'projects'
              ? '项目视图按父子任务层级展示；任务条颜色表示状态，深色覆盖部分为已完成进度，菱形为里程碑。'
              : '多人参与的任务会在每位成员名下显示；顶部任务总数不重复计算。'}
          </Typography>
          <Typography variant="caption" color="text.secondary" component="p" sx={{ mt: 0.5 }}>
            编辑时拖动仅预览；松开后确认才保存。父任务进度由下级汇总，修改日期不会自动移动子任务。
          </Typography>
        </Box>
      </Stack>

      <GanttEditDialog edit={edit} taskPath={editTaskPath} projectName={view === 'projects' ? projectPayload?.project.name ?? '' : planPayload?.projects.find((project) => project.id === edit.draft?.task.project_id)?.name ?? ''} />
      <TaskDetailDrawer
        key={taskId ?? 'none'}
        taskId={taskId}
        canEdit={activeTask?.can_edit}
        onChanged={reload}
        onClose={closeTask}
      />
    </PageContainer>
  );
}
