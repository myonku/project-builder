# HTTP 接口契约

## 1. 文档定位

本文档是系统 HTTP API 的**唯一契约**，规定基础格式、认证、成功/错误信封、分页与并发语义，
以及当前阶段已实现端点的**实测行为**。

- 业务需求以[《需求文档》](./00-需求文档.md)为准。
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

> **实现约束**（承接《01-Go服务端架构设计.md》5.4）——已在 Groups / Projects / Tasks 落地：
>
> - 分页在 SQL 层 `LIMIT/OFFSET` 完成，不全量载入内存后切片；
> - `page >= 1`、`1 <= page_size <= 100` 由服务端**强制校验**，
>   越界返回 `400 invalid_request` 并带字段明细，**不做静默夹取**
>   （静默纠正会让客户端误以为参数生效）；
> - 排序字段必须在白名单内，未知字段返回 `400 invalid_request`；
> - 排序子句由 `listquery` 统一生成，并**始终追加限定名 `id`** 作为次级键。
>   限定名（如 `g.id`）是必需的：列表查询普遍带 JOIN，多张表都有 `id` 列，
>   裸 `id` 会被 PostgreSQL 判为 `ORDER BY "id" is ambiguous`（42702）。
> - 列表的 `pagination.total` 必须与筛选条件一致：计数查询与列表查询
>   必须复用**同一套**筛选构造，否则会出现"total 与实际条目数不符"。

### 2.6 写入、并发和幂等

- 创建、更新、归档、删除和状态变更执行身份、参数、权限和领域校验。
- 更新核心对象**必须**提交 `expected_updated_at`；版本不一致返回 `409 version_conflict`。

> **契约加强项**：Python 版本把 `expected_updated_at` 定义为**可选**，省略即跳过校验，
> 使版本协议静默退化为 last-write-wins。Go 版本将其设为**必填**
> （缺失返回 `422 validation_error`），覆盖 PATCH 类端点、DELETE 任务与归档项目。
> 客户端必须先读取对象拿到 `updated_at`，再提交更新。
- 版本校验必须是**原子比较并交换**：把版本条件放进写语句本身，而不是先读后写。

```sql
UPDATE projects
   SET name = $1, updated_at = now()
 WHERE id = $2 AND updated_at = $3
RETURNING id, updated_at;
```

- `RowsAffected() == 0` 时再补一次**纯存在性查询**，区分"不存在"（404）与"版本冲突"（409）。
  该查询不得复用带身份参数的查询——传入空 UUID 会触发 `22P02` 并退化为 500。

#### 2.6.1 `Idempotency-Key` 语义（`POST /tasks/{task_id}/copy`）

请求头 `Idempotency-Key` 可选；提供时按下述语义处理：

| 情况 | 状态码 | 说明 |
| --- | --- | --- |
| 首次请求 | `201` | 确实创建了副本 |
| 重复请求（同 key、同用户、同任务） | `200` | 返回既有副本，**不再创建** |

用状态码区分"确实创建"与"重放"，是因为两者都会返回 2xx，仅凭成功无法判断是否产生了新对象。

幂等键作用域为 `tasks.copy:<task_id>` 且包含用户，因此同一 key 用在**不同任务**上互不影响、
不同用户的相同 key 也互不影响。记录持久化在 `idempotency_keys` 表，
唯一约束 `(user_id, scope, idempotency_key)` 是并发下的最终保证。

#### 2.6.2 派生字段与时间精度

以下字段由查询计算，**不持久化**，客户端不得写入：

| 字段 | 所属 | 规则 |
| --- | --- | --- |
| `progress` | Project | 有效任务进度的简单平均值；已取消与已逻辑删除的任务不参与；无有效任务时为 `0` |
| `overdue_task_count` | Project | `end_date < 今天` 且状态为 `not_started` / `in_progress` 的任务数 |
| `is_overdue` | Task | 同上，按用户时区计算"今天" |
| `membership_role` / `membership_status` | Group / Project / Task | 当前请求者在该对象中的成员角色；非成员时为 `null` |

`updated_at` 使用**带亚秒精度**的 UTC ISO 8601（如 `2026-10-11T04:30:54.047739Z`）。
必须保留亚秒精度：客户端把 `updated_at` 原样回传作为 `expected_updated_at`，
若对外只渲染到秒，回传值会丢掉微秒部分，原子 CAS 的等值比较必然失败
（表现为"刚读到的版本立刻被判为冲突"）。
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

**已实现**：Health、Auth、Users、Groups、Projects、Tasks、Plan Views。以下未标注"✅ 已实现"的端点属于规划范围。

> 第二阶段（Groups / Projects / Tasks）与第三阶段的 Users / Plan Views 已完成，
> 并经真实 PostgreSQL 16 + Redis 7 验证。实现状态与验证证据见《06-迁移进度与验证.md》。

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

