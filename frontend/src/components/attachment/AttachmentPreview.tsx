import * as React from 'react';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import CircularProgress from '@mui/material/CircularProgress';
import Dialog from '@mui/material/Dialog';
import IconButton from '@mui/material/IconButton';
import Stack from '@mui/material/Stack';
import Typography from '@mui/material/Typography';
import CloseIcon from '@mui/icons-material/Close';
import DownloadIcon from '@mui/icons-material/Download';
import OpenInNewIcon from '@mui/icons-material/OpenInNew';
import { downloadAttachment, loadAttachmentPreview } from '@/api/attachments';
import { PreviewSizeError, toApiError } from '@/api/client';
import { describeApiError, useApiError } from '@/hooks/useApiError';
import { MAX_PREVIEW_BYTES, previewKind } from './preview/formats';
const PreviewBody = React.lazy(() => import('./preview/PreviewBody'));

export function attachmentPreviewPath(id: string) { return `/attachments/${encodeURIComponent(id)}/preview`; }
interface Props {
  attachmentId: string;
  fileName?: string;
  knownSize?: number;
  standalone?: boolean;
  onClose: () => void;
}
class PreviewBoundary extends React.Component<{ children: React.ReactNode }, { failed: boolean }> {
  state = { failed: false };
  static getDerivedStateFromError() { return { failed: true }; }
  render() { return this.state.failed ? <Alert severity="warning" sx={{ m: 2 }}>预览暂时不可用，请下载后查看。</Alert> : this.props.children; }
}
export default function AttachmentPreview({ attachmentId, fileName, knownSize, standalone = false, onClose }: Props) {
  const { handle } = useApiError();
  const titleId = React.useId();
  const [file, setFile] = React.useState<{ blob: Blob; name: string } | null>(null);
  const [error, setError] = React.useState('');
  const [loading, setLoading] = React.useState(true);
  const [downloading, setDownloading] = React.useState(false);
  const [reload, setReload] = React.useState(0);
  React.useEffect(() => {
    const controller = new AbortController();
    let cancelled = false, timedOut = false;
    setFile(null); setError(''); setLoading(true);
    if (knownSize && knownSize > MAX_PREVIEW_BYTES) {
      setLoading(false); setError(new PreviewSizeError().message); return;
    }
    const timer = setTimeout(() => { timedOut = true; controller.abort(); }, 30_000);
    void loadAttachmentPreview(attachmentId, controller.signal).then(result => {
      if (!cancelled) setFile({ blob: result.blob, name: result.filename ?? fileName ?? '附件' });
    }).catch(cause => {
      if (cancelled) return;
      if (timedOut) setError('文件读取时间较长，请重试或下载后查看。');
      else if (cause instanceof PreviewSizeError) setError(cause.message);
      else {
        const apiError = toApiError(cause);
        if (apiError.isUnauthorized) handle(apiError, { silent: true });
        setError(describeApiError(apiError));
      }
    }).finally(() => { clearTimeout(timer); if (!cancelled) setLoading(false); });
    return () => { cancelled = true; clearTimeout(timer); controller.abort(); };
  }, [attachmentId, fileName, knownSize, reload, handle]);
  const name = file?.name ?? fileName ?? '附件预览';
  async function download() {
    setDownloading(true);
    try {
      let data: { blob: Blob; name: string };
      if (file) data = file;
      else { const result = await downloadAttachment(attachmentId); data = { blob: result.blob, name: result.filename ?? fileName ?? '附件' }; }
      const url = URL.createObjectURL(data.blob);
      const anchor = document.createElement('a'); anchor.href = url; anchor.download = data.name;
      document.body.appendChild(anchor); anchor.click(); anchor.remove();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (cause) { handle(toApiError(cause)); }
    finally { setDownloading(false); }
  }
  const content = <Stack sx={{ height: '100%', minHeight: 0, minWidth: 0, bgcolor: 'background.paper' }}>
    <Stack direction="row" sx={{ p: { xs: 1, sm: 2 }, borderBottom: 1, borderColor: 'divider', alignItems: 'center', gap: 1, flexWrap: 'wrap', flexShrink: 0 }}>
      <Typography id={titleId} component="h1" variant="h6" sx={{ flex: '1 1 180px', minWidth: 0, overflowWrap: 'anywhere' }}>{name}</Typography>
      <Button size="small" startIcon={<DownloadIcon />} disabled={downloading} onClick={() => void download()}>{downloading ? '下载中…' : '下载'}</Button>
      {!standalone && <Button size="small" component="a" href={attachmentPreviewPath(attachmentId)} target="_blank" rel="noopener noreferrer" startIcon={<OpenInNewIcon />}>新标签页打开</Button>}
      <IconButton aria-label="关闭预览" title="关闭预览" onClick={onClose}><CloseIcon /></IconButton>
    </Stack>
    {loading ? <Box sx={{ p: 4, textAlign: 'center' }}><CircularProgress aria-label="正在读取附件" size={28} /></Box> : error ? (
      <Alert severity="warning" sx={{ m: 2 }} action={<Button color="inherit" onClick={() => setReload(r => r + 1)}>重试</Button>}>{error}</Alert>
    ) : file ? <PreviewBoundary key={`${attachmentId}-${reload}`}>
      <React.Suspense fallback={<Box sx={{ p: 3 }}><CircularProgress size={28} /></Box>}>
        <PreviewBody blob={file.blob} name={name} kind={previewKind(name)} />
      </React.Suspense>
    </PreviewBoundary> : null}
  </Stack>;
  if (standalone) return <Box sx={{ height: '100dvh', overflow: 'hidden' }}>{content}</Box>;
  return <Dialog open maxWidth={false} onClose={() => { /* Only the explicit close button exits preview. */ }} aria-labelledby={titleId}
    onKeyDown={event => { if (event.key === 'Escape') event.stopPropagation(); }}
    slotProps={{ paper: { sx: { width: 'min(1200px, calc(100vw - 32px))', height: 'calc(100dvh - 64px)', maxHeight: 'calc(100dvh - 32px)', m: 2 } } }}>
    {content}
  </Dialog>;
}
