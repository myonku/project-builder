/**
 * 项目成员面板（放在项目详情页）——F05 §7.2 第 3 步。
 *
 * 契约要点（《A04》§端点 6–9、《A03》§端点 5）：
 * - 列表：`POST /projects/{id}/members/query`（项目可见者即可读）；
 * - 管理动作（加入 / 转移持有者 / 移除）**只有项目持有者或系统管理员**能做，
 *   且管理权不足时后端返回 `404 project_not_found`（不是 403）——
 *   因此 `canManage` 由调用方用 `usePermission` 算好，避免用户点了才吃 404；
 * - `role: 'owner'` = **转移持有者**（同事务先降后升）：界面只提供"转移持有者"，
 *   **不提供"改为普通成员"**（那个请求永远不能成功，《A04》§端点 8，F01 §6 第 9 条）；
 * - **不得移除唯一持有者**，所以持有者那一行不给移除按钮；
 * - 候选池取**归属工作组的活跃成员**：`POST /users/query` 是系统管理员专用（《A02》§端点 5），
 *   而项目持有者通常是组内活跃成员，用组员列表既能覆盖持有者也覆盖管理员（F04 D27）。
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
import GroupAddIcon from '@mui/icons-material/GroupAdd';
import SwapHorizIcon from '@mui/icons-material/SwapHoriz';
import { queryGroupMembers } from '@/api/groups';
import {
  addProjectMember,
  queryProjectMembers,
  removeProjectMember,
  updateProjectMember,
} from '@/api/projects';
import { toApiError, type ApiError } from '@/api/client';
import StatusTag from '@/components/common/StatusTag';
import UserAvatar from '@/components/common/UserAvatar';
import { truncatedText} from '@/components/common/TruncatedText';
import { describeApiError, useApiError } from '@/hooks/useApiError';
import { useDialogs } from '@/hooks/useDialogs';
import { useNotifications } from '@/hooks/useNotifications';
import type { ProjectMember } from '@/types/project';

interface Candidate {
  user_id: string;
  display_name: string;
  username: string;
}

export interface ProjectMembersPanelProps {
  projectId: string;
  /** 项目归属工作组：用来取候选池（组内活跃成员） */
  groupId: string;
  /** 是否允许管理（= `usePermission().canManageProject` 且项目未归档） */
  canManage: boolean;
  /** 成员变化后通知调用方（转移持有者会影响项目本身的 `membership_role`） */
  onChanged?: () => void;
}

