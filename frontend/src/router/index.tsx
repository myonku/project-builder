/**
 * 路由表（F01 §3 / §3.1：官方 `CrudDashboard.tsx` 的入口组件拆成路由配置）。
 *
 * 用 `createBrowserRouter` 而不是模板的 `createHashRouter`：
 * 生产是同源部署 + `try_files $uri /index.html` 回落（F03 §8），
 * 浏览器地址栏里的路径就是真实路径，便于分享与排查。
 */

import { createBrowserRouter } from 'react-router';
import BlankLayout from '@/layouts/BlankLayout';
import DashboardLayout from '@/layouts/DashboardLayout';
import { RequireAdmin, RequireAuth } from '@/router/guards';
import LoginView from '@/views/auth/LoginView';
import DashboardView from '@/views/dashboard/DashboardView';
import ProjectDetailView from '@/views/projects/ProjectDetailView';
import ProjectListView from '@/views/projects/ProjectListView';
import GroupPlanView from '@/views/plan/GroupPlanView';
import PersonalPlanView from '@/views/plan/PersonalPlanView';
import GroupListView from '@/views/admin/GroupListView';
import UserListView from '@/views/admin/UserListView';
import AuditListView from '@/views/audit/AuditListView';
import ReminderListView from '@/views/reminders/ReminderListView';
import NotFoundView from '@/views/NotFoundView';

export const router = createBrowserRouter([
  {
    element: <BlankLayout />,
    children: [{ path: '/login', element: <LoginView /> }],
  },
  {
    element: <RequireAuth />,
    children: [
      {
        element: <DashboardLayout />,
        children: [
          { index: true, element: <DashboardView /> },
          { path: 'projects', element: <ProjectListView /> },
          { path: 'projects/:projectId', element: <ProjectDetailView /> },
          { path: 'plans/personal', element: <PersonalPlanView /> },
          { path: 'plans/group', element: <GroupPlanView /> },
          { path: 'reminders', element: <ReminderListView /> },
          { path: 'audit', element: <AuditListView /> },
          {
            element: <RequireAdmin />,
            children: [
              { path: 'admin/users', element: <UserListView /> },
              { path: 'admin/groups', element: <GroupListView /> },
            ],
          },
        ],
      },
    ],
  },
  { path: '*', element: <NotFoundView /> },
]);
