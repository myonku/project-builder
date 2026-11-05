import type { InsightTask } from '../../api/insights';
import type { PlanTask } from '../../types/plan';
import type { TaskStatus } from '../../types/task';
export type PeriodPreset = 'week' | 'lastWeek' | 'month' | 'lastMonth';
export function offsetDate(date: string, days: number) {return new Date(Date.parse(date + 'T00:00:00Z') + days * 86400000).toISOString().slice(0, 10);}
export function periodRange(preset: PeriodPreset, today: string) {
 const date = new Date(today + 'T00:00:00Z');
 if (preset === 'week' || preset === 'lastWeek') {
  const start = offsetDate(today, -((date.getUTCDay() + 6) % 7) - (preset === 'lastWeek' ? 7 : 0));
  return {from: start, to: offsetDate(start, 6)};
 }
 const month = new Date(Date.UTC(date.getUTCFullYear(), date.getUTCMonth() - (preset === 'lastMonth' ? 1 : 0), 1));
 const next = new Date(Date.UTC(month.getUTCFullYear(), month.getUTCMonth() + 1, 1));
 return {from: month.toISOString().slice(0, 10), to: offsetDate(next.toISOString().slice(0, 10), -1)};
}
export function asPlanTasks(tasks: InsightTask[], today: string): PlanTask[] {
 return tasks.map(task => ({...task, status: task.status as TaskStatus, priority: task.priority as 0 | 1 | 2 | 3,
  parent_id: null, description: null, owner: null, is_overdue: (task.status === 'in_progress' || task.status === 'not_started') && task.end_date < today,
  updated_at: '', updated_by: '', updated_by_name: null,
  members: task.members.map(member => ({...member, role: 'member' as const, avatar_updated_at: null})),
 }));
}
export function topCategories<T extends {label: string; value: number; second?: number; key?: string}>(rows: T[], max: number) {
 if (rows.length <= max) return rows;
 return [...rows.slice(0, max - 1), {label: `其他（${rows.length - max + 1}项）`, key: 'other', value: rows.slice(max - 1).reduce((n, v) => n + v.value, 0), second: rows.slice(max - 1).reduce((n, v) => n + (v.second ?? 0), 0)}];
}