### 4.3 Users ✅ 已实现

本域共 6 个端点：

| 方法 | 路径 | 权限 |
| --- | --- | --- |
| `GET` | `/api/v1/users/{user_id}` | 本人 / 系统管理员 / 同工作组成员 / 同项目成员 |
| `PATCH` | `/api/v1/users/{user_id}` | 本人（资料字段）；系统管理员（全部字段） |
| `POST` | `/api/v1/users/{user_id}/disable` | 仅系统管理员 |
| `POST` | `/api/v1/users` | 仅系统管理员（创建用户） |
| `POST` | `/api/v1/users/query` | 仅系统管理员（用户列表） |
| `POST` | `/api/v1/users/{user_id}/password` | 仅系统管理员（重置密码） |

**实测行为要点**：

- **可见性**：本人、系统管理员，以及与请求者**同属一个工作组或同一项目**的用户。
  其余情况返回 `404 user_not_found`，**不返回 403**，避免向任意登录用户泄漏"该账号存在"。
  该可见范围刻意不比成员列表更宽：同组成员/同项目成员的用户名、显示名与邮箱
  本来就能从成员列表接口读到，因此这里额外暴露的信息为零。
- **修改权限**：仅本人与系统管理员。可见但无权修改返回 `403 permission_denied`，
  与"不可见"的 `404` 语义分开，便于前端区分"没这个人"和"没这个权限"。
- 无权修改 `status` / `system_role`（非管理员）同样返回 `403 permission_denied`，
  并在 `fields` 中指明确字段，而不是静默忽略。
- `username` **不可修改**：需求文档 5.1 只要求维护姓名、邮箱、头像与提醒开关。
  用户名是登录标识与审计主体，改动会破坏两者的稳定性。
- **不能把最后一个启用中的管理员降级或停用**，否则系统将失去全部管理入口。
  返回 `409 conflict` 并在 `fields` 指出原因。该判定使用"排除目标用户后的活跃管理员计数"，
  因此并发降级两个管理员时必然有一方被拒绝（数据库计数 + 领域判定的组合，
  不是先读后写的猜测）。
- 邮箱修改复用注册的归一化与 `uq_users_email_lower` 唯一索引，重复返回 `409 conflict`
  （`fields.email`）。`email` 保存前转小写。
- **至少提供一个可修改字段**：只提交 `expected_updated_at` 的请求返回 `422 validation_error`。
  否则会产生"什么都没改、版本却被推进"的空写入，让其他客户端的版本无谓失效。
- `POST /users/{user_id}/disable` 要求 `expected_updated_at`（契约 2.6：状态变更属于版本化写入），
  仅系统管理员可用；停用后该用户的**既有会话立即失效**，无需会话失效广播
  （契约 2.2：会话有效性每次请求实时回读数据库）。
- 停用是**状态变更而非删除**：`users` 行保留，因此不会因其历史对象上的
  `ON DELETE RESTRICT` 外键而失败，历史数据与操作记录完整保留（需求文档 5.1、4.2 第 6 条）。

#### `PATCH /api/v1/users/{user_id}`

请求字段：

| 字段 | 类型 | 必填 | 约束 |
| --- | --- | --- | --- |
| `display_name` | string | 否 | 1–128 字符（按 Unicode 码点计） |
| `email` | string | 否 | ≤320 字符，基本邮箱语法，保存前转小写 |
| `timezone` | string | 否 | IANA 时区标识，≤64 字符；**空字符串返回 422**（注册时省略或为空默认 `UTC`，更新时不允许清空） |
| `reminder_enabled` | boolean | 否 | 个人全局提醒开关 |
| `status` | string | 否 | `active` / `disabled`；**仅系统管理员** |
| `system_role` | string | 否 | `member` / `admin`；**仅系统管理员** |
| `expected_updated_at` | string | 是 | UTC ISO 8601，必须等于当前 `updated_at` |

成功：`200`，`data` 为公开用户结构（3.1）。

错误：

| 情况 | 状态码 | 错误码 |
| --- | --- | --- |
| 目标用户不存在或对请求者不可见 | `404` | `user_not_found` |
| 可见但无权修改 | `403` | `permission_denied` |
| 非管理员修改 `status` / `system_role` | `403` | `permission_denied`（带 `fields`） |
| `expected_updated_at` 不匹配 | `409` | `version_conflict` |
| 降级或停用最后一个启用中的管理员 | `409` | `conflict`（带 `fields`） |
| 邮箱已被占用 | `409` | `conflict`（带 `fields`） |
| 字段校验失败 / 无任何可改字段 / 未知字段 | `422` | `validation_error` |

#### `POST /api/v1/users`（管理员创建用户）

请求字段与 `POST /auth/register` **完全一致**：

