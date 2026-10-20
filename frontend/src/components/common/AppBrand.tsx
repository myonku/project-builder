/**
 * 公开页品牌区（登录 / 注册顶栏）。
 *
 * 用户确认的形态：**WILT 字标 + Worklane + 文字标题**；窄屏只保留字标，
 * 避免 64px 顶栏里挤下三行内容。应用内（Dashboard）使用 W 图标 + `Worklane`，
 * 见 `DashboardLayout` / `DashboardHeader`。
 */

import Box from '@mui/material/Box';
import Stack from '@mui/material/Stack';
import Typography from '@mui/material/Typography';
import { useTheme } from '@mui/material/styles';
import {
  BRAND_NAME,
  BRAND_TAGLINE,
  BRAND_WORDMARK,
  BRAND_WORDMARK_1X,
} from '@/config/brand';

export default function AppBrand() {
  const theme = useTheme();

  return (
    <Stack direction="row" spacing={1.25} sx={{ alignItems: 'center', minWidth: 0 }}>
      <Box
        component="img"
        src={BRAND_WORDMARK}
        srcSet={`${BRAND_WORDMARK_1X} 1x, ${BRAND_WORDMARK} 2x`}
        alt="WILT"
        draggable={false}
        sx={{
          height: { xs: 30, sm: 40 },
          width: 'auto',
          display: 'block',
          flexShrink: 0,
          // 深蓝端在暗色背景上对比偏低；轻微提亮而不是再维护一套位图
          [theme.getColorSchemeSelector('dark')]: { filter: 'brightness(1.35)' },
        }}
      />
      <Box sx={{ display: { xs: 'none', sm: 'block' }, minWidth: 0 }}>
        <Typography
          variant="subtitle1"
          component="div"
          sx={{ fontWeight: 700, lineHeight: 1.2, whiteSpace: 'nowrap' }}
        >
          {BRAND_NAME}
        </Typography>
        <Typography
          variant="caption"
          color="text.secondary"
          component="div"
          sx={{ lineHeight: 1.2, whiteSpace: 'nowrap' }}
        >
          {BRAND_TAGLINE}
        </Typography>
      </Box>
    </Stack>
  );
}
