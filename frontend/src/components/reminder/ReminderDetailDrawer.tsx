/**
 * 提醒详情抽屉（`?reminderId=`，F05 §7.2 第 6 步）。
 *
 * 为什么值得单独一个抽屉：`last_error`（失败/跳过原因）最长 500 字符，
 * 列表里只能截断显示；排查"这条到底发出去没有、为什么没发出去"需要完整原因 + 四个时间戳
 * （`scheduled_for` / `sent_at` / `created_at` / `updated_at`）+ `attempts`。
 * 这些字段详情端点（《A08》§端点 2）与列表**同源**，所以这里只做展示与重试。
 *
 * 与任务抽屉的差别：提醒**没有版本协议**（重试不带请求体、没有 `expected_updated_at`），
 * 所以这里用手写的"加载 + 重试后重新加载"，不套 `useVersionedForm`。
 */

import * as React from 'react';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Divider from '@mui/material/Divider';
import Drawer from '@mui/material/Drawer';
import IconButton from '@mui/material/IconButton';
import Link from '@mui/material/Link';
import Stack from '@mui/material/Stack';
import Tooltip from '@mui/material/Tooltip';
import Typography from '@mui/material/Typography';
import CloseIcon from '@mui/icons-material/Close';
import ReplayIcon from '@mui/icons-material/Replay';
import { Link as RouterLink } from 'react-router';
import { getReminder, retryReminder } from '@/api/reminders';
import { toApiError, type ApiError } from '@/api/client';
import FullPageLoading from '@/components/common/FullPageLoading';
import StatusTag from '@/components/common/StatusTag';
import { canRetryReminder, reminderStatusMeta } from '@/components/reminder/reminderMeta';
import { describeApiError, useApiError } from '@/hooks/useApiError';
import { useDialogs } from '@/hooks/useDialogs';
import { useNotifications } from '@/hooks/useNotifications';
import { useAuthStore } from '@/stores/auth';
import type { Reminder } from '@/types/reminder';
import { formatForDisplay } from '@/utils/datetime';

export interface ReminderDetailDrawerProps {
  /** 打开的提醒 id；`null` 表示关闭 */
  reminderId: string | null;
  /** 重试成功后通知调用方（刷新列表） */
  onChanged?: () => void;
  onClose: () => void;
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <Stack direction="row" spacing={2} sx={{ justifyContent: 'space-between', alignItems: 'flex-start' }}>
      <Typography variant="body2" color="text.secondary" sx={{ flexShrink: 0 }}>
        {label}
      </Typography>
      <Box sx={{ textAlign: 'right', minWidth: 0 }}>{children}</Box>
    </Stack>
  );
}

