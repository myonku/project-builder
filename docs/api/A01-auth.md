# A01 Auth 接口详解

> 归属：**前后端共用**（`docs/api/`）。通用约定（基础路径 `/api/v1`、成功/错误信封、认证、分页、并发与幂等、限流、未知字段拒绝）见《S02-HTTP接口契约》§2，本文不重复。
> 契约正文（约定性叙述）见《A01》；本文补齐每个接口的**请求与响应数据结构**。

## 接口清单

| # | 方法 | 路径 | 权限 | 说明 |
| --- | --- | --- | --- | --- |
| 1 | `POST` | `/api/v1/auth/register` | 无需登录 | 提交注册申请（pending）；不创建用户、不建立会话 |
| 2 | `POST` | `/api/v1/auth/login` | 无需登录 | 用用户名或邮箱换取会话 Cookie |
| 3 | `POST` | `/api/v1/auth/logout` | 登录用户 | 撤销会话并清除 Cookie；幂等 |
| 4 | `GET` | `/api/v1/auth/me` | 登录用户 | 返回当前会话对应的用户 |

本域特有的一点必须记住：`register` 与 `login` 在**会话网关白名单**内（无 Cookie 也能到达用例层），
`logout` 与 `me` **不在**白名单内，因此必须携带会话 Cookie，缺失时由网关直接返回 `401`（不查 Redis）。
Cookie 名由 `security.session_cookie_name` 配置（默认 `pb_session`），前端不应硬编码，也不应假定令牌形状。

**公开用户结构**（`login` 与 `/auth/me` 返回同一个结构；`register` 返回申请结构，见 §1；字段表见本节与 [README.md](README.md) 的"公共资源表示"）：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string (UUID) | 用户 id（UUIDv7）；客户端不应解析其内容 |
| `username` | string | 登录标识；保存时为小写，注册后不可修改 |
| `email` | string | 邮箱；保存时为小写 |
| `display_name` | string | 显示名 |
| `status` | string | `active` \| `disabled` |
| `system_role` | string | `member` \| `admin` |
| `timezone` | string | IANA 时区标识；决定该用户的"今天"（见《A02》 末段） |
| `reminder_enabled` | boolean | 个人全局提醒开关 |
| `last_login_at` | string \| null | 最后一次登录时刻；从未登录为 `null` |
| `updated_at` | string | 最后变更时刻（UTC ISO 8601，最多 9 位小数秒，**必须原样回传作为版本**） |

**密码、密码摘要与会话令牌永不出现**在响应体中：会话只通过 `Set-Cookie` 下发。

## 1. `POST /api/v1/auth/register`

**用途**：提交一条**注册申请**（`pending`），不创建用户、不建立会话。
申请必须由系统管理员在 `POST /api/v1/registration-requests/{id}/review` 通过后，
才会在同一事务里创建启用账号（详见 [A10](A10-registration-requests.md)）。
**权限**：无需登录（会话网关白名单）。

**路径参数**（无则写"无"）：无

**查询参数**（无则写"无"）：无

**请求体**：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `username` | string | 是 | 3–64 字符；仅 `[A-Za-z0-9_-]`；保存前 `TrimSpace` + 转小写 | 登录标识 |
| `email` | string | 是 | ≤320 字符；基本邮箱语法；保存前 `TrimSpace` + 转小写 | 同时可用于登录 |
| `display_name` | string | 是 | 1–128 字符（按 Unicode 码点计）；`TrimSpace` 后不得为空 | 展示用名称 |
| `password` | string | 是 | ≥ `security.password_min_length`（默认 8）且 ≤256 字符；**不做 trim** | 明文密码，前后空格是合法字符 |
| `timezone` | string | 否 | IANA 时区标识，≤64 字符 | 省略或为空串则存为 `UTC` |

**响应 `201`**（申请已创建，**不是用户**）：

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d07815af",
    "username": "alice",
    "email": "alice@example.com",
    "display_name": "Alice",
    "timezone": "Asia/Shanghai",
    "status": "pending",
    "reviewed_by": null,
    "reviewed_at": null,
    "rejection_reason": null,
    "created_at": "2026-10-11T03:39:11Z",
    "updated_at": "2026-10-11T03:39:11Z"
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `data.id` | string (UUID) | 申请 id |
| `data.username` | string | 已归一化的小写用户名 |
| `data.email` | string | 已归一化的小写邮箱 |
| `data.display_name` | string | 显示名（已 `TrimSpace`） |
| `data.timezone` | string | 提交值或 `UTC` |
| `data.status` | string | 固定 `pending` |
| `data.reviewed_by` | string \| null | 审核管理员 id；未审核为 `null` |
| `data.reviewed_at` | string \| null | 审核时间；未审核为 `null` |
| `data.rejection_reason` | string \| null | 拒绝原因；通过/未审核为 `null` |
| `data.created_at` / `data.updated_at` | string | 申请时间戳 |

