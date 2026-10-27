/**
 * 任务域类型（对应《A05》与 F02 §4.4、§4.5、§6.4）。
 *
 * 没有 `overdue` 状态：逾期是派生字段 `is_overdue`。
 */

export type TaskStatus = 'not_started' | 'in_progress' | 'completed' | 'cancelled';

/** 0 低 / 1 普通 / 2 高 / 3 紧急 */
export type TaskPriority = 0 | 1 | 2 | 3;

export type TaskMemberRole = 'owner' | 'member';

export interface Task {
  readonly has_children: boolean;
  readonly id: string;
  readonly project_id: string;
  /** 最多 3 层 */
  readonly parent_id: string | null;
  /** 不随持有者转移而变 */
  readonly created_by: string;
  /** 1–200 */
  readonly name: string;
  /** ≤4000；更新时空串存 `""`（与项目不同） */
  readonly description: string | null;
  readonly status: TaskStatus;
  /** 0–100 */
  readonly progress: number;
  readonly priority: TaskPriority;
  readonly start_date: string;
  readonly end_date: string;
  readonly is_milestone: boolean;
  /** 决定能否进组内视图 */
  readonly is_shared: boolean;
  /** 派生 */
  readonly is_overdue: boolean;
  /** 逻辑删除 */
  readonly deleted_at: string | null;
  /** 派生；非任务成员为 null */
  readonly membership_role: TaskMemberRole | null;
  /** 最近一次修改本任务行的用户 */
  readonly updated_by: string;
  /** 版本字段 */
  updated_at: string;
}

export interface CreateTaskBody {
  /** 1–200 */
  name: string;
  /** 建议恒传 */
  start_date?: string;
  end_date?: string;
  /** 空串按"未提供"处理 */
  description?: string | null;
  parent_id?: string | null;
  /** 默认 1 */
  priority?: TaskPriority;
  /** 默认 false */
  is_milestone?: boolean;
  /** 默认 false */
  is_shared?: boolean;
  // 不接受 status / progress（新任务固定 not_started / 0）
}

export interface UpdateTaskBody {
  name?: string;
  /** 空串存 `""`（注意与项目不同） */
  description?: string | null;
  status?: TaskStatus;
  /** 0–100 */
  progress?: number;
  priority?: TaskPriority;
  start_date?: string;
  end_date?: string;
  is_milestone?: boolean;
  is_shared?: boolean;
  /** 省略=不修改；`null`=提升为顶层；给值=换父任务（三态，用 `Controller` 显式表达） */
  parent_id?: string | null;
  readonly expected_updated_at: string;
}

/** 「仅日期」端点：只能有这三项，携带其它字段返回 422 */
export interface UpdateTaskDatesBody {
  start_date: string;
  end_date: string;
  readonly expected_updated_at: string;
}

/** `DELETE /tasks/{id}` 也要求 JSON 请求体 */
export interface DeleteTaskBody {
  readonly expected_updated_at: string;
}

export interface CopyTaskBody {
  /** 省略时用"源名称（副本）" */
  name?: string;
  description?: string | null;
  start_date?: string;
  end_date?: string;
  is_shared?: boolean;
}

export interface TaskQueryBody {
  status?: TaskStatus;
  /** 按任务成员筛选（任意角色） */
  assignee_id?: string;
  /** 按任务持有者筛选 */
  owner_id?: string;
  is_shared?: boolean;
  /** 默认 false */
  include_deleted?: boolean;
  page?: number;
  page_size?: number;
  sort?:
    | 'name'
    | 'status'
    | 'progress'
    | 'priority'
    | 'start_date'
    | 'end_date'
    | 'created_at'
    | 'updated_at';
}

/** 任务成员**没有 `status`**——移除即删行 */
export interface TaskMember {
  readonly id: string;
  readonly task_id: string;
  readonly user_id: string;
  /** 恰好一个 owner */
  readonly role: TaskMemberRole;
  readonly username: string;
  readonly display_name: string;
  readonly avatar_updated_at: string | null;
  readonly email: string;
}

export interface TaskMemberQueryBody {
  role?: TaskMemberRole;
  page?: number;
  page_size?: number;
  sort?: 'username' | 'display_name' | 'role' | 'created_at';
}

export interface AddTaskMemberBody {
  /** 必须是该项目成员 */
  user_id: string;
}

/** `role: owner` = **转移持有者** */
export interface UpdateTaskMemberBody {
  role: TaskMemberRole;
}
