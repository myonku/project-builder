export type PreviewKind = 'image' | 'pdf' | 'text' | 'docx' | 'xlsx' | 'unsupported';
export { ATTACHMENT_PREVIEW_MAX_BYTES as MAX_PREVIEW_BYTES } from '@/types/attachment';
export const OFFICE_PREVIEW_BYTES = 8 * 1024 * 1024;
export const TEXT_PREVIEW_BYTES = 2 * 1024 * 1024;
export const TEXT_CHARACTERS = 200_000;
const IMAGES = new Map(Object.entries({ png: 'image/png', jpg: 'image/jpeg', jpeg: 'image/jpeg', gif: 'image/gif', webp: 'image/webp', bmp: 'image/bmp', avif: 'image/avif' }));
const TEXT = new Set(['txt', 'md', 'csv', 'json', 'log', 'yaml', 'yml']);
export function extensionOf(name: string) { return name.split('.').pop()?.toLowerCase() ?? ''; }
export function previewKind(name: string): PreviewKind {
  const ext = extensionOf(name);
  if (IMAGES.has(ext)) return 'image';
  if (TEXT.has(ext)) return 'text';
  if (ext === 'pdf' || ext === 'docx' || ext === 'xlsx') return ext;
  return 'unsupported';
}
export function imageMime(name: string) { return IMAGES.get(extensionOf(name)) ?? 'application/octet-stream'; }
export function decodeText(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer);
  let text: string;
  if (bytes[0] === 0xff && bytes[1] === 0xfe) text = new TextDecoder('utf-16le').decode(bytes);
  else if (bytes[0] === 0xfe && bytes[1] === 0xff) text = new TextDecoder('utf-16be').decode(bytes);
  else {
    try { text = new TextDecoder('utf-8', { fatal: true }).decode(bytes); }
    catch {
      try { text = new TextDecoder('gb18030', { fatal: true }).decode(bytes); }
      catch { throw new Error('无法识别文件中的文字，请下载后查看。'); }
    }
  }
  if (text.includes('\0')) throw new Error('这份文件无法作为文本预览，请下载后查看。');
  return text;
}
export interface PreviewSheet { name: string; rows: string[][] }
export type OfficeResult = { text: string; truncated: boolean } | { sheets: PreviewSheet[]; truncated: boolean } | { error: string };
export interface OfficeRequest { kind: 'docx' | 'xlsx'; buffer: ArrayBuffer }
