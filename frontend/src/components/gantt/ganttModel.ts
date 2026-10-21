/**
 * 甘特图的行模型与时间轴换算（初版只读，F05 §7.2）。
 *
 * 两类图共用同一套"时间窗口"：
 * - **项目视图**：一个项目 + 它的任务，任务按 `parent_id` 组成层级；
 * - **成员视图**：工作组内已共享的任务按任务成员展开，一个任务出现多成员时
 *   会分别落在各自泳道里（任务本身没有被复制，只是视图行重复）。
 *
 * **日期一律按 `YYYY-MM-DD` 自然日处理**，不用 `Date` 做时区换算（F01 §6 第 2 条）。
 * 这里只做纯计算，组件只负责把行、时间刻度和任务条画出来。
 */

import dayjs, { type Dayjs } from 'dayjs';
import type { PlanProject, PlanTask } from '@/types/plan';

export type GanttScale = 'day' | 'week' | 'month';

export interface GanttScaleMeta {
  label: string;
  /** 每一天对应的像素宽度；日视图最宽，月视图最窄 */
  dayWidth: number;
  /** 左右各留多少天，避免任务条贴边 */
  paddingDays: number;
}

export const GANTT_SCALE_META: Record<GanttScale, GanttScaleMeta> = {
  day: { label: '日', dayWidth: 34, paddingDays: 3 },
  week: { label: '周', dayWidth: 12, paddingDays: 7 },
  month: { label: '月', dayWidth: 16, paddingDays: 15 },
};

export interface GanttProjectRow {
  readonly kind: 'project';
  readonly id: string;
  readonly parentId: null;
  readonly depth: 0;
  readonly hasChildren: boolean;
  readonly project: PlanProject;
}

export interface GanttMemberRow {
  readonly kind: 'member';
  readonly id: string;
  readonly parentId: null;
  readonly depth: 0;
  readonly hasChildren: boolean;
  readonly memberId: string;
  readonly displayName: string;
  readonly username: string;
  readonly avatarUpdatedAt: string | null;
  readonly taskCount: number;
  readonly progress: number;
  readonly startDate: string;
  readonly endDate: string;
  /** 该泳道里的任务（同一任务多成员时各泳道各一份引用） */
  readonly tasks: readonly PlanTask[];
}

export interface GanttTaskRow {
  readonly kind: 'task';
  readonly id: string;
  readonly parentId: string;
  readonly depth: number;
  readonly hasChildren: boolean;
  readonly task: PlanTask;
  /** 成员视图里该行所属泳道的 key；项目视图为 `null` */
  readonly memberKey: string | null;
}

export type GanttRow = GanttProjectRow | GanttMemberRow | GanttTaskRow;

/** 成员分组用的 key：真实用户 id，或没有任务成员时的兜底分组 */
export const UNASSIGNED_MEMBER_KEY = '__unassigned__';

export function isProjectGanttRow(row: GanttRow): row is GanttProjectRow {
  return row.kind === 'project';
}

export function isMemberGanttRow(row: GanttRow): row is GanttMemberRow {
  return row.kind === 'member';
}

export function isTaskGanttRow(row: GanttRow): row is GanttTaskRow {
  return row.kind === 'task';
}

/**
 * 项目视图：项目行 + 任务层级。
 *
 * 顶层任务的 `parentId` 指向项目行，因此收起项目行会连带收起所有任务；
 * 父任务被收起时，其整棵子树由 `filterCollapsedRows` 一并移除。
 */
