import * as React from 'react';
import { Link as RouterLink } from 'react-router';
import Alert from '@mui/material/Alert';
import Autocomplete from '@mui/material/Autocomplete';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import IconButton from '@mui/material/IconButton';
import Link from '@mui/material/Link';
import MenuItem from '@mui/material/MenuItem';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';
import Tooltip from '@mui/material/Tooltip';
import Typography from '@mui/material/Typography';
import RefreshIcon from '@mui/icons-material/Refresh';
import SearchIcon from '@mui/icons-material/Search';
import RestartAltIcon from '@mui/icons-material/RestartAlt';
import VisibilityIcon from '@mui/icons-material/Visibility';
import DownloadIcon from '@mui/icons-material/Download';
import DeleteIcon from '@mui/icons-material/Delete';
import DeleteForeverIcon from '@mui/icons-material/DeleteForever';
import { DataGrid } from '@mui/x-data-grid';
import type { GridColDef } from '@mui/x-data-grid';
import { zhCN } from '@mui/x-data-grid/locales';
import { deleteAttachment, downloadAttachment, purgeAttachment, queryMyAttachments } from '@/api/attachments';
import { queryProjects } from '@/api/projects';
import { toApiError } from '@/api/client';
import AttachmentPreview from '@/components/attachment/AttachmentPreview';
import PageContainer from '@/components/common/PageContainer';
import StatusTag from '@/components/common/StatusTag';
import { truncatedText } from '@/components/common/TruncatedText';
import { filterItemSx, filterRowSx } from '@/components/common/filterLayout';
import { describeApiError, useApiError } from '@/hooks/useApiError';
import { useDialogs } from '@/hooks/useDialogs';
import { useListQuery } from '@/hooks/useListQuery';
import { useNotifications } from '@/hooks/useNotifications';
import { useAuthStore } from '@/stores/auth';
import type { MyAttachment, MyAttachmentQueryBody } from '@/types/attachment';
import type { Project } from '@/types/project';
import { formatForDisplay } from '@/utils/datetime';

interface Filters { name: string; type: '' | 'project' | 'task'; projectId: string; deleted: 'active' | 'all' | 'deleted' }
const EMPTY: Filters = { name: '', type: '', projectId: '', deleted: 'active' };
const SORT = ['created_at', 'updated_at', 'original_name', 'size_bytes', 'extension'];
function bodyOf(f: Filters): MyAttachmentQueryBody {
  return { ...(f.name.trim() ? { original_name: f.name.trim() } : {}), ...(f.type ? { container_type: f.type } : {}),
    ...(f.projectId ? { project_id: f.projectId } : {}), ...(f.deleted === 'all' ? { include_deleted: true } : {}),
    ...(f.deleted === 'deleted' ? { deleted_only: true } : {}) };
}
const fieldSx = (minWidth: number) => ({ ...filterItemSx(minWidth), maxWidth: { xs: '100%', sm: minWidth * 2 } });
function sizeOf(bytes: number) { return bytes < 1024 ? `${bytes} B` : bytes < 1024 * 1024 ? `${(bytes / 1024).toFixed(1)} KB` : `${(bytes / 1024 / 1024).toFixed(1)} MB`; }

