# HTTP 接口契约

## 1. 文档定位

本文档是系统 HTTP API 的**唯一契约**，规定基础格式、认证、成功/错误信封、分页与并发语义，
以及当前阶段已实现端点的**实测行为**。

- 业务需求以《01-Go服务端架构设计.md》引用的需求文档为准（见《05-迁移映射与差异.md》第 7 节说明）。
- 数据库与配置基线见《02-数据库模型与迁移.md》《03-配置与运行.md》。
- 本文档取代 Python 版本仓库根目录的《项目计划管理系统-接口文档.md》。
  两者的公共约定（2.x 节）逐条一致；本文档额外记录**已实现端点的实测响应**，
  并对开发过程中的契约加强项给出明确标记。

对应实现：`internal/httpapi/`、`internal/app/*/http.go`。

## 2. 公共约定

### 2.1 基础格式

- 基础地址：`/api/v{API_VERSION}`，当前为 `/api/v1`。
- JSON 请求与响应使用 `application/json`；文件上传使用 `multipart/form-data`（Attachments 阶段）。
- JSON 字段统一 `snake_case`。
- ID 使用标准 36 字符带连字符的小写 UUID 字符串。
- 日期使用 `YYYY-MM-DD` 自然日；瞬时时间使用 UTC ISO 8601，例如 `2026-09-30T04:00:00Z`。
- 成功写响应包含对象 `id` 与最新 `updated_at`。

**实测样例**（`updated_at` 为 `Z` 结尾的 UTC ISO 8601，无小数秒）：

```json
{
  "data": {
    "id": "01a1290b-0ae8-7046-9106-30f2d07815af",
    "username": "wire_113911",
    "email": "wire_113911@example.com",
    "display_name": "Wire",
    "status": "active",
    "system_role": "member",
    "timezone": "UTC",
    "reminder_enabled": true,
    "last_login_at": null,
    "updated_at": "2026-10-11T03:39:11Z"
  }
}
```

> **实现说明**：`id` 为 UUIDv7，其前缀包含毫秒时间戳，因此同一毫秒内生成的
> ID 字典序也保持时间有序。客户端不应解析 ID 内容。

### 2.2 认证

- 业务接口默认要求登录。
- 登录成功后使用 **HttpOnly** 会话 Cookie；会话数据存放 Redis，Cookie 不直接承载权限数据。
- Cookie 属性：`HttpOnly`、`SameSite=Lax`、`Path=/`；`Secure` 由 `security.secure_cookies` 控制，
  生产环境必须为 `true`。
- Cookie 值是不可预测的 64 字符十六进制随机令牌（256 位熵），**不是** UUID。
  会话令牌不使用 UUIDv7（可预测），见《01-Go服务端架构设计.md》引用的全局约定 4.2。
- 未登录返回 `401 Unauthorized`；无权访问返回 `403 Forbidden`。

**两层校验**（必须理解，否则会误判行为）：

1. **会话网关**（`httpapi.SessionGateway`）只检查 Cookie **是否存在**。缺失时直接返回 401，
   不查 Redis。这使 Redis 不可用时业务端点仍能给出明确错误，而不是全部 401。
2. **端点**（`auth.Service.CurrentUser`）读取 Redis 校验会话有效性，并回数据库读取用户。
   伪造的 Cookie 会通过网关但在这一步返回 401。

会话有效性**每次请求实时从数据库读取**，因此：

- 用户被停用后，其既有会话立即失效（无需会话失效广播）。
- 用户角色变更立即生效。

### 2.3 成功响应

单对象与写操作：

```json
{
  "data": {
    "id": "uuid",
    "updated_at": "2026-09-30T04:00:00Z"
  }
}
```

列表：

```json
{
  "data": [],
  "pagination": {
    "page": 1,
    "page_size": 20,
    "total": 0,
    "has_next": false
  }
}
```

- `data` 为列表时，空列表序列化为 `[]` 而**不是** `null`。
- `has_next` 由服务端统一推导：`page * page_size < total`。

### 2.4 错误响应

```json
{
  "error": {
    "code": "project_not_found",
    "message": "Project not found",
    "fields": {
      "project_id": "The project does not exist or is not visible."
    },
    "request_id": "5d96353a3b75b7ce2371cbfa004f5e5a"
  }
}
```

- `code`：机器可读错误码，客户端据此分支。
- `message`：面向人的**英文**描述。中文描述只用于服务端日志。
- `fields`：字段级明细，仅校验类错误存在；`422` 无明细时输出 `{}`，其余省略。
- `request_id`：与响应头 `X-Request-ID` 一致，便于前端反馈问题。

