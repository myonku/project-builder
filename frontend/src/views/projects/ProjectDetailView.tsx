/**
 * 项目详情（`/projects/:projectId`）——F05 §7.2 第 2 步。
 *
 * 本页承担三件事：
 * 1. **版本协议**：`GET /projects/{id}` 拿到的 `updated_at` 由 `useVersionedForm` 原样缓存，
 *    PATCH 时注入 `expected_updated_at`；`409 version_conflict` 由 Hook 弹窗让用户选
 *    「放弃我的修改并刷新」/「保留我的修改」，**不自动重试、不静默合并**（F01 §5.3）；
 * 2. **归档只读**：`status === 'archived' || archived_at !== null` 时**提前禁用**所有写入口
 *    （F01 §6 第 7 条）；归档动作本身**不可逆**，因此要二次确认（《A04》§端点 5）；
 * 3. **任务列表 + 任务抽屉**：`POST /projects/{id}/tasks/query` 复用项目列表那套
 *    `useListQuery` 结构（F01 §5.9）；行点击用 `?taskId=` 打开 `TaskDetailDrawer`，
 *    关掉即回到本页，筛选与滚动位置天然保留（F01 §5.7）。
 *
 * 权限判定一律走 `usePermission`：不要用 `membership_role` 自行推断
 * （F01 §6 第 10 条），也不要依赖计划视图的 `can_manage_project`。
 */

import * as React from 'react';
import Alert from '@mui/material/Alert';
import AlertTitle from '@mui/material/AlertTitle';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Divider from '@mui/material/Divider';
import FormControlLabel from '@mui/material/FormControlLabel';
import IconButton from '@mui/material/IconButton';
import LinearProgress from '@mui/material/LinearProgress';
import MenuItem from '@mui/material/MenuItem';
import Stack from '@mui/material/Stack';
import Switch from '@mui/material/Switch';
import TextField from '@mui/material/TextField';
import Tooltip from '@mui/material/Tooltip';
import Typography from '@mui/material/Typography';
import AddIcon from '@mui/icons-material/Add';
import ArchiveIcon from '@mui/icons-material/Archive';
import EditIcon from '@mui/icons-material/Edit';
import RefreshIcon from '@mui/icons-material/Refresh';
import VisibilityIcon from '@mui/icons-material/Visibility';
import { DataGrid } from '@mui/x-data-grid';
import type { GridColDef, GridRenderCellParams } from '@mui/x-data-grid';
import { zhCN } from '@mui/x-data-grid/locales';
import { useNavigate, useParams, useSearchParams } from 'react-router';
import { archiveProject, getProject, updateProject } from '@/api/projects';
import { createTask, queryProjectTasks } from '@/api/tasks';
import { toApiError, type ApiError } from '@/api/client';
import CellText from '@/components/common/CellText';
import ProgressCell from '@/components/common/ProgressCell';
import FullPageLoading from '@/components/common/FullPageLoading';
import PageContainer from '@/components/common/PageContainer';
import StatusTag from '@/components/common/StatusTag';
import ProjectFormDialog, {
  type ProjectFormValues,
} from '@/components/project/ProjectFormDialog';
import {
  projectMembershipMeta,
  projectStatusMeta,
  projectVisibilityMeta,
} from '@/components/project/projectMeta';
import ProjectMembersPanel from '@/components/project/ProjectMembersPanel';
import TaskDetailDrawer from '@/components/task/TaskDetailDrawer';
import TaskFormDialog, {
  type TaskFormValues,
} from '@/components/task/TaskFormDialog';
import { TASK_STATUS_OPTIONS, taskPriorityMeta, taskStatusMeta } from '@/components/task/taskMeta';
import { useApiError } from '@/hooks/useApiError';
import { useDialogs } from '@/hooks/useDialogs';
import { useGroupOptions } from '@/hooks/useGroupOptions';
import { useListQuery } from '@/hooks/useListQuery';
import { useNotifications } from '@/hooks/useNotifications';
import { usePermission } from '@/hooks/usePermission';
import { useVersionedForm } from '@/hooks/useVersionedForm';
import { useAuthStore } from '@/stores/auth';
import type { Project, UpdateProjectBody } from '@/types/project';
import type { CreateTaskBody, Task, TaskQueryBody, TaskStatus } from '@/types/task';
import { formatForDisplay, formatNaturalDate } from '@/utils/datetime';

