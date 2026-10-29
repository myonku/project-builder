# A02 Users 接口详解

> 归属：**前后端共用**（`docs/api/`）。通用约定（基础路径 `/api/v1`、成功/错误信封、认证、分页、并发与幂等、限流、未知字段拒绝）见《S02-HTTP接口契约》§2，本文不重复。
> 业务范围见《S01》，跨端规则见《S02》；本文定义各接口的请求与响应数据结构。

> 头像使用 GET /users/{id}/avatar、POST/DELETE /me/avatar，公开结构及成员投影含 avatar_updated_at；仅支持 PNG/JPEG。个人资料复用既有 GET/PATCH /users/{id}，密码重置完整规则见 A01。前端方形裁剪只改变保存前交互，不改变本接口的 2 MiB 上限。

> **提醒计划同步（2026-10-29）**：影响任务节点或收件资格的业务写入，与相关提醒计划在同一事务提交；同步失败则业务回滚。当前有效规则、事件身份和授权裁决点见 [S01 §5.7](../S01-需求文档.md#57-邮件提醒)，查询、九种状态与重试边界见 [A08](A08-reminders.md)。业务请求不执行任务提醒 SMTP；已授权尝试允许按原快照完成。

## 接口清单

| # | 方法 | 路径 | 权限 | 说明 |
| --- | --- | --- | --- | --- |
| 1 | `GET` | `/api/v1/users/{user_id}` | 本人 / 系统管理员 / 同工作组成员 / 同项目成员 | 读取单个用户的公开资料 |
| 2 | `PATCH` | `/api/v1/users/{user_id}` | 本人（资料字段）；系统管理员（全部字段） | 更新资料；管理员可改 `status` / `system_role` |
| 3 | `POST` | `/api/v1/users/{user_id}/disable` | 仅系统管理员 | 停用账号（状态变更，数据保留） |
| 4 | `POST` | `/api/v1/users` | 仅系统管理员 | 创建账号（与自助注册同一套实现） |
| 5 | `POST` | `/api/v1/users/query` | 仅系统管理员 | 分页查询账号清单 |
| 6 | `POST` | `/api/v1/users/{user_id}/password` | 仅系统管理员 | 重置指定用户密码 |
| 7 | `GET` | `/api/v1/users/{user_id}/avatar` | 对该用户资料可见 | 读取头像原图；无头像 404 `avatar_not_found` |
| 8 | `POST` | `/api/v1/me/avatar` | 本人 | 上传/替换头像（multipart 字段 `file`） |
| 9 | `DELETE` | `/api/v1/me/avatar` | 本人 | 删除头像；幂等 204 |

本域 9 个端点**全部需要登录**（都不在会话网关白名单内）。
`POST /api/v1/users` 的路径**不带尾部斜杠**：服务端关闭了自动重定向，`/api/v1/users/` 不会被纠正，
而是直接按未命中路由返回 `404 resource_not_found`。

本域返回的用户对象就是《A01》§1 的**公开用户结构**（与 `GET /auth/me` 逐字段一致）：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string (UUID) | 用户 id（UUIDv7）；客户端不应解析其内容 |
| `username` | string | 登录标识；**不可修改**（登录标识与审计主体） |
| `email` | string | 邮箱；保存时为小写 |
| `display_name` | string | 显示名 |
| `status` | string | `active` \| `disabled` |
| `system_role` | string | `member` \| `admin` |
| `timezone` | string | IANA 时区标识，决定该用户的"今天" |
| `reminder_enabled` | boolean | 个人全局提醒开关 |
| `last_login_at` | string \| null | 最后一次登录时刻；从未登录为 `null` |
| `avatar_updated_at` | string \| null | 头像版本；`null` 表示没有头像。前端拼 `?v=` 破缓存，API **不返回 avatar_url** |
| `updated_at` | string | 最后变更时刻（UTC ISO 8601，最多 9 位小数秒） |

**密码、密码摘要与会话令牌永不返回**：本域的所有响应结构里都没有 `password_hash`。

## 1. `GET /api/v1/users/{user_id}`

**用途**：按 id 读取一个用户的公开资料（成员列表里点开某人的场景）。
**权限**：本人 / 系统管理员 / 与请求者**同属一个工作组**（双方成员行均为 `active`）或**同一项目**的用户；其余返回 `404`。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `user_id` | string (UUID) | 目标用户 id；不是合法 UUID 时返回 `400 invalid_request` |

**查询参数**（无则写"无"）：无

**请求体**（无则写"无请求体"）：无请求体

**响应 `200`**：

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d07815af",
    "username": "alice",
    "email": "alice@example.com",
    "display_name": "Alice",
    "status": "active",
    "system_role": "member",
    "timezone": "Asia/Shanghai",
    "reminder_enabled": true,
    "last_login_at": "2026-10-11T09:30:00Z",
    "avatar_updated_at": null,
    "updated_at": "2026-10-11T09:30:00Z"
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `data.id` | string (UUID) | 目标用户 id |
| `data.username` | string | 小写用户名（登录标识） |
| `data.email` | string | 小写邮箱 |
| `data.display_name` | string | 显示名 |
| `data.status` | string | `active` \| `disabled`（停用账号对可见者仍然可读） |
| `data.system_role` | string | `member` \| `admin` |
| `data.timezone` | string | 该用户时区 |
| `data.reminder_enabled` | boolean | 该用户提醒开关（对他人可见，属公开结构） |
| `data.last_login_at` | string \| null | 最后登录时刻 |
| `data.avatar_updated_at` | string \| null | 头像版本；`null` 表示没有头像 |
| `data.updated_at` | string | 当前版本；修改前必须持有它 |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | 路径参数不是合法 UUID（`fields.user_id`） |
| `401` | `authentication_required` | 未登录或会话失效 |
| `404` | `user_not_found` | 用户不存在，**或**对请求者不可见（两种情况同码） |
| `500` | `internal_error` | 未预期服务端错误（含会话后端不可用） |

**备注**：

- **不可见与不存在使用同一个错误码**（`404 user_not_found`）：若不可见返回 `403`，
  任意登录用户就能用状态码差异枚举系统里有哪些账号。
- 可见范围**刻意不比成员列表更宽**：同组成员 / 同项目成员的用户名、显示名与邮箱
  本来就能从成员列表接口读到，因此这里额外暴露的信息为零。
- 可见 ≠ 可改：同组成员只获得读取权限，修改权限见 §2。
- 系统管理员无需是任何组或项目的成员即可读取任意用户。

## 2. `PATCH /api/v1/users/{user_id}`

**用途**：修改用户资料；系统管理员可额外修改账号状态与系统角色。
**权限**：本人（资料字段）/ 系统管理员（全部字段）。**可见但非本人且非管理员** → `403 permission_denied`。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `user_id` | string (UUID) | 目标用户 id；不是合法 UUID 时返回 `400 invalid_request` |

**查询参数**（无则写"无"）：无

**请求体**：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `display_name` | string | 否 | 1–128 字符（按 Unicode 码点计）；`TrimSpace` 后不得为空 | 显示名 |
| `email` | string | 否 | ≤320 字符；基本邮箱语法；保存前转小写 | 邮箱，大小写不敏感唯一 |
| `timezone` | string | 否 | IANA 时区标识，≤64 字符；**空串返回 `422`** | 更新时不允许清空时区 |
| `reminder_enabled` | boolean | 否 | `true` / `false` | 必须显式传布尔值才能关闭提醒（`false` 与"未提供"是两回事） |
| `status` | string | 否 | `active` / `disabled`；**仅系统管理员** | 状态变更 |
| `system_role` | string | 否 | `member` / `admin`；**仅系统管理员** | 角色变更 |
| `expected_updated_at` | string | 是 | UTC ISO 8601，必须等于当前 `updated_at` | 版本字段，缺失返回 `422` |

**响应 `200`**：

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d07815af",
    "username": "alice",
    "email": "alice@example.com",
    "display_name": "Alice Zhang",
    "status": "active",
    "system_role": "member",
    "timezone": "Asia/Shanghai",
    "reminder_enabled": false,
    "last_login_at": "2026-10-11T09:30:00Z",
    "updated_at": "2026-10-11T10:04:12.837465Z"
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `data.id` | string (UUID) | 目标用户 id（不变） |
| `data.username` | string | 用户名（**不可修改**，恒为原值） |
| `data.email` | string | 更新后的邮箱（小写）；未提交则为原值 |
| `data.display_name` | string | 更新后的显示名；未提交则为原值 |
| `data.status` | string | 更新后的状态；未提交则为原值 |
| `data.system_role` | string | 更新后的角色；未提交则为原值 |
| `data.timezone` | string | 更新后的时区；未提交则为原值 |
| `data.reminder_enabled` | boolean | 更新后的开关；未提交则为原值 |
| `data.last_login_at` | string \| null | 最后登录时刻（本端点不改动它） |
| `data.updated_at` | string | **已推进**的新版本，客户端应立即替换本地版本 |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | 路径参数不是合法 UUID；请求体不是合法 JSON / 类型不符 / 为空；`expected_updated_at` 不是可解析的时间串 |
| `401` | `authentication_required` | 未登录或会话失效 |
| `403` | `permission_denied` | 可见但非本人且非管理员；或**非管理员**提交了 `status` / `system_role`（`fields` 逐字段说明原因） |
| `404` | `user_not_found` | 目标用户不存在或对请求者不可见 |
| `409` | `version_conflict` | `expected_updated_at` 与当前 `updated_at` 不匹配 |
| `409` | `conflict` | 邮箱已被其他账号占用（`fields.email`）；或降级 / 停用**最后一个启用中的管理员**（`fields.system_role` 或 `fields.status`） |
| `422` | `validation_error` | 字段校验失败；**无任何可修改字段**（只提交 `expected_updated_at`，`fields.body`）；缺 `expected_updated_at`；请求体含契约未定义字段 |
| `500` | `internal_error` | 未预期服务端错误（含会话后端不可用） |

**备注**：

- **处理顺序**：字段校验（`422`）→ 是否至少有一个可改字段（`422`）→ 对象可见性（`404`）→ 修改权限（`403`）
  → 管理员专用字段（`403`）→ 版本与领域规则（`409`）→ 事务写入。
  也就是说"字段本身不合法"会先于"没权限"返回，前端不要假设 `403` 一定排在 `422` 之前。
- **版本字段必填**：缺失返回 `422 validation_error`；值必须**原样回传**上一次响应里的 `updated_at`
  （含亚秒精度），截断到秒会让原子 CAS 必然失败，表现为"刚读到的版本立刻被判为冲突"。
  传一个格式正确但与当前版本不同的时间得到 `409 version_conflict`；
  传一个**格式不合法**的时间串得到 `400 invalid_request`（`fields.body`），而不是 `422`。
- 拒绝"只有版本、没有可改字段"的请求：否则一次没有任何改动的写也会推进 `updated_at`，
  让其他客户端手里的版本无谓失效。
- `username` 不在字段集合内：提交即未知字段 `422`；密码同样不在这里改，走 §6。
- 非管理员修改他人的 `status` / `system_role` 返回带 `fields` 的 `403`，
  而不是静默忽略——"字段不生效"与"没权限"必须能分辨。
- **不变式**：系统必须至少保留一个**启用中的管理员**（`status=active` 且 `system_role=admin`）。
  该判定使用"排除目标用户后的活跃管理员计数"并在写入的同一事务内复查，
  因此并发降级两个管理员时必然有一方被 `409` 拒绝。
- 邮箱唯一性由数据库 `lower()` 唯一索引兜底，重复返回 `409 conflict`（`fields.email`）；
  把邮箱改成自己的大小写变体不算冲突。
- 副作用：本端点在**同一事务**内写入一条用户操作记录（审计），不派发提醒；
  改 `timezone` 后，`is_overdue` / `overdue_task_count` 的"今天"判定在下一次请求立即按新时区生效。

## 3. `POST /api/v1/users/{user_id}/disable`

**用途**：停用账号——账号不能再登录，其既有会话立即失效，数据保留。
**权限**：仅系统管理员。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `user_id` | string (UUID) | 目标用户 id；不是合法 UUID 时返回 `400 invalid_request` |

**查询参数**（无则写"无"）：无

**请求体**：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `expected_updated_at` | string | 是 | UTC ISO 8601，必须等于目标当前的 `updated_at` | 值取自 `GET /users/{user_id}` 的 `updated_at` |

**响应 `200`**：

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d07815af",
    "username": "bob",
    "email": "bob@example.com",
    "display_name": "Bob",
    "status": "disabled",
    "system_role": "member",
    "timezone": "UTC",
    "reminder_enabled": true,
    "last_login_at": "2026-10-10T08:00:00Z",
    "updated_at": "2026-10-11T10:20:31.004512Z"
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `data.id` | string (UUID) | 被停用的用户 id |
| `data.username` | string | 用户名（不变） |
| `data.email` | string | 邮箱（不变） |
| `data.display_name` | string | 显示名（不变） |
| `data.status` | string | 恒为 `disabled` |
| `data.system_role` | string | 角色不变（停用不等于降级） |
| `data.timezone` | string | 不变 |
| `data.reminder_enabled` | boolean | 不变 |
| `data.last_login_at` | string \| null | 保留停用前的最后登录时刻 |
| `data.updated_at` | string | 已推进的新版本 |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | 路径参数不是合法 UUID；请求体为空 / 不是合法 JSON / 类型不符；`expected_updated_at` 不是可解析的时间串 |
| `401` | `authentication_required` | 未登录或会话失效 |
| `403` | `permission_denied` | 非系统管理员（**先判权限再查对象**：无论目标是否存在都是同一个 `403`） |
| `404` | `user_not_found` | 调用者是管理员，但目标用户不存在 |
| `409` | `conflict` | 目标**已处于停用状态**（`fields.status`，不是"成功但没有变更"）；或目标是最后一个启用中的管理员（`fields.status`） |
| `409` | `version_conflict` | `expected_updated_at` 不匹配 |
| `422` | `validation_error` | 缺 `expected_updated_at`；请求体含契约未定义字段 |
| `500` | `internal_error` | 未预期服务端错误（含会话后端不可用） |

**备注**：

- **顺序**：路径参数与请求体（`400` / `422`）→ 管理员权限（`403`）→ 目标存在性（`404`）
  → 已停用判定（`409`）→ 原子 CAS 写入（`409 version_conflict`）。
  权限在对象查询**之前**，因此该端点不会变成"账号是否存在"的探测接口。
- 停用是**状态变更而非删除**：`users` 行保留，历史对象上的外键与操作记录完整保留。
- 停用后该用户的**既有会话在下一次请求时立即失效**（会话有效性每次请求回读数据库），
  无需会话失效广播；前端在收到 `401` 时应跳转登录页。
- 停用用户不能重新登录：`POST /auth/login` 返回 `403 permission_denied`（见《A01》§2）。
- 副作用：同一事务内写入一条 `disabled` 操作记录。**没有"启用"端点**，需要恢复时由管理员用 §2 的
  `PATCH` 把 `status` 改回 `active`（同样要求版本字段，同样受"至少一个启用中管理员"约束）。

## 4. `POST /api/v1/users`（管理员创建用户）

**用途**：由系统管理员代建账号；行为与自助注册完全一致。
**权限**：仅系统管理员。

**路径参数**（无则写"无"）：无

**查询参数**（无则写"无"）：无

**请求体**（字段集合与 `POST /api/v1/auth/register` **完全一致**）：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `username` | string | 是 | 3–64 字符；仅 `[A-Za-z0-9_-]`；保存前转小写 | 登录标识 |
| `email` | string | 是 | ≤320 字符；基本邮箱语法；保存前转小写 | 邮箱 |
| `display_name` | string | 是 | 1–128 字符（按 Unicode 码点计） | 显示名 |
| `password` | string | 是 | ≥ `security.password_min_length`（默认 8）且 ≤256 字符；**不做 trim** | 初始密码 |
| `timezone` | string | 否 | IANA 时区标识，≤64 字符 | 省略、为空或 `null` 则存为 `UTC` |

**响应 `201`**：

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d07815af",
    "username": "carol",
    "email": "carol@example.com",
    "display_name": "Carol",
    "status": "active",
    "system_role": "member",
    "timezone": "Asia/Shanghai",
    "reminder_enabled": true,
    "last_login_at": null,
    "updated_at": "2026-10-11T03:39:11Z"
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `data.id` | string (UUID) | 新账号 id |
| `data.username` | string | 已归一化的小写用户名 |
| `data.email` | string | 已归一化的小写邮箱 |
| `data.display_name` | string | 显示名 |
| `data.status` | string | 固定 `active` |
| `data.system_role` | string | 固定 `member`（创建端点不接受 `system_role`） |
| `data.timezone` | string | 提交值或 `UTC` |
| `data.reminder_enabled` | boolean | 固定 `true` |
| `data.last_login_at` | null | 新账号恒为 `null` |
| `data.updated_at` | string | 创建时刻 |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | 请求体为空 / 不是合法 JSON / 字段类型不符 / 多个 JSON 值 |
| `401` | `authentication_required` | 未登录或会话失效 |
| `403` | `permission_denied` | 非系统管理员 |
| `409` | `conflict` | 用户名或邮箱已被占用（`fields.username` / `fields.email`） |
| `422` | `validation_error` | 必填字段缺失（`fields` 指出）；字段校验失败；未知字段（例如 `system_role`） |
| `500` | `internal_error` | 未预期服务端错误（含会话后端不可用） |

**备注**：

- **与自助注册共用同一套实现**：字段归一化、密码强度、唯一性检查与约束冲突翻译全部复用注册用例，
  不存在第二份标准（创建者与操作人不同，操作记录里会标注来源）。
- **不自动登录**、不设置会话 Cookie，响应不含任何凭据；新账号用提交的密码即可登录。
- `status` 固定 `active`、`system_role` 固定 `member`、`reminder_enabled` 固定 `true`；
  请求体里出现 `system_role` 会因契约未定义该字段返回 `422`——升角色必须走 §2 的 `PATCH`，
  从而受"至少保留一个启用中管理员"的不变式约束，并留下一次显式可审计的变更。
- 顺序：请求体绑定与必填校验（`400` / `422`）→ 管理员权限（`403`）→ 字段校验（`422`）→ 唯一性（`409`）。
  因此非管理员若请求体本身不合法（缺必填字段、类型不符）会先得到 `422` / `400`，而不是 `403`。
- **第一个管理员不通过本端点产生**：后台账号由配置 `security.bootstrap_admin_*` 在服务启动时初始化
  （幂等，且绝不覆盖既有账号的密码），见《A02》 末段。

## 5. `POST /api/v1/users/query`（用户列表）

**用途**：分页查询全系统账号清单（管理员维护账号的入口）。
**权限**：仅系统管理员。普通成员需要的是**成员列表**，不是全系统账号清单。

**路径参数**（无则写"无"）：无

**查询参数**（无则写"无"）：无（参数放在请求体里，避免与 `POST /users` 的创建语义混淆）

**请求体**：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `include_disabled` | boolean | 否 | 默认 `false` | `false` 只返回 `status=active` 的账号；`true` 时同时返回已停用账号 |
| `page` | integer | 否 | ≥1，默认 `1` | 页码，越界返回 `400`（不静默纠正） |
| `page_size` | integer | 否 | 1–100，默认 `20` | 每页条数 |
| `sort` | string | 否 | 见下；默认 `-created_at` | 排序表达式 |

`sort` 白名单（**仅**这些字段名，未知字段返回 `400 invalid_request` 并在 `fields.sort` 给出可用清单）：

`username`、`display_name`、`email`、`status`、`system_role`、`created_at`、`updated_at`、`last_login_at`

语法：`field` 升序、`-field` 降序，逗号分隔多字段（如 `-last_login_at,username`）；
同一字段重复出现返回 `400`。服务端**始终追加 `id` 作为次级排序键**，因此翻页时不会出现重复行或漏行。

**响应 `200`**：

```json
{
  "data": [
    {
      "id": "01a1290b-0ae8-7046-9106-30f2d07815af",
      "username": "alice",
      "email": "alice@example.com",
      "display_name": "Alice",
      "status": "active",
      "system_role": "admin",
      "timezone": "Asia/Shanghai",
      "reminder_enabled": true,
      "last_login_at": "2026-10-11T09:30:00Z",
      "updated_at": "2026-10-11T10:04:12.837465Z"
    },
    {
      "id": "01a1290b-0b11-7c9d-b2f4-5a0c1e77aabb",
      "username": "bob",
      "email": "bob@example.com",
      "display_name": "Bob",
      "status": "disabled",
      "system_role": "member",
      "timezone": "UTC",
      "reminder_enabled": true,
      "last_login_at": null,
      "updated_at": "2026-10-10T08:00:00Z"
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

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `data` | array | 公开用户结构数组；空结果序列化为 `[]` 而不是 `null` |
| `pagination.page` | integer | 当前页码（回显生效值） |
| `pagination.page_size` | integer | 每页条数（回显生效值） |
| `pagination.total` | integer | **与筛选条件一致**的总条数（两个 `include_disabled` 口径各自准确） |
| `pagination.has_next` | boolean | 服务端推导：`page * page_size < total` |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | `page < 1` 或 `page_size` 越界（`fields.page` / `fields.page_size`）；`sort` 含未知或重复字段（`fields.sort`）；字段类型不符（如 `page` 传字符串） |
| `401` | `authentication_required` | 未登录或会话失效 |
| `403` | `permission_denied` | 非系统管理员 |
| `422` | `validation_error` | 请求体含契约未定义字段 |
| `500` | `internal_error` | 未预期服务端错误（含会话后端不可用） |

**备注**：

- 顺序：请求体绑定（`400` / `422`）→ 管理员权限（`403`）→ 分页校验（`400`）→ 排序校验（`400`）。
  因此非管理员只要请求体能被解析，就一律得到 `403`（不会因为 `page_size=500` 而看到 `400`）。
- 默认排除停用账号，但它**不是被隐藏**：显式 `include_disabled: true` 即可查到，
  因为停用是数据保留而不是删除。两个口径的 `total` 都与其返回的条目数一致
  （计数查询与列表查询复用同一套筛选条件）。
- `data` 只包含账号资料本身，不包含成员关系或聚合统计。
- 空请求体（无 body）返回 `400`；只想用默认参数时应提交 `{}`。
- `last_login_at` 可能为 `null`（从未登录），按它排序时前端需自行决定空值的展示位置。

## 6. `POST /api/v1/users/{user_id}/password`（管理员重置密码）

**用途**：由系统管理员直接为某个账号设置新密码（**不需要**旧密码）。
**权限**：仅系统管理员。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `user_id` | string (UUID) | 目标用户 id；不是合法 UUID 时返回 `400 invalid_request` |

**查询参数**（无则写"无"）：无

**请求体**：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `new_password` | string | 是 | ≥ `security.password_min_length`（默认 8）且 ≤256 字符；**不做 trim** | 字段名是 `new_password`，因为本端点不接受旧密码 |
| `expected_updated_at` | string | 是 | UTC ISO 8601，必须等于目标当前的 `updated_at` | 值取自 `GET /users/{user_id}` 的 `updated_at` |

提交 `password`（而不是 `new_password`）会因契约未定义该字段返回 `422`。

**响应 `200`**：

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d07815af",
    "username": "bob",
    "email": "bob@example.com",
    "display_name": "Bob",
    "status": "active",
    "system_role": "member",
    "timezone": "UTC",
    "reminder_enabled": true,
    "last_login_at": "2026-10-10T08:00:00Z",
    "updated_at": "2026-10-11T11:02:07.551203Z"
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `data.id` | string (UUID) | 目标用户 id |
| `data.username` | string | 用户名（不变） |
| `data.email` | string | 邮箱（不变） |
| `data.display_name` | string | 显示名（不变） |
| `data.status` | string | 状态（不变） |
| `data.system_role` | string | 角色（不变） |
| `data.timezone` | string | 时区（不变） |
| `data.reminder_enabled` | boolean | 提醒开关（不变） |
| `data.last_login_at` | string \| null | 最后登录时刻（不变） |
| `data.updated_at` | string | **已推进**的新版本；响应中**没有**任何密码或摘要字段 |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | 路径参数不是合法 UUID；请求体为空 / 不是合法 JSON / 类型不符；`expected_updated_at` 不是可解析的时间串 |
| `401` | `authentication_required` | 未登录或会话失效 |
| `403` | `permission_denied` | 非系统管理员（**对任何 id 都是同一个 `403`**，因此该端点不会成为账号探测接口） |
| `404` | `user_not_found` | 调用者是管理员，但目标用户不存在 |
| `409` | `version_conflict` | `expected_updated_at` 不匹配（写语句自带版本条件，是原子 CAS） |
| `422` | `validation_error` | `new_password` 强度不足（`fields.new_password`）；缺 `expected_updated_at`；未知字段 |
| `500` | `internal_error` | 未预期服务端错误（含会话后端不可用） |

**备注**：

- 顺序：路径参数与请求体（`400` / `422`）→ 管理员权限（`403`）→ 目标存在性（`404`）
  → 密码强度（`422`）→ CAS 写入（`409`）。**先判权限再查对象**。
- 密码强度与注册**共用同一个校验函数**，两处标准不会分叉；长度口径同《A01》§1 的备注。
- 新密码**立即可用**，旧密码**立即失效**；响应是公开用户结构，明文密码与摘要永不回显，
  日志只记"哪个管理员重置了哪个用户的密码"。
- 重置密码递增 `users.session_version`，旧会话下次请求 401，同时作废该用户未消费的自助重置 token。
- 副作用：同一事务内写入一条 `password_reset` 操作记录（只记动作与对象，不记密码）。

## 7. `GET /api/v1/users/{user_id}/avatar`

**用途**：读取目标用户的头像原图。响应体是**图片字节**，不是 JSON 信封。
**权限**：与 `GET /users/{user_id}` 完全一致（本人 / 系统管理员 / 同工作组成员 / 同项目成员）。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `user_id` | string (UUID) | 目标用户 id；非法 UUID 返回 `400 invalid_request` |

**查询参数**（无则写"无"）：无

**请求体**（无则写"无请求体"）：无请求体

**响应 `200`**：

```text
Content-Type: image/png
Content-Length: 12345
Content-Disposition: inline
X-Content-Type-Options: nosniff
Cache-Control: private, max-age=300
ETag: "1761091200123456789"
```

| 响应头 | 说明 |
| --- | --- |
| `Content-Type` | 服务端保存的 `avatar_mime`（`image/png` 或 `image/jpeg`） |
| `Content-Length` | `avatar_size_bytes` |
| `Content-Disposition` | 固定 `inline` |
| `X-Content-Type-Options` | 固定 `nosniff` |
| `Cache-Control` | 固定 `private, max-age=300` |
| `ETag` | `avatar_updated_at` 的 Unix 纳秒值（带引号） |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | 路径参数不是合法 UUID |
| `401` | `authentication_required` | 未登录或会话失效 |
| `404` | `user_not_found` | 用户不存在，**或**对请求者不可见（同码防枚举） |
| `404` | `avatar_not_found` | 用户可见，但没有上传头像（或对象已缺失） |
| `500` | `internal_error` | 未预期服务端错误 |

**备注**：

- 带 `If-None-Match: <ETag>` 且匹配时返回 **`304`**（无响应体），浏览器可复用缓存。
- `avatar_url` **不由服务端返回**：前端用 `API_BASE + /users/{id}/avatar?v=<avatar_updated_at>` 构造。
- 头像下载**每次重新校验可见性**，因此不能把 URL 当作带权限的长期链接。

## 8. `POST /api/v1/me/avatar`

**用途**：上传或替换**本人**头像（`multipart/form-data`，字段名 `file`）。
**权限**：登录用户，只能操作本人；没有目标 `user_id` 参数。

**路径参数**（无则写"无"）：无

**查询参数**（无则写"无"）：无

**请求体**：`multipart/form-data`，单文件字段 `file`

| 约束 | 值 |
| --- | --- |
| 大小 | ≤ `storage.avatar_max_file_size`（默认 **2 MiB**） |
| 格式 | **PNG / JPEG**（按内容嗅探 + `image.DecodeConfig`；**不支持 WebP/GIF/SVG**） |
| 尺寸 | 宽、高均 ≤ **4096** |
| 扩展名/文件名 | 不参与格式判断，也不进入存储 key |

**响应 `200`**：公开用户结构（含新的 `avatar_updated_at`；`updated_at` **不变**）。

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d07815af",
    "username": "alice",
    "email": "alice@example.com",
    "display_name": "Alice",
    "status": "active",
    "system_role": "member",
    "timezone": "Asia/Shanghai",
    "reminder_enabled": true,
    "last_login_at": "2026-10-21T03:00:00Z",
    "avatar_updated_at": "2026-10-21T05:30:00.123456Z",
    "updated_at": "2026-10-21T03:00:00Z"
  }
}
```

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | 请求不是合法 multipart / 上传内容不完整 |
| `401` | `authentication_required` | 未登录或会话失效 |
| `413` | `file_too_large` | 超过 2 MiB（`fields.file`） |
| `415` | `unsupported_media_type` | 不是 PNG/JPEG，或内容无法解码（`fields.file`） |
| `422` | `validation_error` | 缺少 `file`、空文件、宽高超过 4096（`fields.file`） |
| `500` | `internal_error` | 对象存储或数据库写入失败 |

**备注**：

- 头像**不计入**附件配额（`storage.max_files_per_user` 不适用）。
- 每次上传写**新 key**（`avatars/<user_id>/<uuid>`）；数据库成功后才删除旧对象。
  数据库失败会删除刚写入的新对象，避免孤儿。
- 只推进 `avatar_updated_at`，**不推进** `users.updated_at`：否则个人资料表单的
  `expected_updated_at` 会被头像上传意外作废。
- 审计：同一事务写 `user/updated`，`changes` 只有 `{"avatar":"set"}`，不记录 key/MIME/内容。

## 9. `DELETE /api/v1/me/avatar`

**用途**：删除本人头像。
**权限**：登录用户，只能操作本人。

**路径参数**（无则写"无"）：无

**查询参数**（无则写"无"）：无

**请求体**（无则写"无请求体"）：无请求体

**响应 `204`**：无响应体。**幂等**：没有头像时同样返回 `204`。

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `401` | `authentication_required` | 未登录或会话失效 |
| `500` | `internal_error` | 数据库写入失败 |

**备注**：

- 先清数据库、后删对象：对象删除失败只记录告警，行已不再引用它，页面不会显示旧图。
- 有头像时审计写 `user/updated`，`changes` 为 `{"avatar":"cleared"}`；没有头像时不写审计。
- 同样**不推进** `users.updated_at`。

## W5 补充（2026-10-22）

管理员重置密码成功后吊销该用户全部会话并撤销未使用自助令牌。邮箱变更或停用也撤销未使用令牌。