export default function ReminderDetailDrawer({
  reminderId,
  onChanged,
  onClose,
}: ReminderDetailDrawerProps) {
  const dialogs = useDialogs();
  const { show } = useNotifications();
  const { handle } = useApiError();
  const userTimeZone = useAuthStore((state) => state.user?.timezone);

  const [reminder, setReminder] = React.useState<Reminder | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState<ApiError | null>(null);
  const [retrying, setRetrying] = React.useState(false);

  const load = React.useCallback(
    async (signal?: AbortSignal) => {
      if (!reminderId) {
        setReminder(null);
        return;
      }
      setLoading(true);
      setError(null);
      try {
        setReminder(await getReminder(reminderId, signal));
      } catch (caught) {
        const apiError = toApiError(caught);
        if (apiError.code !== 'aborted') {
          setError(apiError);
          setReminder(null);
        }
      } finally {
        setLoading(false);
      }
    },
    [reminderId],
  );

  React.useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [load]);

  const handleRetry = React.useCallback(async () => {
    if (!reminder) {
      return;
    }
    const confirmed = await dialogs.confirm(
      '重试会把这条提醒重新排入待发送队列；后台派发循环下一次运行时就会再次尝试发送，' +
        '收件人可能因此收到邮件。确认重试？',
      { title: '重试发送提醒', okText: '确认重试', cancelText: '取消', severity: 'warning' },
    );
    if (!confirmed) {
      return;
    }
    setRetrying(true);
    try {
      await retryReminder(reminder.id);
      show('已重新排入待发送队列，稍后刷新查看结果。', { severity: 'success' });
      await load();
      onChanged?.();
    } catch (caught) {
      // `pending` / `sent` 会拿到 409（fields.reminder_id），由统一映射给出文案
      handle(caught);
      await load();
    } finally {
      setRetrying(false);
    }
  }, [dialogs, handle, load, onChanged, reminder, show]);

  const statusMeta = reminderStatusMeta(reminder?.status);
  const retryable = canRetryReminder(reminder?.status);

  return (
    <Drawer
      anchor="right"
      open={reminderId !== null}
      onClose={onClose}
      slotProps={{ paper: { sx: { width: { xs: '100%', sm: 460 } } } }}
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
        <Stack
          direction="row"
          sx={{ alignItems: 'center', justifyContent: 'space-between', px: 2, py: 1.5 }}
        >
          <Typography variant="h6" component="h2">
            提醒详情
          </Typography>
          <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
            <Tooltip
              title={
                retryable
                  ? '重新排入待发送队列'
                  : '只有"发送失败"与"已跳过"可以重试（其它状态服务端会返回 409）'
              }
            >
              <span>
                <Button
                  size="small"
                  variant="outlined"
                  startIcon={<ReplayIcon />}
                  disabled={!retryable || retrying || !reminder}
                  onClick={() => void handleRetry()}
                >
                  重试
                </Button>
              </span>
            </Tooltip>
            <Tooltip title="关闭">
              <IconButton onClick={onClose} aria-label="关闭提醒详情">
                <CloseIcon />
              </IconButton>
            </Tooltip>
          </Stack>
        </Stack>
        <Divider />

        <Box sx={{ flex: 1, overflow: 'auto', px: 2, py: 2 }}>
          {loading && !reminder ? <FullPageLoading message="正在加载提醒…" /> : null}

          {error ? (
            <Alert
              severity={error.isNotFound ? 'info' : 'error'}
              action={
                <Button color="inherit" size="small" onClick={() => void load()}>
                  重试
                </Button>
              }
            >
              {describeApiError(error)}
            </Alert>
          ) : null}

          {reminder ? (
            <Stack spacing={2}>
              <Stack spacing={1}>
                <Typography variant="h6" component="h3">
                  {reminder.kind_label}
                </Typography>
                <Stack direction="row" spacing={1} sx={{ alignItems: 'center', flexWrap: 'wrap', gap: 1 }}>
                  <StatusTag label={statusMeta.label} tone={statusMeta.tone} />
                  <StatusTag label={`尝试 ${reminder.attempts} 次`} tone="default" variant="outlined" />
                </Stack>
                <Typography variant="caption" color="text.secondary">
                  {statusMeta.hint}
                </Typography>
              </Stack>

              {reminder.status === 'skipped' ? (
                <Alert severity="warning" variant="outlined">
                  跳过不是失败：服务端按规则认为这条不该发（例如收件人没有邮箱、关闭了邮件提醒，
                  或<strong>邮件功能未启用</strong>）。若原因已经消失，可以重试。
                </Alert>
              ) : null}

              <Divider />
              <Field label="收件人">
                <Typography variant="body2">{reminder.recipient_email}</Typography>
              </Field>
              <Field label="计划发送">
                <Typography variant="body2">
                  {formatForDisplay(reminder.scheduled_for, userTimeZone)}
                </Typography>
              </Field>
              <Field label="发送时刻">
                <Typography variant="body2">
                  {reminder.sent_at ? formatForDisplay(reminder.sent_at, userTimeZone) : '—'}
                </Typography>
              </Field>
              <Field label="记录创建">
                <Typography variant="body2">
                  {formatForDisplay(reminder.created_at, userTimeZone)}
                </Typography>
              </Field>
              <Field label="最近更新">
                <Typography variant="body2">
                  {formatForDisplay(reminder.updated_at, userTimeZone)}
                </Typography>
              </Field>
              <Field label="关联任务">
                <Link
                  component={RouterLink}
                  to={`/projects?taskId=${reminder.task_id}`}
                  variant="body2"
                  sx={{ fontFamily: 'monospace', wordBreak: 'break-all' }}
                >
                  {reminder.task_id}
                </Link>
              </Field>

              {reminder.last_error ? (
                <>
                  <Divider />
                  <Stack spacing={0.5}>
                    <Typography variant="subtitle2" color="text.secondary">
                      失败 / 跳过原因
                    </Typography>
                    <Typography variant="body2" sx={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
                      {reminder.last_error}
                    </Typography>
                  </Stack>
                </>
              ) : null}

              <Divider />
              <Typography variant="caption" color="text.secondary">
                重试不会立即发信：只是把状态改回「待发送」并把计划时间设为当前时刻，
                实际发送由后台派发循环完成——所以重试后请稍后刷新查看结果。
              </Typography>
            </Stack>
          ) : null}
        </Box>
      </Box>
    </Drawer>
  );
}
