# A04 Projects 接口详解

> 归属：**前后端共用**（`docs/api/`）。通用约定（基础路径 `/api/v1`、成功/错误信封、认证、分页、并发与幂等、限流、未知字段拒绝）见《S02-HTTP接口契约》§2，本文不重复。
> 契约的**约定性叙述**（信封、认证、分页、并发与幂等、限流、处理顺序、加强项）见《S02》；本文补齐每个接口的**请求与响应数据结构**。

> **2026-10-20 规划（未生效）**：项目成员对象将增加 `avatar_updated_at`；项目域不新增头像端点。
> 见 [P01](../P01-Worklane-规划-2026-10-20.md) §4。

全部 9 个端点都要求登录（会话 Cookie，见《S02》§2.2）；未登录返回 `401 authentication_required`。
下文各错误表不再重复这一行以外的通用约定；未列出的 `500 internal_error` 属于未预期服务端错误（《S02》§2.4）。

## 接口清单

| # | 方法 | 路径 | 权限 | 说明 |
| --- | --- | --- | --- | --- |
| 1 | `POST` | `/api/v1/projects` | 登录用户，且是归属工作组的**活跃成员**（或该组所有者） | 创建项目，创建者自动成为持有者与成员 |
| 2 | `POST` | `/api/v1/projects/query` | 登录用户（可见范围见下；系统管理员可见全部项目） | 项目列表：筛选 + 分页 + 排序 |
| 3 | `GET` | `/api/v1/projects/{project_id}` | 项目可见者 | 项目详情（含派生 `progress`、`overdue_task_count`） |
| 4 | `PATCH` | `/api/v1/projects/{project_id}` | 持有者或系统管理员 | 修改项目字段；`expected_updated_at` 必填 |
| 5 | `POST` | `/api/v1/projects/{project_id}/archive` | 持有者或系统管理员 | 归档（此后项目只读） |
| 6 | `POST` | `/api/v1/projects/{project_id}/members/query` | 项目可见者 | 项目成员列表 |
| 7 | `POST` | `/api/v1/projects/{project_id}/members` | 持有者或系统管理员 | 添加成员（一律先按 `member` 加入） |
| 8 | `PATCH` | `/api/v1/projects/{project_id}/members/{user_id}` | 持有者或系统管理员 | 改成员角色；`role: owner` = **转移持有者** |
| 9 | `DELETE` | `/api/v1/projects/{project_id}/members/{user_id}` | 持有者或系统管理员 | 移除成员（删行）；不得移除唯一持有者 |

**权限速查**（角色枚举与判定规则见《S02》§2.2 与《S02》§5，此处只把映射摆成表格）：

| 请求者与项目的关系 | 能否读到项目 | 能否改项目本身（名称/日期/共享级别/成员） |
| --- | --- | --- |
| 项目持有者（`project_members.role = owner`） | 能 | 能 |
| 项目普通成员 | 能 | 不能 |
| 归属组活跃成员，`visibility = group_readonly` | 能（含已共享任务） | 不能 |
| 归属组活跃成员，`visibility = group_editable` | 能 | 不能（可改**已共享**任务，但不能改项目共享权限） |
| 归属组活跃成员，`visibility = private` | 不能（`404`） | 不能 |
| 与项目无关的其它登录用户 | 不能（详情 `404`，列表里不出现） | 不能 |
| 系统管理员（不要求成员/组员身份） | 能 | 能 |
| 归档项目（对所有人，含系统管理员） | 能（只读） | 不能（`409`） |

两点容易踩错，先说明：

- **"不可见"在读取路径上统一是 `404 project_not_found`**（详情、成员列表），不区分"项目不存在"与"我没权限看"（避免泄漏存在性）。
- **写入路径的错误码有两种口径**：项目更新（4）与归档（5）对"不是持有者也不是系统管理员"返回 `403 permission_denied`——**即使项目对请求者完全不可见**，只要项目行存在就是 `403`（只有项目 id 不存在才是 `404`）；成员管理（7/8/9）则把管理权不足一律按"不可见即不存在"处理，返回 `404 project_not_found`。前端要按接口分别处理。

## 项目对象

