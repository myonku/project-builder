/**
 * 操作记录 / 审计列表（`/audit`，仅系统管理员；《A09》）。
 *
 * 这是一个**只读列表页**：一个 `GET /audit`，没有写路径、没有版本协议。
 * 页面结构照 F01 §5.9 的列表页模板（`ProjectListView` / `ReminderListView`）：
 * 页面自有筛选 + `useListQuery` + 服务端分页/排序 + 默认列集合 + 路由懒加载。
 *
 * 三处审计特有的契约细节：
 * 1. **仅系统管理员**：路由外面包了 `RequireAdmin`，侧边栏入口也只对管理员显示；
 *    非管理员即便直接敲 URL 也回首页（《A09》§权限语义）。
 * 2. **时间范围是 RFC3339 且两端都含**（《A09》§端点 1）：界面用与计划页同风格的
 *    `DateTimePicker`（`YYYY-MM-DD HH:mm`）收集到分钟，提交前按账号时区转成 UTC ISO；
 *    保留时间精度，不限制到"天"。
 * 3. **`changes` 只记录新值**，扁平、敏感键已脱敏为 `[redacted]`、单值截断 200 字符。
 *    单元格只放一行摘要，完整内容在详情抽屉里看；不要尝试把它解析成嵌套结构。
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
import RestartAltIcon from '@mui/icons-material/RestartAlt';
import SearchIcon from '@mui/icons-material/Search';
import VisibilityIcon from '@mui/icons-material/Visibility';
import { DataGrid } from '@mui/x-data-grid';
import type { GridColDef, GridRenderCellParams } from '@mui/x-data-grid';
import { zhCN } from '@mui/x-data-grid/locales';
import { DateTimePicker } from '@mui/x-date-pickers/DateTimePicker';
import { queryAudit } from '@/api/audit';
import CellText from '@/components/common/CellText';
import DataTooltip, { textCell } from '@/components/common/DataTooltip';
import DateLocalizationProvider from '@/components/common/DateLocalizationProvider';
import { filterItemSx, filterRowSx } from '@/components/common/filterLayout';
import PageContainer from '@/components/common/PageContainer';
import StatusTag from '@/components/common/StatusTag';
import AuditDetailDrawer from '@/components/audit/AuditDetailDrawer';
import {
  AUDIT_ACTION_OPTIONS,
  AUDIT_OBJECT_TYPE_OPTIONS,
  auditActionMeta,
  auditObjectTypeLabel,
  summarizeAuditChanges,
} from '@/components/audit/auditMeta';
import { describeApiError, useApiError } from '@/hooks/useApiError';
import { useListQuery } from '@/hooks/useListQuery';
import { useAuthStore } from '@/stores/auth';
import type { AuditAction, AuditObjectType, AuditQuery, AuditRecord } from '@/types/audit';
import { DATETIME_FORMAT, dayjs, formatForDisplay } from '@/utils/datetime';
import { isUuid } from '@/utils/uuid';

/**
 * 排序白名单与《A09》§端点 1 **逐字一致**（多一个少一个都会 400）。
 * 默认 `-created_at`：审计是"最近发生的先看"。
 */
const SORT_WHITELIST = ['created_at', 'action', 'object_type'];

interface AuditFilters {
  objectType: AuditObjectType | '';
  action: AuditAction | '';
  objectId: string;
  actorId: string;
  /** `YYYY-MM-DD HH:mm`（DateTimePicker 的值）；提交时按账号时区转 RFC3339 */
  from: string;
  /** 同上；闭区间，早于 from 会被本地校验拦住 */
  to: string;
}

const INITIAL_FILTERS: AuditFilters = {
  objectType: '',
  action: '',
  objectId: '',
  actorId: '',
  from: '',
  to: '',
};

/** 把 `YYYY-MM-DD HH:mm` 按账号时区解释成 RFC3339（UTC ISO 8601） */
function localDateTimeToRfc3339(value: string, timeZone?: string): string | undefined {
  if (!value) {
    return undefined;
  }
  try {
    // keepLocalTime=true：把输入当作目标时区的墙上时间，而不是先按浏览器时区解释
    const parsed = timeZone ? dayjs(value).tz(timeZone, true) : dayjs(value);
    return parsed.isValid() ? parsed.toISOString() : undefined;
  } catch {
    // 非法时区名等极端情况：让调用方按"未填写"处理，最终由后端兜底
    return undefined;
  }
}

