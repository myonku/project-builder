# A06 Plan Views 接口详解

> 归属：**前后端共用**（`docs/api/`）。通用约定（基础路径 `/api/v1`、成功/错误信封、认证、分页、并发与幂等、限流、未知字段拒绝）见《S02-HTTP接口契约》§2，本文不重复。
> 契约正文（约定性叙述）见《A06》；本文补齐每个接口的**请求与响应数据结构**。

> **2026-10-21 W4 已落地**：三个计划视图的任务成员对象已增加 `avatar_updated_at`；
> 这是列表与甘特图批量显示头像的字段，避免逐用户 N+1 请求。见 [P00](../P00-项目统筹与实施总览.md)。

## 接口清单

| # | 方法 | 路径 | 权限 | 说明 |
| --- | --- | --- | --- | --- |
| 1 | `GET` | `/api/v1/plan-views/personal` | 登录用户 | 个人计划：我是**项目成员**的项目及其任务 |
| 2 | `GET` | `/api/v1/plan-views/groups/{group_id}` | 本组活跃成员 / 系统管理员 | 组内共享计划：本组已共享的项目与**已共享**任务 |
| 3 | `GET` | `/api/v1/plan-views/projects/{project_id}/gantt` | 对项目至少有读权限（项目成员 / 组内共享可见 / 系统管理员） | 项目甘特图：层级、日期、状态、进度、成员、里程碑与 `can_edit` |

**三个视图的差异一览（先看这张表，再按需读对应小节）**：

| 视图 | `data` 顶层字段 | 逐任务 `can_edit` | 顶层 `can_manage_project` / `can_edit_tasks` | `data.group_id` | 查询参数 | 分页 |
| --- | --- | --- | --- | --- | --- | --- |
| 个人计划 | `group_id`、`projects`、`tasks`、`summary` | 有 | 无 | 恒为 `null` | 支持 11 个（见 §1） | **无** |
| 组内共享计划 | 同上（同形） | 有 | 无 | 目标工作组 UUID | 与个人计划完全相同 | **无** |
| 项目甘特图 | `project`、`can_manage_project`、`can_edit_tasks`、`tasks`、`summary` | 有 | **有** | 没有顶层字段；项目投影内含 `group_id` | **不接受任何查询参数**（携带即 `400`） | **无** |

**三个端点的共同点**：

- 都是 `GET`、都是**只读**：没有写路径、不写审计、不参与 `expected_updated_at` 版本协议、不需要 `Idempotency-Key`。
- **都不分页**：响应里没有 `pagination`，数组是完整的（甘特图必须一次拿到全部条目）。数据量大时响应会比较大；需要分页时应先改契约（《A06》）。
- 全部要求登录；未登录一律 `401 authentication_required`。`429 rate_limited`（限流，带 `Retry-After`）与 `500 internal_error` 适用于全部端点（《S02》§2.4、§2.8）。
- 逻辑删除的任务**一律不出现**；归档项目默认排除，`include_archived=true` 时项目**与其任务同时**出现。
- 派生字段（`progress`、`overdue_task_count`、`is_overdue`、`can_edit`）复用与写接口相同的领域规则；「今天」按**请求者时区**计算（《A02》 末段）。
- 计划视图的项目 / 任务投影与 [README.md](README.md) "公共资源表示"里的项目 / 任务表示**不完全相同**：不含 `created_by`、`created_at`、`deleted_at`，并额外带 `my_role`、`task_count`、`can_edit`、`owner`、`updated_by` / `updated_by_name` 等（逐字段见 §1 的字段表）。

## 1. `GET /api/v1/plan-views/personal`

**用途**：一次拿到"我是项目成员的项目"及其任务，构成个人计划视图。
**权限**：登录用户。范围**只按项目成员关系**判定，不额外按 `created_by` 放宽（否则会出现"把项目移交出去并被移出成员后仍能看到它"的越权读取）。

**路径参数**（无则写“无”）：无。

**查询参数**：