响应**不含** `Set-Cookie`：提交申请不登录；申请未通过前也没有可登录的账号。

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | 请求体不是合法 JSON、请求体为空、字段类型不符、请求体含多个 JSON 值（`fields.body` 等） |
| `409` | `conflict` | 用户名或邮箱已被用户或**待审核申请**占用：`fields.username` / `fields.email`（不区分大小写） |
| `422` | `validation_error` | 字段校验失败（一次返回全部字段明细）；请求体含契约未定义字段（`fields` 指出该字段名） |
| `500` | `internal_error` | 未预期服务端错误 |

**备注**：

- 处理顺序：请求体绑定（`400` / 未知字段 `422`）→ 字段校验（`422`）→ 唯一性检查（`409`）→ 写入申请。
- 唯一性判定**不区分大小写**（`Alice` 与 `alice` 是同一个账号）；并发提交相同 pending 申请时
  由 `uq_registration_requests_*_lower` 部分唯一索引保证只有一条成功，另一方得到 `409`。
- 被拒绝后可以重新提交；同一 HTTP 请求不会重复创建 pending 申请。
- 请求体**不接受** `status` / `system_role` / `reminder_enabled`：它们不在契约字段集合内，
  提交即因未知字段返回 `422`（而不是被静默忽略）。
- 密码长度下限按**字节数**比较、上限按 **Unicode 码点数**比较，
  因此多字节字符的口令在长度上的实际表现与"字符数"不完全一致。
- 用户名与邮箱保存为小写，`id` 为服务端生成的 UUIDv7；这两项都不接受客户端指定。
- **测试配置** `security.registration_requires_approval=false` 时可恢复到"注册即创建 active 用户"的旧行为；
  生产配置默认且必须为 `true`，见 [B03](../../backend/docs/B03-配置与运行.md)。

## 2. `POST /api/v1/auth/login`

**用途**：用用户名或邮箱 + 明文密码换取一条服务端会话。
**权限**：无需登录（会话网关白名单）。

**路径参数**（无则写"无"）：无

**查询参数**（无则写"无"）：无

**请求体**：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `identifier` | string | 是 | 非空；`TrimSpace` + 转小写 | 用户名**或**邮箱，二者共用一个字段，不区分大小写 |
| `password` | string | 是 | 非空；**不做 trim** | 明文密码 |

**响应 `200`**：

响应头（令牌**只**出现在 Cookie 中，body 里没有令牌字段）：

```text
Set-Cookie: pb_session=<64 位十六进制随机令牌>; Path=/; Max-Age=<security.session_ttl_seconds>; HttpOnly; SameSite=Lax
```

`Secure` 属性由 `security.secure_cookies` 控制（生产必须为 `true`），因此本地 HTTP 调试时该属性不出现。

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
    "updated_at": "2026-10-11T09:30:00Z"
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `data.id` | string (UUID) | 登录成功的用户 id |
| `data.username` | string | 小写用户名 |
| `data.email` | string | 小写邮箱 |
| `data.display_name` | string | 显示名 |
| `data.status` | string | 恒为 `active`（停用账号在登录处被拒） |
| `data.system_role` | string | `member` \| `admin` |
| `data.timezone` | string | 该用户当前时区 |
| `data.reminder_enabled` | boolean | 个人提醒开关 |
| `data.last_login_at` | string | **本次登录时刻**（成功后写入） |
| `data.updated_at` | string | 与 `last_login_at` 同一次写入后的最新版本 |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | 请求体不是合法 JSON、请求体为空、字段类型不符、多个 JSON 值 |
| `401` | `invalid_credentials` | 账号不存在**或**密码错误；两种情况的状态码与 `message` 完全一致（不可用于账号枚举） |
| `403` | `permission_denied` | 凭据正确，但账号已停用（`fields.status`） |
| `422` | `validation_error` | `identifier` / `password` 缺失或为空；请求体含契约未定义字段 |
| `500` | `internal_error` | Redis 未启用或不可用（会话无法建立），不静默降级 |

**备注**：

- 登录成功后按需**透明升级**弱参数密码摘要，不打断用户、不改变响应形状。
- **停用判定在凭据校验之后**：密码错误返回 `401`、密码正确但账号停用返回 `403`，
  这样无法通过状态差异推断账号是否存在（《A01》）。
