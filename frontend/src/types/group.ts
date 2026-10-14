/**
 * 工作组域类型（对应《A03》与 F02 §4.2、§4.5、§4.6、§6.2）。
 *
 * 注意：工作组**没有** `owner` 成员角色——所有者由 `groups.owner_id` 表达，
 * 成员行里的 `role` 只有 `member` / `manager`。
 */

export type GroupStatus = 'active' | 'disabled';
export type GroupMemberRole = 'member' | 'manager';
export type GroupMembershipStatus = 'active' | 'removed';
export type JoinRequestStatus = 'pending' | 'approved' | 'rejected';

export interface Group {
  readonly id: string;
  /** 1–160 */
  readonly name: string;
  /** ≤2000；未设置或已清空为 null */
  readonly description: string | null;
  readonly status: GroupStatus;
  /** 所有者（治理属性，不等同于成员角色） */
  readonly owner_id: string;
  /** 版本字段 */
  updated_at: string;
  /** 派生：非成员为 null */
  readonly membership_role: GroupMemberRole | null;
  /** 派生：非成员为 null */
  readonly membership_status: GroupMembershipStatus | null;
}

export interface CreateGroupBody {
  /** 1–160 */
  name: string;
  /** ≤2000；空/纯空白/null 存 null */
  description?: string | null;
}

export interface UpdateGroupBody {
  name?: string | null;
  /** 出现且为 ""/纯空白/null 表示清空 */
  description?: string | null;
  status?: GroupStatus;
  /** 转移所有权；必须是活跃成员且账号未停用 */
  owner_id?: string;
  readonly expected_updated_at: string;
}

export interface GroupQueryBody {
  status?: GroupStatus;
  page?: number;
  page_size?: number;
  sort?: 'name' | 'status' | 'created_at' | 'updated_at';
}

/** 工作组用**软移除**（`status: 'removed'`），与项目/任务成员不同 */
export interface GroupMember {
  readonly id: string;
  readonly group_id: string;
  readonly user_id: string;
  readonly role: GroupMemberRole;
  readonly status: GroupMembershipStatus;
  readonly joined_at: string;
  readonly username: string;
  readonly display_name: string;
  /** 写响应里为 `""`（《A03》§6/§7）；要展示用户信息请用成员列表或 `GET /users/{id}` */
  readonly email: string;
}

export interface GroupMemberQueryBody {
  role?: GroupMemberRole;
  /** 省略 = 同时返回 active 与 removed */
  status?: GroupMembershipStatus;
  page?: number;
  page_size?: number;
  sort?: 'username' | 'display_name' | 'role' | 'status' | 'joined_at';
}

export interface AddGroupMemberBody {
  user_id: string;
  /** 默认 member */
  role?: GroupMemberRole;
}

/** `role` 与 `status` 至少提供一个；同时提供时**只按 role 生效** */
export interface UpdateGroupMemberBody {
  role?: GroupMemberRole;
  status?: 'removed' | 'active';
}

export interface JoinRequest {
  readonly id: string;
  readonly group_id: string;
  readonly user_id: string;
  readonly status: JoinRequestStatus;
  readonly reviewed_by: string | null;
  readonly reviewed_at: string | null;
  readonly username: string;
  readonly display_name: string;
  readonly email: string;
}

export interface JoinRequestQueryBody {
  status?: JoinRequestStatus;
  page?: number;
  page_size?: number;
  sort?: 'status' | 'created_at' | 'updated_at' | 'username';
}

export interface ReviewJoinRequestBody {
  /** 不能传 pending */
  status: 'approved' | 'rejected';
}