| 参数 | 类型 | 默认 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `project_id` | uuid | 无 | 合法 UUID | 限定单个项目；**同时作用于项目与任务两侧** |
| `project_owner_id` | uuid | 无 | 合法 UUID | 按**项目**持有者筛选 |
| `task_owner_id` | uuid | 无 | 合法 UUID | 按**任务**持有者筛选 |
| `task_member_id` | uuid | 无 | 合法 UUID | 按**任务成员**筛选（含持有者） |
| `project_status` | enum | 无 | `not_started` / `in_progress` / `paused` / `completed` / `archived` / `cancelled` | 项目状态筛选；**同时作用于任务侧**（任务的可见性受其项目约束） |
| `task_status` | enum | 无 | `not_started` / `in_progress` / `completed` / `cancelled` | 任务状态筛选 |
| `priority` | integer | 无 | `0`–`3`（`0` 低 / `1` 普通 / `2` 高 / `3` 紧急） | 任务优先级筛选 |
| `due_from` | date | 无 | `YYYY-MM-DD` | 按**任务结束日期**筛选的下界（**含端点**） |
| `due_to` | date | 无 | `YYYY-MM-DD` | 按**任务结束日期**筛选的上界（**含端点**）；早于 `due_from` 返回 `400 invalid_date_range` |
| `overdue` | boolean | 无 | `true` / `false` | 为真时只返回逾期任务（`end_date < 今天` 且状态为 `not_started` / `in_progress`）；**`false` 等价于不筛选**（不会把非逾期任务筛出来） |
| `include_archived` | boolean | `false` | `true` / `false` | 为真时把归档项目**及其任务**一并纳入 |

> **参数命名刻意消歧**：`project_*` 前缀指项目，`task_*` 前缀指任务；不使用含义模糊的 `owner_id` / `status`。
> **未知查询参数一律拒绝**（`400 invalid_request`，`fields` 中指出参数名并给出可用参数清单）。

**请求体**（无则写“无请求体”）：无请求体（三个端点都是 `GET`）。

**响应 `200`**：

```json
{
  "data": {
    "group_id": null,
    "projects": [
      {
        "id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d20",
        "name": "Migration",
        "description": null,
        "group_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0e",
        "status": "not_started",
        "visibility": "private",
        "start_date": "2026-10-01",
        "end_date": "2026-10-31",
        "color": "#336699",
        "archived_at": null,
        "is_archived": false,
        "is_read_only": false,
        "owner_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0f",
        "my_role": "owner",
        "task_count": 1,
        "progress": 40,
        "overdue_task_count": 1,
        "updated_at": "2026-10-11T04:30:54.047739Z"
      }
    ],
    "tasks": [
      {
        "id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d21",
        "project_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d20",
        "parent_id": null,
        "name": "Prepare migration",
        "description": null,
        "status": "in_progress",
        "progress": 40,
        "priority": 1,
        "start_date": "2026-10-01",
        "end_date": "2026-10-05",
        "is_milestone": false,
        "is_shared": true,
        "is_overdue": true,
        "can_edit": true,
        "owner": {
          "user_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0f",
          "username": "alice",
          "display_name": "Alice",
          "role": "owner"
        },
        "members": [
          {
            "user_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0f",
            "username": "alice",
            "display_name": "Alice",
            "role": "owner"
          }
        ],
        "updated_by": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0f",
        "updated_by_name": "Alice",
        "updated_at": "2026-10-11T04:30:54.047739Z"
      }
    ],
    "summary": {
      "project_count": 1,
      "task_count": 1,
      "in_progress_task_count": 1,
      "overdue_task_count": 1,
      "overall_progress": 40
    }
  }
}
```

顶层字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `data.group_id` | string \| null | 个人视图**恒为 `null`**（组内视图为工作组 UUID） |
| `data.projects` | 项目投影数组 | 空结果为 `[]`；排序固定为 `end_date` 升序、同值按 `id` 升序 |
| `data.tasks` | 任务投影数组 | 空结果为 `[]`；排序固定为 `start_date`、`end_date`、`id` 升序（客户端据此直接渲染甘特行，再用 `parent_id` 组装层级） |
| `data.summary` | object | 汇总统计，与上面两个数组**由同一个数据集算出** |

