/**
 * 登录页等"无侧边栏"页面使用的布局：**顶栏 + 内容区**。
 *
 * 顶栏放两样东西：左侧品牌（Logo + 标题），右侧**明暗切换**。
 * 登录页因此不再自己画顶栏，也不会在卡片上方重复堆叠品牌信息。
 *
 * 用 `position="fixed"` 而不是 `sticky`：内容区自己是滚动容器，
 * fixed 顶栏在移动端与桌面端表现一致（内容区用 `pt` 让开高度，见下）。
 */

import AppBar from '@mui/material/AppBar';
import Box from '@mui/material/Box';
import Toolbar from '@mui/material/Toolbar';
import { Outlet } from 'react-router';
import AppBrand from '@/components/common/AppBrand';
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
        color="default"
        elevation={0}
        sx={{
          borderBottom: 1,
          borderColor: 'divider',
          bgcolor: 'background.paper',
          // 与 Dashboard 的顶栏保持一致：`zIndex.drawer + 1`
          zIndex: (theme) => theme.zIndex.drawer + 1,
        }}
      >
        <Toolbar
          sx={{
            justifyContent: 'space-between',
            gap: 2,
            minHeight: { xs: 64, sm: 72 },
          }}
        >
          <AppBrand />
          <ThemeSwitcher />
        </Toolbar>
      </AppBar>
      <Box
        component="main"
        sx={{
          flex: 1,
          display: 'flex',
          flexDirection: 'column',
          // 让开固定顶栏的高度
          pt: { xs: 8, sm: 9 },
          px: { xs: 2, sm: 3 },
          pb: { xs: 4, sm: 6 },
        }}
      >
        <Outlet />
      </Box>
    </Box>
  );
}