**实测样例**（未登录访问 `/api/v1/auth/me`）：

```json
{
  "error": {
    "code": "authentication_required",
    "message": "Authentication is required.",
    "request_id": "5d96353a3b75b7ce2371cbfa004f5e5a"
  }
}
```

#### 错误码表

| HTTP | 错误码 | 使用场景 |
| --- | --- | --- |
| `400` | `invalid_request` | 请求本身不合法（JSON 语法错误、分页越界、未知筛选/排序字段） |
| `400` | `invalid_date_range` | 结束日期早于开始日期 |
| `401` | `authentication_required` | 未登录或会话失效 |
| `401` | `invalid_credentials` | 凭据错误。**不区分**账号不存在与密码错误 |
| `403` | `permission_denied` | 已登录但无权操作；也用于停用用户登录 |
| `404` | `resource_not_found` | 对象不存在或不可见 |
| `404` | `user_not_found` / `group_not_found` / `project_not_found` / `task_not_found` | 域内精确不存在 |
| `409` | `conflict` | 状态、重复或并发冲突 |
| `409` | `duplicate_member` | 重复成员关系 |
| `409` | `version_conflict` | `expected_updated_at` 不匹配 |
| `413` | `file_too_large` | 文件超过限制 |
| `415` | `unsupported_media_type` | 媒体类型不支持 |
| `422` | `validation_error` | 字段校验失败，带 `fields` |
| `429` | `rate_limited` | 超出限流 |
| `500` | `internal_error` | 未预期服务端错误 |

错误不得暴露密码、令牌、数据库语句、内部堆栈或物理路径。

> **实现保障**：`httpapi.problem.go` 用一张显式映射表覆盖全部错误码，
> 并由 `TestEveryDomainErrorConstantIsMapped` 与 `TestErrorCodesAllHaveStatusAndMessage`
> 断言"每个错误码都已登记状态码与对外文案"。新增错误码时若漏配，测试立即失败。
> 未识别的错误统一兜底为 500 `internal_error`。

### 2.5 分页、筛选和排序

列表接口统一使用页码分页：`page` 从 `1` 开始，默认 `1`；`page_size` 默认 `20`、最大 `100`。
服务端拒绝非法页码与未知筛选/排序字段。排序必须稳定，同字段时使用 `id` 作为次级排序。

参数较多的查询接口使用 `POST .../query`，请求体为 `application/json`，避免与同一资源的
创建 `POST` 路由冲突。可选筛选字段可省略，分页字段省略时使用默认值。

> **实现约束**（承接《01-Go服务端架构设计.md》5.4）：
> 分页必须在 SQL 层 `LIMIT/OFFSET`，不得全量载入内存后切片；
> 排序字段必须白名单校验；排序必须带 `id` 作为次级键。
> 当前阶段无列表端点，上述约束在 Groups/Projects/Tasks 阶段落地并配套测试。

### 2.6 写入、并发和幂等

- 创建、更新、归档、删除和状态变更执行身份、参数、权限和领域校验。
- 更新核心对象提交 `expected_updated_at`；版本不一致返回 `409 version_conflict`。
- 版本校验必须是**原子比较并交换**：把版本条件放进写语句本身，而不是先读后写。

```sql
UPDATE projects
   SET name = $1, updated_at = now()
 WHERE id = $2 AND updated_at = $3
RETURNING id, updated_at;
```

- `RowsAffected() == 0` 时再补一次存在性查询，区分"不存在"与"版本冲突"。
- 可重试操作使用 `Idempotency-Key`，不得产生重复成员、提醒、上传或状态副作用。
- 事务提交后才触发审计和提醒等后续处理。

### 2.7 请求体字段严格性

> **相对 Python 版本的契约加强项**。

请求体中出现契约**未定义**的字段时返回 `422 validation_error`，并在 `fields` 中指出该字段。

理由：Python 版本的校验元数据对 JSON body 静默失效，客户端把 `expected_updated_at`
拼错成 `expected_update_at` 时会被静默忽略，版本协议退化为 last-write-wins 且无任何提示。
显式拒绝让这类错误立即暴露。

同一请求体出现多个 JSON 值（`{...}{...}`）同样返回 `400 invalid_request`。

## 3. 资源表示

### 3.1 用户（公开结构）

```json
{
  "id": "01a1290b-0ae8-7046-9106-30f2d07815af",
  "username": "alice",
  "email": "alice@example.com",
  "display_name": "Alice",
  "status": "active",
  "system_role": "member",
  "timezone": "Asia/Shanghai",
  "reminder_enabled": true,
  "last_login_at": "2026-10-11T03:39:06Z",
  "updated_at": "2026-10-11T03:39:06Z"
}
```

