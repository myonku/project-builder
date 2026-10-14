/**
 * 用户域类型（对应《A02》与 F02 §4.1、§6.1）。
 *
 * **永不返回** `password_hash` 与会话令牌：本文件里不出现任何 password 字段
 * （`ResetPasswordBody.new_password` 是唯一例外，见 §6.1）。
 */

export type UserStatus = 'active' | 'disabled';
export type SystemRole = 'member' | 'admin';

export interface User {
  /** UUIDv7；客户端不应解析其内容 */
  id: string;
  /** 保存为小写 */
  username: string;
  /** 保存为小写 */
  email: string;
  display_name: string;
  status: UserStatus;
  system_role: SystemRole;
  /** IANA 时区名，例如 `Asia/Shanghai` */
  timezone: string;
  reminder_enabled: boolean;
  last_login_at: string | null;
  /** 版本字段：**原样**回传作为 `expected_updated_at`（F01 §5.1） */
  updated_at: string;
}

export interface UpdateUserBody {
  display_name?: string;
  email?: string;
  /** 空串返回 422；省略或 `UTC` 表示不改 */
  timezone?: string;
  reminder_enabled?: boolean;
  /** 仅系统管理员 */
  status?: UserStatus;
  /** 仅系统管理员；创建用户的端点不接受该字段 */
  system_role?: SystemRole;
  /** 由 `useVersionedForm` 注入，表单里不渲染 */
  readonly expected_updated_at: string;
}

export interface DisableUserBody {
  readonly expected_updated_at: string;
}

export interface ResetPasswordBody {
  /** 字段名是 `new_password`，不是 `password` */
  new_password: string;
  readonly expected_updated_at: string;
}

export interface UserQueryBody {
  /** 默认 false；为真时列表并入已停用账号 */
  include_disabled?: boolean;
  page?: number;
  page_size?: number;
  sort?:
    | 'username'
    | 'display_name'
    | 'email'
    | 'status'
    | 'system_role'
    | 'created_at'
    | 'updated_at'
    | 'last_login_at';
}
