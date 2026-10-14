/**
 * 附件域接口（对应《A07》）。
 *
 * - 附件**没有独立权限**，完全继承项目/任务；
 * - 配额按上传者统计并**计入已逻辑删除的附件**——配额满时要么彻底删除，要么换人上传；
 * - 下载不是 JSON 信封，走 `apiDownload`（错误以 JSON 返回，不能用 `<a href>`）。
 *
 * 上传与列表挂在项目/任务路径下，因此这里的函数按 A07 归域，而不是按 URL 前缀拆分。
 */

import { apiDelete, apiDownload, apiPost, apiPostQuery, apiUpload } from './client';
import type { DownloadedFile } from './client';
import type { Page } from '@/types/common';
import type { Attachment, AttachmentQueryBody } from '@/types/attachment';

/** `POST /projects/{id}/attachments`：multipart，字段名 `file` */
export function uploadProjectAttachment(
  projectId: string,
  file: File,
  signal?: AbortSignal,
): Promise<Attachment> {
  return apiUpload<Attachment>(`/projects/${projectId}/attachments`, file, { signal });
}

/** `POST /projects/{id}/attachments/query` */
export function queryProjectAttachments(
  projectId: string,
  body: AttachmentQueryBody,
  signal?: AbortSignal,
): Promise<Page<Attachment>> {
  return apiPostQuery<Attachment>(`/projects/${projectId}/attachments/query`, body, { signal });
}

/** `POST /tasks/{id}/attachments` */
export function uploadTaskAttachment(
  taskId: string,
  file: File,
  signal?: AbortSignal,
): Promise<Attachment> {
  return apiUpload<Attachment>(`/tasks/${taskId}/attachments`, file, { signal });
}

/** `POST /tasks/{id}/attachments/query` */
export function queryTaskAttachments(
  taskId: string,
  body: AttachmentQueryBody,
  signal?: AbortSignal,
): Promise<Page<Attachment>> {
  return apiPostQuery<Attachment>(`/tasks/${taskId}/attachments/query`, body, { signal });
}

/** `GET /attachments/{id}/download`：字节流；错误仍是 JSON 信封 */
export function downloadAttachment(
  attachmentId: string,
  signal?: AbortSignal,
): Promise<DownloadedFile> {
  return apiDownload(`/attachments/${attachmentId}/download`, { signal });
}

/** `DELETE /attachments/{id}`：逻辑删除，返回更新后的 Attachment */
export function deleteAttachment(attachmentId: string): Promise<Attachment> {
  return apiDelete<Attachment>(`/attachments/${attachmentId}`);
}

/** `POST /attachments/{id}/purge`：204；必须先逻辑删除，否则 409 */
export function purgeAttachment(attachmentId: string): Promise<void> {
  return apiPost<void>(`/attachments/${attachmentId}/purge`);
}