export function buildProjectGanttRows(
  project: PlanProject,
  tasks: readonly PlanTask[],
): GanttRow[] {
  const rows: GanttRow[] = [];
  const childrenOf = new Map<string | null, PlanTask[]>();
  for (const task of tasks) {
    const key = task.parent_id ?? null;
    const bucket = childrenOf.get(key);
    if (bucket) {
      bucket.push(task);
    } else {
      childrenOf.set(key, [task]);
    }
  }

  const projectRow: GanttProjectRow = {
    kind: 'project',
    id: `project:${project.id}`,
    parentId: null,
    depth: 0,
    hasChildren: tasks.length > 0,
    project,
  };
  rows.push(projectRow);

  const visited = new Set<string>();
  const walk = (parentId: string | null, depth: number): void => {
    for (const task of childrenOf.get(parentId) ?? []) {
      if (visited.has(task.id)) {
        continue;
      }
      visited.add(task.id);
      const hasChildren = (childrenOf.get(task.id) ?? []).length > 0;
      rows.push({
        kind: 'task',
        id: `task:${task.id}`,
        // 行 id 与 parentId 都必须是**行 id**，不能混用原始任务 id：
        // 否则收起"需求梳理"时，它的子任务会因 parentId 对不上而继续显示
        parentId: parentId === null ? projectRow.id : `task:${parentId}`,
        depth,
        hasChildren,
        task,
        memberKey: null,
      });
      if (hasChildren) {
        walk(task.id, depth + 1);
      }
    }
  };
  walk(null, 1);

  // 父任务不可见时仍要显示子任务，避免"任务数对不上"
  for (const task of tasks) {
    if (!visited.has(task.id)) {
      visited.add(task.id);
      rows.push({
        kind: 'task',
        id: `task:${task.id}`,
        parentId: projectRow.id,
        depth: 1,
        hasChildren: false,
        task,
        memberKey: null,
      });
    }
  }

  return rows;
}

export interface GanttMemberOption {
  readonly id: string;
  readonly display_name: string;
  readonly username: string;
}

/** 从任务成员里收集成员下拉选项（含"未分配"兜底项，放在最后） */
export function collectGanttMembers(tasks: readonly PlanTask[]): GanttMemberOption[] {
  const byId = new Map<string, GanttMemberOption>();
  let hasUnassigned = false;
  for (const task of tasks) {
    const members = effectiveTaskMembers(task);
    if (members.length === 0) {
      hasUnassigned = true;
    }
    for (const member of members) {
      if (!byId.has(member.user_id)) {
        byId.set(member.user_id, {
          id: member.user_id,
          display_name: member.display_name,
          username: member.username,
        });
      }
    }
  }
  const options = [...byId.values()].sort((left, right) =>
    left.display_name.localeCompare(right.display_name, 'zh-CN'),
  );
  if (hasUnassigned) {
    options.push({ id: UNASSIGNED_MEMBER_KEY, display_name: '未分配', username: '' });
  }
  return options;
}

/** 任务成员为空时用持有者兜底；两者都没有则归入"未分配" */
function effectiveTaskMembers(task: PlanTask): PlanTask['members'] {
  if (task.members.length > 0) {
    return task.members;
  }
  return task.owner ? [task.owner] : [];
}

/**
 * 成员视图：每个成员一段泳道，泳道下是该成员参与的任务。
 *
 * `memberId` 有值时只保留该成员；`null` 表示全部成员。
 * 同一任务有多名成员时会复制到多个泳道——这是"以成员为锚点"的刻意语义，
 * 不是数据重复（任务总数与汇总仍以服务端 `summary` 为准）。
 */
