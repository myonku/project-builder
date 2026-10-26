/**
 * 工作组加入申请审核（《A03》§10–§11）。
 *
 * - 默认只看 `pending`；省略 `status` 时服务端返回全部历史申请；
 * - 响应**不含** `created_at` / `updated_at`，所以界面只展示审核人/审核时间，
 *   不伪造“申请时间”；
 * - `approved` 的写入是原子的（申请状态 + 成员关系同事务），审核后刷新成员列表。
 */

import * as React from 'react';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import CircularProgress from '@mui/material/CircularProgress';
import Divider from '@mui/material/Divider';
import IconButton from '@mui/material/IconButton';
import LinearProgress from '@mui/material/LinearProgress';
import MenuItem from '@mui/material/MenuItem';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';
import Tooltip from '@mui/material/Tooltip';
import Typography from '@mui/material/Typography';
import CheckIcon from '@mui/icons-material/Check';
import CloseIcon from '@mui/icons-material/Close';
import RefreshIcon from '@mui/icons-material/Refresh';
import { queryJoinRequests, reviewJoinRequest } from '@/api/groups';
import { toApiError, type ApiError } from '@/api/client';
import StatusTag from '@/components/common/StatusTag';
import UserAvatar from '@/components/common/UserAvatar';
import { truncatedText } from '@/components/common/TruncatedText';
import {
  JOIN_REQUEST_STATUS_OPTIONS,
  joinRequestStatusMeta,
} from '@/components/group/groupMeta';
import { describeApiError, useApiError } from '@/hooks/useApiError';
import { useDialogs } from '@/hooks/useDialogs';
import { useNotifications } from '@/hooks/useNotifications';
import { useAuthStore } from '@/stores/auth';
import type { JoinRequest, JoinRequestStatus } from '@/types/group';
import { formatForDisplay } from '@/utils/datetime';

export interface GroupJoinRequestsPanelProps {
  groupId: string;
  /** 审核通过后通知详情抽屉刷新成员；列表页也可同步刷新 */
  onChanged?: () => void;
}

const PAGE_SIZE = 100;