- `status`：`active` | `disabled`。
- `system_role`：`member` | `admin`。
- **密码、密码摘要和会话令牌永不返回**。该边界由 `PublicUser` 结构与
  `TestPublicUserNeverExposesPasswordHash`、集成测试的响应体泄漏检查共同保证。

### 3.2 项目

状态：`not_started`、`in_progress`、`paused`、`completed`、`archived`、`cancelled`。
共享级别：`private`、`group_readonly`、`group_editable`。

```json
{
  "id": "uuid",
  "name": "Migration",
  "description": null,
  "created_by": "uuid",
  "group_id": "uuid",
  "status": "not_started",
  "visibility": "private",
  "start_date": "2026-10-01",
  "end_date": "2026-10-31",
  "color": "#336699",
  "archived_at": null,
  "progress": 0,
  "overdue_task_count": 0,
  "updated_at": "2026-09-30T04:00:00Z"
}
```

`progress` 与 `overdue_task_count` 是查询派生字段，不持久化。
`progress` 为有效任务进度的简单平均值；已取消任务不参与计算；无有效任务时为 `0`。

### 3.3 任务

状态：`not_started`、`in_progress`、`completed`、`cancelled`。
**没有 `overdue` 状态**：逾期是派生展示字段
（`end_date < today AND status IN ('not_started','in_progress')`，按用户时区计算"今天"），
不作为持久化状态。

```json
{
  "id": "uuid",
  "project_id": "uuid",
  "parent_id": null,
  "created_by": "uuid",
  "name": "Prepare migration",
  "status": "in_progress",
  "progress": 40,
  "priority": 0,
  "start_date": "2026-10-01",
  "end_date": "2026-10-05",
  "is_milestone": false,
  "is_shared": true,
  "is_overdue": false,
  "deleted_at": null,
  "updated_at": "2026-09-30T04:00:00Z"
}
```

## 4. API 域和端点

**已实现**：Health、Auth。以下未标注"已实现"的端点属于规划范围。

