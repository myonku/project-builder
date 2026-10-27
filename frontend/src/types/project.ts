/**
 * 项目域类型（对应《A04》与 F02 §4.3、§4.5、§6.3）。
 *
 * 刻意不返回（前端**不要**定义）：`created_at`、`is_archived`、`is_read_only`、
 * `my_role`、`owner_id`、`task_count`——它们只出现在计划视图的投影里（见 `./plan.ts`）。
 */

export type ProjectStatus =
  | 'not_started'
  | 'in_progress'
  | 'paused'
  | 'completed'
  | 'archived'
  | 'cancelled';

export type ProjectVisibility = 'private' | 'group_readonly' | 'group_editable';

export type ProjectMemberRole = 'owner' | 'member';

export interface Project {
  readonly has_tasks: boolean;
  readonly id: string;
  /** 1–200 */
  readonly name: string;
  /** 必须且只能归属一个组 */
  readonly group_id: string;
  /** ≤4000 */
  readonly description: string | null;
  /** ≠ 持有者（持有者由 members 里 `role='owner'` 决定） */
  readonly created_by: string;
  readonly status: ProjectStatus;
  readonly visibility: ProjectVisibility;
  /** `YYYY-MM-DD` 自然日，**不要用 `Date` 对象** */
  readonly start_date: string;
  readonly end_date: string;
  /** ≤32，自由文本（如 `#336699`） */
  readonly color: string | null;
  /** 非空即视为归档（与 `status` 一起判定） */
  readonly archived_at: string | null;
  /** 派生：有效任务进度简单平均 */
  readonly progress: number;
  /** 派生：按请求者时区算"今天" */
  readonly overdue_task_count: number;
  /** 派生：非成员为 null */
  readonly membership_role: ProjectMemberRole | null;
  /** 版本字段 */
  updated_at: string;
}

export interface CreateProjectBody {
  /** 1–200 */
  name: string;
  group_id: string;
  /** 契约未规定省略行为，建议恒传 */
  start_date?: string;
  end_date?: string;
  description?: string | null;
  /** 默认 private */
  visibility?: ProjectVisibility;
  color?: string | null;
}

export interface UpdateProjectBody {
  progress?: number;
  name?: string;
  /** 空串表示清空（存 null） */
  description?: string | null;
  status?: ProjectStatus;
  visibility?: ProjectVisibility;
  start_date?: string;
  end_date?: string;
  /** 空串表示清空 */
  color?: string | null;
  readonly expected_updated_at: string;
}

export interface ArchiveProjectBody {
  readonly expected_updated_at: string;
}

export interface ProjectQueryBody {
  group_id?: string;
  status?: ProjectStatus;
  owner_id?: string;
  /** 包含匹配，大小写不敏感 */
  name?: string;
  /** 默认 false；归档项目默认排除 */
  include_archived?: boolean;
  page?: number;
  page_size?: number;
  sort?:
    | 'name'
    | 'status'
    | 'visibility'
    | 'start_date'
    | 'end_date'
    | 'created_at'
    | 'updated_at';
}

/** 项目成员**没有 `status`**——移除即删行 */
export interface ProjectMember {
  readonly id: string;
  readonly project_id: string;
  readonly user_id: string;
  /** 恰好一个 owner */
  readonly role: ProjectMemberRole;
  readonly username: string;
  readonly display_name: string;
  readonly avatar_updated_at: string | null;
  readonly email: string;
}

export interface ProjectMemberQueryBody {
  role?: ProjectMemberRole;
  page?: number;
  page_size?: number;
  sort?: 'username' | 'display_name' | 'role' | 'created_at';
}

export interface AddProjectMemberBody {
  user_id: string;
}

/** `role: owner` = **转移持有者**（同事务先降后升），不是"新增第二个持有者" */
export interface UpdateProjectMemberBody {
  role: ProjectMemberRole;
}
