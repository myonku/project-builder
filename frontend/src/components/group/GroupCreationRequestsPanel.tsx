import * as React from 'react';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Dialog from '@mui/material/Dialog';
import DialogActions from '@mui/material/DialogActions';
import DialogContent from '@mui/material/DialogContent';
import DialogTitle from '@mui/material/DialogTitle';
import MenuItem from '@mui/material/MenuItem';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';
import Typography from '@mui/material/Typography';
import { DataGrid, type GridColDef } from '@mui/x-data-grid';
import { zhCN } from '@mui/x-data-grid/locales';
import { getGroupCreation, queryGroupCreations, reviewGroupCreation } from '@/api/groupCreation';
import { toApiError } from '@/api/client';
import StatusTag from '@/components/common/StatusTag';
import { textCell } from '@/components/common/DataTooltip';
import { joinRequestStatusMeta } from './groupMeta';
import { describeApiError } from '@/hooks/useApiError';
import { useDialogs } from '@/hooks/useDialogs';
import { useListQuery } from '@/hooks/useListQuery';
import { useAuthStore } from '@/stores/auth';
import { useNotifications } from '@/hooks/useNotifications';
import { formatForDisplay } from '@/utils/datetime';
import type { JoinRequestStatus } from '@/types/group';
import type { GroupCreationRequest } from '@/types/groupCreation';

