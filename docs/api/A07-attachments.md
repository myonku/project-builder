# A07 附件接口详解

> 归属：**前后端共用**（`docs/api/`）。通用约定（基础路径 `/api/v1`、成功/错误信封、认证、分页、并发与幂等、限流、未知字段拒绝）见《S02-HTTP接口契约》§2，本文不重复。
> 契约正文（约定性叙述）见《A07》；本文补齐每个接口的**请求与响应数据结构**。

> **2026-10-21 W3 已落地**：附件字节支持 `local`（默认）与 **MongoDB GridFS**
> （`storage.backend=gridfs`），**HTTP 契约不变**：`stored_key` 仍是实现细节、下载仍按附件 id、
> 彻底删除仍“先删对象再删行”。旧本地文件由 `cmd/storage-migrate` copy/verify/rollback，
> 账本在 `attachments.storage_backend`；Mongo 健康检查与备份顺序见
> [P01-Worklane-规划-2026-10-20.md](../P01-Worklane-规划-2026-10-20.md) §3。
> **W4 起头像也使用同一 `storage.Store` 抽象**，但走独立的 `avatars` bucket（local 模式为同一根目录下的
> `avatars/` 前缀）；附件与头像的 key/MIME/大小/权限互不影响。

## 接口清单

| # | 方法 | 路径 | 权限 | 说明 |
| --- | --- | --- | --- | --- |
| 1 | `POST` | `/api/v1/projects/{project_id}/attachments` | 对项目有写权限且项目**未归档** | 上传项目附件（`multipart/form-data`） |
| 2 | `POST` | `/api/v1/projects/{project_id}/attachments/query` | 对项目至少有读权限 | 项目附件列表（分页、排序、删除视角） |
| 3 | `POST` | `/api/v1/tasks/{task_id}/attachments` | 对任务有写权限（项目归档时任务只读） | 上传任务附件（`multipart/form-data`） |
| 4 | `POST` | `/api/v1/tasks/{task_id}/attachments/query` | 对任务至少有读权限 | 任务附件列表 |
| 5 | `GET` | `/api/v1/attachments/{attachment_id}/download` | 对所在容器至少有读权限 | 下载字节流；**每次请求重新校验权限** |
| 6 | `DELETE` | `/api/v1/attachments/{attachment_id}` | 对所在容器有写权限且容器可写 | 逻辑删除（字节仍在磁盘上） |
| 7 | `POST` | `/api/v1/attachments/{attachment_id}/purge` | 上传者本人、容器持有者或系统管理员 | 彻底删除（仅限**已逻辑删除**的附件） |

全部 7 个端点都要求登录（会话 Cookie，见《S02》§2.2）；未登录返回 `401 authentication_required`。

**为什么上传/列表挂在容器下、而下载/删除按附件 id 定位**：附件**没有独立权限**，完全继承所在项目/任务。前者用路径表达"归属哪个容器"，后者已经能从行数据反查出容器，因此用附件 id 更直接。

## 附件对象

本文所有返回附件的端点（1、2、3、4、6 的 `data`，以及 2、4 的 `data[]` 元素）结构一致：

```json
{
  "data": {
    "id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e1f",
    "project_id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e20",
    "task_id": null,
    "uploader_id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e30",
    "original_name": "周报 2026-10.txt",
    "extension": ".txt",
    "size_bytes": 26,
    "mime_type": "text/plain",
    "deleted_at": null,
    "created_at": "2026-10-12T02:11:07.918273Z",
    "updated_at": "2026-10-12T02:11:07.918273Z"
  }
}
```

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string(UUID) | 附件 id；下载、逻辑删除、彻底删除都用它 |
| `project_id` | string(UUID) \| null | 项目附件时非空；与 `task_id` **恰有一个非空** |
| `task_id` | string(UUID) \| null | 任务附件时非空 |
| `uploader_id` | string(UUID) | 上传者；配额按此字段统计，逻辑删除后的可见性也依赖它 |
| `original_name` | string | 原始文件名，**仅用于展示**。服务端已剥离路径（`C:\Users\x\a.txt` → `a.txt`）与控制字符，按 255 个码点截断（截断时保留扩展名）；结果为空或为 `.`/`/` 时替换为 `attachment` |
| `extension` | string | 小写扩展名（**含点号**，如 `.txt`）；无扩展名时为空串。点文件（`.pdf` 整体作为文件名）视为无扩展名 |
| `size_bytes` | number | 字节数，与上传内容一致 |
| `mime_type` | string | 上传时记录的 MIME（小写、去空格、超 160 字符截断）。缺失或为 `application/octet-stream` 时按扩展名兜底，再兜底 `application/octet-stream` |
| `deleted_at` | string \| null | 非空表示已逻辑删除（ISO 8601 UTC，可能带亚秒） |
| `created_at` / `updated_at` | string | 上传时间 / 最后变更时间 |