- 会话数据存服务端，Cookie 只是不可预测的随机令牌（256 位熵，非 UUID）；
  前端只需按浏览器默认行为保存 Cookie（`HttpOnly` 意味着 JS 读不到它），跨域场景需 `credentials: include`。
- 口令尝试属于最需要防护的场景，因此登录失败同样计入《S02》§2.8 的全局限流（按来源地址分桶、覆盖未登录端点）；
  收到 `429` 时应按 `Retry-After` 退避，而不是立即重试。

## 3. `POST /api/v1/auth/logout`

**用途**：撤销当前会话并清除会话 Cookie。
**权限**：登录用户（`logout` 不在会话网关白名单内，必须携带会话 Cookie）。

**路径参数**（无则写"无"）：无

**查询参数**（无则写"无"）：无

**请求体**（无则写"无请求体"）：无请求体（携带请求体也不解析、不影响结果）

**响应 `204`**：

无响应体。响应头带清除 Cookie 的指令：

```text
Set-Cookie: pb_session=; Path=/; Max-Age=-1; HttpOnly; SameSite=Lax
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| （无） | — | `204` 不返回任何 body，成功与否只看状态码 |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `401` | `authentication_required` | 未携带会话 Cookie（会话网关拦截，尚未查 Redis） |
| `500` | `internal_error` | 撤销会话时 Redis 返回错误（Redis 未启用时**不算**错误，见备注） |

**备注**：

- **幂等**：Cookie 存在但 Redis 中已无该会话（已过期、已被登出、伪造的令牌）时仍返回 `204`；
  登出不因基础设施状态卡住。
- 与 `401` 的边界：只有**完全没有 Cookie** 才会被网关拦下。
  因此"重复登出"在浏览器自动清掉 Cookie 后可能得到 `401`，前端应把它当作"已登出"处理。
- 服务端**先撤销会话、再写响应**：`204` 返回时该令牌在服务端已经失效，
  客户端继续携带旧令牌的后续请求会得到 `401`。
- 清 Cookie 使用 `Max-Age < 0`，且属性（`Path=/`、`HttpOnly`、`SameSite=Lax`）与写入时一致，
  否则部分浏览器不会覆盖原 Cookie。
- `redis.enabled=false` 时撤销操作直接返回成功，登出仍然返回 `204`。

## 4. `GET /api/v1/auth/me`

**用途**：读取当前会话对应的用户资料，用于前端启动时恢复登录态。
**权限**：登录用户（只能查自己，没有目标参数）。

**路径参数**（无则写"无"）：无

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
    "system_role": "admin",
    "timezone": "Asia/Shanghai",
    "reminder_enabled": true,
    "last_login_at": "2026-10-11T09:30:00Z",
    "updated_at": "2026-10-11T09:30:00Z"
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `data.id` | string (UUID) | 当前用户 id（后续请求的主体） |
| `data.username` | string | 小写用户名 |
| `data.email` | string | 小写邮箱 |
| `data.display_name` | string | 显示名 |
| `data.status` | string | 恒为 `active`（停用会使本端点返回 `401`） |
| `data.system_role` | string | 决定前端是否展示管理员入口 |
| `data.timezone` | string | 用于前端本地化"今天"的展示 |
| `data.reminder_enabled` | boolean | 个人提醒开关 |
| `data.last_login_at` | string \| null | 最后登录时刻 |
| `data.updated_at` | string | 当前版本，也用于 `PATCH /users/{user_id}` 的 `expected_updated_at` |

字段集合与 `GET /api/v1/users/{user_id}`（见《A02》§1）**完全一致**，前端可以共用同一套类型定义。

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `401` | `authentication_required` | 未携带 Cookie（网关拦截）；或 Cookie 存在但 Redis 中会话不存在 / 已过期 / 被登出；或用户已被停用；或会话指向的用户已不存在（孤儿会话） |
| `500` | `internal_error` | Redis 未启用或不可用（无法校验会话） |

**备注**：

- 会话有效性**每次请求实时回读数据库**，因此用户被停用后既有会话在下一次请求立即失效，
  角色、显示名、时区等改动同样立即生效——前端不需要依赖"重新登录才刷新"。
- 停用导致的 `401` 会**顺手清理**该会话；孤儿会话（用户已被删除）同样会被清理。
- 前端把本端点当作"登录态探针"是正确用法；它不返回权限清单，
  具体能否操作某个对象由各业务端点判定（`403` / `404`）。
