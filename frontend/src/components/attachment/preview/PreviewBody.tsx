import * as React from 'react';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import CircularProgress from '@mui/material/CircularProgress';
import MenuItem from '@mui/material/MenuItem';
import Stack from '@mui/material/Stack';
import Table from '@mui/material/Table';
import TableBody from '@mui/material/TableBody';
import TableCell from '@mui/material/TableCell';
import TableRow from '@mui/material/TableRow';
import TextField from '@mui/material/TextField';
import { decodeText, imageMime, OFFICE_PREVIEW_BYTES, TEXT_CHARACTERS, TEXT_PREVIEW_BYTES } from './formats';
import type { OfficeResult, PreviewKind } from './formats';
const PdfPreview = React.lazy(() => import('./PdfPreview'));
const loading = <Box sx={{ p: 3, textAlign: 'center' }}><CircularProgress size={28} aria-label="正在准备预览" /></Box>;

function Picture({ blob, name }: { blob: Blob; name: string }) {
  const [url, setUrl] = React.useState('');
  const [failed, setFailed] = React.useState(false);
  React.useEffect(() => {
    const src = URL.createObjectURL(new Blob([blob], { type: imageMime(name) }));
    setUrl(src); setFailed(false); return () => URL.revokeObjectURL(src);
  }, [blob, name]);
  return failed ? <Alert severity="warning">图片无法显示，请下载后查看。</Alert> : (
    <Box sx={{ flex: 1, overflow: 'auto', p: 2, textAlign: 'center' }}>
      {url && <Box component="img" src={url} alt={name} onError={() => setFailed(true)} sx={{ maxWidth: '100%', maxHeight: '100%', objectFit: 'contain' }} />}
    </Box>
  );
}
function TextPreview({ text }: { text: string }) {
  return <Box component="pre" sx={{ m: 0, p: 2, overflow: 'auto', flex: 1, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere', fontSize: 14, fontFamily: 'monospace' }}>{text || '（文件中没有可展示的文字）'}</Box>;
}
function ParsedPreview({ blob, kind }: { blob: Blob; kind: 'text' | 'docx' | 'xlsx' }) {
  const [result, setResult] = React.useState<OfficeResult | null>(null);
  const [sheetIndex, setSheetIndex] = React.useState(0);
  React.useEffect(() => {
    let cancelled = false;
    let worker: Worker | undefined;
    let timer: ReturnType<typeof setTimeout> | undefined;
    setResult(null); setSheetIndex(0);
    if (blob.size > (kind === 'text' ? TEXT_PREVIEW_BYTES : OFFICE_PREVIEW_BYTES)) {
      setResult({ error: `${kind === 'text' ? '文本超过 2 MB' : '文档超过 8 MB'}，请下载后查看。` });
      return;
    }
    void blob.arrayBuffer().then(buffer => {
      if (cancelled) return;
      if (kind === 'text') {
        const text = decodeText(buffer);
        setResult({ text: text.slice(0, TEXT_CHARACTERS), truncated: text.length > TEXT_CHARACTERS });
      } else {
        worker = new Worker(new URL('./office.worker.ts', import.meta.url), { type: 'module' });
        const finish = (value: OfficeResult) => {
          if (!cancelled) setResult(value);
          clearTimeout(timer); worker?.terminate();
        };
        worker.onmessage = (event: MessageEvent<OfficeResult>) => finish(event.data);
        worker.onerror = () => finish({ error: '文件无法解析，请下载后查看。' });
        timer = setTimeout(() => finish({ error: '文件解析时间较长，请下载后查看。' }), 15_000);
        worker.postMessage({ kind, buffer }, [buffer]);
      }
    }).catch(error => { if (!cancelled) setResult({ error: error instanceof Error ? error.message : '文件无法预览，请下载后查看。' }); });
    return () => { cancelled = true; clearTimeout(timer); worker?.terminate(); };
  }, [blob, kind]);
  if (!result) return loading;
  if ('error' in result) return <Alert severity="warning" sx={{ m: 2 }}>{result.error}</Alert>;
  if ('text' in result) return <>
    {kind === 'docx' && <Alert icon={false} severity="info">文档文字预览；图片、排版等内容请下载原文件查看。</Alert>}
    {result.truncated && <Alert severity="info">内容较长，仅展示前 20 万个字符；完整内容请下载查看。</Alert>}
    <TextPreview text={result.text} />
  </>;
  const sheet = result.sheets[sheetIndex];
  return <Stack sx={{ flex: 1, minHeight: 0 }}>
    <Alert icon={false} severity="info">表格内容预览；公式显示文件中保存的结果，图表和完整排版请下载查看。</Alert>
    {result.truncated && <Alert severity="info">内容较多，仅展示前 10 个工作表、每表前 200 行及 20 列，较长单元格也已截取。</Alert>}
    <TextField select label="工作表" size="small" value={sheetIndex} onChange={event => setSheetIndex(Number(event.target.value))} sx={{ m: 1, maxWidth: 360 }} disabled={!sheet}>
      {result.sheets.map((item, i) => <MenuItem key={i} value={i}>{item.name}</MenuItem>)}
      {!sheet && <MenuItem value={0}>无工作表</MenuItem>}
    </TextField>
    <Box sx={{ flex: 1, overflow: 'auto' }}>
      {sheet?.rows.length ? <Table size="small" aria-label={sheet.name}><TableBody>
        {sheet.rows.map((row, i) => <TableRow key={i}><TableCell component="th" scope="row" sx={{ width: 40, color: 'text.secondary' }}>{i + 1}</TableCell>
          {row.map((cell, j) => <TableCell key={j} sx={{ minWidth: 100, maxWidth: 320, whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{cell}</TableCell>)}
        </TableRow>)}
      </TableBody></Table> : <Alert severity="info">该工作表没有可展示的内容。</Alert>}
    </Box>
  </Stack>;
}
export default function PreviewBody({ blob, name, kind }: { blob: Blob; name: string; kind: PreviewKind }) {
  if (kind === 'image') return <Picture blob={blob} name={name} />;
  if (kind === 'pdf') return <React.Suspense fallback={loading}><PdfPreview blob={blob} /></React.Suspense>;
  if (kind === 'text' || kind === 'docx' || kind === 'xlsx') return <ParsedPreview blob={blob} kind={kind} />;
  return <Alert severity="info" sx={{ m: 2 }}>暂不支持此格式的在线预览，请下载后查看。支持 PDF、常见图片、文本、DOCX 和 XLSX。</Alert>;
}
