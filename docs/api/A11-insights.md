# A11 数据概览、动态与周期汇报接口

> 当前实现：`backend/internal/app/insights/`。通用信封、认证和错误遵循 [S02](../S02-HTTP接口契约.md)。四个端点均只读、要求登录，不使用版本写协议或幂等键。不是 A09 全局审计的普通用户入口。

## 1. 路由与权限

| 方法 | 路径 | 返回内容 |
| --- | --- | --- |
| GET | `/api/v1/insights/overview` | 当前概览、选项、到期桶、里程碑及本周指标 |
| GET | `/api/v1/insights/reports` | 同一次读取的周期汇总、序列、成员/项目分布、成果及下期安排 |
| GET | `/api/v1/insights/activity` | 当前可访问业务对象的动态，排除基线事件 |
| GET | `/api/v1/insights/report-items` | 指定周期指标对应的任务历史明细 |

所有读取复用 A06 的领域权限收敛，并在同一个 REPEATABLE READ 只读事务查询当前事实及事件。组内仅共享项目/任务；组内历史事件还要求发生时属于该组且已共享。当前已不可见、已删除或撤回共享的任务不提供其历史贡献与详情。管理员不会因此自动在组内范围得到私有任务。项目有效进度仍按完整树汇总，不能据此反推隐藏任务。

| scope | 授权集合及筛选 |
| --- | --- |
| self（默认） | 本人作为项目成员的项目；当前/历史任务进一步按对应时点成员含本人过滤。member_id 被规范为本人，显式传其他人返回400 |
| participating | 本人参与项目中的可见叶子任务，不额外按任务成员过滤 |
| group | 必须提供 group_id；调用者为活跃组员或管理员；仅共享内容 |
| member | 必须提供 group_id/member_id；在授权组共享内容中按该成员在相应时点的参与关系过滤，不是读取此人私有计划 |
| project | 必须提供 project_id；按项目甘特图的读权限读取，普通组内访客仍只见共享任务 |

## 2. 公共查询参数

四个端点使用同一参数白名单；未知参数和重复同名参数返回400。部分字段仅在相应端点参与计算，不能认为传入一定改变全部响应。

| 参数 | 默认 / 规则 |
| --- | --- |
| scope | self；枚举见上 |
| group_id/member_id/project_id | 可选合法UUID；按scope补充必填约束；project_id进一步缩小集合 |
| timezone | 用户配置时区；无效IANA时区返回400；显式空字符串按底层时区加载使用UTC，响应回显实际查询值 |
| from/to | 各自省略时分别取当前时区本周周一/周日；自然日YYYY-MM-DD，含首尾；起日不能在未来，结束不得早于开始或超过开始+366天 |
| bucket | day（默认）或week；week按自然周切分，边界周裁剪 |
| include_archived | true/false，默认false；overview的当前任务/行动数字据此排除归档；reports保留当前仍可读的归档贡献，不受此开关排除 |
| as_of | 可选RFC3339Nano，不能晚于服务端当前时间；限制事件观察上界，但不固定当前权限和生成时最新的下期安排 |
| cursor | activity/report-items使用的非负十进制偏移字符串，初次不传；不是签名快照令牌 |
| limit | activity/report-items默认6，上限100、下限1；前端明细传10；overview/reports不分页，也不解析limit/cursor |
| metric | report-items使用，默认completed；支持created/completed/reopened/open/overdue |

界面日期含首尾，内部是报告时区的 `[开始日零点, 结束日次日零点)`；当前周期截止为周期末与as_of较早者。日历日/周/月跨度不能用固定秒数替代，避免夏令时误差。传as_of仍会按当前权限重新收敛，不是不可变历史查询。

## 3. 公共表示

### 3.1 filter、覆盖和时间

filter包含scope、group_id、member_id、project_id、timezone、from、to、bucket、include_archived，空字符串字段保留。as_of与observed_until为UTC时间字符串。coverage为：

```json
{"coverage_start":"2026-11-04T06:27:00Z","complete":false,"gaps":["记录启用前的历史不可还原"]}
```

complete按周期开始是否位于覆盖起点之后判定；不表示所有对象权限永远不变。gaps为空时为[]。迁移前历史不靠updated_at或旧审计猜测；未知/未来存量点为null，不能当0。

### 3.2 TaskState

| 字段 | 类型 / 意义 |
| --- | --- |
| id/project_id/group_id | UUID，任务及上下文 |
| name/project_name | 名称；事件快照可为当时名称，链接打开当前对象 |
| status/progress | 有效状态字符串和0–100整数进度，各自有独立业务含义 |
| start_date/end_date | 自然日YYYY-MM-DD |
| has_children/deleted | 该状态时点的结构与删除资格；父任务不计入叶子指标 |
| is_shared/visibility/archived | 该状态时点的共享/归档事实 |
| is_milestone/priority | 布尔里程碑及0–3优先级 |
| members | 数组，元素为user_id/display_name/username；响应不声明role |
| can_edit | overview当前任务依据当前权限计算；历史快照默认false，不能据此判断当前写资格，查看/写入仍独立鉴权 |

