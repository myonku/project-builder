/**
 * 提醒域接口（对应《A08》）。
 *
 * `status` 语义：`pending` / `sent` 不可重试（409）；`failed` / `skipped` 可重试。
 * 查询串**没有 `status` 参数**：传它会 400。
 */

import { apiGet, apiGetQuery, apiPost } from './client';
import type { Page } from '@/types/common';
import type { Reminder, ReminderQuery } from '@/types/reminder';
import type { QueryValue } from './client';

/** `GET /reminders` */
export function queryReminders(
  query: ReminderQuery = {},
  signal?: AbortSignal,
): Promise<Page<Reminder>> {
  return apiGetQuery<Reminder>('/reminders', { ...query } as Record<string, QueryValue>, {
    signal,
  });
}

/** `GET /reminders/{id}` */
export function getReminder(reminderId: string, signal?: AbortSignal): Promise<Reminder> {
  return apiGet<Reminder>(`/reminders/${reminderId}`, { signal });
}

/** `POST /reminders/{id}/retry`：无请求体；重试后 `status` 回到 `pending` */
export function retryReminder(reminderId: string): Promise<Reminder> {
  return apiPost<Reminder>(`/reminders/${reminderId}/retry`);
}