export function buildMemberGanttRows(
  tasks: readonly PlanTask[],
  memberId: string | null,
): GanttRow[] {
  interface Bucket {
    key: string;
    displayName: string;
    username: string;
    avatarUpdatedAt: string | null;
    tasks: PlanTask[];
  }

  const buckets = new Map<string, Bucket>();
  for (const task of tasks) {
    const members = effectiveTaskMembers(task);
    if (members.length === 0) {
      const bucket = buckets.get(UNASSIGNED_MEMBER_KEY) ?? {
        key: UNASSIGNED_MEMBER_KEY,
        displayName: '未分配',
        username: '',
        avatarUpdatedAt: null,
        tasks: [],
      };
      bucket.tasks.push(task);
      buckets.set(bucket.key, bucket);
      continue;
    }
    for (const member of members) {
      const bucket = buckets.get(member.user_id) ?? {
        key: member.user_id,
        displayName: member.display_name,
        username: member.username,
        avatarUpdatedAt: member.avatar_updated_at,
        tasks: [],
      };
      if (!bucket.tasks.some((existing) => existing.id === task.id)) {
        bucket.tasks.push(task);
      }
      buckets.set(bucket.key, bucket);
    }
  }

  const selected = [...buckets.values()].filter(
    (bucket) => memberId === null || bucket.key === memberId,
  );
  selected.sort((left, right) => {
    if (left.key === UNASSIGNED_MEMBER_KEY) {
      return 1;
    }
    if (right.key === UNASSIGNED_MEMBER_KEY) {
      return -1;
    }
    return left.displayName.localeCompare(right.displayName, 'zh-CN');
  });

  const rows: GanttRow[] = [];
  for (const bucket of selected) {
    const sortedTasks = [...bucket.tasks].sort(compareTasks);
    const memberRowId = `member:${bucket.key}`;
    const startDate = sortedTasks.reduce(
      (earliest, task) => (task.start_date < earliest ? task.start_date : earliest),
      sortedTasks[0]?.start_date ?? '',
    );
    const endDate = sortedTasks.reduce(
      (latest, task) => (task.end_date > latest ? task.end_date : latest),
      sortedTasks[0]?.end_date ?? '',
    );
    const progress =
      sortedTasks.length === 0
        ? 0
        : Math.round(
            sortedTasks.reduce((sum, task) => sum + task.progress, 0) / sortedTasks.length,
          );
    rows.push({
      kind: 'member',
      id: memberRowId,
      parentId: null,
      depth: 0,
      hasChildren: sortedTasks.length > 0,
      memberId: bucket.key,
      displayName: bucket.displayName,
      username: bucket.username,
      avatarUpdatedAt: bucket.avatarUpdatedAt,
      taskCount: sortedTasks.length,
      progress,
      startDate,
      endDate,
      tasks: sortedTasks,
    });
    for (const task of sortedTasks) {
      rows.push({
        kind: 'task',
        id: `${memberRowId}:task:${task.id}`,
        parentId: memberRowId,
        depth: 1,
        hasChildren: false,
        task,
        memberKey: bucket.key,
      });
    }
  }
  return rows;
}

function compareTasks(left: PlanTask, right: PlanTask): number {
  return (
    left.start_date.localeCompare(right.start_date) ||
    left.end_date.localeCompare(right.end_date) ||
    left.name.localeCompare(right.name, 'zh-CN')
  );
}

/**
 * 收起某一行：它的整棵子树都不再渲染。
 * 依赖"父行一定排在子行之前"——两种 build 函数都保证这一点。
 */
export function filterCollapsedRows(
  rows: readonly GanttRow[],
  collapsedIds: ReadonlySet<string>,
): GanttRow[] {
  const hidden = new Set<string>();
  const visible: GanttRow[] = [];
  for (const row of rows) {
    if (row.parentId !== null && (collapsedIds.has(row.parentId) || hidden.has(row.parentId))) {
      hidden.add(row.id);
      continue;
    }
    visible.push(row);
  }
  return visible;
}

export interface GanttMonthSegment {
  readonly key: string;
  readonly label: string;
  readonly startIndex: number;
  readonly days: number;
}

export interface GanttTick {
  readonly index: number;
  readonly label: string;
  readonly sublabel: string;
}

export interface GanttTimeWindow {
  readonly scale: GanttScale;
  readonly start: Dayjs;
  readonly end: Dayjs;
  readonly days: number;
  readonly dayWidth: number;
  readonly width: number;
  readonly months: GanttMonthSegment[];
  readonly ticks: GanttTick[];
  /** 今天在窗口里的天偏移；不在窗口内为 `null` */
  readonly todayIndex: number | null;
  readonly label: string;
}

const WEEKDAYS = ['日', '一', '二', '三', '四', '五', '六'];

/**
 * 由一组自然日构造时间窗口。
 *
 * 空数组时回退到"今天前后各半个月"，保证页面仍有可用的时间轴，而不是一张空白图。
 */
