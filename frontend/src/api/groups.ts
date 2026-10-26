/**
 * 工作组域接口（对应《A03》）。
 *
 * 注意：工作组**写接口**（添加/改成员）返回的 `username` / `display_name` / `email`
 * 是空字符串——要展示用户信息请用成员列表或 `GET /users/{id}`。
 */

import { apiDelete, apiGet, apiPatch, apiPost, apiPostQuery } from './client';
import type { Page } from '@/types/common';
import type {
  AddGroupMemberBody,
  CreateGroupBody,
  Group,
  GroupMember,
  GroupMemberQueryBody,
  GroupQueryBody,
  JoinRequest,
  JoinRequestQueryBody,
  ReviewJoinRequestBody,
  UpdateGroupBody,
  UpdateGroupMemberBody,
  DiscoverGroup,
  DiscoverGroupQuery,
  MyGroupJoinRequest,
  MyGroupJoinRequestQuery,
} from '@/types/group';

export function discoverGroups(body: DiscoverGroupQuery): Promise<Page<DiscoverGroup>> {
  return apiPostQuery<DiscoverGroup>('/groups/discover/query', body);
}

export function queryMyGroupJoinRequests(body: MyGroupJoinRequestQuery): Promise<Page<MyGroupJoinRequest>> {
  return apiPostQuery<MyGroupJoinRequest>('/me/group-join-requests/query', body);
}

/** `POST /groups` */
export function createGroup(body: CreateGroupBody): Promise<Group> {
  return apiPost<Group>('/groups', body);
}

/** `POST /groups/query` */
export function queryGroups(body: GroupQueryBody, signal?: AbortSignal): Promise<Page<Group>> {
  return apiPostQuery<Group>('/groups/query', body, { signal });
}

/** `GET /groups/{id}` */
export function getGroup(groupId: string, signal?: AbortSignal): Promise<Group> {
  return apiGet<Group>(`/groups/${groupId}`, { signal });
}

/** `PATCH /groups/{id}`：`owner_id` 用来**转移所有权** */
export function updateGroup(groupId: string, body: UpdateGroupBody): Promise<Group> {
  return apiPatch<Group>(`/groups/${groupId}`, body);
}

/** `POST /groups/{id}/members/query`：`status` 省略 = 同时返回 active 与 removed */
export function queryGroupMembers(
  groupId: string,
  body: GroupMemberQueryBody,
  signal?: AbortSignal,
): Promise<Page<GroupMember>> {
  return apiPostQuery<GroupMember>(`/groups/${groupId}/members/query`, body, { signal });
}

/** `POST /groups/{id}/members` */
export function addGroupMember(
  groupId: string,
  body: AddGroupMemberBody,
): Promise<GroupMember> {
  return apiPost<GroupMember>(`/groups/${groupId}/members`, body);
}

/** `PATCH /groups/{id}/members/{userId}`：同时提供 role 与 status 时只按 role 生效 */
export function updateGroupMember(
  groupId: string,
  userId: string,
  body: UpdateGroupMemberBody,
): Promise<GroupMember> {
  return apiPatch<GroupMember>(`/groups/${groupId}/members/${userId}`, body);
}

/** `DELETE /groups/{id}/members/{userId}`：204 */
export function removeGroupMember(groupId: string, userId: string): Promise<void> {
  return apiDelete<void>(`/groups/${groupId}/members/${userId}`);
}

/** `POST /groups/{id}/join-requests`：申请加入（无请求体） */
export function createJoinRequest(groupId: string): Promise<JoinRequest> {
  return apiPost<JoinRequest>(`/groups/${groupId}/join-requests`);
}

/** `POST /groups/{id}/join-requests/query` */
export function queryJoinRequests(
  groupId: string,
  body: JoinRequestQueryBody,
  signal?: AbortSignal,
): Promise<Page<JoinRequest>> {
  return apiPostQuery<JoinRequest>(`/groups/${groupId}/join-requests/query`, body, { signal });
}

/** `POST /groups/{id}/join-requests/{userId}/review` */
export function reviewJoinRequest(
  groupId: string,
  userId: string,
  body: ReviewJoinRequestBody,
): Promise<JoinRequest> {
  return apiPost<JoinRequest>(`/groups/${groupId}/join-requests/${userId}/review`, body);
}
