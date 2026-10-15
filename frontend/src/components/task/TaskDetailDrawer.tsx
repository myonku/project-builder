/**
 * 任务详情抽屉（`?taskId=` 承载）。
 *
 * 为什么用 Drawer 而不是路由页：任务单端点挂在 `/api/v1/tasks/{task_id}`（与项目无嵌套），
 * 且几乎总是从项目详情或计划视图点开——Drawer 关掉就回到原列表，筛选与滚动位置天然保留；
 * 项目详情、个人计划、组内计划**三处复用同一个组件**（《F01》§5.7）。
 *
 * 本组件**只读**：展示任务字段与成员。编辑任务（含 `parent_id` 三态与进度联动）与成员管理
 * 需要各自的表单与权限分支，放在功能期的后续步骤，这里不做半成品入口。
 *
 * 刷新语义：`taskId` 变化时由调用方用 `key={taskId}` 重新挂载本组件，
 * 因此 `useVersionedForm` 的 `autoLoad` 只跑一次就够——**不在这里做"用 effect 手动重载"**，
 * 那样会与 Hook 自己的加载 effect 打架。
 */

import * as React from 'react';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Chip from '@mui/material/Chip';
import Divider from '@mui/material/Divider';
import Drawer from '@mui/material/Drawer';
import IconButton from '@mui/material/IconButton';
import Stack from '@mui/material/Stack';
import Tooltip from '@mui/material/Tooltip';
import Typography from '@mui/material/Typography';
import CloseIcon from '@mui/icons-material/Close';
import FullPageLoading from '@/components/common/FullPageLoading';
import StatusTag from '@/components/common/StatusTag';
import { taskPriorityMeta, taskStatusMeta } from '@/components/task/taskMeta';
import { getTask, queryTaskMembers } from '@/api/tasks';
import { toApiError, type ApiError } from '@/api/client';
import { describeApiError } from '@/hooks/useApiError';
import { useVersionedForm } from '@/hooks/useVersionedForm';
import { useAuthStore } from '@/stores/auth';
import type { Task, TaskMember } from '@/types/task';
import { formatForDisplay, formatNaturalDate } from '@/utils/datetime';

export interface TaskDetailDrawerProps {
  /** 打开的任务 id；`null` 表示关闭 */
  taskId: string | null;
  onClose: () => void;
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <Stack direction="row" spacing={2} sx={{ justifyContent: 'space-between', alignItems: 'center' }}>
      <Typography variant="body2" color="text.secondary" sx={{ flexShrink: 0 }}>
        {label}
      </Typography>
      <Box sx={{ textAlign: 'right', minWidth: 0 }}>{children}</Box>
    </Stack>
  );
}

