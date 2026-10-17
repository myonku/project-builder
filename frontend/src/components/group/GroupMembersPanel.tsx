/**
 * 工作组成员管理（《A03》§5–§8）。
 *
 * 契约要点：
 * - 成员是**软移除**：`status='removed'`，恢复走 `PATCH .../members/{user_id}` 的
 *   `{"status":"active"}`；重新 `POST .../members` 会得到 `409 duplicate_member`。
 * - 组里**没有 owner 成员角色**：所有者由 `ownerId` 判定；所有者不可降级/移除，
 *   换人只能走 `PATCH /groups/{id}` 的 `owner_id`（本组件的“转移所有权”）。
 * - 写接口（添加/修改成员）响应的用户展示字段是空串，所以每次变更后重新拉成员列表。
 * - 候选池用管理员专用的 `POST /users/query`（当前页面本身就仅管理员可见）；
 *   已移除的成员也在成员表里，必须从候选池排除，否则会撞 409 duplicate_member。
 */

import * as React from 'react';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import CircularProgress from '@mui/material/CircularProgress';
import Divider from '@mui/material/Divider';
import FormControlLabel from '@mui/material/FormControlLabel';
import IconButton from '@mui/material/IconButton';
import LinearProgress from '@mui/material/LinearProgress';
import MenuItem from '@mui/material/MenuItem';
import Stack from '@mui/material/Stack';
import Switch from '@mui/material/Switch';
import TextField from '@mui/material/TextField';
import Tooltip from '@mui/material/Tooltip';
import Typography from '@mui/material/Typography';
import DeleteIcon from '@mui/icons-material/Delete';
import GroupAddIcon from '@mui/icons-material/GroupAdd';
import RefreshIcon from '@mui/icons-material/Refresh';
import SwapHorizIcon from '@mui/icons-material/SwapHoriz';
import WorkspacePremiumIcon from '@mui/icons-material/WorkspacePremium';
import {
  addGroupMember,
  queryGroupMembers,
  removeGroupMember,
  updateGroupMember,
} from '@/api/groups';
import { queryUsers } from '@/api/users';
import { toApiError, type ApiError } from '@/api/client';
import StatusTag from '@/components/common/StatusTag';
import { truncatedText } from '@/components/common/TruncatedText';
import {
  GROUP_MEMBER_ROLE_OPTIONS,
  groupMemberRoleMeta,
  groupMembershipStatusMeta,
} from '@/components/group/groupMeta';
import { describeApiError, useApiError } from '@/hooks/useApiError';
import { useDialogs } from '@/hooks/useDialogs';
import { useNotifications } from '@/hooks/useNotifications';
import type { GroupMember, GroupMemberRole } from '@/types/group';
import type { User } from '@/types/user';

export interface GroupMembersPanelProps {
  groupId: string;
  /** 当前所有者；用于区分“所有者”与可转移的普通成员 */
  ownerId: string;
  onChanged?: () => void;
  /** 由详情抽屉调用 `updateGroup({ owner_id })`；返回是否成功 */
  onTransferOwner: (userId: string) => Promise<boolean>;
}

