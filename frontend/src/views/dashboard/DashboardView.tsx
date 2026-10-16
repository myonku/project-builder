/**
 * 概览页（`/`，F05 §7.2 第 5 步）。
 *
 * 数据全部来自 `GET /plan-views/personal`（《A06》§1）——**不新增任何接口**：
 * - `summary` 五张卡片直接复用 `components/plan/PlanSummaryTiles.tsx`（与计划视图同源，
 *   因此不可能出现"卡片说 3 个逾期、列表里有 4 行"这类自相矛盾）；
 * - "我的任务"取投影里的任务按**截止日期升序**取前若干条（只取未完成/未取消的），
 *   行点击仍走 `?taskId=` + `TaskDetailDrawer`（与项目详情、计划视图同一套，F01 §5.7）；
 * - "我的项目"取投影里的项目（服务端已按 `end_date` 排序）。
 *
 * 身份与运行信息（`GET /auth/me` 的字段、API 前缀）保留在页面底部：联调时一眼确认
 * "登录的是谁、打到哪个后端"仍然有用，但它不该占据首屏——所以降为次要区块。
 */

import * as React from 'react';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Card from '@mui/material/Card';
import CardContent from '@mui/material/CardContent';
import Chip from '@mui/material/Chip';
import Divider from '@mui/material/Divider';
import Grid from '@mui/material/Grid';
import LinearProgress from '@mui/material/LinearProgress';
import Stack from '@mui/material/Stack';
import Tooltip from '@mui/material/Tooltip';
import Typography from '@mui/material/Typography';
import ArrowForwardIcon from '@mui/icons-material/ArrowForward';
import { Link as RouterLink, useSearchParams } from 'react-router';
import { API_BASE } from '@/api/client';
import CellText from '@/components/common/CellText';
import PageContainer from '@/components/common/PageContainer';
import StatusTag from '@/components/common/StatusTag';
import PlanSummaryTiles from '@/components/plan/PlanSummaryTiles';
import { taskPriorityMeta, taskStatusMeta } from '@/components/task/taskMeta';
import TaskDetailDrawer from '@/components/task/TaskDetailDrawer';
import { describeApiError } from '@/hooks/useApiError';
import { usePlanView } from '@/hooks/usePlanView';
import type { PlanViewQuery } from '@/types/plan';
import { useAuthStore } from '@/stores/auth';
import { formatForDisplay, formatNaturalDate } from '@/utils/datetime';
import type { PlanTask } from '@/types/plan';

/** "我的任务"最多显示几条（概览是"扫一眼"，完整清单在计划视图里） */
const MY_TASK_LIMIT = 6;

/**
 * 概览不筛选：用**模块级常量**而不是内联的 `{}`。
 *
 * 虽然 `usePlanView` 现在按"查询参数的序列化值"比较依赖（不会再因为对象身份变化重复请求），
 * 但调用方传一个稳定引用仍然更清楚；万一以后有人把那个 hook 改回按引用比较，
 * 这里也不会退化成无限请求（2026-10-16 的教训：概览页曾因此 8 秒发出 404 次请求）。
 */
const DASHBOARD_QUERY: PlanViewQuery = {};