`data.projects[]`（项目投影）逐字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | UUID | 项目 id |
| `name` | string | 项目名称 |
| `description` | string \| null | 项目说明 |
| `group_id` | UUID | 归属工作组 |
| `status` | string | 项目状态枚举（同"公共资源表示"的项目） |
| `visibility` | string | `private` / `group_readonly` / `group_editable` |
| `start_date` / `end_date` | string | 自然日 `YYYY-MM-DD` |
| `color` | string \| null | 展示色 |
| `archived_at` | string \| null | 归档时刻（UTC ISO 8601）；未归档为 `null` |
| `is_archived` | boolean | 派生：是否已归档（状态为 `archived` 或 `archived_at` 非空） |
| `is_read_only` | boolean | 派生：归档项目只读；**归档时其下任务一律 `can_edit=false`** |
| `owner_id` | UUID \| null | 项目持有者（正常情况下非空） |
| `my_role` | string \| null | 当前用户在该项目中的成员角色：`owner` / `member`；**`null` 表示不是项目成员**（只可能通过组内共享看到它） |
| `task_count` | integer | **本视图内可见**的任务数 |
| `progress` | integer | 派生：本视图内可见任务的进度简单平均值（已取消任务不参与；无有效任务时为 `0`） |
| `overdue_task_count` | integer | 派生：本视图内可见任务中已逾期的条数 |
| `updated_at` | string | 项目最后变更时刻（UTC ISO 8601，带亚秒精度） |

`data.tasks[]`（任务投影）逐字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | UUID | 任务 id |
| `project_id` | UUID | 所属项目（一定也在 `projects` 里） |
| `parent_id` | UUID \| null | 父任务；`null` 为顶层任务（层级最多 3 层，见《A05》） |
| `name` | string | 任务名称 |
| `description` | string \| null | 任务说明 |
| `status` | string | `not_started` / `in_progress` / `completed` / `cancelled`（**没有 `overdue`**：逾期是派生字段） |
| `progress` | integer | 0–100 |
| `priority` | integer | `0` 低 / `1` 普通 / `2` 高 / `3` 紧急 |
| `start_date` / `end_date` | string | 自然日 `YYYY-MM-DD` |
| `is_milestone` | boolean | 是否里程碑 |
| `is_shared` | boolean | 是否已共享给工作组（决定它能否进入组内视图） |
| `is_overdue` | boolean | 派生：`end_date < 今天` 且状态为 `not_started` / `in_progress`；「今天」按**请求者时区**计算 |
| `can_edit` | boolean | 派生：当前用户能否修改该任务（归档项目恒为 `false`；组内只读共享同样为 `false`）。**客户端据此决定是否允许拖拽改日期，但服务端写入口仍会独立校验一次** |
| `owner` | 任务成员对象 \| null | 任务持有者；没有持有者时为 `null` |
| `members` | 任务成员对象数组 | 全部成员（含持有者）；**已按"持有者优先、显示名"排序**，可直接用于"按成员分组"展示（服务端不提供 `group_by` 参数） |
| `updated_by` | UUID | 最后修改人的用户 id（需求文档 5.6） |
| `updated_by_name` | string \| null | 最后修改人的显示名（服务端一次 JOIN 取出，客户端不必逐行再查用户） |
| `updated_at` | string | 最后修改时刻（UTC ISO 8601，带亚秒精度） |

任务成员对象（`owner` 与 `members[]` 的元素）：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `user_id` | UUID | 用户 id |
| `username` | string | 用户名 |
| `display_name` | string | 显示名 |
| `avatar_updated_at` | string \| null | 头像版本；`null` 表示无头像（W4） |
| `role` | string | 该用户在**任务**中的角色：`owner` / `member` |

`data.summary` 逐字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `project_count` | integer | 视图内项目数（恒等于 `projects` 的长度） |
| `task_count` | integer | 视图内任务数（恒等于 `tasks` 的长度） |
| `in_progress_task_count` | integer | 状态为 `in_progress` 的任务数 |
| `overdue_task_count` | integer | 逾期任务数（与逐任务的 `is_overdue` 同源） |
| `overall_progress` | integer | 全部可见任务进度的简单平均值（已取消任务不参与；无有效任务时为 `0`） |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `401` | `authentication_required` | 未登录或会话失效 |
| `400` | `invalid_request` | 未知查询参数（`fields.<参数名>`，并给出可用清单）；非法 UUID；非法状态枚举；非法优先级；非法布尔值；非法日期格式（不是 `YYYY-MM-DD`） |
| `400` | `invalid_date_range` | `due_from` 晚于 `due_to`（`fields.due_from`、`fields.due_to`） |

