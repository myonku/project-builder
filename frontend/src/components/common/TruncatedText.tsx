import type { ReactElement } from 'react';
import Tooltip from '@mui/material/Tooltip';
import { dataTooltipSlotProps } from '@/components/common/DataTooltip';
import type { TooltipProps } from '@mui/material/Tooltip';
import Typography from '@mui/material/Typography';
import type { TypographyProps } from '@mui/material/Typography';

export interface TruncatedTextOptions {
  /** 超过该字符数才截断，默认 15 */
  threshold?: number;
  /** 截断后保留的字符数，默认 12 */
  keep?: number;
  /** 省略号，默认 '...'，也可传 '…' */
  ellipsis?: string;
  /** 透传给 Typography 的 props（variant / color / sx ...） */
  typographyProps?: TypographyProps;
  /** 透传给 Tooltip 的 props（placement / arrow ...） */
  tooltipProps?: Omit<TooltipProps, 'title' | 'children'>;
}

/**
 * 渲染长度受限的文本，超长时截断并在 hover 时通过 Tooltip 显示完整内容。
 */
export function truncatedText(
  text: string | null | undefined,
  options: TruncatedTextOptions = {},
): ReactElement {
  const {
    threshold = 15,
    keep = 12,
    ellipsis = '...',
    typographyProps,
    tooltipProps,
  } = options;

  const str = text ?? '';

  // 用 Array.from 按「码点」切分，避免把 emoji / 代理对字符切坏
  const chars = Array.from(str);
  const isTruncated = chars.length > threshold;
  const display = isTruncated ? chars.slice(0, keep).join('') + ellipsis : str;

  const content = (
    <Typography
      component="span"
      variant="body2"
      {...typographyProps}
    >
      {display}
    </Typography>
  );

  // 没被截断就不需要 Tooltip，避免无意义的浮层
  if (!isTruncated) return content;

  return (
    <Tooltip
      title={str}
      arrow={false}
      // 统一外观只定义一处（`components/common/DataTooltip.tsx`）：表格单元格与本函数共用同一份
      // 样式常量，因此"表格里的悬浮提示"与"抽屉/成员面板里的截断提示"看起来完全一致（F04 D33）。
      slotProps={dataTooltipSlotProps}
      {...tooltipProps}
    >
      {content}
    </Tooltip>
  );
}