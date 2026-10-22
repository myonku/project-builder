/**
 * 用户管理（`/admin/users`，仅系统管理员）——《A02》，F05 §7.2 第 9 步。
 *
 * 列表只支持 `include_disabled` / 分页 / 排序（A02 §5 没有文本筛选字段），
 * 因此这里不做一个"只在当前页生效"的搜索框；用户多时按列排序后翻页。
 *
 * 账号动作按契约拆分：
 * - 新建：`POST /users`，不接受 `system_role`（新账号固定 member）；
 * - 改资料 / 角色：`PATCH /users/{id}`，版本冲突由 `UserEditDialog` 的
 *   `useVersionedForm` 处理；
 * - 停用：`POST /users/{id}/disable`（专用端点）；
 * - 启用：没有专用端点，用 `PATCH {status:'active'}`；
 * - 重置密码：`POST /users/{id}/password`，字段名 `new_password`。
 *
 * 系统至少要保留一个启用中的管理员：降级/停用最后一个会返回 409，
 * 这里把 `fields.status` / `fields.system_role` 的中文说明直接展示。
 */

import * as React from 'react';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import FormControlLabel from '@mui/material/FormControlLabel';
import IconButton from '@mui/material/IconButton';
import Stack from '@mui/material/Stack';
import Switch from '@mui/material/Switch';
import Typography from '@mui/material/Typography';
import AddIcon from '@mui/icons-material/Add';
import BlockIcon from '@mui/icons-material/Block';
import CheckCircleIcon from '@mui/icons-material/CheckCircle';
import EditIcon from '@mui/icons-material/Edit';
import LockResetIcon from '@mui/icons-material/LockReset';
import RefreshIcon from '@mui/icons-material/Refresh';
import { DataGrid } from '@mui/x-data-grid';
import type { GridColDef, GridRenderCellParams } from '@mui/x-data-grid';
import { zhCN } from '@mui/x-data-grid/locales';
import { createUser, disableUser, queryUsers, updateUser } from '@/api/users';
import { toApiError, type ApiError } from '@/api/client';
import DataTooltip, { textCell } from '@/components/common/DataTooltip';
import PageContainer from '@/components/common/PageContainer';
import StatusTag from '@/components/common/StatusTag';
import UserAvatar from '@/components/common/UserAvatar';
import ResetPasswordDialog from '@/components/user/ResetPasswordDialog';
import UserCreateDialog, { type UserCreateValues } from '@/components/user/UserCreateDialog';
import UserEditDialog from '@/components/user/UserEditDialog';
import { systemRoleMeta, userStatusMeta } from '@/components/user/userMeta';
import { describeApiError, useApiError } from '@/hooks/useApiError';
import { useDialogs } from '@/hooks/useDialogs';
import { useListQuery } from '@/hooks/useListQuery';
import { useNotifications } from '@/hooks/useNotifications';
import { useAuthStore } from '@/stores/auth';
import type { SystemRole, User, UserQueryBody, UserStatus } from '@/types/user';
import { formatForDisplay } from '@/utils/datetime';

/** 与《A02》§5 的排序白名单逐字一致 */
const SORT_WHITELIST = [
  'username',
  'display_name',
  'email',
  'status',
  'system_role',
  'created_at',
  'updated_at',
  'last_login_at',
] as const;

interface UserFilters {
  includeDisabled: boolean;
}

const INITIAL_FILTERS: UserFilters = { includeDisabled: false };