刻意**不返回**：

- `stored_key`：存储布局是实现细节，客户端一律用 `id` 下载（存储标识由服务端生成，规则 `attachments/<uuid 前两位>/<uuid>`，**绝不使用用户提供的文件名**）。
- `download_url`：URL 由客户端按 `GET /api/v1/attachments/{attachment_id}/download` 自行拼接，避免服务端拼出与部署前缀不符的地址。

## 1. `POST /api/v1/projects/{project_id}/attachments`

**用途**：把一个文件作为附件挂到指定项目上。
**权限**：登录用户，且对项目有**写权限**、项目**未归档**。无权/不可见时按"不可见即不存在"处理（见下）。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `project_id` | string(UUID) | 目标项目 |

**查询参数**：无。

**请求体**：`multipart/form-data`，**单文件**，无其它表单字段：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `file` | file part | 是 | 非空、不超过 `storage.max_file_size`、扩展名符合 `storage.allowed_extensions` | 文件内容；必须带 `filename`（扩展名判定与展示名都取自它）。详细口径见"multipart 细节" |

**响应 `201`**：附件对象（见上），字段为完整结构。

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | 请求不是合法的 multipart（例如用 `application/json` 发 `{"file":"x"}`）；`fields.file` 有说明 |
| `401` | `authentication_required` | 未登录或会话失效 |
| `403` | `permission_denied` | 项目可见但对项目无写权限；项目已归档（`fields` 为空） |
| `404` | `project_not_found` | 项目不存在或对当前用户不可见（`fields.project_id`） |
| `409` | `conflict` | 该账号附件数已达 `storage.max_files_per_user`（`fields.file` 说明上限与"先彻底删除"） |
| `413` | `file_too_large` | 文件超过 `storage.max_file_size`（`fields.file` 给出字节上限） |
| `415` | `unsupported_media_type` | 扩展名不在 `storage.allowed_extensions` 白名单内；白名单启用且文件**没有扩展名**时同样 `415`（`fields.file`） |
| `422` | `validation_error` | 缺少 `file` 字段、空文件（0 字节）（`fields.file`） |
| `500` | `internal_error` | 写对象或写行失败（例如对象存储不可用） |

**备注**：

- **处理顺序**（顺序决定了你看到哪个错误）：容器存在性与可见性（`404`）→ 写权限（`403`）→ 大小（`413`）→ 扩展名（`415`）→ 配额（`409`）→ 写对象 → 写行 + 写审计。
- **副作用**：上传成功会在**同一个数据库事务**里写一条操作记录（`object_type = attachment`、`action = created`、`object_id` = 附件 id），因此"业务成功 ⇒ 审计一定存在"（见《A09》）。审计只记元数据（`original_name` / `extension` / `size_bytes` / `project_id` / `task_id`），**绝不记文件内容**。
- **幂等**：不幂等。每成功一次就产生一个新附件、占用一份配额。重试请先修正错误原因，不要盲重试。
- **"不可见"与"无权"必须区分**：项目对当前用户不可见时返回 `404`（不泄漏项目是否存在），可见但无写权限时返回 `403`。

## 2. `POST /api/v1/projects/{project_id}/attachments/query`

**用途**：分页列出某个项目的附件，可选择把已逻辑删除的附件纳入。
**权限**：登录用户且对项目至少有读权限；不可读时与"项目不存在"同码。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `project_id` | string(UUID) | 目标项目 |

**查询参数**：无（筛选与分页都在 JSON 请求体里）。

