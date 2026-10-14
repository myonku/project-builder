/**
 * 应用标识（本项目文件，替代模板自带的 `SitemarkIcon`）。
 *
 * 模板的 `SitemarkIcon` 是 MUI 官方文档站的商标图形，直接留在产物里等于给内部系统
 * 挂了一个别人的品牌。这里换成一个中性的"层级/项目树"标记：
 * - 不引入图片资源，纯 SVG，跟随主题 `primary` 色与文字色；
 * - 需要换成正式 Logo 时，只改这一个文件（或替换为 `<img src="/logo.svg">`，
 *   静态资源放 `public/`）。
 */

import SvgIcon from '@mui/material/SvgIcon';
import type { SvgIconProps } from '@mui/material/SvgIcon';

export default function AppLogo(props: SvgIconProps) {
  return (
    <SvgIcon
      viewBox="0 0 32 32"
      sx={{ fontSize: 30, color: 'primary.main' }}
      aria-hidden="true"
      {...props}
    >
      {/* 顶层节点 */}
      <rect x="11" y="2" width="10" height="7" rx="2" fill="currentColor" />
      {/* 连线 */}
      <path
        d="M16 9v5M6 20v-3h20v3"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        fill="none"
      />
      {/* 下层节点 */}
      <rect x="1" y="20" width="10" height="7" rx="2" fill="currentColor" opacity="0.75" />
      <rect x="21" y="20" width="10" height="7" rx="2" fill="currentColor" opacity="0.75" />
      {/* 里程碑菱形 */}
      <path d="M16 19.5l2.6 2.6L16 24.7l-2.6-2.6z" fill="currentColor" />
    </SvgIcon>
  );
}