| 字段 | 类型 | 必填 | 约束 |
| --- | --- | --- | --- |
| `username` | string | 是 | 3–64 字符，仅 `[A-Za-z0-9_-]`，保存前转小写 |
| `email` | string | 是 | ≤320 字符，基本邮箱语法，保存前转小写 |
| `display_name` | string | 是 | 1–128 字符（按 Unicode 码点计） |
| `password` | string | 是 | ≥`security.password_min_length`（默认 8）且 ≤256 字符；**不做 trim** |
| `timezone` | string | 否 | IANA 时区标识；省略或为空则默认 `UTC` |

成功：`201`，`data` 为公开用户结构（与自助注册的成功响应形状一致）。

行为要点：

- **仅系统管理员**；非管理员返回 `403 permission_denied`。
- **与自助注册共用同一套实现**：字段归一化、密码强度、唯一性检查与约束冲突翻译
  全部复用注册用例，不存在第二份实现。这条边界由
  `TestAdminCreateUserEqualsSelfRegistration` 与
  `TestPasswordRuleIsSharedBetweenRegistrationAndReset` 共同锁住。
- `status` 固定 `active`、`system_role` 固定 `member`、`reminder_enabled` 固定 `true`。
  **创建端点不接受 `system_role`**：管理员不能借创建直接造出第二个管理员，
  升角色必须走 `PATCH`，从而受"至少保留一个启用中管理员"的不变式约束，
  也留下一次显式可审计的变更。请求体里出现 `system_role` 会因契约未定义该字段而 `422`。
- **不自动登录**、不设置会话 Cookie，响应不含任何凭据（契约 4.2 同款语义）。
- 用户名或邮箱重复返回 `409 conflict`（带 `fields`），最终由 `lower()` 唯一索引保证。

#### `POST /api/v1/users/query`（用户列表）

请求字段：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `include_disabled` | boolean | 否 | 默认 `false`：只返回启用中账号；显式 `true` 时同时返回已停用账号 |
| `page` / `page_size` | integer | 否 | 见 2.5（`page>=1`、`1<=page_size<=100`，越界 `400`） |
| `sort` | string | 否 | 白名单：`username`、`display_name`、`email`、`status`、`system_role`、`created_at`、`updated_at`、`last_login_at`；默认 `-created_at` |

成功：`200`，`data` 为公开用户结构数组 + `pagination`。

行为要点：

- **仅系统管理员**；其余返回 `403 permission_denied`。
  （普通成员需要的是**成员列表**，而不是全系统账号清单。）
- 默认排除停用账号，但它**不是被隐藏**：显式 `include_disabled: true` 即可查到，
  因为停用是数据保留而不是删除（需求文档 4.2 第 6 条）。
  两个口径的 `pagination.total` 都与其返回的条目数一致——计数查询与列表查询
  复用**同一套**筛选条件（第二阶段实测过二者不一致的缺陷）。
- 排序在 SQL 层完成，未知字段返回 `400 invalid_request`，且始终追加 `id` 次级键。
- 只返回账号资料本身，不返回成员关系或聚合统计（避免列表接口变成报表）。

#### `POST /api/v1/users/{user_id}/password`（管理员重置密码）

请求字段：

| 字段 | 类型 | 必填 | 约束 |
| --- | --- | --- | --- |
| `new_password` | string | 是 | ≥`security.password_min_length`（默认 8）且 ≤256 字符；**不做 trim**；字段名是 `new_password` 而不是 `password`，因为本端点不接受旧密码 |
| `expected_updated_at` | string | 是 | UTC ISO 8601，必须等于当前 `updated_at` |

成功：`200`，`data` 为公开用户结构，`updated_at` 已推进。

行为要点：

- **仅系统管理员**；非管理员一律 `403 permission_denied`，**先判权限再查对象**，
  因此该端点不会变成"账号是否存在"的探测接口。
- 目标不存在返回 `404 user_not_found`；版本不匹配返回 `409 version_conflict`
  （写语句本身带版本条件，是原子 CAS）。
- 密码强度与注册**共用同一个校验函数**，两处标准不会分叉。
- 响应是公开用户结构：**明文密码与摘要永不回显**；日志只记录
  "哪个管理员重置了哪个用户的密码"。
- 新密码立即可用于登录，旧密码立即失效。

> **已知限制（2026-10-11 已确认为有意行为）**：重置密码**不会**吊销该用户已有的会话。
> 已发出的会话仍在其 TTL 内有效，直到用户被停用或会话自然过期。
>
> 依据：本系统面向少数人员的内部小组，用于内部进度讨论，安全强度要求不高；
> 重置后由用户重新登录即可。需要"重置 + 立即踢下线"时的现成做法是
> **先停用、再启用**该账号——停用会让既有会话在下一次请求时立即失效。
>
> 该行为由 `TestAdminResetPassword` 显式断言。若日后引入会话代际并改为吊销，
> 该用例会失败，从而强制同步更新本节与《06》7.3 的决策记录。

