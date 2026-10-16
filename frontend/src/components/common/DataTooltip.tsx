/**
 * 数据展示类的悬浮提示（F04 D33 的"方案 3"）。
 *
 * **为什么要专门做一层**：MUI DataGrid 对"没有 `renderCell`"的列，会把格式化后的文本塞进单元格的
 * `title` 属性（源码 `components/cell/GridCell.mjs`：`if (children === undefined) { ...; title = valueString }`），
 * 那是**浏览器原生提示框**——外观由浏览器/系统决定，不受主题控制，也无法与 MUI `Tooltip` 统一。
 * 于是同一个应用里会出现"项目列表全是原生白底黑框、邮件列表一半灰底圆角"的割裂。
 *
 * **约定**（与 F01 §6 第 34 条一致）：
 * - **表格单元格里**的提示一律用本模块（`DataTooltip` / `textCell`），外观统一为"仿原生 title"：
 *   白底、细黑边、无圆角、小字号；
 * - **`TruncatedText`**（成员面板那种行内截断文本）用同一份样式常量；
 * - 顶栏 / 抽屉里的按钮提示等"系统 UI"保持 MUI 默认外观，不做覆盖（避免把表格风强加给全局）。
 *
 * **只有写了 `renderCell` 才能去掉原生 `title`**：DataGrid 没有关闭它的开关，这是唯一的杠杆。
 */

import * as React from 'react';
import Tooltip from '@mui/material/Tooltip';
import type { TooltipProps } from '@mui/material/Tooltip';
import type { GridRenderCellParams, GridValidRowModel } from '@mui/x-data-grid';
import CellText from '@/components/common/CellText';

/**
 * 统一外观：模仿浏览器原生 title / DataGrid 截断内容的观感。
 *
 * **有意固定为白底黑字**（不走主题的 `background.paper` / `text.primary`），
 * 这是原 `TruncatedText` 里的设计意图——亮暗两种主题下都保持同一种"表格风"。
 * 唯一的取舍是暗色模式下会有一块亮色浮层；要改成跟随主题，只需把下面两行换成
 * `backgroundColor: 'background.paper'` / `color: 'text.primary'`，表格与截断函数会一起变。
 */
export const dataTooltipSlotProps: NonNullable<TooltipProps['slotProps']> = {
  tooltip: {
    sx: {
      backgroundColor: '#fff',
      color: '#000',
      border: '2px solid #000',
      borderRadius: 0,
      boxShadow: 'none',
      fontSize: '0.75rem',
      padding: '2px 5px',
      maxWidth: 420,
    },
  },
};

export interface DataTooltipProps {
  /** 完整值；为空字符串时不渲染提示（MUI 也会对空 title 报警告） */
  title: string | null | undefined;
  children: React.ReactElement;
  placement?: TooltipProps['placement'];
}

/**
 * 数据展示用的悬浮提示：`title` 为空时直接返回子元素（不产生空提示框）。
 *
 * 子元素必须是**行内**元素（例如 `CellText`），否则会破坏单元格的垂直居中（F01 §6 第 22 条）。
 */
export default function DataTooltip({ title, children, placement = 'bottom' }: DataTooltipProps) {
  const text = (title ?? '').trim();
  if (text === '') {
    return children;
  }
  return (
    <Tooltip title={text} placement={placement} arrow={false} slotProps={dataTooltipSlotProps}>
      {children}
    </Tooltip>
  );
}

/** 空值与占位破折号不给提示（原生 title 会对 `—` 也弹一个框，是噪点） */
function isPlaceholder(text: string): boolean {
  return text === '' || text === '—' || text === '-';
}

export interface TextCellOptions {
  /** 是否给提示，默认 `true`；数字/百分比这类"一眼能看全"的列传 `false` */
  tooltip?: boolean;
}

/**
 * 文本类列的 `renderCell` 工厂：渲染行内文本（`CellText`），并按需包一层统一提示。
 *
 * 用法（**不要再写 `title` 属性，也不要再套 `truncatedText`**）：
 * ```tsx
 * { field: 'end_date', headerName: '结束日期', width: 110,
 *   valueFormatter: (value) => formatNaturalDate(value as string | null),
 *   renderCell: textCell<Project>() }
 * ```
 * `params.formattedValue` 已经过 `valueFormatter`，所以格式化逻辑仍然只写一份。
 */
export function textCell<T extends GridValidRowModel>(options: TextCellOptions = {}) {
  const { tooltip = true } = options;
  return function renderTextCell(params: GridRenderCellParams<T>): React.ReactElement {
    const text = params.formattedValue == null ? '' : String(params.formattedValue);
    const content = <CellText>{text === '' ? '—' : text}</CellText>;
    if (!tooltip || isPlaceholder(text)) {
      return content;
    }
    return <DataTooltip title={text}>{content}</DataTooltip>;
  };
}
