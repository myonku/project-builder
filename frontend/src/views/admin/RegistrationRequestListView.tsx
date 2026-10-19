/**
 * 注册申请审核页（仅系统管理员）。
 *
 * 独立路由 /admin/registration-requests，与 /admin/users 并列：
 * 申请审核与用户资料维护的分页、筛选和动作完全不同，不拼在同一个页面。
 */

import * as React from 'react';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import MenuItem from '@mui/material/MenuItem';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import RefreshIcon from '@mui/icons-material/Refresh';
import CheckIcon from '@mui/icons-material/Check';
import CloseIcon from '@mui/icons-material/Close';
import { DataGrid, type GridColDef, type GridRenderCellParams } from '@mui/x-data-grid';
import { toApiError } from '@/api/client';
import { queryRegistrationRequests, reviewRegistrationRequest } from '@/api/registration';
import PageContainer from '@/components/common/PageContainer';
import StatusTag, { type StatusTone } from '@/components/common/StatusTag';
import { textCell } from '@/components/common/DataTooltip';
import { filterItemSx, filterRowSx } from '@/components/common/filterLayout';
import { useApiError } from '@/hooks/useApiError';
import { useDialogs } from '@/hooks/useDialogs';
import { useListQuery } from '@/hooks/useListQuery';
import { useNotifications } from '@/hooks/useNotifications';
import { useAuthStore } from '@/stores/auth';
import type {
  RegistrationRequest,
  RegistrationRequestStatus,
} from '@/types/registration';
import { formatForDisplay } from '@/utils/datetime';

const SORT_WHITELIST = ['created_at', 'updated_at', 'username', 'status'] as const;

interface RegistrationFilters {
  status: RegistrationRequestStatus | '';
}

const INITIAL_FILTERS: RegistrationFilters = { status: 'pending' };

const STATUS_META: Record<RegistrationRequestStatus, { label: string; tone: StatusTone }> = {
  pending: { label: '待审核', tone: 'warning' },
  approved: { label: '已通过', tone: 'success' },
  rejected: { label: '已拒绝', tone: 'error' },
};

const STATUS_OPTIONS: { value: RegistrationRequestStatus | ''; label: string }[] = [
  { value: 'pending', label: '待审核' },
  { value: 'approved', label: '已通过' },
  { value: 'rejected', label: '已拒绝' },
  { value: '', label: '全部状态' },
];