export default function TaskDetailDrawer({ taskId, onClose }: TaskDetailDrawerProps) {
  const userTimeZone = useAuthStore((state) => state.user?.timezone);

  const { entity: task, loading, error } = useVersionedForm<Task, { expected_updated_at: string }>({
    // 本组件只读，`save` 不会被调用；这里仍走同一个 Hook 是为了拿到一致的加载/错误语义，
    // 后续接入"编辑任务"时直接把它换成真正的 `updateTask(id, body)` 即可。
    load: (signal) => getTask(taskId as string, signal),
    save: () => Promise.reject(new Error('TaskDetailDrawer 目前是只读视图')),
    autoLoad: taskId !== null,
  });

  const [members, setMembers] = React.useState<TaskMember[]>([]);
  const [membersError, setMembersError] = React.useState<ApiError | null>(null);

  React.useEffect(() => {
    if (!taskId) {
      return undefined;
    }
    const controller = new AbortController();
    let cancelled = false;
    setMembers([]);
    setMembersError(null);
    void (async () => {
      try {
        const page = await queryTaskMembers(
          taskId,
          { page: 1, page_size: 100, sort: 'role' },
          controller.signal,
        );
        if (!cancelled) {
          setMembers(page.data ?? []);
        }
      } catch (caught) {
        if (cancelled) {
          return;
        }
        const apiError = toApiError(caught);
        if (apiError.code !== 'aborted') {
          setMembersError(apiError);
        }
      }
    })();
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [taskId]);

  const statusMeta = taskStatusMeta(task?.status);
  const priorityMeta = taskPriorityMeta(task?.priority);

  return (
    <Drawer
      anchor="right"
      open={taskId !== null}
      onClose={onClose}
      slotProps={{ paper: { sx: { width: { xs: '100%', sm: 480 } } } }}
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
        <Stack
          direction="row"
          sx={{ alignItems: 'center', justifyContent: 'space-between', px: 2, py: 1.5 }}
        >
          <Typography variant="h6" component="h2">
            任务详情
          </Typography>
          <Tooltip title="关闭">
            <IconButton onClick={onClose} aria-label="关闭任务详情">
              <CloseIcon />
            </IconButton>
          </Tooltip>
        </Stack>
        <Divider />

        <Box sx={{ flex: 1, overflow: 'auto', px: 2, py: 2 }}>
          {loading && !task ? <FullPageLoading message="正在加载任务…" /> : null}

          {error ? (
            <Alert severity={error.isNotFound ? 'info' : 'error'}>
              {describeApiError(error)}
            </Alert>
          ) : null}

          {task ? (
            <Stack spacing={2}>
              <Stack spacing={1}>
                <Typography variant="h6" component="h3">
                  {task.name}
                </Typography>
                <Stack direction="row" spacing={1} sx={{ flexWrap: 'wrap', gap: 1 }}>
                  <StatusTag label={statusMeta.label} tone={statusMeta.tone} />
                  <StatusTag
                    label={`优先级 ${priorityMeta.label}`}
                    tone={priorityMeta.tone}
                    variant="outlined"
                  />
                  {task.is_milestone ? (
                    <StatusTag label="里程碑" tone="info" variant="outlined" />
                  ) : null}
                  {task.is_shared ? (
                    <StatusTag label="已共享给组" tone="primary" variant="outlined" />
                  ) : null}
                  {task.is_overdue ? <StatusTag label="已逾期" tone="error" /> : null}
                  {task.deleted_at ? (
                    <StatusTag label="已删除" tone="default" variant="outlined" />
                  ) : null}
                </Stack>
              </Stack>

              {task.deleted_at ? (
                <Alert severity="warning">
                  该任务已被逻辑删除（{formatForDisplay(task.deleted_at, userTimeZone)}），
                  默认不出现在列表里；如需恢复请联系系统管理员。
                </Alert>
              ) : null}

              <Divider />
              <Field label="进度">
                <Typography variant="body2">{task.progress}%</Typography>
              </Field>
              <Field label="开始日期">
                <Typography variant="body2">{formatNaturalDate(task.start_date)}</Typography>
              </Field>
              <Field label="结束日期">
                <Typography variant="body2">{formatNaturalDate(task.end_date)}</Typography>
              </Field>
              <Field label="我的角色">
                {task.membership_role ? (
                  <StatusTag
                    label={task.membership_role === 'owner' ? '持有者' : '成员'}
                    tone={task.membership_role === 'owner' ? 'primary' : 'default'}
                    variant="outlined"
                  />
                ) : (
                  <Typography variant="body2" color="text.secondary">
                    非任务成员
                  </Typography>
                )}
              </Field>
              <Field label="上级任务">
                <Typography variant="body2" sx={{ fontFamily: 'monospace' }}>
                  {task.parent_id ?? '—'}
                </Typography>
              </Field>
              <Field label="最近更新">
                <Typography variant="body2">
                  {formatForDisplay(task.updated_at, userTimeZone)}
                </Typography>
              </Field>

              {task.description ? (
                <>
                  <Divider />
                  <Stack spacing={0.5}>
                    <Typography variant="subtitle2" color="text.secondary">
                      描述
                    </Typography>
                    <Typography variant="body2" sx={{ whiteSpace: 'pre-wrap' }}>
                      {task.description}
                    </Typography>
                  </Stack>
                </>
              ) : null}

              <Divider />
              <Stack spacing={1}>
                <Typography variant="subtitle2" color="text.secondary">
                  任务成员（{members.length}）
                </Typography>
                {membersError ? (
                  <Alert severity="error">{describeApiError(membersError)}</Alert>
                ) : null}
                {members.length === 0 && !membersError ? (
                  <Typography variant="body2" color="text.secondary">
                    暂无成员
                  </Typography>
                ) : null}
                <Stack direction="row" sx={{ flexWrap: 'wrap', gap: 1 }}>
                  {members.map((member) => (
                    <Chip
                      key={member.id}
                      size="small"
                      variant={member.role === 'owner' ? 'filled' : 'outlined'}
                      color={member.role === 'owner' ? 'primary' : 'default'}
                      label={`${member.display_name}${member.role === 'owner' ? '（持有者）' : ''}`}
                    />
                  ))}
                </Stack>
              </Stack>

              <Alert severity="info" variant="outlined">
                编辑任务、成员管理与附件将在功能期的后续步骤接入；本视图只读。
              </Alert>
            </Stack>
          ) : null}
        </Box>
      </Box>
    </Drawer>
  );
}