export default function AttachmentListView() {
  const { handle } = useApiError();
  const { show } = useNotifications();
  const dialogs = useDialogs();
  const timeZone = useAuthStore(state => state.user?.timezone);
  const [form, setForm] = React.useState<Filters>(EMPTY);
  const [selectedProject, setSelectedProject] = React.useState<Project | null>(null);
  const [projectSearch, setProjectSearch] = React.useState('');
  const [projectOptions, setProjectOptions] = React.useState<Project[]>([]);
  const [projectsLoading, setProjectsLoading] = React.useState(false);
  const [projectsError, setProjectsError] = React.useState('');
  const [preview, setPreview] = React.useState<MyAttachment | null>(null);
  const [busyId, setBusyId] = React.useState<string | null>(null);
  const list = useListQuery<MyAttachment, Filters>({
    initialFilters: EMPTY, initialSort: [{ field: 'created_at', sort: 'desc' }], sortWhitelist: SORT,
    query: ({ filters, page, pageSize, sort }) => queryMyAttachments({ ...bodyOf(filters), page, page_size: pageSize, sort }),
  });
  React.useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;
    const timer = setTimeout(() => {
      setProjectsLoading(true); setProjectsError('');
      void queryProjects({ ...(projectSearch.trim() ? { name: projectSearch.trim() } : {}), include_archived: true, page_size: 20, sort: 'name' }, controller.signal)
        .then(result => { if (!cancelled) setProjectOptions(result.data); })
        .catch(cause => { if (!cancelled) { setProjectOptions([]); setProjectsError(describeApiError(cause)); } })
        .finally(() => { if (!cancelled) setProjectsLoading(false); });
    }, 250);
    return () => { cancelled = true; clearTimeout(timer); controller.abort(); };
  }, [projectSearch]);

  async function act(file: MyAttachment, action: 'download' | 'delete' | 'purge') {
    if (busyId) return;
    if (action !== 'download') {
      const confirmed = await dialogs.confirm(action === 'delete'
        ? `删除「${file.original_name}」？删除后仍占用空间，可在“仅已删除”中找到并彻底删除。`
        : `彻底删除「${file.original_name}」？文件将无法恢复，并释放占用空间。`,
      { title: action === 'delete' ? '删除附件' : '彻底删除附件', okText: action === 'delete' ? '删除' : '彻底删除', cancelText: '取消', severity: 'warning' });
      if (!confirmed) return;
    }
    setBusyId(file.id);
    try {
      if (action === 'download') {
        const result = await downloadAttachment(file.id);
        const url = URL.createObjectURL(result.blob);
        const anchor = document.createElement('a'); anchor.href = url; anchor.download = result.filename ?? file.original_name;
        document.body.appendChild(anchor); anchor.click(); anchor.remove(); setTimeout(() => URL.revokeObjectURL(url), 1000);
      } else {
        if (action === 'delete') await deleteAttachment(file.id); else await purgeAttachment(file.id);
        show(action === 'delete' ? '附件已删除，可在“仅已删除”中查看。' : '附件已彻底删除。', { severity: 'success' });
        if ((action === 'purge' || list.filters.deleted === 'active') && list.rows.length === 1 && list.paginationModel.page > 0) list.onPaginationModelChange({ ...list.paginationModel, page: list.paginationModel.page - 1 });
        else list.reload();
      }
    } catch (cause) { handle(toApiError(cause)); if (action !== 'download') list.reload(); }
    finally { setBusyId(null); }
  }

  const columns: GridColDef<MyAttachment>[] = [
    { field: 'original_name', headerName: '文件名', flex: 1, minWidth: 200, renderCell: ({ row }) => (
      <Link component="button" type="button" aria-label={`预览 ${row.original_name}`} onClick={() => setPreview(row)}
        sx={{ maxWidth: '100%', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis', textAlign: 'left' }}>
        {truncatedText(row.original_name, { threshold: 32, keep: 28, ellipsis: '…' })}
      </Link>) },
    { field: 'source', headerName: '所属项目 / 任务', width: 220, sortable: false, renderCell: ({ row }) => (
      <Stack sx={{ minWidth: 0, maxWidth: '100%', justifyContent: 'center', height: '100%' }}>
        <Link component={RouterLink} to={`/projects/${row.source.project.id}`} sx={{ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', display: 'block', lineHeight: '24px' }}>
          {truncatedText(row.source.project.name, { threshold: 20, keep: 16, ellipsis: '…' })}
        </Link>
        {row.source.task ? <Link component={RouterLink} to={`/projects/${row.source.project.id}?taskId=${row.source.task.id}`} sx={{ minWidth: 0, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', display: 'block', lineHeight: '24px' }}>
          {truncatedText(row.source.task.name, { threshold: 20, keep: 16, ellipsis: '…', typographyProps: { variant: 'caption' } })}
        </Link> : <Typography variant="caption" color="text.secondary">项目附件</Typography>}
      </Stack>) },
    { field: 'size_bytes', headerName: '大小', width: 100, valueFormatter: value => sizeOf(value as number) },
    { field: 'created_at', headerName: '上传时间', width: 175, valueFormatter: value => formatForDisplay(value as string, timeZone) },
    { field: 'deleted_at', headerName: '状态', width: 100, sortable: false, renderCell: ({ row }) => <StatusTag label={row.deleted_at ? '已删除' : row.source.read_only ? '只读' : '正常'} tone={row.deleted_at ? 'default' : row.source.read_only ? 'warning' : 'success'} variant="outlined" /> },
    { field: 'actions', headerName: '操作', width: 150, sortable: false, filterable: false, renderCell: ({ row }) => <Stack direction="row" sx={{ height: '100%', alignItems: 'center' }}>
      <Tooltip title="预览"><IconButton aria-label={`预览附件 ${row.original_name}`} size="small" onClick={() => setPreview(row)}><VisibilityIcon fontSize="small" /></IconButton></Tooltip>
      <Tooltip title="下载"><span><IconButton aria-label={`下载 ${row.original_name}`} size="small" disabled={busyId !== null} onClick={() => void act(row, 'download')}><DownloadIcon fontSize="small" /></IconButton></span></Tooltip>
      {row.deleted_at ? <Tooltip title={row.permissions.can_purge ? '彻底删除' : '当前无法彻底删除'}><span><IconButton aria-label={`彻底删除 ${row.original_name}`} color="error" size="small" disabled={busyId !== null || !row.permissions.can_purge} onClick={() => void act(row, 'purge')}><DeleteForeverIcon fontSize="small" /></IconButton></span></Tooltip>
        : <Tooltip title={row.permissions.can_delete ? '删除' : row.source.read_only ? '归档项目只读' : '当前没有删除权限'}><span><IconButton aria-label={`删除 ${row.original_name}`} size="small" disabled={busyId !== null || !row.permissions.can_delete} onClick={() => void act(row, 'delete')}><DeleteIcon fontSize="small" /></IconButton></span></Tooltip>}
    </Stack> },
  ];
  return <PageContainer title="我的附件" breadcrumbs={[{ title: '工作台' }, { title: '附件管理' }]}
    actions={<Button variant="outlined" startIcon={<RefreshIcon />} onClick={list.reload} disabled={list.loading}>刷新</Button>}>
    <Stack spacing={2} sx={{ minWidth: 0 }}>
      <Typography variant="body2" color="text.secondary">集中管理你上传且当前可访问的附件。删除后仍占用空间，彻底删除后才会释放。</Typography>
      <Box component="form" onSubmit={event => { event.preventDefault(); list.setFilters(form); }}>
        <Box sx={filterRowSx()}>
          <TextField label="文件名" placeholder="输入文件名查找" value={form.name} onChange={event => setForm({ ...form, name: event.target.value })} size="small" sx={fieldSx(180)} slotProps={{ htmlInput: { maxLength: 255 } }} />
          <TextField label="附件类型" select value={form.type} onChange={event => setForm({ ...form, type: event.target.value as Filters['type'] })} size="small" sx={fieldSx(150)} slotProps={{ select: { displayEmpty: true }, inputLabel: { shrink: true } }}>
            <MenuItem value="">全部</MenuItem><MenuItem value="project">项目附件</MenuItem><MenuItem value="task">任务附件</MenuItem>
          </TextField>
          <Autocomplete<Project> options={projectOptions} value={selectedProject} loading={projectsLoading} filterOptions={options => options}
            getOptionLabel={option => option.name} getOptionKey={option => option.id} isOptionEqualToValue={(a, b) => a.id === b.id}
            onInputChange={(_event, value, reason) => { if (reason === 'input' || reason === 'clear') setProjectSearch(value); }}
            onChange={(_event, value) => { setSelectedProject(value); setForm({ ...form, projectId: value?.id ?? '' }); }}
            renderInput={params => <TextField {...params} label="所属项目" placeholder="输入名称查找项目" size="small" error={!!projectsError} helperText={projectsError || undefined} />}
            noOptionsText="没有匹配的项目" loadingText="正在读取…" sx={fieldSx(180)} />
          <TextField label="删除状态" select value={form.deleted} onChange={event => setForm({ ...form, deleted: event.target.value as Filters['deleted'] })} size="small" sx={fieldSx(150)}>
            <MenuItem value="active">未删除</MenuItem><MenuItem value="all">含已删除</MenuItem><MenuItem value="deleted">仅已删除</MenuItem>
          </TextField>
        </Box>
        <Box sx={{ ...filterRowSx(true), mt: 2 }}>
          <Button type="submit" variant="contained" startIcon={<SearchIcon />}>查询</Button>
          <Button variant="outlined" startIcon={<RestartAltIcon />} onClick={() => { setForm(EMPTY); setSelectedProject(null); setProjectSearch(''); list.setFilters(EMPTY); }}>重置</Button>
        </Box>
      </Box>
      {list.error && <Alert severity="error" action={<Button color="inherit" onClick={list.reload}>重试</Button>}>{describeApiError(list.error)}</Alert>}
      <Typography variant="body2" color="text.secondary">共 {list.rowCount} 个附件</Typography>
      <Box sx={{ height: 'clamp(360px, calc(100dvh - 390px), 760px)', width: '100%', minWidth: 0 }}>
        <DataGrid rows={list.rows} columns={columns} loading={list.loading} rowCount={list.rowCount} rowHeight={64}
          paginationMode="server" sortingMode="server" filterMode="server" disableColumnFilter disableRowSelectionOnClick
          paginationModel={list.paginationModel} onPaginationModelChange={list.onPaginationModelChange}
          sortModel={list.sortModel} onSortModelChange={list.onSortModelChange} pageSizeOptions={[10, 20, 50, 100]}
          localeText={{ ...zhCN.components.MuiDataGrid.defaultProps.localeText, noRowsLabel: '没有符合条件的附件' }}
          sx={{ '& .MuiDataGrid-cell': { display: 'flex', alignItems: 'center' }, '& .MuiDataGrid-virtualScroller': { scrollbarGutter: 'stable' } }} />
      </Box>
    </Stack>
    {preview && <AttachmentPreview attachmentId={preview.id} fileName={preview.original_name} knownSize={preview.size_bytes} onClose={() => setPreview(null)} />}
  </PageContainer>;
}
