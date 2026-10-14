/**
 * 项目域接口（对应《A04》）。
 *
 * 权限提示：项目**更新与归档**对"不是持有者也不是系统管理员"返回 `403`；
 * 而**成员管理**（添加/改/删）对同一类请求者返回 `404 project_not_found`。
 * 前端按接口分别处理（《A04》§权限速查）。
 */

import { apiDelete, apiGet, apiPatch, apiPost, apiPostQuery } from './client';
import type { Page } from '@/types/common';
import type {
  AddProjectMemberBody,
  ArchiveProjectBody,
  CreateProjectBody,
  Project,
  ProjectMember,
  ProjectMemberQueryBody,
  ProjectQueryBody,
  UpdateProjectBody,
  UpdateProjectMemberBody,
} from '@/types/project';

/** `POST /projects` */
export function createProject(body: CreateProjectBody): Promise<Project> {
  return apiPost<Project>('/projects', body);
}

/** `POST /projects/query`：归档项目默认排除，`include_archived: true` 才查得到 */
export function queryProjects(
  body: ProjectQueryBody,
  signal?: AbortSignal,
): Promise<Page<Project>> {
  return apiPostQuery<Project>('/projects/query', body, { signal });
}

/** `GET /projects/{id}`：不可见与不存在共用 `project_not_found` */
export function getProject(projectId: string, signal?: AbortSignal): Promise<Project> {
  return apiGet<Project>(`/projects/${projectId}`, { signal });
}

/** `PATCH /projects/{id}` */
export function updateProject(
  projectId: string,
  body: UpdateProjectBody,
): Promise<Project> {
  return apiPatch<Project>(`/projects/${projectId}`, body);
}

/** `POST /projects/{id}/archive`：**不可逆**，没有解除归档的端点 */
export function archiveProject(
  projectId: string,
  body: ArchiveProjectBody,
): Promise<Project> {
  return apiPost<Project>(`/projects/${projectId}/archive`, body);
}

/** `POST /projects/{id}/members/query` */
export function queryProjectMembers(
  projectId: string,
  body: ProjectMemberQueryBody,
  signal?: AbortSignal,
): Promise<Page<ProjectMember>> {
  return apiPostQuery<ProjectMember>(`/projects/${projectId}/members/query`, body, { signal });
}

/** `POST /projects/{id}/members` */
export function addProjectMember(
  projectId: string,
  body: AddProjectMemberBody,
): Promise<ProjectMember> {
  return apiPost<ProjectMember>(`/projects/${projectId}/members`, body);
}

/** `PATCH /projects/{id}/members/{userId}`：`role: 'owner'` = **转移**持有者 */
export function updateProjectMember(
  projectId: string,
  userId: string,
  body: UpdateProjectMemberBody,
): Promise<ProjectMember> {
  return apiPatch<ProjectMember>(`/projects/${projectId}/members/${userId}`, body);
}

/** `DELETE /projects/{id}/members/{userId}`：204；不得移除唯一持有者 */
export function removeProjectMember(projectId: string, userId: string): Promise<void> {
  return apiDelete<void>(`/projects/${projectId}/members/${userId}`);
}
