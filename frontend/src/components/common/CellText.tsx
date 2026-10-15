/**
 * 表格单元格里的文本（F01 §6 第 22 条）。
 *
 * **为什么必须用行内元素**：MUI DataGrid 的单元格不是 flex 容器，而是用
 * `line-height: <行高>` 把**行内**内容垂直居中。`Typography` 默认渲染成 `<p>`（块级），
 * 块级子元素不吃父级的 `line-height`，于是会顶到单元格顶部——
 * 实测"逾期任务"列里的 `0` 比同行其它内容**高 13.6px**，而 `Chip`（`inline-flex`）正常。
 *
 * 所以单元格里的纯文本一律用本组件（`component="span"`），不要直接用 `<Typography>`。
 * 需要更复杂的布局时，用 `sx={{ display: 'flex', alignItems: 'center', height: '100%' }}` 自己居中。
 */

import * as React from 'react';
import Typography from '@mui/material/Typography';

export interface CellTextProps {
  children: React.ReactNode;
  /** 次要信息（灰字），例如"0 条逾期"、"我的角色为空" */
  muted?: boolean;
  /** 强调（中等字重），例如金额、进度 */
  strong?: boolean;
}

export default function CellText({ children, muted = false, strong = false }: CellTextProps) {
  return (
    <Typography
      component="span"
      variant="body2"
      color={muted ? 'text.secondary' : undefined}
      sx={strong ? { fontWeight: 500 } : undefined}
    >
      {children}
    </Typography>
  );
}
