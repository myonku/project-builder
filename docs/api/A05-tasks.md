# A05 Tasks 接口详解

> 归属：**前后端共用**（`docs/api/`）。通用约定（基础路径 `/api/v1`、成功/错误信封、认证、分页、并发与幂等、限流、未知字段拒绝）见《S02-HTTP接口契约》§2，本文不重复。
> 契约的**约定性叙述**（信封、认证、分页、并发与幂等、限流、处理顺序、加强项）见《S02》；本文补齐每个接口的**请求与响应数据结构**。

> **2026-10-21 W4 已落地**：任务成员对象已增加 `avatar_updated_at`；任务响应本身仍不返回 owner/members，
> 头像展示由成员面板/计划视图投影提供。见 [文档总览](../README.md)。

全部 11 个端点都要求登录（会话 Cookie，见《S02》§2.2）；未登录返回 `401 authentication_required`。
下文各错误表不再重复这一行以外的通用约定；未列出的 `500 internal_error` 属于未预期服务端错误（《S02》§2.4）。

> **提醒计划同步（2026-10-29）**：影响任务节点或收件资格的业务写入，与相关提醒计划在同一事务提交；同步失败则业务回滚。当前有效规则、事件身份和授权裁决点见 [S01 §5.7](../S01-需求文档.md#57-邮件提醒)，查询、九种状态与重试边界见 [A08](A08-reminders.md)。业务请求不执行任务提醒 SMTP；已授权尝试允许按原快照完成。

## 接口清单

| #  | 方法       | 路径                                          | 权限                                 | 说明                                               |
| -- | ---------- | --------------------------------------------- | ------------------------------------ | -------------------------------------------------- |
| 1  | `POST`   | `/api/v1/projects/{project_id}/tasks`       | 对项目有**写权限**且项目未归档 | 在项目下创建任务；创建者成为任务持有者             |
| 2  | `POST`   | `/api/v1/projects/{project_id}/tasks/query` | 对项目至少有**读权限**         | 任务列表：状态/成员/共享/删除视角 + 分页排序       |
| 3  | `GET`    | `/api/v1/tasks/{task_id}`                   | 任务可见者                           | 任务详情（含派生`is_overdue`）                   |
| 4  | `PATCH`  | `/api/v1/tasks/{task_id}`                   | 对任务有写权限且项目未归档           | 修改任务字段；`expected_updated_at` 必填         |
| 5  | `PATCH`  | `/api/v1/tasks/{task_id}/dates`             | 同上（4）                            | 甘特图日期编辑（只接受日期与版本）                 |
| 6  | `DELETE` | `/api/v1/tasks/{task_id}`                   | 同上（4）                            | 逻辑删除；请求体携带`expected_updated_at`        |
| 7  | `POST`   | `/api/v1/tasks/{task_id}/copy`              | 同上（4）                            | 复制任务；`Idempotency-Key` 请求头可选           |
| 8  | `POST`   | `/api/v1/tasks/{task_id}/members/query`     | 任务可见者                           | 任务成员列表                                       |
| 9  | `POST`   | `/api/v1/tasks/{task_id}/members`           | 对任务有写权限                       | 添加任务成员（目标必须是项目成员）                 |
| 10 | `PATCH`  | `/api/v1/tasks/{task_id}/members/{user_id}` | 对任务有写权限                       | 改成员角色；`role: owner` = **转移持有者** |
| 11 | `DELETE` | `/api/v1/tasks/{task_id}/members/{user_id}` | 对任务有写权限                       | 移除成员；不得移除唯一持有者                       |

**路径结构**（见《S02》§3 的端点总览与本文件"接口清单"）：**集合**端点挂在 `/projects/{project_id}/tasks`（创建、列表），**单任务**端点只挂在 `/tasks/{task_id}`。实现里**没有** `/projects/{project_id}/tasks/{task_id}` 这种入口，前端不要按嵌套路径拼单任务地址。

**权限速查**（任务权限建立在项目权限之上）：

| 请求者对项目的关系                              | 能否读任务             | 能否写任务             | 说明                                      |
| ----------------------------------------------- | ---------------------- | ---------------------- | ----------------------------------------- |
| 项目持有者                                      | 能（项目内全部任务）   | 能                     | 项目级`manage`                          |
| 项目普通成员                                    | 能（项目内全部任务）   | 能                     | **不需要是任务成员**                |
| 归属组活跃成员，`visibility = group_editable` | 仅**已共享**任务 | 仅**已共享**任务 | 未共享任务按不存在处理（`404`）         |
| 归属组活跃成员，`visibility = group_readonly` | 仅**已共享**任务 | 不能                   | 可见但只读（`403`）；未共享任务 `404` |
| 归属组活跃成员，`visibility = private`        | 不能                   | 不能                   | `404`                                   |
| 系统管理员（不要求成员/组员身份）               | 能                     | 能                     | 归档项目仍只读                            |
| 已归档项目                                      | 能（只读）             | 不能                   | 写入一律`409 conflict`                  |

**任务成员身份不是编辑前提**：能否改任务由**项目权限 + 任务是否共享**决定；任务成员关系只影响 `membership_role`、`owner_id`/`assignee_id` 筛选与提醒收件人。
**"不可见"一律是 `404`**：任务级不可见是 `404 task_not_found`，项目级不可见是 `404 project_not_found`（哪一个按端点而异，见各端点错误表）。两者刻意分开，前端可据此区分"任务没了"与"整个项目看不到"。

## 任务对象

端点 1/3/4/5/7 的 `data` 与端点 2 的 `data[]` 元素都是同一个结构：

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d0781601",
    "project_id": "01a1290b-0ae8-7046-9106-30f2d07815af",
    "parent_id": null,
    "has_children": false,
    "created_by": "01a1290b-0ae8-7046-9106-30f2d07815c1",
    "name": "准备迁移方案",
    "description": null,
    "status": "in_progress",
    "progress": 40,
    "priority": 1,
    "start_date": "2026-10-01",
    "end_date": "2026-10-05",
    "is_milestone": false,
    "is_shared": true,
    "is_overdue": false,
    "deleted_at": null,
    "membership_role": "owner",
    "updated_by": "01a1290b-0ae8-7046-9106-30f2d07815c1",
    "updated_at": "2026-10-11T04:30:54.047739Z"
  }
}
```

| 字段                          | 类型                     | 说明                                                                                                                                                     |
| ----------------------------- | ------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `id`                        | string(UUID)             | 任务 id                                                                                                                                                  |
| `project_id`                | string(UUID)             | 所属项目                                                                                                                                                 |
| `parent_id`                 | string(UUID)\| null      | 父任务；`null` 表示顶层任务。层级最多 3 层，子任务必须与父任务同项目且落在父任务日期区间内                                                             |
| `created_by`                | string(UUID)             | 创建者。**不随持有者转移而变**（持有者由成员关系表达）                                                                                             |
| `name`                      | string                   | 名称，1–200 字符（按 Unicode 码点计）                                                                                                                   |
| `description`               | string\| null            | 描述，≤4000 字符；创建时传空串存`null`，更新时传空串存 `""`（见端点 4 备注）                                                                        |
| `status`                    | string                   | `not_started` \| `in_progress` \| `completed` \| `cancelled`。**没有 `overdue`**：逾期是派生字段                                         |
| `progress`                  | number                   | 0–100 整数有效进度：叶子手工、非叶子按直接下级汇总 |
| `has_children` | boolean | 只读；是否有未删除直接子任务，全取消下级仍为 true |
| `priority`                  | number                   | `0` 低 / `1` 普通 / `2` 高 / `3` 紧急；数据库列没有 `CHECK`，取值集合完全由领域层保证，越界返回 `422`                                        |
| `start_date` / `end_date` | string(date)             | `YYYY-MM-DD`；恒有 `end_date >= start_date`                                                                                                          |
| `is_milestone`              | boolean                  | 里程碑标记（纯展示语义，不改变日期或状态规则）                                                                                                           |
| `is_shared`                 | boolean                  | 是否共享给归属工作组；**组内视图（含组内成员）看到任务的前提**                                                                                     |
| `is_overdue`                | boolean                  | **派生**：`end_date < 今天` 且状态为 `not_started`/`in_progress`；已完成/已取消不算逾期。"今天"按**请求者时区**计算                    |
| `deleted_at`                | string(timestamp)\| null | 非空表示已逻辑删除                                                                                                                                       |
| `membership_role`           | string\| null            | **派生**：请求者在本**任务**中的角色（`owner`/`member`）；非任务成员为 `null`。注意：是项目成员不等于任务成员，因此这里常常是 `null` |
| `updated_by`                | string(UUID)             | 最近一次修改**本任务行**的用户。创建时＝创建者；与 `updated_at` 在同一条语句里推进                                                               |
| `updated_at`                | string(timestamp)        | UTC ISO 8601，**带亚秒精度**；更新类接口要求原样回传为 `expected_updated_at`（《S02》§2.6.2）                                                   |

**与"任务修改"无关的操作不推进 `updated_by`/`updated_at`**：成员变更、附件上传都不触碰 `tasks` 行。因此"最后修改人"回答的是"谁改了任务本身"，成员关系的历史要看操作记录（《A09》）。

刻意**不返回**：

- `updated_by_name`：显示名只在计划视图（《A06》）里由服务端一并取出。任务端点只给 `updated_by`（UUID）；要显示名字请自行查 `GET /api/v1/users/{user_id}`（《A02》）。
- `can_edit` / `is_read_only` / `my_role` / `owner_id` / `members` / `summary`：这些都是**计划视图**的投影字段（《A06》）。任务端点用 `membership_role` 表达"我在这个任务里的角色"；"能不能改"要按上面的权限速查自行判定（或直接用计划视图的 `can_edit`）。
- `created_at`：不返回（可 `sort`，但拿不到值）。
- 子任务与附件：本对象不含子任务数组，层级由前端按 `parent_id` 组装（列表端点会返回同一项目的全部任务）；附件见《A07》。

**派生与联动规则**（写请求也会用到的判定）：

| 规则             | 内容                                                                                                                                                                                                    |
| ---------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 叶子状态 ↔ 进度联动 | 显式给`progress` 但**没给** `status` 时，状态按进度推导：`0` → `not_started`，`1–99` → `in_progress`，`100` → `completed`。显式给了 `status` 时以显式值为准（不做反向推导） |
| 逾期             | `end_date < 今天 AND status IN ('not_started','in_progress')`；不是持久化状态，数据库也不会接受 `overdue`                                                                                           |
| 日期区间         | 任何写入都必须满足`start_date <= end_date`，否则 `400 invalid_date_range`；部分更新按**合并后的最终值**校验                                                                                   |
| 层级             | 顶层任务深度为 1，最多 3 层；第 4 层返回`422`（`fields.parent_id`）                                                                                                                                 |
| 父任务           | 必须同项目（否则`400`）、未删除、且日期区间覆盖子任务（否则 `422`）；不能是自身或自己的子孙（`422`）                                                                                              |

### 父任务汇总与写入边界

- has_children 按未删除结构判定，与分页、筛选、共享和取消无关。非叶子的普通 status/progress 字段即使值未改变也返回 422 validation_error，当前 fields.progress 说明“状态和进度由直接下级汇总，不可手工修改”。其余资料、日期、成员操作仍按原权限。
- 直接下级等权、逐层传递；取消分支排除，全部下级取消为 cancelled/0；显示整数不决定状态，完成须全部有效下级状态完成且实际进度为 100%，详见 S01 §5.4.1。内部 precise_progress 不对外返回。
- 创建、修改、删除、改父级在项目锁保护的事务中刷新结果；旧/新祖先链与项目同步变化。移动整棵子树仍不得超过三层，禁止循环、跨项目父子关系及父日期不能覆盖存活子任务的修改；事务内结构校验可在 fields.parent_id 返回 422。
- 添加首个子任务即切换为汇总；移走/删除最后子任务后承接最后汇总展示值恢复手工。逻辑删除父任务不删除子任务，存活子任务成为有效根；存储层恢复重新计入。没有新增任务恢复 HTTP 端点。
- 祖先的有效值或结构标志发生变化时，其 updated_at 及 updated_by 可随触发者更新；项目版本随任务变更推进。客户端不得假设只有直接编辑的任务才会版本冲突。

## 任务成员对象

端点 9/10 的 `data` 与端点 8 的 `data[]` 元素：

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d0781610",
    "task_id": "01a1290b-0ae8-7046-9106-30f2d0781601",
    "user_id": "01a1290b-0ae8-7046-9106-30f2d07815c1",
    "role": "owner",
    "username": "alice",
    "display_name": "Alice",
    "email": "alice@example.com",
    "avatar_updated_at": null
  }
}
```

