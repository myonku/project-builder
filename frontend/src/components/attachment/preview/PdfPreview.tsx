import * as React from 'react';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import CircularProgress from '@mui/material/CircularProgress';
import Stack from '@mui/material/Stack';
import Typography from '@mui/material/Typography';
import * as pdfjs from 'pdfjs-dist';
import workerUrl from 'pdfjs-dist/build/pdf.worker.min.mjs?url';
pdfjs.GlobalWorkerOptions.workerSrc = workerUrl;

export default function PdfPreview({ blob }: { blob: Blob }) {
  const [pdf, setPdf] = React.useState<pdfjs.PDFDocumentProxy | null>(null);
  const [page, setPage] = React.useState(1);
  const [zoom, setZoom] = React.useState(1);
  const [busy, setBusy] = React.useState(true);
  const [error, setError] = React.useState('');
  const canvas = React.useRef<HTMLCanvasElement>(null);
  const holder = React.useRef<HTMLDivElement>(null);
  const [width, setWidth] = React.useState(800);
  React.useEffect(() => {
    let cancelled = false;
    let task: pdfjs.PDFDocumentLoadingTask | undefined;
    setError(''); setPdf(null); setPage(1); setBusy(true);
    void blob.arrayBuffer().then(async data => {
      if (cancelled) return;
      const assets = `${import.meta.env.BASE_URL}pdfjs/`;
      task = pdfjs.getDocument({ data, enableXfa: false,
        cMapUrl: `${assets}cmaps/`, cMapPacked: true, standardFontDataUrl: `${assets}standard_fonts/`, wasmUrl: `${assets}wasm/`,
        useSystemFonts: true, maxImageSize: 16_000_000, canvasMaxAreaInBytes: 32_000_000 });
      const loaded = await task.promise;
      if (!cancelled) setPdf(loaded);
    }).catch(cause => {
      if (!cancelled) { setBusy(false); setError(cause?.name === 'PasswordException' ? '文件已加密，请下载后使用密码打开。' : 'PDF 无法预览，请下载后查看。'); }
    });
    return () => { cancelled = true; void task?.destroy(); };
  }, [blob]);
  React.useEffect(() => {
    const element = holder.current; if (!element) return;
    const observer = new ResizeObserver(([entry]) => setWidth(Math.max(240, entry.contentRect.width - 32)));
    observer.observe(element); return () => observer.disconnect();
  }, []);
  React.useEffect(() => {
    if (!pdf || !canvas.current) return;
    let cancelled = false;
    let rendering: pdfjs.RenderTask | undefined;
    setBusy(true); setError('');
    void pdf.getPage(page).then(async current => {
      if (cancelled || !canvas.current) return;
      const base = current.getViewport({ scale: 1 });
      const scale = Math.min(width / base.width * zoom, 3);
      const css = current.getViewport({ scale });
      // Cap both canvas area and side length for scanned or unusually large pages.
      const ratio = Math.min(window.devicePixelRatio || 1, 2, Math.sqrt(8_000_000 / (css.width * css.height)), 8192 / Math.max(css.width, css.height));
      const viewport = current.getViewport({ scale: scale * ratio });
      const element = canvas.current;
      element.width = Math.ceil(viewport.width); element.height = Math.ceil(viewport.height);
      element.style.width = `${css.width}px`; element.style.height = `${css.height}px`;
      rendering = current.render({ canvas: element, viewport });
      await rendering.promise;
      if (!cancelled) setBusy(false);
    }).catch(cause => {
      if (!cancelled && cause?.name !== 'RenderingCancelledException') { setBusy(false); setError('这一页无法显示，请下载后查看。'); }
    });
    return () => { cancelled = true; rendering?.cancel(); };
  }, [pdf, page, zoom, width]);
  return (
    <Stack sx={{ flex: 1, minHeight: 0 }}>
      <Stack direction="row" spacing={1} sx={{ p: 1, alignItems: 'center', flexWrap: 'wrap', gap: 1 }}>
        <Button size="small" disabled={!pdf || page <= 1} onClick={() => setPage(p => p - 1)}>上一页</Button>
        <Typography variant="body2">第 {page} / {pdf?.numPages ?? '—'} 页</Typography>
        <Button size="small" disabled={!pdf || page >= pdf.numPages} onClick={() => setPage(p => p + 1)}>下一页</Button>
        <Button size="small" disabled={zoom <= 0.5} onClick={() => setZoom(z => Math.max(0.5, z - 0.25))}>缩小</Button>
        <Typography variant="body2">{Math.round(zoom * 100)}%</Typography>
        <Button size="small" disabled={zoom >= 2} onClick={() => setZoom(z => Math.min(2, z + 0.25))}>放大</Button>
      </Stack>
      {error && <Alert severity="warning">{error}</Alert>}
      <Box ref={holder} sx={{ flex: 1, overflow: 'auto', p: 2, bgcolor: 'grey.100' }}>
        {busy && <CircularProgress size={24} aria-label="正在显示 PDF" />}
        <canvas ref={canvas} aria-label={`PDF 第 ${page} 页`} style={{ display: error ? 'none' : 'block', margin: '0 auto', background: 'white' }} />
      </Box>
    </Stack>
  );
}
