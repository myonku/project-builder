import * as React from 'react';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Dialog from '@mui/material/Dialog';
import DialogActions from '@mui/material/DialogActions';
import DialogContent from '@mui/material/DialogContent';
import DialogTitle from '@mui/material/DialogTitle';
import Link from '@mui/material/Link';
import MenuItem from '@mui/material/MenuItem';
import Stack from '@mui/material/Stack';
import Tab from '@mui/material/Tab';
import Tabs from '@mui/material/Tabs';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import RefreshIcon from '@mui/icons-material/Refresh';
import SearchIcon from '@mui/icons-material/Search';
import RestartAltIcon from '@mui/icons-material/RestartAlt';
import { DataGrid, type GridColDef, type GridValidRowModel } from '@mui/x-data-grid';
import { zhCN } from '@mui/x-data-grid/locales';
import { createJoinRequest, discoverGroups, queryMyGroupJoinRequests, queryGroups } from '@/api/groups';
import PageContainer from '@/components/common/PageContainer';
import StatusTag from '@/components/common/StatusTag';
import { textCell } from '@/components/common/DataTooltip';
import { filterItemSx, filterRowSx } from '@/components/common/filterLayout';
import JoinedGroupDrawer from '@/components/group/JoinedGroupDrawer';
import GroupJoinRequestsPanel from '@/components/group/GroupJoinRequestsPanel';
import { joinRequestStatusMeta } from '@/components/group/groupMeta';
import { describeApiError } from '@/hooks/useApiError';
import { useDialogs } from '@/hooks/useDialogs';
import { useListQuery, type UseListQueryResult } from '@/hooks/useListQuery';
import { useNotifications } from '@/hooks/useNotifications';
import { useAuthStore } from '@/stores/auth';
import type { DiscoverGroup, MyGroupJoinRequest, JoinRequestStatus, GroupMembershipStatus, Group, GroupQueryBody } from '@/types/group';
import { formatForDisplay } from '@/utils/datetime';

const DISCOVER_SORT = ['name', 'created_at'] as const;
const REQUEST_SORT = ['status', 'created_at', 'updated_at'] as const;
type GridList<T> = Pick<UseListQueryResult<T, object>, 'rows' | 'loading' | 'rowCount' | 'paginationModel' | 'onPaginationModelChange' | 'sortModel' | 'onSortModelChange'>;
function QueryGrid<T extends GridValidRowModel>({ list, columns, height = 'clamp(400px, calc(100dvh - 340px), 900px)' }: { list: GridList<T>; columns: GridColDef<T>[]; height?: string | number }) {
  return <Box sx={{ height, minWidth: 0 }}>
    <DataGrid rows={list.rows} columns={columns} loading={list.loading} rowCount={list.rowCount}
      paginationMode="server" sortingMode="server" filterMode="server" paginationModel={list.paginationModel}
      onPaginationModelChange={list.onPaginationModelChange} sortModel={list.sortModel} onSortModelChange={list.onSortModelChange}
      pageSizeOptions={[10, 20, 50]} disableColumnFilter disableRowSelectionOnClick
      localeText={zhCN.components.MuiDataGrid.defaultProps.localeText} />
  </Box>;
}
function applicationLabel(membership: GroupMembershipStatus | null, status: JoinRequestStatus | null, active = true) {
  if (!active) return '工作组已停用';
  if (membership === 'active') return '已加入';
  if (status === 'pending') return '待审核';
  return status ? '重新申请' : '申请加入';
}

