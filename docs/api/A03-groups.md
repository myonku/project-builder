# A03 Groups 接口详解

> 归属：**前后端共用**（`docs/api/`）。通用约定（基础路径 `/api/v1`、成功/错误信封、认证、分页、并发与幂等、限流、未知字段拒绝）见《S02-HTTP接口契约》§2，本文不重复。
> 契约正文（约定性叙述）见《A03》；本文补齐每个接口的**请求与响应数据结构**。

> **2026-10-20 规划（未生效）**：工作组成员与加入申请对象将增加 `avatar_updated_at`（仅用于前端拼头像 URL）；
> 不新增头像相关的工作组端点。见 [P01](../P01-Worklane-规划-2026-10-20.md) §4。

## 接口清单

| # | 方法 | 路径 | 权限 | 说明 |
| --- | --- | --- | --- | --- |
| 1 | `POST` | `/api/v1/groups` | 登录用户 | 创建工作组；创建者自动成为所有者与组管理员 |
| 2 | `POST` | `/api/v1/groups/query` | 登录用户 | 查询我可见的工作组（分页） |
| 3 | `GET` | `/api/v1/groups/{group_id}` | 所有者 / 活跃成员 / 系统管理员 | 工作组详情 |
| 4 | `PATCH` | `/api/v1/groups/{group_id}` | 所有者 / 组管理员 / 系统管理员（**转移所有权仅所有者或系统管理员**） | 修改工作组；携带 `owner_id` 表示转移所有权 |
| 5 | `POST` | `/api/v1/groups/{group_id}/members/query` | 所有者 / 活跃成员 / 系统管理员 | 成员列表（分页） |
| 6 | `POST` | `/api/v1/groups/{group_id}/members` | 所有者 / 组管理员 / 系统管理员 | 直接添加成员（不经申请） |
| 7 | `PATCH` | `/api/v1/groups/{group_id}/members/{user_id}` | 所有者 / 组管理员 / 系统管理员 | 设置成员角色或状态（提升 / 降级 / 移除 / 恢复） |
| 8 | `DELETE` | `/api/v1/groups/{group_id}/members/{user_id}` | 所有者 / 组管理员 / 系统管理员 | 移除成员（软移除，个人数据保留） |
| 9 | `POST` | `/api/v1/groups/{group_id}/join-requests` | 登录用户 | 当前用户申请加入工作组 |
| 10 | `POST` | `/api/v1/groups/{group_id}/join-requests/query` | 所有者 / 组管理员 / 系统管理员 | 加入申请列表（分页） |
| 11 | `POST` | `/api/v1/groups/{group_id}/join-requests/{user_id}/review` | 所有者 / 组管理员 / 系统管理员 | 审核加入申请 |

**本域特有的两条口径（先读这两条，否则会误判状态码）**：

- 全部 11 个端点都要求登录（会话 Cookie），未登录一律 `401 authentication_required`。
- **"无权"不总是 404**：
  - 详情、成员列表、成员增/改/删、加入申请查询与审核：非成员或权限不足一律 `404 group_not_found`，不向任意登录用户泄漏"该组存在"（《A03》）。
  - `PATCH /api/v1/groups/{group_id}` 是**例外**：对象存在但无权限返回 `403 permission_denied`，包括普通成员、组管理员与非成员尝试转移所有权。
- 各接口的"错误"表只列本域特有情形；`429 rate_limited`（限流，带 `Retry-After`）与 `500 internal_error` 适用于所有端点，见《S02》§2.4、§2.8。

**本域的三个响应对象（下文各接口引用同名结构）**：

工作组对象：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | UUID | 工作组 id |
| `name` | string | 名称，1–160 字符 |
| `description` | string \| null | 说明，≤2000 字符；未设置或已清空时为 `null` |
| `status` | string | `active` / `disabled` |
| `owner_id` | UUID | **所有者**（工作组自身的治理属性，不等同于成员角色） |
| `updated_at` | string | UTC ISO 8601，**带亚秒精度**，用作 `expected_updated_at` |
| `membership_role` | string \| null | 当前请求者在本组的成员角色：`member` / `manager`；非成员时为 `null`（系统管理员可见任意组） |
| `membership_status` | string \| null | 当前请求者成员关系状态：`active` / `removed`；非成员时为 `null` |

