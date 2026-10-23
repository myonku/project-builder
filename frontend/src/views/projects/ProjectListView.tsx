/**
 * 项目列表（`/projects`）——F05 §7.2 第 1 步：`useListQuery` 的第一个真实使用者。
 *
 * 这个页面要证明四件事（《F01》§5.4、《F02》§2.1）：
 * 1. **服务器模式分页**：`paginationMode` / `sortingMode` / `filterMode` 全部 `"server"`，
 *    `rowCount` 取 `pagination.total`（**不是** `rows.length`），`page` 的 0/1 基换算只在
 *    `useListQuery` 里做；
 * 2. **排序白名单**：只有《A04》允许的字段可排序，其余列 `sortable: false`——
 *    让用户点不出一个必然 400 的排序；
 * 3. **筛选由页面自己维护**：不透传 DataGrid 的 `filterModel`（后端没有该协议），
 *    筛选条件通过 `useListQuery` 的 `filters` 传下去；
 * 4. **归档项目**：默认不出现，勾选"含归档"才查得到（《A04》§端点 2）。
 *
 * 本轮**不做**新建/编辑：它们与项目详情共用同一个对象与版本协议，
 * 留给 F05 §7.2 第 2 步一起做，避免这里出现"点了没反应"的假按钮。
 */

import * as React from 'react';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import FormControlLabel from '@mui/material/FormControlLabel';
import IconButton from '@mui/material/IconButton';
import MenuItem from '@mui/material/MenuItem';
import Stack from '@mui/material/Stack';
import Switch from '@mui/material/Switch';
import TextField from '@mui/material/TextField';
import AddIcon from '@mui/icons-material/Add';
import RefreshIcon from '@mui/icons-material/Refresh';
import RestartAltIcon from '@mui/icons-material/RestartAlt';
import SearchIcon from '@mui/icons-material/Search';
import VisibilityIcon from '@mui/icons-material/Visibility';
import { DataGrid } from '@mui/x-data-grid';
import type { GridColDef, GridRenderCellParams } from '@mui/x-data-grid';
import { zhCN } from '@mui/x-data-grid/locales';
import { useNavigate } from 'react-router';
import { createProject, queryProjects } from '@/api/projects';
import { toApiError, type ApiError } from '@/api/client';
import CellText from '@/components/common/CellText';
import ProgressCell from '@/components/common/ProgressCell';
import DataTooltip, { textCell } from '@/components/common/DataTooltip';
import { filterItemSx, filterRowSx } from '@/components/common/filterLayout';
import ProjectFormDialog, {
  type ProjectFormValues,
} from '@/components/project/ProjectFormDialog';
import StatusTag from '@/components/common/StatusTag';
import PageContainer from '@/components/common/PageContainer';
import {
  PROJECT_STATUS_OPTIONS,
  projectMembershipMeta,
  projectStatusMeta,
  projectVisibilityMeta,
} from '@/components/project/projectMeta';
import { useApiError } from '@/hooks/useApiError';
import { useGroupOptions } from '@/hooks/useGroupOptions';
import { useListQuery } from '@/hooks/useListQuery';
import { useNotifications } from '@/hooks/useNotifications';
import { useAuthStore } from '@/stores/auth';
import type { CreateProjectBody, Project, ProjectQueryBody, ProjectStatus } from '@/types/project';
import { formatForDisplay, formatNaturalDate } from '@/utils/datetime';

/** 页面自有的筛选状态（与 DataGrid 的 filterModel 无关） */
interface ProjectFilters {
  /** 名称包含匹配（大小写不敏感），提交前 trim，空串不发送 */
  name: string;
  /** 空串 = 不按工作组筛选 */
  groupId: string;
  /** 空串 = 不按状态筛选 */
  status: ProjectStatus | '';
  includeArchived: boolean;
}

const INITIAL_FILTERS: ProjectFilters = {
  name: '',
  groupId: '',
  status: '',
  includeArchived: false,
};

/**
 * 排序字段白名单：与《A04》的 `ProjectQueryBody.sort` 逐一对应。
 * 漏掉一个会让用户点那一列就 400；多写一个同样 400——两边必须严格一致。
 */
const SORT_WHITELIST = [
  'name',
  'status',
  'visibility',
  'start_date',
  'end_date',
  'created_at',
  'updated_at',
] as const;