> **首个管理员由配置初始化，不通过 HTTP 创建**：上述三个端点都要求调用者已是管理员，
> 而系统**刻意不提供**"把自己变成管理员"的端点。首个系统级后台账号在**服务启动时**
> 按 `security.bootstrap_admin_*` 配置录入（见《03-配置与运行.md》3.6.1 与 7.3）。
>
> 该初始化是幂等的，且**绝不覆盖既有账号的密码**：账号已存在时只确保其 `system_role`
> 为 `admin`，因此配置里的一次性密码在账号建好之后即失效。
> 全链路（初始化 → 用该凭据登录 → 创建用户 → 列表 → 重置密码）由集成测试
> `TestBootstrapAdministratorUnlocksAdminEndpoints` 覆盖，冒烟脚本亦覆盖其成功路径。

**对 `today` 语义的影响**：Users 落地后，`is_overdue`、`overdue_task_count` 与项目列表的
逾期判定不再使用服务器 UTC 日历日，而是按**请求者的 `timezone`** 计算"今天"
（需求文档 5.7：底层时间统一 UTC，上层按用户时区计算业务日期）。
`domain.Today(now, timezone)` 是这条规则的唯一实现位置。

### 4.4 Groups ✅ 已实现

**实测行为要点**：

- 创建工作组在**同一事务**内写入 `groups.owner_id` 与 `group_members(owner, manager, active)`，
  返回的 `membership_role` 为 `manager`。不存在"有组但没有管理者"的中间状态。
- **所有者不可被降级或移除**。所有者身份是 `groups.owner_id`，成员行只是它的投影；
  允许降级会让两者静默不一致。请求返回 `403 permission_denied`。
- 非成员访问工作组详情/成员列表返回 `404 group_not_found` 而不是 `403`，
  避免向任意登录用户泄漏"该组存在"。无**管理**权限时同样返回 `404`。
- 加入申请的生命周期：`pending` → `approved` / `rejected`；
  `approved` 时在**同一事务**内写入成员关系，否则会出现"已通过但访问不了组内计划"。
- 重复审核返回 `409 conflict`（Python 版本此处漏捕 `IntegrityError`，实测返回 500）。
- 被拒绝后可**重新申请**：申请记录按 `(group_id, user_id)` 唯一，重新申请是把既有行翻回 `pending`，
  而不是插入新行。
- 移除成员是**软移除**（`status` 置为 `removed`），个人数据保留；被移除者立即无法访问组内计划。

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

### 4.5 Projects ✅ 已实现

**实测行为要点**：

- 创建者必须是**归属工作组的活跃成员**，否则返回 `403 permission_denied`
  （否则会创建出"自己看不见的项目"）。归属组不存在返回 `422`。
- 新项目默认 `status=not_started`、`visibility=private`，创建者为 owner 与成员。
- `progress` 与 `overdue_task_count` 是查询派生字段，见 2.6.2。
- **归档项目只读**：归档后修改项目、修改/创建/删除其下任务、改任务日期、改成员
  一律返回 `409 conflict`（`fields.project_id` 说明原因）；读取仍可用。
- 归档项目默认从列表排除；`include_archived: true` 时可见（需求文档 5.3：历史数据可查询）。
- `visibility` 三个级别的实测差异：
  - `private`：非项目成员不可见（`404`）；
  - `group_readonly`：组员可见、可读已共享任务，但**不能**修改任务，也**不能**修改项目；
  - `group_editable`：组员可修改**已共享**任务，但仍**不能**修改项目共享权限。