### 3.3 Metrics与计数

metrics为created/completed/reopened/open/overdue五个整数。周期新增、完成、重开各按任务id去重；完成只认非completed→completed的有效叶子变化，重开为completed→未开始/进行中。创建基线不算新建；父级自动汇总不算叶子完成。open为观测期末未完成存量，overdue为该时点截止日已过的子集。

同一任务可在多成员报表各计一次，组级仅计一次；事件发生时成员决定成果归属，期末成员决定期末存量。操作人不是自动成果承担人。未知覆盖下返回的计数仅为已记录部分，界面必须标识不完整；未覆盖周期不能据0得出无工作的结论。

## 4. overview 响应

`data`包含：

| 字段 | 内容 |
| --- | --- |
| filter/as_of/coverage | 本次范围、时间及覆盖 |
| tasks | 当前可见叶子TaskState数组；默认排除归档 |
| status | not_started/in_progress/completed/cancelled四状态计数 |
| open/overdue/due_soon | 未完成、逾期、今天至今天+7天未完成数量 |
| due | 14项序列，date为今天至+13天；**created字段在此表示该日未完成到期数量**，不是历史新建；completed为0、open为null |
| milestones | 未完成且截止不晚于今天+30天的里程碑，包含已逾期；前端截取时标明总数 |
| period | 按from/to（默认本周）计算的Metrics |
| member_options | 可用成员选项数组；任务/授权历史投影的成员，不是全组无任务成员目录 |
| project_options | 项目id→名称映射，来自当前授权任务上下文；不是包含零任务项目的完整项目目录 |

当前任务构成与历史周期指标不一定同一个数量口径。选项与动态可涉及当前仍可读的归档项目，不能用下拉选项证明编辑权。

## 5. reports 响应

`data`包含filter/as_of/observed_until/coverage、metrics、series、members、projects、achievements、attention、next、previous、policy。

- series：每项date（桶开始）、until（桶最后日）、created/completed（桶内去重数量）、open（桶末存量或null）。跨日重开再完成可能在不同桶各计一次，桶总和不保证等于周期去重完成数。
- members/projects：每项id/name/completed/open；按未完成降序及id稳定排序。成员多人参与可重复，项目按任务归属。未分配成员使用`unassigned`标识，不是用户UUID。
- achievements：代表成果候选TaskState；attention：期末逾期候选；前端选择/截取不改变metrics。
- next：用生成时当前计划筛选下一周期的未完成任务，明确标为最新安排，不是历史承诺基线。
- previous：仅当当前周期已结束且前一对应周期覆盖完整时为Metrics，否则null；无按期完成率、工时或效率推论。
- policy：当前授权/归档/多人计数说明。原始events不随reports公开输出，核查使用report-items。

示例：`GET /api/v1/insights/reports?scope=group&group_id=<UUID>&from=2026-11-02&to=2026-11-08&timezone=Asia%2FShanghai&bucket=day`。

## 6. activity 与 report-items

activity的data为items、next_cursor、as_of、coverage。items每项为id、occurred_at、actor_id（可null）、actor_name、before（可null）、after（TaskState）、kind。kind包括created/completed/reopened/cancelled/rescheduled/members/sharing/structure/restored/status/updated；排除基线。原私有或别组before状态会裁去，不公开原始审计、邮箱、密码或提醒发送事实。当前不能匹配授权叶子范围的事件不提供，这不是全量删除/结构审计。

report-items的data为items（TaskState数组）、total、next_cursor、as_of、policy；created/completed/reopened为周期事件去重，open/overdue为期末回放状态。明细跳转任务详情后看到的是当前状态，不能将两者混写。

next_cursor为下一页偏移字符串，无下一页为""；不存在S02通用pagination信封。建议续页携带初次as_of，但当前权限变化仍可使集合变化；客户端应提示并整份重读，不把cursor当数据库快照句柄。

## 7. 错误与兼容边界

401会话无效；400 invalid_request用于未知/重复参数、非法UUID/时区/枚举/日期区间/游标/条数/未来as_of等，fields指出字段。这里非法日期范围实际返回invalid_request，不套用A06的invalid_date_range。不可见组/项目按A06返回404 group_not_found/project_not_found；授权范围为空为200空集合。429及未预期500沿用S02。

当前实现按请求用户实时授权，单次聚合一致，但不保存不可变报告。历史资料在当前权限变化后可能消失；自由文本草稿只在前端会话中，不由这些GET接口保存或发送。
