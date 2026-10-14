/**
 * 品牌区（Logo + 标题），登录页与侧边栏头部共用，
 * 保证"换 Logo"只发生在一处。
 */

import Stack from '@mui/material/Stack';
import Typography from '@mui/material/Typography';
import AppLogo from '@/components/common/AppLogo';

export interface AppBrandProps {
  /** 是否显示文字标题 */
  showTitle?: boolean;
  title?: string;
}

export default function AppBrand({ showTitle = true, title }: AppBrandProps) {
  const appTitle = title ?? import.meta.env.VITE_APP_TITLE ?? '项目/计划管理系统';

  return (
    <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
      <AppLogo />
      {showTitle ? (
        <Typography variant="h6" component="span" sx={{ fontWeight: 700, whiteSpace: 'nowrap' }}>
          {appTitle}
        </Typography>
      ) : null}
    </Stack>
  );
}
