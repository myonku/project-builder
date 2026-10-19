# A10 注册申请接口详解

> 归属：**前后端共用**（`docs/api/`）。通用约定（基础路径 `/api/v1`、成功/错误信封、认证、分页、并发与幂等、限流、未知字段拒绝）见《S02-HTTP接口契约》§2，本文不重复。
> 公共入口是 [A01](A01-auth.md) §1 的 `POST /auth/register`；本文只描述管理员审核端。

## 接口清单

| # | 方法 | 路径 | 权限 | 说明 |
| --- | --- | --- | --- | --- |
| 1 | `POST` | `/api/v1/registration-requests/query` | 仅系统管理员 | 分页查询注册申请 |
| 2 | `POST` | `/api/v1/registration-requests/{request_id}/review` | 仅系统管理员 | 通过/拒绝申请；通过时创建启用账号 |

**权限**：两个端点都要求系统管理员（`system_role=admin`）；非管理员返回 `403 permission_denied`。
两个端点都需要会话 Cookie；未登录由会话网关直接返回 `401`。

**申请结构**（`data` / `data[]`）：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string (UUID) | 申请 id |
| `username` | string | 小写用户名 |
| `email` | string | 小写邮箱 |
| `display_name` | string | 显示名 |
| `timezone` | string | IANA 时区；省略时存 `UTC` |
| `status` | string | `pending` \| `approved` \| `rejected` |
| `reviewed_by` | string \| null | 审核管理员 id |
| `reviewed_at` | string \| null | 审核时间（UTC ISO 8601） |
| `rejection_reason` | string \| null | 拒绝原因；通过/未审核为 `null` |
| `created_at` / `updated_at` | string | 时间戳 |

**`password_hash` 永不返回**：它只存在于申请表与审核事务中。

## 1. `POST /api/v1/registration-requests/query`

**用途**：审核列表，默认按 `-created_at` 排序。
**权限**：系统管理员。

**请求体**：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `status` | string | 否 | `pending` / `approved` / `rejected`；省略返回全部状态 |
| `page` | integer | 否 | ≥1，默认 1 |
| `page_size` | integer | 否 | 1–100，默认 20 |
| `sort` | string | 否 | 白名单：`created_at`、`updated_at`、`username`、`status`；前缀 `-` 降序 |

**响应 `200`**：

```json
{
  "data": [
    {
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
  ],
  "pagination": { "page": 1, "page_size": 20, "total": 1, "has_next": false }
}
```

**错误**：`401` 未登录、`403` 非管理员、`400` 非法状态/分页/排序、`500`。

## 2. `POST /api/v1/registration-requests/{request_id}/review`

**用途**：审核一条申请。
- `approved`：在同一事务内检查身份仍可用 → 创建 `status=active`、`system_role=member` 的用户 → 把申请标记为 `approved`；
- `rejected`：只把申请标记为 `rejected`，可带拒绝原因；不创建用户。

**权限**：系统管理员。

**路径参数**：`request_id` 为申请 UUID。

**请求体**：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `status` | string | 是 | 只能是 `approved` 或 `rejected`；`pending` 非法 |
| `reason` | string | 否 | 拒绝原因，≤2000 字符；通过时忽略 |

**响应 `200`**：

```json
{
  "data": {
    "request": {
      "id": "01a1290b-0ae8-7046-9106-30f2d07815af",
      "status": "approved",
      "reviewed_by": "01a1290b-1111-7000-8000-000000000001",
      "reviewed_at": "2026-10-11T04:00:00Z",
      "rejection_reason": null
    },
    "user": {
      "id": "01a1290b-2222-7000-8000-000000000002",
      "username": "alice",
      "status": "active",
      "system_role": "member"
    }
  }
}
```

`data.request` 字段同申请结构；`data.user` 仅在 `approved` 时出现，字段同 A01 公共用户结构。

**并发与错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `401` | `authentication_required` | 未登录 |
| `403` | `permission_denied` | 非系统管理员 |
| `404` | `registration_request_not_found` | 申请不存在 |
| `409` | `conflict` | 申请已不是 `pending`（重复审核）；或通过时用户名/邮箱已被占用（`fields.username` / `fields.email`） |
| `422` | `validation_error` | `status` 非法、原因超长、请求体含未定义字段 |
| `500` | `internal_error` | 未预期服务端错误 |

**一致性**：

- 审核用 `SELECT ... FOR UPDATE` 锁住申请行，并只允许从 `pending` 迁移；
  两个管理员同时审核时只有一个成功，另一个得到 `409`。
- 通过时“检查身份 → 插入 users → 更新申请”在同一事务内完成；任一步失败全部回滚，
  不会出现“申请已通过但没有账号”或“账号已建但申请仍 pending”。
- 通过后写一条 `user/created` 审计，操作人是审核管理员；拒绝暂不写审计。
