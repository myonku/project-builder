/**
 * 任务成员面板（放在任务详情抽屉里）——F05 §7.2 第 3 步。
 *
 * 契约要点（《A05》§端点 9–11）：
 * - **成员管理只要求"对任务有写权限"**，不要求是任务持有者，也不要求自己是成员；
 * - 加入任务成员的前提是**目标已经是项目成员**（否则 422），因此候选池取项目成员；
 * - `role: 'owner'` 是**转移持有者**（同事务先降后升），不是"新增第二个持有者"；
 * - **不得移除/降级唯一持有者**（后端 403）——所以持有者那一行不提供这两个动作（F01 §6 第 9 条）；
 * - 归档项目上任何写入都会 409，因此 `canManage` 由调用方按"项目写权限 + 未归档"算好传进来。
 */

import * as React from 'react';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import Divider from '@mui/material/Divider';
import IconButton from '@mui/material/IconButton';
import MenuItem from '@mui/material/MenuItem';
import Stack from '@mui/material/Stack';
import TextField from '@mui/material/TextField';
import Tooltip from '@mui/material/Tooltip';
import Typography from '@mui/material/Typography';
import DeleteIcon from '@mui/icons-material/Delete';
import RefreshIcon from '@mui/icons-material/Refresh';
import SwapHorizIcon from '@mui/icons-material/SwapHoriz';
import { addTaskMember, queryTaskMembers, removeTaskMember, updateTaskMember } from '@/api/tasks';
import { toApiError, type ApiError } from '@/api/client';
import StatusTag from '@/components/common/StatusTag';
import { describeApiError, useApiError } from '@/hooks/useApiError';
import { useDialogs } from '@/hooks/useDialogs';
import { useNotifications } from '@/hooks/useNotifications';
import type { ProjectMember } from '@/types/project';
import type { TaskMember } from '@/types/task';
import { truncatedText } from '../common/TruncatedText';

export interface TaskMembersPanelProps {
  taskId: string;
  /** 是否允许管理（= 对项目有写权限且项目未归档） */
  canManage: boolean;
  /** 候选池：项目成员（《A05》要求加入任务成员前必须是项目成员） */
  projectMembers: ProjectMember[];
  /** 成员变化后通知调用方（例如刷新任务列表） */
  onChanged?: () => void;
}

