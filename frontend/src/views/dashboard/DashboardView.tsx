/**
 * 概览页（`/`）。
 *
 * 骨架期的作用是**证明链路通了**：当前用户的字段来自 `GET /auth/me`（会话 Cookie + 信封解包），
 * 并展示 API 前缀、时区与系统角色，便于联调时一眼确认"登录的是谁、打到哪个后端"。
 * 真正的统计卡片会在功能阶段接 `GET /plan-views/personal` 的 `summary`。
 */

import type { ReactNode } from 'react';
import Box from '@mui/material/Box';
import Card from '@mui/material/Card';
import CardContent from '@mui/material/CardContent';
import Chip from '@mui/material/Chip';
import Divider from '@mui/material/Divider';
import Grid from '@mui/material/Grid';
import Stack from '@mui/material/Stack';
import Typography from '@mui/material/Typography';
import PageContainer from '@/components/common/PageContainer';
import PlaceholderContent from '@/components/common/PlaceholderContent';
import { API_BASE } from '@/api/client';
import { useAuthStore } from '@/stores/auth';
import { formatForDisplay } from '@/utils/datetime';

function InfoRow({ label, value }: { label: string; value: ReactNode }) {
  return (
    <Stack direction="row" spacing={2} sx={{ justifyContent: 'space-between' }}>
      <Typography variant="body2" color="text.secondary">
        {label}
      </Typography>
      <Typography variant="body2" sx={{ fontFamily: 'inherit' }}>
        {value}
      </Typography>
    </Stack>
  );
}

export default function DashboardView() {
  const user = useAuthStore((state) => state.user);

  return (
    <PageContainer title="概览">
      <Grid container spacing={2}>
        <Grid size={{ xs: 12, md: 6 }}>
          <Card variant="outlined">
            <CardContent>
              <Typography variant="h6" gutterBottom>
                当前登录用户
              </Typography>
              {user ? (
                <Stack spacing={1} divider={<Divider flexItem />}>
                  <InfoRow label="显示名" value={user.display_name} />
                  <InfoRow label="用户名" value={user.username} />
                  <InfoRow label="邮箱" value={user.email} />
                  <InfoRow
                    label="系统角色"
                    value={
                      <Chip
                        size="small"
                        color={user.system_role === 'admin' ? 'primary' : 'default'}
                        label={user.system_role === 'admin' ? '系统管理员' : '普通成员'}
                      />
                    }
                  />
                  <InfoRow label="状态" value={user.status === 'active' ? '正常' : '已停用'} />
                  <InfoRow label="时区" value={user.timezone} />
                  <InfoRow label="邮件提醒" value={user.reminder_enabled ? '开启' : '关闭'} />
                  <InfoRow
                    label="上次登录"
                    value={formatForDisplay(user.last_login_at, user.timezone)}
                  />
                </Stack>
              ) : (
                <Typography variant="body2" color="text.secondary">
                  未获取到用户信息。
                </Typography>
              )}
            </CardContent>
          </Card>
        </Grid>
        <Grid size={{ xs: 12, md: 6 }}>
          <Card variant="outlined">
            <CardContent>
              <Typography variant="h6" gutterBottom>
                运行信息
              </Typography>
              <Stack spacing={1} divider={<Divider flexItem />}>
                <InfoRow label="API 前缀" value={<code>{API_BASE}</code>} />
                <InfoRow label="开发代理" value="/api → 127.0.0.1:8000" />
                <InfoRow label="主题" value="MUI 共享主题层（亮/暗）" />
              </Stack>
              <Box sx={{ mt: 2 }}>
                <Typography variant="caption" color="text.secondary">
                  骨架期只接通了认证链路；下列页面为占位实现。
                </Typography>
              </Box>
            </CardContent>
          </Card>
        </Grid>
      </Grid>
      <Box sx={{ mt: 3 }}>
        <PlaceholderContent
          description="概览页的统计卡片（项目数 / 任务数 / 进行中 / 逾期 / 总体进度）将在功能阶段接个人计划端点。"
          endpoints={['GET /plan-views/personal → summary']}
        />
      </Box>
    </PageContainer>
  );
}