export default function GroupJoinRequestsPanel({
  groupId,
  onChanged,
}: GroupJoinRequestsPanelProps) {
  const dialogs = useDialogs();
  const { show } = useNotifications();
  const { handle } = useApiError();
  const userTimeZone = useAuthStore((state) => state.user?.timezone);

  const [status, setStatus] = React.useState<JoinRequestStatus | ''>('pending');
  const [requests, setRequests] = React.useState<JoinRequest[]>([]);
  const [total, setTotal] = React.useState(0);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState<ApiError | null>(null);
  const [busyId, setBusyId] = React.useState<string | null>(null);
  const [refreshToken, setRefreshToken] = React.useState(0);

  const reload = React.useCallback(() => {
    setRefreshToken((token) => token + 1);
  }, []);

  React.useEffect(() => {
    const controller = new AbortController();
    let cancelled = false;
    setLoading(true);
    setError(null);
    void (async () => {
      try {
        const page = await queryJoinRequests(
          groupId,
          {
            ...(status ? { status } : {}),
            page: 1,
            page_size: PAGE_SIZE,
            sort: 'created_at',
          },
          controller.signal,
        );
        if (cancelled) {
          return;
        }
        setRequests(page.data ?? []);
        setTotal(page.pagination?.total ?? 0);
      } catch (caught) {
        if (cancelled) {
          return;
        }
        const apiError = toApiError(caught);
        if (apiError.code !== 'aborted') {
          setError(apiError);
          setRequests([]);
          setTotal(0);
        }
      } finally {
        if (!cancelled) {
          setLoading(false);
        }
      }
    })();
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [groupId, status, refreshToken]);

  const review = React.useCallback(
    async (request: JoinRequest, decision: 'approved' | 'rejected') => {
      const verb = decision === 'approved' ? '通过' : '拒绝';
      const confirmed = await dialogs.confirm(
        `${verb}「${request.display_name || request.username}」的加入申请？` +
          (decision === 'approved'
            ? '通过后会立即成为该组的普通成员。'
            : '拒绝后申请人可以稍后重新申请。'),
        {
          title: `${verb}加入申请`,
          okText: verb,
          cancelText: '取消',
          severity: decision === 'approved' ? 'info' : 'warning',
        },
      );
      if (!confirmed) {
        return;
      }
      setBusyId(request.user_id);
      try {
        await reviewJoinRequest(groupId, request.user_id, { status: decision });
        show(`已${verb}加入申请。`, { severity: 'success' });
        reload();
        onChanged?.();
      } catch (caught) {
        const apiError = toApiError(caught);
        if (apiError.status === 409) {
          show(
            apiError.fields.status ?? '该申请已被审核过，请刷新列表。',
            { severity: 'warning' },
          );
          reload();
        } else {
          handle(apiError);
        }
      } finally {
        setBusyId(null);
      }
    },
    [dialogs, groupId, handle, onChanged, reload, show],
  );

  return (
    <Stack spacing={1.5} sx={{ pt: 1.5 }}>
      <Stack
        direction="row"
        sx={{ alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: 1 }}
      >
        <TextField
          select
          size="small"
          label="申请状态"
          value={status}
          onChange={(event) => setStatus(event.target.value as JoinRequestStatus | '')}
          sx={{ minWidth: 160 }}
          slotProps={{ inputLabel: { shrink: true }, select: { displayEmpty: true } }}
        >
          <MenuItem value="">全部状态</MenuItem>
          {JOIN_REQUEST_STATUS_OPTIONS.map((option) => (
            <MenuItem key={option.value} value={option.value}>
              {option.label}
            </MenuItem>
          ))}
        </TextField>
        <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
          <Typography variant="caption" color="text.secondary">
            共 {total} 条
          </Typography>
          <Tooltip title="重新加载加入申请">
            <span>
              <IconButton
                size="small"
                aria-label="刷新加入申请"
                onClick={reload}
                disabled={loading}
              >
                <RefreshIcon fontSize="small" />
              </IconButton>
            </span>
          </Tooltip>
        </Stack>
      </Stack>

      {loading ? <LinearProgress /> : null}
      {error ? (
        <Alert
          severity={error.isNotFound ? 'info' : 'error'}
          action={
            <Button color="inherit" onClick={reload}>
              重试
            </Button>
          }
        >
          {describeApiError(error)}
        </Alert>
      ) : null}

      <Typography variant="caption" color="text.secondary">
        可查看待处理或已审核的加入申请。
        {total > PAGE_SIZE ? ` 共 ${total} 条，当前只加载前 ${PAGE_SIZE} 条。` : ''}
      </Typography>

      <Stack divider={<Divider flexItem />} spacing={1}>
        {requests.map((request) => {
          const statusMeta = joinRequestStatusMeta(request.status);
          const pending = request.status === 'pending';
          return (
            <Stack
              key={request.id}
              direction="row"
              spacing={1}
              sx={{ alignItems: 'center', justifyContent: 'space-between' }}
            >
              <Box sx={{ minWidth: 0 }}>
                <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
                  <UserAvatar user={{ id: request.user_id, display_name: request.display_name, avatar_updated_at: request.avatar_updated_at }} size={28} />
                  <Typography variant="body2" noWrap>
                    {request.display_name || request.username}
                  </Typography>
                </Stack>
                {truncatedText(
                  `${request.username}${request.email ? ` · ${request.email}` : ''}`,
                  {
                    typographyProps: { variant: 'caption', color: 'text.secondary' },
                  },
                )}
                {!pending ? (
                  <Typography variant="caption" color="text.secondary" component="div">
                    {request.reviewed_by
                      ? `审核人 ${request.reviewed_by.slice(-8)}`
                      : '审核人未知'}
                    {request.reviewed_at
                      ? ` · ${formatForDisplay(request.reviewed_at, userTimeZone)}`
                      : ''}
                  </Typography>
                ) : null}
              </Box>
              <Stack direction="row" spacing={0.5} sx={{ alignItems: 'center' }}>
                <StatusTag label={statusMeta.label} tone={statusMeta.tone} variant="outlined" />
                {pending ? (
                  <>
                    <Tooltip title="通过申请（加入为普通成员）">
                      <span>
                        <IconButton
                          size="small"
                          color="success"
                          aria-label={`通过 ${request.username} 的加入申请`}
                          disabled={busyId === request.user_id}
                          onClick={() => void review(request, 'approved')}
                        >
                          {busyId === request.user_id ? (
                            <CircularProgress size={16} />
                          ) : (
                            <CheckIcon fontSize="small" />
                          )}
                        </IconButton>
                      </span>
                    </Tooltip>
                    <Tooltip title="拒绝申请">
                      <span>
                        <IconButton
                          size="small"
                          color="warning"
                          aria-label={`拒绝 ${request.username} 的加入申请`}
                          disabled={busyId === request.user_id}
                          onClick={() => void review(request, 'rejected')}
                        >
                          <CloseIcon fontSize="small" />
                        </IconButton>
                      </span>
                    </Tooltip>
                  </>
                ) : null}
              </Stack>
            </Stack>
          );
        })}
      </Stack>

      {!loading && requests.length === 0 ? (
        <Typography variant="body2" color="text.secondary">
          当前筛选下没有加入申请。
        </Typography>
      ) : null}
    </Stack>
  );
}