/** 任务列表的排序白名单：与《A05》的 `TaskQueryBody.sort` 逐一对应 */
const TASK_SORT_WHITELIST = [
  'name',
  'status',
  'progress',
  'priority',
  'start_date',
  'end_date',
  'created_at',
  'updated_at',
] as const;

interface TaskFilters {
  status: TaskStatus | '';
  includeDeleted: boolean;
}

const INITIAL_TASK_FILTERS: TaskFilters = { status: '', includeDeleted: false };

/** 只把**用户实际改动过**的字段放进 PATCH；空串表示清空（《A04》§端点 4） */
function buildProjectPatch(
  values: ProjectFormValues,
  project: Project,
): Omit<UpdateProjectBody, 'expected_updated_at'> {
  const patch: Omit<UpdateProjectBody, 'expected_updated_at'> = {};
  if (values.name !== project.name) {
    patch.name = values.name;
  }
  if (values.description !== (project.description ?? '')) {
    patch.description = values.description;
  }
  if (values.color !== (project.color ?? '')) {
    patch.color = values.color;
  }
  if (values.visibility !== project.visibility) {
    patch.visibility = values.visibility;
  }
  if (values.start_date !== project.start_date) {
    patch.start_date = values.start_date;
  }
  if (values.end_date !== project.end_date) {
    patch.end_date = values.end_date;
  }
  return patch;
}

/** 项目本体的版本协议（抽出来只是为了让主组件短一点；形状与其它详情页一致） */
function useProjectResource(projectId: string) {
  return useVersionedForm<Project, UpdateProjectBody>({
    load: (signal) => getProject(projectId, signal),
    save: (body) => updateProject(projectId, body),
    autoLoad: Boolean(projectId),
  });
}

