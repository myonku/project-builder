/**
 * 路由表（F01 §3 / §3.1：官方 `CrudDashboard.tsx` 的入口组件拆成路由配置）。
 *
 * 用 `createBrowserRouter` 而不是模板的 `createHashRouter`：
 * 生产是同源部署 + `try_files $uri /index.html` 回落（F03 §8），
 * 浏览器地址栏里的路径就是真实路径，便于分享与排查。
 *
 * ## 按路由拆包
 *
 * 项目列表引入了 `@mui/x-data-grid`（含中文语言包），整包约 500 kB；
 * 登录页一秒都用不到它。因此**只有登录页与 404 是静态导入，其余视图全部懒加载**：
 * 让首屏只下载外壳 + 登录页本身，其余按访问路由再取。
 *
 * 连概览页（`index`）也懒加载：它引入的 `utils/datetime`（dayjs）实测约 91 kB（gzip），
 * 而登录页根本用不到日期处理（F04 D23）。
 *
 * `page()` 把 `React.lazy` 与 `Suspense` 包成一个元素工厂，避免每条路由都写一遍；
 * 加载期间的占位用 `FullPageLoading`，与 `RequireAuth` 探测会话时的观感一致。
 */

import * as React from 'react';
import { createBrowserRouter } from 'react-router';
import FullPageLoading from '@/components/common/FullPageLoading';
import BlankLayout from '@/layouts/BlankLayout';
import DashboardLayout from '@/layouts/DashboardLayout';
import { RequireAdmin, RequireAuth } from '@/router/guards';
import LoginView from '@/views/auth/LoginView';
import RegisterView from '@/views/auth/RegisterView';
import NotFoundView from '@/views/NotFoundView';

/** 懒加载一个页面视图，并给出统一的加载占位 */
function page(loader: () => Promise<{ default: React.ComponentType }>): React.ReactElement {
  const Component = React.lazy(loader);
  return (
    <React.Suspense fallback={<FullPageLoading />}>
      <Component />
    </React.Suspense>
  );
}

export const router = createBrowserRouter([
  {
    element: <BlankLayout />,
    children: [
      { path: '/login', element: <LoginView /> },
      { path: '/register', element: <RegisterView /> },
      { path: '/forgot-password', element: page(() => import('@/views/auth/PasswordResetView')) },
      { path: '/reset-password', element: page(() => import('@/views/auth/ResetPasswordView')) },
    ],
  },
  {
    element: <RequireAuth />,
    children: [
      { path: 'attachments/:attachmentId/preview', element: page(() => import('@/views/attachments/AttachmentPreviewView')) },
      {
        element: <DashboardLayout />,
        children: [
          {
            index: true,
            element: page(() => import('@/views/dashboard/DashboardView')),
          },
          {
            // 个人信息不在侧栏；入口在右上角用户菜单与概览页（P01 §4）
            path: 'me',
            element: page(() => import('@/views/me/ProfileView')),
          },
          {
            path: 'groups',
            element: page(() => import('@/views/groups/GroupJoinView')),
          },
          {
            path: 'attachments',
            element: page(() => import('@/views/attachments/AttachmentListView')),
          },
          {
            path: 'projects',
            element: page(() => import('@/views/projects/ProjectListView')),
          },
          {
            path: 'projects/:projectId',
            element: page(() => import('@/views/projects/ProjectDetailView')),
          },
          {
            path: 'plans/personal',
            element: page(() => import('@/views/plan/PersonalPlanView')),
          },
          {
            path: 'plans/group',
            element: page(() => import('@/views/plan/GroupPlanView')),
          },
          {
            path: 'gantt',
            element: page(() => import('@/views/gantt/GanttView')),
          },
          {
            path: 'reminders',
            element: page(() => import('@/views/reminders/ReminderListView')),
          },
          {
            // 操作记录仅系统管理员可查看（《A09》§权限语义）：非管理员直接回首页，
            // 不渲染一个必然 403 的页面（F01 §6 第 5 条的 403/404 区分）。
            element: <RequireAdmin />,
            children: [
              {
                path: 'audit',
                element: page(() => import('@/views/audit/AuditListView')),
              },
              {
                path: 'admin/users',
                element: page(() => import('@/views/admin/UserListView')),
              },
              {
                path: 'admin/registration-requests',
                element: page(() => import('@/views/admin/RegistrationRequestListView')),
              },
              {
                path: 'admin/groups',
                element: page(() => import('@/views/admin/GroupListView')),
              },
            ],
          },
        ],
      },
    ],
  },
  { path: '*', element: <NotFoundView /> },
]);
