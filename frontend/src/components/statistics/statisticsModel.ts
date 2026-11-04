import type { PlanProject, PlanTask, PlanTaskMember } from '../../types/plan';
import type { TaskStatus } from '../../types/task';

export const STATUSES: TaskStatus[] = ['not_started', 'in_progress', 'completed', 'cancelled'];
export const DUE_LABELS = ['逾期', '今天', '未来 1–7 天', '未来 8–30 天', '30 天后'] as const;
export type Selection = { kind: 'status'; value: TaskStatus } | { kind: 'due'; value: number };
export const isOpen = (task: PlanTask) => task.status === 'not_started' || task.status === 'in_progress';
export const uniqueTasks = (tasks: readonly PlanTask[]) => [...new Map(tasks.map(task => [task.id, task])).values()];
export const leafTasks = (tasks: readonly PlanTask[]) => uniqueTasks(tasks).filter(task => !task.has_children);
export const isUUID = (value: string) => /^[\da-f]{8}-[\da-f]{4}-[\da-f]{4}-[\da-f]{4}-[\da-f]{12}$/i.test(value);
export function validDate(value: string) {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return false;
  const date = new Date(`${value}T00:00:00Z`);
  return Number.isFinite(date.getTime()) && date.toISOString().slice(0, 10) === value;
}
// UTC is used only to calculate the distance between calendar dates, never to convert a task date.
export const dayDistance = (date: string, today: string) =>
  Math.round((Date.parse(`${date}T00:00:00Z`) - Date.parse(`${today}T00:00:00Z`)) / 86_400_000);
export function dueBucket(task: PlanTask, today: string): number | null {
  if (!isOpen(task)) return null;
  if (task.is_overdue) return 0;
  const days = dayDistance(task.end_date, today);
  if (days < 0 || !Number.isFinite(days)) return null;
  return days === 0 ? 1 : days <= 7 ? 2 : days <= 30 ? 3 : 4;
}
export function taskMetrics(tasks: readonly PlanTask[]) {
  const status = Object.fromEntries(STATUSES.map(s => [s, 0])) as Record<TaskStatus, number>;
  let overdue = 0;
  for (const task of tasks) { status[task.status]++; if (task.is_overdue && isOpen(task)) overdue++; }
  const denominator = tasks.length - status.cancelled;
  return { total: tasks.length, status, overdue, open: status.not_started + status.in_progress,
    completion: denominator ? Math.round(status.completed / denominator * 100) : null };
}
export function selectedTasks(tasks: readonly PlanTask[], selection: Selection | null, today: string) {
  if (!selection) return [...tasks];
  return tasks.filter(task => selection.kind === 'status'
    ? task.status === selection.value : dueBucket(task, today) === selection.value);
}
export function statisticsScope(projects: readonly PlanProject[], tasks: readonly PlanTask[], projectId: string, from: string, to: string) {
  const baseProjects = projects.filter(project => !projectId || project.id === projectId);
  const ids = new Set(baseProjects.map(project => project.id));
  const leaves = leafTasks(tasks).filter(task => ids.has(task.project_id));
  const filtered = leaves.filter(task => (!from || task.end_date >= from) && (!to || task.end_date <= to));
  const hits = new Set(filtered.map(task => task.project_id));
  return { leaves, tasks: filtered, projects: from || to ? baseProjects.filter(project => hits.has(project.id)) : baseProjects };
}
export interface MemberDistribution { member: PlanTaskMember; tasks: PlanTask[] }
export function memberDistribution(leaves: readonly PlanTask[], filtered: readonly PlanTask[]): MemberDistribution[] {
  const rows = new Map<string, MemberDistribution>();
  for (const task of leaves) for (const member of task.members) {
    if (!rows.has(member.user_id)) rows.set(member.user_id, { member, tasks: [] });
  }
  for (const task of filtered) for (const id of new Set(task.members.map(member => member.user_id))) {
    rows.get(id)?.tasks.push(task);
  }
  return [...rows.values()].sort((a, b) => taskMetrics(b.tasks).open - taskMetrics(a.tasks).open
    || a.member.display_name.localeCompare(b.member.display_name) || a.member.user_id.localeCompare(b.member.user_id));
}
export function reportText(scope: string, tasks: readonly PlanTask[], projectCount: number, readAt: string, today: string, origin: string, notes: string[] = []) {
  const m = taskMetrics(tasks);
  const attention = tasks.filter(isOpen).sort((a, b) => a.end_date.localeCompare(b.end_date) || a.id.localeCompare(b.id));
  return ['Worklane 数据统计', scope, `最近成功读取：${readAt}`, '口径：当前可见叶子任务；日期范围按截止日期，不是历史完成期间。',
    `涉及项目 ${projectCount}；叶子任务 ${m.total}；已完成 ${m.status.completed}；未完成 ${m.open}；逾期 ${m.overdue}；已取消 ${m.status.cancelled}。`,
    ...notes,
    `未完成到期分布（今天 ${today}）：${DUE_LABELS.map((label, i) => `${label} ${tasks.filter(t => dueBucket(t, today) === i).length}`).join('；')}。`,
    '需关注任务（按截止日期）：', ...attention.slice(0, 5).map(t => `- ${t.name} · ${t.end_date}${t.is_overdue ? ' · 逾期' : ''}\n  ${origin}/projects/${t.project_id}?taskId=${t.id}`),
    ...(attention.length > 5 ? [`另有 ${attention.length - 5} 条未完成任务未列出。`] : []),
    '成员参与任务可重复计入不同成员，不代表工时或效率。链接按接收者权限实时读取。'].join('\n');
}