**请求体**（`application/json`；**必须发一个 JSON 对象**，空请求体返回 `400 invalid_request`，`fields.body`）：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `include_deleted` | boolean | 否（默认 `false`） | 与 `deleted_only` 不能同时为 `true` | 为真时把已逻辑删除的附件纳入候选集（具体哪些行可见仍受可见性限制） |
| `deleted_only` | boolean | 否（默认 `false`） | 与 `include_deleted` 不能同时为 `true` | 为真时**只**返回已逻辑删除的附件（"回收站"视角） |
| `page` | number | 否（默认 `1`） | `>= 1`，越界 `400` | 页码 |
| `page_size` | number | 否（默认 `20`） | `1..100`，越界 `400` | 每页条数 |
| `sort` | string | 否（默认 `-created_at`） | 白名单见下 | 排序表达式，逗号分隔多字段，前缀 `-` 表示降序 |

`sort` 白名单：`created_at`、`updated_at`、`original_name`、`size_bytes`、`extension`。默认按上传时间**倒序**（最近上传的排前面）。服务端始终追加 `id` 作为次级排序键，因此翻页不重不漏；非法字段、重复字段返回 `400 invalid_request`（`fields.sort`，说明可用字段）。

请求体示例：

```json
{
  "include_deleted": true,
  "page": 1,
  "page_size": 20,
  "sort": "-created_at"
}
```

**响应 `200`**：

