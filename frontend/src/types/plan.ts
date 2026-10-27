/**
 * 计划视图的投影类型（对应《A06》与 F02 §5）。
 *
 * 整个投影都标 `readonly`：修改走的是 `/projects/*` 与 `/tasks/*`，
 * 它们返回 `Project` / `Task`（字段集合与 `PlanProject` / `PlanTask` 并不相同）。
 */

import type { ProjectStatus, ProjectVisibility } from './project';
import type { TaskPriority, TaskStatus } from './task';

export type PlanRole = 'owner' | 'member' | null;

export interface PlanProject {
  readonly id: string;
  readonly name: string;
  readonly description: string | null;
  readonly group_id: string;
  readonly status: ProjectStatus;
  readonly visibility: ProjectVisibility;
  readonly start_date: string;
  readonly end_date: string;
  readonly color: string | null;
  readonly archived_at: string | null;
  /** = `status === 'archived' || archived_at !== null` */
  readonly is_archived: boolean;
  readonly is_read_only: boolean;
  readonly owner_id: string | null;
  /** 计划视图里的 `membership_role` 别名，仅用于展示 */
  readonly my_role: PlanRole;
  /** **本视图内可见**的任务数（可能小于项目详情口径） */
  readonly task_count: number;
  readonly progress: number;
  readonly overdue_task_count: number;
  readonly updated_at: string;
}

/** 计划视图的成员对象**不带** `id` / `task_id`（与 `TaskMember` 不同） */
export interface PlanTaskMember {
  readonly user_id: string;
  readonly username: string;
  readonly display_name: string;
  readonly avatar_updated_at: string | null;
  readonly role: 'owner' | 'member';
}

export interface PlanTask {
  readonly has_children: boolean;
  readonly id: string;
  readonly project_id: string;
  readonly parent_id: string | null;
  readonly name: string;
  readonly description: string | null;
  readonly status: TaskStatus;
  readonly progress: number;
  readonly priority: TaskPriority;
  readonly start_date: string;
  readonly end_date: string;
  readonly is_milestone: boolean;
  readonly is_shared: boolean;
  readonly is_overdue: boolean;
  /** 归档项目恒为 false；组内只读共享同样 false */
  readonly can_edit: boolean;
  readonly owner: PlanTaskMember | null;
  /** 已按"持有者优先、显示名"排序 */
  readonly members: PlanTaskMember[];
  readonly updated_by: string;
  readonly updated_by_name: string | null;
  readonly updated_at: string;
}

export interface PlanSummary {
  readonly project_count: number;
  readonly task_count: number;
  readonly in_progress_task_count: number;
  readonly overdue_task_count: number;
  readonly overall_progress: number;
}

export interface PlanViewPayload {
  /** 个人计划恒为 null */
  readonly group_id: string | null;
  readonly projects: PlanProject[];
  readonly tasks: PlanTask[];
  readonly summary: PlanSummary;
}

/**
 * 项目甘特图端点的信封：顶层多两个权限字段。
 * **`can_manage_project` 对系统管理员可能为 `false`**（《A06》§3 备注），
 * 不要用它做权限判定的唯一依据（F02 §8）。
 */
export interface GanttPayload {
  readonly project: PlanProject;
  readonly can_manage_project: boolean;
  readonly can_edit_tasks: boolean;
  readonly tasks: PlanTask[];
  /** `project_count` 恒为 1 */
  readonly summary: PlanSummary;
}

/** 个人计划与组内计划共用的查询参数（走查询串；未知参数一律 400） */
export interface PlanViewQuery {
  /** 同时作用于项目与任务两侧 */
  project_id?: string;
  project_owner_id?: string;
  task_owner_id?: string;
  task_member_id?: string;
  project_status?: ProjectStatus;
  task_status?: TaskStatus;
  priority?: TaskPriority;
  /** `YYYY-MM-DD`，含端点 */
  due_from?: string;
  /** 同上；早于 `due_from` 返回 400 invalid_date_range */
  due_to?: string;
  /** true 只返回逾期；false 等价于不筛选 */
  overdue?: boolean;
  /** 默认 false */
  include_archived?: boolean;
}
