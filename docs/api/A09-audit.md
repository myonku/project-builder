# A09 操作记录接口详解

> 归属：**前后端共用**（`docs/api/`）。通用约定（基础路径 `/api/v1`、成功/错误信封、认证、分页、并发与幂等、限流、未知字段拒绝）见《S02-HTTP接口契约》§2，本文不重复。
> 契约正文（约定性叙述）见《A09》；本文补齐该接口的**请求与响应数据结构**。

> **2026-10-20 规划（未生效）**：审计记录将增加 `actor_avatar_updated_at`（随 actor 投影 JOIN 取得）；
> 头像更新复用 `user/updated` 动作，`changes` 只记 `avatar=set|cleared`。见 [P01](../P01-Worklane-规划-2026-10-20.md) §4。

## 接口清单

| # | 方法 | 路径 | 权限 | 说明 |
| --- | --- | --- | --- | --- |
| 1 | `GET` | `/api/v1/audit` | **仅系统管理员** | 操作记录列表（筛选、分页、排序） |

要求登录（会话 Cookie，见《S02》§2.2）。

**权限语义**：

| 调用者 | 结果 |
| --- | --- |
| 未登录 / 会话失效 | `401 authentication_required` |
| 已登录但不是系统管理员（含**组管理员**） | `403 permission_denied` |
| 系统管理员 | `200` |

**为什么是 `403` 而不是 `404`**：审计端点的存在不是秘密（需求文档 §4.1 把"查看全局操作记录"列为系统管理员的职责），而"到底有没有这条记录"本来就只有管理员能看见。组管理员**不扩权**。

**为什么没有 `404` / `409`**：这是只读集合端点，"对象不存在"与"版本冲突"两个维度都不适用；筛选命中 0 条是 `200` + 空数组。

## 1. `GET /api/v1/audit`

**用途**：按对象、操作人、动作与时间范围检索操作记录，回答"这个对象被谁、什么时候改过"。
**权限**：仅系统管理员。

**路径参数**：无。

**查询参数**（《A09》 概括为 7 个维度：对象类型、对象 id、操作人、动作、时间范围、分页、排序；时间范围与分页各含两个参数名，因此下表共 9 行。**出现其它任何参数一律 `400`**）：

| 参数 | 类型 | 默认 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `object_type` | string | 无（不筛选） | 枚举见下，非法值 `400` | 被操作对象的类型 |
| `object_id` | string(UUID) | 无（不筛选） | 必须是合法 UUID，否则 `400` | 被操作对象 id |
| `actor_id` | string(UUID) | 无（不筛选） | 必须是合法 UUID，否则 `400` | 操作人用户 id |
| `action` | string | 无（不筛选） | 枚举见下，非法值 `400` | 动作 |
| `from` | string(RFC3339) | 无（不筛选） | 合法 RFC3339，否则 `400` | 时间下界，**含**（`created_at >= from`） |
| `to` | string(RFC3339) | 无（不筛选） | 合法 RFC3339；早于 `from` 返回 `400 invalid_date_range` | 时间上界，**含**（`created_at <= to`） |
| `page` | number | `1` | `>= 1`，越界 `400` | 页码 |
| `page_size` | number | `20` | `1..100`，越界 `400` | 每页条数 |
| `sort` | string | `-created_at` | 白名单见下 | 排序表达式，逗号分隔多字段，前缀 `-` 表示降序 |

参数可任意组合（AND 关系）。空值（`from=`、`to=`、`object_type=`）按"未提供"处理，即不筛选。

**`from` / `to` 的时间格式与闭区间语义**：

- 格式为 **RFC3339**，例如 `2026-10-12T00:00:00Z` 或带偏移的 `2026-10-12T08:00:00+08:00`；只写日期（`2026-10-12`）**不合法**（`400 invalid_request`，`fields.from` / `fields.to` 给出示例）。
- 值会被换算为 UTC 后比较。
- **两端都含**：`from` 命中同一时刻的记录，`to` 同样命中同一时刻的记录。因此"取某一天"要写 `from=<当天 00:00:00Z>&to=<次日 00:00:00Z>` 会多带一条次日零点整的记录；正确写法是按业务需要的**半开区间在客户端裁剪**，或接受这个边界包含。
- `to < from` 返回 `400 invalid_date_range`（`fields.to`）——这是《S02》§2.4 单独登记的错误码，便于前端区分"区间写反了"与"参数格式错"。

