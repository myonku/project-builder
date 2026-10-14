/**
 * 应用入口。
 *
 * 顺序有意如此（与《F01》§3 目录结构一致）：
 * `AppTheme`（主题）→ `CssBaseline` →
 * `NotificationsProvider`（Snackbar）→ `DialogsProvider`（Promise 化对话框）→ 路由。
 *
 * `useApiError` 同时依赖 Notifications 与 Router，因此两者都必须包在
 * `RouterProvider` 的**外层**（Provider 提供上下文，Router 提供 navigate）。
 *
 * 主题采用 **MUI 默认的 Material Design 2 风格**，不再向 `AppTheme` 传组件覆盖
 * （见 `theme/AppTheme.tsx` 与 F04 D16）。
 */

import CssBaseline from '@mui/material/CssBaseline';
import { RouterProvider } from 'react-router';
import AppTheme from '@/theme/AppTheme';
import { router } from '@/router';
import { DialogsProvider } from '@/hooks/useDialogs';
import { NotificationsProvider } from '@/hooks/useNotifications';

export default function App() {
  return (
    <AppTheme>
      <CssBaseline enableColorScheme />
      <NotificationsProvider>
        <DialogsProvider>
          <RouterProvider router={router} />
        </DialogsProvider>
      </NotificationsProvider>
    </AppTheme>
  );
}
