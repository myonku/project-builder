/**
 * 项目域的**展示元数据**：状态 / 可见性 / 成员角色的中文文案与标签颜色。
 *
 * 这里只放"怎么显示"，不参与任何权限判定（F02 §8：`membership_role` 只用于展示，
 * 能不能改要看 `usePermission` 的结论）。枚举取值以《A04》与 F02 §4.3 为准，
 * 出现未知值时降级为原样显示而不是崩溃。
 */

import type { StatusTone } from '@/components/common/StatusTag';
import type {
  ProjectMemberRole,
  ProjectStatus,
  ProjectVisibility,
} from '@/types/project';

export interface StatusMeta {
  label: string;
  tone: StatusTone;
}

const PROJECT_STATUS_META: Record<ProjectStatus, StatusMeta> = {
  not_started: { label: '未开始', tone: 'default' },
  in_progress: { label: '进行中', tone: 'primary' },
  paused: { label: '已暂停', tone: 'warning' },
  completed: { label: '已完成', tone: 'success' },
  archived: { label: '已归档', tone: 'default' },
  cancelled: { label: '已取消', tone: 'error' },
};

const PROJECT_VISIBILITY_META: Record<ProjectVisibility, StatusMeta> = {
  private: { label: '私有', tone: 'default' },
  group_readonly: { label: '组内只读', tone: 'info' },
  group_editable: { label: '组内可编辑', tone: 'primary' },
};

const PROJECT_MEMBERSHIP_META: Record<ProjectMemberRole, StatusMeta> = {
  owner: { label: '持有者', tone: 'primary' },
  member: { label: '成员', tone: 'default' },
};

const UNKNOWN: StatusMeta = { label: '未知', tone: 'default' };

export function projectStatusMeta(
  status: ProjectStatus | string | null | undefined,
): StatusMeta {
  if (!status) {
    return UNKNOWN;
  }
  return PROJECT_STATUS_META[status as ProjectStatus] ?? { label: String(status), tone: 'default' };
}

export function projectVisibilityMeta(
  visibility: ProjectVisibility | string | null | undefined,
): StatusMeta {
  if (!visibility) {
    return UNKNOWN;
  }
  return (
    PROJECT_VISIBILITY_META[visibility as ProjectVisibility] ?? {
      label: String(visibility),
      tone: 'default',
    }
  );
}

/** 非成员时为 `null`（派生字段），此时不显示标签 */
export function projectMembershipMeta(
  role: ProjectMemberRole | null | undefined,
): StatusMeta | null {
  if (!role) {
    return null;
  }
  return PROJECT_MEMBERSHIP_META[role] ?? UNKNOWN;
}

/** 状态下拉框的选项（顺序按项目生命周期排列） */
export const PROJECT_STATUS_OPTIONS: { value: ProjectStatus; label: string }[] = (
  Object.keys(PROJECT_STATUS_META) as ProjectStatus[]
).map((value) => ({ value, label: PROJECT_STATUS_META[value].label }));
