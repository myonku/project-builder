/**
 * 工作组详情抽屉（`?groupId=`）——《A03》，F05 §7.2 第 9 步。
 *
 * 工作组不是"列表 + 全量数据"：详情、成员、加入申请是三个独立的分页端点，
 * 因此抽屉里按标签页组织：
 * - 概览：`GET /groups/{id}` + 编辑（`useVersionedForm` 管版本协议）；
 * - 成员：`GroupMembersPanel`（软移除 / 恢复 / 角色 / 转移所有权）；
 * - 加入申请：`GroupJoinRequestsPanel`（审核通过/拒绝）。
 *
 * 转移所有权走 `PATCH /groups/{id}` 的 `owner_id`（A03 §4）：同一次请求只执行转移，
 * 其它字段不生效；成功后会刷新组与成员。
 */

import * as React from 'react';
import Alert from '@mui/material/Alert';
import Box from '@mui/material/Box';
import Button from '@mui/material/Button';
import CircularProgress from '@mui/material/CircularProgress';
import Divider from '@mui/material/Divider';
import Drawer from '@mui/material/Drawer';
import IconButton from '@mui/material/IconButton';
import Stack from '@mui/material/Stack';
import Tab from '@mui/material/Tab';
import Tabs from '@mui/material/Tabs';
import Tooltip from '@mui/material/Tooltip';
import Typography from '@mui/material/Typography';
import CloseIcon from '@mui/icons-material/Close';
import EditIcon from '@mui/icons-material/Edit';
import { getGroup, updateGroup } from '@/api/groups';
import type { ApiError } from '@/api/client';
import StatusTag from '@/components/common/StatusTag';
import GroupFormDialog, {
  type GroupFormValues,
} from '@/components/group/GroupFormDialog';
import GroupJoinRequestsPanel from '@/components/group/GroupJoinRequestsPanel';
import GroupMembersPanel from '@/components/group/GroupMembersPanel';
import {
  groupMemberRoleMeta,
  groupMembershipStatusMeta,
  groupStatusMeta,
} from '@/components/group/groupMeta';
import { describeApiError, useApiError } from '@/hooks/useApiError';
import { useNotifications } from '@/hooks/useNotifications';
import { useVersionedForm } from '@/hooks/useVersionedForm';
import { useAuthStore } from '@/stores/auth';
import type { Group, UpdateGroupBody } from '@/types/group';
import { formatForDisplay } from '@/utils/datetime';

export interface GroupDetailDrawerProps {
  /** 打开的组 id；`null` 表示关闭 */
  groupId: string | null;
  onClose: () => void;
  /** 组或成员变化后通知列表刷新（状态/更新时间/成员数） */
  onChanged?: () => void;
}

function InfoRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <Stack direction="row" spacing={2} sx={{ justifyContent: 'space-between' }}>
      <Typography variant="body2" color="text.secondary" sx={{ flexShrink: 0 }}>
        {label}
      </Typography>
      <Box sx={{ minWidth: 0, textAlign: 'right', wordBreak: 'break-word' }}>{children}</Box>
    </Stack>
  );
}

function buildGroupPatch(values: GroupFormValues, group: Group): Omit<UpdateGroupBody, 'expected_updated_at'> {
  const patch: Omit<UpdateGroupBody, 'expected_updated_at'> = {};
  if (values.name !== group.name) {
    patch.name = values.name;
  }
  if (values.description !== (group.description ?? '')) {
    patch.description = values.description;
  }
  if (values.status !== group.status) {
    patch.status = values.status;
  }
  return patch;
}