export default function ProjectDetailView() {
  const { projectId = '' } = useParams();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const taskId = searchParams.get('taskId');
  const dialogs = useDialogs();
  const { show } = useNotifications();
  const { handle, describe } = useApiError();
  const permission = usePermission();
  const userTimeZone = useAuthStore((state) => state.user?.timezone);
  const { options: groupOptions, nameById: groupNameById } = useGroupOptions();

  const [editOpen, setEditOpen] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [saveError, setSaveError] = React.useState<ApiError | null>(null);
  const [taskFilters, setTaskFilters] = React.useState<TaskFilters>(INITIAL_TASK_FILTERS);
  const [createTaskOpen, setCreateTaskOpen] = React.useState(false);
  const [creatingTask, setCreatingTask] = React.useState(false);
  const [createTaskError, setCreateTaskError] = React.useState<ApiError | null>(null);


  const {
    entity: project,
    loading: projectLoading,
    error: projectError,
    load: reloadProject,
    save: saveProject,
  } = useProjectResource(projectId);

  React.useEffect(() => {
    if (projectError) {
      handle(projectError, { silent: true });
    }
  }, [projectError, handle]);

  const canManage = project ? permission.canManageProject(project) : false;
  // 任务与任务成员的写权限依据是「项目权限 + 任务是否共享」（F01 §6 第 8 条），与"是否是任务成员"无关
  const canWriteTasks = project
    ? permission.canWriteTasksOfProject(project) && !permission.isProjectReadOnly(project)
    : false;

  const readOnly = project ? permission.isProjectReadOnly(project) : false;
  const statusMeta = projectStatusMeta(project?.status);
  const visibilityMeta = projectVisibilityMeta(project?.visibility);
  const membershipMeta = projectMembershipMeta(project?.membership_role ?? null);

  const {
    rows: tasks,
    rowCount,
    loading: tasksLoading,
    error: tasksError,
    paginationModel,
    onPaginationModelChange,
    sortModel,
    onSortModelChange,
    setFilters: applyTaskFilters,
    reload: reloadTasks,
  } = useListQuery<Task, TaskFilters>({
    query: ({ filters, page, pageSize, sort }) => {
      const body: TaskQueryBody = { page, page_size: pageSize };
      if (filters.status) {
        body.status = filters.status;
      }
      if (filters.includeDeleted) {
        body.include_deleted = true;
      }
      if (sort) {
        body.sort = sort as TaskQueryBody['sort'];
      }
      return queryProjectTasks(projectId, body);
    },
    initialFilters: INITIAL_TASK_FILTERS,
    sortWhitelist: TASK_SORT_WHITELIST,
    enabled: Boolean(projectId),
    deps: [projectId],
  });

  React.useEffect(() => {
    if (tasksError) {
      handle(tasksError, { silent: true });
    }
  }, [tasksError, handle]);

  const handleSave = React.useCallback(
    async (values: ProjectFormValues) => {
      if (!project) {
        return;
      }
      const patch = buildProjectPatch(values, project);
      if (Object.keys(patch).length === 0) {
        show('没有需要保存的修改。', { severity: 'info' });
        setEditOpen(false);
        return;
      }
      setSaving(true);
      setSaveError(null);
      const outcome = await saveProject(patch);
      setSaving(false);
      if (outcome.ok) {
        setEditOpen(false);
        show('项目已保存。', { severity: 'success' });
        return;
      }
      if (outcome.resolution === 'reloaded') {
        // 用户选了"放弃我的修改并刷新"：Hook 已重新加载，直接关掉对话框
        setEditOpen(false);
        show('已刷新为服务端最新数据。', { severity: 'info' });
        return;
      }
      // 'kept'：版本号已刷新，保留用户输入让他再提交一次；'aborted'：其它错误
      setSaveError(outcome.error);
      if (outcome.resolution === 'kept') {
        show('版本号已刷新为服务端最新值，可以再次提交。', { severity: 'warning' });
      }
    },
    [project, saveProject, show],
  );

  const handleArchive = React.useCallback(async () => {
    if (!project) {
      return;
    }
    const confirmed = await dialogs.confirm(
      `归档后该项目及其下任务将变为只读，而且没有解除归档的端点（《A04》§端点 5）。确认归档「${project.name}」吗？`,
      { title: '归档项目', okText: '确认归档', cancelText: '取消', severity: 'warning' },
    );
    if (!confirmed) {
      return;
    }
    try {
      await archiveProject(project.id, { expected_updated_at: project.updated_at });
      show('项目已归档，现在只读。', { severity: 'success' });
    } catch (error) {
      const apiError = toApiError(error);
      handle(apiError, {
        message: apiError.isVersionConflict
          ? '项目已被其他人修改，已为你刷新，请确认后再归档。'
          : undefined,
      });
    } finally {
      // 无论成功还是冲突，都重新加载：让 status / archived_at / updated_at 回到服务端口径
      await reloadProject();
    }
  }, [dialogs, handle, project, reloadProject, show]);

  /** 新建任务：POST /projects/{id}/tasks（不接受 status/progress，新任务固定未开始 0%） */
  const handleCreateTask = React.useCallback(
    async (values: TaskFormValues) => {
      setCreatingTask(true);
      setCreateTaskError(null);
      const body: CreateTaskBody = {
        name: values.name,
        start_date: values.start_date,
        end_date: values.end_date,
        priority: values.priority as CreateTaskBody['priority'],
        is_milestone: values.is_milestone,
        is_shared: values.is_shared,
      };
      if (values.description.trim()) {
        body.description = values.description;
      }
      if (values.parent_id) {
        body.parent_id = values.parent_id;
      }
      try {
        const created = await createTask(projectId, body);
        setCreateTaskOpen(false);
        reloadTasks();
        void reloadProject();
        show(`任务「${created.name}」已创建。`, { severity: 'success' });
      } catch (error) {
        const apiError = toApiError(error);
        setCreateTaskError(apiError);
        if (Object.keys(apiError.fields).length === 0) {
          handle(apiError, { silent: false });
        }
      } finally {
        setCreatingTask(false);
      }
    },
    [handle, projectId, reloadProject, reloadTasks, show],
  );
  const openTask = React.useCallback(
    (nextTaskId: string) => {
      const next = new URLSearchParams(searchParams);
      next.set('taskId', nextTaskId);
      setSearchParams(next);
    },
    [searchParams, setSearchParams],
  );

  const closeTask = React.useCallback(() => {
    const next = new URLSearchParams(searchParams);
    next.delete('taskId');
    setSearchParams(next, { replace: true });
  }, [searchParams, setSearchParams]);

  const taskColumns = React.useMemo<GridColDef<Task>[]>(
    () => [
      {
        field: 'name',
        headerName: '任务名称',
        flex: 1,
        minWidth: 200,
        renderCell: (params: GridRenderCellParams<Task, string>) => (
          <Box
            component="span"
            sx={{ fontWeight: 500, cursor: 'pointer', '&:hover': { textDecoration: 'underline' } }}
          >
            {params.value}
          </Box>
        ),
      },
      {
        field: 'status',
        headerName: '状态',
        width: 110,
        renderCell: (params: GridRenderCellParams<Task, TaskStatus>) => {
          const meta = taskStatusMeta(params.value);
          return <StatusTag label={meta.label} tone={meta.tone} />;
        },
      },
      {
        field: 'priority',
        headerName: '优先级',
        width: 100,
        renderCell: (params: GridRenderCellParams<Task, Task['priority']>) => {
          const meta = taskPriorityMeta(params.value);
          return <StatusTag label={meta.label} tone={meta.tone} variant="outlined" />;
        },
      },
      {
        field: 'progress',
        headerName: '进度',
        width: 130,
        // 进度条 + 文字是块级容器：必须自己撑满单元格才能垂直居中（F01 §6 第 28 条）
        renderCell: (params: GridRenderCellParams<Task, number>) => (
          <ProgressCell value={params.value} />
        ),
      },
      {
        field: 'start_date',
        headerName: '开始日期',
        width: 110,
        valueFormatter: (value) => formatNaturalDate(value as string | null),
      },
      {
        field: 'end_date',
        headerName: '结束日期',
        width: 110,
        valueFormatter: (value) => formatNaturalDate(value as string | null),
      },
      {
        field: 'is_shared',
        headerName: '共享',
        width: 90,
        sortable: false,
        renderCell: (params: GridRenderCellParams<Task, boolean>) =>
          params.value ? (
            <StatusTag label="已共享" tone="primary" variant="outlined" />
          ) : (
            <CellText muted>—</CellText>
          ),
      },
      {
        field: 'is_overdue',
        headerName: '逾期',
        width: 90,
        sortable: false,
        renderCell: (params: GridRenderCellParams<Task, boolean>) =>
          params.value ? <StatusTag label="已逾期" tone="error" /> : <CellText muted>—</CellText>,
      },
      {
        field: 'updated_at',
        headerName: '最近更新',
        width: 150,
        valueFormatter: (value) => formatForDisplay(value as string, userTimeZone),
      },
      {
        field: 'actions',
        headerName: '操作',
        width: 70,
        sortable: false,
        filterable: false,
        disableColumnMenu: true,
        renderCell: (params: GridRenderCellParams<Task>) => (
          <Tooltip title="打开任务详情">
            <IconButton
              size="small"
              aria-label={`打开任务 ${params.row.name}`}
              onClick={(event) => {
                event.stopPropagation();
                openTask(params.row.id);
              }}
            >
              <VisibilityIcon fontSize="small" />
            </IconButton>
          </Tooltip>
        ),
      },
    ],
    [openTask, userTimeZone],
  );

  const breadcrumbs = [
    { title: '概览', path: '/' },
    { title: '项目', path: '/projects' },
    { title: project ? project.name : '详情' },
  ];

  if (projectLoading && !project) {
    return (
      <PageContainer title="项目详情" breadcrumbs={breadcrumbs}>
        <FullPageLoading message="正在加载项目…" />
      </PageContainer>
    );
  }

  if (!project) {
    return (
      <PageContainer title="项目详情" breadcrumbs={breadcrumbs}>
        <Alert
          severity="info"
          action={
            <Button color="inherit" size="small" onClick={() => navigate('/projects')}>
              返回列表
            </Button>
          }
        >
          {projectError ? describe(projectError) : '没有找到这个项目。'}
        </Alert>
      </PageContainer>
    );
  }

  return (
    <PageContainer
      title={project.name}
      breadcrumbs={breadcrumbs}
      actions={
        <Stack direction="row" spacing={1}>
          <Tooltip title="重新加载项目与任务">
            <span>
              <Button
                variant="outlined"
                startIcon={<RefreshIcon />}
                onClick={() => {
                  void reloadProject();
                  reloadTasks();
                }}
                disabled={projectLoading}
              >
                刷新
              </Button>
            </span>
          </Tooltip>
          <Tooltip title={readOnly ? '归档项目只读，不能修改' : ''}>
            <span>
              <Button
                variant="contained"
                startIcon={<EditIcon />}
                onClick={() => {
                  setSaveError(null);
                  setEditOpen(true);
                }}
                disabled={!canManage || readOnly}
              >
                编辑
              </Button>
            </span>
          </Tooltip>
          <Tooltip title={readOnly ? '项目已归档' : '归档后只读且不可撤销'}>
            <span>
              <Button
                color="warning"
                variant="outlined"
                startIcon={<ArchiveIcon />}
                onClick={() => void handleArchive()}
                disabled={!canManage || readOnly}
              >
                归档
              </Button>
            </span>
          </Tooltip>
        </Stack>
      }
    >
      <Stack spacing={2}>
        {readOnly ? (
          <Alert severity="warning">
            <AlertTitle>项目已归档（只读）</AlertTitle>
            归档不可逆，项目及其下任务都不能再修改：《A04》§端点 5 没有提供解除归档的端点，
            写入一律返回 <code>409 conflict</code>。
          </Alert>
        ) : null}

        {!canManage && !readOnly ? (
          <Alert severity="info" variant="outlined">
            你在该项目里的角色是「{membershipMeta?.label ?? '非成员'}」；只有项目持有者或系统管理员
            能修改项目本身（《A04》§权限速查）。
          </Alert>
        ) : null}

        {projectError ? <Alert severity="error">{describe(projectError)}</Alert> : null}

        <Stack direction="row" spacing={1} sx={{ flexWrap: 'wrap', gap: 1 }}>
          <StatusTag label={statusMeta.label} tone={statusMeta.tone} />
          <StatusTag label={visibilityMeta.label} tone={visibilityMeta.tone} variant="outlined" />
          {project.overdue_task_count > 0 ? (
            <StatusTag label={`逾期任务 ${project.overdue_task_count}`} tone="error" />
          ) : null}
        </Stack>

        <Stack direction={{ xs: 'column', md: 'row' }} spacing={3}>
          <Stack spacing={1} sx={{ minWidth: 240 }}>
            <Typography variant="subtitle2" color="text.secondary">
              基本信息
            </Typography>
            <InfoRow label="工作组" value={groupNameById.get(project.group_id) ?? '—'} />
            <InfoRow label="开始日期" value={formatNaturalDate(project.start_date)} />
            <InfoRow label="结束日期" value={formatNaturalDate(project.end_date)} />
            <InfoRow label="我的角色" value={membershipMeta?.label ?? '非成员'} />
            <InfoRow label="颜色" value={project.color || '—'} />
          </Stack>
          <Stack spacing={1} sx={{ minWidth: 220 }}>
            <Typography variant="subtitle2" color="text.secondary">
              进度
            </Typography>
            <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
              <LinearProgress
                variant="determinate"
                value={project.progress}
                sx={{ flex: 1, height: 8, borderRadius: 4 }}
              />
              <Typography variant="body2">{project.progress}%</Typography>
            </Stack>
            <Typography variant="caption" color="text.secondary">
              派生字段：有效任务进度的平均值（《A04》）
            </Typography>
          </Stack>
          <Stack spacing={1} sx={{ minWidth: 240 }}>
            <Typography variant="subtitle2" color="text.secondary">
              审计信息
            </Typography>
            <InfoRow label="最近更新" value={formatForDisplay(project.updated_at, userTimeZone)} />
            <InfoRow
              label="归档时间"
              value={project.archived_at ? formatForDisplay(project.archived_at, userTimeZone) : '—'}
            />
            <InfoRow label="创建者" value={project.created_by} />
          </Stack>
        </Stack>

        {project.description ? (
          <Stack spacing={0.5}>
            <Typography variant="subtitle2" color="text.secondary">
              描述
            </Typography>
            <Typography variant="body2" sx={{ whiteSpace: 'pre-wrap' }}>
              {project.description}
            </Typography>
          </Stack>
        ) : null}

        <Divider />

        <ProjectMembersPanel
          projectId={project.id}
          groupId={project.group_id}
          canManage={canManage && !readOnly}
          onChanged={() => void reloadProject()}
        />

        <Divider />

        <Stack direction="row" spacing={2} sx={{ alignItems: 'center', flexWrap: 'wrap' }}>
          <Typography variant="h6" component="h2">
            任务
          </Typography>
          <Tooltip title={canWriteTasks ? '' : '你在这个项目上没有写任务的权限，或项目已归档'}>
            <span>
              <Button
                size="medium"
                variant="contained"
                startIcon={<AddIcon />}
                disabled={!canWriteTasks}
                onClick={() => {
                  setCreateTaskError(null);
                  setCreateTaskOpen(true);
                }}
              >
                新建任务
              </Button>
            </span>
          </Tooltip>
          <TextField
            select
            size="small"
            label="状态"
            value={taskFilters.status}
            onChange={(event) => {
              const next = { ...taskFilters, status: event.target.value as TaskStatus | '' };
              setTaskFilters(next);
              applyTaskFilters(next);
            }}
            sx={{ minWidth: 150 }}
          >
            <MenuItem value="">全部</MenuItem>
            {TASK_STATUS_OPTIONS.map((option) => (
              <MenuItem key={option.value} value={option.value}>
                {option.label}
              </MenuItem>
            ))}
          </TextField>
          <FormControlLabel
            control={
              <Switch
                checked={taskFilters.includeDeleted}
                onChange={(event) => {
                  const next = { ...taskFilters, includeDeleted: event.target.checked };
                  setTaskFilters(next);
                  applyTaskFilters(next);
                }}
              />
            }
            label="含已删除"
          />
          <Typography variant="body2" color="text.secondary">
            共 {rowCount} 个任务
          </Typography>
        </Stack>

        {tasksError ? (
          <Alert
            severity="error"
            action={
              <Button color="inherit" size="small" onClick={reloadTasks}>
                重试
              </Button>
            }
          >
            {describe(tasksError)}
          </Alert>
        ) : null}

        <Box
          sx={{
            height: 'clamp(320px, calc(100dvh - 520px), 620px)',
            display: 'flex',
            flexDirection: 'column',
          }}
        >
          <DataGrid<Task>
            rows={tasks}
            columns={taskColumns}
            getRowId={(row) => row.id}
            loading={tasksLoading}
            paginationMode="server"
            sortingMode="server"
            filterMode="server"
            rowCount={rowCount}
            paginationModel={paginationModel}
            onPaginationModelChange={onPaginationModelChange}
            pageSizeOptions={[10, 20, 50, 100]}
            sortModel={sortModel}
            onSortModelChange={onSortModelChange}
            disableColumnFilter
            disableRowSelectionOnClick
            // 开始日期 / 最近更新默认收起，保证常用列在 1440 视口内不横向滚动（列菜单可打开）
            initialState={{
              columns: {
                columnVisibilityModel: { start_date: false, updated_at: false },
              },
            }}
            onRowClick={(params) => openTask(params.row.id)}
            sx={{
              flex: 1,
              '& .MuiDataGrid-row': { cursor: 'pointer' },
              '& .MuiDataGrid-virtualScroller': { scrollbarGutter: 'stable' },
            }}
            localeText={zhCN.components.MuiDataGrid.defaultProps.localeText}
          />
        </Box>
      </Stack>

      <ProjectFormDialog
        open={editOpen}
        mode="edit"
        project={project}
        groups={groupOptions}
        submitting={saving}
        serverError={saveError}
        onClose={() => setEditOpen(false)}
        onSubmit={(values) => void handleSave(values)}
      />

      {/* key=taskId：换任务时重新挂载，让 Hook 的自动加载只跑一次（见组件头注释） */}
      <TaskDetailDrawer
        key={taskId ?? 'none'}
        taskId={taskId}
        canEdit={canWriteTasks}
        onChanged={reloadTasks}
        onClose={closeTask}
      />

      <TaskFormDialog
        open={createTaskOpen}
        mode="create"
        projectId={projectId}
        submitting={creatingTask}
        serverError={createTaskError}
        onClose={() => setCreateTaskOpen(false)}
        onSubmit={(values) => void handleCreateTask(values)}
      />
    </PageContainer>
  );
}

function InfoRow({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <Stack direction="row" spacing={2} sx={{ justifyContent: 'space-between' }}>
      <Typography variant="body2" color="text.secondary" sx={{ flexShrink: 0 }}>
        {label}
      </Typography>
      <Typography variant="body2" sx={{ wordBreak: 'break-all', textAlign: 'right' }}>
        {value}
      </Typography>
    </Stack>
  );
}