端点 1/3/4/5 的 `data` 与端点 2 的 `data[]` 元素都是同一个结构：

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d07815af",
    "name": "核心系统迁移",
    "group_id": "01a1290b-0ae8-7046-9106-30f2d07815b0",
    "description": null,
    "created_by": "01a1290b-0ae8-7046-9106-30f2d07815c1",
    "status": "not_started",
    "visibility": "private",
    "start_date": "2026-10-01",
    "end_date": "2026-10-31",
    "color": "#336699",
    "archived_at": null,
    "progress": 0,
    "overdue_task_count": 0,
    "membership_role": "owner",
    "updated_at": "2026-10-11T04:30:54.047739Z"
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string(UUID) | 项目 id |
| `name` | string | 名称，1–200 字符（按 Unicode 码点计） |
| `group_id` | string(UUID) | 归属工作组；项目必须且只能归属一个组 |
| `description` | string \| null | 描述，≤4000 字符；未填写时为 `null` |
| `created_by` | string(UUID) | 创建者用户 id。**它不是持有者**——创建者可以在端点 8 把持有者转给别人 |
| `status` | string | `not_started` \| `in_progress` \| `paused` \| `completed` \| `archived` \| `cancelled` |
| `visibility` | string | `private` \| `group_readonly` \| `group_editable` |
| `start_date` / `end_date` | string(date) | `YYYY-MM-DD`；恒有 `end_date >= start_date` |
| `color` | string \| null | 展示色（自由文本，≤32 字符，如 `#336699`） |
| `archived_at` | string(timestamp) \| null | 归档瞬时时间；"是否归档"的判定是 `status == "archived"` **或** `archived_at != null`（任一成立即视为归档） |
| `progress` | number | **派生**：有效任务进度的简单平均值（0–100 整数）。已取消与已逻辑删除的任务不参与；无有效任务时为 `0` |
| `overdue_task_count` | number | **派生**：`end_date < 今天` 且状态为 `not_started`/`in_progress` 的未删除任务数。"今天"按**请求者时区**计算（《S02》§5 的加强项） |
| `membership_role` | string \| null | **派生**：当前请求者在本项目的角色（`owner`/`member`）；非项目成员（含仅靠组内共享可见者、以及不具成员身份的系统管理员）为 `null` |
| `updated_at` | string(timestamp) | UTC ISO 8601，**带亚秒精度**；更新类接口要求把它原样回传为 `expected_updated_at`（《S02》§2.6.2） |

刻意**不返回**：

- `created_at`：项目详情与列表都不返回。`sort` 允许按 `created_at` 排序，但拿不到该值（要按时间展示只能自己记录）。
- `is_archived` / `is_read_only` / `my_role` / `owner_id` / `task_count`：这些是**计划视图**的投影字段（《A06》），`/projects/*` 不返回。判断归档用上面那条规则；要拿持有者请在成员列表里找 `role == "owner"`。

## 项目成员对象

端点 7/8 的 `data` 与端点 6 的 `data[]` 元素：

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d07815d2",
    "project_id": "01a1290b-0ae8-7046-9106-30f2d07815af",
    "user_id": "01a1290b-0ae8-7046-9106-30f2d07815c1",
    "role": "owner",
    "username": "alice",
    "display_name": "Alice",
    "email": "alice@example.com"
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string(UUID) | 成员关系行 id（不是用户 id） |
| `project_id` | string(UUID) | 项目 id |
| `user_id` | string(UUID) | 成员用户 id |
| `role` | string | `owner` \| `member`；一个项目**恰好一个** `owner` |
| `username` | string | 用户名（登录标识） |
| `display_name` | string | 显示名 |
| `email` | string | 邮箱 |

刻意**不返回** `created_at`（加入时间）：它可用于 `sort`，但不在响应里。项目成员关系也没有 `status` 字段——项目成员**没有软移除**，移除即删行（端点 9）。

## 1. `POST /api/v1/projects`

**用途**：在指定工作组下新建项目，创建者在同一事务内成为项目持有者与成员。
**权限**：登录用户，且**必须是归属工作组的活跃成员**（或该组的所有者）。系统管理员在此**不豁免**：它要求的是一层业务前置条件（否则会创建出"自己也看不见的项目"），而不是对已有项目的权限判定。

**路径参数**：无。

**查询参数**：无。

**请求体**：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `name` | string | 是 | 去首尾空白后非空，1–200 字符 | 项目名称 |
| `group_id` | string(UUID) | 是 | 组必须存在且状态为 `active` | 归属工作组 |
| `start_date` | string(date) | 否（见备注） | `YYYY-MM-DD`，不得晚于 `end_date` | 开始日期 |
| `end_date` | string(date) | 否（见备注） | `YYYY-MM-DD`，不得早于 `start_date` | 结束日期 |
| `description` | string \| null | 否 | ≤4000 字符；空串按"未提供"处理（存 `null`） | 描述 |
| `visibility` | string | 否（默认 `private`） | `private` \| `group_readonly` \| `group_editable` | 共享级别 |
| `color` | string \| null | 否 | ≤32 字符；空串按"未提供"处理 | 展示色 |

请求体示例：

```json
{
  "name": "核心系统迁移",
  "group_id": "01a1290b-0ae8-7046-9106-30f2d07815b0",
  "start_date": "2026-10-01",
  "end_date": "2026-10-31",
  "visibility": "group_readonly",
  "color": "#336699"
}
```