| 字段                                        | 类型         | 说明                                                          |
| ------------------------------------------- | ------------ | ------------------------------------------------------------- |
| `id`                                      | string(UUID) | 成员关系行 id（不是用户 id）                                  |
| `task_id`                                 | string(UUID) | 任务 id                                                       |
| `user_id`                                 | string(UUID) | 成员用户 id                                                   |
| `role`                                    | string       | `owner` \| `member`；一个任务**恰好一个** `owner` |
| `username` / `display_name` / `email` | string       | 用户展示字段                                                  |
| `avatar_updated_at` | string \| null | 成员头像版本；`null` 表示无头像（W4） |

刻意**不返回** `created_at`（可用于 `sort`，但不在响应里）。任务成员关系也没有 `status`：移除即删行。

## 1. `POST /api/v1/projects/{project_id}/tasks`

**用途**：在项目下创建任务，创建者在同一事务内成为任务持有者与成员。
**权限**：对项目有**写权限**且项目未归档——项目持有者、项目成员、`group_editable` 的组内活跃成员、系统管理员。仅有读权限（如 `group_readonly` 的组员）返回 `403 permission_denied`；归档项目返回 `409 conflict`。

**路径参数**：

| 参数           | 类型         | 说明                                                                     |
| -------------- | ------------ | ------------------------------------------------------------------------ |
| `project_id` | string(UUID) | 目标项目；非法 UUID 返回`400 invalid_request`（`fields.project_id`） |

**查询参数**：无。

**请求体**：

| 字段             | 类型                | 必填                | 约束                                               | 说明                            |
| ---------------- | ------------------- | ------------------- | -------------------------------------------------- | ------------------------------- |
| `name`         | string              | 是                  | 去首尾空白后非空，1–200 字符                      | 任务名称                        |
| `start_date`   | string(date)        | 否（见备注）        | `YYYY-MM-DD`，不得晚于 `end_date`              | 开始日期                        |
| `end_date`     | string(date)        | 否（见备注）        | `YYYY-MM-DD`，不得早于 `start_date`            | 结束日期                        |
| `description`  | string\| null       | 否                  | ≤4000 字符；空串按"未提供"处理（存`null`）      | 描述                            |
| `parent_id`    | string(UUID)\| null | 否                  | 父任务必须同项目、未删除、层级 ≤3、区间覆盖本任务 | 父任务；`null`/省略＝顶层任务 |
| `priority`     | number              | 否（默认`1`）     | `0`/`1`/`2`/`3`，其它值 → `422`         | 优先级                          |
| `is_milestone` | boolean             | 否（默认`false`） | —                                                 | 里程碑标记                      |
| `is_shared`    | boolean             | 否（默认`false`） | —                                                 | 是否共享给归属工作组            |

**请求体不接受 `status`、`progress`**：新任务固定 `status = not_started`、`progress = 0`（与需求文档 5.4 一致）。带这两个字段会因"契约未定义"返回 `422`。

请求体示例：

```json
{
  "name": "准备迁移方案",
  "start_date": "2026-10-01",
  "end_date": "2026-10-05",
  "parent_id": null,
  "priority": 2,
  "is_milestone": false,
  "is_shared": true
}
```

