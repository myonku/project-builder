# A08 提醒接口详解

> 前后端共用。业务规则见 [S01 §5.7](../S01-需求文档.md#57-邮件提醒)，通用协议见 [S02](../S02-HTTP接口契约.md)。本页描述当前已交付的提醒计划及发送历史契约。

## 接口清单与作用域

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/api/v1/reminders` | 提醒列表，分页、筛选、排序 |
| GET | `/api/v1/reminders/{reminder_id}` | 提醒详情与发送尝试证据 |
| POST | `/api/v1/reminders/{reminder_id}/retry` | 把仍有效的明确失败计划重新排队 |

三个端点均需登录，未登录返回 `401 authentication_required`。普通用户仅可读取/重试发给自己的记录；系统管理员可访问全部。非本人且非管理员的详情和重试返回 `404 reminder_not_found`，与不存在一致，不泄漏记录存在性。计划、事件、尝试的内部维护不新增公共写接口。

## 提醒对象

列表的 `data[]`、详情及重试的 `data` 使用相同字段。**历史载荷按场景加载**：列表、重试响应的 `delivery_attempts` 为 null；详情有尝试时为数组，无尝试时仍为 null。

```json
{
  "data": {
    "id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e1f",
    "task_id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e20",
    "recipient_id": "01a12d87-0b3c-7c4d-8e5f-6a7b8c9d0e30",
    "recipient_email": "alice@example.test",
    "kind": "task_overdue",
    "kind_label": "任务已延期",
    "cycle": 2,
    "plan_version": 3,
    "event_id": null,
    "covered_by": null,
    "can_retry": false,
    "delivery_attempts": null,
    "scheduled_for": "2026-10-29T01:00:00Z",
    "status": "unknown",
    "attempts": 1,
    "last_error": "发送结果待核实",
    "sent_at": null,
    "created_at": "2026-10-28T03:00:00Z",
    "updated_at": "2026-10-29T01:00:05Z"
  }
}
```

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| `id / task_id / recipient_id` | string(UUID) | 计划记录、任务、用户身份 |
| `recipient_email` | string | 当前计划地址；同步可更新未发送计划，授权时捕获实际地址。历史尝试中的地址在 snapshot 中保留 |
| `kind / kind_label` | string | 类型及服务端中文文案，见下表 |
| `cycle` | number | 节点日期周期；开始和截止各自独立。事件身份以 event_id 为准，不能仅靠 cycle 判断事件是否相同 |
| `plan_version` | number | 计划同步版本，与业务对象的 CAS updated_at 区分 |
| `event_id` | string(UUID) 或 null | 事件型提醒身份；真实新事件使用新 ID |
| `covered_by` | string(UUID) 或 null | 同日抑制引用的成功尝试；不是另一条提醒的 ID，也不提供公共尝试维护端点 |
| `can_retry` | boolean | 服务端给出的重试候选资格。前端依此展示按钮；POST 仍会复核时效与去重，true 不保证随后请求必成功 |
| `delivery_attempts` | array 或 null | 仅详情按作用域加载发送尝试；列表/重试恒为 null，无历史也为 null |
| `scheduled_for` | string(UTC timestamp) | 应发时刻，可能在未来或已经过去；不是实际发送时刻 |
| `status` | string | 生命周期状态，见下表 |
| `attempts` | number | 已获得发送授权的次数；授权后崩溃也计一次，取消/替代/抑制不增加，retry 本身不增加 |
| `last_error` | string | 最近发送诊断或失效/替代/抑制/人工核实原因。普通发送成功时为空串；人工核实成功可以保留核实说明，不保证所有 sent 都为空串 |
| `sent_at` | string 或 null | SMTP 已确认接受时刻，或管理员有证据确认的实际接受时刻；未明确成功为 null |
| `created_at / updated_at` | string(UTC timestamp) | 记录建立/最后变更时间，保留亚秒精度 |

`dedupe_key`、内部 legacy 标志、guard 和运维处置参数不属于公开 DTO。

### 类型

| kind | kind_label | 规则 |
| --- | --- | --- |
| task_assigned | 新任务分配 | 成员关系从无到有；不是重复保存或恢复开关 |
| task_start_soon | 任务即将开始 | 未开始且开始日期仍在未来；提前一个工作日当地 09:00 |
| task_due_soon | 任务即将到期 | 所有有效未完成/未取消任务，包含未开始任务；提前一个工作日当地 09:00 |
| task_overdue | 任务已延期 | 自然逾期为截止次日当地 09:00；新建/改期/恢复时已逾期可立即入队 |
| task_owner_changed | 任务持有者变更 | 实际转移给该用户，普通角色保存或改期不产生事件 |

### 生命周期

| status | 对外含义 | 普通 API 可否重试 |
| --- | --- | --- |
| pending | 已有有效待发计划，未来或已到时；全局发送关闭时保持此状态 | 否 |
| sending | 发送授权已持久化，等待该尝试结算 | 否 |
| sent | 已确认 SMTP 接受，保留历史；不保证收件箱必达 | 否 |
| failed | 有明确未接受证据的发送失败 | 仅 can_retry=true 且 POST 复核通过 |
| unknown | 可能已被接受、授权超时或成功后落账失败，阻挡同范围后续发送 | 否，需管理员运维核实 |
| skipped | 迁移保留的旧跳过记录，当前全局关闭不再生成该状态 | 否，当前实现不赋予历史 skipped 重试资格 |
| cancelled | 任务/收件资格失效等导致取消 | 否 |
| superseded | 日期/有效状态变化或更新事件使旧计划被替代 | 否 |
| suppressed | 本周期/事件已被实际发送日的同类成功覆盖，之后不再补发 | 否 |

恢复资格会按规则重算节点计划，但不会擦除成功/抑制事实；历史事件不因开启开关自动复活。日期实际变化才能产生新的相应节点周期，且仍受同日去重和 unknown 隔离约束。

### 发送尝试证据

详情 `delivery_attempts[]` 的字段：

| 字段 | 类型 | 含义 |
| --- | --- | --- |
| id | string(UUID) | 本次发送授权的唯一尝试身份 |
| status | string | sending、sent、failed、unknown |
| authorized_at / expires_at | string(timestamp) | 授权及超时检测截止时间；超时不表示可换进程重发 |
| sent_at | string 或 null | 明确成功的实际接受时刻 |
| timezone | string | 原发送时区；迁移记录可能是当前时区投影，snapshot 标记来源 |
| snapshot | object | 不透明证据 JSON。正常记录含 `To / ToName / Subject / Body`、`task_version / project_version`；迁移记录可能只含 legacy、email、original_status、original_error、timezone_source 等，不能假设历史邮件全文可还原 |
| error | string | 该尝试诊断；人工核实后仍可能保留原始错误，最终结果以 status/sent_at 为准 |

快照原始邮件键保留大小写，不转换为可写请求对象。正常同步不改写已授权的内容/地址；管理员 resolution 及其操作者/依据保存在运维证据中，通过 B03 的维护工具导出，不新增公共修改入口。尝试按 authorized_at、id 排序。

## 1. GET /reminders

允许的查询参数仅以下五个，未知参数返回 `400 invalid_request`：

| 参数 | 默认/约束 | 说明 |
| --- | --- | --- |
| kind | 可省略 | 类型等值筛选；格式合法但不在枚举中的值返回空列表，不返回 400 |
| task_id | 可省略，调用方应传合法 UUID | 限定任务，仍受收件人/管理员作用域约束 |
| page | 1，至少 1 | 页码 |
| page_size | 20，1..100 | 每页条数 |
| sort | -scheduled_for | 逗号分隔，允许 scheduled_for、created_at、updated_at、kind、status、attempts，前缀 - 降序 |

服务端始终追加 id 排序键。默认是计划时间倒序，未来较远的计划也会排在前面，不应解释为“最近要发的优先”。页码/大小越界、未知或重复排序字段返回 400。成功为 `200`，信封是 `data: []` 和通用 pagination；无数据返回空数组。total 与列表使用同一权限过滤。

**没有 status 查询参数**，传入会 400。前端“状态（当前页）”只过滤本页，不改变服务端 total；需要跨页归拢可使用允许的 status 排序。

## 2. GET /reminders/{reminder_id}

无请求体；返回 `200` 提醒对象并加载该记录的发送尝试。不存在或他人的记录返回 404，未登录返回 401。无 ETag/条件 GET；updated_at 仅表示记录变化，尝试信息可通过刷新详情获取。

## 3. POST /reminders/{reminder_id}/retry

请求体可省略或为 `{}`；当前处理器不读取请求体，不将其用于计划维护。先执行既有作用域检查，再在事务内重新同步、校验状态、日期周期、当前资格、guard 及实际发送日成功事实。

- 仅当前、非旧迁移证据、明确未接受的 failed 计划可能成功。其他状态、失效或过时计划、同日已有成功、存在 sending/unknown 尝试均返回 `409 conflict`（fields.reminder_id 给出原因）。即使 GET 的 can_retry=true，也须处理 409。
- 成功返回 `200`：status=pending，scheduled_for 为当前时刻，last_error 清为空串，sent_at 仍为 null，can_retry=false，delivery_attempts=null，attempts 不变。实际发送由后续派发完成，客户端刷新详情查看历史与结果。
- 重试成功与审计同事务提交：object_type=reminder、action=updated、object_id 为提醒 ID，changes 包含 status=pending、reason=manual_retry，actor_id 为操作人。拒绝重试不写审计、不增加尝试次数。

## 发送与查询边界

业务事务中创建/同步计划，后台仅派发已到时计划；普通名称/描述编辑不改变既定时间。改期后的旧未授权计划不可继续发送，已授权尝试按原快照完成；实际接受与数据库提交不具备跨系统原子性。

按“任务 + 用户 + 类型 + 当前时区实际发送日”判重，同时约束同节点周期成功次数。已到时的新周期同日命中时保存 suppressed；未来另一天计划保留。旧邮件不撤回，未知结果不占成功事实但会保持独占阻挡，不能靠改期或新事件绕过。

后台结果不伪造 actor 写入普通操作记录，查询结果与快照看提醒接口；人工 retry 有操作人，因此写入 audit。管理员未知核实的依据保存在尝试 resolution，见 [B03](../../backend/docs/B03-配置与运行.md)；密码重置邮件链路独立。

## 现有输入边界

`task_id` 查询值和 `reminder_id` 路径值尚无前置 UUID 校验；非 UUID 可能在数据库层以 500 结束，不承诺 400。调用方应只传合法 UUID。此行为不因本轮生命周期更新而改变。