**响应 `201`**：

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d07815af",
    "name": "核心系统迁移",
    "group_id": "01a1290b-0ae8-7046-9106-30f2d07815b0",
    "description": null,
    "created_by": "01a1290b-0ae8-7046-9106-30f2d07815c1",
    "status": "not_started",
    "visibility": "group_readonly",
    "start_date": "2026-10-01",
    "end_date": "2026-10-31",
    "color": "#336699",
    "archived_at": null,
    "progress": 0,
    "overdue_task_count": 0,
    "membership_role": "owner",
    "updated_at": "2026-10-11T04:30:54.047739Z"
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string(UUID) | 新项目 id |
| `status` | string | 固定 `not_started` |
| `visibility` | string | 请求提供的值；省略时为 `private` |
| `archived_at` | null | 新项目未归档 |
| `progress` / `overdue_task_count` | number | 恒为 `0`（新项目没有任务） |
| `membership_role` | string | 固定 `owner`（创建者即持有者） |
| 其余字段 | — | `name`/`description`/`created_by`/`group_id`/`start_date`/`end_date`/`color`/`updated_at`，语义见"项目对象" |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | 请求体为空、不是合法 JSON、含多个 JSON 值、或字段类型不符（`fields.body` 或相应字段名） |
| `400` | `invalid_date_range` | `start_date` 晚于 `end_date`（`fields.start_date`、`fields.end_date`） |
| `401` | `authentication_required` | 未登录或会话失效 |
| `403` | `permission_denied` | 创建者不是归属工作组的活跃成员、也不是该组所有者（`fields.group_id`） |
| `409` | `conflict` | 归属工作组已停用（`fields.group_id`） |
| `422` | `validation_error` | 缺少 `name` / `group_id`；`name` 为空白或超过 200 字符；`description`、`color` 超长；`visibility` 不在枚举内；归属工作组不存在或不可见（`fields.group_id`）；请求体含契约未定义字段（《S02》§2.7） |
| `500` | `internal_error` | `group_id` 不是合法 UUID（该字符串会直达数据库并触发 `22P02`，见文末）等未预期错误 |

**备注**：

- **校验顺序**（决定你看到哪个错误）：绑定请求体（`400`/`422`）→ 字段归一化（`400 invalid_date_range` 优先于 `422`）→ 归属组存在性/状态/成员身份（`422`/`409`/`403`）→ 写库。
- **副作用**：创建项目与写入 `project_members(owner)` 在**同一事务**内完成，不存在"有项目但没有持有者"的中间状态；同一事务内还写一条操作记录（`object_type = project`、`action = created`，见《A09》）。不产生任何提醒。
- **幂等**：不幂等，也不接受 `Idempotency-Key`；重复提交会创建两个项目。
- **`start_date`/`end_date` 必须由客户端提供**：字段在实现里没有"必填"标记，也没有默认值，省略时按 Go 零值日期 `0001-01-01` 写入并返回 `201`（数据库约束仍然成立）。契约未规定省略行为，详见文末"不确定或契约未写清的点"。

## 2. `POST /api/v1/projects/query`

**用途**：分页查询当前用户可见的项目，支持按归属组、状态、持有者与名称筛选。
**权限**：登录用户。可见范围＝"我是成员的项目"∪"归属组的活跃成员且 `visibility` 非 `private` 的项目"∪（系统管理员）全部项目。**不**额外按 `created_by` 放宽：把项目移交出去并被移出成员后，就看不到它了。

**路径参数**：无。

**查询参数**：无（筛选、分页与排序都在 JSON 请求体里）。

**请求体**（必须是 JSON 对象；`{}` 合法，完全没有请求体返回 `400 invalid_request`，`fields.body`）：

