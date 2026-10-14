/**
 * 路由守卫（F01 §3）。
 *
 * - `RequireAuth`：未登录 → `/login?redirect=<原路径>`；**先探测会话**（`GET /auth/me`）再决定，
 *   避免"刷新页面就闪一下登录页"；
 * - `RequireAdmin`：只有 `system_role === 'admin'` 能进；普通成员访问管理路由时
 *   回首页而不是渲染 404（**403 与 404 是两回事**，F01 §6 第 5 条）。
 */

import * as React from 'react';
import { Navigate, Outlet, useLocation } from 'react-router';
import FullPageLoading from '@/components/common/FullPageLoading';
import { useAuthStore } from '@/stores/auth';

export function RequireAuth() {
  const status = useAuthStore((state) => state.status);
  const loadMe = useAuthStore((state) => state.loadMe);
  const location = useLocation();

  React.useEffect(() => {
    if (status === 'unknown') {
      void loadMe();
    }
  }, [status, loadMe]);

  if (status === 'unknown' || status === 'loading') {
    return <FullPageLoading message="正在检查登录状态…" />;
  }

  if (status === 'anonymous') {
    const redirect = `${location.pathname}${location.search}`;
    return <Navigate to={`/login?redirect=${encodeURIComponent(redirect)}`} replace />;
  }

  return <Outlet />;
}

export function RequireAdmin() {
  const user = useAuthStore((state) => state.user);

  if (user?.system_role !== 'admin') {
    // 已登录但无权限：回首页，而不是伪装成"页面不存在"
    return <Navigate to="/" replace />;
  }

  return <Outlet />;
}