- 名称搜索中的 `%` `_` `\` 按**字面量**处理（已转义），不会被当作 LIKE 通配符。
- 项目成员可以来自归属组以外的其他工作组。
- **持有者唯一，且可以转移**：`PATCH /projects/{id}/members/{user_id}` 携带 `role: owner`
  表示**把持有者身份转移给该成员**——在**同一事务**内先降现任持有者、再升目标，
  因此任何时刻都恰好一个持有者（数据库的部分唯一索引保证"至多一个"，事务保证"至少一个"）。
  原持有者随之降为普通成员并失去项目管理权。
- 降级唯一 owner（`role: member`）返回 `403 permission_denied`：持有者不能"空缺"，
  要换人就用转移，而不是先降后升两步——两步之间项目会处于无人可管理的状态。
- 转移目标必须是**有效用户**（未停用），否则返回 `422 validation_error`（`fields.user_id`）：
  把持有者交给停用账号会让项目立刻进入"谁都改不了"的状态。
- 已是持有者时再次请求转移返回 `409 conflict`，而不是"成功但没有变化"。
- 目标不是项目成员时返回 `404`，且**整个转移回滚**（第一步的降级不会留下）。
  这是"必须放在同一事务里"的直接体现。

- `POST /projects`：字段 `name`、`group_id`、`start_date`、`end_date`，可选 `description`、`visibility`、`color`；创建者自动成为 owner/member。
- `POST /projects/query`：字段 `group_id`、`status`、`owner_id`、`name`、`page`、`page_size`、`sort`。
- `GET /projects/{project_id}`：项目详情、成员摘要、派生进度与逾期任务数。
- `PATCH /projects/{project_id}`：owner 修改项目字段；要求 `expected_updated_at`。
- `POST /projects/{project_id}/archive`：owner 或管理员归档项目。
- `POST /projects/{project_id}/members/query`：项目成员列表。
- `POST /projects/{project_id}/members`：owner 添加成员，字段 `user_id`；目标用户必须是有效用户，允许来自项目归属组以外的其他工作组。
- `PATCH /projects/{project_id}/members/{user_id}`：持有者修改成员角色；`role: owner` 表示**转移持有者**（见上），`role: member` 表示降级。
- `DELETE /projects/{project_id}/members/{user_id}`：owner 移除成员，不得移除唯一 owner。

### 4.6 Tasks ✅ 已实现

**实测行为要点**：

- 创建者在同一事务内被写入 `task_members(owner)`；创建者不是项目成员时会被拒绝
  （需先加入项目），不会静默创建一个"创建者不可见"的任务。
- `owner_id` 与 `assignee_id` 组合筛选**必须能命中**：每个成员条件各用一个独立
  `EXISTS` 子查询判定。Python 版本把两个谓词压在同一 JOIN 行上，导致组合筛选恒为空。
- 逻辑删除在**单查与列表两条路径上行为一致**：默认都排除 `deleted_at IS NOT NULL`；
  `include_deleted: true` 时列表可见，但单查仍返回 `404`。
- 层级限制 3 层：创建第 4 层返回 `422`（`fields.parent_id`）。
  父任务必须同项目（`400`，因为这是请求本身的问题）且时间区间覆盖子任务（`422`）。
- `parent_id` 的 JSON 语义：**省略** = 不修改；显式 `null` = 提升为顶层任务。
- `PATCH /tasks/{id}/dates` 只接受 `start_date`、`end_date`、`expected_updated_at`；
  携带 `name` 等字段返回 `422`（契约未定义该字段）。
- 进度与状态联动：显式设置 `progress` 而未给 `status` 时，状态按进度推导
  （0 → `not_started`，1–99 → `in_progress`，100 → `completed`）。
- `priority` 取值 `0`（低）/ `1`（普通）/ `2`（高）/ `3`（紧急）。
  数据库列没有 `CHECK` 约束，该取值集合**完全由领域层保证**，越界返回 `422`。
- 任务成员必须是项目成员（`422`）；该不变式在数据库层没有约束，由用例层保证。
- 任务级不可见返回 `404 task_not_found`；项目级不可见返回 `404 project_not_found`。
- **任务持有者同样可以转移**（需求文档 5.4 的"修改持有者"）：语义与项目一致——
  `role: owner` 在同一事务内先降现任、再升目标；`role: member` 不能降级唯一持有者（`403`）；
  转移目标必须是有效用户（`422`）；已是持有者时再次转移返回 `409`；
  目标不是任务成员时 `404` 且整体回滚。
  注意：任务**成员管理**只要求项目成员身份（`PermWrite`），不要求是任务持有者，
  因此转移持有者不会改变"谁能管理任务成员"。

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

### 4.7 Plan Views ✅ 已实现

计划视图是**只读聚合端点**，共 3 个：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/v1/plan-views/personal` | 个人计划：我是项目成员的项目及其任务 |
| `GET` | `/api/v1/plan-views/groups/{group_id}` | 组内共享计划：本组已共享的项目与已共享任务 |
| `GET` | `/api/v1/plan-views/projects/{project_id}/gantt` | 项目甘特图：层级、日期、状态、进度、成员、里程碑与 `can_edit` |

#### 共同约定

- **不分页**。计划视图是一个整体视图（甘特图必须一次拿到全部条目），
  因此响应里的数组是完整的，不存在"截断但不告知"的情况。
  代价是数据量大时响应会比较大；若组内规模增长到需要分页，应当**改契约**
  （引入分页或要求时间窗口），而不是悄悄截断。
- 汇总统计 `summary` 与数组**由同一个数据集算出**，因此
  `summary.task_count` 恒等于 `tasks` 的长度，不存在"统计与列表各算一套"的空间。
- **逻辑删除的任务一律不出现**。计划视图描述的是"计划"，已删除任务不属于任何计划；
  历史数据仍可通过 `POST /projects/{id}/tasks/query`（`include_deleted: true`）查询。
- **归档项目默认排除**，`include_archived=true` 时纳入；纳入时它的任务也会**同时**出现
  （不会出现"项目在、任务不在"的不一致），且 `is_read_only` 为真、任务的 `can_edit` 为假。
