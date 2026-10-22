/**
 * 邮件提醒列表（`/reminders`，F05 §7.2 第 6 步）。
 *
 * 三个端点（《A08》）：列表 `GET /reminders`、详情 `GET /reminders/{id}`（抽屉）、
 * 重试 `POST /reminders/{id}/retry`（只有 `failed` / `skipped` 可重试）。
 *
 * 三处与普通列表页**不同**的契约细节，都在这里显式处理：
 * 1. **查询串没有 `status` 参数**（传了会 `400`）——所以"状态"筛选是**对当前页做客户端过滤**，
 *    界面上必须说清楚"只作用于当前页"，否则用户会以为总数也跟着变了（《A08》§端点 1 备注）；
 * 2. **`kind` 服务端不校验取值**：传枚举外的值得到的是空列表而不是 `400`，
 *    因此下拉里只放 5 个枚举值，不提供自由输入；
 * 3. **`sort` 白名单**只有 `scheduled_for` / `created_at` / `updated_at` / `kind` / `status` / `attempts`——
 *    多写一个或少写一个都会 `400`，所以列头的 `sortable` 必须与白名单严格一致。
 *
 * 可见范围由服务端决定：普通成员只看发给自己的，系统管理员看全部（页面上标明当前范围）。
 */

import * as React from 'react';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import IconButton from '@mui/material/IconButton';
import MenuItem from '@mui/material/MenuItem';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';

import Typography from '@mui/material/Typography';
import RefreshIcon from '@mui/icons-material/Refresh';
import ReplayIcon from '@mui/icons-material/Replay';
import RestartAltIcon from '@mui/icons-material/RestartAlt';
import SearchIcon from '@mui/icons-material/Search';
import VisibilityIcon from '@mui/icons-material/Visibility';
import { DataGrid } from '@mui/x-data-grid';
import type { GridColDef, GridRenderCellParams } from '@mui/x-data-grid';
import { zhCN } from '@mui/x-data-grid/locales';
import { useSearchParams } from 'react-router';
import { queryReminders, retryReminder } from '@/api/reminders';
import { toApiError } from '@/api/client';
import CellText from '@/components/common/CellText';
import DataTooltip, { textCell } from '@/components/common/DataTooltip';
import { filterItemSx, filterRowSx } from '@/components/common/filterLayout';
import PageContainer from '@/components/common/PageContainer';
import StatusTag from '@/components/common/StatusTag';
import ReminderDetailDrawer from '@/components/reminder/ReminderDetailDrawer';
import {
  REMINDER_KIND_OPTIONS,
  REMINDER_STATUS_OPTIONS,
  canRetryReminder,
  reminderStatusMeta,
} from '@/components/reminder/reminderMeta';
import { describeApiError, useApiError } from '@/hooks/useApiError';
import { useDialogs } from '@/hooks/useDialogs';
import { useListQuery } from '@/hooks/useListQuery';
import { useNotifications } from '@/hooks/useNotifications';
import { useAuthStore } from '@/stores/auth';
import type { Reminder, ReminderQuery, ReminderStatus } from '@/types/reminder';
import { formatForDisplay } from '@/utils/datetime';
import { isUuid } from '@/utils/uuid';

/** 与《A08》§端点 1 的白名单**逐字一致**（多一个少一个都会 400） */
const SORT_WHITELIST = ['scheduled_for', 'created_at', 'updated_at', 'kind', 'status', 'attempts'];

interface ReminderFilters {
  kind: string;
  task_id: string;
  /** **只作用于当前页**：接口没有 `status` 参数（见文件头注释） */
  pageStatus: ReminderStatus | '';
}

const INITIAL_FILTERS: ReminderFilters = { kind: '', task_id: '', pageStatus: '' };