export default function ProjectMembersPanel({
  projectId,
  groupId,
  canManage,
  onChanged,
}: ProjectMembersPanelProps) {
  const dialogs = useDialogs();
  const { show } = useNotifications();
  const { handle } = useApiError();

  const [members, setMembers] = React.useState<ProjectMember[]>([]);
  const [candidates, setCandidates] = React.useState<Candidate[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState<ApiError | null>(null);
  const [selectedUserId, setSelectedUserId] = React.useState('');
  const [busy, setBusy] = React.useState(false);

  const load = React.useCallback(
    async (signal?: AbortSignal) => {
      setLoading(true);
      setError(null);
      try {
        const page = await queryProjectMembers(
          projectId,
          { page: 1, page_size: 100, sort: 'role' },
          signal,
        );
        const list = page.data ?? [];
        setMembers(list);
        if (canManage && groupId) {
          // 候选池：组内活跃成员 - 已是项目成员的人
          const inProject = new Set(list.map((item) => item.user_id));
          const groupPage = await queryGroupMembers(
            groupId,
            { status: 'active', page: 1, page_size: 100, sort: 'display_name' },
            signal,
          );
          setCandidates(
            (groupPage.data ?? [])
              .filter((item) => !inProject.has(item.user_id))
              .map((item) => ({
                user_id: item.user_id,
                display_name: item.display_name,
                username: item.username,
              })),
          );
        } else {
          setCandidates([]);
        }
      } catch (caught) {
        const apiError = toApiError(caught);
        if (apiError.code !== 'aborted') {
          setError(apiError);
        }
      } finally {
        setLoading(false);
      }
    },
    [canManage, groupId, projectId],
  );

  React.useEffect(() => {
    const controller = new AbortController();
    setSelectedUserId('');
    void load(controller.signal);
    return () => controller.abort();
  }, [load]);

  const runMutation = React.useCallback(
    async (action: () => Promise<unknown>, successMessage: string) => {
      setBusy(true);
      try {
        await action();
        await load();
        onChanged?.();
        show(successMessage, { severity: 'success' });
      } catch (caught) {
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
      () => addProjectMember(projectId, { user_id: selectedUserId }),
      '已加入项目成员（默认是普通成员）。',
    );
    setSelectedUserId('');
  }, [projectId, runMutation, selectedUserId]);

  const handleTransferOwner = React.useCallback(
    async (member: ProjectMember) => {
      const confirmed = await dialogs.confirm(
        `把项目持有者转移给「${member.display_name}」？转移后原持有者将失去项目管理权（系统管理员除外）。`,
        { title: '转移项目持有者', okText: '确认转移', cancelText: '取消', severity: 'warning' },
      );
      if (!confirmed) {
        return;
      }
      await runMutation(
        () => updateProjectMember(projectId, member.user_id, { role: 'owner' }),
        `持有者已转移给 ${member.display_name}。`,
      );
    },
    [dialogs, projectId, runMutation],
  );

  const handleRemove = React.useCallback(
    async (member: ProjectMember) => {
      const confirmed = await dialogs.confirm(
        `把「${member.display_name}」移出项目？移出后该用户将看不到这个项目（系统管理员除外）。`,
        { title: '移除项目成员', okText: '移除', cancelText: '取消', severity: 'warning' },
      );
      if (!confirmed) {
        return;
      }
      await runMutation(
        () => removeProjectMember(projectId, member.user_id),
        `已移除 ${member.display_name}。`,
      );
    },
    [dialogs, projectId, runMutation],
  );

  return (
    <Stack spacing={1.5}>
      <Stack direction="row" sx={{ alignItems: 'center', justifyContent: 'space-between' }}>
        <Typography variant="h6" component="h2">
          成员
          <Typography component="span" variant="body2" color="text.secondary" sx={{ ml: 1 }}>
            共 {members.length} 人
          </Typography>
        </Typography>
        <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
          <Tooltip title="重新加载成员与候选（例如刚把某人加进工作组）">
            <span>
              <IconButton size="small" aria-label="刷新成员列表" onClick={() => void load()} disabled={loading}>
                <RefreshIcon fontSize="small" />
              </IconButton>
            </span>
          </Tooltip>
          {canManage && candidates.length > 0 ? (
            <>
            <TextField
              select
              size="small"
              label="从组内活跃成员选择"
              value={selectedUserId}
              onChange={(event) => setSelectedUserId(event.target.value)}
              sx={{ minWidth: 220 }}
            >
              {candidates.map((candidate) => (
                <MenuItem key={candidate.user_id} value={candidate.user_id}>
                  {candidate.display_name}（{candidate.username}）
                </MenuItem>
              ))}
            </TextField>
              <Button
                size="medium"
                variant="outlined"
                startIcon={<GroupAddIcon />}
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
                <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
                  <UserAvatar user={{ id: member.user_id, display_name: member.display_name, avatar_updated_at: member.avatar_updated_at }} size={28} />
                  <Typography variant="body2" noWrap>
                    {member.display_name}
                  </Typography>
                </Stack>
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
                    <Tooltip title="移除项目成员">
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

      {loading && members.length === 0 ? (
        <Typography variant="body2" color="text.secondary">
          正在加载成员…
        </Typography>
      ) : null}

      {canManage && candidates.length === 0 && members.length > 0 ? (
        <Typography variant="caption" color="text.secondary">
          归属工作组的活跃成员都已经在项目里了。要加入组外的人，先把他加进工作组（或由系统管理员指派）。
        </Typography>
      ) : null}
    </Stack>
  );
}