**响应 `201`**：

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d0781601",
    "project_id": "01a1290b-0ae8-7046-9106-30f2d07815af",
    "parent_id": null,
    "has_children": false,
    "created_by": "01a1290b-0ae8-7046-9106-30f2d07815c1",
    "name": "准备迁移方案",
    "description": null,
    "status": "not_started",
    "progress": 0,
    "priority": 2,
    "start_date": "2026-10-01",
    "end_date": "2026-10-05",
    "is_milestone": false,
    "is_shared": true,
    "is_overdue": false,
    "deleted_at": null,
    "membership_role": "owner",
    "updated_by": "01a1290b-0ae8-7046-9106-30f2d07815c1",
    "updated_at": "2026-10-11T04:30:54.047739Z"
  }
}
```

| 字段                      | 类型            | 说明                                                                                                                                                                                                |
| ------------------------- | --------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `status` / `progress` | string / number | 固定`not_started` / `0`                                                                                                                                                                         |
| `membership_role`       | string          | 固定`owner`（创建者在同一事务内被写入 `task_members(owner)`）                                                                                                                                   |
| `updated_by`            | string(UUID)    | 固定等于创建者（创建也算一次修改）                                                                                                                                                                  |
| `deleted_at`            | null            | 新任务未删除                                                                                                                                                                                        |
| 其余字段                  | —              | `id`/`project_id`/`parent_id`/`created_by`/`name`/`description`/`priority`/`start_date`/`end_date`/`is_milestone`/`is_shared`/`is_overdue`/`updated_at`，语义见"任务对象" |

**错误**：

| 状态码  | `error.code`              | 触发条件                                                                                                                                                                                                                                     |
| ------- | --------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `400` | `invalid_request`         | 请求体为空/非 JSON/字段类型不符；`project_id` 不是合法 UUID；**父任务与子任务不属于同一项目**（`fields.parent_id`）                                                                                                                |
| `400` | `invalid_date_range`      | `start_date` 晚于 `end_date`（`fields.start_date`、`fields.end_date`）。此错误优先于其余字段校验                                                                                                                                     |
| `401` | `authentication_required` | 未登录或会话失效                                                                                                                                                                                                                             |
| `403` | `permission_denied`       | 项目可见但只有读权限（例如`group_readonly` 的组员）                                                                                                                                                                                        |
| `404` | `project_not_found`       | 项目不存在或对当前用户不可见（该错误**不带** `fields`）                                                                                                                                                                              |
| `404` | `task_not_found`          | `parent_id` 指向的任务不存在或已被逻辑删除（该错误**不带** `fields`）                                                                                                                                                              |
| `409` | `conflict`                | 项目已归档，不可新建任务（`fields.project_id`）                                                                                                                                                                                            |
| `422` | `validation_error`        | 缺少`name`；`name` 空白或超长；`description` 超长；`priority` 越界；层级超过 3 层（`fields.parent_id`）；子任务日期未落在父任务区间内（`fields.start_date`）；请求体含契约未定义字段（含 `status`/`progress`，《S02》§2.7） |
| `500` | `internal_error`          | `parent_id` 不是合法 UUID（直达数据库触发 `22P02`，见文末）等未预期错误                                                                                                                                                                  |

**备注**：

- **校验顺序**（决定你看到哪个错误）：绑定请求体（`400`/`422`，含缺少 `name`）→ 项目可见性与写权限（`404`/`403`/`409`）→ 字段归一化（`400 invalid_date_range` 优先于 `422`）→ 父任务校验（`404`/`400`/`422`）→ 写库。
- **`start_date`/`end_date` 必须由客户端提供**：字段没有必填标记也没有默认值，省略时按 Go 零值日期 `0001-01-01` 写入并返回 `201`（详见文末）。
- **`priority` 省略时是 `1`（普通）**：服务端总是显式写入该值，因此不会落到数据库列的默认值 `0`。
- **父子层级**：新建子任务时校验父任务深度，最多 3 层；第 4 层返回 `422`。
- **创建者不必是项目成员**：实现只校验"对项目有写权限"，`group_editable` 的组内活跃成员即便不是项目成员也能创建并成为任务持有者（口径与边界详见文末）。
- **副作用**：创建任务、写入 `task_members(owner)` 与写一条操作记录（`action = created`）在**同一事务**内完成（《A09》）。创建**不产生**提醒（提醒只在"任务成员被加入"与"持有者转移"时写入）。
- **不幂等**：重复提交会创建多个任务。

## 2. `POST /api/v1/projects/{project_id}/tasks/query`

**用途**：分页查询项目下的任务，支持状态、成员、共享与逻辑删除视角筛选。
**权限**：对项目至少有读权限（项目成员 / 组内共享可见者 / 系统管理员）。不可见返回 `404 project_not_found`。

**路径参数**：

| 参数           | 类型         | 说明                                            |
| -------------- | ------------ | ----------------------------------------------- |
| `project_id` | string(UUID) | 目标项目；非法 UUID 返回`400 invalid_request` |

**查询参数**：无（筛选、分页与排序都在 JSON 请求体里）。

**请求体**（必须是 JSON 对象；`{}` 合法，完全没有请求体返回 `400 invalid_request`，`fields.body`）：

| 字段                | 类型         | 默认                   | 约束                                                    | 说明                                                  |
| ------------------- | ------------ | ---------------------- | ------------------------------------------------------- | ----------------------------------------------------- |
| `status`          | string       | 不限                   | 四种任务状态之一，其它值 →`400`（`fields.status`） | 按任务状态筛选                                        |
| `assignee_id`     | string(UUID) | 不限                   | 非法 UUID →`400`（`fields.assignee_id`）           | 按**任务成员**筛选（任意角色，含持有者）        |
| `owner_id`        | string(UUID) | 不限                   | 非法 UUID →`400`（`fields.owner_id`）              | 按**任务持有者**筛选（`role = owner` 的成员） |
| `is_shared`       | boolean      | 不限                   | —                                                      | 按是否已共享筛选                                      |
| `include_deleted` | boolean      | `false`              | —                                                      | 为真时把已逻辑删除的任务纳入候选集                    |
| `page`            | number       | `1`                  | `>= 1`，越界 `400`                                  | 页码                                                  |
| `page_size`       | number       | `20`                 | `1..100`，越界 `400`                                | 每页条数                                              |
| `sort`            | string       | `start_date`（升序） | 白名单见下；未知或重复字段 →`400`（`fields.sort`） | 排序表达式，逗号分隔多字段，前缀`-` 表示降序        |

`sort` 白名单：`name`、`status`、`progress`、`priority`、`start_date`、`end_date`、`created_at`、`updated_at`。
服务端始终追加 `id` 作为次级排序键，因此翻页不重不漏（《S02》§2.5）。

请求体示例：

```json
{
  "status": "in_progress",
  "owner_id": "01a1290b-0ae8-7046-9106-30f2d07815c1",
  "assignee_id": "01a1290b-0ae8-7046-9106-30f2d07815d5",
  "is_shared": true,
  "include_deleted": false,
  "page": 1,
  "page_size": 20,
  "sort": "start_date"
}
```

**响应 `200`**：

```json
{
  "data": [
    {
      "id": "01a1290b-0ae8-7046-9106-30f2d0781601",
      "project_id": "01a1290b-0ae8-7046-9106-30f2d07815af",
      "parent_id": null,
      "has_children": false,
      "created_by": "01a1290b-0ae8-7046-9106-30f2d07815c1",
      "name": "准备迁移方案",
      "description": null,
      "status": "in_progress",
      "progress": 40,
      "priority": 1,
      "start_date": "2026-10-01",
      "end_date": "2026-10-05",
      "is_milestone": false,
      "is_shared": true,
      "is_overdue": false,
      "deleted_at": null,
      "membership_role": "owner",
      "updated_by": "01a1290b-0ae8-7046-9106-30f2d07815c1",
      "updated_at": "2026-10-11T04:30:54.047739Z"
    }
  ],
  "pagination": {
    "page": 1,
    "page_size": 20,
    "total": 1,
    "has_next": false
  }
}
```

| 字段           | 类型   | 说明                                                                                                   |
| -------------- | ------ | ------------------------------------------------------------------------------------------------------ |
| `data`       | array  | 任务对象数组（见"任务对象"）；空结果是`[]` 而不是 `null`                                           |
| `pagination` | object | `page`/`page_size`/`total`/`has_next`；`has_next = page * page_size < total`（《S02》§2.3） |

**错误**：

| 状态码  | `error.code`              | 触发条件                                                                                                                                                              |
| ------- | --------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `400` | `invalid_request`         | 请求体为空/非 JSON 对象/字段类型不符；分页越界；`sort` 未知或重复；`status` 不在枚举内；`assignee_id`、`owner_id` 不是合法 UUID；`project_id` 不是合法 UUID |
| `401` | `authentication_required` | 未登录或会话失效                                                                                                                                                      |
| `404` | `project_not_found`       | 项目不存在或对当前用户不可见                                                                                                                                          |
| `422` | `validation_error`        | 请求体含契约未定义的字段（《S02》§2.7）                                                                                                                              |

**备注**：

- **`owner_id` 与 `assignee_id` 可以同时给出，且必须能命中**：两个条件各用独立子查询判定，因此"甲持有、乙承担"的任务在组合筛选下仍然返回。把两个谓词压在同一行会恒为空（这条警告写在《S02》§5 的加强项里）。
- **组内只读查看者的 `is_shared` 会被强制**：对 `visibility = group_readonly` 的组员，服务端一律按"只看已共享任务"过滤，即使请求里写了 `is_shared: false`，响应也不会提示这一点（避免越权枚举）。
- **默认排除逻辑删除**：`include_deleted: true` 时纳入（历史数据可查）。注意这与单任务详情不同——详情**永远**返回 `404`（端点 3）。
- **归档项目下的任务照常列出**（列表按可见性过滤，不排除归档项目），只是所有写入会失败。
- **`pagination.total` 与返回条目数一致**：计数与列表复用同一套筛选构造。
- **层级不由本端点组装**：`data` 是扁平数组，父子关系靠 `parent_id`；需要树形/甘特图请用《A06》的甘特图端点。

## 3. `GET /api/v1/tasks/{task_id}`

**用途**：读取任务详情（含派生 `is_overdue` 与 `membership_role`）。
**权限**：任务可见者（权限不为 `none`）。

**路径参数**：

| 参数        | 类型         | 说明                                                                 |
| ----------- | ------------ | -------------------------------------------------------------------- |
| `task_id` | string(UUID) | 任务 id；非法 UUID 返回`400 invalid_request`（`fields.task_id`） |

**查询参数**：无（多余查询参数被忽略，本域 GET 端点不解析查询串）。

**请求体**：无请求体。

**响应 `200`**：任务对象（字段见上）。

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d0781601",
    "project_id": "01a1290b-0ae8-7046-9106-30f2d07815af",
    "parent_id": "01a1290b-0ae8-7046-9106-30f2d0781600",
    "has_children": false,
    "created_by": "01a1290b-0ae8-7046-9106-30f2d07815c1",
    "name": "准备迁移方案",
    "description": null,
    "status": "in_progress",
    "progress": 40,
    "priority": 1,
    "start_date": "2026-10-01",
    "end_date": "2026-10-05",
    "is_milestone": false,
    "is_shared": true,
    "is_overdue": false,
    "deleted_at": null,
    "membership_role": null,
    "updated_by": "01a1290b-0ae8-7046-9106-30f2d07815d5",
    "updated_at": "2026-10-11T04:30:54.047739Z"
  }
}
```

