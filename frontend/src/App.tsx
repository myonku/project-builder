/**
 * 应用入口。
 *
 * 顺序有意如此（与《F01》§3 目录结构一致）：
 * `AppTheme`（主题，含颜色方案）→ `CssBaseline` →
 * `NotificationsProvider`（Snackbar）→ `DialogsProvider`（Promise 化对话框）→ 路由。
 *
 * `useApiError` 同时依赖 Notifications 与 Router，因此两者都必须包在
 * `RouterProvider` 的**外层**（Provider 提供上下文，Router 提供 navigate）。
 */

import CssBaseline from '@mui/material/CssBaseline';
import type { ThemeOptions } from '@mui/material/styles';
import { RouterProvider } from 'react-router';
import AppTheme from '@/theme/AppTheme';
import {
  buttonCustomizations,
  dataGridCustomizations,
  datePickersCustomizations,
  formInputCustomizations,
  sidebarCustomizations,
} from '@/theme/customizations';
import { router } from '@/router';
import { DialogsProvider } from '@/hooks/useDialogs';
import { NotificationsProvider } from '@/hooks/useNotifications';

// 主题定制对象在模块级合并一次：`AppTheme` 内部按引用记忆化，
// 每次渲染新建对象会让主题重建（F01 §6 第 13 条）。
const themeComponents: ThemeOptions['components'] = {
  ...dataGridCustomizations,
  ...datePickersCustomizations,
  ...sidebarCustomizations,
  ...formInputCustomizations,
  ...buttonCustomizations,
};

export default function App() {
  return (
    <AppTheme themeComponents={themeComponents}>
      <CssBaseline enableColorScheme />
      <NotificationsProvider>
        <DialogsProvider>
          <RouterProvider router={router} />
        </DialogsProvider>
      </NotificationsProvider>
    </AppTheme>
  );
}