export default function ProjectListView() {
  const navigate = useNavigate();
  const { handle, describe } = useApiError();
  const userTimeZone = useAuthStore((state) => state.user?.timezone);
  const { options: groupOptions, nameById: groupNameById, loading: groupsLoading } =
    useGroupOptions();

  const { show } = useNotifications();
  const [filters, setFilters] = React.useState<ProjectFilters>(INITIAL_FILTERS);
  const [createOpen, setCreateOpen] = React.useState(false);
  const [creating, setCreating] = React.useState(false);
  const [createError, setCreateError] = React.useState<ApiError | null>(null);
  /** 文本框的草稿值：按"查询"或回车才生效，避免每敲一个字打一次请求 */
  const [nameDraft, setNameDraft] = React.useState('');

  const {
    rows,
    rowCount,
    loading,
    error,
    paginationModel,
    onPaginationModelChange,
    sortModel,
    onSortModelChange,
    setFilters: applyFilters,
    reload,
  } = useListQuery<Project, ProjectFilters>({
    query: ({ filters: active, page, pageSize, sort }) => {
      const body: ProjectQueryBody = {
        page,
        page_size: pageSize,
        include_archived: active.includeArchived,
      };
      const name = active.name.trim();
      if (name) {
        body.name = name;
      }
      if (active.groupId) {
        body.group_id = active.groupId;
      }
      if (active.status) {
        body.status = active.status;
      }
      if (sort) {
        // `useListQuery` 已按 SORT_WHITELIST 过滤过，这里只是把 string 收窄回契约的字面量联合
        body.sort = sort as ProjectQueryBody['sort'];
      }
      return queryProjects(body);
    },
    initialFilters: INITIAL_FILTERS,
    sortWhitelist: SORT_WHITELIST,
    deps: [],
  });

  // 401（会话失效）等由统一映射处理：跳登录页；其余错误在页面内用 Alert 呈现
  React.useEffect(() => {
    if (error) {
      handle(error, { silent: true });
    }
  }, [error, handle]);

  /**
   * 新建项目：`POST /projects`。
   *
   * 只把有值的字段放进请求体（空的 `description` / `color` 省略而不是发空串，
   * 避免"创建时顺手清空"这种无意义语义）；`422` 的 `fields` 由表单对话框回填到字段。
   */
  const handleCreate = React.useCallback(
    async (values: ProjectFormValues) => {
      setCreating(true);
      setCreateError(null);
      const body: CreateProjectBody = {
        name: values.name,
        group_id: values.group_id,
        start_date: values.start_date,
        end_date: values.end_date,
        visibility: values.visibility,
      };
      if (values.description.trim()) {
        body.description = values.description;
      }
      if (values.color.trim()) {
        body.color = values.color;
      }
      try {
        const created = await createProject(body);
        setCreateOpen(false);
        show(`项目「${created.name}」已创建。`, { severity: 'success' });
        // 直接进入新项目详情：创建后最可能要做的事就是加任务
        navigate(`/projects/${created.id}`);
      } catch (error) {
        const apiError = toApiError(error);
        setCreateError(apiError);
        // 没有字段明细时（403/409/网络）额外给一次全局提示，避免对话框里只有一句话不明显
        if (Object.keys(apiError.fields).length === 0) {
          handle(apiError, { silent: false });
        }
      } finally {
        setCreating(false);
      }
    },
    [handle, navigate, show],
  );

  /** 筛选变化一律走 setFilters：它会把页码重置回第 1 页 */
  const commitFilters = React.useCallback(
    (patch: Partial<ProjectFilters>) => {
      const next = { ...filters, ...patch };
      setFilters(next);
      setNameDraft(next.name);
      applyFilters(next);
    },
    [applyFilters, filters],
  );

  const handleReset = React.useCallback(() => {
    setFilters(INITIAL_FILTERS);
    setNameDraft('');
    applyFilters(INITIAL_FILTERS);
  }, [applyFilters]);

  /**
   * 列宽经过一次实测调整：默认可见列的宽度之和要能放进 1440 视口的内容区
   * （侧边栏 240 + 间距），否则"最近更新 / 操作"会被挤到横向滚动之外。
   * 「开始日期」「我的角色」默认隐藏，需要时用列菜单（⋮ → 管理列）打开。
   */
  const columns = React.useMemo<GridColDef<Project>[]>(
    () => [
      {
        field: 'name',
        headerName: '项目名称',
        flex: 1,
        minWidth: 200,
        // 名称列承担"打开详情"的入口，渲染成链接便于键盘操作
        renderCell: (params: GridRenderCellParams<Project, string>) => (
          <Box
            component="span"
            sx={{
              fontWeight: 500,
              cursor: 'pointer',
              '&:hover': { textDecoration: 'underline' },
            }}
            onClick={(event) => {
              event.stopPropagation();
              navigate(`/projects/${params.row.id}`);
            }}
          >
            {params.value}
          </Box>
        ),
      },
      {
        field: 'status',
        headerName: '状态',
        width: 110,
        renderCell: (params: GridRenderCellParams<Project, ProjectStatus>) => {
          const meta = projectStatusMeta(params.value);
          const tag = <StatusTag label={meta.label} tone={meta.tone} />;
          // 归档的精确时间放在 tooltip 里，避免再占一列（状态列已经显示"已归档"）
          return params.row.archived_at ? (
            <DataTooltip title={`归档于 ${formatForDisplay(params.row.archived_at, userTimeZone)}`}>
              <Box component="span">{tag}</Box>
            </DataTooltip>
          ) : (
            tag
          );
        },
      },
      {
        field: 'visibility',
        headerName: '共享级别',
        width: 110,
        renderCell: (params: GridRenderCellParams<Project, Project['visibility']>) => {
          const meta = projectVisibilityMeta(params.value);
          return <StatusTag label={meta.label} tone={meta.tone} variant="outlined" />;
        },
      },
      {
        field: 'group_id',
        headerName: '工作组',
        width: 140,
        sortable: false, // 不在排序白名单里
        valueGetter: (_value, row) =>
          groupNameById.get(row.group_id) ?? `${row.group_id.slice(0, 8)}…`,
        // 写了 renderCell 才会去掉 DataGrid 自动塞的原生 title（F01 §6 第 34 条）
        renderCell: textCell<Project>(),
      },
      {
        field: 'start_date',
        headerName: '开始日期',
        width: 110,
        // 自然日：原样展示，不做任何时区换算（《F01》§5.1）
        valueFormatter: (value) => formatNaturalDate(value as string | null),
        renderCell: textCell<Project>(),
      },
      {
        field: 'end_date',
        headerName: '结束日期',
        width: 110,
        valueFormatter: (value) => formatNaturalDate(value as string | null),
        renderCell: textCell<Project>(),
      },
      {
        field: 'progress',
        headerName: '进度',
        width: 130,
        sortable: false, // 派生字段，不在白名单里
        valueFormatter: (value: number) => `${value}%`,
        renderCell: (params: GridRenderCellParams<Project, number>) => (
          <ProgressCell value={params.value} />
        ),
      },
      {
        field: 'overdue_task_count',
        headerName: '逾期任务',
        width: 100,
        sortable: false,
        renderCell: (params: GridRenderCellParams<Project, number>) =>
          (params.value ?? 0) > 0 ? (
            <StatusTag label={String(params.value)} tone="error" />
          ) : (
            <CellText muted>0</CellText>
          ),
      },
      {
        field: 'membership_role',
        headerName: '我的角色',
        width: 100,
        sortable: false,
        renderCell: (params: GridRenderCellParams<Project, Project['membership_role']>) => {
          const meta = projectMembershipMeta(params.value);
          return meta ? (
            <StatusTag label={meta.label} tone={meta.tone} variant="outlined" />
          ) : (
            <CellText muted>—</CellText>
          );
        },
      },
      {
        field: 'updated_at',
        headerName: '最近更新',
        width: 140,
        // 版本字段原样透传，这里只是**展示**格式化（提交时仍走 useVersionedForm）
        valueFormatter: (value: string) => formatForDisplay(value, userTimeZone),
        renderCell: textCell<Project>(),
      },
      {
        field: 'actions',
        headerName: '操作',
        width: 70,
        sortable: false,
        filterable: false,
        disableColumnMenu: true,
        renderCell: (params: GridRenderCellParams<Project>) => (
          <DataTooltip title="打开详情">
            <IconButton
              size="small"
              aria-label={`打开项目 ${params.row.name}`}
              onClick={(event) => {
                event.stopPropagation();
                navigate(`/projects/${params.row.id}`);
              }}
            >
              <VisibilityIcon fontSize="small" />
            </IconButton>
          </DataTooltip>
        ),
      },
    ],
    [groupNameById, navigate, userTimeZone],
  );

  return (
    <PageContainer
      title="项目"
      breadcrumbs={[{ title: '工作台', path: '/projects' }, { title: '项目' }]}
      actions={
        <Stack direction="row" spacing={1}>
          <Button
            variant="contained"
            startIcon={<AddIcon />}
            onClick={() => {
              setCreateError(null);
              setCreateOpen(true);
            }}
          >
            新建项目
          </Button>
          <Button
            variant="outlined"
            startIcon={<RefreshIcon />}
            onClick={reload}
            disabled={loading}
          >
            刷新
          </Button>
        </Stack>
      }
    >
      <Stack spacing={2} sx={{ flex: 1, minHeight: 0 }}>
        <Box
          component="form"
          onSubmit={(event) => {
            event.preventDefault();
            commitFilters({ name: nameDraft });
          }}
          sx={{ pb: 2, borderBottom: 1, borderColor: 'divider' }}
        >
          <Stack spacing={1.5}>
            {/* ① 三个筛选条件：动态宽度铺满一行（与提醒/计划页同一套 filterLayout） */}
            <Box sx={filterRowSx(false)}>
              <TextField
                size="small"
                label="项目名称"
                placeholder="包含匹配"
                value={nameDraft}
                onChange={(event) => setNameDraft(event.target.value)}
                sx={filterItemSx(220, 440)}
              />
              <TextField
                select
                size="small"
                label="工作组"
                value={filters.groupId}
                onChange={(event) => commitFilters({ groupId: event.target.value })}
                disabled={groupsLoading}
                sx={filterItemSx(200, 400)}
                slotProps={{ inputLabel: { shrink: true }, select: { displayEmpty: true } }}
              >
                <MenuItem value="">全部</MenuItem>
                {groupOptions.map((option) => (
                  <MenuItem key={option.id} value={option.id}>
                    {option.name}
                  </MenuItem>
                ))}
              </TextField>
              <TextField
                select
                size="small"
                label="状态"
                value={filters.status}
                onChange={(event) =>
                  commitFilters({ status: event.target.value as ProjectStatus | '' })
                }
                sx={filterItemSx(160, 320)}
                slotProps={{ inputLabel: { shrink: true }, select: { displayEmpty: true } }}
              >
                <MenuItem value="">全部</MenuItem>
                {PROJECT_STATUS_OPTIONS.map((option) => (
                  <MenuItem key={option.value} value={option.value}>
                    {option.label}
                  </MenuItem>
                ))}
              </TextField>
            </Box>

            {/* ② 含归档 + 查询/重置：全部下移一行，靠右（与提醒页第二层一致） */}
            <Box sx={filterRowSx(true)}>
              <FormControlLabel
                control={
                  <Switch
                    checked={filters.includeArchived}
                    onChange={(event) =>
                      commitFilters({ includeArchived: event.target.checked })
                    }
                  />
                }
                label="含归档项目"
                sx={{ m: 0 }}
              />
              <Button type="submit" variant="contained" startIcon={<SearchIcon />}>
                查询
              </Button>
              <Button
                type="button"
                variant="outlined"
                startIcon={<RestartAltIcon />}
                onClick={handleReset}
              >
                重置
              </Button>
            </Box>
          </Stack>
        </Box>

        {error ? (
          <Alert
            severity="error"
            action={
              <Button color="inherit" size="small" onClick={reload}>
                重试
              </Button>
            }
          >
            {describe(error)}
          </Alert>
        ) : null}

        {/*
          表格自己滚：高度跟着视口走，保证**分页条始终可见**。
          若让页面整体滚动（由内容撑开），20 行会把分页条推到屏幕外，
          用户看不到"共 N 条"，也就无法确认服务器模式分页是否正确。
        */}
        <Box
          sx={{
            height: 'clamp(360px, calc(100dvh - 320px), 900px)',
            display: 'flex',
            flexDirection: 'column',
          }}
        >
          <DataGrid<Project>
            rows={rows}
            columns={columns}
            getRowId={(row) => row.id}
            loading={loading}
            // 三个开关都必须显式设成 server，否则 DataGrid 会在本地再分页/排序一遍（F02 §2.1）
            paginationMode="server"
            sortingMode="server"
            filterMode="server"
            // 全量条数来自服务端；用 rows.length 会让分页条错乱
            rowCount={rowCount}
            paginationModel={paginationModel}
            onPaginationModelChange={onPaginationModelChange}
            pageSizeOptions={[10, 20, 50, 100]}
            sortModel={sortModel}
            onSortModelChange={onSortModelChange}
            // 筛选由页面自己维护：关掉列筛选面板，避免误以为它会把条件发给后端
            disableColumnFilter
            disableRowSelectionOnClick
            // 默认列集合：把"开始日期 / 我的角色"收进列菜单，保证常用列在 1440 视口内不横向滚动
            initialState={{
              columns: {
                columnVisibilityModel: { start_date: false, membership_role: false },
              },
            }}
            onRowClick={(params) => navigate(`/projects/${params.row.id}`)}
            sx={{
              flex: 1,
              '& .MuiDataGrid-row': { cursor: 'pointer' },
              // 让浏览器**始终**为纵向滚动条预留位置：DataGrid 的 flex 列按 clientWidth 计算宽度，
              // 纵向滚动条晚一步出现时会挤掉约 15px，凭空多出一条横向滚动条
              '& .MuiDataGrid-virtualScroller': { scrollbarGutter: 'stable' },
            }}
            localeText={zhCN.components.MuiDataGrid.defaultProps.localeText}
          />
        </Box>
      </Stack>

      <ProjectFormDialog
        open={createOpen}
        mode="create"
        groups={groupOptions}
        submitting={creating}
        serverError={createError}
        onClose={() => setCreateOpen(false)}
        onSubmit={(values) => void handleCreate(values)}
      />
    </PageContainer>
  );
}