export default function UserListView() {
  const dialogs = useDialogs();
  const { show } = useNotifications();
  const { handle } = useApiError();
  const currentUser = useAuthStore((state) => state.user);
  const userTimeZone = currentUser?.timezone;

  const [createOpen, setCreateOpen] = React.useState(false);
  const [creating, setCreating] = React.useState(false);
  const [createError, setCreateError] = React.useState<ApiError | null>(null);
  const [editingUserId, setEditingUserId] = React.useState<string | null>(null);
  const [resetUser, setResetUser] = React.useState<User | null>(null);
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
  } = useListQuery<User, UserFilters>({
    query: ({ page, pageSize, sort, filters: active }) => {
      const body: UserQueryBody = {
        page,
        page_size: pageSize,
        // A02 要求显式传 include_disabled（默认 false 时也不能靠省略猜测）
        include_disabled: active.includeDisabled,
      };
      if (sort) {
        body.sort = sort as UserQueryBody['sort'];
      }
      return queryUsers(body);
    },
    initialFilters: INITIAL_FILTERS,
    sortWhitelist: SORT_WHITELIST,
    initialSort: [{ field: 'username', sort: 'asc' }],
    deps: [],
  });

  React.useEffect(() => {
    if (error) {
      handle(error, { silent: true });
    }
  }, [error, handle]);

  /** 409 的中文字段明细比通用 409 文案更有用（最后一个管理员等） */
  const reportConflict = React.useCallback(
    (caught: unknown, fallback: string) => {
      const apiError = toApiError(caught);
      const detail =
        apiError.fields.status ?? apiError.fields.system_role ?? apiError.fields.email;
      if (apiError.status === 409) {
        show(detail ?? fallback, { severity: 'warning' });
      } else {
        handle(apiError);
      }
    },
    [handle, show],
  );

  const handleCreate = React.useCallback(
    async (values: UserCreateValues) => {
      setCreating(true);
      setCreateError(null);
      try {
        const created = await createUser({
          username: values.username.trim(),
          email: values.email.trim(),
          display_name: values.display_name.trim(),
          password: values.password,
          timezone: values.timezone.trim() || undefined,
        });
        setCreateOpen(false);
        show(`用户「${created.display_name}」已创建。`, { severity: 'success' });
        reload();
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
    [handle, reload, show],
  );

  const handleDisable = React.useCallback(
    async (user: User) => {
      const confirmed = await dialogs.confirm(
        `停用「${user.display_name}」？停用后该账号不能再登录，已有会话立即失效；` +
          '数据与操作记录都会保留。系统至少要保留一个启用中的管理员。',
        {
          title: '停用账号',
          okText: '停用',
          cancelText: '取消',
          severity: 'warning',
        },
      );
      if (!confirmed) {
        return;
      }
      setBusyId(user.id);
      try {
        await disableUser(user.id, { expected_updated_at: user.updated_at });
        show(`账号「${user.display_name}」已停用。`, { severity: 'success' });
        reload();
      } catch (caught) {
        reportConflict(caught, '当前状态不允许停用该账号。');
        reload();
      } finally {
        setBusyId(null);
      }
    },
    [dialogs, reload, reportConflict, show],
  );

  const handleEnable = React.useCallback(
    async (user: User) => {
      setBusyId(user.id);
      try {
        await updateUser(user.id, {
          status: 'active',
          expected_updated_at: user.updated_at,
        });
        show(`账号「${user.display_name}」已启用。`, { severity: 'success' });
        reload();
      } catch (caught) {
        reportConflict(caught, '当前状态不允许启用该账号。');
        reload();
      } finally {
        setBusyId(null);
      }
    },
    [reload, reportConflict, show],
  );

  const columns = React.useMemo<GridColDef<User>[]>(
    () => [
      {
        field: 'username',
        headerName: '用户',
        width: 190,
        renderCell: (params: GridRenderCellParams<User>) => (
          <Stack
            direction="row"
            spacing={1}
            sx={{ alignItems: 'center', minWidth: 0, height: '100%' }}
          >
            <UserAvatar user={params.row} size={28} />
            <Typography variant="body2" noWrap>
              {params.row.username}
            </Typography>
          </Stack>
        ),
      },
      {
        field: 'display_name',
        headerName: '显示名',
        width: 160,
        renderCell: textCell<User>(),
      },
      {
        field: 'email',
        headerName: '邮箱',
        flex: 1,
        minWidth: 200,
        renderCell: textCell<User>(),
      },
      {
        field: 'system_role',
        headerName: '系统角色',
        width: 125,
        renderCell: (params: GridRenderCellParams<User, SystemRole>) => {
          const meta = systemRoleMeta(params.row.system_role);
          return <StatusTag label={meta.label} tone={meta.tone} variant="outlined" />;
        },
      },
      {
        field: 'status',
        headerName: '状态',
        width: 110,
        renderCell: (params: GridRenderCellParams<User, UserStatus>) => {
          const meta = userStatusMeta(params.row.status);
          return <StatusTag label={meta.label} tone={meta.tone} />;
        },
      },
      {
        field: 'last_login_at',
        headerName: '最后登录',
        width: 170,
        valueFormatter: (value) =>
          value ? formatForDisplay(value as string, userTimeZone) : '—',
        renderCell: textCell<User>(),
      },
      {
        field: 'updated_at',
        headerName: '最近更新',
        width: 170,
        valueFormatter: (value) => formatForDisplay(value as string, userTimeZone),
        renderCell: textCell<User>(),
      },
      {
        field: 'actions',
        headerName: '操作',
        width: 150,
        sortable: false,
        filterable: false,
        disableColumnMenu: true,
        renderCell: (params: GridRenderCellParams<User>) => {
          const user = params.row;
          const isSelf = user.id === currentUser?.id;
          return (
            <Stack direction="row" spacing={0.25} sx={{ alignItems: 'center' }}>
              <DataTooltip title="编辑资料 / 角色">
                <IconButton
                  size="small"
                  aria-label={`编辑用户 ${user.username}`}
                  onClick={(event) => {
                    event.stopPropagation();
                    setEditingUserId(user.id);
                  }}
                >
                  <EditIcon fontSize="small" />
                </IconButton>
              </DataTooltip>
              <DataTooltip title="重置密码">
                <IconButton
                  size="small"
                  aria-label={`重置密码 ${user.username}`}
                  onClick={(event) => {
                    event.stopPropagation();
                    setResetUser(user);
                  }}
                >
                  <LockResetIcon fontSize="small" />
                </IconButton>
              </DataTooltip>
              {user.status === 'active' ? (
                <DataTooltip title={isSelf ? '当前登录账号不能在这里停用' : '停用账号'}>
                  <span>
                    <IconButton
                      size="small"
                      color="warning"
                      aria-label={`停用用户 ${user.username}`}
                      disabled={isSelf || busyId === user.id}
                      onClick={(event) => {
                        event.stopPropagation();
                        void handleDisable(user);
                      }}
                    >
                      <BlockIcon fontSize="small" />
                    </IconButton>
                  </span>
                </DataTooltip>
              ) : (
                <DataTooltip title="启用账号">
                  <span>
                    <IconButton
                      size="small"
                      color="success"
                      aria-label={`启用用户 ${user.username}`}
                      disabled={busyId === user.id}
                      onClick={(event) => {
                        event.stopPropagation();
                        void handleEnable(user);
                      }}
                    >
                      <CheckCircleIcon fontSize="small" />
                    </IconButton>
                  </span>
                </DataTooltip>
              )}
            </Stack>
          );
        },
      },
    ],
    [busyId, currentUser?.id, handleDisable, handleEnable, userTimeZone],
  );

  return (
    <PageContainer
      title="用户管理"
      breadcrumbs={[{ title: '系统管理', path: '/admin/users' }, { title: '用户管理' }]}
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
            新建用户
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
          系统管理员可管理全部账号。A02 的列表接口只支持“含停用账号”、分页与排序
          （没有文本筛选）；停用账号默认不显示。
        </Typography>

        <Stack direction="row" spacing={2} sx={{ alignItems: 'center', flexWrap: 'wrap' }}>
          <FormControlLabel
            control={
              <Switch
                checked={filters.includeDisabled}
                onChange={(event) =>
                  setFilters({ ...filters, includeDisabled: event.target.checked })
                }
              />
            }
            label="含已停用账号"
          />
          <Typography variant="body2" color="text.secondary">
            共 {rowCount} 个账号
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
          <DataGrid<User>
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
            initialState={{
              columns: {
                // 最近更新不占主视图；需要时从列菜单打开
                columnVisibilityModel: { updated_at: false },
              },
            }}
            localeText={zhCN.components.MuiDataGrid.defaultProps.localeText}
            sx={{
              border: 0,
              flex: 1,
              '& .MuiDataGrid-virtualScroller': { scrollbarGutter: 'stable' },
            }}
          />
        </Box>
      </Stack>

      <UserCreateDialog
        open={createOpen}
        submitting={creating}
        serverError={createError}
        onClose={() => setCreateOpen(false)}
        onSubmit={(values) => void handleCreate(values)}
      />

      <UserEditDialog
        key={editingUserId ?? 'none'}
        userId={editingUserId}
        onClose={() => setEditingUserId(null)}
        onSaved={reload}
      />

      <ResetPasswordDialog
        user={resetUser}
        onClose={() => setResetUser(null)}
        onDone={() => {
          setResetUser(null);
          reload();
        }}
        onReload={reload}
      />
    </PageContainer>
  );
}
