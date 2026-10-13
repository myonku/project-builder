# A08 提醒接口详解

> 归属：**前后端共用**（`docs/api/`）。通用约定（基础路径 `/api/v1`、成功/错误信封、认证、分页、并发与幂等、限流、未知字段拒绝）见《S02-HTTP接口契约》§2，本文不重复。
> 契约正文（约定性叙述）见《A08》；本文补齐每个接口的**请求与响应数据结构**。

## 接口清单

| # | 方法 | 路径 | 权限 | 说明 |
| --- | --- | --- | --- | --- |
| 1 | `GET` | `/api/v1/reminders` | 登录用户；普通用户只看发给自己的，系统管理员看全部 | 提醒列表（筛选、分页、排序） |
| 2 | `GET` | `/api/v1/reminders/{reminder_id}` | 收件人本人或系统管理员 | 提醒详情 |
| 3 | `POST` | `/api/v1/reminders/{reminder_id}/retry` | 收件人本人或系统管理员 | 手动重试：仅 `failed` / `skipped` 可重试，其余 `409` |

3 个端点都要求登录（会话 Cookie，见《S02》§2.2）；未登录返回 `401 authentication_required`。

**作用域（三个端点一致）**：普通用户只能看到**发给自己的**提醒（`recipient_id = 当前用户`）；系统管理员看全部——"谁没收到、为什么没收到"是处置异常所需的信息。**他人的提醒按不存在处理（`404`）**，不是 `403`：这样"这个 id 存不存在"本身不会被非管理员探测出来。

## 提醒对象

本文所有返回提醒的端点（1 的 `data[]` 元素，2、3 的 `data`）结构一致：

