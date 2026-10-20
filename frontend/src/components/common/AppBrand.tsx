/**
 * 登录 / 注册卡片顶部的品牌装饰。
 *
 * 顶栏只留 W 主题图标，这里承担公开页的完整品牌信息：
 * - 大一点的 W 主题图标；
 * - `Work Items & Layered Timelines` 文字；
 * - 暗色下轻微提亮字标。
 */

import Stack from '@mui/material/Stack';
import Typography from '@mui/material/Typography';
import AppLogo from '@/components/common/AppLogo';
import {
  BRAND_NAME,
  BRAND_TAGLINE,
} from '@/config/brand';
import { useTheme } from '@mui/material/styles';

export default function AppBrand() {
  const theme = useTheme();
  return (
    <Stack
      spacing={1.25}
      sx={{
        alignItems: 'center',
        justifyContent: 'center',
        textAlign: 'center',
        width: '100%',
      }}
    >
      <Stack
        direction="row"
        spacing={1}
        sx={{
          alignItems: 'center',
          justifyContent: 'center',
          textAlign: 'center',
        }}
      >
        <AppLogo height={36} />
        <Typography
          component="h1" variant="h4"
          sx={{
            color: (theme.vars ?? theme).palette.primary.main,
            fontWeight: 500,
            fontSize: 36,
            ml: 0.75,
            whiteSpace: 'nowrap',
            lineHeight: 1,
          }}
        >
          {BRAND_NAME}
        </Typography>
      </Stack>
      <Typography
        variant="body2"
        color="text.secondary"
        sx={{ fontSize: 14, lineHeight: 1.4 }}
      >
        {BRAND_TAGLINE}
      </Typography>
    </Stack>
  );
}