### 4.1 Health ✅ 已实现

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/v1/health/check` | 要求 `arg` 请求头；缺失返回 `422`，成功返回 `200` |
| `GET` | `/api/v1/health/live` | 存活检查，**不检查**任何外部依赖，成功返回 `200` |
| `GET` | `/api/v1/health/ready` | 就绪检查，真实检查数据库与 Redis |
| `GET` | `/healthz` | 根级存活探针（无版本前缀），供容器/反代直接使用 |

`/health/ready` 的行为：

| 情况 | 状态码 | 响应体 |
| --- | --- | --- |
| 全部可用 | `200` | `{"status":"ready"}` |
| 数据库不可用 | `503` | `{"status":"not_ready","error":"database_unavailable"}` |
| Redis 不可用（且已启用） | `503` | `{"status":"not_ready","error":"redis_unavailable"}` |

`redis.enabled=false` 时 `/health/ready` 不检查 Redis（视为通过）。

> **实测**：健康检查端点属于会话白名单，无需 Cookie。
> 白名单按"去掉版本前缀后的相对路径"匹配，因此升级 `API_VERSION` 时无需改动白名单。

### 4.2 Auth ✅ 已实现

#### `POST /api/v1/auth/register`

请求字段：

| 字段 | 类型 | 必填 | 约束 |
| --- | --- | --- | --- |
| `username` | string | 是 | 3–64 字符，仅 `[A-Za-z0-9_-]`，保存前转小写 |
| `email` | string | 是 | ≤320 字符，基本邮箱语法，保存前转小写 |
| `display_name` | string | 是 | 1–128 字符（按 Unicode 码点计） |
| `password` | string | 是 | ≥`security.password_min_length`（默认 8）且 ≤256 字符；**不做 trim** |
| `timezone` | string | 否 | IANA 时区标识，≤64 字符；省略或为空则默认 `UTC` |

成功：`201`，`data` 为公开用户结构。

行为要点：

- **不自动登录**，不设置会话 Cookie。
- `status` 固定为 `active`，`system_role` 固定为 `member`（新用户不得默认为管理员）。
- `reminder_enabled` 固定为 `true`。
- 用户名与邮箱**不区分大小写唯一**：数据库层有 `uq_users_username_lower` /
  `uq_users_email_lower` 唯一索引作为最终保证。应用层的提前检查只是提示，
  并发注册同一账号时由数据库拒绝并翻译为 `409 conflict`（有并发集成测试覆盖）。
- 一次返回**全部**字段错误，避免前端逐次往返修正。

错误：

| 情况 | 状态码 | 错误码 |
| --- | --- | --- |
| 字段校验失败 | `422` | `validation_error`（带 `fields`） |
| 用户名或邮箱已存在 | `409` | `conflict`（`fields` 指出具体字段） |
| 请求体非法 JSON / 未知字段 | `400` / `422` | `invalid_request` / `validation_error` |

#### `POST /api/v1/auth/login`

请求字段：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `identifier` | string | 是 | 用户名**或**邮箱；不区分大小写 |
| `password` | string | 是 | 明文密码 |

成功：`200`，`data` 为公开用户结构，并设置 HttpOnly 会话 Cookie。

行为要点：

- 登录成功后记录 `last_login_at`。
- 密码摘要参数弱于当前配置时按需**升级**（登录成功后透明完成，不打断用户）。
- 凭据错误统一返回 `401 invalid_credentials`，**不区分**"账号不存在"与"密码错误"，
  且对外 `message` 完全一致，避免账号枚举。
- **停用用户不能登录**：凭据正确时才检查状态（避免通过状态差异推断账号是否存在），
  返回 `403 permission_denied`。
- Redis 未启用时返回 `500 internal_error`，而不是静默降级或崩溃。

#### `POST /api/v1/auth/logout`

成功：`204`，无响应体。撤销 Redis 会话并设置清除 Cookie（`Max-Age < 0`）。

**幂等**：令牌缺失或已失效仍返回成功，登出不会因基础设施状态卡住。

#### `GET /api/v1/auth/me`

成功：`200`，`data` 为公开用户结构。

- 会话网关保证 Cookie 存在；端点真正校验 Redis 会话并回数据库读取用户。
- 用户被停用时返回 `401`，并顺手清理该会话。
- 会话指向已不存在的用户时返回 `401`，并清理孤儿会话。

### 4.3 Users ⬜ 规划中

- `GET /users/{user_id}`：查询公开用户信息。
- `PATCH /users/{user_id}`：修改显示名、邮箱、时区、提醒开关；管理员可修改状态与系统角色；要求版本字段。
- `POST /users/{user_id}/disable`：管理员停用用户，数据保留。

### 4.4 Groups ⬜ 规划中

- `POST /groups`：创建工作组，创建者自动成为 owner/manager；字段 `name`、`description`。
- `POST /groups/query`：查询当前用户所属工作组；字段 `status`、`page`、`page_size`、`sort`。
- `GET /groups/{group_id}`：工作组详情。
- `PATCH /groups/{group_id}`：owner/管理员修改名称、描述或状态；要求版本字段。
- `POST /groups/{group_id}/members/query`：成员列表；字段 `role`、`status`、`page`、`page_size`、`sort`。
- `POST /groups/{group_id}/join-requests`：当前用户申请加入；重复申请返回 `409`。
- `POST /groups/{group_id}/join-requests/query`：owner、manager 或管理员查询申请。
- `POST /groups/{group_id}/join-requests/{user_id}/review`：字段 `status`，只能为 `approved` 或 `rejected`。
- `PATCH /groups/{group_id}/members/{user_id}`：设置成员角色或状态。
- `DELETE /groups/{group_id}/members/{user_id}`：移除成员，个人数据保留。

### 4.5 Projects ⬜ 规划中

- `POST /projects`：字段 `name`、`group_id`、`start_date`、`end_date`，可选 `description`、`visibility`、`color`；创建者自动成为 owner/member。
- `POST /projects/query`：字段 `group_id`、`status`、`owner_id`、`name`、`page`、`page_size`、`sort`。
- `GET /projects/{project_id}`：项目详情、成员摘要、派生进度与逾期任务数。
- `PATCH /projects/{project_id}`：owner 修改项目字段；要求 `expected_updated_at`。
- `POST /projects/{project_id}/archive`：owner 或管理员归档项目。
- `POST /projects/{project_id}/members/query`：项目成员列表。
- `POST /projects/{project_id}/members`：owner 添加成员，字段 `user_id`；目标用户必须是有效用户，允许来自项目归属组以外的其他工作组。
- `PATCH /projects/{project_id}/members/{user_id}`：owner 修改成员角色，最多一个 owner。
- `DELETE /projects/{project_id}/members/{user_id}`：owner 移除成员，不得移除唯一 owner。

### 4.6 Tasks ⬜ 规划中

- `POST /projects/{project_id}/tasks`：字段 `name`、`start_date`、`end_date`，可选 `parent_id`、`description`、`priority`、`is_milestone`、`is_shared`。
- `POST /projects/{project_id}/tasks/query`：字段 `status`、`assignee_id`、`owner_id`、`is_shared`、`include_deleted`、`page`、`page_size`、`sort`；默认排除逻辑删除。
- `GET /tasks/{task_id}`：任务详情、成员、父子关系与派生 `is_overdue`。
- `PATCH /tasks/{task_id}`：owner 或满足共享编辑权限的用户修改任务；要求版本字段。
- `DELETE /tasks/{task_id}`：逻辑删除任务。
- `POST /tasks/{task_id}/copy`：复制基础字段，不复制提醒发送记录；使用 `Idempotency-Key`。
- `POST /tasks/{task_id}/members/query`、`POST/PATCH/DELETE /tasks/{task_id}/members[/{user_id}]`：成员变更；成员必须属于项目，最多一个 owner。
- `PATCH /tasks/{task_id}/dates`：甘特图日期编辑；归档项目只读，日期范围必须合法。

> **组合筛选警告**（承接《01-Go服务端架构设计.md》5.3）：
> `owner_id` 与 `assignee_id` 同时给出时，**必须**为每个成员条件各用一个别名 JOIN。
> Python 版本把两个谓词放在同一张成员表的同一行，要求同一个人既是 owner 又是 assignee，
> 实测单条件各返回 1 条、组合恒为 0 条。该场景必须有集成测试覆盖。

### 4.7 Plan Views ⬜ 规划中

- `GET /plan-views/personal`：当前用户创建、持有或参与的项目和任务。
- `GET /plan-views/groups/{group_id}`：组内共享计划；项目共享级别与任务 `is_shared` 同时决定可见内容。
- `GET /plan-views/projects/{project_id}/gantt`：返回层级、日期、状态、进度、owner、成员、里程碑与 `can_edit`。

### 4.8 Attachments、Reminders、Audit ⬜ 规划中

- 附件：`POST/GET /projects/{project_id}/attachments`、`POST/GET /tasks/{task_id}/attachments`、`GET /attachments/{attachment_id}/download`、`DELETE /attachments/{attachment_id}`。上传使用 multipart；下载与删除重新校验权限。
- 提醒：`GET /reminders`、`GET /reminders/{reminder_id}`、`POST /reminders/{reminder_id}/retry`。事务提交后生成，使用 `dedupe_key`。
- 审计：`GET /audit`，支持 `object_type`、`object_id`、`actor_id`、`action`、时间范围与分页。

## 5. 写接口处理顺序

```text
认证 / 当前用户
  -> 请求与字段校验（绑定失败 → 400 / 422）
  -> 对象存在性与可见性（不存在 → 404）
  -> 权限判断（无权 → 403）
  -> expected_updated_at / Idempotency-Key（冲突 → 409）
  -> 领域规则（非法 → 400 / 422）
  -> 用例服务与事务写入（CAS + RETURNING）
  -> 提交后：审计、提醒、文件处理
  -> 统一响应信封