**`object_type` 完整枚举（6 个）**：

| `object_type` | 含义 |
| --- | --- |
| `user` | 用户 |
| `group` | 工作组 |
| `project` | 项目 |
| `task` | 任务 |
| `attachment` | 附件 |
| `reminder` | 提醒 |

**`action` 完整枚举（13 个）**：

| `action` | 含义 |
| --- | --- |
| `created` | 创建 |
| `updated` | 修改（**人工重试提醒也记为 `updated`**） |
| `archived` | 归档 |
| `deleted` | 逻辑删除 |
| `purged` | 彻底删除（不可逆动作） |
| `member_added` | 成员加入 |
| `member_role_changed` | 成员角色变更（含"把某人设为持有者"这类角色改写） |
| `member_removed` | 成员移除 |
| `owner_transferred` | 持有者**转移**（`object_id` 是容器，`changes` 里带新任与前任） |
| `join_request_reviewed` | 工作组加入申请的审核结果 |
| `shared` | 项目共享级别（可见性）变更 |
| `password_reset` | 管理员重置密码 |
| `disabled` | 停用账号 |

枚举值在领域常量、数据库 `CHECK` 约束与《A09》 三处一致，并由测试断言前两者严格相等；本文与《A09》 的清单逐条对应。

**`sort` 白名单**：`created_at`、`action`、`object_type`。默认按 `created_at` **倒序**（最近发生的最先看到）。服务端始终追加 `id` 作为次级排序键，因此同一微秒内的多条记录也有稳定顺序，翻页不重不漏。

**请求体**：无请求体。

请求示例：

```http
GET /api/v1/audit?object_type=project&object_id=01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e20&from=2026-10-12T00:00:00Z&to=2026-10-12T23:59:59Z&page=1&page_size=20&sort=-created_at
Cookie: <会话 Cookie>
```

**响应 `200`**（沿用公共成功信封与分页）：

