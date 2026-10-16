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
 *
 * **必须 `forwardRef`**：`<Tooltip>`（含 `DataTooltip`）会给子元素挂 `ref` 来绑定 hover/focus。
 * 函数组件不转发 ref 时，React 只是警告并丢掉那个 ref——工具提示**静默失效**
 * （DOM 里看不出 `data-mui-internal-clone-element`，也不报错）。这是 2026-10-16 实测踩到的坑（F01 §6 第 35 条）。
 */

import * as React from 'react';
import Typography from '@mui/material/Typography';
import type { SxProps, Theme } from '@mui/material/styles';

export type CellTextProps = {
  children: React.ReactNode;
  /** 次要信息（灰字），例如"0 条逾期"、"我的角色为空" */
  muted?: boolean;
  /** 强调（中等字重），例如金额、进度 */
  strong?: boolean;
  /** 追加样式（宽度、对齐等）；不会覆盖行内元素这条底线 */
  sx?: SxProps<Theme>;
} & Omit<
  React.ComponentProps<typeof Typography>,
  'children' | 'component' | 'variant' | 'color' | 'sx' | 'ref'
>;

/**
 * `...rest` 必须摊到**同一个**接了 `ref` 的元素上：`<Tooltip>` 会把
 * `onMouseOver` / `onFocus` / `aria-label` 等 props 注入子元素，只转 ref 不转 props 时
 * MUI 会报 "The `children` component of the Tooltip is not forwarding its props correctly"，
 * 提示**静默失效**（看不到 DOM 标记、悬停无反应）。
 */
const CellText = React.forwardRef<HTMLSpanElement, CellTextProps>(function CellText(
  { children, muted = false, strong = false, sx, ...rest },
  ref,
) {
  return (
    <Typography
      {...rest}
      ref={ref}
      component="span"
      variant="body2"
      color={muted ? 'text.secondary' : undefined}
      sx={[strong ? { fontWeight: 500 } : {}, ...(Array.isArray(sx) ? sx : [sx])]}
    >
      {children}
    </Typography>
  );
});

export default CellText;