export default function GroupDetailDrawer({
  groupId,
  onClose,
  onChanged,
}: GroupDetailDrawerProps) {
  const { show } = useNotifications();
  const { handle } = useApiError();
  const userTimeZone = useAuthStore((state) => state.user?.timezone);

  const [tab, setTab] = React.useState<'members' | 'requests'>('members');
  const [editOpen, setEditOpen] = React.useState(false);
  const [editError, setEditError] = React.useState<ApiError | null>(null);

  const {
    entity: group,
    loading,
    saving,
    error,
    save: saveGroup,
    reload: reloadGroup,
  } = useVersionedForm<Group, UpdateGroupBody>({
    load: (signal) => getGroup(groupId as string, signal),
    save: (body) => updateGroup(groupId as string, body),
    autoLoad: groupId !== null,
    conflictTitle: '工作组已被其他人修改',
  });

  React.useEffect(() => {
    setTab('members');
    setEditOpen(false);
    setEditError(null);
  }, [groupId]);

  React.useEffect(() => {
    if (error) {
      handle(error, { silent: true });
    }
  }, [error, handle]);

  const handleEditSubmit = React.useCallback(
    async (values: GroupFormValues) => {
      if (!group) {
        return;
      }
      const patch = buildGroupPatch(values, group);
      if (Object.keys(patch).length === 0) {
        show('没有需要保存的修改。', { severity: 'info' });
        setEditOpen(false);
        return;
      }
      setEditError(null);
      const outcome = await saveGroup(patch);
      if (outcome.ok) {
        // PATCH 的普通更新响应不带调用者的成员关系投影；重新 GET 一次，避免概览
        // 把管理员误显示成“非成员”（A03 §4 备注）。
        await reloadGroup();
        show('工作组已保存。', { severity: 'success' });
        setEditOpen(false);
        onChanged?.();
        return;
      }
      if (outcome.resolution === 'reloaded') {
        show('已刷新为服务端最新数据。', { severity: 'info' });
        setEditOpen(false);
        onChanged?.();
        return;
      }
      setEditError(outcome.error);
      if (outcome.resolution === 'kept') {
        show('版本号已刷新为服务端最新值，可以再次提交。', { severity: 'warning' });
      }
    },
    [group, onChanged, reloadGroup, saveGroup, show],
  );

  const handleTransferOwner = React.useCallback(
    async (userId: string): Promise<boolean> => {
      const outcome = await saveGroup({ owner_id: userId });
      if (outcome.ok) {
        // 转移响应附带的是新所有者的成员投影，不是调用者的；必须重新 GET。
        await reloadGroup();
        onChanged?.();
        return true;
      }
      if (outcome.resolution === 'reloaded') {
        show('组已被其他人修改，已刷新，请重新确认转移。', { severity: 'warning' });
        onChanged?.();
        return false;
      }
      if (outcome.resolution === 'kept') {
        show('版本号已刷新，请再次确认转移。', { severity: 'warning' });
        return false;
      }
      const apiError = outcome.error;
      const detail = apiError.fields.owner_id ?? apiError.fields.status;
      if (apiError.status === 409) {
        show(detail ?? describeApiError(apiError), { severity: 'warning' });
      } else {
        handle(apiError);
      }
      return false;
    },
    [handle, onChanged, reloadGroup, saveGroup, show],
  );

  const statusMeta = groupStatusMeta(group?.status ?? 'active');

  return (
    <Drawer
      anchor="right"
      open={groupId !== null}
      onClose={onClose}
      slotProps={{
        paper: {
          sx: {
            width: { xs: '100%', sm: 760 },
            // 与任务/审计/附件抽屉一致：让出 absolute AppBar 的高度
            top: { xs: 56, sm: 64 },
            height: { xs: 'calc(100% - 56px)', sm: 'calc(100% - 64px)' },
          },
        },
      }}
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', height: '100%' }}>
        <Stack
          direction="row"
          spacing={1}
          sx={{ alignItems: 'center', justifyContent: 'space-between', px: 2, py: 1.5 }}
        >
          <Box sx={{ minWidth: 0 }}>
            <Typography variant="h6" component="h2" noWrap>
              {group?.name ?? '工作组详情'}
            </Typography>
            {group ? (
              <Stack direction="row" spacing={1} sx={{ alignItems: 'center', mt: 0.5 }}>
                <StatusTag label={statusMeta.label} tone={statusMeta.tone} />
                <Typography variant="caption" color="text.secondary">
                  更新于 {formatForDisplay(group.updated_at, userTimeZone)}
                </Typography>
              </Stack>
            ) : null}
          </Box>
          <Stack direction="row" spacing={1} sx={{ alignItems: 'center' }}>
            <Tooltip title={group ? '编辑工作组' : ''}>
              <span>
                <Button
                  size="small"
                  variant="outlined"
                  startIcon={<EditIcon />}
                  disabled={!group}
                  onClick={() => {
                    setEditError(null);
                    setEditOpen(true);
                  }}
                >
                  编辑
                </Button>
              </span>
            </Tooltip>
            <Tooltip title="关闭">
              <IconButton onClick={onClose} aria-label="关闭工作组详情">
                <CloseIcon />
              </IconButton>
            </Tooltip>
          </Stack>
        </Stack>
        <Divider />

        <Box sx={{ flex: 1, overflow: 'auto', px: 2, py: 2 }}>
          {loading && !group ? (
            <Stack sx={{ alignItems: 'center', py: 6 }}>
              <CircularProgress />
            </Stack>
          ) : null}

          {error ? (
            <Alert severity={error.isNotFound ? 'info' : 'error'} sx={{ mb: 2 }}>
              {describeApiError(error)}
            </Alert>
          ) : null}

          {group ? (
            <Stack spacing={2}>
              <Stack spacing={1}>
                {group.description ? (
                  <Typography variant="body2" sx={{ whiteSpace: 'pre-wrap' }}>
                    {group.description}
                  </Typography>
                ) : (
                  <Typography variant="body2" color="text.secondary">
                    暂无说明。
                  </Typography>
                )}
                <InfoRow label="所有者">
                  <Tooltip title={group.owner_id}>
                    <Typography variant="caption" sx={{ fontFamily: 'monospace' }}>
                      {group.owner_id.slice(-8)}
                    </Typography>
                  </Tooltip>
                </InfoRow>
                <InfoRow label="成员关系">
                  <Typography variant="body2" color="text.secondary">
                    {group.membership_role && group.membership_status
                      ? `${groupMemberRoleMeta(group.membership_role).label} / ${
                          groupMembershipStatusMeta(group.membership_status).label
                        }`
                      : '非成员（系统管理员可见）'}
                  </Typography>
                </InfoRow>
              </Stack>

              <Divider />
              <Tabs
                value={tab}
                onChange={(_event, value: 'members' | 'requests') => setTab(value)}
                variant="fullWidth"
              >
                <Tab value="members" label="成员" />
                <Tab value="requests" label="加入申请" />
              </Tabs>

              {tab === 'members' ? (
                <GroupMembersPanel
                  groupId={group.id}
                  ownerId={group.owner_id}
                  onChanged={onChanged}
                  onTransferOwner={handleTransferOwner}
                />
              ) : (
                <GroupJoinRequestsPanel groupId={group.id} onChanged={onChanged} />
              )}
            </Stack>
          ) : null}
        </Box>
      </Box>

      <GroupFormDialog
        open={editOpen}
        mode="edit"
        group={group}
        submitting={saving}
        serverError={editError}
        onClose={() => setEditOpen(false)}
        onSubmit={(values) => void handleEditSubmit(values)}
      />
    </Drawer>
  );
}
