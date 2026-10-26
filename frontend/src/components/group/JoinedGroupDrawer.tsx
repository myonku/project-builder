import * as React from 'react';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import CircularProgress from '@mui/material/CircularProgress';
import Divider from '@mui/material/Divider';
import Drawer from '@mui/material/Drawer';
import IconButton from '@mui/material/IconButton';
import Stack from '@mui/material/Stack';
import Typography from '@mui/material/Typography';
import CloseIcon from '@mui/icons-material/Close';
import RefreshIcon from '@mui/icons-material/Refresh';
import { DataGrid, type GridColDef } from '@mui/x-data-grid';
import { zhCN } from '@mui/x-data-grid/locales';
import { getGroup, queryGroupMembers } from '@/api/groups';
import UserAvatar from '@/components/common/UserAvatar';
import StatusTag from '@/components/common/StatusTag';
import DataTooltip, { textCell } from '@/components/common/DataTooltip';
import { groupStatusMeta, groupMemberRoleMeta } from '@/components/group/groupMeta';
import { describeApiError } from '@/hooks/useApiError';
import { useListQuery } from '@/hooks/useListQuery';
import { useAuthStore } from '@/stores/auth';
import type { Group, GroupMember, GroupMemberQueryBody } from '@/types/group';
import { formatForDisplay } from '@/utils/datetime';

const MEMBER_SORT = ['display_name', 'username', 'role', 'joined_at'] as const;

/** Read-only member view; management controls stay in the existing management drawer. */
export default function JoinedGroupDrawer({ groupId, onClose }: { groupId: string; onClose: () => void }) {
  const [group, setGroup] = React.useState<Group | null>(null);
  const [error, setError] = React.useState<unknown>(null);
  const [loading, setLoading] = React.useState(true);
  const [version, setVersion] = React.useState(0);
  const timezone = useAuthStore(state => state.user?.timezone);
  React.useEffect(() => {
    const controller = new AbortController();
    setGroup(null);
    setLoading(true);
    setError(null);
    void getGroup(groupId, controller.signal)
      .then(value => { if (!controller.signal.aborted) setGroup(value); })
      .catch(cause => { if (!controller.signal.aborted) setError(cause); })
      .finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [groupId, version]);
  const members = useListQuery<GroupMember, Record<string, never>>({
    query: ({ page, pageSize, sort }) => queryGroupMembers(groupId, {
      status: 'active', page, page_size: pageSize, sort: sort as GroupMemberQueryBody['sort'],
    }),
    initialFilters: {}, initialSort: [{ field: 'display_name', sort: 'asc' }],
    sortWhitelist: MEMBER_SORT, enabled: !!group, deps: [groupId, version],
  });
  const columns: GridColDef<GroupMember>[] = [
    {
      field: 'display_name', headerName: '成员', width: 190, display: 'flex',
      renderCell: ({ row }) => <Stack direction="row" spacing={1} sx={{ alignItems: 'center', minWidth: 0 }}>
        <UserAvatar user={{ id: row.user_id, display_name: row.display_name, avatar_updated_at: row.avatar_updated_at }} size={28} alt="" />
        <DataTooltip title={row.display_name}><Typography variant="body2" noWrap>{row.display_name}</Typography></DataTooltip>
      </Stack>,
    },
    { field: 'username', headerName: '用户名', width: 140, renderCell: textCell<GroupMember>() },
    { field: 'email', headerName: '邮箱', minWidth: 200, flex: 1, sortable: false, renderCell: textCell<GroupMember>() },
    { field: 'role', headerName: '角色', width: 120, renderCell: ({ row }) => <StatusTag label={row.user_id === group?.owner_id ? '所有者' : groupMemberRoleMeta(row.role).label} tone={row.user_id === group?.owner_id ? 'primary' : groupMemberRoleMeta(row.role).tone} /> },
    { field: 'joined_at', headerName: '加入时间', width: 180, renderCell: ({ row }) => <span>{formatForDisplay(row.joined_at, timezone)}</span> },
  ];
  return <Drawer open anchor="right" onClose={onClose} slotProps={{ paper: { sx: {
    width: { xs: '100%', md: 800 }, maxWidth: '100%',
    top: { xs: 56, sm: 64 },
    height: { xs: 'calc(100% - 56px)', sm: 'calc(100% - 64px)' },
  } } }}>
    <Box role="region" aria-label="已加入工作组详情" sx={{ height: '100%', display: 'flex', flexDirection: 'column', minWidth: 0 }}>
      <Stack direction="row" spacing={1} sx={{ px: 2, py: 1.5, alignItems: 'center' }}>
        <Typography variant="h6" component="h2" noWrap sx={{ flex: 1, minWidth: 0 }}>{group?.name ?? '工作组详情'}</Typography>
        <IconButton aria-label="刷新工作组详情" onClick={() => setVersion(value => value + 1)} disabled={loading}><RefreshIcon /></IconButton>
        <IconButton aria-label="关闭工作组详情" onClick={onClose}><CloseIcon /></IconButton>
      </Stack>
      <Divider />
      <Box sx={{ overflow: 'auto', flex: 1, p: 2, minWidth: 0 }}>
        {loading && <Stack sx={{ alignItems: 'center', py: 4 }}><CircularProgress aria-label="正在读取工作组" /></Stack>}
        {!!error && <Alert severity="info" action={<Button color="inherit" onClick={() => setVersion(value => value + 1)}>重试</Button>}>{describeApiError(error)}</Alert>}
        {group && <Stack spacing={2} sx={{ minWidth: 0 }}>
          <Typography variant="subtitle1" component="h3">基本信息</Typography>
          <Typography sx={{ overflowWrap: 'anywhere' }}>{group.name}</Typography>
          <Box><StatusTag label={groupStatusMeta(group.status).label} tone={groupStatusMeta(group.status).tone} /></Box>
          <Typography variant="body2" color="text.secondary" sx={{ overflowWrap: 'anywhere' }}>工作组 ID：{group.id}</Typography>
          <Typography variant="body2" sx={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{group.description || '暂无说明。'}</Typography>
          <Divider />
          <Typography variant="subtitle1" component="h3">成员 · {members.rowCount} 人</Typography>
          {members.error && <Alert severity="error" action={<Button color="inherit" onClick={members.reload}>重试</Button>}>{describeApiError(members.error)}</Alert>}
          <Box sx={{ height: 460, minWidth: 0 }}>
            <DataGrid rows={members.rows} columns={columns} loading={members.loading} rowCount={members.rowCount}
              paginationMode="server" sortingMode="server" filterMode="server" paginationModel={members.paginationModel}
              onPaginationModelChange={members.onPaginationModelChange} sortModel={members.sortModel} onSortModelChange={members.onSortModelChange}
              pageSizeOptions={[10, 20, 50]} disableColumnFilter disableRowSelectionOnClick
              localeText={zhCN.components.MuiDataGrid.defaultProps.localeText} />
          </Box>
        </Stack>}
      </Box>
    </Box>
  </Drawer>;
}
