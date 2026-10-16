/**
 * 提醒域的状态文案与配色（《A08》§`status` 枚举）。
 *
 * **类型文案不由前端维护**：接口直接返回 `kind_label`（中文），
 * 前端自己再写一份映射就会在服务端加类型时悄悄落后——所以这里只放 `status`。
 *
 * `failed` 与 `skipped` 必须分开看（《A08》§`status` 枚举）：
 * `failed` = 发送尝试失败，需要人排查；`skipped` = 按规则就不该发（没有邮箱、
 * 关了邮件提醒、任务已完成、**邮件功能未启用**），原因可能已经消失。
 * 两者都可以重试，但把它们混成一个"异常"数字会让失败列表永远噪声不断。
 */

import type { ReminderStatus } from '@/types/reminder';
import type { StatusTone } from '@/components/common/StatusTag';

export interface ReminderStatusMeta {
  label: string;
  tone: StatusTone;
  /** 悬浮说明：这个状态意味着什么 */
  hint: string;
}

const STATUS_META: Record<ReminderStatus, ReminderStatusMeta> = {
  pending: {
    label: '待发送',
    tone: 'info',
    hint: '已排队：计划时间未到，或还没被派发循环取走。不能重试（已在队列里）。',
  },
  sent: {
    label: '已发送',
    tone: 'success',
    hint: '发送成功。不能重试——重试等于重复发送。',
  },
  failed: {
    label: '发送失败',
    tone: 'error',
    hint: '发送尝试失败（SMTP 报错、超时等），需要人排查。可以重试。',
  },
  skipped: {
    label: '已跳过',
    tone: 'warning',
    hint: '按规则不应发生发送（没有邮箱、已停用、关闭了邮件提醒、任务已完成/取消、邮件功能未启用）。可以重试。',
  },
};

export function reminderStatusMeta(status: ReminderStatus | string | null | undefined): ReminderStatusMeta {
  if (status && status in STATUS_META) {
    return STATUS_META[status as ReminderStatus];
  }
  return { label: String(status ?? '未知'), tone: 'default', hint: '未知状态' };
}

/** **只有 `failed` / `skipped` 可重试**；`pending` / `sent` 会拿到 409（《A08》§端点 3） */
export function canRetryReminder(status: ReminderStatus | string | null | undefined): boolean {
  return status === 'failed' || status === 'skipped';
}

/** 状态筛选下拉的选项（**客户端筛选当前页**，因为接口没有 `status` 参数） */
export const REMINDER_STATUS_OPTIONS: { value: ReminderStatus; label: string }[] = [
  { value: 'failed', label: '发送失败' },
  { value: 'skipped', label: '已跳过' },
  { value: 'pending', label: '待发送' },
  { value: 'sent', label: '已发送' },
];

/** 类型筛选下拉：只用 5 个枚举值（服务端不校验取值集合，传别的会得到空列表而不是 400） */
export const REMINDER_KIND_OPTIONS: { value: string; label: string }[] = [
  { value: 'task_assigned', label: '新任务分配' },
  { value: 'task_start_soon', label: '任务即将开始' },
  { value: 'task_due_soon', label: '任务即将到期' },
  { value: 'task_overdue', label: '任务已延期' },
  { value: 'task_owner_changed', label: '任务持有者变更' },
];