| 字段         | 类型 | 说明                                                                                                                                          |
| ------------ | ---- | --------------------------------------------------------------------------------------------------------------------------------------------- |
| （全部字段） | —   | 与"任务对象"逐字段一致；上例展示两个常见形态：`parent_id` 非空（子任务）、`membership_role` 为 `null`（请求者是项目成员但不是任务成员） |

**错误**：

| 状态码  | `error.code`              | 触发条件                                                                |
| ------- | --------------------------- | ----------------------------------------------------------------------- |
| `400` | `invalid_request`         | `task_id` 不是合法 UUID                                               |
| `401` | `authentication_required` | 未登录或会话失效                                                        |
| `404` | `task_not_found`          | 任务不存在，或**已逻辑删除**（详情没有 `include_deleted` 参数） |
| `404` | `project_not_found`       | 任务存在，但项目对请求者不可见，或任务未共享而请求者只有组内身份        |

**备注**：

- **两个 `404` 码必须分开处理**：`task_not_found` 表示"这个任务不可用（不存在或已删）"，`project_not_found` 表示"整个项目对你看不到"。前端可据此决定是留在当前页面还是回列表。
- **已删除任务查不到是刻意的**：详情端点没有 `include_deleted` 参数，历史数据只能通过列表 `include_deleted: true` 查询。两条路径的**默认**行为一致（都排除已删除任务）。
- **响应不含子任务、成员、附件**：`parent_id` 只给出父任务 id；成员列表用端点 8，附件见《A07》，`updated_by` 的显示名见《A02》。

## 4. `PATCH /api/v1/tasks/{task_id}`

**用途**：修改任务字段（名称、描述、状态、进度、优先级、日期、里程碑、共享、父任务）。
**权限**：对任务有写权限（项目持有者/成员、`group_editable` 且任务已共享的组员、系统管理员）。归档项目返回 `409`；任务可见但只有读权限（`group_readonly` 且任务已共享）返回 `403`；任务不可见（未共享或项目不可见）返回 `404`。

**路径参数**：

| 参数        | 类型         | 说明                                           |
| ----------- | ------------ | ---------------------------------------------- |
| `task_id` | string(UUID) | 任务 id；非法 UUID 返回`400 invalid_request` |

**查询参数**：无。

**请求体**：

| 字段                    | 类型                | 必填         | 约束                                             | 说明                                                                  |
| ----------------------- | ------------------- | ------------ | ------------------------------------------------ | --------------------------------------------------------------------- |
| `name`                | string              | 否           | 去首尾空白后非空，1–200 字符                    | 名称                                                                  |
| `description`         | string\| null       | 否           | ≤4000 字符；**空串存 `""`**             | 描述（清空语义见备注）                                                |
| `status`              | string              | 否           | 四种任务状态之一；仅 has_children=false 可写                                 | 状态；显式给出时不做进度推导                                          |
| `progress` | integer | 否 | 0–100；仅 has_children=false 可写 | 只给进度时推导状态；显式 status 优先 |
| `priority`            | number              | 否           | `0`/`1`/`2`/`3`，其它 → `422`         | 优先级                                                                |
| `start_date`          | string(date)        | 否           | 与现有`end_date` 合并后必须 `start <= end`   | 开始日期                                                              |
| `end_date`            | string(date)        | 否           | 与现有`start_date` 合并后必须 `start <= end` | 结束日期                                                              |
| `is_milestone`        | boolean             | 否           | —                                               | 里程碑标记                                                            |
| `is_shared`           | boolean             | 否           | —                                               | 是否共享给归属组（影响组内可见性与组内可编辑者的写权限）              |
| `parent_id`           | string(UUID)\| null | 否           | 见"父任务"规则                                   | **省略＝不修改；显式 `null`＝提升为顶层任务；给值＝换父任务** |
| `expected_updated_at` | string(timestamp)   | **是** | 必须等于当前`updated_at`                       | 乐观并发版本                                                          |

请求体示例（省略 `parent_id`，只改进度）：

```json
{
  "progress": 60,
  "expected_updated_at": "2026-10-11T04:30:54.047739Z"
}
```

