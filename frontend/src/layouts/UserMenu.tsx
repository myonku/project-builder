/**
 * 头部右侧的用户菜单（本项目新增，非模板文件）。
 *
 * 模板的 Header 只有主题切换；本项目需要"当前用户 + 退出登录"：
 * 会话是 HttpOnly Cookie，前端无法自行失效，必须调 `POST /auth/logout`。
 */

import * as React from 'react';
import Divider from '@mui/material/Divider';
import IconButton from '@mui/material/IconButton';
import ListItemIcon from '@mui/material/ListItemIcon';
import Menu from '@mui/material/Menu';
import MenuItem from '@mui/material/MenuItem';
import Tooltip from '@mui/material/Tooltip';
import Typography from '@mui/material/Typography';
import LogoutIcon from '@mui/icons-material/Logout';
import PersonIcon from '@mui/icons-material/Person';
import { Stack } from '@mui/material';
import { useNavigate } from 'react-router';
import UserAvatar from '@/components/common/UserAvatar';
import { useAuthStore } from '@/stores/auth';
import { useApiError } from '@/hooks/useApiError';

export default function UserMenu() {
  const navigate = useNavigate();
  const user = useAuthStore((state) => state.user);
  const signOut = useAuthStore((state) => state.signOut);
  const { handle } = useApiError();
  const [anchorEl, setAnchorEl] = React.useState<HTMLElement | null>(null);
  const [signingOut, setSigningOut] = React.useState(false);

  const open = Boolean(anchorEl);

  const handleSignOut = React.useCallback(async () => {
    setSigningOut(true);
    try {
      await signOut();
      navigate('/login', { replace: true });
    } catch (error) {
      // 即便后端失败，store 也已回到未登录态；这里只提示原因
      handle(error, { message: '退出登录未能完成，请稍后重试。' });
      navigate('/login', { replace: true });
    } finally {
      setSigningOut(false);
      setAnchorEl(null);
    }
  }, [handle, navigate, signOut]);

  return (
    <React.Fragment>
      <Tooltip title="账号" enterDelay={1000}>
        <IconButton
          size="small"
          aria-label="打开账号菜单"
          onClick={(event) => setAnchorEl(event.currentTarget)}
        >
          <UserAvatar user={user} size={30} />
        </IconButton>
      </Tooltip>
      <Menu
        anchorEl={anchorEl}
        open={open}
        onClose={() => setAnchorEl(null)}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
        transformOrigin={{ vertical: 'top', horizontal: 'right' }}
      >
        <Stack sx={{ px: 2, py: 1, minWidth: 200 }}>
          <Typography variant="subtitle2">{user?.display_name ?? '未登录'}</Typography>
          <Typography variant="caption" color="text.secondary">
            {user?.email ?? ''}
          </Typography>
          {user?.system_role === 'admin' ? (
            <Typography variant="caption" color="primary">
              系统管理员
            </Typography>
          ) : null}
        </Stack>
        <Divider />
        <MenuItem
          onClick={() => {
            setAnchorEl(null);
            navigate('/me');
          }}
        >
          <ListItemIcon>
            <PersonIcon fontSize="small" />
          </ListItemIcon>
          个人信息
        </MenuItem>
        <MenuItem onClick={handleSignOut} disabled={signingOut}>
          <ListItemIcon>
            <LogoutIcon fontSize="small" />
          </ListItemIcon>
          退出登录
        </MenuItem>
      </Menu>
    </React.Fragment>
  );
}
