import { unzipSync } from 'fflate';
import { TEXT_CHARACTERS } from './formats';
import type { OfficeRequest, OfficeResult } from './formats';
const worker = globalThis as unknown as {
  onmessage: ((event: MessageEvent<OfficeRequest>) => void) | null;
  postMessage: (message: OfficeResult) => void;
};
worker.onmessage = async ({ data }) => {
  try {
    // Inspect ZIP metadata without inflating entries before handing them to an Office parser.
    let expanded = 0, entries = 0;
    const names = new Set<string>();
    unzipSync(new Uint8Array(data.buffer), { filter(entry) {
      expanded += entry.originalSize; entries++; names.add(entry.name);
      if (expanded > 40 * 1024 * 1024 || entries > 2000) throw new Error('文件内容过大，无法在线预览，请下载后查看。');
      return false;
    } });
    if (!names.has(data.kind === 'docx' ? 'word/document.xml' : 'xl/workbook.xml')) throw new Error('文件格式与扩展名不符，请下载后查看。');
    if (data.kind === 'docx') {
      const mammoth = await import('mammoth');
      const { value } = await mammoth.extractRawText({ arrayBuffer: data.buffer });
      worker.postMessage({ text: value.slice(0, TEXT_CHARACTERS), truncated: value.length > TEXT_CHARACTERS });
    } else {
      const { default: readXlsxFile } = await import('read-excel-file/web-worker');
      const workbook = await readXlsxFile(new Blob([data.buffer]));
      let truncated = workbook.length > 10, remaining = 500_000;
      const sheets = workbook.slice(0, 10).map(sheet => {
        if (sheet.data.length > 200) truncated = true;
        return { name: sheet.sheet.slice(0, 100), rows: sheet.data.slice(0, 200).map(row => {
          if (row.length > 20) truncated = true;
          return row.slice(0, 20).map(value => {
            const text = value == null ? '' : Object.prototype.toString.call(value) === '[object Date]' ? (value as unknown as Date).toISOString().slice(0, 10) : String(value);
            const shown = text.slice(0, Math.min(200, Math.max(0, remaining)));
            if (shown.length !== text.length) truncated = true;
            remaining -= shown.length; return shown;
          });
        }) };
      });
      worker.postMessage({ sheets, truncated });
    }
  } catch (error) {
    const message = error instanceof Error && /^(文件内容过大|文件格式与扩展名不符)/.test(error.message)
      ? error.message : '文件无法解析或已损坏，请下载后查看。';
    worker.postMessage({ error: message });
  }
};
