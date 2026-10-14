/**
 * 登录页等"无侧边栏"页面使用的空白布局。
 */

import Box from '@mui/material/Box';
import { Outlet } from 'react-router';

export default function BlankLayout() {
  return (
    <Box sx={{ minHeight: '100%', display: 'flex', flexDirection: 'column' }}>
      <Outlet />
    </Box>
  );
}
