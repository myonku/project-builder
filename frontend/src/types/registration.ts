/**
 * 自助注册申请（对应《A10》/ F02 §6.8）。
 *
 * 申请通过审核之前不是用户：不能登录、不会出现在用户列表；审核通过后
 * 后端在同一事务里创建 User，并把申请标记为 approved。
 */

import type { PaginationQuery } from './common';
import type { User } from './user';

export type RegistrationRequestStatus = 'pending' | 'approved' | 'rejected';

export interface RegistrationRequest {
  readonly id: string;
  readonly username: string;
  readonly email: string;
  readonly display_name: string;
  readonly timezone: string;
  readonly status: RegistrationRequestStatus;
  readonly reviewed_by: string | null;
  readonly reviewed_at: string | null;
  readonly rejection_reason: string | null;
  readonly created_at: string;
  readonly updated_at: string;
}

export interface RegistrationRequestQuery extends PaginationQuery {
  /** 省略时返回全部状态；审核页默认传 pending */
  status?: RegistrationRequestStatus;
}

export interface RegistrationReviewBody {
  /** 只能审核为 approved 或 rejected；pending 不能作为审核结果 */
  status: 'approved' | 'rejected';
  /** 拒绝原因，可省略；通过时忽略 */
  reason?: string;
}

export interface RegistrationReviewResult {
  request: RegistrationRequest;
  /** 审核通过时返回新建的用户 */
  user?: User;
}

/** 注册提交的返回：生产配置为待审核申请；测试配置可能直接返回 User。 */
export type RegisterResponse = RegistrationRequest | User;