```json
{
  "data": [
    {
      "id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e1f",
      "project_id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e20",
      "task_id": null,
      "uploader_id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e30",
      "original_name": "周报 2026-10.txt",
      "extension": ".txt",
      "size_bytes": 26,
      "mime_type": "text/plain",
      "deleted_at": null,
      "created_at": "2026-10-12T02:11:07.918273Z",
      "updated_at": "2026-10-12T02:11:07.918273Z"
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
| `data` | array | 附件对象数组；无数据时是 `[]`（不是 `null`） |
| `pagination` | object | 通用分页元信息，`has_next = page * page_size < total`（见《S02》§2.3） |

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | 请求体为空/非 JSON 对象；`page` / `page_size` 越界；`sort` 含未知或重复字段 |
| `401` | `authentication_required` | 未登录或会话失效 |
| `404` | `project_not_found` | 项目不存在或对当前用户不可见（含"成员但已移除"） |
| `422` | `validation_error` | `include_deleted` 与 `deleted_only` 同时为 `true`（`fields.deleted_only`）；请求体含契约未定义字段（《S02》§2.7） |

**备注**：

- **项目列表不含任务的附件**：归属过滤在 SQL 层完成，项目视角下 `task_id` 非空的行不会出现。
- **可见性与 `total` 同源**：看不到的行既不出现在 `data` 里，也不计入 `pagination.total`。这是刻意的——否则会出现"total 说 5 条、列表只给 2 条"。
- **`include_deleted` / `deleted_only` 只放宽"是否取已删除行"**，不放宽"谁有权看到已删除行"：除上传者本人、容器持有者、系统管理员外，其他人看到的是 0 条。

## 3. `POST /api/v1/tasks/{task_id}/attachments`

**用途**：把一个文件作为附件挂到指定任务上。
**权限**：登录用户，且对任务有**写权限**。任务所在项目已归档时，任务权限本身只给读，因此上传返回 `403`。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `task_id` | string(UUID) | 目标任务 |

**查询参数**：无。

**请求体**：`multipart/form-data`，**单文件**，无其它表单字段：

| 字段 | 类型 | 必填 | 约束 | 说明 |
| --- | --- | --- | --- | --- |
| `file` | file part | 是 | 非空、不超过 `storage.max_file_size`、扩展名符合 `storage.allowed_extensions` | 文件内容；必须带 `filename`（扩展名判定与展示名都取自它）。详细口径见"multipart 细节" |

**响应 `201`**：附件对象，其中 `task_id` 非空、`project_id` 为 `null`。

**错误**：与端点 1 相同，只是把 `project_id` 相关的错误换成任务相关：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `400` | `invalid_request` | 非 multipart 请求（`fields.file`） |
| `401` | `authentication_required` | 未登录或会话失效 |
| `403` | `permission_denied` | 任务可见但无写权限（含所在项目已归档） |
| `404` | `task_not_found` | 任务不存在、已逻辑删除或不可见 |
| `409` | `conflict` | 配额已满（`fields.file`） |
| `413` | `file_too_large` | 超过 `storage.max_file_size` |
| `415` | `unsupported_media_type` | 扩展名不在白名单内 |
| `422` | `validation_error` | 缺少 `file` 字段、空文件 |
| `500` | `internal_error` | 写对象或写行失败 |

**备注**：与端点 1 完全同理——处理顺序、审计副作用（`object_type = attachment`、`action = created`）、不幂等性都一致。上传任务的附件**不会**出现在项目附件列表里（反之亦然），因为列表按 `project_id` / `task_id` 严格区分归属。

## 4. `POST /api/v1/tasks/{task_id}/attachments/query`

**用途**：分页列出某个任务的附件。
**权限**：登录用户且对任务至少有读权限。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `task_id` | string(UUID) | 目标任务 |

**查询参数**：无。

**请求体**、**响应 `200`**、**排序白名单**：与端点 2 完全一致（同一套 `include_deleted` / `deleted_only` / `page` / `page_size` / `sort`，同样的附件对象与 `pagination`）。

**错误**：与端点 2 相同，但"容器不存在或不可见"为 `404 task_not_found`。

**备注**：`include_deleted` / `deleted_only` 的语义、可见性与 `total` 同源、空列表为 `[]` 都与端点 2 一致。

## 5. `GET /api/v1/attachments/{attachment_id}/download`

**用途**：下载附件的原始字节。
**权限**：登录用户且对附件所在项目/任务至少有读权限。**每次请求都重新校验**——下载链接不携带任何凭证，权限完全由当前会话决定。附件已逻辑删除时，只有上传者本人、容器持有者与系统管理员还能下载，其他人得到 `404`。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `attachment_id` | string(UUID) | 附件 id |

**查询参数**：无。

**请求体**：无请求体。

**响应 `200`**（**不是 JSON 信封**，响应体就是文件字节）：

| 响应头 | 值 | 说明 |
| --- | --- | --- |
| `Content-Type` | 附件的 `mime_type`；为空时回 `application/octet-stream` | 让浏览器按正确类型处理 |
| `Content-Length` | `size_bytes` 的十进制字符串 | 字节数 |
| `Content-Disposition` | `attachment; filename="<ASCII 回退名>"; filename*=UTF-8''<百分号编码名>` | 见下 |
| `Cache-Control` | `private, no-store` | 权限按会话判定，不得被共享缓存留存 |

**没有响应体字段表**：响应体是字节流，不套 `data` 信封。

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `401` | `authentication_required` | 未登录或会话失效 |
| `404` | `attachment_not_found` | 附件不存在；所在容器不可见；附件已逻辑删除且调用者既不是上传者也不是持有者/管理员 |
| `500` | `internal_error` | **"有行无对象"**：数据库里有附件行，但对象存储里已找不到对应字节（或存储读取失败）。这是服务端数据一致性问题，不是"你请求错了"，因此**刻意不返回 `404`** |

**备注**：

- **逻辑删除不会让下载变成 404 或 410**：对有权者仍返回 `200` 与完整字节。逻辑删除改变的是**可见性范围**（收窄到上传者/持有者/管理员）与配额口径，不是"内容不可读"。
- 响应头一旦发出就无法改状态码，因此**流式传输中途失败不会改变已返回的 `200`**：客户端应校验 `Content-Length` 与收到的字节数是否一致。

## 6. `DELETE /api/v1/attachments/{attachment_id}`

**用途**：逻辑删除附件——置 `deleted_at`，字节**仍在磁盘上**。
**权限**：登录用户，且对附件所在容器有写权限、容器可写（归档项目下的附件只读）。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `attachment_id` | string(UUID) | 附件 id |

**查询参数**：无。

**请求体**：无请求体。

**响应 `200`**：附件对象，其中 `deleted_at` 非空、`updated_at` 为本次变更时间。

```json
{
  "data": {
    "id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e1f",
    "project_id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e20",
    "task_id": null,
    "uploader_id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e30",
    "original_name": "周报 2026-10.txt",
    "extension": ".txt",
    "size_bytes": 26,
    "mime_type": "text/plain",
    "deleted_at": "2026-10-12T02:20:00Z",
    "created_at": "2026-10-12T02:11:07.918273Z",
    "updated_at": "2026-10-12T02:20:00Z"
  }
}
```

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `401` | `authentication_required` | 未登录或会话失效 |
| `403` | `permission_denied` | 附件可见但无写权限，或容器只读（归档项目） |
| `404` | `attachment_not_found` | 附件不存在，或所在容器不可见 |
| `409` | `conflict` | 该附件**已被逻辑删除**（`fields.attachment_id`）；重复删除不会报成成功 |

**备注**：

- **不幂等**：第二次逻辑删除返回 `409`（而不是 `200`）。写语句带 `deleted_at IS NULL` 条件，0 行更新后再补一次纯存在性查询，目的是区分"不存在"（`404`）与"已经删过了"（`409`），避免把一次无变化的请求报成成功。
- **审计**：成功时写一条 `object_type = attachment`、`action = deleted` 的记录（与状态变更同事务）。
- **副作用**：不释放配额（字节仍在磁盘，`storage.max_files_per_user` 继续计入这一份）。

## 7. `POST /api/v1/attachments/{attachment_id}/purge`

**用途**：彻底删除——先删对象存储里的字节，再删数据库行（不可逆）。
**权限**：上传者本人、容器持有者（`PermManage`）或系统管理员，即"能看到已逻辑删除附件的人"。其他人得到 `404`。

**路径参数**：

| 参数 | 类型 | 说明 |
| --- | --- | --- |
| `attachment_id` | string(UUID) | 附件 id |

**查询参数**：无。

**请求体**：无请求体（服务端不读取请求体，发 `{}` 或不发都可以）。

**响应 `204`**：无响应体（不是 `{"data": ...}`）。

**错误**：

| 状态码 | `error.code` | 触发条件 |
| --- | --- | --- |
| `401` | `authentication_required` | 未登录或会话失效 |
| `404` | `attachment_not_found` | 附件不存在；容器不可见；或已逻辑删除但调用者不是上传者/持有者/管理员（"别人的回收站"按不存在处理） |
| `409` | `conflict` | 附件**尚未逻辑删除**（`fields.attachment_id` 提示先逻辑删除）。强制两步走，避免误操作不可逆地销毁文件 |
| `500` | `internal_error` | 删对象或删行失败 |

**备注**：

- **两步语义**：`DELETE`（逻辑删除，返回 `200` + 附件对象）→ `POST .../purge`（彻底删除，返回 `204`）。未逻辑删除时 `purge` 返回 `409`，**且不会留下审计记录**（被拒的请求不产生副作用）。
- **顺序的理由**（对外可观测的后果）：先删字节再删行。若中途失败，会留下"行还在、对象没了"，此时再调一次 `purge` 即可成功（删除不存在的对象是幂等的）；反过来会留下"行没了、对象还在"——那堆字节再没有任何记录指向它，无人能发现、无人能清理。因此下载这类附件会得到 `500`（见端点 5），而不是静默的 `404`。
- **配额回收点**：彻底删除成功后，该账号的附件计数减少一份（逻辑删除**不**减少）。
- **审计**：成功时写一条 `action = purged` 的记录，`changes` 含 `original_name` / `extension` / `size_bytes` / `project_id` / `task_id`（谁在什么时候彻底删掉了什么）；不记文件内容。

## multipart 细节（端点 1、3 共用）

**表单字段名**：`file`，且请求中**只处理这一个字段**。字段名不匹配（例如 `upload`）等价于"缺少 `file` 字段"，返回 `422 validation_error`，`fields.file = "缺少 file 字段（multipart/form-data）"`。

请求构造示例：

```http
POST /api/v1/projects/{project_id}/attachments HTTP/1.1
Cookie: <会话 Cookie>
Content-Type: multipart/form-data; boundary=----X