export default function GroupJoinView() {
  const [tab, setTab] = React.useState<'groups' | 'requests'>('groups');
  const [detailGroupId, setDetailGroupId] = React.useState<string | null>(null);
  const [query, setQuery] = React.useState('');
  const [busyGroup, setBusyGroup] = React.useState<string | null>(null);
  const [submitError, setSubmitError] = React.useState<unknown>(null);
  const [reviewGroup, setReviewGroup] = React.useState<DiscoverGroup | null>(null);
  const dialogs = useDialogs();
  const { show } = useNotifications();
  const currentUser = useAuthStore(s => s.user);
  const timezone = currentUser?.timezone;
  const joined = useListQuery<Group, Record<string, never>>({
    query: ({ page, pageSize, sort }) => queryGroups({ membership_status: 'active', page, page_size: pageSize, sort: sort as GroupQueryBody['sort'] }),
    initialFilters: {}, sortWhitelist: ['name', 'status', 'updated_at'], initialSort: [{ field: 'name', sort: 'asc' }], enabled: tab === 'groups',
  });
  const discover = useListQuery<DiscoverGroup, { q: string }>({
    query: ({ filters, page, pageSize, sort }) => discoverGroups({ q: filters.q, page, page_size: pageSize, sort }),
    initialFilters: { q: '' }, sortWhitelist: DISCOVER_SORT, initialSort: [{ field: 'name', sort: 'asc' }], enabled: tab === 'requests',
  });
  const requests = useListQuery<MyGroupJoinRequest, { status: JoinRequestStatus | '' }>({
    query: ({ filters, page, pageSize, sort }) => queryMyGroupJoinRequests({ ...(filters.status ? { status: filters.status } : {}), page, page_size: pageSize, sort }),
    initialFilters: { status: '' }, sortWhitelist: REQUEST_SORT, initialSort: [{ field: 'updated_at', sort: 'desc' }], enabled: tab === 'requests',
  });
  const refresh = React.useCallback(() => { joined.reload(); discover.reload(); requests.reload(); }, [joined.reload, discover.reload, requests.reload]);
  async function apply(groupID: string, name: string) {
    if (busyGroup) return;
    if (!await dialogs.confirm(`申请加入「${name}」？审核通过后即可访问组内计划。`, { title: '申请加入工作组', okText: '提交申请', cancelText: '取消' })) return;
    setBusyGroup(groupID); setSubmitError(null);
    try {
      await createJoinRequest(groupID);
      show('加入申请已提交，可在“我的申请”查看审核结果。', { severity: 'success' });
    } catch (error) { setSubmitError(error); }
    finally { setBusyGroup(null); refresh(); }
  }
  const joinedColumns: GridColDef<Group>[] = [
    { field: 'name', headerName: '工作组', flex: 1, minWidth: 180, display: 'flex', renderCell: ({ row }) => <Link component="button" variant="body2" underline="hover" onClick={() => setDetailGroupId(row.id)} sx={{ maxWidth: '100%', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', textAlign: 'left' }}>{row.name}</Link> },
    { field: 'id', headerName: '工作组 ID', width: 320, sortable: false, renderCell: textCell<Group>() },
    { field: 'description', headerName: '说明', flex: 1, minWidth: 180, sortable: false, renderCell: textCell<Group>() },
    { field: 'status', headerName: '状态', width: 110, renderCell: ({ row }) => <StatusTag label={row.status === 'active' ? '启用' : '已停用'} tone={row.status === 'active' ? 'success' : 'default'} /> },
    { field: 'actions', headerName: '操作', width: 220, sortable: false, display: 'flex', renderCell: ({ row }) => <Stack direction="row" spacing={1}>
      <Button size="small" onClick={() => setDetailGroupId(row.id)}>查看详情</Button>
      {(row.status === 'active' && (row.owner_id === currentUser?.id || row.membership_role === 'manager' || currentUser?.system_role === 'admin')) && <Button size="small" onClick={() => setReviewGroup({ id: row.id, name: row.name, description: row.description, status: 'active', membership_status: row.membership_status, request_status: null, can_review: true })}>审核申请</Button>}
    </Stack> },
  ];
  const discoverColumns: GridColDef<DiscoverGroup>[] = [
    { field: 'name', headerName: '工作组', flex: 1, minWidth: 180, renderCell: textCell<DiscoverGroup>() },
    { field: 'id', headerName: '工作组 ID', width: 320, sortable: false, renderCell: textCell<DiscoverGroup>() },
    { field: 'description', headerName: '说明', flex: 1, minWidth: 180, sortable: false, renderCell: textCell<DiscoverGroup>() },
    { field: 'actions', headerName: '操作', width: 260, sortable: false, display: 'flex', renderCell: ({ row }) => <Stack direction="row" spacing={1}>
      <Button size="small" variant="outlined" disabled={!!busyGroup || row.membership_status === 'active' || row.request_status === 'pending'}
        onClick={() => void apply(row.id, row.name)}>{busyGroup === row.id ? '提交中…' : applicationLabel(row.membership_status, row.request_status)}</Button>
      {row.can_review && <Button size="small" onClick={() => setReviewGroup(row)}>审核申请</Button>}
    </Stack> },
  ];
  const requestColumns: GridColDef<MyGroupJoinRequest>[] = [
    { field: 'group_name', headerName: '工作组', flex: 1, minWidth: 180, sortable: false, renderCell: textCell<MyGroupJoinRequest>() },
    { field: 'group_id', headerName: '工作组 ID', width: 320, sortable: false, renderCell: textCell<MyGroupJoinRequest>() },
    { field: 'status', headerName: '申请状态', width: 110, renderCell: ({ row }) => { const meta = joinRequestStatusMeta(row.status); return <StatusTag label={meta.label} tone={meta.tone} />; } },
    { field: 'updated_at', headerName: '最近更新', width: 180, renderCell: ({ row }) => <span>{formatForDisplay(row.updated_at, timezone)}</span> },
    { field: 'reviewed_at', headerName: '审核时间', width: 180, sortable: false, renderCell: ({ row }) => <span>{row.reviewed_at ? formatForDisplay(row.reviewed_at, timezone) : '—'}</span> },
    { field: 'actions', headerName: '操作', width: 160, sortable: false, display: 'flex', renderCell: ({ row }) => <Button size="small" variant="outlined"
      disabled={!!busyGroup || row.group_status !== 'active' || row.membership_status === 'active' || row.status === 'pending'} onClick={() => void apply(row.group_id, row.group_name)}>
      {busyGroup === row.group_id ? '提交中…' : applicationLabel(row.membership_status, row.status, row.group_status === 'active')}</Button> },
  ];
  const list = tab === 'groups' ? joined : requests;
  return <PageContainer title="工作组" breadcrumbs={[{ title: '工作台', path: '/projects' }, { title: '工作组' }]}
    actions={<Button variant="outlined" startIcon={<RefreshIcon />} onClick={refresh} disabled={list.loading}>刷新</Button>}>
    <Stack spacing={2} sx={{ minWidth: 0 }}>
      <Tabs value={tab} onChange={(_e, next) => { setTab(next); setSubmitError(null); }}>
        <Tab label="我的组" value="groups" /><Tab label="我的申请" value="requests" />
      </Tabs>
      {tab === 'groups' ? <>
        <Typography variant="body2" color="text.secondary">查看你已加入的工作组，点击组名了解组信息和成员。</Typography>
        {joined.error && <Alert severity="error" action={<Button color="inherit" onClick={joined.reload}>重试</Button>}>{describeApiError(joined.error)}</Alert>}
        <QueryGrid list={joined} columns={joinedColumns} />
      </> : <>
        <Typography variant="h6" component="h2">申请加入</Typography>
        <Typography variant="body2" color="text.secondary">按组名或完整 ID 查找工作组，提交申请后等待审核。</Typography>
        <Box component="form" onSubmit={event => { event.preventDefault(); discover.setFilters({ q: query.trim() }); }} sx={filterRowSx(false)}>
          <TextField label="组名或完整 ID" value={query} onChange={e => setQuery(e.target.value)} size="small" sx={filterItemSx(240, 600)} slotProps={{ htmlInput: { maxLength: 160 } }} />
          <Button variant="contained" type="submit" startIcon={<SearchIcon />} disabled={discover.loading}>查询</Button>
          <Button variant="outlined" startIcon={<RestartAltIcon />} onClick={() => { setQuery(''); discover.setFilters({ q: '' }); }}>重置</Button>
        </Box>
        {!!submitError && <Alert severity="error">{describeApiError(submitError)}</Alert>}
        {discover.error && <Alert severity="error" action={<Button color="inherit" onClick={discover.reload}>重试</Button>}>{describeApiError(discover.error)}</Alert>}
        <QueryGrid list={discover} columns={discoverColumns} height={360} />
        <Typography variant="h6" component="h2" sx={{ pt: 2 }}>申请记录</Typography>
        <Typography variant="body2" color="text.secondary">查看每个工作组的最新申请状态。重新申请后，此处会更新为本次状态。</Typography>
        <TextField select label="申请状态" size="small" value={requests.filters.status} sx={{ width: { xs: '100%', sm: 240 } }}
          onChange={e => requests.setFilters({ status: e.target.value as JoinRequestStatus | '' })} slotProps={{ inputLabel: { shrink: true }, select: { displayEmpty: true } }}>
          <MenuItem value="">全部状态</MenuItem>{(['pending', 'approved', 'rejected'] as const).map(status => <MenuItem key={status} value={status}>{joinRequestStatusMeta(status).label}</MenuItem>)}
        </TextField>
        {requests.error && <Alert severity="error" action={<Button color="inherit" onClick={requests.reload}>重试</Button>}>{describeApiError(requests.error)}</Alert>}
        <QueryGrid list={requests} columns={requestColumns} />
      </>}
    </Stack>
    {detailGroupId && <JoinedGroupDrawer key={detailGroupId} groupId={detailGroupId} onClose={() => setDetailGroupId(null)} />}
    <Dialog open={!!reviewGroup} onClose={() => setReviewGroup(null)} fullWidth maxWidth="md" aria-labelledby="group-review-title">
      <DialogTitle id="group-review-title" sx={{ overflowWrap: 'anywhere' }}>{reviewGroup?.name} · 加入申请</DialogTitle>
      <DialogContent>{reviewGroup && <GroupJoinRequestsPanel key={reviewGroup.id} groupId={reviewGroup.id} onChanged={refresh} />}</DialogContent>
      <DialogActions><Button onClick={() => setReviewGroup(null)}>关闭</Button></DialogActions>
    </Dialog>
  </PageContainer>;
}