- 派生字段全部复用既有规则，不重写一套：`progress`（简单平均、排除已取消）、
  `is_overdue` / `overdue_task_count`（`end_date < 今天` 且状态未结束）、
  `can_edit`（与任务写入口使用同一个领域判定）。
- 「今天」按**请求者时区**计算，与会话中的用户 `timezone` 一致（见 4.3 末段）。

#### 查询参数（个人视图与组内视图）

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `project_id` | uuid | 限定单个项目；**同时作用于项目与任务两侧** |
| `project_owner_id` | uuid | 按**项目**持有者筛选 |
| `task_owner_id` | uuid | 按**任务**持有者筛选 |
| `task_member_id` | uuid | 按**任务成员**筛选（含持有者） |
| `project_status` | enum | 项目状态 |
| `task_status` | enum | 任务状态 |
| `priority` | int | 任务优先级 `0`–`3` |
| `due_from` / `due_to` | date | 按**任务结束日期**的范围筛选（含端点） |
| `overdue` | bool | 为真时只返回逾期任务 |
| `include_archived` | bool | 见上 |

> **参数命名刻意消歧**：需求文档里的"按持有者筛选"同时可能指项目持有者或任务持有者，
> "按状态筛选"同时可能指项目状态或任务状态。契约用 `project_*` / `task_*` 前缀把两者分开，
> 不使用含义模糊的 `owner_id` / `status`。

> **未知查询参数一律拒绝**（`400 invalid_request`，`fields` 中指出参数名并给出可用清单）。
> 理由与 2.7 对请求体的要求同源：把 `task_status` 拼成 `status` 会被静默忽略，
> 客户端以为筛选生效、实际拿到全量数据，而接口没有任何提示。

甘特图端点**不接受任何查询参数**（携带即 `400`）：它是"一个项目的完整画面"，
任何筛选都会让画面不完整。

#### 成功响应（个人视图）

```json
{
  "data": {
    "group_id": null,
    "projects": [
      {
        "id": "uuid", "name": "Migration", "description": null,
        "group_id": "uuid", "status": "not_started", "visibility": "private",
        "start_date": "2026-10-01", "end_date": "2026-10-31", "color": "#336699",
        "archived_at": null, "is_archived": false, "is_read_only": false,
        "owner_id": "uuid", "my_role": "owner",
        "task_count": 3, "progress": 40, "overdue_task_count": 1,
        "updated_at": "2026-10-11T04:30:54.047739Z"
      }
    ],
    "tasks": [
      {
        "id": "uuid", "project_id": "uuid", "parent_id": null,
        "name": "Prepare migration", "description": null,
        "status": "in_progress", "progress": 40, "priority": 1,
        "start_date": "2026-10-01", "end_date": "2026-10-05",
        "is_milestone": false, "is_shared": true, "is_overdue": false,
        "can_edit": true,
        "owner": {"user_id": "uuid", "username": "alice", "display_name": "Alice", "role": "owner"},
        "members": [
          {"user_id": "uuid", "username": "alice", "display_name": "Alice", "role": "owner"}
        ],
        "updated_by": "uuid", "updated_by_name": "Alice",
        "updated_at": "2026-10-11T04:30:54.047739Z"
      }
    ],
    "summary": {
      "project_count": 1, "task_count": 3,
      "in_progress_task_count": 1, "overdue_task_count": 1, "overall_progress": 40
    }
  }
}
```

- `my_role`：`owner` / `member`；`null` 表示不是项目成员（只通过组内共享可见）。
- `updated_by` / `updated_by_name`：满足需求文档 5.6「查看任务最后修改人和最后修改时间」。
  显示名由服务端在列表查询里一并取出，客户端不必为每一行再查一次用户。
- `members` 已按"持有者优先、显示名"排序，可直接用于"按成员分组"的展示；
  **分组本身是展示行为**，服务端不再提供 `group_by` 参数——它只会改变 JSON 形状，
  而客户端已经拿到了分组所需的全部信息。
- 组内视图的响应与个人视图同形，差别只有 `group_id` 非空，以及内容范围不同。

#### 成功响应（甘特图）

```json
{
  "data": {
    "project": { "...同上的项目投影..." },
    "can_manage_project": true,
    "can_edit_tasks": true,
    "tasks": [ "...同上的任务投影..." ],
    "summary": { "...同上的汇总..." }
  }
}
```

- `can_manage_project`：能否修改项目本身（名称、日期、共享级别、成员）——即是否为持有者。
- `can_edit_tasks`：能否修改该项目下的任务——归档项目恒为 `false`。
- 任务按 `start_date, end_date, id` 排序，客户端依据 `parent_id` 组装层级
  （最多 3 层，见 4.6）。

#### 实测行为要点

