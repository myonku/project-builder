/**
 * 用户域接口（对应《A02》）。
 *
 * `GET /users/{id}` 的"不可见"与"不存在"共用 `user_not_found`（避免账号枚举）。
 */

import { apiGet, apiPatch, apiPost, apiPostQuery } from './client';
import type {
  DisableUserBody,
  ResetPasswordBody,
  UpdateUserBody,
  User,
  UserQueryBody,
} from '@/types/user';
import type { Page } from '@/types/common';
import type { RegisterBody } from '@/types/auth';

/** `GET /users/{id}` */
export function getUser(userId: string, signal?: AbortSignal): Promise<User> {
  return apiGet<User>(`/users/${userId}`, { signal });
}

/** `PATCH /users/{id}`：只提交用户实际修改的字段 + `expected_updated_at` */
export function updateUser(userId: string, body: UpdateUserBody): Promise<User> {
  return apiPatch<User>(`/users/${userId}`, body);
}

/** `POST /users/{id}/disable`：对已停用用户返回 409 */
export function disableUser(userId: string, body: DisableUserBody): Promise<User> {
  return apiPost<User>(`/users/${userId}/disable`, body);
}

/** `POST /users`：管理员代建账号，与自助注册共用实现；**不接受 `system_role`** */
export function createUser(body: RegisterBody): Promise<User> {
  return apiPost<User>('/users', body);
}

/** `POST /users/query`：`include_disabled` 必须显式传，默认隐藏停用账号 */
export function queryUsers(body: UserQueryBody, signal?: AbortSignal): Promise<Page<User>> {
  return apiPostQuery<User>('/users/query', body, { signal });
}

/** `POST /users/{id}/password`：字段名是 `new_password` */
export function resetPassword(userId: string, body: ResetPasswordBody): Promise<User> {
  return apiPost<User>(`/users/${userId}/password`, body);
}
