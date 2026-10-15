/**
 * 任务域的**展示元数据**：状态、优先级的中文文案与标签配色。
 *
 * 与 `components/project/projectMeta.ts` 同构，只放"怎么显示"，
 * 不参与权限判定——能不能改任务看计划视图的 `can_edit` 或
 * `usePermission().canWriteTasksOfProject`（F02 §8）。
 */

import type { StatusTone } from '@/components/common/StatusTag';
import type { TaskPriority, TaskStatus } from '@/types/task';

export interface TaskStatusMeta {
  label: string;
  tone: StatusTone;
}

/** 没有 `overdue` 状态：逾期是派生字段 `is_overdue`（《A05》） */
const TASK_STATUS_META: Record<TaskStatus, TaskStatusMeta> = {
  not_started: { label: '未开始', tone: 'default' },
  in_progress: { label: '进行中', tone: 'primary' },
  completed: { label: '已完成', tone: 'success' },
  cancelled: { label: '已取消', tone: 'error' },
};

/** 0 低 / 1 普通 / 2 高 / 3 紧急 */
const TASK_PRIORITY_META: Record<TaskPriority, TaskStatusMeta> = {
  0: { label: '低', tone: 'default' },
  1: { label: '普通', tone: 'default' },
  2: { label: '高', tone: 'warning' },
  3: { label: '紧急', tone: 'error' },
};

const UNKNOWN: TaskStatusMeta = { label: '未知', tone: 'default' };

export function taskStatusMeta(status: TaskStatus | string | null | undefined): TaskStatusMeta {
  if (!status) {
    return UNKNOWN;
  }
  return TASK_STATUS_META[status as TaskStatus] ?? { label: String(status), tone: 'default' };
}

export function taskPriorityMeta(
  priority: TaskPriority | number | null | undefined,
): TaskStatusMeta {
  if (priority === null || priority === undefined) {
    return UNKNOWN;
  }
  return (
    TASK_PRIORITY_META[priority as TaskPriority] ?? {
      label: String(priority),
      tone: 'default',
    }
  );
}

export const TASK_STATUS_OPTIONS: { value: TaskStatus; label: string }[] = (
  Object.keys(TASK_STATUS_META) as TaskStatus[]
).map((value) => ({ value, label: TASK_STATUS_META[value].label }));

export const TASK_PRIORITY_OPTIONS: { value: TaskPriority; label: string }[] = (
  [0, 1, 2, 3] as TaskPriority[]
).map((value) => ({ value, label: TASK_PRIORITY_META[value].label }));
