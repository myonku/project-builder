/**
 * 审计域的展示元数据（`components/audit/`，对应《A09》）。
 *
 * 与 `components/project/projectMeta.ts`、`components/reminder/reminderMeta.ts` 同一套做法：
 * **枚举 → 中文文案 / 颜色只在这里维护一份**，页面与详情抽屉都从这里取，避免两处漂移。
 *
 * 注意：`changes` 的**键名不在这里做中文映射**。契约明确未穷举键集合（《A09》§不确定点），
 * 前端把键名当作服务端字段名原样展示；只有 `action` / `object_type` 是封闭枚举。
 */

import type { StatusTone } from '@/components/common/StatusTag';
import type { AuditAction, AuditObjectType } from '@/types/audit';

export interface AuditActionMeta {
  label: string;
  tone: StatusTone;
}

/** 13 个动作（《A09》§`action` 完整枚举） */
const ACTION_META: Record<AuditAction, AuditActionMeta> = {
  created: { label: '创建', tone: 'success' },
  updated: { label: '修改', tone: 'info' },
  archived: { label: '归档', tone: 'warning' },
  deleted: { label: '删除', tone: 'warning' },
  purged: { label: '彻底删除', tone: 'error' },
  member_added: { label: '成员加入', tone: 'primary' },
  member_role_changed: { label: '成员角色变更', tone: 'secondary' },
  member_removed: { label: '成员移除', tone: 'warning' },
  owner_transferred: { label: '持有者转移', tone: 'warning' },
  join_request_reviewed: { label: '加入申请审核', tone: 'info' },
  shared: { label: '共享级别变更', tone: 'primary' },
  password_reset: { label: '重置密码', tone: 'warning' },
  disabled: { label: '停用账号', tone: 'error' },
};

/** 6 个对象类型（《A09》§`object_type` 完整枚举） */
const OBJECT_TYPE_LABELS: Record<AuditObjectType, string> = {
  user: '用户',
  group: '工作组',
  project: '项目',
  task: '任务',
  attachment: '附件',
  reminder: '提醒',
};

export function auditActionMeta(action: AuditAction): AuditActionMeta {
  return ACTION_META[action];
}

export function auditObjectTypeLabel(objectType: AuditObjectType): string {
  return OBJECT_TYPE_LABELS[objectType];
}

/** 筛选下拉的选项（顺序与《A09》枚举一致） */
export const AUDIT_ACTION_OPTIONS = (Object.keys(ACTION_META) as AuditAction[]).map((value) => ({
  value,
  label: ACTION_META[value].label,
}));

export const AUDIT_OBJECT_TYPE_OPTIONS = (
  Object.keys(OBJECT_TYPE_LABELS) as AuditObjectType[]
).map((value) => ({ value, label: OBJECT_TYPE_LABELS[value] }));

/** `changes` 的键按字典序展示：服务端已经排序，前端再排一次不依赖返回顺序 */
export function auditChangeEntries(changes: Record<string, string>): Array<[string, string]> {
  return Object.entries(changes).sort(([left], [right]) => left.localeCompare(right));
}

/** 把值压成单行，避免 `changes` 里的换行把单元格撑开 */
function compactValue(value: string, maxLength = 40): string {
  const oneLine = value.replace(/\s+/g, ' ').trim();
  return oneLine.length > maxLength ? `${oneLine.slice(0, maxLength - 1)}…` : oneLine;
}

/**
 * 单元格里的一行摘要：`key=value`，最多 3 项；完整内容在 Tooltip / 详情抽屉里。
 * 空对象返回空串，由调用方渲染 `—`。
 */
export function summarizeAuditChanges(changes: Record<string, string>, maxKeys = 3): string {
  const entries = auditChangeEntries(changes);
  if (entries.length === 0) {
    return '';
  }
  const parts = entries
    .slice(0, maxKeys)
    .map(([key, value]) => `${key}=${compactValue(value)}`);
  return entries.length > maxKeys
    ? `${parts.join('，')}…（共 ${entries.length} 项）`
    : parts.join('，');
}