**响应 `200`**：任务对象，`updated_at` 与 `updated_by` 一并推进为本次写入者。

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d0781601",
    "project_id": "01a1290b-0ae8-7046-9106-30f2d07815af",
    "parent_id": null,
    "has_children": false,
    "created_by": "01a1290b-0ae8-7046-9106-30f2d07815c1",
    "name": "准备迁移方案",
    "description": null,
    "status": "in_progress",
    "progress": 60,
    "priority": 1,
    "start_date": "2026-10-01",
    "end_date": "2026-10-05",
    "is_milestone": false,
    "is_shared": true,
    "is_overdue": false,
    "deleted_at": null,
    "membership_role": "owner",
    "updated_by": "01a1290b-0ae8-7046-9106-30f2d07815d5",
    "updated_at": "2026-10-11T06:20:31.882190Z"
  }
}
```

| 字段           | 类型         | 说明                                                                                          |
| -------------- | ------------ | --------------------------------------------------------------------------------------------- |
| `status`     | string       | 未提供`status` 但提供了 `progress` 时，为按进度推导出的状态（上例 60 → `in_progress`） |
| `updated_by` | string(UUID) | 本次执行者（不是`created_by`）                                                              |
| 其余字段       | —           | 未提供的字段保持原值，语义见"任务对象"                                                        |

**错误**：

| 状态码  | `error.code`              | 触发条件                                                                                                                                                                                                                              |
| ------- | --------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `400` | `invalid_request`         | 请求体为空/非 JSON/字段类型不符；`task_id` 不是合法 UUID；父任务跨项目或父任务就是自身（`fields.parent_id`）                                                                                                                      |
| `400` | `invalid_date_range`      | **合并后**的日期区间倒置（只改一端也会被判出来；`fields.start_date`、`fields.end_date`）                                                                                                                                    |
| `401` | `authentication_required` | 未登录或会话失效                                                                                                                                                                                                                      |
| `403` | `permission_denied`       | 任务可见但请求者只有读权限（`group_readonly` 且任务已共享）                                                                                                                                                                         |
| `404` | `task_not_found`          | 任务不存在或已逻辑删除                                                                                                                                                                                                                |
| `404` | `project_not_found`       | 项目不可见，或任务未共享而请求者只有组内身份                                                                                                                                                                                          |
| `409` | `conflict`                | 项目已归档，任务只读（`fields.project_id`）                                                                                                                                                                                         |
| `409` | `version_conflict`        | `expected_updated_at` 与当前版本不一致                                                                                                                                                                                              |
| `422` | `validation_error`        | `name` 空白或超长；`status` 不在枚举内；`progress` 越界；`priority` 越界；`description` 超长；层级超过 3 层或区间未覆盖（`fields.parent_id`/`fields.start_date`）；缺少 `expected_updated_at`；请求体含契约未定义字段 |
| `500` | `internal_error`          | `parent_id` 不是合法 UUID（触发 `22P02`，见文末）等未预期错误                                                                                                                                                                     |

**备注**：

- **叶子状态与进度联动**：仅 has_children=false 时可写。只给 `progress` 时状态按进度推导（0 / 1–99 / 100）；同时给 `status` 与 `progress` 时两者都按你给的值写入（不做一致性校验）。
- **日期区间用合并后的最终值校验**：只改 `end_date` 也会与现有 `start_date` 比较，避免"改坏了却没报错"。
- **`parent_id` 的三态语义**（只能用原始报文区分，因此服务端按 key 是否存在判定）：省略＝不动；`null`＝提升为顶层任务；给 id ＝ 换父任务（并重新做同项目、层级、区间覆盖与环检测）。
- **`description` 的清空语义与项目不同**：任务传空串会存成**空字符串**（响应 `""`），项目传空串会存成 `null`（《A04》端点 4）。前端若要统一"清空"，请按域区分展示。
- **空写入会成功**：只提交 `expected_updated_at` 也返回 `200`，并推进 `updated_at` 与 `updated_by`。服务端没有"至少一个可改字段"的校验。
- **归档只读对所有人生效**（含系统管理员）：`409 conflict`，`fields.project_id` 说明原因。
- **副作用**：同事务写用户操作审计，并刷新受影响祖先与项目的有效结果及版本；changes 只含本次请求实际提供的字段。日期和有效状态变化会在同一事务内同步受影响任务/祖先的提醒计划（A08）；已获发送授权的邮件可能仍按原快照发送，前端不另行创建或撤销提醒。
- **成员变更不影响本接口的版本**：成员关系与附件不触碰 `tasks` 行，因此它们的变更不会推进 `updated_at`，也就不会让你的 `expected_updated_at` 失效。

## 5. `PATCH /api/v1/tasks/{task_id}/dates`

**用途**：甘特图日期拖拽或面板确认保存——只改日期，不碰名称/状态/进度等字段。进度编辑复用端点 4，仅提交 progress 和开始本笔编辑时捕获的 expected_updated_at，不附带旧 status。
**权限**：与端点 4 完全相同。

**路径参数**：

| 参数        | 类型         | 说明                                           |
| ----------- | ------------ | ---------------------------------------------- |
| `task_id` | string(UUID) | 任务 id；非法 UUID 返回`400 invalid_request` |

**查询参数**：无。

**请求体**（字段集合严格收敛，**只有这三项**）：

| 字段                    | 类型              | 必填                               | 约束                                    | 说明         |
| ----------------------- | ----------------- | ---------------------------------- | --------------------------------------- | ------------ |
| `start_date`          | string(date)      | 否（但**必须提供**，见备注） | `YYYY-MM-DD`，不得晚于 `end_date`   | 新的开始日期 |
| `end_date`            | string(date)      | 否（但**必须提供**，见备注） | `YYYY-MM-DD`，不得早于 `start_date` | 新的结束日期 |
| `expected_updated_at` | string(timestamp) | **是**                       | 必须等于当前`updated_at`              | 乐观并发版本 |

携带 `name`、`status`、`progress` 等字段会因"契约未定义"返回 `422 validation_error`（不静默忽略）。

```json
{
  "start_date": "2026-10-02",
  "end_date": "2026-10-06",
  "expected_updated_at": "2026-10-11T04:30:54.047739Z"
}
```

**响应 `200`**：

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d0781601",
    "project_id": "01a1290b-0ae8-7046-9106-30f2d07815af",
    "parent_id": null,
    "has_children": false,
    "created_by": "01a1290b-0ae8-7046-9106-30f2d07815c1",
    "name": "准备迁移方案",
    "description": null,
    "status": "in_progress",
    "progress": 60,
    "priority": 1,
    "start_date": "2026-10-02",
    "end_date": "2026-10-06",
    "is_milestone": false,
    "is_shared": true,
    "is_overdue": false,
    "deleted_at": null,
    "membership_role": "owner",
    "updated_by": "01a1290b-0ae8-7046-9106-30f2d07815c1",
    "updated_at": "2026-10-11T06:31:02.115007Z"
  }
}
```

| 字段                            | 类型                  | 说明                                   |
| ------------------------------- | --------------------- | -------------------------------------- |
| `start_date` / `end_date`   | string(date)          | 本次写入的新日期                       |
| `updated_by` / `updated_at` | string(UUID) / string | 甘特图拖拽同样是一次修改：两者一起推进 |
| 其余字段                        | —                    | 与"任务对象"一致，未受影响             |

**错误**：

| 状态码  | `error.code`              | 触发条件                                                                                                             |
| ------- | --------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| `400` | `invalid_request`         | 请求体为空/非 JSON/字段类型不符；`task_id` 不是合法 UUID                                                           |
| `400` | `invalid_date_range`      | `start_date` 晚于 `end_date`（`fields.start_date`、`fields.end_date`）                                       |
| `401` | `authentication_required` | 未登录或会话失效                                                                                                     |
| `403` | `permission_denied`       | 任务可见但只有读权限                                                                                                 |
| `404` | `task_not_found`          | 任务不存在或已逻辑删除                                                                                               |
| `404` | `project_not_found`       | 项目不可见，或任务未共享而请求者只有组内身份                                                                         |
| `409` | `conflict`                | 项目已归档（`fields.project_id`）                                                                                  |
| `409` | `version_conflict`        | `expected_updated_at` 不匹配                                                                                       |
| `422` | `validation_error`        | 缺少`expected_updated_at`；携带 `name` 等契约未定义字段；新日期未落在父任务区间，或父日期未覆盖存活子任务（fields.start_date/fields.parent_id） |

**备注**：

- **字段收敛是有意的**：早期实现该端点复用了全量更新结构，导致"仅日期"端点能改名称与状态；现在多带任何字段都会 `422`。
- **两个日期都要给**：字段没有必填标记也没有默认值，只给一个会让另一个被写成零值日期 `0001-01-01`（服务端无法区分"未提供"与"要改成零值"）。详见文末。
- **父任务区间仍然生效**：任务有父任务时，新日期必须落在父任务区间内，否则 `422`。
- **副作用**：写一条 `action = updated` 的操作记录，`changes` 只含 `start_date`、`end_date`（同事务）；不产生提醒。