**备注**：

- **不存在 `404`**：不可见的项目/任务只是不出现在结果里（`project_id` 指向别组项目时得到空数组，而不是错误）。
- 「今天」取不到用户时区时按 UTC 日历日回退（该值只影响派生展示字段与 `overdue` 筛选，不会让整个视图失败）。
- 筛选是"机械缩小范围"，逐对象的可见性仍由权限判定收敛，因此不必担心筛选参数被当作越权手段。

## 2. `GET /api/v1/plan-views/groups/{group_id}`

**用途**：组内共享计划——本组**已共享**的项目，以及这些项目下**已共享**的任务。
**权限**：该组的**活跃成员**或系统管理员；其它情况返回 `404 group_not_found`（与"不存在"同码，不用状态码差异泄漏组是否存在）。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `group_id` | UUID | 工作组 id；不是合法 UUID 返回 `400 invalid_request`（`fields.group_id`），不交给数据库 |

**查询参数**：与 §1 的 11 个参数**完全相同**（同一套白名单与同一套校验），差别只在作用范围：

| 参数 | 类型 | 默认 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `project_id` | uuid | 无 | 合法 UUID | 限定单个项目；同时作用于项目与任务两侧 |
| `project_owner_id` | uuid | 无 | 合法 UUID | 按项目持有者筛选 |
| `task_owner_id` | uuid | 无 | 合法 UUID | 按任务持有者筛选 |
| `task_member_id` | uuid | 无 | 合法 UUID | 按任务成员筛选（含持有者） |
| `project_status` | enum | 无 | 项目状态枚举 | 项目状态筛选（同时作用于任务侧） |
| `task_status` | enum | 无 | 任务状态枚举 | 任务状态筛选 |
| `priority` | integer | 无 | `0`–`3` | 任务优先级筛选 |
| `due_from` / `due_to` | date | 无 | `YYYY-MM-DD`，含端点 | 按任务结束日期筛选；倒置返回 `400 invalid_date_range` |
| `overdue` | boolean | 无 | `true` / `false` | 为真时只返回逾期任务；`false` 等价于不筛选 |
| `include_archived` | boolean | `false` | `true` / `false` | 纳入归档项目及其任务 |

**请求体**（无则写“无请求体”）：无请求体。

**响应 `200`**：与 §1 **同形**，只有 `group_id` 非空以及内容范围不同：

```json
{
  "data": {
    "group_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0e",
    "projects": [
      {
        "id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d20",
        "name": "Migration",
        "description": null,
        "group_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0e",
        "status": "in_progress",
        "visibility": "group_readonly",
        "start_date": "2026-10-01",
        "end_date": "2026-10-31",
        "color": "#336699",
        "archived_at": null,
        "is_archived": false,
        "is_read_only": false,
        "owner_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0f",
        "my_role": null,
        "task_count": 1,
        "progress": 40,
        "overdue_task_count": 1,
        "updated_at": "2026-10-11T04:30:54.047739Z"
      }
    ],
    "tasks": [
      {
        "id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d21",
        "project_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d20",
        "parent_id": null,
        "name": "Prepare migration",
        "description": null,
        "status": "in_progress",
        "progress": 40,
        "priority": 1,
        "start_date": "2026-10-01",
        "end_date": "2026-10-05",
        "is_milestone": false,
        "is_shared": true,
        "is_overdue": true,
        "can_edit": false,
        "owner": {
          "user_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0f",
          "username": "alice",
          "display_name": "Alice",
          "role": "owner"
        },
        "members": [
          {
            "user_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0f",
            "username": "alice",
            "display_name": "Alice",
            "role": "owner"
          }
        ],
        "updated_by": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0f",
        "updated_by_name": "Alice",
        "updated_at": "2026-10-11T04:30:54.047739Z"
      }
    ],
    "summary": {
      "project_count": 1,
      "task_count": 1,
      "in_progress_task_count": 1,
      "overdue_task_count": 1,
      "overall_progress": 40
    }
  }
}
```