export default function TaskMembersPanel({
  taskId,
  canManage,
  projectMembers,
  onChanged,
}: TaskMembersPanelProps) {
  const dialogs = useDialogs();
  const { show } = useNotifications();
  const { handle } = useApiError();

  const [members, setMembers] = React.useState<TaskMember[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState<ApiError | null>(null);
  const [selectedUserId, setSelectedUserId] = React.useState('');
  const [busy, setBusy] = React.useState(false);

  const load = React.useCallback(
    async (signal?: AbortSignal) => {
      setLoading(true);
      setError(null);
      try {
        const page = await queryTaskMembers(
          taskId,
          { page: 1, page_size: 100, sort: 'role' },
          signal,
        );
        setMembers(page.data ?? []);
      } catch (caught) {
        const apiError = toApiError(caught);
        if (apiError.code !== 'aborted') {
          setError(apiError);
        }
      } finally {
        setLoading(false);
      }
    },
    [taskId],
  );

  React.useEffect(() => {
    const controller = new AbortController();
    setSelectedUserId('');
    void load(controller.signal);
    return () => controller.abort();
  }, [load]);

  const memberIds = React.useMemo(() => new Set(members.map((m) => m.user_id)), [members]);
  const candidates = React.useMemo(
    () => projectMembers.filter((member) => !memberIds.has(member.user_id)),
    [projectMembers, memberIds],
  );

  /** 统一处理"写操作 + 重新加载 + 通知" */
  const runMutation = React.useCallback(
    async (action: () => Promise<unknown>, successMessage: string) => {
      setBusy(true);
      try {
        await action();
        await load();
        onChanged?.();
        show(successMessage, { severity: 'success' });
      } catch (caught) {
        // 403（唯一持有者 / 只读共享）、404（不可见）、409（归档）都走统一映射
        handle(caught);
      } finally {
        setBusy(false);
      }
    },
    [handle, load, onChanged, show],
  );

  const handleAdd = React.useCallback(async () => {
    if (!selectedUserId) {
      return;
    }
    await runMutation(
      () => addTaskMember(taskId, { user_id: selectedUserId }),
      '已加入任务成员。',
    );
    setSelectedUserId('');
  }, [runMutation, selectedUserId, taskId]);

  const handleTransferOwner = React.useCallback(
    async (member: TaskMember) => {
      const confirmed = await dialogs.confirm(
        `把任务持有者转移给「${member.display_name}」？当前持有者会同时被降为普通成员。`,
        { title: '转移任务持有者', okText: '确认转移', cancelText: '取消', severity: 'warning' },
      );
      if (!confirmed) {
        return;
      }
      await runMutation(
        () => updateTaskMember(taskId, member.user_id, { role: 'owner' }),
        `持有者已转移给 ${member.display_name}。`,
      );
    },
    [dialogs, runMutation, taskId],
  );

  const handleRemove = React.useCallback(
    async (member: TaskMember) => {
      const confirmed = await dialogs.confirm(`把「${member.display_name}」移出任务成员？`, {
        title: '移除任务成员',
        okText: '移除',
        cancelText: '取消',
        severity: 'warning',
      });
      if (!confirmed) {
        return;
      }
      await runMutation(
        () => removeTaskMember(taskId, member.user_id),
        `已移除 ${member.display_name}。`,
      );
    },
    [dialogs, runMutation, taskId],
  );

  return (
    <Stack spacing={1.5}>
      <Stack direction="row" sx={{ alignItems: 'center', justifyContent: 'space-between' }}>
        <Typography variant="subtitle2" color="text.secondary">
          任务成员（{members.length}）
        </Typography>
        <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
          <Tooltip title="重新加载成员与候选">
            <span>
              <IconButton size="small" aria-label="刷新任务成员" onClick={() => void load()} disabled={loading}>
                <RefreshIcon fontSize="small" />
              </IconButton>
            </span>
          </Tooltip>
          {canManage && candidates.length > 0 ? (
            <>
            <TextField
              select
              size="small"
              label="从项目成员里选"
              value={selectedUserId}
              onChange={(event) => setSelectedUserId(event.target.value)}
              sx={{ minWidth: 180 }}
            >
              {candidates.map((member) => (
                <MenuItem key={member.user_id} value={member.user_id}>
                  {member.display_name}
                </MenuItem>
              ))}
            </TextField>
              <Button
                size="medium"
                variant="outlined"
                onClick={() => void handleAdd()}
                disabled={busy || !selectedUserId}
              >
                加入
              </Button>
            </>
          ) : null}
        </Stack>
      </Stack>

      {error ? (
        <Alert
          severity={error.isNotFound ? 'info' : 'error'}
          action={
            <Button color="inherit" onClick={() => void load()}>
              重试
            </Button>
          }
        >
          {describeApiError(error)}
        </Alert>
      ) : null}

      {!error && members.length === 0 && !loading ? (
        <Typography variant="body2" color="text.secondary">
          暂无任务成员
        </Typography>
      ) : null}

      <Stack divider={<Divider flexItem />} spacing={1}>
        {members.map((member) => {
          const isOwner = member.role === 'owner';
          return (
            <Stack
              key={member.id}
              direction="row"
              spacing={1}
              sx={{ alignItems: 'center', justifyContent: 'space-between' }}
            >
              <Box sx={{ minWidth: 0 }}>
                <Typography variant="body2" noWrap>
                  {member.display_name}
                </Typography>
                {/* truncatedText 自带 <Typography component="span">，不要再套一层（否则字号/颜色被外层覆盖） */}
                {truncatedText(member.username + (member.email ? ` · ${member.email}` : ''), {
                  typographyProps: { variant: 'caption', color: 'text.secondary' },
                })}
              </Box>
              <Stack direction="row" spacing={0.5} sx={{ alignItems: 'center' }}>
                {isOwner ? (
                  <StatusTag label="持有者" tone="primary" />
                ) : (
                  <StatusTag label="成员" tone="default" variant="outlined" />
                )}
                {canManage && !isOwner ? (
                  <>
                    <Tooltip title="转移持有者（当前持有者会同时降为成员）">
                      <span>
                        <IconButton
                          size="small"
                          aria-label={`把持有者转移给 ${member.display_name}`}
                          onClick={() => void handleTransferOwner(member)}
                          disabled={busy}
                        >
                          <SwapHorizIcon fontSize="small" />
                        </IconButton>
                      </span>
                    </Tooltip>
                    <Tooltip title="移出任务成员">
                      <span>
                        <IconButton
                          size="small"
                          aria-label={`移除 ${member.display_name}`}
                          onClick={() => void handleRemove(member)}
                          disabled={busy}
                        >
                          <DeleteIcon fontSize="small" />
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

      {canManage && candidates.length === 0 && projectMembers.length > 0 ? (
        <Typography variant="caption" color="text.secondary">
          所有项目成员都已经在这个任务里了。
        </Typography>
      ) : null}
    </Stack>
  );
}
