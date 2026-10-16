import type { ReactElement } from 'react';
import Tooltip from '@mui/material/Tooltip';
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
    <Tooltip title={str} arrow={false}
      slotProps={{
        tooltip: {
          sx: {
            // 此处有意未设定成随主题亮暗色变化的样式，而是固定为白底黑字，
            // 意为更贴近 MUI 表格 DataGrid 中截断内容的默认样式，保持风格统一。
            backgroundColor: '#fff',        // 白色背景
            color: '#000',                  // 黑色文字
            border: '2px solid #000',       // 黑色边框
            borderRadius: 0,                // 无圆角
            boxShadow: 'none',              // 移除默认阴影
            fontSize: '0.75rem',            // 可选：贴近 DataGrid 的小字号
            padding: '2px 5px',             // 可选：紧凑内边距
          },
        },
      }}
      {...tooltipProps}
    >
      {content}
    </Tooltip>
  );
}