**甘特图调用约束（客户端交互，非新增服务端限制）**：日期确认提交两端及原样版本，每笔一个请求；进度写入使用端点 4。取消/无变化不请求。400/422 在原版本上修正后重新明确确认；409 version_conflict 丢弃草稿、重读后重新发起，403/404/归档拒绝同样锁定核对；网络、超时或异常响应不自动重试，也不宣称已回滚。已成功但重读失败须保留“已保存”事实。父任务进度只读，已取消叶子的甘特图进度入口关闭属于产品限制，不新增后端禁令。详细交互见 [F01 §5.11](../../frontend/docs/F01-前端架构设计.md#511-甘特图视图的标准结构gantt日期与进度编辑)。

## 6. `DELETE /api/v1/tasks/{task_id}`

**用途**：逻辑删除任务（写 `deleted_at`，行与历史保留）。
**权限**：与端点 4 完全相同（写权限 + 项目未归档）。

**路径参数**：

| 参数        | 类型         | 说明                                           |
| ----------- | ------------ | ---------------------------------------------- |
| `task_id` | string(UUID) | 任务 id；非法 UUID 返回`400 invalid_request` |

**查询参数**：无。

**请求体**（**DELETE 也要求 JSON 请求体**，因为删除是版本化写入，《S02》§2.6）：

| 字段                    | 类型              | 必填         | 约束                       | 说明         |
| ----------------------- | ----------------- | ------------ | -------------------------- | ------------ |
| `expected_updated_at` | string(timestamp) | **是** | 必须等于当前`updated_at` | 乐观并发版本 |

```json
{
  "expected_updated_at": "2026-10-11T06:31:02.115007Z"
}
```

**响应 `204`**：无响应体（不是 `{"data": ...}`）。

**错误**：

| 状态码  | `error.code`              | 触发条件                                                                           |
| ------- | --------------------------- | ---------------------------------------------------------------------------------- |
| `400` | `invalid_request`         | 请求体为空、不是合法 JSON、含多个 JSON 值或字段类型不符；`task_id` 不是合法 UUID |
| `401` | `authentication_required` | 未登录或会话失效                                                                   |
| `403` | `permission_denied`       | 任务可见但只有读权限                                                               |
| `404` | `task_not_found`          | 任务不存在或**已经逻辑删除**（重复删除不会报成功）                           |
| `404` | `project_not_found`       | 项目不可见，或任务未共享而请求者只有组内身份                                       |
| `409` | `conflict`                | 项目已归档（`fields.project_id`）                                                |
| `409` | `version_conflict`        | `expected_updated_at` 不匹配                                                     |
| `422` | `validation_error`        | 缺少`expected_updated_at`；请求体含契约未定义字段                                |

**备注**：

- **删除是逻辑的**：`deleted_at` 非空，行、成员关系与附件都保留；任务的审计记录只增不减（《A09》）。
- **不可恢复**：没有"恢复已删除任务"的端点。历史数据只能通过列表 `include_deleted: true` 查到，详情永远 `404`。
- **`updated_by` 会记录执行者**（需求文档 5.8 把删除列为关键操作），因此删完之后在库里仍能回答"谁删的"。
- **不幂等**：对已删除任务再删一次返回 `404 task_not_found`。
- **副作用**：写一条 `action = deleted` 的操作记录（同事务）；不发送提醒（不通知成员任务被删）。

## 7. `POST /api/v1/tasks/{task_id}/copy`

**用途**：复制任务——按源任务的基础字段新建一个任务，可用 `Idempotency-Key` 保证重试不产生重复副本。
**权限**：与端点 4 完全相同（对源任务有写权限 + 项目未归档）。

**路径参数**：

| 参数        | 类型         | 说明                                                                |
| ----------- | ------------ | ------------------------------------------------------------------- |
| `task_id` | string(UUID) | 被复制的**源任务** id；非法 UUID 返回 `400 invalid_request` |

**查询参数**：无。

**请求头**：

| 头                  | 必填 | 说明                                                                     |
| ------------------- | ---- | ------------------------------------------------------------------------ |
| `Idempotency-Key` | 否   | 提供时按幂等语义处理；**不提供则每次调用都新建一个副本**（不幂等） |

**请求体**（必须是 JSON 对象；`{}` 合法，表示全部沿用源任务）：

| 字段            | 类型          | 必填 | 约束                                       | 说明                                                            |
| --------------- | ------------- | ---- | ------------------------------------------ | --------------------------------------------------------------- |
| `name`        | string        | 否   | 1–200 字符，空白或超长 →`422`          | 副本名称；省略时用"源名称（副本）"，若超过 200 字符则退回源名称 |
| `description` | string\| null | 否   | ≤4000 字符                                | 副本描述；省略时沿用源任务                                      |
| `start_date`  | string(date)  | 否   | 与`end_date` 合并后必须 `start <= end` | 副本开始日期；省略时沿用源任务                                  |
| `end_date`    | string(date)  | 否   | 同上                                       | 副本结束日期；省略时沿用源任务                                  |
| `is_shared`   | boolean       | 否   | —                                         | 副本是否共享；省略时沿用源任务                                  |

```json
{
  "name": "准备迁移方案（第二轮）",
  "start_date": "2026-11-01",
  "end_date": "2026-11-05"
}
```

**响应 `201`（首次创建）/ `200`（同一 key 的重放）**：

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d0781620",
    "project_id": "01a1290b-0ae8-7046-9106-30f2d07815af",
    "parent_id": null,
    "has_children": false,
    "created_by": "01a1290b-0ae8-7046-9106-30f2d07815d5",
    "name": "准备迁移方案（第二轮）",
    "description": null,
    "status": "in_progress",
    "progress": 60,
    "priority": 1,
    "start_date": "2026-11-01",
    "end_date": "2026-11-05",
    "is_milestone": false,
    "is_shared": true,
    "is_overdue": false,
    "deleted_at": null,
    "membership_role": "owner",
    "updated_by": "01a1290b-0ae8-7046-9106-30f2d07815d5",
    "updated_at": "2026-10-11T06:40:12.334455Z"
  }
}
```

| 字段                                                        | 类型         | 说明                                                         |
| ----------------------------------------------------------- | ------------ | ------------------------------------------------------------ |
| `id`                                                      | string(UUID) | **新**任务 id（重放时是既有副本的 id，与首次响应一致） |
| `parent_id`                                               | null         | 复制**不保留**父子关系，副本是顶层任务                 |
| `created_by` / `updated_by`                             | string(UUID) | 复制者（`updated_by` 不是源任务的最后修改人）              |
| `deleted_at`                                              | null         | 副本是新任务                                                 |
| `membership_role`                                         | string       | 固定`owner`（只有复制者被写入成员关系）                    |
| `status` / `progress` / `priority` / `is_milestone` | —           | **沿用源任务**（见备注）                               |
| 其余字段                                                    | —           | 见"任务对象"                                                 |

**错误**：

| 状态码  | `error.code`              | 触发条件                                                                                                                 |
| ------- | --------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| `400` | `invalid_request`         | 请求体为空/非 JSON/字段类型不符；`task_id` 不是合法 UUID                                                               |
| `400` | `invalid_date_range`      | 覆盖后的日期区间倒置（`fields.start_date`、`fields.end_date`）                                                       |
| `401` | `authentication_required` | 未登录或会话失效                                                                                                         |
| `403` | `permission_denied`       | 源任务可见但请求者只有读权限                                                                                             |
| `404` | `task_not_found`          | 源任务不存在、已逻辑删除或对请求者不可见                                                                                 |
| `409` | `conflict`                | 项目已归档（`fields.project_id`）；并发提交同一 `Idempotency-Key` 时后到的请求（`fields.Idempotency-Key`，见文末） |
| `422` | `validation_error`        | `name` 空白或超长；`description` 超长；请求体含契约未定义字段                                                        |

**备注**：

- **幂等语义**（《S02》§2.6.1）：提供 `Idempotency-Key` 时，首次返回 `201`，同一 key（同用户、同源任务）的重复请求返回 `200` + 既有副本，**不再创建**。作用域是 `tasks.copy:<源任务 id>` 且包含用户，因此同一 key 用在别的任务或别的用户上互不影响；不提供 key 则完全不幂等。
- **重放返回的是既有副本本身**：即使副本之后被逻辑删除，重放仍返回 `200`，但对象里 `deleted_at` 非空。
- **复制范围（按实现）**：`name`（缺省加"（副本）"）、`description`、`start_date`、`end_date`、`is_shared`、**`status`、`progress`、`priority`、`is_milestone`** 一并沿用源任务；**不复制** `parent_id`、`deleted_at`、任务成员（只有复制者作为 `owner` 写入）、附件与提醒发送记录。因此源任务若是"已完成 100%"，副本一诞生也是"已完成 100%"——需要重置时请在复制后调端点 4。
- **副作用**：新任务与其 `owner` 成员关系同事务写入，并写一条 `action = created` 的操作记录，`changes` 含 `copied_from`（源任务 id），便于追溯"这条任务从哪来"。不写提醒，也不复制源任务的提醒历史。
- **并发边界**：幂等键的唯一约束在**创建副本之后**写入，因此极端的并发重试下可能返回 `409 conflict` 而副本已经创建（详见文末）。

## 8. `POST /api/v1/tasks/{task_id}/members/query`

**用途**：分页列出任务成员。
**权限**：任务可见者。不可见返回 `404`。

**路径参数**：

| 参数        | 类型         | 说明                                           |
| ----------- | ------------ | ---------------------------------------------- |
| `task_id` | string(UUID) | 任务 id；非法 UUID 返回`400 invalid_request` |

**查询参数**：无。

**请求体**（必须是 JSON 对象；`{}` 合法）：

| 字段          | 类型   | 默认                   | 约束                                                                                    | 说明           |
| ------------- | ------ | ---------------------- | --------------------------------------------------------------------------------------- | -------------- |
| `role`      | string | 不限                   | 仅`owner` \| `member`，其它值 → `400`（`fields.role`）                         | 按成员角色筛选 |
| `page`      | number | `1`                  | `>= 1`，越界 `400`                                                                  | 页码           |
| `page_size` | number | `20`                 | `1..100`，越界 `400`                                                                | 每页条数       |
| `sort`      | string | `created_at`（升序） | 白名单：`username`、`display_name`、`role`、`created_at`；未知或重复 → `400` | 排序表达式     |

**响应 `200`**：

```json
{
  "data": [
    {
      "id": "01a1290b-0ae8-7046-9106-30f2d0781610",
      "task_id": "01a1290b-0ae8-7046-9106-30f2d0781601",
      "user_id": "01a1290b-0ae8-7046-9106-30f2d07815c1",
      "role": "owner",
      "username": "alice",
      "display_name": "Alice",
      "email": "alice@example.com"
    },
    {
      "id": "01a1290b-0ae8-7046-9106-30f2d0781611",
      "task_id": "01a1290b-0ae8-7046-9106-30f2d0781601",
      "user_id": "01a1290b-0ae8-7046-9106-30f2d07815d5",
      "role": "member",
      "username": "bob",
      "display_name": "Bob",
      "email": "bob@example.com"
    }
  ],
  "pagination": {
    "page": 1,
    "page_size": 20,
    "total": 2,
    "has_next": false
  }
}
```

| 字段           | 类型   | 说明                                                     |
| -------------- | ------ | -------------------------------------------------------- |
| `data`       | array  | 任务成员对象数组（字段见"任务成员对象"）；空结果是`[]` |
| `pagination` | object | 通用分页元信息                                           |

**错误**：

| 状态码  | `error.code`              | 触发条件                                                                                                                  |
| ------- | --------------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| `400` | `invalid_request`         | 请求体为空/非 JSON/类型不符；分页越界；`sort` 未知或重复；`role` 不是 `owner`/`member`；`task_id` 不是合法 UUID |
| `401` | `authentication_required` | 未登录或会话失效                                                                                                          |
| `404` | `task_not_found`          | 任务不存在或已逻辑删除                                                                                                    |
| `404` | `project_not_found`       | 项目不可见，或任务未共享而请求者只有组内身份                                                                              |
| `422` | `validation_error`        | 请求体含契约未定义字段                                                                                                    |

**备注**：

- 默认按加入时间升序（不做"持有者优先"）；要拿持有者直接用 `role: "owner"` 筛选（恰好一条）。
- 响应不含 `created_at`（可用于 `sort`，但取不到值）。
- **任务成员不改变任务内容**：增删成员不会推进任务的 `updated_at`/`updated_by`（见端点 4 备注）。

## 9. `POST /api/v1/tasks/{task_id}/members`

**用途**：把用户加入任务，默认角色为 `member`。
**权限**：对任务有**写权限**即可——**不要求是任务持有者**（任务成员管理只要求项目成员身份）。任务不可见返回 `404 task_not_found`，可见但只读返回 `403`。

**路径参数**：

| 参数        | 类型         | 说明                                           |
| ----------- | ------------ | ---------------------------------------------- |
| `task_id` | string(UUID) | 任务 id；非法 UUID 返回`400 invalid_request` |

**查询参数**：无。

**请求体**：

| 字段        | 类型         | 必填 | 约束                                                                | 说明         |
| ----------- | ------------ | ---- | ------------------------------------------------------------------- | ------------ |
| `user_id` | string(UUID) | 是   | 目标用户必须存在且`status = active`，且**必须是该项目成员** | 要加入的用户 |

```json
{
  "user_id": "01a1290b-0ae8-7046-9106-30f2d07815d5"
}
```

**响应 `201`**：

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d0781611",
    "task_id": "01a1290b-0ae8-7046-9106-30f2d0781601",
    "user_id": "01a1290b-0ae8-7046-9106-30f2d07815d5",
    "role": "member",
    "username": "bob",
    "display_name": "Bob",
    "email": "bob@example.com"
  }
}
```

