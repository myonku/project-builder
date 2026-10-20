/**
 * Worklane 主题图标（W 标记）。
 *
 * 资产来自 `src/assets/brand/`：2x 为主、1x 作为低 DPR 回退。图标在顶栏与
 * `Worklane` 文字并排时是装饰性的，`alt=""` + `aria-hidden` 避免读屏重复；
 * 链接本身的 `aria-label` 由 `DashboardHeader` 提供。
 */

import Box from '@mui/material/Box';
import { BRAND_MARK, BRAND_MARK_1X } from '@/config/brand';

export interface AppLogoProps {
  /** 显示高度（px）；默认 30，顶栏容器高 40 */
  height?: number;
}

export default function AppLogo({ height = 30 }: AppLogoProps) {
  return (
    <Box
      component="img"
      src={BRAND_MARK}
      srcSet={`${BRAND_MARK_1X} 1x, ${BRAND_MARK} 2x`}
      alt=""
      aria-hidden="true"
      draggable={false}
      sx={{ height, width: 'auto', display: 'block' }}
    />
  );
}