| 字段 | 类型 | 默认 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `group_id` | string(UUID) | 不限 | 非法 UUID → `400`（`fields.group_id`） | 按归属工作组筛选 |
| `status` | string | 不限 | 六种项目状态之一，其它值 → `400`（`fields.status`） | 按项目状态筛选 |
| `owner_id` | string(UUID) | 不限 | 非法 UUID → `400`（`fields.owner_id`） | 按**项目持有者**筛选（与"当前用户可见性"独立判定，互不污染） |
| `name` | string | 不限 | 无长度限制；`%` `_` `\` 按**字面量**处理 | 名称包含匹配（大小写不敏感） |
| `include_archived` | boolean | `false` | — | 为真时把归档项目纳入候选集 |
| `page` | number | `1` | `>= 1`，越界 `400` | 页码 |
| `page_size` | number | `20` | `1..100`，越界 `400` | 每页条数 |
| `sort` | string | `end_date`（升序） | 白名单见下；未知或重复字段 → `400`（`fields.sort`） | 排序表达式，逗号分隔多字段，前缀 `-` 表示降序 |

`sort` 白名单：`name`、`status`、`visibility`、`start_date`、`end_date`、`created_at`、`updated_at`。
服务端始终追加 `id` 作为次级排序键，因此翻页不重不漏（《S02》§2.5）。

请求体示例：

```json
{
  "group_id": "01a1290b-0ae8-7046-9106-30f2d07815b0",
  "status": "in_progress",
  "owner_id": "01a1290b-0ae8-7046-9106-30f2d07815c1",
  "name": "迁移",
  "include_archived": false,
  "page": 1,
  "page_size": 20,
  "sort": "-end_date"
}
```

**响应 `200`**：

```json
{
  "data": [
    {
      "id": "01a1290b-0ae8-7046-9106-30f2d07815af",
      "name": "核心系统迁移",
      "group_id": "01a1290b-0ae8-7046-9106-30f2d07815b0",
      "description": null,
      "created_by": "01a1290b-0ae8-7046-9106-30f2d07815c1",
      "status": "in_progress",
      "visibility": "private",
      "start_date": "2026-10-01",
      "end_date": "2026-10-31",
      "color": null,
      "archived_at": null,
      "progress": 40,
      "overdue_task_count": 1,
      "membership_role": "owner",
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

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `data` | array | 项目对象数组（见"项目对象"）；空结果是 `[]` 而不是 `null` |
| `pagination` | object | `page`/`page_size`/`total`/`has_next`；`has_next = page * page_size < total`（《S02》§2.3） |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | 请求体为空/非 JSON 对象/字段类型不符；`page`、`page_size` 越界；`sort` 含未知或重复字段；`status` 不在枚举内；`group_id`、`owner_id` 不是合法 UUID |
| `401` | `authentication_required` | 未登录或会话失效 |
| `422` | `validation_error` | 请求体含契约未定义的字段（《S02》§2.7） |

**备注**：

- **归档项目默认排除**：筛选条件是 `archived_at IS NULL AND status <> 'archived'`，显式 `include_archived: true` 即可查到（需求文档 5.3：历史数据可查询）。
- **组合筛选不会互相污染**：每个条件各用一个独立子查询（持有者条件与可见性条件分开），因此"按持有者 + 按状态"这类组合能正常命中。
- **`pagination.total` 与返回条目数一致**：计数查询与列表查询复用同一套筛选条件。
- 系统管理员不再按成员关系过滤（否则连"要处理哪个项目"都找不到）。
- 名称搜索里的 `%`、`_`、`\` 已转义，按字面量匹配（输入 `%` 不会匹配全部）。

## 3. `GET /api/v1/projects/{project_id}`

**用途**：读取项目详情，含派生进度与逾期任务数、以及当前请求者在本项目的角色。
**权限**：项目可见者（项目成员 / 归属组活跃成员且共享级别非 `private` / 系统管理员）。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `project_id` | string(UUID) | 项目 id；不是合法 UUID 时返回 `400 invalid_request`（`fields.project_id`） |

**查询参数**：无（多余查询参数被忽略，本域 GET 端点不解析查询串）。

**请求体**：无请求体。

**响应 `200`**：项目对象（结构见上），其中 `progress`、`overdue_task_count`、`membership_role` 为派生字段。

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d07815af",
    "name": "核心系统迁移",
    "group_id": "01a1290b-0ae8-7046-9106-30f2d07815b0",
    "description": null,
    "created_by": "01a1290b-0ae8-7046-9106-30f2d07815c1",
    "status": "in_progress",
    "visibility": "private",
    "start_date": "2026-10-01",
    "end_date": "2026-10-31",
    "color": null,
    "archived_at": null,
    "progress": 40,
    "overdue_task_count": 1,
    "membership_role": "owner",
    "updated_at": "2026-10-11T04:30:54.047739Z"
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| （全部字段） | — | 与"项目对象"逐字段一致；本端点只按"单个项目"返回，`membership_role` 反映请求者在本项目的角色，非成员为 `null` |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | `project_id` 不是合法 UUID |
| `401` | `authentication_required` | 未登录或会话失效 |
| `404` | `project_not_found` | 项目不存在，或对当前用户不可见（两者同码，不带 `fields`） |

**备注**：

- **"不可见"与"不存在"必须同码**：私有项目对非成员一律 `404`，否则任意登录用户都能探测项目是否存在。
- 本端点的 `progress`/`overdue_task_count` 基于项目的**全部**未删除任务；组内共享视图（《A06》的 `GET /plan-views/groups/{group_id}`）按**该视图可见的**任务计算，两者口径可能不同（这是刻意的，避免用进度数值泄漏未共享任务的进展）。
- 归档项目仍可正常读取（端点 5 只让写入失败）。

## 4. `PATCH /api/v1/projects/{project_id}`

**用途**：修改项目的名称、描述、状态、共享级别、日期区间与颜色。
**权限**：项目持有者或系统管理员（组内共享级别**不**授予项目管理权，需求文档 4.3）。可见但无管理权返回 `403`。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `project_id` | string(UUID) | 项目 id；非法 UUID 返回 `400 invalid_request` |

**查询参数**：无。

**请求体**：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `name` | string | 否 | 去首尾空白后非空，1–200 字符 | 项目名称 |
| `description` | string \| null | 否 | ≤4000 字符；**空串表示清空**（存 `null`） | 描述 |
| `status` | string | 否 | 六种项目状态之一（枚举**包含** `archived`） | 状态 |
| `visibility` | string | 否 | `private` \| `group_readonly` \| `group_editable` | 共享级别 |
| `start_date` | string(date) | 否 | 与现有 `end_date` 合并后必须 `start <= end` | 开始日期 |
| `end_date` | string(date) | 否 | 与现有 `start_date` 合并后必须 `start <= end` | 结束日期 |
| `color` | string \| null | 否 | ≤32 字符；**空串表示清空** | 展示色 |
| `expected_updated_at` | string(timestamp) | **是** | 必须等于当前 `updated_at`（UTC ISO 8601，原样回传） | 乐观并发版本 |

请求体示例：

```json
{
  "end_date": "2026-11-30",
  "visibility": "group_editable",
  "expected_updated_at": "2026-10-11T04:30:54.047739Z"
}
```

**响应 `200`**：项目对象，`updated_at` 已推进为本次写入时间。

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d07815af",
    "name": "核心系统迁移",
    "group_id": "01a1290b-0ae8-7046-9106-30f2d07815b0",
    "description": null,
    "created_by": "01a1290b-0ae8-7046-9106-30f2d07815c1",
    "status": "in_progress",
    "visibility": "group_editable",
    "start_date": "2026-10-01",
    "end_date": "2026-11-30",
    "color": null,
    "archived_at": null,
    "progress": 40,
    "overdue_task_count": 0,
    "membership_role": "owner",
    "updated_at": "2026-10-11T06:02:11.512004Z"
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| （全部字段） | — | 与"项目对象"一致；未提供的字段保持原值，`updated_at` 一定变化 |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | 请求体为空/非 JSON/字段类型不符；`project_id` 不是合法 UUID |
| `400` | `invalid_date_range` | **合并后**的日期区间倒置（只改 `end_date` 也会被判出来；`fields.start_date`、`fields.end_date`） |
| `401` | `authentication_required` | 未登录或会话失效 |
| `403` | `permission_denied` | 项目行存在，但请求者既不是持有者也不是系统管理员（**项目对请求者不可见时同样是 `403`**，不带 `fields`） |
| `404` | `project_not_found` | 项目 id 在库中不存在 |
| `409` | `conflict` | 项目处于归档只读状态（`fields.project_id`） |
| `409` | `version_conflict` | `expected_updated_at` 与当前版本不一致 |
| `422` | `validation_error` | `name` 空白或超长、`status`/`visibility` 不在枚举内、`description`/`color` 超长；缺少 `expected_updated_at`；请求体含契约未定义字段 |

**备注**：

- **归档只读**：归档项目上除"显式提供 `status`"以外的任何修改都返回 `409 conflict`（`fields.project_id`）。这一例外只跳过只读检查，**不会清空 `archived_at`**，因此项目仍然是归档状态（仍只读、仍默认不在列表里）——详见文末。
- **空写入会成功**：只提交 `expected_updated_at`（不带任何可改字段）也会返回 `200` 并推进 `updated_at`。服务端**没有**"至少一个可改字段"的校验（该规则只见于 Users 域的 `PATCH /users/{user_id}`，《A02》），因此不要让前端在"什么都没改"时提交请求，否则别人的版本会无谓失效。
- **清空语义**：`description`、`color` 传空串等价于置 `null`（响应里为 `null`）。这一点与 Tasks 域的 `description` 不同（见《A05》端点 4）。
- **版本校验是原子 CAS**：版本条件写在 `UPDATE ... WHERE updated_at = $n` 里，`0` 行更新后再补一次纯存在性查询，用于区分 `404`（不存在）与 `409 version_conflict`（《S02》§2.6）。
- **副作用**：写一条 `action = updated` 的操作记录；本次包含 `visibility` 时额外写一条 `action = shared` 记录（便于回答"为什么这个项目突然对全组可见"）。两者与业务写在同事务。
- **注意 `status: "archived"`**：枚举里包含它，因此本端点也能把项目改成归档状态，但**不会写 `archived_at`**。正式归档入口是端点 5。

## 5. `POST /api/v1/projects/{project_id}/archive`

**用途**：归档项目——把 `status` 置为 `archived` 并写入 `archived_at`，此后项目及其下任务只读。
**权限**：项目持有者或系统管理员。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `project_id` | string(UUID) | 项目 id；非法 UUID 返回 `400 invalid_request` |

**查询参数**：无。

**请求体**：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `expected_updated_at` | string(timestamp) | **是** | 必须等于当前 `updated_at` | 乐观并发版本（归档是版本化写入） |

```json
{
  "expected_updated_at": "2026-10-11T04:30:54.047739Z"
}
```

**响应 `200`**：

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d07815af",
    "name": "核心系统迁移",
    "group_id": "01a1290b-0ae8-7046-9106-30f2d07815b0",
    "description": null,
    "created_by": "01a1290b-0ae8-7046-9106-30f2d07815c1",
    "status": "archived",
    "visibility": "private",
    "start_date": "2026-10-01",
    "end_date": "2026-10-31",
    "color": null,
    "archived_at": "2026-10-11T06:10:00Z",
    "progress": 40,
    "overdue_task_count": 1,
    "membership_role": "owner",
    "updated_at": "2026-10-11T06:10:00Z"
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `status` | string | `archived`（端点 5 是唯一会同时写 `status` 与 `archived_at` 的入口） |
| `archived_at` | string(timestamp) | 非空；与 `status` 一起表达"已归档" |
| 其余字段 | — | 与"项目对象"一致 |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | 请求体为空/非 JSON/字段类型不符；`project_id` 不是合法 UUID |
| `401` | `authentication_required` | 未登录或会话失效 |
| `403` | `permission_denied` | 项目行存在，但请求者既不是持有者也不是系统管理员（**项目不可见时同样是 `403`**，不带 `fields`） |
| `404` | `project_not_found` | 项目 id 在库中不存在 |
| `409` | `conflict` | 项目**已经归档**（`fields.project_id` 说明原因），不会报成"成功但没有变化" |
| `409` | `version_conflict` | `expected_updated_at` 不匹配 |
| `422` | `validation_error` | 缺少 `expected_updated_at`；请求体含契约未定义字段 |

**备注**：

- **归档不可逆**：没有"解除归档"端点，且 `archived_at` 一旦写入不会被任何端点清空。前端不要把归档设计成可撤销操作（细节见文末）。
- **归档后的行为**（读取全部正常，写入全部失败）：修改项目、创建/修改/删除其下任务、改任务日期、改项目或任务成员一律 `409 conflict`（`fields.project_id`）；项目默认从列表排除，`include_archived: true` 可见（其任务用 `POST /projects/{id}/tasks/query` 仍可查，任务列表不排除归档项目）。
- **对系统管理员同样只读**：管理员放行只豁免权限判定，不豁免状态规则与不变式（《S02》§5）。
- **副作用**：写一条 `action = archived` 的操作记录（同事务）。

## 6. `POST /api/v1/projects/{project_id}/members/query`

**用途**：分页列出项目成员。
**权限**：项目可见者（成员 / 组内共享可见者 / 系统管理员）。不可见返回 `404`。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `project_id` | string(UUID) | 项目 id；非法 UUID 返回 `400 invalid_request` |

**查询参数**：无。

**请求体**（必须是 JSON 对象；`{}` 合法）：

| 字段 | 类型 | 默认 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `role` | string | 不限 | 仅 `owner` \| `member`，其它值 → `400`（`fields.role`） | 按成员角色筛选 |
| `page` | number | `1` | `>= 1`，越界 `400` | 页码 |
| `page_size` | number | `20` | `1..100`，越界 `400` | 每页条数 |
| `sort` | string | `created_at`（升序） | 白名单：`username`、`display_name`、`role`、`created_at`；未知或重复 → `400` | 排序表达式 |

**响应 `200`**：

```json
{
  "data": [
    {
      "id": "01a1290b-0ae8-7046-9106-30f2d07815d2",
      "project_id": "01a1290b-0ae8-7046-9106-30f2d07815af",
      "user_id": "01a1290b-0ae8-7046-9106-30f2d07815c1",
      "role": "owner",
      "username": "alice",
      "display_name": "Alice",
      "email": "alice@example.com"
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

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `data` | array | 项目成员对象数组（字段见"项目成员对象"）；空结果是 `[]` |
| `pagination` | object | 通用分页元信息 |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | 请求体为空/非 JSON/类型不符；分页越界；`sort` 未知或重复；`role` 不是 `owner`/`member`；`project_id` 不是合法 UUID |
| `401` | `authentication_required` | 未登录或会话失效 |
| `404` | `project_not_found` | 项目不存在或不可见 |
| `422` | `validation_error` | 请求体含契约未定义字段 |

**备注**：

- **不做"持有者优先"排序**：默认按加入时间升序。`sort: "role"` 是字符串排序（升序时 `member` 排在 `owner` 之前），要"持有者排最前"请在展示层自行处理，或直接用 `role: "owner"` 过滤（恰好一条）。
- **已停用用户仍在列表里**：成员查询不过滤 `users.status`；项目成员关系**没有** `status` 字段（不同于工作组的软移除）。若某成员账号被停用，他依然占一行。
- 响应不含 `created_at`，虽然可以按它排序。

## 7. `POST /api/v1/projects/{project_id}/members`

**用途**：把用户加入项目，新成员一律先按 `member` 加入。
**权限**：项目持有者或系统管理员。**管理权不足（含项目对请求者不可见、或仅可读）返回 `404 project_not_found`**，而不是 `403`。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `project_id` | string(UUID) | 项目 id；非法 UUID 返回 `400 invalid_request` |

**查询参数**：无。

**请求体**：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `user_id` | string(UUID) | 是 | 目标用户必须存在且 `status = active`；不得已是本项目成员 | 要加入的用户 |

```json
{
  "user_id": "01a1290b-0ae8-7046-9106-30f2d07815c1"
}
```

**响应 `201`**：

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d07815d2",
    "project_id": "01a1290b-0ae8-7046-9106-30f2d07815af",
    "user_id": "01a1290b-0ae8-7046-9106-30f2d07815c1",
    "role": "member",
    "username": "bob",
    "display_name": "Bob",
    "email": "bob@example.com"
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `role` | string | 恒为 `member`。**提升为持有者必须走端点 8**，避免"添加即持有者"与"至多一个持有者"的唯一索引冲突 |
| 其余字段 | — | 见"项目成员对象" |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | 请求体为空/非 JSON/类型不符；`project_id` 不是合法 UUID |
| `401` | `authentication_required` | 未登录或会话失效 |
| `404` | `project_not_found` | 项目不存在、不可见，或请求者不是持有者/系统管理员（管理权不足按"不存在"处理） |
| `404` | `user_not_found` | `user_id` 是合法 UUID 但用户不存在（该错误**不带** `fields`） |
| `409` | `conflict` | 项目处于归档只读状态（`fields.project_id`） |
| `409` | `duplicate_member` | 该用户已是项目成员（`fields.user_id`） |
| `422` | `validation_error` | 缺少 `user_id`；目标用户已被停用（`fields.user_id`）；请求体含契约未定义字段 |
| `500` | `internal_error` | `user_id` 不是合法 UUID（直达数据库触发 `22P02`，见文末）；写库失败等未预期错误 |

**备注**：

- **成员可以来自归属工作组之外**（需求文档 5.3）：数据库层只有一个指向 `users` 的外键，没有组归属复合外键。
- **目标用户必须有效**：把已停用账号拉进项目会让"谁都能看但只有他能改"的判定失去意义（`422`）。
- **校验顺序**：管理权（`404`）→ 归档（`409`）→ 目标用户存在（`404 user_not_found`）→ 目标有效（`422`）→ 唯一约束（`409 duplicate_member`）。
- **副作用**：写一条 `action = member_added` 的操作记录（同事务）；不产生提醒（提醒只属于 Tasks 域）。
- **不幂等**：重复提交返回 `409 duplicate_member`，不是 `201`。

## 8. `PATCH /api/v1/projects/{project_id}/members/{user_id}`

**用途**：修改成员角色。`role: owner` 表示**把项目持有者身份转移给该成员**（不是"提升"）；`role: member` 表示降级。
**权限**：项目持有者或系统管理员。无管理权返回 `404 project_not_found`（系统管理员不需要是成员，因此也用它兜底无人可管的异常项目）。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `project_id` | string(UUID) | 项目 id；非法 UUID 返回 `400 invalid_request` |
| `user_id` | string(UUID) | 目标成员的用户 id；非法 UUID 返回 `400 invalid_request` |

**查询参数**：无。

**请求体**：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `role` | string | 是 | `owner` \| `member`，其它值 → `422`（`fields.role`） | `owner` = 转移持有者；`member` = 降级 |

转移持者请求示例：

```json
{
  "role": "owner"
}
```

**响应 `200`**：

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d07815d2",
    "project_id": "01a1290b-0ae8-7046-9106-30f2d07815af",
    "user_id": "01a1290b-0ae8-7046-9106-30f2d07815c1",
    "role": "owner",
    "username": "bob",
    "display_name": "Bob",
    "email": "bob@example.com"
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `role` | string | 变更后的角色；转移成功时为目标成员的新角色 `owner` |
| 其余字段 | — | 见"项目成员对象" |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | 请求体为空/非 JSON/类型不符；`project_id` 或 `user_id` 不是合法 UUID |
| `401` | `authentication_required` | 未登录或会话失效 |
| `403` | `permission_denied` | 目标成员**是唯一持有者**却被降级（`fields.user_id`：要换人请直接转移，不要先降后升） |
| `404` | `project_not_found` | 项目不存在、不可见，或请求者无管理权 |
| `404` | `resource_not_found` | 目标用户不是本项目成员（`fields.user_id`）。**整个转移不做任何写入**，因此不会留下"现任已降级"的中间态 |
| `409` | `conflict` | 项目处于归档只读状态（`fields.project_id`）；目标**已经是**持有者时再次请求转移（`fields.role`）；`role: member` 但目标本来就不是持有者（`fields.role`） |
| `422` | `validation_error` | `role` 不在枚举内；转移目标已被停用（`fields.user_id`）；缺少 `role`；请求体含契约未定义字段 |

**备注**：

- **持有者唯一，且可以转移**：`project_members` 上有"每个项目至多一个 `owner`"的部分唯一索引；转移在**同一事务**内先降现任、再升目标，因此任何时刻都恰好一个持有者。任一步失败整体回滚。
- **转移目标必须有效**：目标用户必须 `status = active`，否则项目会立刻进入"谁都改不了"的状态（`422`）。
- **已经是持有者时再次转移返回 `409`**，而不是"成功但没有变化"。
- **`role: member` 实际不会成功**：在"恰好一个持有者、不可空缺"的不变式下，目标是现任持有者时返回 `403`（唯一持有者不可降级），目标是普通成员时返回 `409`（"该成员不是项目持有者"）。前端若要展示"改为普通成员"，请先判断当前 `role`，不要依赖这个分支成功。
- **转移的后果**：原持有者降为 `member` 并失去项目管理权（含成员管理）；新持有者获得全部项目管理权。
- **副作用**：`owner_transferred`（转移）或 `member_role_changed`（其它角色变更）操作记录，同事务；不产生提醒。

## 9. `DELETE /api/v1/projects/{project_id}/members/{user_id}`

**用途**：把成员移出项目（删除成员关系行）。
**权限**：项目持有者或系统管理员。无管理权返回 `404 project_not_found`。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `project_id` | string(UUID) | 项目 id；非法 UUID 返回 `400 invalid_request` |
| `user_id` | string(UUID) | 目标成员的用户 id；非法 UUID 返回 `400 invalid_request` |

**查询参数**：无。

**请求体**：无请求体（服务端不读取请求体，发 `{}` 或不发都可以）。

**响应 `204`**：无响应体（不是 `{"data": ...}`）。

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | `project_id` 或 `user_id` 不是合法 UUID |
| `401` | `authentication_required` | 未登录或会话失效 |
| `403` | `permission_denied` | 目标成员是项目的**唯一持有者**（`fields.user_id`），项目不能变成无人可管理 |
| `404` | `project_not_found` | 项目不存在、不可见，或请求者无管理权 |
| `404` | `resource_not_found` | 目标用户不是本项目成员（`fields.user_id`） |
| `409` | `conflict` | 项目处于归档只读状态（`fields.project_id`） |

**备注**：

- **项目成员没有软移除**（与工作组不同）：移除即删除该行，个人数据不受影响。被移除者立刻失去对**私有**项目及其任务的可见性（若项目对归属组共享，他仍可能以组员身份看到已共享内容）。
- 不得移除唯一持有者（`403`）；要换人请用端点 8 转移。
- **不幂等**：对已不是成员的用户再次调用返回 `404 resource_not_found`，不是 `204`。
- **副作用**：写一条 `action = member_removed` 的操作记录（同事务）；不产生提醒。

## 不确定或契约未写清的点

| 点 | 说明 |
| --- | --- |
| 创建项目时省略 `start_date` / `end_date`（契约未规定，按实现描述） | 两个字段在绑定结构上**没有必填标记**，服务端也没有默认值：省略时按 Go 零值日期 `0001-01-01` 写入并返回 `201`（数据库 `DATE NOT NULL` 与 `end_date >= start_date` 都成立）。《S02》§4.5 把两个日期列为创建字段，但未规定省略行为。**建议前端恒传两个日期**；若要把"缺失即 `422`"定成契约，属于契约新增项。 |
| `group_id` / `user_id` 不是合法 UUID | 本域只有**路径参数**（`project_id`、`user_id`）与列表筛选参数（`owner_id`、`group_id`）做了前置 UUID 校验（返回 `400`）。请求体里的 `group_id`（端点 1）与 `user_id`（端点 7）**没有**前置校验：非 UUID 字符串会直达数据库并触发 PostgreSQL `22P02`，最终兜底为 `500 internal_error`。与《A07》记录的"附件域路径参数"情形同类。 |
| 归档能否解除 | 没有"解除归档"端点，`archived_at` 不会被任何端点清空，而"只读"与"列表排除"都按 `status == "archived" \|\| archived_at != null` 判定。`PATCH /projects/{id}` 携带 `status`（非 `archived`）时会跳过只读检查、放行本次写入，但项目**依然是归档状态**。契约未定义"解除归档"的语义，前端不要把它设计成可逆操作。 |
| 只提交 `expected_updated_at` 的空写入 | 服务端不校验"至少一个可改字段"，空写入返回 `200` 并推进 `updated_at`，会让其它客户端的版本失效。该规则只写进了 Users 域（《A02》），Projects 域契约未提。 |
| `role: member` 的可用性 | 见端点 8 备注：因为持有者恰好一个，`role: member` 永远不能成功（`403` 或 `409`）。《S02》§5 只写了"不得降级唯一 owner"，未说明对普通成员返回 `409`。 |
| "无权"的错误码不一致 | 项目更新/归档对"不是持有者也不是系统管理员"返回 `403 permission_denied`——**项目对请求者不可见时也是 `403`**（相当于对外区分了"项目 id 存在"与"不存在"）；成员管理端点（7/8/9）对同一类请求者返回 `404 project_not_found`。契约未统一这两处，前端需按接口分别处理。 |
| 归档项目上的写入报错顺序 | 端点 4/5 的归档检查在权限检查**之后**：非持有者操作已归档项目会先得到 `403`，而不是 `409`。前端若要展示"项目已归档，不可修改"，请仅在确认自己有管理权后再依赖这个提示。 |
| 日期/时间字符串格式非法的错误码 | `httpapi.Date` / `Timestamp` 的自定义解析错误既不是 `json.SyntaxError` 也不是 `UnmarshalTypeError`，会落到请求体解析的兜底分支：`400 invalid_request`，`fields.body = "请求体无法解析"`——**具体字段名不会出现在 `fields` 里**。《S02》§2.7 只规定了"未知字段"（`422`）与"多个 JSON 值"（`400`），未规定这一情形。 |
| `paused` 状态语义 | 枚举里有 `paused`（见 [README.md](README.md) 的"公共资源表示"），但契约未描述它与 `in_progress` 在派生字段上的差别：`progress` 与 `overdue_task_count` 的计算只看任务，不看项目的 `paused`。前端不要假设 `paused` 会改变逾期或进度的口径。 |