工作组成员对象：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | UUID | 成员关系行 id |
| `group_id` | UUID | 所属工作组 |
| `user_id` | UUID | 成员用户 |
| `role` | string | `member` / `manager`（**没有** `owner`：所有者由 `groups.owner_id` 表达） |
| `status` | string | `active` / `removed` |
| `joined_at` | string | 加入时刻，UTC ISO 8601 |
| `username` / `display_name` / `email` | string | 用户展示字段；**写接口（添加/修改成员）的响应里为空字符串 `""`**，见 §6、§7 备注 |

加入申请对象：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | UUID | 申请行 id |
| `group_id` / `user_id` | UUID | 目标工作组 / 申请人 |
| `status` | string | `pending` / `approved` / `rejected` |
| `reviewed_by` | UUID \| null | 审核人；未审核为 `null` |
| `reviewed_at` | string \| null | 审核时刻（UTC ISO 8601）；未审核为 `null` |
| `username` / `display_name` / `email` | string | 申请人的用户展示字段（列表由服务端一次取出，客户端不必逐行再查用户） |

## 1. `POST /api/v1/groups`

**用途**：创建一个工作组，创建者在**同一个事务**内成为所有者（`owner_id`）与组管理员（`role=manager`）。
**权限**：登录用户（无需额外权限；系统管理员与普通成员行为一致）。

**路径参数**（无则写“无”）：无。

**查询参数**（无则写“无”）：无。

**请求体**：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `name` | string | 是 | 先 `trim`；1–160 字符（按 Unicode 码点）；不可为空白 | 工作组名称 |
| `description` | string | 否 | ≤2000 字符（按 Unicode 码点） | 说明；省略、`null`、空串或纯空白都存为 `null` |

**响应 `201`**：