```json
{
  "data": [
    {
      "id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e1f",
      "actor_id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e30",
      "actor_name": "张三",
      "action": "updated",
      "object_type": "project",
      "object_id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e20",
      "changes": {
        "status": "completed"
      },
      "request_id": "7fa4d1a0a50c68c8ee362a11853873ff",
      "created_at": "2026-10-12T02:11:07.918273Z"
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
| `data` | array | 操作记录数组；无数据时是 `[]`（不是 `null`） |
| `pagination` | object | 通用分页元信息，`has_next = page * page_size < total`（见《S02》§2.3） |

记录对象字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string(UUID) | 记录 id，用于稳定排序与去重 |
| `actor_id` | string(UUID) | 操作人用户 id |
| `actor_name` | string | 操作人**当前的**显示名（不是操作时刻的快照）：账号改名后旧记录跟着显示新名字，更符合排查直觉。用户没设显示名时为用户名 |
| `action` | string | 动作枚举（见上） |
| `object_type` | string | 对象类型枚举（见上） |
| `object_id` | string(UUID) | 被操作对象 id |
| `changes` | object | "字段名 → 新值"的**扁平对象**，见下 |
| `request_id` | string | 与响应头 `X-Request-ID` 同源，把一次请求产生的多条记录串起来；便于对照日志 |
| `created_at` | string | 记录写入时刻（UTC ISO 8601，可能带亚秒） |

**`changes` 的语义（前端最容易误解的一处）**：

- 它**只记录新值**，不记录修改前的旧值（需求只要求"关键变更内容"，不要求完整前后对照）。要还原"从什么改成什么"，只能靠同一对象相邻两条记录对比。
- 它是**扁平**的字符串映射（`map[string]string`），没有嵌套；非字符串值（数字、布尔、日期）都以字符串形式出现。
- **敏感键的值被替换为 `[redacted]`**：键名（小写后）**包含** `password`、`passwd`、`secret`、`token`、`hash`、`credential`、`authorization`、`cookie`、`body`、`content`、`file` 中任一特征时，值固定为字符串 `"[redacted]"`。**键本身保留**——让人看得出"这个字段变过"，但不泄漏内容。判断用"包含"而不是"相等"，因为真实字段常带前缀（`user_password`、`oauth2_access_token`、`mail_body`）。
- 值按**字符**截断到 200 个字符，超出部分以 `…` 结尾。
- 键去重后按**字典序**保留前 20 个（保证结果稳定，不随 map 遍历顺序变化）；空键丢弃。
- 没有任何变更时是空对象 `{}`（不是 `null`）。
- 上传/密码类操作**只记录动作与对象**，不记录内容；附件记录只含元数据（`original_name` / `extension` / `size_bytes` / `project_id` / `task_id`），**绝不含文件内容**。

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | 出现白名单之外的查询参数（`fields.query` 列出未知参数与允许的参数）；`object_type` / `action` 取值不在枚举内（`fields` 对应字段）；`object_id` / `actor_id` 不是合法 UUID；`from` / `to` 不是合法 RFC3339；`page` / `page_size` 越界；`sort` 含未知或重复字段 |
| `400` | `invalid_date_range` | `to` 早于 `from`（`fields.to`） |
| `401` | `authentication_required` | 未登录或会话失效 |
| `403` | `permission_denied` | 已登录但不是系统管理员 |
| `500` | `internal_error` | 未预期服务端错误 |

**没有 `404` / `409`**：只读端点，不存在"对象不存在"与"状态冲突"两种语义。

**备注**：

- **未知参数一律拒绝**：把 `object_type` 拼成 `objectType`、把 `action` 拼成 `actions` 都会得到 `400`，而不是"筛选没生效但看起来成功了"。
- **成员变更记在容器上**：`object_id` 是工作组/项目/任务的 id，**不是**成员行 id。因此"这个项目上发生过什么"用一次 `object_id` 查询就能答完；成员行 id 属于实现细节。
- **覆盖范围**：审计行由各业务写路径在**自己的事务里**写入，因此"业务写成功 ⇒ 一定有记录""记录写不进去 ⇒ 业务整体回滚"。反过来说，**审计表里有的就是真正成功过的写操作**，不存在"操作失败但留下记录"。
- **哪些角色/动作不可见**：审计只记录**用户发起的**写操作。后台周期任务没有操作人，不写记录——最典型的是**提醒的发送结果**（`sent` / `failed` / `skipped`）：它们只在 `GET /api/v1/reminders` 的 `status` / `last_error` / `attempts` 里可见；**人工重试**有操作人，因此记为 `object_type = reminder`、`action = updated`，`changes = {"status":"pending","reason":"manual_retry"}`。
- **`changes` 的键是字段名，不是中文文案**：例如提醒重试的 `reason` 值 `manual_retry`、附件相关记录的 `original_name` / `extension` / `size_bytes` 等，都由服务端按业务语义写入。前端若要展示中文，需要自己维护映射（键集合随业务扩展，契约未穷举列出全部可能键）。
- **不做自动清理**：记录只增不删，没有 TTL、没有归档接口，也没有"删除记录"的端点；分页是唯一的浏览方式。数据量增长在内部小组规模下可忽略，这是刻意的取舍。
- **`request_id` 与响应头同名但含义不同**：记录里的 `request_id` 是**当初产生这条记录的那次请求**的 id，不是你这次查询的 id。排查时可以把《S02》§2.4 错误响应里的 `request_id` 与它对齐，得到"同一次请求都改了什么"。

## 不确定或契约未写清的点

| 点 | 说明 |
| --- | --- |
| `changes` 可能出现哪些键 | 代码按各业务写路径逐个传入，契约（《A09》）只给出示例与"扁平、仅新值、截断 200 字符、敏感键 `[redacted]`"的规则，**未穷举键清单**。前端不应把键集合写死。 |
| 时间范围"取整天"的边界 | `from` / `to` 都是闭区间，因此按 `[当天00:00Z, 次日00:00Z]` 查询会多带次日零点整那条记录。《S02》只说"两端都含"，未规定推荐写法。 |
| `actor_name` 空值 | SQL 直接取 `users.display_name`（该列 `NOT NULL`），因此正常情况下不会是空串；契约未描述"用户被删除（记录级联）"的情形——审计行的 `actor_id` 外键保证操作人存在。 |
| `action` 枚举的产生点覆盖度 | 13 个动作在领域常量、数据库 `CHECK` 与契约三处一致（有测试断言），但契约未逐一说明每个动作由哪些端点产生；本文只列出含义。若前端要按动作做"事件时间线"，建议以实测数据为准。 |
