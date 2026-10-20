/**
 * 登录 / 注册页布局：**与 Dashboard 同款的顶栏 + 内容区**。
 *
 * 顶栏只放 W 主题图标（不放 Worklane 文字），右侧是明暗切换；品牌文字/字标
 * 下沉到卡片顶部的 `AppBrand` 装饰区。这样两个公开页的顶栏与应用内顶栏
 * 使用同一套高度、边框、z-index 与图标尺寸，视觉上不会“换了一个应用”。
 */

import AppBar from '@mui/material/AppBar';
import Box from '@mui/material/Box';
import Toolbar from '@mui/material/Toolbar';
import { Link, Outlet } from 'react-router';
import AppLogo from '@/components/common/AppLogo';
import ThemeSwitcher from '@/layouts/ThemeSwitcher';

export default function BlankLayout() {
  return (
    <Box
      sx={{
        minHeight: '100dvh',
        display: 'flex',
        flexDirection: 'column',
        bgcolor: 'background.default',
      }}
    >
      <AppBar
        position="fixed"
        color="inherit"
        elevation={0}
        sx={{
          borderBottom: 1,
          borderColor: 'divider',
          bgcolor: 'background.paper',
          // 与 Dashboard 的顶栏保持一致：`zIndex.drawer + 1`
          zIndex: (theme) => theme.zIndex.drawer + 1,
        }}
      >
        <Toolbar sx={{ minHeight: 64 }}>
          <Link to="/login" style={{ textDecoration: 'none' }} aria-label="Worklane 登录页">
            <AppLogo height={26} />
          </Link>
          <Box sx={{ ml: 'auto' }}>
            <ThemeSwitcher />
          </Box>
        </Toolbar>
      </AppBar>
      <Box
        component="main"
        sx={{
          flex: 1,
          display: 'flex',
          flexDirection: 'column',
          // 让开固定顶栏的 64px
          pt: 8,
          px: { xs: 2, sm: 3 },
          pb: { xs: 4, sm: 6 },
        }}
      >
        <Outlet />
      </Box>
    </Box>
  );
}
