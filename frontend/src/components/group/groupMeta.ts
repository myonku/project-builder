/**
 * 工作组域的展示元数据（`components/group/`，对应《A03》）。
 *
 * 注意：组**没有** `owner` 成员角色——所有者由 `owner_id` 表达，成员行的 role
 * 只有 `member` / `manager`；所有者的成员行通常也是 `manager`，但判定一律看 `owner_id`。
 */

import type { StatusTone } from '@/components/common/StatusTag';
import type {
  GroupMemberRole,
  GroupMembershipStatus,
  GroupStatus,
  JoinRequestStatus,
} from '@/types/group';

export interface GroupMeta {
  label: string;
  tone: StatusTone;
}

const GROUP_STATUS_META: Record<GroupStatus, GroupMeta> = {
  active: { label: '启用', tone: 'success' },
  disabled: { label: '已停用', tone: 'default' },
};

const ROLE_META: Record<GroupMemberRole, GroupMeta> = {
  manager: { label: '组管理员', tone: 'primary' },
  member: { label: '成员', tone: 'default' },
};

const MEMBERSHIP_STATUS_META: Record<GroupMembershipStatus, GroupMeta> = {
  active: { label: '在组', tone: 'success' },
  removed: { label: '已移除', tone: 'default' },
};

const JOIN_STATUS_META: Record<JoinRequestStatus, GroupMeta> = {
  pending: { label: '待审核', tone: 'warning' },
  approved: { label: '已通过', tone: 'success' },
  rejected: { label: '已拒绝', tone: 'default' },
};

export function groupStatusMeta(status: GroupStatus): GroupMeta {
  return GROUP_STATUS_META[status];
}

export function groupMemberRoleMeta(role: GroupMemberRole): GroupMeta {
  return ROLE_META[role];
}

export function groupMembershipStatusMeta(status: GroupMembershipStatus): GroupMeta {
  return MEMBERSHIP_STATUS_META[status];
}

export function joinRequestStatusMeta(status: JoinRequestStatus): GroupMeta {
  return JOIN_STATUS_META[status];
}

export const GROUP_STATUS_OPTIONS = (Object.keys(GROUP_STATUS_META) as GroupStatus[]).map(
  (value) => ({ value, label: GROUP_STATUS_META[value].label }),
);

export const GROUP_MEMBER_ROLE_OPTIONS = (Object.keys(ROLE_META) as GroupMemberRole[]).map(
  (value) => ({ value, label: ROLE_META[value].label }),
);

export const JOIN_REQUEST_STATUS_OPTIONS = (
  Object.keys(JOIN_STATUS_META) as JoinRequestStatus[]
).map((value) => ({ value, label: JOIN_STATUS_META[value].label }));
