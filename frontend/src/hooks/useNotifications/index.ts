/**
 * 模板 `hooks/useNotifications/` 目录的转发入口（保留目录形态的理由同 `useDialogs`）。
 *
 * 注意：模板只提供 `show(message, options)` 与 `close(key)`，
 * **没有** `showError` / `showSuccess` 这类语义化方法——错误提示统一走
 * `useApiError()`（F01 §5.2）。
 */

export { default as NotificationsProvider } from './NotificationsProvider';
export { default as useNotifications } from './useNotifications';
export type { CloseNotification, ShowNotification, ShowNotificationOptions } from './useNotifications';
