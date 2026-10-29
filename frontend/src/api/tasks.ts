/**
 * 任务域接口（对应《A05》）。
 *
 * **任务成员身份不是编辑前提**：能否改任务由*项目权限 + 任务是否共享*决定（《A05》§权限速查）。
 * 任务单端点在 `/tasks/{task_id}`（与项目无嵌套）；创建与列表挂在项目下。
 */

import { apiDelete, apiGet, apiPatch, apiPost, apiPostQuery } from './client';
import type { Page } from '@/types/common';
import type {
  AddTaskMemberBody,
  CopyTaskBody,
  CreateTaskBody,
  DeleteTaskBody,
  Task,
  TaskMember,
  TaskMemberQueryBody,
  TaskQueryBody,
  UpdateTaskBody,
  UpdateTaskDatesBody,
  UpdateTaskMemberBody,
} from '@/types/task';

/** `POST /projects/{project_id}/tasks`：创建者成为任务持有者 */
export function createTask(projectId: string, body: CreateTaskBody): Promise<Task> {
  return apiPost<Task>(`/projects/${projectId}/tasks`, body);
}

/** `POST /projects/{project_id}/tasks/query` */
export function queryProjectTasks(
  projectId: string,
  body: TaskQueryBody,
  signal?: AbortSignal,
): Promise<Page<Task>> {
  return apiPostQuery<Task>(`/projects/${projectId}/tasks/query`, body, { signal });
}

/** `GET /tasks/{id}` */
export function getTask(taskId: string, signal?: AbortSignal): Promise<Task> {
  return apiGet<Task>(`/tasks/${taskId}`, { signal });
}

/** `PATCH /tasks/{id}` */
export function updateTask(taskId: string, body: UpdateTaskBody, signal?: AbortSignal): Promise<Task> {
  return apiPatch<Task>(`/tasks/${taskId}`, body, { signal });
}

/** `PATCH /tasks/{id}/dates`：**只接受日期与版本字段**，多带字段会 422 */
export function updateTaskDates(taskId: string, body: UpdateTaskDatesBody, signal?: AbortSignal): Promise<Task> {
  return apiPatch<Task>(`/tasks/${taskId}/dates`, body, { signal });
}

/** `DELETE /tasks/{id}`：逻辑删除，也要求 JSON 请求体 */
export function deleteTask(taskId: string, body: DeleteTaskBody): Promise<void> {
  return apiDelete<void>(`/tasks/${taskId}`, body);
}

/**
 * `POST /tasks/{id}/copy`
 *
 * `Idempotency-Key` 可选：首次创建返回 **201**、重放返回 **200**。
 * key 由调用方用 `useIdempotencyKey` 生成，网络重试要**复用同一个 key**（F01 §5.5）。
 */
export function copyTask(
  taskId: string,
  body: CopyTaskBody,
  idempotencyKey?: string,
): Promise<Task> {
  return apiPost<Task>(`/tasks/${taskId}/copy`, body, {
    headers: idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : undefined,
  });
}

/** `POST /tasks/{id}/members/query` */
export function queryTaskMembers(
  taskId: string,
  body: TaskMemberQueryBody,
  signal?: AbortSignal,
): Promise<Page<TaskMember>> {
  return apiPostQuery<TaskMember>(`/tasks/${taskId}/members/query`, body, { signal });
}

/** `POST /tasks/{id}/members`：目标必须是该项目成员 */
export function addTaskMember(taskId: string, body: AddTaskMemberBody): Promise<TaskMember> {
  return apiPost<TaskMember>(`/tasks/${taskId}/members`, body);
}

/** `PATCH /tasks/{id}/members/{userId}`：`role: 'owner'` = **转移**持有者 */
export function updateTaskMember(
  taskId: string,
  userId: string,
  body: UpdateTaskMemberBody,
): Promise<TaskMember> {
  return apiPatch<TaskMember>(`/tasks/${taskId}/members/${userId}`, body);
}

/** `DELETE /tasks/{id}/members/{userId}`：204；不得移除唯一持有者 */
export function removeTaskMember(taskId: string, userId: string): Promise<void> {
  return apiDelete<void>(`/tasks/${taskId}/members/${userId}`);
}