```json
{
  "data": {
    "id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e1f",
    "task_id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e20",
    "recipient_id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e30",
    "recipient_email": "alice@example.com",
    "kind": "task_due_soon",
    "kind_label": "任务即将到期",
    "scheduled_for": "2026-10-12T01:00:00Z",
    "status": "failed",
    "attempts": 1,
    "last_error": "SMTP 连接超时",
    "sent_at": null,
    "created_at": "2026-10-11T03:00:00Z",
    "updated_at": "2026-10-12T01:00:05.417293Z"
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string(UUID) | 提醒 id（详情与重试都用它） |
| `task_id` | string(UUID) | 关联任务 |
| `recipient_id` | string(UUID) | 收件人用户 id |
| `recipient_email` | string | 收件人邮箱，是**生成提醒那一刻的快照**。用户之后改邮箱不会改写既有提醒，因此它回答的是"当时发到哪个地址" |
| `kind` | string | 提醒类型枚举，见下 |
| `kind_label` | string | 类型的中文说明，**前端可直接展示**，不必自己维护映射表 |
| `scheduled_for` | string | **计划发送时刻**（UTC，可能带亚秒）。由工作日规则与收件人时区算出，是"什么时候该发"而不是"什么时候发了" |
| `status` | string | 状态枚举，见下 |
| `attempts` | number | 已尝试次数：**发送成功、发送失败、被跳过都各计一次**。因此 `attempts >= 1` 只说明"这一行至少被处理过一轮" |
| `last_error` | string | 失败原因或跳过原因；成功时为**空串**（不是 `null`）。失败原因可能被截断（发送失败最多 500 字符，邮件功能未启用最多 200 字符） |
| `sent_at` | string \| null | 发送成功时刻；未成功时为 `null` |
| `created_at` / `updated_at` | string | 记录写入时间 / 最后一次状态变更时间 |

刻意**不返回** `dedupe_key`：它是幂等机制的载体（`<类型>:<任务>:<收件人>:<时间窗口>`），属于实现细节，客户端不需要也不该依赖它。

### `kind` 枚举（5 种）

| `kind` | `kind_label` | 触发方式 | 含义 |
| --- | --- | --- | --- |
| `task_assigned` | 新任务分配 | 事件型 | 用户被加入任务成员 |
| `task_start_soon` | 任务即将开始 | 时间型 | 任务开始日**前 1 个工作日** |
| `task_due_soon` | 任务即将到期 | 时间型 | 任务到期日**前 1 个工作日**（尚未开始的任务不产生此类提醒——它该收的是"开始前"提醒） |
| `task_overdue` | 任务已延期 | 时间型 | 到期日已过；**每个任务只提醒一次** |
| `task_owner_changed` | 任务持有者变更 | 事件型 | 任务持有者被转移给该用户 |

枚举取值在领域常量与本文档两处一致；服务端**不校验** `kind` 查询参数的取值集合（见端点 1 的备注）。

### `status` 枚举（4 种）

| `status` | 含义 | 是否可手动重试 |
| --- | --- | --- |
| `pending` | 待发送（在队列中，计划时间未到或尚未被派发循环取走） | 否，`409`（已在队列中，无需重试） |
| `sent` | 已发送成功 | 否，`409`（重试等于重复发送） |
| `failed` | **发送尝试失败**：SMTP 报错、连接超时等，需要人排查 | 是 |
| `skipped` | **按规则就不该发**：收件人没有邮箱/已停用/关闭了邮件提醒、任务已完成/已取消/已删除、**邮件功能未启用** | 是（原因可能已经消失，例如运维刚把 SMTP 配好） |

`failed` 与 `skipped` **必须分开消费**：把 `skipped` 混进失败列表，会让失败列表永远噪声不断，而 `failed` 的语义是"需要人排查"。前端做"异常提醒"视图时应只取 `failed`。

**为什么 `sent` 不允许重试**：需求要求"同一提醒不重复发送"。`pending` 与 `sent` 的重试请求都返回 `409 conflict`，且**不产生任何副作用**（不写审计、不改 `attempts`）。

## 1. `GET /api/v1/reminders`

**用途**：分页查询提醒；普通用户查自己的提醒收件箱，管理员用来排查"谁没收到、为什么没收到"。
**权限**：登录用户；系统管理员看全部。

**路径参数**：无。

**查询参数**：

| 参数 | 类型 | 默认 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `kind` | string | 无（不筛选） | 建议取 5 个枚举值之一 | 按提醒类型筛选 |
| `task_id` | string(UUID) | 无（不筛选） | — | 按任务筛选（"这条任务的提醒都发给谁了"） |
| `page` | number | `1` | `>= 1`，越界 `400` | 页码 |
| `page_size` | number | `20` | `1..100`，越界 `400` | 每页条数 |
| `sort` | string | `-scheduled_for` | 白名单见下 | 排序表达式，逗号分隔多字段，前缀 `-` 表示降序 |

`sort` 白名单：`scheduled_for`、`created_at`、`updated_at`、`kind`、`status`、`attempts`。默认按**计划发送时间倒序**（最近要发的排前面）。服务端始终追加 `id` 作为次级排序键，保证翻页不重不漏。

**请求体**：无请求体。

请求示例：

```http
GET /api/v1/reminders?kind=task_overdue&page=1&page_size=20&sort=-scheduled_for
Cookie: <会话 Cookie>
```

**响应 `200`**：

```json
{
  "data": [
    {
      "id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e1f",
      "task_id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e20",
      "recipient_id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e30",
      "recipient_email": "alice@example.com",
      "kind": "task_overdue",
      "kind_label": "任务已延期",
      "scheduled_for": "2026-10-11T00:00:00Z",
      "status": "sent",
      "attempts": 1,
      "last_error": "",
      "sent_at": "2026-10-11T01:02:03Z",
      "created_at": "2026-10-11T00:00:00Z",
      "updated_at": "2026-10-11T01:02:03.114287Z"
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
| `data` | array | 提醒对象数组；无数据时是 `[]`（不是 `null`） |
| `pagination` | object | 通用分页元信息，`has_next = page * page_size < total`（见《S02》§2.3） |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | 出现白名单之外的查询参数（`fields.query` 列出未知参数与允许的参数）；`page` / `page_size` 越界；`sort` 含未知或重复字段（`fields.sort`） |
| `401` | `authentication_required` | 未登录或会话失效 |

**备注**：

- **未知查询参数一律拒绝**，不静默忽略：把 `kind` 拼成 `kinds` 会被返回 `400`，而不是"筛选没生效但看起来成功了"。允许的参数集合就是上表 5 个。
- **没有 `status` 筛选参数**（虽然服务端内部的数据筛选条件支持状态）：要按状态过滤，请在客户端对 `data` 过滤，或分别用 `kind` 收窄。传 `status=...` 会得到 `400 invalid_request`。
- `kind` 传一个**格式合法但不在枚举内**的值（如 `foo`）**不会**返回 `400`，而是返回空列表——服务端对 `kind` 只做等值匹配、不校验取值集合。契约未把"非法 kind"列为错误，前端应只用 5 个枚举值。
- **列表里能看到什么**：`status` / `last_error` / `attempts` / `sent_at` 就是发送结果的完整对外表示。后台派发产生的 `sent` / `failed` / `skipped` **不会**写进操作记录（见下面的"发送结果与操作记录"）。
- **总条数与作用域同源**：非管理员看不到的提醒既不出现在 `data` 里，也不计入 `pagination.total`。

## 2. `GET /api/v1/reminders/{reminder_id}`

**用途**：读取单条提醒的完整状态（排查"这条到底发出去了没有、为什么没发出去"）。
**权限**：收件人本人或系统管理员；其他人按不存在处理。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `reminder_id` | string(UUID) | 提醒 id |

**查询参数**：无。

**请求体**：无请求体。

**响应 `200`**：提醒对象（结构与端点 1 的元素**完全同源**——同一构造函数，不存在"详情比列表多字段"的情况）。

```json
{
  "data": {
    "id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e1f",
    "task_id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e20",
    "recipient_id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e30",
    "recipient_email": "alice@example.com",
    "kind": "task_overdue",
    "kind_label": "任务已延期",
    "scheduled_for": "2026-10-11T00:00:00Z",
    "status": "failed",
    "attempts": 1,
    "last_error": "SMTP 连接超时",
    "sent_at": null,
    "created_at": "2026-10-11T00:00:00Z",
    "updated_at": "2026-10-11T01:00:05.417293Z"
  }
}
```

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `401` | `authentication_required` | 未登录或会话失效 |
| `404` | `reminder_not_found` | 提醒不存在；**或提醒不属于当前用户且当前用户不是系统管理员**（两者同码，不泄漏存在性） |

**备注**：只读端点，没有 `409` 语义；`ETag` / `If-None-Match` 不支持，请用 `updated_at` 判断是否变化。

## 3. `POST /api/v1/reminders/{reminder_id}/retry`

**用途**：把一条失败或被跳过的提醒**重新排入待发送**（`status` 回到 `pending`、`scheduled_for` 改为当前时刻），实际发送由后台派发循环完成。
**权限**：收件人本人或系统管理员；其他人按不存在处理。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `reminder_id` | string(UUID) | 提醒 id |

**查询参数**：无。

**请求体**：无请求体（服务端不读取请求体，发 `{}` 或不发都可以）。

**响应 `200`**：更新后的提醒对象。**重试不立即发信**，因此响应里 `status` 为 `pending`、`sent_at` 仍为 `null`、`last_error` 被清空为空串、`attempts` 保持重试前的值、`scheduled_for` 变为重试时刻。

```json
{
  "data": {
    "id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e1f",
    "task_id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e20",
    "recipient_id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e30",
    "recipient_email": "alice@example.com",
    "kind": "task_overdue",
    "kind_label": "任务已延期",
    "scheduled_for": "2026-10-12T02:30:00Z",
    "status": "pending",
    "attempts": 1,
    "last_error": "",
    "sent_at": null,
    "created_at": "2026-10-11T00:00:00Z",
    "updated_at": "2026-10-12T02:30:00.881402Z"
  }
}
```

| 字段 | 重试后的取值 | 说明 |
| --- | --- | --- |
| `status` | `"pending"` | 已重新排队 |
| `scheduled_for` | 重试请求时刻 | 取当前时刻（不是原计划时间），因此下一轮派发会立刻取走它 |
| `last_error` | `""` | 上一次的失败/跳过原因被清空 |
| `attempts` | 不变 | 重试本身不计次数；真正的下一次发送结果才会 `+1` |
| `sent_at` | `null` | 尚未发送 |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `401` | `authentication_required` | 未登录或会话失效 |
| `404` | `reminder_not_found` | 提醒不存在；或不属于当前用户且不是系统管理员 |
| `409` | `conflict` | **`status = pending`**：`fields.reminder_id = "该提醒仍在待发送队列中，无需重试"` |
| `409` | `conflict` | **`status = sent`**：`fields.reminder_id = "该提醒已发送成功，重试会导致重复发送"` |

**允许/拒绝条件速查**：

| 当前 `status` | 结果 |
| --- | --- |
| `failed` | `200`，重新排入待发送 |
| `skipped` | `200`，重新排入待发送（原因可能已消失，例如 SMTP 刚配好） |
| `pending` | `409 conflict` |
| `sent` | `409 conflict` |

**备注**：

- **状态判定与写入是一步的**：写语句带 `status IN ('failed','skipped')` 条件，0 行更新时再查一次以区分"不存在"（`404`）与"状态不允许"（`409`）。因此并发重试只有一次会成功，另一次得到 `409`。
- **副作用（唯一会写操作记录的提醒动作）**：重试成功会写一条操作记录 —— `object_type = reminder`、`action = updated`、`object_id` = 提醒 id，`changes = {"status":"pending","reason":"manual_retry"}`、`actor_id` = 执行重试的人。被拒的重试（`409`）**不留记录**。
- **重试后的发送结果仍是后台行为**：派发循环下次运行时若发送成功，状态变 `sent` 且 `attempts + 1`；若再次失败，回到 `failed` 并更新 `last_error`。前端应轮询/刷新详情，而不是期望重试响应就是最终结果。

## 发送结果与操作记录（对外可见的差异）

这一点直接影响前端怎么做"异常排查"页面：

| 事件 | 是否写操作记录（`GET /api/v1/audit`） | 在哪里可见 |
| --- | --- | --- |
| 后台派发：发送成功（`sent`） | **不写** | `GET /api/v1/reminders` 的 `status` / `sent_at` / `attempts` |
| 后台派发：发送失败（`failed`） | **不写** | 同上，另有 `last_error` |
| 后台派发：按规则跳过（`skipped`） | **不写** | 同上，`last_error` 即跳过原因 |
| 人工重试（本身） | **写**（`action = updated`） | 两个地方都能看到 |

**为什么发送结果不写操作记录**：后台派发循环是周期任务，**没有操作人**，而操作记录的 `actor_id` 是必填外键（审计必须有主体）。把收件人或任务持有者当成操作人会记下"一个人没做过的事"。发送结果本来就完整地记在提醒行上，通过 `GET /api/v1/reminders` 可查；而**人工重试有操作人**，因此照常入账。

推论：想回答"这封提醒为什么没发出去"，要查的是 **reminders 列表的 `status` / `last_error` / `attempts`**，不是 `GET /api/v1/audit`；audit 只能回答"谁在什么时候点了重试"。

## 生成规则（客户端的预期边界）

| 规则 | 对外表现 |
| --- | --- |
| 时间型提醒"提前 1 个工作日" | 周一到期 → 上周五生成/发送；周末不产生提醒日 |
| 计时按**收件人所在时区**计算"今天" | 同一任务对不同时区的成员可能落在不同的提醒日；`scheduled_for` 是收件人本地上午 9 点（`ReminderSendHour = 9`）换算成的 UTC 时刻 |
| 延期提醒每个任务只提醒一次 | 同一任务不会出现第二条 `task_overdue` |
| 收件人范围 | 任务成员（含任务持有者）与**项目持有者** |
| 不合格的收件人 | 生成阶段**不落库**（不会有 `skipped` 行）；但派发时若收件人已停用/关闭提醒，该行会如实记为 `skipped` 并带原因 |
| 事件型提醒（新分配、持有者变更） | 在任务写入路径**事务提交后**插入一行，发送仍由后台循环完成 |
| 幂等 | 重复扫描、重复运行不会重复生成或重复发送（唯一约束保证） |
| 邮件内容 | 至少含项目名、任务名、负责人、计划周期、状态、进度、任务链接与提醒原因（链接模板来自 `mail.task_url_template`） |

## 不确定或契约未写清的点

| 点 | 说明 |
| --- | --- |
| 非法 `kind` 查询值的行为 | 服务端只做等值匹配、不校验枚举，因此返回空列表而不是 `400`；《A08》 未规定该情形。 |
| `status` 查询参数 | 服务端数据层支持按状态筛选，但端点的**查询参数白名单不含 `status`**，传它会 `400`。这是实现与数据层能力的差异，契约未提及；若前端需要"只看失败"的视图，需要先在契约里新增该参数。 |
| `task_id` 传非 UUID | 端点的查询参数未做 UUID 格式校验，非 UUID 会走到数据库并可能以 `500 internal_error` 结束。契约未规定；建议恒传 UUID。 |
| `reminder_id` 路径参数非 UUID | 与 `task_id` 同理：无前置格式校验，《S02》未规定错误码。 |
| `scheduled_for` 与 `created_at` 的关系 | 时间型提醒的 `scheduled_for` 是"计划时刻"（收件人本地 9 点），事件型提醒取写入时刻。契约未逐一规定每种 `kind` 的构造公式，本文只写可观测结论。 |