------X
Content-Disposition: form-data; name="file"; filename="周报 2026-10.txt"
Content-Type: text/plain

<文件字节>
------X--
```

| 项 | 取值 | 来源 |
| --- | --- | --- |
| 单文件大小上限 | 默认 10 MiB（10485760 字节） | 配置项 `storage.max_file_size`（TOML 键；环境变量形式为 `STORAGE_MAX_FILE_SIZE`）。超限 `413 file_too_large` |
| 请求体上限 | 文件上限 + 1 MiB（multipart 分帧预留） | 服务端行为，不对外配置。`http.MaxBytesReader` 强制执行，**不依赖客户端提供的 `Content-Length`**；因此略超文件上限的请求也会得到 `413` |
| 扩展名白名单 | 默认**不限制** | 配置项 `storage.allowed_extensions`（环境变量 `STORAGE_ALLOWED_EXTENSIONS`）。**空数组或只含 `*` 表示不限制**；一旦配置了具体值，白名单外的扩展名返回 `415 unsupported_media_type`，未配白名单时任何扩展名（含无扩展名）都放行 |
| 账号份数上限 | 默认 1000 | 配置项 `storage.max_files_per_user`（环境变量 `STORAGE_MAX_FILES_PER_USER`）。达到上限后上传返回 `409 conflict` |
| 单次上传文件数 | **1** | 不支持多文件一次上传；也**不支持**断点续传 |
| 大小写匹配 | 扩展名比较**不区分大小写**（`FILE.PDF` 与白名单里的 `.pdf` 等价） | 展示用 `extension` 一律为小写 |

**配额口径**：按**上传者**统计该账号关联的附件数，**含已逻辑删除的**。原因是它们仍占磁盘，彻底删除才是回收点；若不计数，用户可以靠"上传 + 逻辑删除"无限占用存储。因此"配额满了"的正确操作是**先逻辑删除、再彻底删除**，而不是逻辑删除。

**上传错误码与状态码的对应**（《A07》 已实测，这里是补齐触发细节）：

| 情形 | 状态码 | `error.code` | `fields` |
| --- | --- | --- | --- |
| 请求体超过文件上限 + 分帧预留 | `413` | `file_too_large` | `file`：给出字节上限 |
| 文件超过 `storage.max_file_size` | `413` | `file_too_large` | `file` |
| 扩展名不在白名单内 / 白名单启用但无扩展名 | `415` | `unsupported_media_type` | `file`：给出不支持的扩展名 |
| 空文件（0 字节） | `422` | `validation_error` | `file`："文件内容为空" |
| 缺少 `file` 字段 | `422` | `validation_error` | `file` |
| 上传内容不完整（`io.ErrUnexpectedEOF`） | `400` | `invalid_request` | `file`："上传内容不完整" |
| 非 multipart 请求 / multipart 无法解析 | `400` | `invalid_request` | `file`："无法解析 multipart 请求" |
| 容器不存在或不可见 | `404` | `project_not_found` / `task_not_found` | 对应 id 字段 |
| 容器可见但无写权限（归档项目） | `403` | `permission_denied` | 空 |
| 配额已满 | `409` | `conflict` | `file`：上限与"先彻底删除" |

**文件名与展示**：`original_name` 保留用户原始文件名（剥离路径与控制字符后），中文、空格都能正常保存；真正落到磁盘的标识由服务端生成，与文件名无关，因此恶意文件名无法影响存储布局。

## 已知未实现（不要按它们设计前端）

| 能力 | 状态 |
| --- | --- |
| 多文件一次上传 | 不支持（每次一个 `file`） |
| 断点续传 / Range 下载 | 不支持 |
| 回收站界面 | 服务端只提供 `deleted_only` / `include_deleted` 两个视角参数，无独立资源 |
| 过期自动清理 | 不做；回收磁盘的唯一途径是端点 7 的彻底删除 |

## 不确定或契约未写清的点

| 点 | 说明 |
| --- | --- |
| 路径参数不是合法 UUID 时的错误码 | 代码里附件域的路径参数（`project_id` / `task_id` / `attachment_id`）**没有**像 projects 域那样做 `idgen.IsUUID` 前置校验，非法 UUID 会由 PostgreSQL 报 `22P02`，兜底为 `500 internal_error`。契约（《A07》）未规定这一情形。**建议前端恒传 UUID，不要依赖某个具体错误码**；若要把"非法 UUID → `400 invalid_request`"写进契约，属于契约新增项。 |
| 下载响应头在流式传输中途失败时的行为 | 代码无法在响应头已发出后改状态码，只记录日志。契约未规定客户端应如何识别这种截断（实践上只能靠 `Content-Length` 校验）。 |
| 上传请求体上限的精确取值 | 服务端为 `storage.max_file_size + 1 MiB`，属于实现细节，契约只承诺"超过 `storage.max_file_size` 返回 `413`"。 |
