/**
 * 工作组管理（`/admin/groups`，仅系统管理员）——《A03》，F05 §7.2 第 9 步。
 *
 * 列表按 §5.9 的服务器分页模板铺；详情用 `?groupId=` 抽屉承载：
 * - 详情/编辑走 `GET /groups/{id}` + `PATCH /groups/{id}`（版本协议在抽屉内）；
 * - 成员与加入申请在抽屉的两个标签页里，分别调用 A03 §5–§8 / §10–§11；
 * - 工作组没有删除端点，停用通过 PATCH 的 `status` 完成。
 *
 * 列表接口只有 `status` / 分页 / 排序，没有文本筛选；因此页面不提供“只过滤当前页”的搜索框。
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
import AddIcon from '@mui/icons-material/Add';
import RefreshIcon from '@mui/icons-material/Refresh';
import VisibilityIcon from '@mui/icons-material/Visibility';
import { DataGrid } from '@mui/x-data-grid';
import type { GridColDef, GridRenderCellParams } from '@mui/x-data-grid';
import { zhCN } from '@mui/x-data-grid/locales';
import { useSearchParams } from 'react-router';
import { createGroup, queryGroups } from '@/api/groups';
import { toApiError, type ApiError } from '@/api/client';
import DataTooltip, { textCell } from '@/components/common/DataTooltip';
import PageContainer from '@/components/common/PageContainer';
import StatusTag from '@/components/common/StatusTag';
import GroupDetailDrawer from '@/components/group/GroupDetailDrawer';
import GroupFormDialog, { type GroupFormValues } from '@/components/group/GroupFormDialog';
import {
  GROUP_STATUS_OPTIONS,
  groupStatusMeta,
} from '@/components/group/groupMeta';
import { describeApiError, useApiError } from '@/hooks/useApiError';
import { useListQuery } from '@/hooks/useListQuery';
import { useNotifications } from '@/hooks/useNotifications';
import { useAuthStore } from '@/stores/auth';
import type { Group, GroupQueryBody, GroupStatus } from '@/types/group';
import { formatForDisplay } from '@/utils/datetime';

/** 与《A03》§2 的白名单逐字一致 */
const SORT_WHITELIST = ['name', 'status', 'created_at', 'updated_at'] as const;

interface GroupFilters {
  status: GroupStatus | '';
}

const INITIAL_FILTERS: GroupFilters = { status: '' };

