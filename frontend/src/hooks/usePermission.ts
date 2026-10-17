/**
 * 权限判定（F02 §8 的三条硬约定）。
 *
 * **不要**在页面里散写权限判断，全部走这里：
 *
 * 1. `membership_role` / `my_role` 只用于展示，**不用于权限判定**；
 * 2. 项目能不能改：`membership_role === 'owner'` **或** 当前用户 `system_role === 'admin'`；
 * 3. 归档是**对所有人生效**的只读（含系统管理员）：`status === 'archived' || archived_at !== null`。
 *
 * 任务的可写性还有一个"客户端信息不完备"的现实：`/projects/*` 返回的 `Project`
 * 不带"我是不是归属组活跃成员"，因此当 `visibility === 'group_editable'` 时，
 * 本 Hook 只在**任务已共享**（`is_shared`）的前提下按可写处理；
 * 计划视图返回的 `PlanTask.can_edit` 才是服务端权威结论——两者都在时优先用后者。
 */

import * as React from 'react';
import { useAuthStore } from '@/stores/auth';
import type { PlanTask } from '@/types/plan';
import type { Project } from '@/types/project';
import type { Task } from '@/types/task';
import type { SystemRole, User } from '@/types/user';

/** 归档判定：`status` 与 `archived_at` **任一**成立即视为归档 */
export function isArchived(project: {
  status: string;
  archived_at: string | null;
}): boolean {
  return project.status === 'archived' || project.archived_at !== null;
}

/** 归档后项目及其下任务对所有人只读（含系统管理员） */
export function isProjectReadOnly(project: {
  status: string;
  archived_at: string | null;
}): boolean {
  return isArchived(project);
}

export function isSystemAdmin(user: User | null): boolean {
  return user?.system_role === 'admin';
}

/**
 * 能否改**项目本身**（名称/日期/共享级别/成员）：持有者或系统管理员，且未归档。
 * 组内共享级别**不**授予项目管理权（《A04》§权限速查）。
 */
export function canManageProject(
  project: Pick<Project, 'status' | 'archived_at' | 'membership_role'>,
  user: User | null,
): boolean {
  if (isProjectReadOnly(project)) {
    return false;
  }
  if (isSystemAdmin(user)) {
    return true;
  }
  return project.membership_role === 'owner';
}

/**
 * 能否写**项目下的任务**：项目级写权限 + 任务是否共享。
 *
 * - 项目可写（持有者/普通成员/系统管理员）→ 全部任务可写；
 * - 仅组内可见（`group_editable` / `group_readonly`）→ 只有**已共享**任务可写，
 *   且 `group_readonly` 永远只读（此时前端无法区分这两种 visibility 下的组内身份，
 *   因此保守地按"只有 `group_editable` 才可能写"处理）。
 */
export function canWriteTasksOfProject(
  project: Pick<Project, 'status' | 'archived_at' | 'membership_role' | 'visibility'>,
  user: User | null,
  task?: Pick<Task, 'is_shared'> | null,
): boolean {
  if (isProjectReadOnly(project)) {
    return false;
  }
  if (isSystemAdmin(user)) {
    return true;
  }
  if (project.membership_role === 'owner' || project.membership_role === 'member') {
    return true;
  }
  if (project.visibility === 'group_editable' && task?.is_shared) {
    return true;
  }
  return false;
}

/**
 * 能否写**项目级内容**（目前是项目附件；与"改项目本身"不同）。
 *
 * 服务端附件写权限是 `ProjectPermission >= PermWrite`（A07 `CanWriteAttachment`）：
 * - 项目持有者 / 普通成员 / 系统管理员；
 * - `group_editable` 的归属组活跃成员（即使不是项目成员）；
 * - 归档项目统一只读。
 *
 * 不要用 `canManageProject` 代替：普通成员可以传项目附件，但不能改项目本身。
 */
export function canWriteProjectContent(
  project: Pick<Project, 'status' | 'archived_at' | 'membership_role' | 'visibility'>,
  user: User | null,
): boolean {
  if (isProjectReadOnly(project)) {
    return false;
  }
  if (isSystemAdmin(user)) {
    return true;
  }
  if (project.membership_role === 'owner' || project.membership_role === 'member') {
    return true;
  }
  return project.visibility === 'group_editable';
}

/** 计划视图里服务端已经算好了 `can_edit`，直接采信 */
export function canEditPlanTask(task: Pick<PlanTask, 'can_edit'>): boolean {
  return task.can_edit;
}

/** 当前用户是否具备某个系统角色 */
export function hasSystemRole(user: User | null, role: SystemRole): boolean {
  return user?.system_role === role;
}

export interface PermissionHelpers {
  user: User | null;
  isAdmin: boolean;
  isArchived: typeof isArchived;
  isProjectReadOnly: typeof isProjectReadOnly;
  canManageProject: (
    project: Pick<Project, 'status' | 'archived_at' | 'membership_role'>,
  ) => boolean;
  canWriteTasksOfProject: (
    project: Pick<Project, 'status' | 'archived_at' | 'membership_role' | 'visibility'>,
    task?: Pick<Task, 'is_shared'> | null,
  ) => boolean;
  canWriteProjectContent: (
    project: Pick<Project, 'status' | 'archived_at' | 'membership_role' | 'visibility'>,
  ) => boolean;
  canEditPlanTask: typeof canEditPlanTask;
}

/** 绑定当前用户的权限判定集合；页面里只调这个 Hook */
export function usePermission(): PermissionHelpers {
  const user = useAuthStore((state) => state.user);

  return React.useMemo(
    () => ({
      user,
      isAdmin: isSystemAdmin(user),
      isArchived,
      isProjectReadOnly,
      canManageProject: (project) => canManageProject(project, user),
      canWriteTasksOfProject: (project, task) =>
        canWriteTasksOfProject(project, user, task),
      canWriteProjectContent: (project) => canWriteProjectContent(project, user),
      canEditPlanTask,
    }),
    [user],
  );
}