与个人视图的**逐字段差异**（其余字段与 §1 的字段表完全一致，不重复）：

| 字段 | 组内视图的取值规则 |
| --- | --- |
| `data.group_id` | 目标工作组 UUID（不为 `null`） |
| `data.projects` | **只含已共享的项目**：`visibility` 必须是 `group_readonly` 或 `group_editable`；`private` 项目即使属于该组也不出现 |
| `data.projects[].my_role` | 我在该项目中的角色；**查看者不是项目成员（组内访客）时为 `null`** |
| `data.projects[].task_count` | 基于**组内视图可见**的任务（可能小于 `GET /api/v1/projects/{project_id}` 的口径） |
| `data.projects[].progress` / `overdue_task_count` | 同上，**基于组内视图可见任务**计算——这是刻意的：若按项目全部任务算，就会用进度数值泄漏未共享任务的存在与进展（《A06》） |
| `data.tasks` | **只含已共享任务**（`is_shared = true`）。"项目已共享但任务未共享时任务不得出现"，**该限制与查看者是不是项目成员无关** |
| `data.tasks[].can_edit` | 按逐任务权限：组内只读（`group_readonly`）→ `false`；组内可编辑（`group_editable`）且任务已共享 → `true`；**项目成员在组内页面同样只能编辑已共享任务** |
| `data.summary` | `project_count` / `task_count` 恒等于对应数组长度；`task_count` 也只统计已共享任务 |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `401` | `authentication_required` | 未登录或会话失效 |
| `400` | `invalid_request` | `group_id` 不是合法 UUID；未知查询参数；非法 UUID / 状态 / 优先级 / 布尔值 / 日期格式 |
| `400` | `invalid_date_range` | `due_from` 晚于 `due_to` |
| `404` | `group_not_found` | 工作组不存在，或请求者不是该组的活跃成员（系统管理员不受此限） |

**备注**：

- **系统管理员可以查看任意组的共享计划**（"处理异常数据"需要这条只读路径）；普通组员只能看自己所属组。
- **被移出工作组后立即失去访问**：成员关系置为 `removed` 之后再请求本端点返回 `404`。
- "组内可见"不等于"可编辑"：只读共享项目下所有任务的 `can_edit` 都是 `false`，且**不能**通过本视图修改项目共享级别（项目共享级别永远属于项目管理权限，见《A04》、§4.7）。
- 未共享的私有项目与未共享任务不进入本视图，因此组内访客**看不到它们的存在**，包括通过 `progress` / `task_count` 反推。

## 3. `GET /api/v1/plan-views/projects/{project_id}/gantt`

**用途**：一个项目的"完整画面"：项目本身、全部可见任务的层级信息（`parent_id`）、日期、状态、进度、成员、里程碑，以及**当前用户能否编辑**。
**权限**：对项目至少有读权限——项目成员、或通过组内共享可见（活跃组员 + `group_readonly` / `group_editable`）、或系统管理员；其它情况返回 `404 project_not_found`（与"不存在"同码，不泄漏存在性）。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `project_id` | UUID | 项目 id；不是合法 UUID 返回 `400 invalid_request`（`fields.project_id`），不交给数据库 |

**查询参数**（无则写“无”）：**无——本端点不接受任何查询参数**。携带任何查询参数（包括看起来无害的 `page`）都返回 `400 invalid_request`，`fields` 中指出参数名并提示"本端点不接受查询参数"。理由：它是"一个项目的完整画面"，任何筛选都会让画面不完整（《A06》）。

**请求体**（无则写“无请求体”）：无请求体。

**响应 `200`**：