export default function GroupListView() {
  const { show } = useNotifications();
  const { handle } = useApiError();
  const userTimeZone = useAuthStore((state) => state.user?.timezone);
  const [searchParams, setSearchParams] = useSearchParams();
  const groupId = searchParams.get('groupId');

  const [createOpen, setCreateOpen] = React.useState(false);
  const [creating, setCreating] = React.useState(false);
  const [createError, setCreateError] = React.useState<ApiError | null>(null);

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
  } = useListQuery<Group, GroupFilters>({
    query: ({ page, pageSize, sort, filters: active }) => {
      const body: GroupQueryBody = { page, page_size: pageSize };
      if (active.status) {
        body.status = active.status;
      }
      if (sort) {
        body.sort = sort as GroupQueryBody['sort'];
      }
      return queryGroups(body);
    },
    initialFilters: INITIAL_FILTERS,
    sortWhitelist: SORT_WHITELIST,
    initialSort: [{ field: 'name', sort: 'asc' }],
    deps: [],
  });

  React.useEffect(() => {
    if (error) {
      handle(error, { silent: true });
    }
  }, [error, handle]);

  const openDetail = React.useCallback(
    (id: string) => {
      const next = new URLSearchParams(searchParams);
      next.set('groupId', id);
      setSearchParams(next, { replace: false });
    },
    [searchParams, setSearchParams],
  );

  const closeDetail = React.useCallback(() => {
    const next = new URLSearchParams(searchParams);
    next.delete('groupId');
    setSearchParams(next, { replace: false });
  }, [searchParams, setSearchParams]);

  const handleCreate = React.useCallback(
    async (values: GroupFormValues) => {
      setCreating(true);
      setCreateError(null);
      try {
        const body = {
          name: values.name.trim(),
          ...(values.description.trim() ? { description: values.description.trim() } : {}),
        };
        const created = await createGroup(body);
        setCreateOpen(false);
        show(`工作组「${created.name}」已创建。`, { severity: 'success' });
        reload();
        openDetail(created.id);
      } catch (caught) {
        const apiError = toApiError(caught);
        setCreateError(apiError);
        if (Object.keys(apiError.fields).length === 0) {
          handle(apiError);
        }
      } finally {
        setCreating(false);
      }
    },
    [handle, openDetail, reload, show],
  );

  const columns = React.useMemo<GridColDef<Group>[]>(
    () => [
      {
        field: 'name',
        headerName: '工作组名称',
        flex: 1,
        minWidth: 220,
        renderCell: (params: GridRenderCellParams<Group>) => (
          <Box
            component="span"
            sx={{ fontWeight: 500, cursor: 'pointer', '&:hover': { textDecoration: 'underline' } }}
            onClick={(event) => {
              event.stopPropagation();
              openDetail(params.row.id);
            }}
          >
            {params.row.name}
          </Box>
        ),
      },
      {
        field: 'status',
        headerName: '状态',
        width: 110,
        renderCell: (params: GridRenderCellParams<Group, GroupStatus>) => {
          const meta = groupStatusMeta(params.row.status);
          return <StatusTag label={meta.label} tone={meta.tone} />;
        },
      },
      {
        field: 'owner_id',
        headerName: '所有者',
        width: 150,
        sortable: false,
        renderCell: (params: GridRenderCellParams<Group, string>) => {
          const ownerId = params.row.owner_id;
          return (
            <DataTooltip title={ownerId}>
              <Box component="span" sx={{ fontFamily: 'monospace' }}>
                {ownerId.slice(-8)}
              </Box>
            </DataTooltip>
          );
        },
      },
      {
        field: 'updated_at',
        headerName: '最近更新',
        width: 170,
        valueFormatter: (value) => formatForDisplay(value as string, userTimeZone),
        renderCell: textCell<Group>(),
      },
      {
        field: 'actions',
        headerName: '操作',
        width: 70,
        sortable: false,
        filterable: false,
        disableColumnMenu: true,
        renderCell: (params: GridRenderCellParams<Group>) => (
          <DataTooltip title="打开工作组详情">
            <IconButton
              size="small"
              aria-label={`打开工作组 ${params.row.name}`}
              onClick={(event) => {
                event.stopPropagation();
                openDetail(params.row.id);
              }}
            >
              <VisibilityIcon fontSize="small" />
            </IconButton>
          </DataTooltip>
        ),
      },
    ],
    [openDetail, userTimeZone],
  );

  return (
    <PageContainer
      title="工作组管理"
      breadcrumbs={[{ title: '概览', path: '/' }, { title: '工作组管理' }]}
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
            新建工作组
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
      <Stack spacing={2}>
        <Typography variant="body2" color="text.secondary">
          系统管理员可见全部工作组；成员、所有者与加入申请的修改都在详情抽屉里完成。
        </Typography>

        <Stack direction="row" spacing={2} sx={{ alignItems: 'center', flexWrap: 'wrap' }}>
          <TextField
            select
            size="small"
            label="状态"
            value={filters.status}
            onChange={(event) =>
              setFilters({ status: event.target.value as GroupStatus | '' })
            }
            sx={{ minWidth: 160 }}
            slotProps={{ inputLabel: { shrink: true } }}
          >
            <MenuItem value="">全部</MenuItem>
            {GROUP_STATUS_OPTIONS.map((option) => (
              <MenuItem key={option.value} value={option.value}>
                {option.label}
              </MenuItem>
            ))}
          </TextField>
          <Typography variant="body2" color="text.secondary">
            共 {rowCount} 个工作组
          </Typography>
        </Stack>

        {error ? (
          <Alert
            severity="error"
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
            height: 'clamp(360px, calc(100dvh - 320px), 900px)',
            width: '100%',
            display: 'flex',
            flexDirection: 'column',
            scrollbarGutter: 'stable',
          }}
        >
          <DataGrid<Group>
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
            onRowClick={(params) => openDetail(params.row.id)}
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

      <GroupFormDialog
        open={createOpen}
        mode="create"
        submitting={creating}
        serverError={createError}
        onClose={() => setCreateOpen(false)}
        onSubmit={(values) => void handleCreate(values)}
      />

      <GroupDetailDrawer
        key={groupId ?? 'none'}
        groupId={groupId}
        onClose={closeDetail}
        onChanged={reload}
      />
    </PageContainer>
  );
}
