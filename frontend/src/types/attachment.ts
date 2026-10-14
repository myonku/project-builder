/**
 * 附件域类型（对应《A07》与 F02 §4.7、§6.5）。
 *
 * 不返回 `stored_key` / `download_url`：下载 URL 由客户端按
 * `GET /api/v1/attachments/{id}/download` 自行拼接。
 * 附件**没有独立权限**，完全继承项目/任务。
 */

export interface Attachment {
  readonly id: string;
  /** 与 `task_id` 恰有一个非空 */
  readonly project_id: string | null;
  readonly task_id: string | null;
  readonly uploader_id: string;
  /** 展示用；服务端已剥离路径与控制字符 */
  readonly original_name: string;
  /** 小写含点号（如 `".txt"`）；无扩展名为 `""` */
  readonly extension: string;
  readonly size_bytes: number;
  readonly mime_type: string;
  readonly deleted_at: string | null;
  readonly created_at: string;
  readonly updated_at: string;
}

export interface AttachmentQueryBody {
  /** 与 `deleted_only` 不能同时为 true */
  include_deleted?: boolean;
  deleted_only?: boolean;
  page?: number;
  page_size?: number;
  sort?: 'created_at' | 'updated_at' | 'original_name' | 'size_bytes' | 'extension';
}