```json
{
  "data": {
    "project": {
      "id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d20",
      "name": "Migration",
      "description": null,
      "group_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0e",
      "status": "in_progress",
      "visibility": "group_readonly",
      "start_date": "2026-10-01",
      "end_date": "2026-10-31",
      "color": "#336699",
      "archived_at": null,
      "is_archived": false,
      "is_read_only": false,
      "owner_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0f",
      "my_role": null,
      "task_count": 1,
      "progress": 40,
      "overdue_task_count": 1,
      "updated_at": "2026-10-11T04:30:54.047739Z"
    },
    "can_manage_project": false,
    "can_edit_tasks": false,
    "tasks": [
      {
        "id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d21",
        "project_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d20",
        "parent_id": null,
        "name": "Prepare migration",
        "description": null,
        "status": "in_progress",
        "progress": 40,
        "priority": 1,
        "start_date": "2026-10-01",
        "end_date": "2026-10-05",
        "is_milestone": false,
        "is_shared": true,
        "is_overdue": true,
        "can_edit": false,
        "owner": {
          "user_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0f",
          "username": "alice",
          "display_name": "Alice",
          "role": "owner"
        },
        "members": [
          {
            "user_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0f",
            "username": "alice",
            "display_name": "Alice",
            "role": "owner"
          }
        ],
        "updated_by": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0f",
        "updated_by_name": "Alice",
        "updated_at": "2026-10-11T04:30:54.047739Z"
      }
    ],
    "summary": {
      "project_count": 1,
      "task_count": 1,
      "in_progress_task_count": 1,
      "overdue_task_count": 1,
      "overall_progress": 40
    }
  }
}
```

顶层字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `data.project` | 项目投影 | **与 §1 的 `projects[]` 元素同一个结构**（字段含义见 §1 的项目投影表）；注意它本身就带 `my_role`、`task_count`、`progress`、`overdue_task_count` |
| `data.can_manage_project` | boolean | 能否修改**项目本身**（名称、日期、共享级别、成员）——即是否为项目持有者 |
| `data.can_edit_tasks` | boolean | 能否修改该项目下的任务；**归档项目恒为 `false`**（与逐任务 `can_edit` 一起用于进入编辑态前的边界判断） |
| `data.tasks` | 任务投影数组 | **与 §1 的 `tasks[]` 元素同一个结构**（字段含义见 §1 的任务投影表）；排序固定为 `start_date`、`end_date`、`id` 升序 |
| `data.summary` | object | 与 §1 的 `summary` 同结构；`project_count` 恒为 `1`，`task_count` 恒等于 `tasks` 的长度 |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `401` | `authentication_required` | 未登录或会话失效 |
| `400` | `invalid_request` | `project_id` 不是合法 UUID；**携带了任何查询参数**（`fields.<参数名>`） |
| `404` | `project_not_found` | 项目不存在，或请求者对项目无读权限（成员/组内共享/系统管理员都不满足） |

**备注**：

- **层级由客户端组装**：服务端不返回嵌套结构，只给 `parent_id`（最多 3 层，见《A05》）。里程碑由 `is_milestone` 标识，进度由 `progress` 标识。
- **可见任务的范围随权限收敛**：项目持有者/成员看到全部任务；组内访客（非项目成员）只看到已共享任务，且组内只读共享的任务 `can_edit` 为 `false`。因此**同一项目对不同人返回的 `tasks` 长度、`project.task_count` / `progress` / `overdue_task_count` 与 `summary` 可能不同**——它们都基于"本次请求者可见的任务"。
- **归档项目**：`project.is_archived` 与 `project.is_read_only` 为 `true`，`can_edit_tasks` 与每个任务的 `can_edit` 均为 `false`；本项目**默认不进入** §1 / §2 的视图（除非 `include_archived=true`），但本端点直接按 id 访问时**归档项目仍可读**（"归档 = 只读"，不是删除）。
- `can_manage_project` 与 `can_edit_tasks` 相互独立：项目普通成员通常是 `can_manage_project=false` 且 `can_edit_tasks=true`。
- **已知差异（供复核）**：`can_manage_project` 的判定只使用"当前用户 + 项目成员关系"，未携带系统角色，因此**系统管理员若不是该项目的持有者，本字段为 `false`**；而 `can_edit_tasks` 与逐任务 `can_edit` 对系统管理员照常放行（《A03》–§4.6 的管理员放行、§6 的相应条目）。**前端不要用 `can_manage_project=false` 推断"此人对该项目没有管理权"**，服务端写入口仍会独立判定。
- "拖拽改日期"属于客户端交互，落库走 `PATCH /api/v1/tasks/{task_id}/dates`（《A05》）；"时间尺度（日/周/月）"与"折叠"是纯展示参数，**服务端不提供**，客户端用响应里的完整日期区间自行换算。