function validateLocalDateTime(value: string): string | null {
  if (!value) {
    return null;
  }
  return dayjs(value).isValid() ? null : '时间格式不正确';
}

function NoAuditRows() {
  return (
    <Stack sx={{ alignItems: 'center', justifyContent: 'center', height: '100%' }}>
      <Typography variant="body2" color="text.secondary">
        没有符合条件的操作记录
      </Typography>
    </Stack>
  );
}

export default function AuditListView() {
  const { handle } = useApiError();
  const userTimeZone = useAuthStore((state) => state.user?.timezone);

  const [draft, setDraft] = React.useState<AuditFilters>(INITIAL_FILTERS);
  const [selected, setSelected] = React.useState<AuditRecord | null>(null);

  const objectIdError =
    draft.objectId !== '' && !isUuid(draft.objectId) ? '对象 id 必须是 UUID' : null;
  const actorIdError =
    draft.actorId !== '' && !isUuid(draft.actorId) ? '操作人 id 必须是 UUID' : null;
  const fromError = validateLocalDateTime(draft.from);
  const toError = validateLocalDateTime(draft.to);
  const rangeError =
    !fromError &&
    !toError &&
    draft.from !== '' &&
    draft.to !== '' &&
    dayjs(draft.to).isBefore(dayjs(draft.from))
      ? '结束时间不能早于开始时间'
      : null;
  const hasDraftError = Boolean(
    objectIdError || actorIdError || fromError || toError || rangeError,
  );

  const {
    rows,
    rowCount,
    loading,
    error,
    paginationModel,
    onPaginationModelChange,
    sortModel,
    onSortModelChange,
    setFilters,
    reload,
  } = useListQuery<AuditRecord, AuditFilters>({
    query: ({ page, pageSize, sort, filters: active }) => {
      const query: AuditQuery = { page, page_size: pageSize };
      if (active.objectType) {
        query.object_type = active.objectType;
      }
      if (active.action) {
        query.action = active.action;
      }
      if (active.objectId) {
        query.object_id = active.objectId;
      }
      if (active.actorId) {
        query.actor_id = active.actorId;
      }
      if (active.from) {
        const from = localDateTimeToRfc3339(active.from, userTimeZone);
        if (from) {
          query.from = from;
        }
      }
      if (active.to) {
        const to = localDateTimeToRfc3339(active.to, userTimeZone);
        if (to) {
          query.to = to;
        }
      }
      if (sort) {
        query.sort = sort;
      }
      return queryAudit(query);
    },
    initialFilters: INITIAL_FILTERS,
    sortWhitelist: SORT_WHITELIST,
    initialSort: [{ field: 'created_at', sort: 'desc' }],
    deps: [],
  });

  // 401（会话失效）交给统一映射跳登录；403 等其余错误留在页面里展示
  React.useEffect(() => {
    if (error) {
      handle(error, { silent: true });
    }
  }, [error, handle]);

  const apply = React.useCallback(() => {
    if (hasDraftError) {
      return;
    }
    setFilters(draft);
  }, [draft, hasDraftError, setFilters]);

  const reset = React.useCallback(() => {
    setDraft(INITIAL_FILTERS);
    setFilters(INITIAL_FILTERS);
    setSelected(null);
  }, [setFilters]);

  const columns = React.useMemo<GridColDef<AuditRecord>[]>(
    () => [
      {
        field: 'created_at',
        headerName: '操作时间',
        width: 170,
        valueFormatter: (value) => formatForDisplay(value as string, userTimeZone),
        renderCell: textCell<AuditRecord>(),
      },
      {
        field: 'action',
        headerName: '动作',
        width: 130,
        renderCell: (params: GridRenderCellParams<AuditRecord, AuditAction>) => {
          const meta = auditActionMeta(params.row.action);
          return <StatusTag label={meta.label} tone={meta.tone} />;
        },
      },
      {
        field: 'object_type',
        headerName: '对象类型',
        width: 110,
        renderCell: (params: GridRenderCellParams<AuditRecord, AuditObjectType>) => (
          <StatusTag label={auditObjectTypeLabel(params.row.object_type)} variant="outlined" />
        ),
      },
      {
        field: 'actor_name',
        headerName: '操作人',
        width: 160,
        sortable: false, // 不在白名单里
        renderCell: textCell<AuditRecord>(),
      },
      {
        field: 'object_id',
        headerName: '对象 id',
        width: 150,
        sortable: false,
        renderCell: (params: GridRenderCellParams<AuditRecord, string>) => {
          const objectId = params.row.object_id;
          return (
            <DataTooltip title={objectId}>
              <CellText sx={{ fontFamily: 'monospace' }}>{objectId.slice(-8)}</CellText>
            </DataTooltip>
          );
        },
      },
      {
        field: 'changes',
        headerName: '变更摘要',
        flex: 1,
        minWidth: 220,
        sortable: false,
        renderCell: (params: GridRenderCellParams<AuditRecord>) => {
          const summary = summarizeAuditChanges(params.row.changes);
          if (!summary) {
            return <CellText muted>—</CellText>;
          }
          return (
            <DataTooltip title={summary}>
              <CellText>{summary}</CellText>
            </DataTooltip>
          );
        },
      },
      {
        field: 'request_id',
        headerName: 'request_id',
        width: 140,
        sortable: false,
        renderCell: (params: GridRenderCellParams<AuditRecord, string>) => {
          const requestId = params.row.request_id;
          return (
            <DataTooltip title={requestId}>
              <CellText sx={{ fontFamily: 'monospace' }}>{requestId.slice(0, 8)}…</CellText>
            </DataTooltip>
          );
        },
      },
      {
        field: 'actions',
        headerName: '操作',
        width: 70,
        sortable: false,
        disableColumnMenu: true,
        renderCell: (params: GridRenderCellParams<AuditRecord>) => (
          <DataTooltip title="查看完整变更与 request_id">
            <IconButton
              size="small"
              aria-label={`查看操作记录 ${params.row.id}`}
              onClick={(event) => {
                event.stopPropagation();
                setSelected(params.row);
              }}
            >
              <VisibilityIcon fontSize="small" />
            </IconButton>
          </DataTooltip>
        ),
      },
    ],
    [userTimeZone],
  );

  const scopeNote = `仅系统管理员可查看；审计记录与业务写操作同事务写入，只增不删。时间按账号时区（${
    userTimeZone ?? '浏览器本地时区'
  }）填写，from / to 两端都含。`;

  return (
    <PageContainer
      title="操作记录"
      breadcrumbs={[{ title: '概览', path: '/' }, { title: '操作记录' }]}
      actions={
        <Button
          variant="outlined"
          startIcon={<RefreshIcon />}
          onClick={reload}
          disabled={loading}
        >
          刷新
        </Button>
      }
    >
      <Stack spacing={2} sx={{ flex: 1, minHeight: 0 }}>
        <Typography variant="body2" color="text.secondary">
          {scopeNote}
        </Typography>

        <Box
          component="form"
          onSubmit={(event) => {
            event.preventDefault();
            apply();
          }}
          sx={{ pb: 2, borderBottom: 1, borderColor: 'divider' }}
        >
          <Stack spacing={1.5}>
            <Box sx={filterRowSx(false, true)}>
              <TextField
                select
                size="small"
                label="对象类型"
                value={draft.objectType}
                onChange={(event) =>
                  setDraft({ ...draft, objectType: event.target.value as AuditObjectType | '' })
                }
                sx={filterItemSx(150, 300)}
                slotProps={{ inputLabel: { shrink: true } }}
              >
                <MenuItem value="">全部类型</MenuItem>
                {AUDIT_OBJECT_TYPE_OPTIONS.map((option) => (
                  <MenuItem key={option.value} value={option.value}>
                    {option.label}
                  </MenuItem>
                ))}
              </TextField>

              <TextField
                select
                size="small"
                label="动作"
                value={draft.action}
                onChange={(event) =>
                  setDraft({ ...draft, action: event.target.value as AuditAction | '' })
                }
                sx={filterItemSx(170, 340)}
                slotProps={{ inputLabel: { shrink: true } }}
              >
                <MenuItem value="">全部动作</MenuItem>
                {AUDIT_ACTION_OPTIONS.map((option) => (
                  <MenuItem key={option.value} value={option.value}>
                    {option.label}
                  </MenuItem>
                ))}
              </TextField>

              <TextField
                size="small"
                label="对象 id"
                placeholder="UUID，可留空"
                value={draft.objectId}
                onChange={(event) =>
                  setDraft({ ...draft, objectId: event.target.value.trim() })
                }
                error={Boolean(objectIdError)}
                helperText={objectIdError ?? undefined}
                sx={filterItemSx(240, 480)}
              />

              <TextField
                size="small"
                label="操作人 id"
                placeholder="UUID，可留空"
                value={draft.actorId}
                onChange={(event) =>
                  setDraft({ ...draft, actorId: event.target.value.trim() })
                }
                error={Boolean(actorIdError)}
                helperText={actorIdError ?? undefined}
                sx={filterItemSx(240, 480)}
              />
            </Box>

            <DateLocalizationProvider>
              <Box sx={filterRowSx(true, true)}>
                <DateTimePicker
                  label="开始时间（含）"
                  value={draft.from ? dayjs(draft.from) : null}
                  onChange={(next) =>
                    setDraft({ ...draft, from: next ? next.format(DATETIME_FORMAT) : '' })
                  }
                  format={DATETIME_FORMAT}
                  slotProps={{
                    textField: {
                      size: 'small',
                      sx: { width: 210 },
                      error: Boolean(fromError) || Boolean(rangeError),
                      helperText: fromError ?? undefined,
                    },
                  }}
                />
                <DateTimePicker
                  label="结束时间（含）"
                  value={draft.to ? dayjs(draft.to) : null}
                  onChange={(next) =>
                    setDraft({ ...draft, to: next ? next.format(DATETIME_FORMAT) : '' })
                  }
                  format={DATETIME_FORMAT}
                  slotProps={{
                    textField: {
                      size: 'small',
                      sx: { width: 210 },
                      error: Boolean(toError) || Boolean(rangeError),
                      helperText: rangeError ?? toError ?? undefined,
                    },
                  }}
                />
              </Box>
            </DateLocalizationProvider>

            <Box sx={filterRowSx(true)}>
              <Button
                type="submit"
                variant="contained"
                startIcon={<SearchIcon />}
                disabled={loading || hasDraftError}
              >
                查询
              </Button>
              <Button type="button" variant="outlined" startIcon={<RestartAltIcon />} onClick={reset}>
                重置
              </Button>
            </Box>
          </Stack>
        </Box>

        {error ? (
          <Alert
            severity={error.isForbidden ? 'warning' : 'error'}
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
            height: 'clamp(360px, calc(100dvh - 460px), 900px)',
            display: 'flex',
            flexDirection: 'column',
            width: '100%',
            scrollbarGutter: 'stable',
          }}
        >
          <DataGrid<AuditRecord>
            rows={rows}
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
            onRowClick={(params) => setSelected(params.row)}
            initialState={{
              columns: {
                // request_id 很长，默认收进列菜单；完整值在详情抽屉里也能看到
                columnVisibilityModel: { request_id: false },
              },
            }}
            slots={{ noRowsOverlay: NoAuditRows }}
            localeText={zhCN.components.MuiDataGrid.defaultProps.localeText}
            sx={{
              border: 0,
              flex: 1,
              '& .MuiDataGrid-row': { cursor: 'pointer' },
              '& .MuiDataGrid-virtualScroller': { scrollbarGutter: 'stable' },
            }}
          />
        </Box>
      </Stack>

      <AuditDetailDrawer record={selected} onClose={() => setSelected(null)} />
    </PageContainer>
  );
}