- **个人视图的范围是"我是项目成员的项目"**，不额外按 `created_by` 放宽。
  项目创建者默认成为持有者，因此"我创建的项目"在正常情形下就是"我是成员的项目"；
  若把 `created_by = 我` 也算进来，就会出现"把项目移交出去并被移出成员后仍能看到它"
  的越权读取。以成员关系为准。
- **组内视图只包含已共享内容**：项目必须是 `group_readonly` / `group_editable`，
  任务必须 `is_shared = true`（需求文档 4.3："项目已共享但任务未共享时，
  任务不得出现在组内视图"）。**该限制与查看者是不是项目成员无关**——
  即使是项目成员，组内页面看到的也只有已共享任务。
- **逐对象的可见性与可编辑性由领域层的权限函数收敛**，而不是在计划视图里再写一套 SQL 可见性谓词。
  因此组内访客（非项目成员）看不到未共享任务，也不能编辑只读共享任务；
  归档项目下的一切都不可编辑。计划视图的判定与 `tasks` 模块的写入口**同源**。
- 非本组活跃成员访问组内视图返回 `404 group_not_found`；不可见的项目访问甘特图返回
  `404 project_not_found`（与"不存在"同码，不泄漏存在性）。
- **组内视图里项目的 `progress` 与 `overdue_task_count` 基于该视图可见的任务计算**，
  与 `GET /projects/{id}` 的口径（基于项目**全部**任务）可能不同。
  这是刻意的：组内访客看不到未共享任务，若按全部任务计算，就会用进度数值泄漏
  未共享任务的存在与进展。个人视图与甘特图在查看者为项目成员时与项目详情一致。

错误：

| 情况 | 状态码 | 错误码 |
| --- | --- | --- |
| 未登录 | `401` | `authentication_required` |
| 非本组活跃成员访问组内视图 | `404` | `group_not_found` |
| 项目不存在或不可见 | `404` | `project_not_found` |
| 未知查询参数 / 非法 UUID / 非法状态 / 非法优先级 / 非法布尔值 / 非法日期格式 | `400` | `invalid_request`（带 `fields`） |
| `due_from` 晚于 `due_to` | `400` | `invalid_date_range` |
| 路径参数不是合法 UUID | `400` | `invalid_request` |

> **时间尺度（日 / 周 / 月）是纯展示参数，服务端不提供**：响应已经包含完整的日期区间，
> 客户端可自由切换尺度。若将来需要服务端提供周/月分桶（例如为了避免前后端对"一周从哪天开始"
> 理解不一致），应作为契约新增项引入，而不是现在猜一个形状。
> 同理，"折叠""拖拽改日期"分别属于客户端状态与既有的 `PATCH /tasks/{id}/dates`。

### 4.8 Attachments、Reminders、Audit ⬜ 规划中（第三阶段剩余）

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
| 2.6 | `expected_updated_at` 为**必填**，缺失返回 `422` | Python 版本设为可选，省略即跳过校验，版本协议静默退化为 last-write-wins |
| 2.6.1 | `copy` 重放返回 `200`、首次创建返回 `201` | 两者都是 2xx，仅凭成功无法判断是否产生了新对象 |
| 2.6.2 | `updated_at` 必须带亚秒精度 | 客户端原样回传作为版本；截断到秒会让 CAS 必然失败 |
| 4.6 | `PATCH /tasks/{id}/dates` 只接受日期与版本字段 | Python 版本复用全量更新结构，名称/状态/进度可被"仅日期"端点修改 |
| 4.6 | 归档项目对其下任务**只读** | Python 版本 `_can_manage_task` 未检查 `project.status`，归档后仍可修改 |
| 4.4–4.6 | 组合筛选（`owner_id` + `assignee_id`）必须能命中 | Python 版本把两个谓词压在同一 JOIN 行上，组合筛选恒为空 |
| 4.3–4.6 | 项目级不可见返回 `project_not_found`，任务级返回 `task_not_found` | 用同一错误码覆盖两种情况会让前端提示失准 |
| 4.3 | 资料更新拒绝"只有版本、无任何可改字段"的请求（422） | 无变更的写同样推进 `updated_at`，让其他客户端手里的版本无谓失效 |
| 4.3 | `GET /users/{id}` 的"不可见"与"不存在"使用同一错误码 `user_not_found` | 用 403/404 的差异会让任意登录用户把接口变成账号枚举器 |
| 4.3 | 系统必须至少保留一个启用中的管理员，否则 409 | 管理员是唯一的管理入口，清零后没有任何接口可以恢复 |
| 4.3 | `POST /users/{id}/disable` 对已停用用户返回 409 | 与上一条同理：不让无变更请求推进版本，也不把"本来就已经停用"报成成功 |
| 4.3 | `is_overdue` / `overdue_task_count` 按**请求者时区**计算"今天" | 固定时区（服务器 UTC 或硬编码 UTC+8）会让不在该时区的成员看到错误的逾期标记 |
| 4.3 | 创建用户的端点**不接受** `system_role`，新账号固定 `member` | 允许在创建时直接指定管理员，就绕过了"至少保留一个启用中管理员"的判定与一次显式可审计的角色变更 |
| 4.3 | 管理员代建账号与自助注册**共用同一套实现**，不复制注册流程 | 两套实现迟早会在密码强度、归一化或唯一性检查上分叉，而这三项都属于安全边界 |
| 4.3 | 用户列表的"是否含停用账号"必须显式传 `include_disabled`，两个口径的 `total` 都与条目数一致 | 默认隐藏会让管理员以为账号消失了；而计数与列表各写一套筛选会出现 total 与实际条目数不符（第二阶段实测缺陷） |
| 4.3 | 重置密码**先判权限再查对象**，非管理员对任何 ID 都得到同一个 403 | 否则该端点会变成"账号是否存在"的探测接口 |
| 4.7 | 计划视图的查询参数**未知即拒绝**，且用 `project_*` / `task_*` 前缀消歧 | 拼错的参数名会被静默忽略，客户端以为筛选生效却拿到全量数据；而"持有者/状态"不加前缀时无法判断指的是项目还是任务 |
| 4.5–4.6 | `role: owner` 解释为**转移持有者**（同事务先降后升），而不是"新增第二个持有者"；降级唯一持有者返回 403 | 持有者唯一，因此"设为持有者"只可能是转移。修复前的实现把该请求映射成"提升"，又在已存在持有者时拒绝，导致持有者**永远无法变更**，而错误文案却让用户"先转移持有者" |

