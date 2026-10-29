/**
 * 提醒域类型（对应《A08》与 F02 §4.8）。
 *
 * 整对象 `readonly`：唯一能改提醒的端点是 `POST /reminders/{id}/retry`（无请求体），
 * 因此标 `readonly` 不会挡住任何合法操作。
 */

export type ReminderKind =
  | 'task_assigned'
  | 'task_start_soon'
  | 'task_due_soon'
  | 'task_overdue'
  | 'task_owner_changed';

export type ReminderStatus = 'pending' | 'sending' | 'sent' | 'failed' | 'unknown' | 'skipped' | 'cancelled' | 'superseded' | 'suppressed';

export interface Reminder {
  readonly id: string;
 readonly cycle: number;
 readonly plan_version: number;
 readonly can_retry: boolean;
 readonly covered_by: string | null;
 readonly event_id: string | null;
 readonly delivery_attempts: readonly DeliveryEvidence[] | null;
  readonly task_id: string;
  readonly recipient_id: string;
  /** 生成时刻的快照 */
  readonly recipient_email: string;
  readonly kind: ReminderKind;
  /** 中文说明，可直接展示 */
  readonly kind_label: string;
  /** 计划发送时刻（UTC，可能带亚秒） */
  readonly scheduled_for: string;
  readonly status: ReminderStatus;
  /** 获得发送授权的次数；取消/抑制不计次数 */
  readonly attempts: number;
  /** 成功时为 `""`（不是 null） */
  readonly last_error: string;
  readonly sent_at: string | null;
  readonly created_at: string;
  readonly updated_at: string;
}

/** 走查询串。**没有 `status` 参数**：传它会 400 */
export interface ReminderQuery {
  kind?: ReminderKind;
  task_id?: string;
  page?: number;
  page_size?: number;
  sort?: 'scheduled_for' | 'created_at' | 'updated_at' | 'kind' | 'status' | 'attempts';
}

export interface DeliveryEvidence {
 readonly id: string;
 readonly status: string;
 readonly authorized_at: string;
 readonly expires_at: string;
 readonly sent_at: string | null;
 readonly timezone: string;
 readonly error: string;
 readonly snapshot: { readonly To?: string; readonly Subject?: string; readonly Body?: string; readonly legacy?: boolean };
}
