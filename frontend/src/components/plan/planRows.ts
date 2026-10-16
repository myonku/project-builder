/**
 * 计划视图的行模型（个人计划与组内计划共用）。
 *
 * 服务端返回的是**两个平铺数组**（`projects` + `tasks`，见《A06》§1）：
 * - `projects` 已按 `end_date`、`id` 升序；
 * - `tasks` 已按 `start_date`、`end_date`、`id` 升序，层级靠 `parent_id` 自行组装（最多 3 层）。
 *
 * 这里把两者合成**一张表**：每个项目一行"项目行"，其下紧跟它自己的任务行（按 `parent_id` 递归缩进）。
 * 之所以不用 DataGrid 的 `treeData`：那需要行自带完整路径且对行类型统一，
 * 而我们的项目行与任务行**语义不同**（列展示、点击行为、权限来源都不一样），
 * 用"扁平行 + depth 缩进"更直白，也不依赖额外特性。
 */

import type { PlanProject, PlanTask, PlanViewPayload } from '@/types/plan';
import type { ProjectStatus } from '@/types/project';
import type { TaskStatus } from '@/types/task';

/** 缩进的最大层级（《A05》：任务层级最多 3 层；防御性地留一层余量） */
const MAX_DEPTH = 4;

export interface PlanProjectRow {
  /** 行 id：项目行的 id 就是项目 id（与任务 id 不会重复） */
  readonly id: string;
  readonly kind: 'project';
  readonly project: PlanProject;
  readonly depth: 0;
}

export interface PlanTaskRow {
  readonly id: string;
  readonly kind: 'task';
  readonly task: PlanTask;
  /** 0 = 顶层任务；项目行下的第一层任务 depth 为 1 */
  readonly depth: number;
}

export type PlanRow = PlanProjectRow | PlanTaskRow;

export function isProjectRow(row: PlanRow): row is PlanProjectRow {
  return row.kind === 'project';
}

export function isTaskRow(row: PlanRow): row is PlanTaskRow {
  return row.kind === 'task';
}

/**
 * 把 `projects` + `tasks` 合成扁平行数组：项目行在前，其任务行紧随其后。
 *
 * 同一父任务下的子任务保持**服务端给的顺序**（`start_date` → `end_date` → `id`）。
 * 带 `visited` 守卫：万一数据里出现环（正常不会），也只是少显示而不是死循环。
 */
export function buildPlanRows(payload: PlanViewPayload | null | undefined): PlanRow[] {
  if (!payload) {
    return [];
  }

  const tasksByProject = new Map<string, PlanTask[]>();
  for (const task of payload.tasks) {
    const bucket = tasksByProject.get(task.project_id);
    if (bucket) {
      bucket.push(task);
    } else {
      tasksByProject.set(task.project_id, [task]);
    }
  }

  const rows: PlanRow[] = [];
  for (const project of payload.projects) {
    rows.push({ id: project.id, kind: 'project', project, depth: 0 });

    const projectTasks = tasksByProject.get(project.id) ?? [];
    if (projectTasks.length === 0) {
      continue;
    }

    // parent_id → 子任务（保持服务端顺序）
    const childrenOf = new Map<string | null, PlanTask[]>();
    for (const task of projectTasks) {
      const key = task.parent_id ?? null;
      const bucket = childrenOf.get(key);
      if (bucket) {
        bucket.push(task);
      } else {
        childrenOf.set(key, [task]);
      }
    }

    const visited = new Set<string>();
    const walk = (parentId: string | null, depth: number): void => {
      if (depth > MAX_DEPTH) {
        return;
      }
      for (const task of childrenOf.get(parentId) ?? []) {
        if (visited.has(task.id)) {
          continue;
        }
        visited.add(task.id);
        rows.push({ id: task.id, kind: 'task', task, depth });
        walk(task.id, depth + 1);
      }
    };
    walk(null, 1);

    // 父任务不可见（被筛掉）时，子任务不会出现在上面的遍历里；补一遍兜底，避免"任务总数对不上"
    for (const task of projectTasks) {
      if (!visited.has(task.id)) {
        visited.add(task.id);
        rows.push({ id: task.id, kind: 'task', task, depth: 1 });
      }
    }
  }

  return rows;
}

export function planRowName(row: PlanRow): string {
  return isProjectRow(row) ? row.project.name : row.task.name;
}

export function planRowStatus(row: PlanRow): TaskStatus | ProjectStatus {
  return isProjectRow(row) ? row.project.status : row.task.status;
}

export function planRowProgress(row: PlanRow): number {
  return isProjectRow(row) ? row.project.progress : row.task.progress;
}

export function planRowIsOverdue(row: PlanRow): boolean {
  return isProjectRow(row) ? row.project.overdue_task_count > 0 : row.task.is_overdue;
}

export function planRowStart(row: PlanRow): string {
  return isProjectRow(row) ? row.project.start_date : row.task.start_date;
}

export function planRowEnd(row: PlanRow): string {
  return isProjectRow(row) ? row.project.end_date : row.task.end_date;
}
