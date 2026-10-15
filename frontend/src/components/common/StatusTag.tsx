/**
 * 通用状态标签（F01 §3 的 `components/common/StatusTag`）。
 *
 * 各域的"状态 → 文案/颜色"映射写在各自域的 `*Meta.ts` 里（例如
 * `components/project/projectMeta.ts`），本组件只负责渲染，
 * 避免每个页面各写一套 `<Chip>` 拼装。
 */

import Chip from '@mui/material/Chip';

/** 与 MUI `Chip` 的 `color` 取值一致，另加 `default`（灰） */
export type StatusTone =
  | 'default'
  | 'primary'
  | 'secondary'
  | 'success'
  | 'warning'
  | 'error'
  | 'info';

export interface StatusTagProps {
  label: string;
  tone?: StatusTone;
  size?: 'small' | 'medium';
  /** `outlined` 用于弱化的次要标记（例如"归档"） */
  variant?: 'filled' | 'outlined';
}

export default function StatusTag({
  label,
  tone = 'default',
  size = 'small',
  variant = 'filled',
}: StatusTagProps) {
  return <Chip label={label} color={tone} size={size} variant={variant} />;
}