export default function ReminderListView() {
  const dialogs = useDialogs();
  const { show } = useNotifications();
  const { handle } = useApiError();
  const userRole = useAuthStore((state) => state.user?.system_role);
  const userTimeZone = useAuthStore((state) => state.user?.timezone);
  const [searchParams, setSearchParams] = useSearchParams();

  const [draft, setDraft] = React.useState<ReminderFilters>(INITIAL_FILTERS);
  const [retryingId, setRetryingId] = React.useState<string | null>(null);

  const taskIdError = draft.task_id !== '' && !isUuid(draft.task_id) ? '请输入完整的任务编号' : null;

  const {
    rows,
    rowCount,
    loading,
    error,
    paginationModel,
    onPaginationModelChange,
    sortModel,
    onSortModelChange,
    filters,
    setFilters,
    reload,
  } = useListQuery<Reminder, ReminderFilters>({
    query: ({ page, pageSize, sort, filters: current }) => {
      const query: ReminderQuery = { page, page_size: pageSize };
      if (current.kind) {
        query.kind = current.kind as ReminderQuery['kind'];
      }
      if (current.task_id) {
        query.task_id = current.task_id;
      }
      if (sort) {
        // `useListQuery` 已按白名单过滤，这里只把 string 收窄回契约的字面量联合
        query.sort = sort as ReminderQuery['sort'];
      }
      return queryReminders(query);
    },
    initialFilters: INITIAL_FILTERS,
    initialSort: [{ field: 'scheduled_for', sort: 'desc' }],
    sortWhitelist: SORT_WHITELIST,
    deps: [],
  });

  /** 当前页里按状态过滤后的行（接口不支持服务端状态筛选） */
  const visibleRows = React.useMemo(
    () => (filters.pageStatus ? rows.filter((row) => row.status === filters.pageStatus) : rows),
    [filters.pageStatus, rows],
  );

  const apply = React.useCallback(() => {
    if (draft.task_id !== '' && !isUuid(draft.task_id)) {
      return; // 明显的非法输入先在本地拦住，别让服务端退化成空列表
    }
    setFilters(draft);
  }, [draft, setFilters]);

  const reset = React.useCallback(() => {
    setDraft(INITIAL_FILTERS);
    setFilters(INITIAL_FILTERS);
  }, [setFilters]);

  const reminderId = searchParams.get('reminderId');
  const openDetail = React.useCallback(
    (id: string) => {
      const next = new URLSearchParams(searchParams);
      next.set('reminderId', id);
      setSearchParams(next, { replace: false });
    },
    [searchParams, setSearchParams],
  );
  const closeDetail = React.useCallback(() => {
    const next = new URLSearchParams(searchParams);
    next.delete('reminderId');
    setSearchParams(next, { replace: false });
  }, [searchParams, setSearchParams]);

  const handleRetry = React.useCallback(
    async (reminder: Reminder) => {
      const confirmed = await dialogs.confirm(
        `重试「${reminder.kind_label}」？重试会把这条提醒重新排入待发送队列，` +
          '后台派发循环下一次运行时会再次尝试发送，收件人可能因此收到邮件。',
        { title: '重试发送提醒', okText: '确认重试', cancelText: '取消', severity: 'warning' },
      );
      if (!confirmed) {
        return;
      }
      setRetryingId(reminder.id);
      try {
        await retryReminder(reminder.id);
        reload();
        show('已重新排入待发送队列，稍后刷新查看结果。', { severity: 'success' });
      } catch (caught) {
        // 并发下可能撞上 409（另一处已经重试过），统一映射会给出"已在队列中/已发送"的文案
        handle(toApiError(caught));
        reload();
      } finally {
        setRetryingId(null);
      }
    },
    [dialogs, handle, reload, show],
  );

  const columns = React.useMemo<GridColDef<Reminder>[]>(
    () => [
      {
        field: 'kind',
        headerName: '类型',
        width: 140,
        // `kind_label` 由服务端给中文，前端不再维护映射表
        valueGetter: (_value, row) => row.kind_label,
        renderCell: textCell<Reminder>(),
      },
      {
        field: 'status',
        headerName: '状态',
        width: 110,
        renderCell: (params: GridRenderCellParams<Reminder, ReminderStatus>) => {
          const meta = reminderStatusMeta(params.value);
          return (
            <DataTooltip title={meta.hint}>
              <span>
                <StatusTag label={meta.label} tone={meta.tone} />
              </span>
            </DataTooltip>
          );
        },
      },
      {
        field: 'attempts',
        headerName: '尝试',
        width: 80,
        type: 'number',
        renderCell: (params: GridRenderCellParams<Reminder, number>) => (
          <CellText muted={params.value === 0}>{params.value}</CellText>
        ),
      },
      {
        field: 'recipient_email',
        headerName: '收件人',
        width: 180,
        sortable: false, // 不在白名单里
        // 单元格里的长文本交给 DataGrid 自己截断（F01 §6 第 29 条）：
        // **不要**再套 `truncatedText`——那是给"非表格的行内文本"（成员面板）用的，
        // 在表格里用会双重截断（表格已按像素省略，函数又按字符砍一刀）。
        // 保留 Tooltip 只是为了不用展开就能看到完整值。
        renderCell: (params: GridRenderCellParams<Reminder, string>) => (
          <DataTooltip title={params.value ?? ''}>
            <span>{params.value}</span>
          </DataTooltip>
        ),
      },
      {
        field: 'scheduled_for',
        headerName: '计划发送',
        width: 160,
        valueFormatter: (value) => formatForDisplay(value as string, userTimeZone),
        renderCell: textCell<Reminder>(),
      },
      {
        field: 'sent_at',
        headerName: '发送时刻',
        width: 160,
        sortable: false, // 不在白名单里
        valueFormatter: (value) => (value ? formatForDisplay(value as string, userTimeZone) : '—'),
        renderCell: textCell<Reminder>(),
      },
      {
        field: 'last_error',
        headerName: '失败 / 跳过原因',
        flex: 1,
        minWidth: 170,
        sortable: false,
        // 同上：截断交给 DataGrid；完整原因本来还能在详情抽屉里看到
        renderCell: (params: GridRenderCellParams<Reminder, string>) => {
          const text = params.value ?? '';
          if (!text) {
            return <CellText muted>—</CellText>;
          }
          return (
            <DataTooltip title={text}>
              <span>{text}</span>
            </DataTooltip>
          );
        },
      },
      {
        field: 'task_id',
        headerName: '关联任务',
        width: 160,
        sortable: false,
        renderCell: (params: GridRenderCellParams<Reminder, string>) => (
          <Typography variant="caption" sx={{ fontFamily: 'monospace' }} noWrap>
            {params.value?.slice(-8)}
          </Typography>
        ),
      },
      {
        field: 'created_at',
        headerName: '记录创建',
        width: 170,
        valueFormatter: (value) => formatForDisplay(value as string, userTimeZone),
        renderCell: textCell<Reminder>(),
      },
      {
        field: 'updated_at',
        headerName: '最近更新',
        width: 170,
        valueFormatter: (value) => formatForDisplay(value as string, userTimeZone),
        renderCell: textCell<Reminder>(),
      },
      {
        field: 'actions',
        headerName: '操作',
        width: 90,
        sortable: false,
        renderCell: (params: GridRenderCellParams<Reminder>) => {
          const reminder = params.row;
          const retryable = canRetryReminder(reminder.status);
          return (
            <Stack direction="row" spacing={0.5}>
              <DataTooltip title="查看详情（发送时间与失败原因）">
                <IconButton
                  size="small"
                  aria-label={`查看提醒 ${reminder.id}`}
                  onClick={(event) => {
                    event.stopPropagation();
                    openDetail(reminder.id);
                  }}
                >
                  <VisibilityIcon fontSize="small" />
                </IconButton>
              </DataTooltip>
              <DataTooltip
                title={
                  retryable
                    ? '重新排入待发送队列'
                    : `「${reminderStatusMeta(reminder.status).label}」无需重试`
                }
              >
                <span>
                  <IconButton
                    size="small"
                    aria-label={`重试提醒 ${reminder.id}`}
                    disabled={!retryable || retryingId === reminder.id}
                    onClick={(event) => {
                      event.stopPropagation();
                      void handleRetry(reminder);
                    }}
                  >
                    <ReplayIcon fontSize="small" />
                  </IconButton>
                </span>
              </DataTooltip>
            </Stack>
          );
        },
      },
    ],
    [handleRetry, openDetail, retryingId, userTimeZone],
  );

  const scopeText =
    userRole === 'admin'
      ? '系统管理员：可以看到全部用户的提醒（用来排查"谁没收到、为什么没收到"）'
      : '只显示发给你的提醒';

  return (
    <PageContainer
      title="邮件提醒"
      breadcrumbs={[{ title: '工作台', path: '/projects' }, { title: '邮件提醒' }]}
      actions={
        <Button variant="outlined" startIcon={<RefreshIcon />} onClick={reload} disabled={loading}>
          刷新
        </Button>
      }
    >
      <Stack spacing={2}>
        <Typography variant="body2" color="text.secondary">
          {scopeText}
        </Typography>

        {/* 筛选区：与计划视图同一套版式（一条底部分割线 + flex/gap 行） */}
        <Box sx={{ pb: 2, borderBottom: 1, borderColor: 'divider' }}>
          <Stack spacing={1.5}>
            <Box sx={filterRowSx(false)}>
              <TextField
                select
                size="small"
                label="类型"
                value={draft.kind}
                onChange={(event) => setDraft({ ...draft, kind: event.target.value })}
                sx={filterItemSx(150, 360)}
                slotProps={{ inputLabel: { shrink: true }, select: { displayEmpty: true } }}
                helperText="按提醒类型筛选"
              >
                <MenuItem value="">全部类型</MenuItem>
                {REMINDER_KIND_OPTIONS.map((option) => (
                  <MenuItem key={option.value} value={option.value}>
                    {option.label}
                  </MenuItem>
                ))}
              </TextField>

              <TextField
                size="small"
                label="任务编号"
                value={draft.task_id}
                onChange={(event) => setDraft({ ...draft, task_id: event.target.value.trim() })}
                error={Boolean(taskIdError)}
                helperText={taskIdError ?? '按任务筛选，可留空'}
                sx={filterItemSx(220, 520)}
              />

              <TextField
                select
                size="small"
                label="状态（当前页）"
                value={draft.pageStatus}
                onChange={(event) =>
                  setDraft({ ...draft, pageStatus: event.target.value as ReminderStatus | '' })
                }
                sx={filterItemSx(160, 300)}
                slotProps={{ inputLabel: { shrink: true }, select: { displayEmpty: true } }}
                helperText="此项只过滤当前页"
              >
                <MenuItem value="">全部状态</MenuItem>
                {REMINDER_STATUS_OPTIONS.map((option) => (
                  <MenuItem key={option.value} value={option.value}>
                    {option.label}
                  </MenuItem>
                ))}
              </TextField>
            </Box>

            <Box sx={filterRowSx(true)}>
              <Button
                variant="contained"
                startIcon={<SearchIcon />}
                onClick={apply}
                disabled={loading || Boolean(taskIdError)}
              >
                查询
              </Button>
              <Button variant="outlined" startIcon={<RestartAltIcon />} onClick={reset}>
                重置
              </Button>
            </Box>
          </Stack>
        </Box>

        {filters.pageStatus ? (
          <Alert severity="info" variant="outlined">
            状态筛选仅作用于<strong>当前页</strong>：本页 {rows.length} 条中有 {visibleRows.length} 条符合，
            全部记录共 {rowCount} 条。
            想按状态归拢全部数据，请点「状态」列头排序。
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

        <Box
          sx={{
            height: 'clamp(360px, calc(100dvh - 420px), 900px)',
            width: '100%',
            scrollbarGutter: 'stable',
          }}
        >
          <DataGrid<Reminder>
            rows={visibleRows}
            columns={columns}
            getRowId={(row) => row.id}
            loading={loading}
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
            onRowClick={(params) => openDetail(params.row.id)}
            initialState={{
              columns: {
                // 次要列默认收起：UUID 与创建/更新时间，需要时从列菜单打开
                columnVisibilityModel: { task_id: false, created_at: false, updated_at: false },
              },
            }}
            localeText={zhCN.components.MuiDataGrid.defaultProps.localeText}
            sx={{ border: 0 }}
          />
        </Box>
      </Stack>

      <ReminderDetailDrawer
        key={reminderId ?? 'none'}
        reminderId={reminderId}
        onChanged={reload}
        onClose={closeDetail}
      />
    </PageContainer>
  );
}