function SectionCard({
  title,
  action,
  children,
}: {
  title: string;
  action?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <Card variant="outlined" sx={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
      <CardContent sx={{ flex: 1 }}>
        <Stack direction="row" sx={{ alignItems: 'center', justifyContent: 'space-between', mb: 1 }}>
          <Typography variant="h6" component="h2">
            {title}
          </Typography>
          {action}
        </Stack>
        {children}
      </CardContent>
    </Card>
  );
}

function InfoRow({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <Stack direction="row" spacing={2} sx={{ justifyContent: 'space-between' }}>
      <Typography variant="body2" color="text.secondary">
        {label}
      </Typography>
      <Typography variant="body2" component="div">
        {value}
      </Typography>
    </Stack>
  );
}

/** 一条"我的任务"：名称 + 状态/优先级标签 + 截止日期 */
function MyTaskRow({ task, onClick }: { task: PlanTask; onClick: () => void }) {
  const statusMeta = taskStatusMeta(task.status);
  const priorityMeta = taskPriorityMeta(task.priority);

  return (
    <Stack
      direction="row"
      spacing={1.5}
      sx={{ alignItems: 'center', justifyContent: 'space-between', cursor: 'pointer', py: 0.5 }}
      onClick={onClick}
      role="button"
      tabIndex={0}
      onKeyDown={(event) => {
        if (event.key === 'Enter' || event.key === ' ') {
          event.preventDefault();
          onClick();
        }
      }}
    >
      <Box sx={{ minWidth: 0 }}>
        <Typography variant="body2" noWrap title={task.name}>
          {task.name}
        </Typography>
        <Typography variant="caption" color="text.secondary" noWrap>
          {formatNaturalDate(task.end_date)} 截止
          {task.owner ? ` · ${task.owner.display_name}` : ''}
        </Typography>
      </Box>
      <Stack direction="row" spacing={0.5} sx={{ flexShrink: 0, alignItems: 'center' }}>
        {task.is_overdue ? <StatusTag label="已逾期" tone="error" /> : null}
        {task.priority >= 2 ? (
          <StatusTag label={priorityMeta.label} tone={priorityMeta.tone} variant="outlined" />
        ) : null}
        <StatusTag label={statusMeta.label} tone={statusMeta.tone} />
      </Stack>
    </Stack>
  );
}

export default function DashboardView() {
  const user = useAuthStore((state) => state.user);
  const [searchParams, setSearchParams] = useSearchParams();

  // 概览不筛选：拿的就是"我个人计划的全部"，再在客户端挑出要展示的部分
  const { payload, loading, error, reload } = usePlanView({
    groupId: null,
    query: DASHBOARD_QUERY,
  });

  const taskId = searchParams.get('taskId');
  const openTask = React.useCallback(
    (nextTaskId: string) => {
      const next = new URLSearchParams(searchParams);
      next.set('taskId', nextTaskId);
      setSearchParams(next, { replace: false });
    },
    [searchParams, setSearchParams],
  );
  const closeTask = React.useCallback(() => {
    const next = new URLSearchParams(searchParams);
    next.delete('taskId');
    setSearchParams(next, { replace: false });
  }, [searchParams, setSearchParams]);

  /** 待办任务：未完成、未取消，按截止日期升序（逾期自然排在最前） */
  const myTasks = React.useMemo(() => {
    const tasks = payload?.tasks ?? [];
    return tasks
      .filter((task) => task.status !== 'completed' && task.status !== 'cancelled')
      .sort((a, b) => (a.end_date === b.end_date ? a.id.localeCompare(b.id) : a.end_date < b.end_date ? -1 : 1))
      .slice(0, MY_TASK_LIMIT);
  }, [payload]);

  const activeTask = React.useMemo(
    () => payload?.tasks.find((task) => task.id === taskId) ?? null,
    [payload, taskId],
  );

  return (
    <PageContainer
      title="概览"
      actions={
        <Button variant="outlined" onClick={reload} disabled={loading}>
          刷新
        </Button>
      }
    >
      <Stack spacing={3}>
        {error ? (
          <Alert
            severity={error.isNotFound ? 'info' : 'error'}
            action={
              <Button color="inherit" size="small" onClick={reload}>
                重试
              </Button>
            }
          >
            {describeApiError(error)}
          </Alert>
        ) : null}

        <PlanSummaryTiles summary={payload?.summary} />

        <Grid container spacing={2}>
          <Grid size={{ xs: 12, md: 7 }}>
            <SectionCard
              title={`待办任务${myTasks.length > 0 ? `（最近 ${myTasks.length} 条）` : ''}`}
              action={
                <Button
                  size="small"
                  endIcon={<ArrowForwardIcon />}
                  component={RouterLink}
                  to="/plans/personal"
                >
                  个人计划
                </Button>
              }
            >
              {myTasks.length === 0 ? (
                <Typography variant="body2" color="text.secondary">
                  {loading ? '正在加载…' : '没有待办任务：没有未完成的任务，或者你还没有参与任何项目。'}
                </Typography>
              ) : (
                <Stack divider={<Divider flexItem />}>
                  {myTasks.map((task) => (
                    <MyTaskRow key={task.id} task={task} onClick={() => openTask(task.id)} />
                  ))}
                </Stack>
              )}
            </SectionCard>
          </Grid>

          <Grid size={{ xs: 12, md: 5 }}>
            <SectionCard
              title="我的项目"
              action={
                <Button
                  size="small"
                  endIcon={<ArrowForwardIcon />}
                  component={RouterLink}
                  to="/projects"
                >
                  项目列表
                </Button>
              }
            >
              {(payload?.projects.length ?? 0) === 0 ? (
                <Typography variant="body2" color="text.secondary">
                  {loading ? '正在加载…' : '还没有参与任何项目。'}
                </Typography>
              ) : (
                <Stack spacing={1.5} divider={<Divider flexItem />}>
                  {(payload?.projects ?? []).slice(0, 5).map((project) => (
                    <Stack key={project.id} spacing={0.5}>
                      <Stack
                        direction="row"
                        spacing={1}
                        sx={{ alignItems: 'center', justifyContent: 'space-between' }}
                      >
                        <Typography
                          variant="body2"
                          component={RouterLink}
                          to={`/projects/${project.id}`}
                          sx={{
                            color: 'inherit',
                            textDecoration: 'none',
                            fontWeight: 500,
                            minWidth: 0,
                            '&:hover': { textDecoration: 'underline' },
                          }}
                          noWrap
                        >
                          {project.name}
                        </Typography>
                        <CellText muted>{project.progress}%</CellText>
                      </Stack>
                      <Tooltip title={`${project.task_count} 个任务，${project.overdue_task_count} 个逾期`}>
                        <Box>
                          <LinearProgress
                            variant="determinate"
                            value={project.progress}
                            color={project.overdue_task_count > 0 ? 'warning' : 'primary'}
                            sx={{ height: 6, borderRadius: 3 }}
                          />
                        </Box>
                      </Tooltip>
                      <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
                        <Typography variant="caption" color="text.secondary">
                          {formatNaturalDate(project.start_date)} – {formatNaturalDate(project.end_date)}
                        </Typography>
                        {project.overdue_task_count > 0 ? (
                          <StatusTag label={`逾期 ${project.overdue_task_count}`} tone="error" />
                        ) : null}
                        {project.is_archived ? (
                          <StatusTag label="已归档" tone="default" variant="outlined" />
                        ) : null}
                      </Stack>
                    </Stack>
                  ))}
                </Stack>
              )}
            </SectionCard>
          </Grid>
        </Grid>

        {/* 次要区块：身份与运行信息（联调用，不该占据首屏） */}
        <Grid container spacing={2}>
          <Grid size={{ xs: 12, md: 6 }}>
            <SectionCard title="当前登录用户">
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
                  <InfoRow label="时区" value={user.timezone} />
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
            </SectionCard>
          </Grid>
          <Grid size={{ xs: 12, md: 6 }}>
            <SectionCard title="运行信息">
              <Stack spacing={1} divider={<Divider flexItem />}>
                <InfoRow label="API 前缀" value={<code>{API_BASE}</code>} />
                <InfoRow label="开发代理" value="/api → 127.0.0.1:8000" />
                <InfoRow label="主题" value="MUI 默认 Material Design 2（亮/暗）" />
                <InfoRow
                  label="数据来源"
                  value={<code>GET /plan-views/personal</code>}
                />
              </Stack>
            </SectionCard>
          </Grid>
        </Grid>
      </Stack>

      <TaskDetailDrawer
        key={taskId ?? 'none'}
        taskId={taskId}
        canEdit={activeTask?.can_edit ?? false}
        onChanged={reload}
        onClose={closeTask}
      />
    </PageContainer>
  );
}
