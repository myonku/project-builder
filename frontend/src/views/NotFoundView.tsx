/**
 * 路由级 404（未知路径）。
 *
 * **注意区分两种 404**（F01 §6 第 4 条）：
 * - 这里的 404 是"**这条路由不存在**"，属于前端问题；
 * - 接口返回的 `*_not_found` 是"对象不存在**或**你没有访问权限"，
 *   在业务页面里必须统一渲染成"不存在或不可见"，不能导向本页。
 */

import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Stack from '@mui/material/Stack';
import Typography from '@mui/material/Typography';
import { Link } from 'react-router';

export default function NotFoundView() {
  return (
    <Box
      sx={{
        minHeight: '100dvh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        p: 3,
      }}
    >
      <Stack spacing={2} sx={{ alignItems: 'center', textAlign: 'center' }}>
        <Typography variant="h4">页面不存在</Typography>
        <Typography variant="body2" color="text.secondary">
          你访问的路径没有对应的页面。
        </Typography>
        <Button component={Link} to="/" variant="contained">
          回到首页
        </Button>
      </Stack>
    </Box>
  );
}