```

API 层只负责请求解析、响应序列化与错误映射，不直接查询数据库或复制领域规则。

## 6. 契约加强项汇总

以下条目**严于** Python 版本的接口文档，属于本次重写的强化，客户端需要遵守：

| 项 | 强化内容 | 动机 |
| --- | --- | --- |
| 2.7 | 未知请求体字段返回 `422` 而不是静默忽略 | Python 版本因此发生过版本协议静默降级 |
| 2.5 | 分页与排序字段必须服务端白名单校验 | Python 版本 `sort` 校验后被丢弃、`page_size=500` 被接受 |
| 2.4 | 错误 `message` 固定为英文，中文只进日志 | 避免同一错误码出现语言不一致的对外文案 |
| 2.2 | 会话令牌必须是 256 位随机值，不得用 UUIDv7 | UUIDv7 可预测，不适用于安全令牌 |
| 2.6 | 版本校验必须原子 CAS，不得先读后写 | 先读后写在并发下两个请求都能通过 |
| 4.2 | 注册不自动登录，重复注册由数据库唯一索引保证 | 应用层检查存在竞争窗口 |

## 7. 变更规则

1. 契约变更必须先改本文档，再改代码；实现与文档冲突时以文档为准。
2. 新增端点必须同时补：请求字段约束、成功响应形状、全部失败状态码与错误码、
   并发与幂等语义、以及在《06-迁移进度与验证.md》中登记的测试覆盖维度。
3. 新增错误码必须同步更新 2.4 的错误码表与 `httpapi/problem.go` 的映射表；
   测试会断言映射完整性。
4. 不得用空路由或仅返回成功的占位实现冒充"已实现"。
