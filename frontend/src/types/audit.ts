/**
 * 审计域类型（对应《A09》与 F02 §4.9）。
 *
 * 审计记录**与业务写在同一个事务里**；`changes` 已由服务端扁平化、脱敏与截断。
 */

export type AuditObjectType =
  | 'user'
  | 'group'
  | 'project'
  | 'task'
  | 'attachment'
  | 'reminder';

/** 白名单 13 个动作（《S02》§7 变更记录：由 16 个收敛而来） */
export type AuditAction =
  | 'created'
  | 'updated'
  | 'archived'
  | 'deleted'
  | 'purged'
  | 'member_added'
  | 'member_role_changed'
  | 'member_removed'
  | 'owner_transferred'
  | 'join_request_reviewed'
  | 'shared'
  | 'password_reset'
  | 'disabled';

export interface AuditRecord {
  readonly id: string;
  readonly actor_id: string;
  /** 操作人**当前**显示名（不是快照） */
  readonly actor_name: string;
  readonly action: AuditAction;
  readonly object_type: AuditObjectType;
  readonly object_id: string;
  /** 扁平；敏感键值为 `"[redacted]"`；截断 200 字符；字典序前 20 键 */
  readonly changes: Record<string, string>;
  /** 当初产生这条记录的那次请求的 id */
  readonly request_id: string;
  readonly created_at: string;
}

/** 走查询串 */
export interface AuditQuery {
  object_type?: AuditObjectType;
  /** 合法 UUID */
  object_id?: string;
  /** 合法 UUID */
  actor_id?: string;
  action?: AuditAction;
  /** RFC3339；两端都含 */
  from?: string;
  /** 同上；早于 `from` 返回 400 invalid_date_range */
  to?: string;
  page?: number;
  page_size?: number;
  /** 排序表达式：字段白名单 `created_at` / `action` / `object_type`；`-` 前缀降序，可逗号分隔 */
  sort?: string;
}