interface Filters { status: JoinRequestStatus | ''; q: string }
export default function GroupCreationRequestsPanel({ admin = false, reloadToken = 0, onOpenGroup, onResubmit }: {
  admin?: boolean; reloadToken?: number; onOpenGroup: (id: string) => void;
  onResubmit?: (request: GroupCreationRequest) => void;
}) {
  const user = useAuthStore(s => s.user);
  const dialogs = useDialogs();
  const { show } = useNotifications();
  const [search, setSearch] = React.useState('');
  const [selectedId, setSelectedId] = React.useState<string | null>(null);
  const [detail, setDetail] = React.useState<GroupCreationRequest | null>(null);
  const [detailError, setDetailError] = React.useState('');
  const [detailLoading, setDetailLoading] = React.useState(false);
  const [reviewing, setReviewing] = React.useState(false);
  const [mustVerify, setMustVerify] = React.useState(false);
  const [reason, setReason] = React.useState('');
  const [reasonError, setReasonError] = React.useState('');
  const [detailToken, setDetailToken] = React.useState(0);
  const reviewLock = React.useRef(false);
  const detailSequence = React.useRef(0);
  const list = useListQuery<GroupCreationRequest, Filters>({
    query: ({ page, pageSize, sort, filters }) => queryGroupCreations({ page, page_size: pageSize, sort,
      ...(filters.status ? { status: filters.status } : {}), ...(admin && filters.q ? { q: filters.q } : {}) }, admin),
    initialFilters: { status: admin ? 'pending' : '', q: '' },
    sortWhitelist: ['created_at', 'updated_at', 'status'],
    initialSort: [{ field: 'created_at', sort: admin ? 'asc' : 'desc' }], deps: [reloadToken, user?.id],
  });
  React.useEffect(() => {
    if (!selectedId) return;
    const sequence = ++detailSequence.current;
    let cancelled = false;
    setDetailLoading(true); setDetailError('');
    void getGroupCreation(selectedId).then(result => {
      if (!cancelled && sequence === detailSequence.current) { setDetail(result); setMustVerify(false); }
    }).catch(error => {
      if (!cancelled && sequence === detailSequence.current) { setDetail(null); setDetailError(describeApiError(error)); }
    }).finally(() => { if (!cancelled && sequence === detailSequence.current) setDetailLoading(false); });
    return () => { cancelled = true; };
  }, [selectedId, detailToken]);

  const openDetail = (id: string) => { setDetail(null); setReason(''); setReasonError(''); setMustVerify(false); setSelectedId(id); };
  const current = detail?.id === selectedId ? detail : null;
  async function review(status: 'approved' | 'rejected') {
    if (!current || reviewLock.current || mustVerify) return;
    const rejection = reason.trim();
    if (status === 'rejected' && (!rejection || Array.from(rejection).length > 2000)) { setReasonError('请填写 1–2000 个字符的拒绝理由'); return; }
    reviewLock.current = true; setReviewing(true); setDetailError(''); setReasonError('');
    try {
      if (status === 'approved' && !await dialogs.confirm(`通过「${current.name}」的新建申请？${current.display_name ?? current.username ?? '申请人'}将成为工作组所有者。`, { title: '通过新建申请', okText: '通过并创建', cancelText: '取消' })) return;
      const result = await reviewGroupCreation(current.id, { status, expected_updated_at: current.updated_at, ...(status === 'rejected' ? { rejection_reason: rejection } : {}) });
      setDetail(result); list.reload(); show(status === 'approved' ? '申请已通过，工作组已创建。' : '申请已拒绝。', { severity: 'success' });
    } catch (caught) {
      const error = toApiError(caught);
      setDetailError(Object.values(error.fields)[0] ?? describeApiError(error));
      if (error.fields.rejection_reason) setReasonError(error.fields.rejection_reason);
      if (error.status === 409 || error.status === 0 || error.status >= 500) {
        setMustVerify(true);
        try {
          const latest = await getGroupCreation(current.id);
          setDetail(latest); if (latest.status !== 'pending') setMustVerify(false);
        } catch { /* explicit verification remains available */ }
      }
      list.reload();
    } finally { reviewLock.current = false; setReviewing(false); }
  }
  const columns: GridColDef<GroupCreationRequest>[] = [
    { field: 'name', headerName: '申请时名称', minWidth: 190, flex: 1, sortable: false, renderCell: textCell<GroupCreationRequest>() },
    ...(admin ? [{ field: 'display_name', headerName: '申请人', minWidth: 160, flex: 1, sortable: false,
      renderCell: ({ row }: { row: GroupCreationRequest }) => `${row.display_name}（${row.username}）` }] : []),
    { field: 'status', headerName: '状态', width: 110, renderCell: ({ row }) => { const meta = joinRequestStatusMeta(row.status); return <StatusTag label={meta.label} tone={meta.tone} />; } },
    { field: 'created_at', headerName: '提交时间', width: 180, renderCell: ({ row }) => <span>{formatForDisplay(row.created_at, user?.timezone)}</span> },
    { field: 'reviewed_at', headerName: '审核时间', width: 180, sortable: false, renderCell: ({ row }) => <span>{row.reviewed_at ? formatForDisplay(row.reviewed_at, user?.timezone) : '—'}</span> },
    { field: 'actions', headerName: '操作', width: 240, sortable: false, display: 'flex', renderCell: ({ row }) => <Stack direction="row" spacing={1}>
      <Button size="small" onClick={() => openDetail(row.id)}>{admin && row.status === 'pending' ? '查看并审核' : '查看申请'}</Button>
      {row.group_id && row.can_access_group && <Button size="small" onClick={() => onOpenGroup(row.group_id!)}>进入工作组</Button>}
      {!admin && row.status === 'rejected' && <Button size="small" onClick={() => onResubmit?.(row)}>重新申请</Button>}
    </Stack> },
  ];
  return <Stack spacing={2} sx={{ minWidth: 0 }}>
    <Typography variant="h6" component="h2">{admin ? '新建申请审核' : '我的新建申请'}</Typography>
    <Typography variant="body2" color="text.secondary">{admin ? '审核通过后才创建工作组；申请人成为所有者，审核者不会自动加入。' : '每次新建申请单独留存，审核通过后可进入工作组。'}</Typography>
    <Stack direction="row" spacing={1} useFlexGap sx={{ flexWrap: 'wrap' }}>
      <TextField select size="small" label="新建申请状态" value={list.filters.status} sx={{ minWidth: 180 }}
        onChange={e => list.patchFilters({ status: e.target.value as JoinRequestStatus | '' })} slotProps={{ inputLabel: { shrink: true }, select: { displayEmpty: true } }}>
        <MenuItem value="">全部状态</MenuItem>{(['pending', 'approved', 'rejected'] as const).map(s => <MenuItem key={s} value={s}>{joinRequestStatusMeta(s).label}</MenuItem>)}
      </TextField>
      {admin && <Box component="form" onSubmit={e => { e.preventDefault(); list.patchFilters({ q: search.trim() }); }} sx={{ display: 'flex', gap: 1, flexWrap: 'wrap' }}>
        <TextField size="small" label="申请名称或申请人" value={search} onChange={e => setSearch(e.target.value)} />
        <Button type="submit" variant="outlined">查询</Button><Button onClick={() => { setSearch(''); list.patchFilters({ q: '' }); }}>重置</Button>
      </Box>}
      <Button onClick={list.reload} disabled={list.loading}>刷新申请</Button>
    </Stack>
    <Typography variant="body2" color="text.secondary">{list.filters.status === 'pending' ? '待审核' : '当前筛选'}共 {list.rowCount} 条</Typography>
    {list.error && <Alert severity="error">{describeApiError(list.error)}</Alert>}
    <Box sx={{ height: 'clamp(360px, calc(100dvh - 390px), 850px)', minWidth: 0 }}><DataGrid rows={list.rows} columns={columns} loading={list.loading}
      rowCount={list.rowCount} paginationMode="server" sortingMode="server" filterMode="server" paginationModel={list.paginationModel} onPaginationModelChange={list.onPaginationModelChange}
      sortModel={list.sortModel} onSortModelChange={list.onSortModelChange} disableColumnFilter disableRowSelectionOnClick pageSizeOptions={[10, 20, 50, 100]}
      localeText={zhCN.components.MuiDataGrid.defaultProps.localeText} /></Box>
    <Dialog open={!!selectedId} onClose={() => { if (!reviewing) setSelectedId(null); }} fullWidth maxWidth="sm" aria-labelledby="group-creation-detail-title">
      <DialogTitle id="group-creation-detail-title">新建申请详情</DialogTitle>
      <DialogContent><Stack spacing={2} sx={{ overflowWrap: 'anywhere' }}>
        {detailLoading && <Typography>正在读取申请…</Typography>}
        {detailError && <Alert severity="error">{detailError}</Alert>}
        {mustVerify && <Alert severity="warning">审核结果待核对。请读取最新状态后再操作。</Alert>}
        {current && <>
          <Typography variant="h6">{current.name}</Typography>
          <StatusTag {...joinRequestStatusMeta(current.status)} />
          {admin && <Typography>申请人：{current.display_name}（{current.username}） · {current.applicant_status === 'active' ? '账号启用' : '账号已停用'}</Typography>}
          <Typography sx={{ whiteSpace: 'pre-wrap' }}>说明：{current.description || '未填写'}</Typography>
          <Typography sx={{ whiteSpace: 'pre-wrap' }}>申请理由：{current.reason}</Typography>
          <Typography>提交时间：{formatForDisplay(current.created_at, user?.timezone)}</Typography>
          {current.reviewed_at && <Typography>审核时间：{formatForDisplay(current.reviewed_at, user?.timezone)}</Typography>}
          {current.rejection_reason && <Alert severity="warning" sx={{ whiteSpace: 'pre-wrap' }}>拒绝理由：{current.rejection_reason}</Alert>}
          {current.status === 'approved' && <Typography>当前工作组：{current.can_access_group ? `${current.group_name}（${current.group_status === 'active' ? '启用' : '已停用'}）` : '当前无权查看。历史审批结果不变。'}</Typography>}
          {admin && current.status === 'pending' && <TextField label="拒绝理由" multiline minRows={2} value={reason} onChange={e => setReason(e.target.value)} disabled={reviewing || mustVerify} error={!!reasonError} helperText={reasonError || '拒绝时必填，最多 2000 个字符'} />}
        </>}
      </Stack></DialogContent>
      <DialogActions sx={{ flexWrap: 'wrap', gap: 1 }}>
        <Button onClick={() => setSelectedId(null)} disabled={reviewing}>关闭</Button>
        <Button onClick={() => setDetailToken(t => t + 1)} disabled={detailLoading || reviewing}>核对最新状态</Button>
        {current?.group_id && current.can_access_group && <Button onClick={() => { onOpenGroup(current.group_id!); setSelectedId(null); }}>进入工作组</Button>}
        {!admin && current?.status === 'rejected' && <Button onClick={() => { onResubmit?.(current); setSelectedId(null); }}>重新申请</Button>}
        {admin && current?.status === 'pending' && <>
          <Button color="error" onClick={() => void review('rejected')} disabled={reviewing || mustVerify || detailLoading}>拒绝申请</Button>
          <Button variant="contained" onClick={() => void review('approved')} disabled={reviewing || mustVerify || detailLoading || current.applicant_status !== 'active'}>通过申请</Button>
        </>}
      </DialogActions>
    </Dialog>
  </Stack>;
}
