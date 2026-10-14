/**
 * 全屏加载占位（会话探测、路由级数据加载时使用）。
 */

import Box from '@mui/material/Box';
import CircularProgress from '@mui/material/CircularProgress';
import Stack from '@mui/material/Stack';
import Typography from '@mui/material/Typography';

export interface FullPageLoadingProps {
  message?: string;
}

export default function FullPageLoading({ message = '正在加载…' }: FullPageLoadingProps) {
  return (
    <Box
      sx={{
        flex: 1,
        minHeight: '40vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
      }}
    >
      <Stack spacing={2} sx={{ alignItems: 'center' }}>
        <CircularProgress size={28} />
        <Typography variant="body2" color="text.secondary">
          {message}
        </Typography>
      </Stack>
    </Box>
  );
}