| 字段     | 类型   | 说明                                                                              |
| -------- | ------ | --------------------------------------------------------------------------------- |
| `role` | string | 恒为`member`；提升为持有者必须走端点 10（避免与"至多一个持有者"的唯一索引冲突） |
| 其余字段 | —     | 见"任务成员对象"                                                                  |

**错误**：

| 状态码  | `error.code`              | 触发条件                                                                                                                              |
| ------- | --------------------------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| `400` | `invalid_request`         | 请求体为空/非 JSON/类型不符；`task_id` 不是合法 UUID                                                                                |
| `401` | `authentication_required` | 未登录或会话失效                                                                                                                      |
| `403` | `permission_denied`       | 任务可见但请求者只有读权限                                                                                                            |
| `404` | `task_not_found`          | 任务不存在或已逻辑删除；**或任务对请求者不可见**（项目不可见、任务未共享时，成员管理端点统一按"任务不存在"处理）                |
| `404` | `user_not_found`          | `user_id` 是合法 UUID 但用户不存在（该错误**不带** `fields`）                                                               |
| `409` | `conflict`                | 项目已归档（`fields.project_id`）                                                                                                   |
| `409` | `duplicate_member`        | 该用户已是任务成员（`fields.user_id`）                                                                                              |
| `422` | `validation_error`        | 缺少`user_id`；目标用户已被停用（`fields.user_id`）；**目标用户不是项目成员**（`fields.user_id`）；请求体含契约未定义字段 |
| `500` | `internal_error`          | `user_id` 不是合法 UUID（触发 `22P02`，见文末）等未预期错误                                                                       |

**备注**：

- **任务成员必须是项目成员**（需求文档 5.4）：这条不变式在数据库层**没有**约束，完全由用例层保证，**系统管理员也不例外**（管理员也只能先加项目成员）。违反时返回 `422`（`fields.user_id`）。
- **校验顺序**：任务可见性与写权限（`404`/`403`）→ 归档（`409`）→ 目标用户存在（`404 user_not_found`）→ 目标有效（`422`）→ 目标是否为项目成员（`422`）→ 唯一约束（`409 duplicate_member`）。
- **副作用**：写一条 `action = member_added` 的操作记录（同事务），并插入一条"任务分配"提醒记录——真正的邮件由后台派发（《S02》§2.6 与需求文档 §9：发送不阻塞本次请求）。
- **不幂等**：重复添加返回 `409 duplicate_member`。若客户端需要在重试下安全，请自行先查成员列表。

## 10. `PATCH /api/v1/tasks/{task_id}/members/{user_id}`

**用途**：修改任务成员角色。`role: owner` 表示**把任务持有者身份转移给该成员**；`role: member` 表示降级。
**权限**：与端点 9 相同（对任务有写权限；**不要求是当前持有者**）。系统管理员可以转移任意任务的持有者（持有者被停用时的兜底路径）。

**路径参数**：

| 参数        | 类型         | 说明                                                     |
| ----------- | ------------ | -------------------------------------------------------- |
| `task_id` | string(UUID) | 任务 id；非法 UUID 返回`400 invalid_request`           |
| `user_id` | string(UUID) | 目标成员的用户 id；非法 UUID 返回`400 invalid_request` |

**查询参数**：无。

**请求体**：

| 字段     | 类型   | 必填 | 约束                                                          | 说明                                      |
| -------- | ------ | ---- | ------------------------------------------------------------- | ----------------------------------------- |
| `role` | string | 是   | `owner` \| `member`，其它值 → `422`（`fields.role`） | `owner` = 转移持有者；`member` = 降级 |

```json
{
  "role": "owner"
}
```