## 7. 变更规则

1. 契约变更必须先改本文档，再改代码；实现与文档冲突时以文档为准。
2. 新增端点必须同时补：请求字段约束、成功响应形状、全部失败状态码与错误码、
   并发与幂等语义、以及在《06-迁移进度与验证.md》中登记的测试覆盖维度。
3. 新增错误码必须同步更新 2.4 的错误码表与 `httpapi/problem.go` 的映射表；
   测试会断言映射完整性。
4. 不得用空路由或仅返回成功的占位实现冒充"已实现"。

## 8. 变更记录

| 日期 | 变更 | 原因 |
| --- | --- | --- |
| 2026-10-11 | 建立本文档：公共约定、资源表示、已实现端点的实测行为、契约加强项汇总 | Go 重写第一阶段 |
| 2026-10-11 | 4.4–4.6 标记为已实现并补实测行为要点；2.5 / 2.6 / 2.6.1 / 2.6.2 / 2.7 补充实现约束与实测样例 | 第二阶段（Groups / Projects / Tasks）完成 |
| 2026-10-11 | 4.3 Users 由规划改为**已实现**：补齐三个端点的字段表、错误表与行为要点；说明未实现的用户管理动作与原因；新增第 6 节 5 条契约加强项（空更新拒绝、可见性错误码、最后管理员不变式、重复停用、按用户时区计算"今天"） | 第三阶段 Users 模块落地，契约必须先于代码成为唯一权威 |
| 2026-10-11 | 4.3 补齐**管理员端点**：`POST /users`（创建）、`POST /users/query`（列表，含 `include_disabled`）、`POST /users/{user_id}/password`（重置密码）；新增 4 条契约加强项；显式登记两处限制（重置不吊销既有会话、缺少首个管理员的受支持初始化路径） | 需求文档 5.1 要求的管理员用户管理动作落地；两处限制必须先写清再由人工决策，不能默默留白 |
| 2026-10-11 | 两处限制按人工决策收口：首个管理员改为**配置驱动、启动时初始化**（不再需要直接写库），并说明其幂等与"绝不覆盖密码"语义；重置密码不吊销会话**确认为有意行为**（内部小组、安全要求不高），保留"先停用再启用"作为立即踢下线的做法 | 用户确认：普通用户不需要管理员角色，只需初始化时录入一个；会话吊销保持现状 |
| 2026-10-11 | 4.7 Plan Views 由规划改为**已实现**：三个端点的完整字段表、筛选参数表、成功/错误响应与实测行为要点；显式声明五处设计选择（不分页、统计与列表同源、逻辑删除一律排除、未知参数拒绝、组内视图 progress 基于可见任务）；新增 1 条契约加强项 | 第三阶段 Plan Views 落地；其中"组内视图的 progress 与项目详情口径不同"属于必须让客户端知道的差异 |
| 2026-10-11 | 4.5 / 4.6 修正**持有者转移**语义：`role: owner` 表示同事务内先降现任、再升目标的转移；补记转移目标必须是有效用户、已被拒绝的情形与回滚保证 | 修复"持有者永远无法变更"（`promote` 恒被拒绝、`demote` 恒被拒绝，而文案却让用户先转移）：需求文档 5.4 明确把"修改持有者"列为任务支持的编辑项 |