export function buildGanttWindow(
  dates: readonly string[],
  scale: GanttScale,
  today: string,
): GanttTimeWindow {
  const meta = GANTT_SCALE_META[scale];
  const parsed = dates
    .map((value) => dayjs(value))
    .filter((value) => value.isValid())
    .map((value) => value.startOf('day'));

  const todayDate = dayjs(today).startOf('day');
  let first = parsed.length > 0 ? parsed.reduce((a, b) => (a.isBefore(b) ? a : b)) : todayDate;
  let last = parsed.length > 0 ? parsed.reduce((a, b) => (a.isAfter(b) ? a : b)) : todayDate;
  if (parsed.length === 0) {
    first = todayDate.subtract(14, 'day');
    last = todayDate.add(14, 'day');
  }

  const start = first.subtract(meta.paddingDays, 'day').startOf('day');
  const end = last.add(meta.paddingDays, 'day').startOf('day');
  const days = Math.max(1, end.diff(start, 'day') + 1);
  const dayWidth = meta.dayWidth;

  const months: GanttMonthSegment[] = [];
  for (let index = 0; index < days; index += 1) {
    const date = start.add(index, 'day');
    const key = date.format('YYYY-MM');
    const current = months[months.length - 1];
    if (current?.key === key) {
      months[months.length - 1] = { ...current, days: current.days + 1 };
    } else {
      months.push({
        key,
        label: date.format('YYYY年M月'),
        startIndex: index,
        days: 1,
      });
    }
  }

  const ticks: GanttTick[] = [];
  if (scale === 'day') {
    for (let index = 0; index < days; index += 1) {
      const date = start.add(index, 'day');
      ticks.push({
        index,
        label: date.format('D'),
        sublabel: WEEKDAYS[date.day()],
      });
    }
  } else if (scale === 'week') {
    for (let index = 0; index < days; index += 1) {
      const date = start.add(index, 'day');
      if (index === 0 || date.day() === 1) {
        ticks.push({
          index,
          label: date.format('M/D'),
          sublabel: '',
        });
      }
    }
  }

  const todayIndex = todayDate.diff(start, 'day');
  const hasToday = todayIndex >= 0 && todayIndex < days;
  const label = `${start.format('YYYY-MM-DD')} ~ ${end.format('YYYY-MM-DD')}`;

  return {
    scale,
    start,
    end,
    days,
    dayWidth,
    width: days * dayWidth,
    months,
    ticks,
    todayIndex: hasToday ? todayIndex : null,
    label,
  };
}

/** 取出一行任务的日期范围（成员行用自身聚合范围） */
export function rowDates(row: GanttRow): string[] {
  if (row.kind === 'project') {
    return [row.project.start_date, row.project.end_date];
  }
  if (row.kind === 'member') {
    return row.startDate && row.endDate ? [row.startDate, row.endDate] : [];
  }
  return [row.task.start_date, row.task.end_date];
}

export interface GanttBarGeometry {
  readonly left: number;
  readonly width: number;
  /** 任务是否完全落在窗口之外；调用方可以跳过渲染 */
  readonly visible: boolean;
}

/**
 * 任务条的像素几何：按自然日包含端点，超出窗口的部分裁掉。
 * 最小宽度 4px，避免"同一天开始/结束"的任务细成一条线。
 */
export function barGeometry(
  startDate: string,
  endDate: string,
  window: GanttTimeWindow,
): GanttBarGeometry {
  const rawStart = dayjs(startDate);
  const rawEnd = dayjs(endDate);
  if (!rawStart.isValid() || !rawEnd.isValid()) {
    return { left: 0, width: 0, visible: false };
  }
  const start = rawStart.startOf('day');
  const end = rawEnd.startOf('day').isBefore(start) ? start : rawEnd.startOf('day');
  if (end.isBefore(window.start) || start.isAfter(window.end)) {
    return { left: 0, width: 0, visible: false };
  }
  const clippedStart = start.isBefore(window.start) ? window.start : start;
  const clippedEnd = end.isAfter(window.end) ? window.end : end;
  const startIndex = clippedStart.diff(window.start, 'day');
  const dayCount = Math.max(1, clippedEnd.diff(clippedStart, 'day') + 1);
  return {
    left: startIndex * window.dayWidth,
    width: Math.max(4, dayCount * window.dayWidth),
    visible: true,
  };
}