```json
{
  "data": {
    "id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0e",
    "name": "研发组",
    "description": "负责核心系统",
    "status": "active",
    "owner_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0f",
    "updated_at": "2026-10-11T04:30:54.047739Z",
    "membership_role": "manager",
    "membership_status": "active"
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `data` | 工作组对象 | 见上文结构；`membership_role` 恒为 `manager`、`membership_status` 恒为 `active`（创建者即所有者） |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `401` | `authentication_required` | 未登录或会话失效 |
| `400` | `invalid_request` | 请求体为空、不是合法 JSON，或含多个 JSON 值（`fields.body`） |
| `422` | `validation_error` | `name` 缺失 / 为空 / 超过 160 字符（`fields.name`）；`description` 超过 2000 字符（`fields.description`）；请求体含契约未定义的字段 |

**备注**：

- **无版本字段**：创建不参与 `expected_updated_at` 协议（不存在"当前版本"可比较）。
- **不幂等**：工作组名称没有唯一约束，重复提交会产生两个工作组；需要去重请由客户端保证。
- 副作用：同一事务写审计 `created`（`object_type=group`，`changes` 至少含 `name`）——审计与业务同生共死（《S02》§2.6、§4.10）。
- `status` 由服务端固定为 `active`，请求体不接受 `status`（出现即 `422`）。

## 2. `POST /api/v1/groups/query`

**用途**：分页查询"当前用户可见的工作组"。
**权限**：登录用户。

**路径参数**（无则写“无”）：无。

**查询参数**（无则写“无”）：无——筛选、分页与排序都在请求体里（参数较多的列表统一用 `POST .../query`，见《S02》§2.5）。

**请求体**：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `status` | string | 否 | `active` / `disabled`；其它值 `400 invalid_request`（`fields.status`） | 按工作组状态筛选；省略 = 不过滤 |
| `page` | integer | 否 | ≥1，默认 `1`；越界 `400`（`fields.page`） | 页码 |
| `page_size` | integer | 否 | 1–100，默认 `20`；越界 `400`（`fields.page_size`，不做静默夹取） | 每页数量 |
| `sort` | string | 否 | 白名单：`name`、`status`、`created_at`、`updated_at`；前缀 `-` 表示降序，可逗号分隔多字段；默认 `-created_at`；未知字段或重复字段 `400`（`fields.sort`） | 排序；服务端始终追加 `id` 作为次级键 |

**响应 `200`**：

```json
{
  "data": [
    {
      "id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0e",
      "name": "研发组",
      "description": "负责核心系统",
      "status": "active",
      "owner_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0f",
      "updated_at": "2026-10-11T04:30:54.047739Z",
      "membership_role": "manager",
      "membership_status": "active"
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
| `data` | 工作组对象数组 | 空结果序列化为 `[]` 而不是 `null`（《S02》§2.3） |
| `data[].membership_role` / `membership_status` | string \| null | 当前请求者在该组的成员关系；系统管理员可看到自己**不是成员**的组，此时两者为 `null` |
| `pagination` | object | `page` / `page_size` / `total` / `has_next`；`total` 与筛选条件同源，`has_next = page * page_size < total` |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `401` | `authentication_required` | 未登录或会话失效 |
| `400` | `invalid_request` | `page < 1`、`page_size` 越界、`sort` 未知字段 / 重复字段、`status` 取值非法；请求体为空或不是合法 JSON（`fields.body`） |
| `422` | `validation_error` | 请求体含契约未定义的字段 |

**备注**：

- **可见范围（与前端的"我的工作组"一致但有细节）**：当前用户在该组有一条 `group_members` 记录即为可见——**包括 `status=removed` 的已移除成员关系**（前端需要展示"已被移除/已停用"）；系统管理员额外可见全部工作组（《A03》、§6 的管理员放行条目）。
- **列表口径与详情口径不同**：同一用户对被移除的组会出现在列表里（`membership_status` 为 `removed`），但 `GET /groups/{group_id}` 返回 `404`。
- **待审核的加入申请不会让组出现在列表里**：范围只看成员记录（或系统管理员），`pending` 的申请不构成可见性。前端要展示"我申请中的组"必须另查申请（本域没有"我的申请列表"端点）。
- 响应**不含** `created_at`，但 `sort` 支持它（可用它排序，取不到该值）。
- 无版本字段、无幂等键；只读端点不写审计。

## 3. `GET /api/v1/groups/{group_id}`

**用途**：读取单个工作组的详情，以及当前请求者在其中的成员角色。
**权限**：该组的所有者、**活跃**成员，或系统管理员；其它情况返回 `404 group_not_found`。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `group_id` | UUID | 工作组 id；不是合法 UUID 时返回 `400 invalid_request`（`fields.group_id`），不交给数据库 |

**查询参数**（无则写“无”）：无。

**请求体**（无则写“无请求体”）：无请求体。

**响应 `200`**：

```json
{
  "data": {
    "id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0e",
    "name": "研发组",
    "description": null,
    "status": "active",
    "owner_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0f",
    "updated_at": "2026-10-11T04:30:54.047739Z",
    "membership_role": "member",
    "membership_status": "active"
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `data` | 工作组对象 | 见 §接口清单 的结构定义；`membership_role` 对所有者恒为 `manager` |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `401` | `authentication_required` | 未登录或会话失效 |
| `400` | `invalid_request` | `group_id` 不是合法 UUID（`fields.group_id`） |
| `404` | `group_not_found` | 工作组不存在，或请求者既不是所有者也不是活跃成员（与"不存在"同码，避免泄漏存在性） |

**备注**：

- **停用的组对原成员仍然可见**（他们需要看到"组已停用"）；成员关系为 `removed` 之后立即不可见。
- 所有者即使成员关系异常（只可能由直接改库产生）也仍可读取自己的组。
- 本域**不校验查询字符串**：`GET /api/v1/groups/{group_id}?anything=1` 会忽略多余参数并正常返回（与 Plan Views 的"未知查询参数即 400"不同）。
- 无版本要求、无副作用（只读，不写审计）。

## 4. `PATCH /api/v1/groups/{group_id}`

**用途**：修改工作组名称、描述或状态；携带 `owner_id` 时表示**转移工作组所有权**。
**权限**：所有者、组管理员或系统管理员可修改字段；**转移所有权仅当前所有者或系统管理员**（组管理员、普通成员、非成员都不行）。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `group_id` | UUID | 工作组 id；非法 UUID 返回 `400 invalid_request`（`fields.group_id`） |

**查询参数**（无则写“无”）：无。

**请求体**：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `name` | string \| null | 否 | `trim` 后 1–160 字符 | 省略或 `null` = 不修改 |
| `description` | string \| null | 否 | ≤2000 字符 | **是否出现该键决定是否修改**：出现且为 `""`、纯空白或 `null` 都表示**清空**（存 `null`）；省略 = 不修改 |
| `status` | string | 否 | `active` / `disabled` | 启用 / 停用工作组；省略或 `null` = 不修改 |
| `owner_id` | UUID | 否 | 必须是该组的**活跃成员**，且账号未被停用；不能等于当前所有者 | **转移工作组所有权**（见下方专段） |
| `expected_updated_at` | string | **是** | UTC ISO 8601，必须等于当前 `updated_at`（原样回传，含亚秒精度） | 缺失 `422`；不匹配 `409 version_conflict` |

**响应 `200`**：

```json
{
  "data": {
    "id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0e",
    "name": "研发组（改名）",
    "description": null,
    "status": "active",
    "owner_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0f",
    "updated_at": "2026-10-11T05:02:11.318204Z",
    "membership_role": null,
    "membership_status": null
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `data` | 工作组对象 | `updated_at` 已推进；**普通更新的响应不含成员关系投影**，`membership_role` / `membership_status` 为 `null`（这两个字段以 `GET /groups/{group_id}` 为准） |
| `data.owner_id` | UUID | 转移所有权时为**新所有者**；普通更新时不变 |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `401` | `authentication_required` | 未登录或会话失效 |
| `400` | `invalid_request` | `group_id` 不是合法 UUID；请求体为空或不是合法 JSON |
| `403` | `permission_denied` | 请求者不是所有者 / 组管理员 / 系统管理员；或组管理员、普通成员、非成员尝试转移所有权 |
| `404` | `group_not_found` | `group_id` 不存在 |
| `409` | `version_conflict` | `expected_updated_at` 与当前值不一致（含"用秒级精度回传导致必然失败"的情形） |
| `409` | `conflict` | `owner_id` 等于当前所有者（`fields.owner_id`：没有变化不假装成功） |
| `422` | `validation_error` | 缺 `expected_updated_at`；`name` 为空 / 超过 160 字符；`description` 超过 2000 字符；`status` 取值非法；`owner_id` 目标不是该组活跃成员或已被停用；请求体含契约未定义的字段 |

**所有权转移（`owner_id`）**：

- **前置条件**（不满足时在写入前给出明确错误，不让数据库约束兜底）：

  | 情形 | 状态码 | `error.code` | `fields` |
  | --- | --- | --- | --- |
  | 目标用户不是该工作组成员 | `422` | `validation_error` | `owner_id` |
  | 目标是成员但状态为 `removed` | `422` | `validation_error` | `owner_id` |
  | 目标账号已被停用 | `422` | `validation_error` | `owner_id` |
  | 目标已经是当前所有者 | `409` | `conflict` | `owner_id` |
  | 请求者不是当前所有者（也不是系统管理员） | `403` | `permission_denied` | — |
  | `expected_updated_at` 不匹配 | `409` | `version_conflict` | — |

- **写入是原子的**：同一事务内做三件事——改写 `groups.owner_id`、把目标成员置为 `role=manager, status=active`、把原所有者降为 `role=member`（**保留成员身份**，即交出管理权）。三者同生共死，因此不存在"是所有者但没有管理者成员行"或"两个所有者"的中间状态。
- 原所有者之后可以被新所有者显式再设为 `manager`（那是一次独立、可审计的操作，见 §7）。
- **转移响应附带的是新所有者的成员关系投影**（`membership_role=manager`、`membership_status=active`），不是调用者的。
- **同一次请求里同时给出 `owner_id` 与 `name` / `description` / `status` 时，只执行转移，其它字段不生效**。要同时改名请拆成两次请求。

**备注**：

- **并发（CAS）**：版本条件写在 `UPDATE groups ... WHERE id = $1 AND updated_at = $2` 里，是原子比较并交换；`RowsAffected = 0` 时再补一次纯存在性查询，用于区分 `404 group_not_found` 与 `409 version_conflict`（《S02》§2.6）。
- 未提供任何可改字段（只带 `expected_updated_at`）**并不报错**：本次写入仍会推进 `updated_at`（《A02》 对用户资料的"拒绝空更新"规则在这里不适用，本域没有这条校验）。
- **停用后再启用只有系统管理员能做**：`status=disabled` 之后，所有者与组管理员的管理权限判定都会关闭（否则管理员兜底失效），只有系统管理员可以把它改回 `active`。
- 副作用：审计 `updated`（`changes` 只含本次实际提供的字段：`name` / `description` / `status`）或 `owner_transferred`（`changes` 含 `user_id` 与 `previous_owner_id`）；被拒绝的写入（`403` / `409`）不留审计记录。

## 5. `POST /api/v1/groups/{group_id}/members/query`

**用途**：分页查询工作组成员（含用户名、显示名与邮箱快照）。
**权限**：所有者、**活跃**成员或系统管理员；其它情况返回 `404 group_not_found`。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `group_id` | UUID | 工作组 id；非法 UUID 返回 `400 invalid_request` |

**查询参数**（无则写“无”）：无（走请求体）。

**请求体**：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `role` | string | 否 | `member` / `manager`；其它值 `400 invalid_request`（`fields.role`） | 按成员角色筛选；省略 = 不过滤 |
| `status` | string | 否 | `active` / `removed`；其它值 `400 invalid_request`（`fields.status`） | 按成员状态筛选；**省略 = 同时返回 `active` 与 `removed`** |
| `page` | integer | 否 | ≥1，默认 `1`；越界 `400` | 页码 |
| `page_size` | integer | 否 | 1–100，默认 `20`；越界 `400` | 每页数量 |
| `sort` | string | 否 | 白名单：`username`、`display_name`、`role`、`status`、`joined_at`；默认 `joined_at`（**升序**，最早加入在前）；未知字段 `400` | 排序 |

**响应 `200`**：

```json
{
  "data": [
    {
      "id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d11",
      "group_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0e",
      "user_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0f",
      "role": "manager",
      "status": "active",
      "joined_at": "2026-10-11T04:30:54.047739Z",
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
| `data` | 工作组成员对象数组 | 空结果为 `[]` |
| `data[].username` / `display_name` / `email` | string | 用户当前值（不是加入时的快照）；本端点一定返回，可直接用于展示 |
| `pagination` | object | 与筛选条件同源 |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `401` | `authentication_required` | 未登录或会话失效 |
| `400` | `invalid_request` | `page` / `page_size` 越界、`sort` 未知字段、`role` 或 `status` 取值非法；`group_id` 非法 UUID；请求体非法 JSON |
| `404` | `group_not_found` | 组不存在，或请求者不是所有者 / 活跃成员 / 系统管理员 |
| `422` | `validation_error` | 请求体含契约未定义的字段 |

**备注**：

- **默认不过滤状态**：省略 `status` 时 `removed` 的历史成员也会出现在列表里，前端若只要"当前成员"必须显式传 `"status": "active"`。
- 列表是**只读**的：无版本字段、无审计写入。
- 校验顺序是"分页 → 可见性 → 取值 → 排序"，因此非成员传 `page_size=500` 得到的是 `400` 而不是 `404`；要判断可见性请用合法分页参数。

## 6. `POST /api/v1/groups/{group_id}/members`

**用途**：由有管理权限的人**直接把用户加入**工作组（不经过加入申请）。
**权限**：所有者、组管理员或系统管理员；其它情况返回 `404 group_not_found`。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `group_id` | UUID | 工作组 id；非法 UUID 返回 `400 invalid_request` |

**查询参数**（无则写“无”）：无。

**请求体**：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `user_id` | UUID | 是 | 必须是**已存在且未被停用**的用户 | 被加入的用户；缺失返回 `422`（`fields.user_id`） |
| `role` | string | 否 | `member`（默认）/ `manager` | 初始成员角色；其它值 `422 validation_error`（`fields.role`） |

**响应 `201`**：

```json
{
  "data": {
    "id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d11",
    "group_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0e",
    "user_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d10",
    "role": "member",
    "status": "active",
    "joined_at": "2026-10-11T05:10:02.551900Z",
    "username": "",
    "display_name": "",
    "email": ""
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `data` | 工作组成员对象 | `status` 恒为 `active`；`joined_at` 为服务端当前时刻 |
| `data.username` / `display_name` / `email` | string | **恒为空字符串 `""`**（本响应只由成员关系的写入结果投影而来）；要展示用户信息请用 §5 的成员列表或 `GET /api/v1/users/{user_id}` |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `401` | `authentication_required` | 未登录或会话失效 |
| `400` | `invalid_request` | `group_id` 不是合法 UUID；请求体非法 JSON |
| `404` | `group_not_found` | 组不存在，或请求者不是所有者 / 组管理员 / 系统管理员（普通成员与非成员都是这一条） |
| `404` | `user_not_found` | `user_id` 对应用户不存在（`fields.user_id`） |
| `409` | `duplicate_member` | 该用户在该组**已有成员记录**（`unique(group_id, user_id)` 冲突，`fields.user_id`） |
| `422` | `validation_error` | 缺 `user_id`；`role` 取值非法；目标用户已被停用（`fields.user_id`）；请求体含契约未定义的字段 |

**备注**：

- **重复添加与"恢复被移除成员"不是同一个操作**：被软移除的成员记录仍在，再 `POST` 会命中唯一约束返回 `409 duplicate_member`；恢复要用 §7 的 `PATCH ... {"status": "active"}`。
- 目标用户必须**未被停用**（把停用账号拉进组没有意义）；但**不要求**他与请求者或组有任何既有关系。
- 成员角色只有 `member` / `manager`：本端点无法"设出所有者"，所有权转移走 §4 的 `owner_id`。
- 无 `expected_updated_at` 要求（成员关系不参与版本协议）。
- 副作用：审计 `member_added`（`changes` 含 `user_id`、`role`），与业务同事务。

## 7. `PATCH /api/v1/groups/{group_id}/members/{user_id}`

**用途**：设置某个成员的角色（提升为组管理员 / 降为普通成员）或状态（软移除 / 恢复）。
**权限**：所有者、组管理员或系统管理员；其它情况返回 `404 group_not_found`。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `group_id` | UUID | 工作组 id；非法 UUID 返回 `400 invalid_request`（`fields.group_id`） |
| `user_id` | UUID | 目标成员的用户 id；非法 UUID 返回 `400 invalid_request`（`fields.user_id`） |

**查询参数**（无则写“无”）：无。

**请求体**：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `role` | string | 否 | `manager`（提升）/ `member`（降级） | 与 `status` **至少提供一个**；同时提供时**只按 `role` 生效** |
| `status` | string | 否 | `removed`（软移除）/ `active`（恢复被移除的成员） | 见上 |

**响应 `200`**：

```json
{
  "data": {
    "id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d11",
    "group_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0e",
    "user_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d10",
    "role": "manager",
    "status": "active",
    "joined_at": "2026-10-11T05:10:02.551900Z",
    "username": "",
    "display_name": "",
    "email": ""
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `data` | 工作组成员对象 | 只反映成员关系本身；`role` 或 `status` 已按请求更新 |
| `data.username` / `display_name` / `email` | string | **恒为空字符串 `""`**（与 §6 同理） |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `401` | `authentication_required` | 未登录或会话失效 |
| `400` | `invalid_request` | 路径参数不是合法 UUID；请求体非法 JSON |
| `403` | `permission_denied` | **目标是本组所有者**而被降级或移除（`fields.user_id`）：所有者身份不容许与成员行不一致 |
| `404` | `group_not_found` | 组不存在，或请求者不是所有者 / 组管理员 / 系统管理员 |
| `404` | `user_not_found` | `user_id` 不是该工作组成员（`fields.user_id`） |
| `409` | `conflict` | 该成员已经是 `manager`（提升）或不是 `manager`（降级）——`fields.role`；该成员已被 `removed`（再移除）或已是 `active`（再恢复）——`fields.status` |
| `409` | `conflict` | 降级或移除**组内唯一的管理者**（目标不是所有者；`fields.user_id`） |
| `422` | `validation_error` | 既没给 `role` 也没给 `status`（`fields.role`）；取值非法（`fields.role` / `fields.status`）；请求体含契约未定义的字段 |

**备注**：

- **无 `expected_updated_at`**：成员关系的变更不参与版本协议，也不需要先读取工作组。
- **恢复被移除成员只有这一条路径**：`status: "active"`；`POST .../members` 会因唯一约束返回 `409 duplicate_member`。
- **"移除"是软移除**：`status` 置为 `removed`，成员行与个人数据保留；被移除者立即失去组内访问（详情与组内计划视图返回 `404`），但在自己的 `POST /groups/query` 列表里仍能看到该组。
- **所有者不可降级/不可移除**（`403`）：所有者身份是 `groups.owner_id`，成员行只是它的投影；要换人必须走 §4 的所有权转移，而不是"先降后升"。
- **"唯一管理者"判定**只统计 `role=manager AND status=active` 的行；目标是所有者时跳过该判定（由上一行的规则单独保护），因此不会出现"组没有任何可管理成员"的状态。
- 传入未定义的字段（如 `expected_updated_at`）返回 `422`，不会静默忽略。
- 副作用：审计 `member_role_changed`（提升/降级）、`member_removed`（置 `removed`）、`member_added`（恢复活跃，同"成员关系再次生效"）；与业务同事务。

## 8. `DELETE /api/v1/groups/{group_id}/members/{user_id}`

**用途**：把成员从工作组移除（**软移除**：`status` 置为 `removed`，个人数据保留）。
**权限**：所有者、组管理员或系统管理员；其它情况返回 `404 group_not_found`。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `group_id` | UUID | 工作组 id；非法 UUID 返回 `400 invalid_request` |
| `user_id` | UUID | 目标成员的用户 id；非法 UUID 返回 `400 invalid_request` |

**查询参数**（无则写“无”）：无。

**请求体**（无则写“无请求体”）：无请求体（**该端点不读取请求体**，因此任何请求体都不会被校验，也不会因未知字段返回 `422`；本端点不需要 `expected_updated_at`）。

**响应 `204`**：无响应体。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| — | — | 成功时响应体为空；成员的新状态请用 §5 成员列表或 `GET /groups/{group_id}` 读取 |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `401` | `authentication_required` | 未登录或会话失效 |
| `400` | `invalid_request` | 路径参数不是合法 UUID |
| `403` | `permission_denied` | **目标是本组所有者**（`fields.user_id`）：所有者不可被移除 |
| `404` | `group_not_found` | 组不存在，或请求者不是所有者 / 组管理员 / 系统管理员 |
| `404` | `user_not_found` | `user_id` 在该组**没有成员记录**（`fields.user_id`） |
| `409` | `conflict` | 该成员**已被移除**（`status` 已是 `removed`，`fields.status`）：重复移除不是"成功但没有变化" |

**备注**：

- 与 §7 的 `{"status": "removed"}` **是同一个操作**（同一领域动作、同一 `member_removed` 审计记录）：前者是 REST 风格的删除入口，后者是"按状态变更"的入口。已处于 `removed` 的成员再走本端点返回 `409 conflict`（不是 `404`：成员行仍在，只是状态已是 `removed`）。
- 与 §6 的"添加"配对使用：**添加 → 移除 → 再次添加会得到 `409 duplicate_member`**，恢复只能用 `PATCH ... {"status": "active"}`。
- 被移除者立即失去组内访问（`GET /groups/{group_id}`、组内计划视图均 `404`）；其既往数据（任务、项目成员关系）不受影响（需求文档 5.2）。
- 副作用：审计 `member_removed`（`changes` 含 `user_id`），与业务同事务。

## 9. `POST /api/v1/groups/{group_id}/join-requests`

**用途**：当前登录用户申请加入某个工作组（等待所有者 / 组管理员 / 系统管理员审核）。
**权限**：登录用户——**不要求**是该组成员，这正是申请的入口。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `group_id` | UUID | 目标工作组 id；非法 UUID 返回 `400 invalid_request` |

**查询参数**（无则写“无”）：无。

**请求体**（无则写“无请求体”）：无请求体（**该端点不读取请求体**：申请人取自会话，不接受任何字段，也不会因未知字段返回 `422`）。

**响应 `201`**：

```json
{
  "data": {
    "id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d12",
    "group_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0e",
    "user_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d10",
    "status": "pending",
    "reviewed_by": null,
    "reviewed_at": null,
    "username": "bob",
    "display_name": "Bob",
    "email": "bob@example.com"
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `data` | 加入申请对象 | 新建或"重新申请"时 `status` 恒为 `pending`、`reviewed_by` 与 `reviewed_at` 为 `null` |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `401` | `authentication_required` | 未登录或会话失效 |
| `400` | `invalid_request` | `group_id` 不是合法 UUID |
| `404` | `group_not_found` | 目标工作组不存在 |
| `409` | `conflict` | 工作组已停用（`fields.status`）；已是该组活跃成员（`fields.user_id`）；已存在待审核的申请（`fields.user_id`） |

**备注**：

- **生命周期**：`pending` → `approved` / `rejected`（不可回到 `pending`）。
- **重复申请是冲突，不是新建**：`unique(group_id, user_id)` 只允许一行；`pending` 时再申请返回 `409`。
- **被拒绝（或被移除）之后可以重新申请，且响应仍是 `201`**：此时是把既有行翻回 `pending`（`reviewed_by`、`reviewed_at` 被清空），不会插入第二行。
- 审核通过前不能访问组内数据：此时 `GET /groups/{group_id}` 仍返回 `404`。
- 副作用：审计 `created`（`object_type=group`，`changes` 含 `user_id`、`join_request_id`；"重新申请"记同一动作）。

## 10. `POST /api/v1/groups/{group_id}/join-requests/query`

**用途**：分页查询该工作组的加入申请（审核工作台）。
**权限**：所有者、组管理员或系统管理员；其它情况返回 `404 group_not_found`。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `group_id` | UUID | 工作组 id；非法 UUID 返回 `400 invalid_request` |

**查询参数**（无则写“无”）：无（走请求体）。

**请求体**：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `status` | string | 否 | `pending` / `approved` / `rejected`；其它值 `400 invalid_request`（`fields.status`） | 按申请状态筛选；省略 = 全部 |
| `page` | integer | 否 | ≥1，默认 `1`；越界 `400` | 页码 |
| `page_size` | integer | 否 | 1–100，默认 `20`；越界 `400` | 每页数量 |
| `sort` | string | 否 | 白名单：`status`、`created_at`、`updated_at`、`username`；默认 `created_at`（**升序**）；未知字段 `400` | 排序 |

**响应 `200`**：

```json
{
  "data": [
    {
      "id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d12",
      "group_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0e",
      "user_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d10",
      "status": "pending",
      "reviewed_by": null,
      "reviewed_at": null,
      "username": "bob",
      "display_name": "Bob",
      "email": "bob@example.com"
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
| `data` | 加入申请对象数组 | 空结果为 `[]`；申请人的用户名 / 显示名 / 邮箱一并返回，客户端不必逐行再查用户 |
| `pagination` | object | `total` 与 `status` 筛选同源 |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `401` | `authentication_required` | 未登录或会话失效 |
| `400` | `invalid_request` | `page` / `page_size` 越界、`sort` 未知字段、`status` 取值非法；`group_id` 非法 UUID；请求体非法 JSON |
| `404` | `group_not_found` | 组不存在，或请求者不是所有者 / 组管理员 / 系统管理员（普通成员看不到待审核列表） |
| `422` | `validation_error` | 请求体含契约未定义的字段 |

**备注**：

- **响应不含 `created_at` / `updated_at`**：`sort` 支持按它们排序，但当前响应结构里取不到这两个值（需要展示申请时间时请按《S02》§6 的变更规则先改契约）。
- 只读端点：无版本字段、无审计写入。
- 申请一旦被审核（`approved` / `rejected`）不会自动消失，默认（不传 `status`）仍会返回，前端通常显式传 `"status": "pending"` 作为待办列表。

## 11. `POST /api/v1/groups/{group_id}/join-requests/{user_id}/review`

**用途**：审核某用户的加入申请（通过或拒绝）。
**权限**：所有者、组管理员或系统管理员；其它情况返回 `404 group_not_found`。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `group_id` | UUID | 工作组 id；非法 UUID 返回 `400 invalid_request` |
| `user_id` | UUID | **申请人**的用户 id（不是审核人，也不是成员行 id）；非法 UUID 返回 `400 invalid_request` |

**查询参数**（无则写“无”）：无。

**请求体**：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `status` | string | 是 | `approved` / `rejected`；其它值 `422 validation_error`（`fields.status`） | 审核结果；**不能**传 `pending` |

**响应 `200`**：

```json
{
  "data": {
    "id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d12",
    "group_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0e",
    "user_id": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d10",
    "status": "approved",
    "reviewed_by": "0199f0c2-8b1e-7a3c-9d2f-5e6a7b8c9d0f",
    "reviewed_at": "2026-10-11T06:22:31.884512Z",
    "username": "bob",
    "display_name": "Bob",
    "email": "bob@example.com"
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `data` | 加入申请对象 | `status` 为本次审核结果；`reviewed_by` 为审核人、`reviewed_at` 为审核时刻（UTC ISO 8601） |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `401` | `authentication_required` | 未登录或会话失效 |
| `400` | `invalid_request` | 路径参数不是合法 UUID；请求体为空或非法 JSON |
| `404` | `group_not_found` | 组不存在，或请求者不是所有者 / 组管理员 / 系统管理员 |
| `404` | `resource_not_found` | 该用户在本组**没有加入申请**（`fields.user_id`） |
| `409` | `conflict` | 申请已处于 `approved` / `rejected`，不能重复审核（`fields.status`） |
| `422` | `validation_error` | 缺 `status`；`status` 不是 `approved` / `rejected`；请求体含契约未定义的字段 |

**备注**：

- **`approved` 的写入是原子的**：同一事务内先做 `pending → approved` 的状态迁移（迁移条件带 `status = 'pending'`，因此并发下只有一个请求能改，另一个得到 `409`），再写入/恢复成员关系 `role=member, status=active`。所以不会出现"显示已通过但访问不了组内计划"。
- **重复审核必然冲突**：第二次请求返回 `409 conflict`（早期实现此处漏捕唯一约束冲突，曾返回 `500`）。
- **对"被移除后重新申请并通过"是幂等恢复**：成员关系已存在时执行 `status` 置回 `active`（不产生重复成员行），角色保持 `member`（不会把用户恢复成 `manager`）。
- 审核结果不可回退到 `pending`：申请人对已 `rejected` 的申请要重新申请，走 §9（那会清空 `reviewed_by` / `reviewed_at`）。
- 副作用：审计 `join_request_reviewed`（`changes` 含 `user_id`、`decision`），与业务同事务。
