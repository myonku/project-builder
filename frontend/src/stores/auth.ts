/**
 * 认证态（**窄状态**：只有当前用户，见 F01 §4）。
 *
 * 业务数据（项目/任务/计划视图）**不进 store**——它们按 id 与筛选条件取，
 * 放进全局 store 会立刻出现缓存新鲜度问题，进而让 `expected_updated_at` 提交必然 409。
 *
 * 会话本身在 HttpOnly Cookie 里，前端状态只是 `GET /auth/me` 的一份缓存。
 */

import { create } from 'zustand';
import * as authApi from '@/api/auth';
import { toApiError, type ApiError } from '@/api/client';
import type { LoginBody } from '@/types/auth';
import type { User } from '@/types/user';

export type AuthStatus =
  /** 尚未探测过（应用刚启动） */
  | 'unknown'
  /** 正在请求 `GET /auth/me` */
  | 'loading'
  | 'authenticated'
  | 'anonymous';

export interface AuthState {
  user: User | null;
  status: AuthStatus;
  /** 最近一次 `loadMe` / `signIn` 的错误，供登录页展示 */
  error: ApiError | null;
  /** 探测当前会话；并发调用会合并成一次请求 */
  loadMe: (options?: { force?: boolean }) => Promise<User | null>;
  signIn: (body: LoginBody) => Promise<User>;
  signOut: () => Promise<void>;
  /** 把本地认证态清空（401 处理用，不调后端） */
  clear: () => void;
  /** 局部更新当前用户缓存（改自己资料后用） */
  setUser: (user: User) => void;
}

/** 并发去重：同一时刻只允许一次 `GET /auth/me` */
let inflight: Promise<User | null> | null = null;

export const useAuthStore = create<AuthState>()((set, get) => ({
  user: null,
  status: 'unknown',
  error: null,

  loadMe: async (options) => {
    const force = options?.force ?? false;
    const { status } = get();
    if (!force && status === 'authenticated') {
      return get().user;
    }
    if (inflight) {
      return inflight;
    }
    set({ status: 'loading', error: null });
    inflight = (async () => {
      try {
        const user = await authApi.me();
        set({ user, status: 'authenticated', error: null });
        return user;
      } catch (error) {
        const apiError = toApiError(error);
        if (apiError.isUnauthorized) {
          // 未登录是正常分支：不当成错误展示
          set({ user: null, status: 'anonymous', error: null });
        } else {
          set({ user: null, status: 'anonymous', error: apiError });
        }
        return null;
      } finally {
        inflight = null;
      }
    })();
    return inflight;
  },

  signIn: async (body) => {
    set({ status: 'loading', error: null });
    try {
      const user = await authApi.login(body);
      set({ user, status: 'authenticated', error: null });
      return user;
    } catch (error) {
      const apiError = toApiError(error);
      set({ user: null, status: 'anonymous', error: apiError });
      throw apiError;
    }
  },

  signOut: async () => {
    try {
      await authApi.logout();
    } finally {
      // 即使后端返回错误，本地也必须回到未登录态，否则用户会卡在"看起来已登录"
      set({ user: null, status: 'anonymous', error: null });
    }
  },

  clear: () => {
    set({ user: null, status: 'anonymous', error: null });
  },

  setUser: (user) => {
    set({ user, status: 'authenticated' });
  },
}));
