/**
 * 模板 `hooks/useDialogs/` 目录的转发入口。
 *
 * 说明（本项目对 F01 §3.1 的调整）：官方模板把这一组拆成
 * `DialogsContext` / `DialogsProvider` / `useDialogs` 三个文件，
 * 合并成单文件会偏离上游、妨碍将来按模板 diff 升级，因此保留目录形态，
 * 用本文件提供一个稳定的导入路径 `@/hooks/useDialogs`。
 */

export { default as DialogsProvider } from './DialogsProvider';
export { useDialogs, AlertDialog, ConfirmDialog, PromptDialog } from './useDialogs';
export type {
  AlertOptions,
  CloseDialog,
  ConfirmOptions,
  DialogComponent,
  DialogHook,
  DialogProps,
  OpenDialog,
  OpenDialogOptions,
  PromptOptions,
} from './useDialogs';
