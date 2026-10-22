/**
 * 认证域接口（对应《A01》）。
 *
 * 登录成功后令牌只在 `Set-Cookie` 里，前端拿不到也不需要拿；
 * 注册**不自动登录**（《S02》§5 加强项），需要再调一次 `login()`。
 */

import { apiGet, apiPost } from './client';
import type { LoginBody, RegisterBody } from '@/types/auth';
import type { RegisterResponse } from '@/types/registration';
import type { User } from '@/types/user';

/** `POST /auth/login`：成功返回当前用户，会话写入 HttpOnly Cookie */
export function login(body: LoginBody): Promise<User> {
  return apiPost<User>('/auth/login', body);
}

/**
 * `POST /auth/register`：生产配置下只创建待审核申请，不创建用户、不自动登录。
 *
 * 返回 `RegistrationRequest`；仅测试配置关闭审核时返回 `User`。
 */
export function register(body: RegisterBody): Promise<RegisterResponse> {
  return apiPost<RegisterResponse>('/auth/register', body);
}

/** `POST /auth/logout`：204，返回 `undefined` */
export function logout(): Promise<void> {
  return apiPost<void>('/auth/logout');
}

/** `GET /auth/me`：401 代表未登录（`authentication_required`） */
export function me(signal?: AbortSignal): Promise<User> {
  return apiGet<User>('/auth/me', { signal });
}

export function requestPasswordReset(body: { email: string }): Promise<{ status: 'accepted' }> {
  return apiPost('/auth/password-reset/request', body);
}
export function confirmPasswordReset(body: { token: string; new_password: string }): Promise<void> {
  return apiPost('/auth/password-reset/confirm', body);
}
