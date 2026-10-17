/**
 * 用户域的展示元数据（`components/user/`，对应《A02》）。
 *
 * 用户对象从 A02 起就是公开结构；这里只维护**枚举 → 中文文案/颜色**，
 * 不包含 password / hash 等任何敏感字段。
 */

import type { StatusTone } from '@/components/common/StatusTag';
import type { SystemRole, UserStatus } from '@/types/user';

export interface UserMeta {
  label: string;
  tone: StatusTone;
}

const STATUS_META: Record<UserStatus, UserMeta> = {
  active: { label: '启用', tone: 'success' },
  disabled: { label: '已停用', tone: 'default' },
};

const ROLE_META: Record<SystemRole, UserMeta> = {
  admin: { label: '系统管理员', tone: 'primary' },
  member: { label: '普通成员', tone: 'default' },
};

export function userStatusMeta(status: UserStatus): UserMeta {
  return STATUS_META[status];
}

export function systemRoleMeta(role: SystemRole): UserMeta {
  return ROLE_META[role];
}

export const USER_STATUS_OPTIONS = (Object.keys(STATUS_META) as UserStatus[]).map((value) => ({
  value,
  label: STATUS_META[value].label,
}));

export const SYSTEM_ROLE_OPTIONS = (Object.keys(ROLE_META) as SystemRole[]).map((value) => ({
  value,
  label: ROLE_META[value].label,
}));