export default function RegistrationRequestListView() {
  const dialogs = useDialogs();
  const { show } = useNotifications();
  const { handle } = useApiError();
  const userTimeZone = useAuthStore((state) => state.user?.timezone);
  const [busyId, setBusyId] = React.useState<string | null>(null);

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
  } = useListQuery<RegistrationRequest, RegistrationFilters>({
    query: ({ page, pageSize, sort, filters: active }) =>
      queryRegistrationRequests({
        page,
        page_size: pageSize,
        ...(sort ? { sort } : {}),
        ...(active.status ? { status: active.status } : {}),
      }),
    initialFilters: INITIAL_FILTERS,
    sortWhitelist: SORT_WHITELIST,
    initialSort: [{ field: 'created_at', sort: 'desc' }],
  });

  React.useEffect(() => {
    if (error) {
      handle(error, { silent: true });
    }
  }, [error, handle]);

  const reportConflict = React.useCallback(
    (caught: unknown, fallback: string) => {
      const apiError = toApiError(caught);
      const detail =
        apiError.fields.status ?? apiError.fields.username ?? apiError.fields.email;
      if (apiError.status === 409) {
        show(detail ?? fallback, { severity: 'warning' });
      } else {
        handle(apiError);
      }
    },
    [handle, show],
  );

  const handleReview = React.useCallback(
    async (request: RegistrationRequest, decision: 'approved' | 'rejected') => {
      const title = decision === 'approved' ? '通过注册申请' : '拒绝注册申请';
      let reason: string | undefined;
      if (decision === 'approved') {
        const confirmed = await dialogs.confirm(
          `通过「${request.display_name}（${request.username}）」的注册申请？\n` +
            '通过后会立即创建启用账号，用户名/邮箱必须仍然可用。',
          { title, okText: '通过', cancelText: '取消', severity: 'success' },
        );
        if (!confirmed) {
          return;
        }
      } else {
        const input = await dialogs.prompt(
          `拒绝「${request.display_name}（${request.username}）」的注册申请。请填写原因（会展示在申请记录里）：`,
          { title, okText: '拒绝', cancelText: '取消' },
        );
        if (input === null) {
          return;
        }
        reason = input.trim() || undefined;
      }

      setBusyId(request.id);
      try {
        const result = await reviewRegistrationRequest(request.id, {
          status: decision,
          ...(reason ? { reason } : {}),
        });
        show(
          decision === 'approved'
            ? `已通过「${result.user?.display_name ?? request.display_name}」的注册申请。`
            : `已拒绝「${request.display_name}」的注册申请。`,
          { severity: 'success' },
        );
        reload();
      } catch (caught) {
        reportConflict(caught, '当前状态不允许审核该申请。');
        reload();
      } finally {
        setBusyId(null);
      }
    },
    [dialogs, reload, reportConflict, show],
  );

  const columns = React.useMemo<GridColDef<RegistrationRequest>[]>(
    () => [
      { field: 'username', headerName: '用户名', width: 150, renderCell: textCell() },
      { field: 'display_name', headerName: '显示名称', width: 150, renderCell: textCell() },
      { field: 'email', headerName: '邮箱', width: 220, renderCell: textCell() },
      { field: 'timezone', headerName: '时区', width: 140, renderCell: textCell() },
      {
        field: 'status',
        headerName: '状态',
        width: 110,
        sortable: true,
        renderCell: (params: GridRenderCellParams<RegistrationRequest>) => {
          const status = params.row.status;
          const meta = STATUS_META[status];
          return <StatusTag label={meta.label} tone={meta.tone} />;
        },
      },
      {
        field: 'created_at',
        headerName: '申请时间',
        width: 180,
        renderCell: (params: GridRenderCellParams<RegistrationRequest>) => (
          <span>{formatForDisplay(params.row.created_at, userTimeZone)}</span>
        ),
      },
      {
        field: 'reviewed_at',
        headerName: '审核时间',
        width: 180,
        renderCell: (params: GridRenderCellParams<RegistrationRequest>) => (
          <span>{params.row.reviewed_at ? formatForDisplay(params.row.reviewed_at, userTimeZone) : '—'}</span>
        ),
      },
      {
        field: 'actions',
        headerName: '操作',
        width: 190,
        sortable: false,
        filterable: false,
        renderCell: (params: GridRenderCellParams<RegistrationRequest>) => {
          if (params.row.status !== 'pending') {
            return (
              <Typography variant="body2" color="text.secondary">
                {params.row.rejection_reason ?? '—'}
              </Typography>
            );
          }
          return (
            <Stack direction="row" spacing={1}>
              <Button
                size="small"
                variant="contained"
                color="success"
                startIcon={<CheckIcon />}
                disabled={busyId === params.row.id}
                onClick={() => void handleReview(params.row, 'approved')}
              >
                通过
              </Button>
              <Button
                size="small"
                variant="outlined"
                color="error"
                startIcon={<CloseIcon />}
                disabled={busyId === params.row.id}
                onClick={() => void handleReview(params.row, 'rejected')}
              >
                拒绝
              </Button>
            </Stack>
          );
        },
      },
    ],
    [busyId, handleReview, userTimeZone],
  );

  return (
    <PageContainer
      title="注册申请"
      breadcrumbs={[{ title: '系统管理' }, { title: '注册申请' }]}
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
      <Stack spacing={2}>
        <Typography variant="body2" color="text.secondary">
          自助注册的账号不会立即创建；管理员在这里通过后，系统才会创建启用账号。
        </Typography>

        <Box sx={{ pb: 2, borderBottom: 1, borderColor: 'divider' }}>
          <Box sx={filterRowSx(false)}>
            <TextField
              select
              size="small"
              label="申请状态"
              value={filters.status}
              onChange={(event) =>
                setFilters({ status: event.target.value as RegistrationRequestStatus | '' })
              }
              sx={filterItemSx(160, 240)}
              slotProps={{ inputLabel: { shrink: true }, select: { displayEmpty: true } }}
            >
              {STATUS_OPTIONS.map((option) => (
                <MenuItem key={option.value || 'all'} value={option.value}>
                  {option.label}
                </MenuItem>
              ))}
            </TextField>
          </Box>
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
            {error.message}
          </Alert>
        ) : null}

        <Box sx={{ height: 'clamp(360px, calc(100dvh - 320px), 720px)' }}>
          <DataGrid<RegistrationRequest>
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
            sortModel={sortModel}
            onSortModelChange={onSortModelChange}
            pageSizeOptions={[10, 20, 50]}
            disableColumnFilter
            disableRowSelectionOnClick
          />
        </Box>
      </Stack>
    </PageContainer>
  );
}
