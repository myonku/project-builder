# 接口详解（前后端共用）

本目录是**逐接口的详细说明**：每个端点的方法、路径、权限、路径/查询参数、请求体字段、
响应数据结构、错误码与边界行为。前端对接直接读这里。

## 怎么读

| 顺序 | 文档 | 作用 |
| --- | --- | --- |
| 1 | [S01-需求文档.md](../S01-需求文档.md) | 业务范围与验收边界（**业务需求的唯一权威来源**） |
| 2 | [S02-HTTP接口契约.md](../S02-HTTP接口契约.md) | **约定性叙述**：基础格式、认证、成功/错误信封、分页与排序、并发与幂等、限流、字段严格性、错误码表、契约加强项 |
| 3 | 本目录（按域查阅） | 每个接口的**具体请求/响应数据结构** |

- 约定只写在《S02》，本目录**不重复**；两者冲突时以《S02》为准（它是契约正文）。
- 本目录只描述**对外可见行为**，不写服务端实现细节。
- 字段名一律是 JSON 里的名字（`snake_case`），与《S02》§2.1 的基础格式一致。

## 域目录

| 文档 | 域 | 端点前缀 | 端点数 |
| --- | --- | --- | --- |
| [A01-auth.md](A01-auth.md) | Auth：注册、登录、登出、当前用户 | `/api/v1/auth/*` | 4 |
| [A02-users.md](A02-users.md) | Users：资料、管理员创建/列表/停用/重置密码 | `/api/v1/users/*` | 6 |
| [A03-groups.md](A03-groups.md) | Groups：工作组、成员、加入申请、所有权转移 | `/api/v1/groups/*` | 11 |
| [A04-projects.md](A04-projects.md) | Projects：项目、成员、归档、持有者转移 | `/api/v1/projects/*` | 9 |
| [A05-tasks.md](A05-tasks.md) | Tasks：任务、成员、层级、幂等复制 | `/api/v1/tasks/*`、`/api/v1/projects/{project_id}/tasks` | 11 |
| [A06-plan-views.md](A06-plan-views.md) | Plan Views：个人计划、组内共享计划、项目甘特图 | `/api/v1/plan-views/*` | 3 |
| [A07-attachments.md](A07-attachments.md) | 附件：上传、列表、下载、逻辑删除、彻底删除 | `/api/v1/attachments/*` 等 | 7 |
| [A08-reminders.md](A08-reminders.md) | 提醒：列表、详情、手动重试 | `/api/v1/reminders/*` | 3 |
| [A09-audit.md](A09-audit.md) | 操作记录（仅系统管理员可查） | `/api/v1/audit` | 1 |

合计 **55 个业务端点**，另有 4 个健康检查端点（见下）。

> 本目录的文件按域编号（`A01`…）。共用文档用 `S` 前缀，服务端文档用 `B` 前缀
> （见 `backend/docs/B04-设计偏离与差异登记.md` §7.2），前端将来的文档用 `F` 前缀。

## 健康检查（无需登录）

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/api/v1/health/check` | 要求 `arg` 请求头；缺失返回 `422`，成功 `200` |
| `GET` | `/api/v1/health/live` | 存活检查，**不检查**任何外部依赖 |
| `GET` | `/api/v1/health/ready` | 就绪检查，真实检查数据库与 Redis |
| `GET` | `/healthz` | 根级存活探针（无版本前缀），供容器/反向代理使用 |

`/health/ready` 的响应体是**例外形状**（不带 `data` 信封）：

| 情况 | 状态码 | 响应体 |
| --- | --- | --- |
| 全部可用 | `200` | `{"status":"ready"}` |
| 数据库不可用 | `503` | `{"status":"not_ready","error":"database_unavailable"}` |
| Redis 不可用（且已启用） | `503` | `{"status":"not_ready","error":"redis_unavailable"}` |

`redis.enabled=false` 时 `/health/ready` 不检查 Redis（视为通过）。

## 约定速查（细节见《S02》§2）

```jsonc
// 成功
{ "data": { /* 对象或数组 */ }, "pagination": { "page": 1, "page_size": 20, "total": 42, "has_next": true } }

// 失败
{ "error": { "code": "validation_error", "message": "…", "fields": { "username": "…" } } }
```

- **认证**：会话 Cookie（登录成功后由服务端下发），登录态相关的约定见《S02》§2.2。
- **分页/排序**：`page`、`page_size`、`sort`，白名单取值见各域文档与《S02》§2.5。
- **并发控制**：更新类接口要求 `expected_updated_at`，版本过期返回 `409 version_conflict`（《S02》§2.6）。
- **幂等**：任务复制用 `Idempotency-Key` 请求头（《S02》§2.6）。
- **未知字段**：请求体里出现契约未定义的字段返回 `422`（《S02》§2.7），用于防止拼错字段名被静默忽略。
- **限流**：超过阈值返回 `429` 且带 `Retry-After`（《S02》§2.8）。

## 这些文档怎么保持可信

1. 逐接口内容以《S02》契约（已实测）为准，字段类型/必填/约束/错误码**回到代码核对**
   （路由注册、绑定结构、领域校验、错误码映射表）。
2. 域文档顶部的"接口清单"必须与实际注册的路由**逐条一致**（方法 + 路径），
   端点数与《B05-进度与验证记录.md》第 2 节登记的 59 个端点对齐。
3. 新增或修改端点时：**先改《S02》契约，再改这里，最后改代码**。

## 公共资源表示（字段名与语义的唯一出处）

下面三张表是**多个端点共用的规范化结构**：字段名与语义以这里为准，
各端点文档只写"自己特有或与公共结构不同"的部分。
（本节原先在《S02》§3，2026-10-13 移到这里：契约只保留约定性叙述，具体字段结构归接口详解。）


### 1. 用户（公开结构）

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

### 2. 项目

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

### 3. 任务

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