**响应 `200`**：

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d0781611",
    "task_id": "01a1290b-0ae8-7046-9106-30f2d0781601",
    "user_id": "01a1290b-0ae8-7046-9106-30f2d07815d5",
    "role": "owner",
    "username": "bob",
    "display_name": "Bob",
    "email": "bob@example.com"
  }
}
```

| 字段     | 类型   | 说明                                                |
| -------- | ------ | --------------------------------------------------- |
| `role` | string | 变更后的角色；转移成功时为目标成员的新角色`owner` |
| 其余字段 | —     | 见"任务成员对象"                                    |

**错误**：

| 状态码  | `error.code`              | 触发条件                                                                                                                                                     |
| ------- | --------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `400` | `invalid_request`         | 请求体为空/非 JSON/类型不符；`task_id` 或 `user_id` 不是合法 UUID                                                                                        |
| `401` | `authentication_required` | 未登录或会话失效                                                                                                                                             |
| `403` | `permission_denied`       | 目标成员**是唯一持有者**却被降级（`fields.user_id`）；或请求者只有读权限                                                                             |
| `404` | `task_not_found`          | 任务不存在或已逻辑删除；**或任务对请求者不可见**（成员管理端点用同一个码覆盖两种情况）                                                                 |
| `404` | `resource_not_found`      | 目标用户不是本任务成员（`fields.user_id`）。**整个转移在写入之前就被拒绝**，不会留下"现任已降级"的中间态                                             |
| `409` | `conflict`                | 项目已归档（`fields.project_id`）；目标**已经是**持有者时再次请求转移（`fields.role`）；`role: member` 但目标本来就不是持有者（`fields.role`） |
| `422` | `validation_error`        | `role` 不在枚举内；转移目标已被停用（`fields.user_id`）；缺少 `role`；请求体含契约未定义字段                                                           |

**备注**：

- **持有者唯一，且可以转移**：`task_members` 上有"每个任务至多一个 `owner`"的部分唯一索引；转移在**同一事务**内先降现任、再升目标，任何一步失败整体回滚。
- **转移目标必须有效**（`status = active`），否则任务会立刻进入"谁都改不了"的状态（`422`）。
- **已经是持有者时再次转移返回 `409`**，而不是"成功但没有变化"。
- **`role: member` 实际不会成功**：在"恰好一个持有者、不可空缺"的不变式下，目标是现任持有者时返回 `403`，目标是普通成员时返回 `409`（"该成员不是任务持有者"）。
- **转移不改变"谁能管理任务成员"**：成员管理只要求项目写权限，因此原持有者转移后仍可能继续增删成员（与项目域不同，项目域的管理权随持有者转移）。
- **副作用**：写 `owner_transferred`（转移）或 `member_role_changed`（其它变更）操作记录；**只在转移时**插入一条"持有者变更"提醒记录（普通角色调整不构成持有者变更）。
- **不推进任务的 `updated_at`/`updated_by`**：成员关系不属于"任务本身的修改"。

## 11. `DELETE /api/v1/tasks/{task_id}/members/{user_id}`

**用途**：把成员移出任务（删除成员关系行）。
**权限**：对任务有写权限（与端点 9 相同）。

**路径参数**：

| 参数        | 类型         | 说明                                                     |
| ----------- | ------------ | -------------------------------------------------------- |
| `task_id` | string(UUID) | 任务 id；非法 UUID 返回`400 invalid_request`           |
| `user_id` | string(UUID) | 目标成员的用户 id；非法 UUID 返回`400 invalid_request` |

**查询参数**：无。

**请求体**：无请求体（服务端不读取请求体）。

**响应 `204`**：无响应体（不是 `{"data": ...}`）。

**错误**：

| 状态码  | `error.code`              | 触发条件                                                                                     |
| ------- | --------------------------- | -------------------------------------------------------------------------------------------- |
| `400` | `invalid_request`         | `task_id` 或 `user_id` 不是合法 UUID                                                     |
| `401` | `authentication_required` | 未登录或会话失效                                                                             |
| `403` | `permission_denied`       | 目标成员是任务的**唯一持有者**（`fields.user_id`）；或请求者只有读权限               |
| `404` | `task_not_found`          | 任务不存在或已逻辑删除；**或任务对请求者不可见**（成员管理端点用同一个码覆盖两种情况） |
| `404` | `resource_not_found`      | 目标用户不是本任务成员（`fields.user_id`）                                                 |
| `409` | `conflict`                | 项目已归档（`fields.project_id`）                                                          |

**备注**：

- **没有软移除**：删除即删行；用户账号与个人数据不受影响。被移除者立刻失去该任务的 `membership_role`，并可能不再收到该任务的提醒。
- 不得移除唯一持有者（`403`）；要换人请用端点 10 转移。
- **不幂等**：对已不是成员的用户再次调用返回 `404 resource_not_found`，不是 `204`。
- **副作用**：写一条 `action = member_removed` 的操作记录（同事务）；不插入提醒，也不推进 `updated_at`/`updated_by`。

## 实现边界与兼容事项

| 点                                                                           | 说明                                                                                                                                                                                                                                                                                                                                                              |
| ---------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 创建任务/改日期时省略`start_date` / `end_date`（契约未规定，按实现描述） | 端点 1（创建）与端点 5（改日期）的日期字段都没有必填标记，也没有默认值：省略时按 Go 零值日期`0001-01-01` 写入（`PATCH .../dates` 只给一个日期会让另一端变成 `0001-01-01`），而数据库的 `end_date >= start_date` 仍然成立，因此返回 `2xx`。端点 7（复制）不受影响：未提供的日期沿用源任务。《S02》§4.6 未规定省略行为。**建议前端恒传两个日期**。 |
| `parent_id` / `user_id` 不是合法 UUID                                    | 本域只有**路径参数**（`task_id`、`user_id`）与列表筛选参数（`owner_id`、`assignee_id`）做了前置 UUID 校验（`400`）。请求体里的 `parent_id`（端点 1、4）与 `user_id`（端点 9）**没有**前置校验：非 UUID 字符串会直达数据库并触发 PostgreSQL `22P02`，兜底为 `500 internal_error`（与《A07》记录的"附件域路径参数"情形同类）。        |
| 创建者是否必须是项目成员                                                     | 实现的校验只覆盖"对项目有写权限"：`group_editable` 的组内活跃成员即便不是项目成员也能创建任务并成为其唯一持有者；没有"必须是项目成员"这一限制。前端若需要该语义，要在客户端自行约束，或提出需求变更（改代码 + 补《S02》）。                                                                                                                                     |
| 复制任务的字段范围                                                           | 需求只说"复制基础字段、不复制提醒发送记录"，未逐字段列明。实现复制的是整个任务行，再清空`id`/`created_by`/`deleted_at`/`parent_id`，因此 `status`、`progress`、`priority`、`is_milestone` 也一并沿用源任务；成员只写入复制者（`owner`），附件不复制。前端要"复制成一个未开始的任务"必须自己再调端点 4。                                         |
| 并发使用同一`Idempotency-Key`                                              | 幂等键的唯一约束在**创建副本之后**写入：并发重试时落后的请求会拿到 `409 conflict`（`fields.Idempotency-Key`），而副本可能已经存在。契约只规定首次 `201`、重放 `200`，未规定并发下的结果。                                                                                                                                                           |
| 重放返回已删除的副本                                                         | 幂等键命中时按"包含已逻辑删除"读取既有任务，因此副本之后被删除，重放仍返回`200` 且对象里 `deleted_at` 非空（而不是 `404`）。契约未规定。                                                                                                                                                                                                                    |
| 组内只读查看者的`is_shared`                                                | 对`visibility = group_readonly` 的组员，列表端点**强制**按 `is_shared = true` 过滤，即使请求里写 `is_shared: false`，响应也不说明这一点。契约未明确。                                                                                                                                                                                                 |
| `role: member` 的可用性                                                    | 见端点 10 备注：因为持有者恰好一个，`role: member` 永远不能成功（`403` 或 `409`）。契约只写了"不得降级唯一持有者"，未说明对普通成员返回 `409`。                                                                                                                                                                                                           |
| "无权"与"不可见"的错误码                                                     | 端点 4/5/6 对"任务不可见"返回`404 task_not_found`、对"项目不可见/任务未共享"返回 `404 project_not_found`、对"可见但只读"返回 `403 permission_denied`；端点 7（复制）把"不可见"也归到 `404 task_not_found`；成员管理端点（9/10/11）无写权限时一律 `404 task_not_found`（不再区分项目级不可见）。契约未统一这些区分规则，前端需按接口处理。               |
| 日期/时间字符串格式非法的错误码                                              | `httpapi.Date` / `Timestamp` 的自定义解析错误既不是 `json.SyntaxError` 也不是 `UnmarshalTypeError`，会落到请求体解析的兜底分支：`400 invalid_request`，`fields.body = "请求体无法解析"`——**具体字段名不会出现在 `fields` 里**。《S02》§2.7 只规定了"未知字段"（`422`）与"多个 JSON 值"（`400`），未规定这一情形。                      |
| 端点的`updated_by` 与计划视图的 `updated_by_name`                        | 任务端点只返回`updated_by`（UUID），`updated_by_name` 只在计划视图（《A06》）里提供。若前端要在任务详情/列表直接显示"最后修改人"，需要额外查用户或用计划视图。契约未说明任务域是否应补 `updated_by_name`。                                                                                                                                                  |