export default function GroupMembersPanel({
  groupId,
  ownerId,
  onChanged,
  onTransferOwner,
}: GroupMembersPanelProps) {
  const dialogs = useDialogs();
  const { show } = useNotifications();
  const { handle } = useApiError();

  const [members, setMembers] = React.useState<GroupMember[]>([]);
  const [candidates, setCandidates] = React.useState<User[]>([]);
  const [candidateTotal, setCandidateTotal] = React.useState(0);
  const [memberTotal, setMemberTotal] = React.useState(0);
  const [includeRemoved, setIncludeRemoved] = React.useState(false);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState<ApiError | null>(null);
  const [selectedUserId, setSelectedUserId] = React.useState('');
  const [selectedRole, setSelectedRole] = React.useState<GroupMemberRole>('member');
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
        const [memberPage, userPage] = await Promise.all([
          queryGroupMembers(
            groupId,
            { page: 1, page_size: 100, sort: 'joined_at' },
            controller.signal,
          ),
          queryUsers(
            { include_disabled: false, page: 1, page_size: 100, sort: 'display_name' },
            controller.signal,
          ),
        ]);
        if (cancelled) {
          return;
        }
        const memberRows = memberPage.data ?? [];
        setMembers(memberRows);
        setMemberTotal(memberPage.pagination?.total ?? 0);
        const memberIds = new Set(memberRows.map((member) => member.user_id));
        const users = userPage.data ?? [];
        setCandidateTotal(userPage.pagination?.total ?? 0);
        setCandidates(users.filter((user) => !memberIds.has(user.id)));
        setSelectedUserId((current) =>
          users.some((user) => user.id === current) && !memberIds.has(current) ? current : '',
        );
      } catch (caught) {
        if (cancelled) {
          return;
        }
        const apiError = toApiError(caught);
        if (apiError.code !== 'aborted') {
          setError(apiError);
          setMembers([]);
          setCandidates([]);
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
  }, [groupId, ownerId, refreshToken]);

  const reportConflict = React.useCallback(
    (caught: unknown, fallback: string) => {
      const apiError = toApiError(caught);
      const detail = apiError.fields.role ?? apiError.fields.status ?? apiError.fields.user_id;
      if (apiError.status === 409) {
        show(detail ?? fallback, { severity: 'warning' });
      } else {
        handle(apiError);
      }
    },
    [handle, show],
  );

  const run = React.useCallback(
    async (action: () => Promise<unknown>, successMessage: string) => {
      try {
        await action();
        show(successMessage, { severity: 'success' });
        reload();
        onChanged?.();
      } catch (caught) {
        reportConflict(caught, '当前状态不允许该成员操作。');
      }
    },
    [onChanged, reload, reportConflict, show],
  );

  const handleAdd = React.useCallback(async () => {
    if (!selectedUserId) {
      return;
    }
    setBusyId('add');
    await run(
      () => addGroupMember(groupId, { user_id: selectedUserId, role: selectedRole }),
      '成员已加入工作组。',
    );
    setBusyId(null);
    setSelectedUserId('');
  }, [groupId, run, selectedRole, selectedUserId]);

  const handleToggleRole = React.useCallback(
    async (member: GroupMember) => {
      const nextRole: GroupMemberRole = member.role === 'manager' ? 'member' : 'manager';
      setBusyId(member.user_id);
      await run(
        () => updateGroupMember(groupId, member.user_id, { role: nextRole }),
        nextRole === 'manager' ? '已提升为组管理员。' : '已降为普通成员。',
      );
      setBusyId(null);
    },
    [groupId, run],
  );

  const handleRemove = React.useCallback(
    async (member: GroupMember) => {
      const confirmed = await dialogs.confirm(
        `把「${member.display_name}」移出工作组？这是软移除：个人数据与历史记录保留，` +
          '之后可以用“恢复”把成员关系重新置为 active。',
        {
          title: '移除工作组成员',
          okText: '移除',
          cancelText: '取消',
          severity: 'warning',
        },
      );
      if (!confirmed) {
        return;
      }
      setBusyId(member.user_id);
      await run(
        () => removeGroupMember(groupId, member.user_id),
        '成员已软移除（status=removed）。',
      );
      setBusyId(null);
    },
    [dialogs, groupId, run],
  );

  const handleRestore = React.useCallback(
    async (member: GroupMember) => {
      setBusyId(member.user_id);
      await run(
        () => updateGroupMember(groupId, member.user_id, { status: 'active' }),
        '成员关系已恢复。',
      );
      setBusyId(null);
    },
    [groupId, run],
  );

  const handleTransferOwner = React.useCallback(
    async (member: GroupMember) => {
      const confirmed = await dialogs.confirm(
        `把工作组所有者转移给「${member.display_name}」？当前所有者会保留成员身份并降为普通成员；` +
          '其余字段不会在这次请求里修改。',
        {
          title: '转移工作组所有权',
          okText: '确认转移',
          cancelText: '取消',
          severity: 'warning',
        },
      );
      if (!confirmed) {
        return;
      }
      setBusyId(member.user_id);
      try {
        const transferred = await onTransferOwner(member.user_id);
        if (transferred) {
          show('工作组所有权已转移。', { severity: 'success' });
          reload();
          onChanged?.();
        }
      } finally {
        setBusyId(null);
      }
    },
    [dialogs, onChanged, onTransferOwner, reload, show],
  );

  const visibleMembers = includeRemoved
    ? members
    : members.filter((member) => member.status === 'active');
  const activeMembers = members.filter((member) => member.status === 'active').length;

  return (
    <Stack spacing={1.5}>
      <Stack
        direction="row"
        sx={{ alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: 1 }}
      >
        <Typography variant="subtitle2" color="text.secondary">
          成员（在组 {activeMembers} 人，共 {memberTotal} 条关系）
        </Typography>
        <Stack direction="row" spacing={1} sx={{ alignItems: 'center', flexWrap: 'wrap' }}>
          <FormControlLabel
            sx={{ mr: 0 }}
            control={
              <Switch
                size="small"
                checked={includeRemoved}
                onChange={(event) => setIncludeRemoved(event.target.checked)}
              />
            }
            label={<Typography variant="body2">含已移除</Typography>}
          />
          <Tooltip title="重新加载成员与候选用户">
            <span>
              <IconButton size="small" aria-label="刷新工作组成员" onClick={reload} disabled={loading}>
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
            <Button color="inherit" size="small" onClick={reload}>
              重试
            </Button>
          }
        >
          {describeApiError(error)}
        </Alert>
      ) : null}

      <Box
        component="form"
        onSubmit={(event) => {
          event.preventDefault();
          void handleAdd();
        }}
        sx={{
          display: 'flex',
          flexWrap: 'wrap',
          gap: 1,
          alignItems: 'center',
        }}
      >
        <TextField
          select
          size="small"
          label="添加成员"
          value={selectedUserId}
          onChange={(event) => setSelectedUserId(event.target.value)}
          sx={{ minWidth: 240, flex: '1 1 240px' }}
          helperText={
            candidateTotal > 100
              ? `共 ${candidateTotal} 个账号，候选只取前 100 个`
              : '候选为未停用且不在本组的账号'
          }
          disabled={loading || Boolean(error)}
        >
          <MenuItem value="">请选择用户</MenuItem>
          {candidates.map((candidate) => (
            <MenuItem key={candidate.id} value={candidate.id}>
              {candidate.display_name}（{candidate.username}）
            </MenuItem>
          ))}
        </TextField>
        <TextField
          select
          size="small"
          label="初始角色"
          value={selectedRole}
          onChange={(event) => setSelectedRole(event.target.value as GroupMemberRole)}
          sx={{ minWidth: 130 }}
        >
          {GROUP_MEMBER_ROLE_OPTIONS.map((option) => (
            <MenuItem key={option.value} value={option.value}>
              {option.label}
            </MenuItem>
          ))}
        </TextField>
        <Button
          type="submit"
          variant="outlined"
          startIcon={busyId === 'add' ? <CircularProgress size={16} /> : <GroupAddIcon />}
          disabled={!selectedUserId || busyId === 'add' || loading}
        >
          加入
        </Button>
      </Box>

      {candidates.length === 0 && !loading ? (
        <Typography variant="caption" color="text.secondary">
          前 100 个可用账号里没有候选（都已在该组，或账号数量超过候选窗口）。
        </Typography>
      ) : null}
      {memberTotal > 100 ? (
        <Typography variant="caption" color="text.secondary">
          成员关系共 {memberTotal} 条，本面板只加载前 100 条；更早的成员请直接用成员列表接口查询。
        </Typography>
      ) : null}

      <Stack divider={<Divider flexItem />} spacing={1}>
        {visibleMembers.map((member) => {
          const isOwner = member.user_id === ownerId;
          const isRemoved = member.status === 'removed';
          const roleMeta = groupMemberRoleMeta(member.role);
          const statusMeta = groupMembershipStatusMeta(member.status);
          return (
            <Stack
              key={member.id}
              direction="row"
              spacing={1}
              sx={{ alignItems: 'center', justifyContent: 'space-between' }}
            >
              <Box sx={{ minWidth: 0 }}>
                <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
                  <Typography variant="body2" noWrap>
                    {member.display_name || member.username || member.user_id.slice(-8)}
                  </Typography>
                  {isRemoved ? (
                    <StatusTag label={statusMeta.label} tone={statusMeta.tone} variant="outlined" />
                  ) : null}
                </Stack>
                {truncatedText(
                  `${member.username}${member.email ? ` · ${member.email}` : ''}`,
                  {
                    typographyProps: { variant: 'caption', color: 'text.secondary' },
                  },
                )}
              </Box>

              <Stack direction="row" spacing={0.5} sx={{ alignItems: 'center' }}>
                <StatusTag
                  label={isOwner ? '所有者' : roleMeta.label}
                  tone={isOwner ? 'primary' : roleMeta.tone}
                  variant="outlined"
                />
                {!isRemoved && !isOwner ? (
                  <Tooltip title={member.role === 'manager' ? '降为普通成员' : '提升为组管理员'}>
                    <span>
                      <IconButton
                        size="small"
                        aria-label={`${member.role === 'manager' ? '降级' : '提升'} ${member.display_name}`}
                        disabled={busyId === member.user_id}
                        onClick={() => void handleToggleRole(member)}
                      >
                        <SwapHorizIcon fontSize="small" />
                      </IconButton>
                    </span>
                  </Tooltip>
                ) : null}
                {!isRemoved && !isOwner ? (
                  <Tooltip title="转移工作组所有权">
                    <span>
                      <IconButton
                        size="small"
                        color="primary"
                        aria-label={`把所有权转移给 ${member.display_name}`}
                        disabled={busyId === member.user_id}
                        onClick={() => void handleTransferOwner(member)}
                      >
                        <WorkspacePremiumIcon fontSize="small" />
                      </IconButton>
                    </span>
                  </Tooltip>
                ) : null}
                {isRemoved ? (
                  <Tooltip title="恢复成员关系（status=active）">
                    <span>
                      <Button
                        size="small"
                        onClick={() => void handleRestore(member)}
                        disabled={busyId === member.user_id}
                      >
                        恢复
                      </Button>
                    </span>
                  </Tooltip>
                ) : !isOwner ? (
                  <Tooltip title="软移除（个人数据保留）">
                    <span>
                      <IconButton
                        size="small"
                        color="warning"
                        aria-label={`移除成员 ${member.display_name}`}
                        disabled={busyId === member.user_id}
                        onClick={() => void handleRemove(member)}
                      >
                        <DeleteIcon fontSize="small" />
                      </IconButton>
                    </span>
                  </Tooltip>
                ) : null}
              </Stack>
            </Stack>
          );
        })}
      </Stack>

      {!loading && visibleMembers.length === 0 ? (
        <Typography variant="body2" color="text.secondary">
          暂无{includeRemoved ? '' : '在组'}成员。
        </Typography>
      ) : null}
    </Stack>
  );
}
