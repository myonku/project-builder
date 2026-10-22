# Worklane — Go 服务端

Worklane（Work Items & Layered Timelines）的服务端，使用 **Go 1.26 + Gin + pgx** 实现。

应用面向内部网络，不面向公网开放。2026-10 的迁移/对象存储/头像规划见
[../docs/P01-Worklane-规划-2026-10-20.md](../docs/P01-Worklane-规划-2026-10-20.md)
（W1 品牌、W2 `/me`、W3 Mongo/GridFS、W4 头像已落地；W5 已落地）。

> **本文件是服务端的入口文档**（怎么跑、怎么测、代码怎么分层）。
> 仓库级总览见 [../README.md](../README.md)；前后端共用的文档（业务需求、HTTP 接口契约）在
> [../docs/](../docs/README.md)。
> **下面所有命令都在本目录（`backend/`）下执行**——`config.toml`、`docker/`、`scripts/` 都在这一层。

## 当前状态

**四个阶段已全部完成并经真实依赖验证**：地基 + Auth、Groups / Projects / Tasks、Users、Plan Views、Attachments、Reminders、Audit，以及交付收口（端到端验收、备份恢复演练、部署核对）。

> **2026-10 状态**：W1 品牌、W2 `/me`、W3 Mongo/GridFS、W4 头像已落地；W5 邮箱重置密码已落地。
> 当前迁移 head **9**、HTTP 端点 **66**（业务 62 + 健康检查 4）；详见 P01 与 B05 §5.22–§5.24。

| 能力 | 状态 |
| --- | --- |
| 工程骨架、配置加载、结构化日志、优雅关闭 | ✅ 已实现 |
| `platform`：PostgreSQL(pgxpool)、Redis、可注入时钟、UUIDv7/安全令牌、Argon2id、对象存储、SMTP、request id 传递 | ✅ 已实现 |
| `domain`：错误类型体系、用户规则、时间与逾期规则、权限基线、审计枚举与脱敏 | ✅ 已实现 |
| `httpapi`：requestID、访问日志、panic 恢复、CORS、会话网关、全局限流、统一信封、错误映射、健康检查 | ✅ 已实现 |
| 迁移 `000001_init`（9 张表、CHECK、部分唯一索引、外键删除行为）+ `000002`–`000007`（均 up/down 可逆） | ✅ 已实现（head `7`） |
| Auth：注册、登录、登出、当前用户（Argon2id + Redis 会话 + HttpOnly Cookie） | ✅ 已实现 |
| Groups / Projects / Tasks | ✅ 已实现（第二阶段） |
| Users：资料查询/更新、管理员停用、管理员创建/列表/重置密码、按用户时区计算"今天" | ✅ 已实现（第三阶段） |
| Plan Views：个人计划、组内共享计划、项目甘特图（含"任务最后修改人"） | ✅ 已实现（第三阶段） |
| Attachments：上传/列表/下载/逻辑删除/彻底删除（7 个端点） | ✅ 已实现（第三阶段） |
| Reminders：五种提醒、周期派发、幂等去重、失败原因与重试（3 个端点） | ✅ 已实现（第三阶段） |
| Audit：操作记录（**与业务同事务写入**，仅系统管理员可查，`GET /api/v1/audit`） | ✅ 已实现（第三阶段，2026-10-12） |

已注册端点共 **66 个**（Health 4 / Auth 6 / Users 9 / Groups 11 / Projects 9 / Tasks 11 / Plan Views 3 / Attachments 7 / Reminders 3 / Audit 1 / Registration Requests 2）。

真实依赖验证结果（PostgreSQL 16 + Redis 7 + **MongoDB 9.0.4**，2026-10-21 复跑）：

- `go test ./... -count=1 -p 1` 全量通过：顶层 **346** 项、含子用例合计 **942** 项，0 失败、0 跳过（设置 `TEST_MONGO_URI` 后 GridFS 用例一并执行），覆盖真实数据库上的迁移可逆性、约束生效性、单一 owner 不变式、审计的同事务与脱敏语义，以及八个域的端到端用例。
- `scripts/smoke.ps1` 对运行中的服务执行 **207 项断言全部通过**（健康检查、认证全链路、用户资料与管理员端点（含成功路径）、计划视图三端点、持有者与工作组所有权转移、管理员兜底、附件全链路（含 multipart 与两步删除）、提醒生成与派发、**审计的可见性、脱敏与全链路可追溯**、三域 CRUD、分页边界、排序白名单、版本冲突、组合筛选、幂等复制、归档只读）。
  **前提**：服务以 `SCHEDULER_INTERVAL_SECONDS=5` 且 `SECURITY_REGISTRATION_REQUIRES_APPROVAL=false` 启动（`config.toml` 的注册审核默认是 `true`，而冒烟会在注册后直接登录）；换用别的配置会得到 201 通过 / 1 失败或 141 项失败，原因见 [backend/docs/B05 §7.0.1](./docs/B05-进度与验证记录.md) 第 4/6 条坑。
- 关键实现经**变异验证**：逐条注入缺陷并确认对应用例确实失败，见 [backend/docs/B05](./docs/B05-进度与验证记录.md) 5.7。
- **首个管理员**在系统初始化时按 `security.bootstrap_admin_*` 录入（幂等、绝不覆盖既有密码）；本系统面向内部小组，不做管理员分发。见 [backend/docs/B03](./docs/B03-配置与运行.md) 3.6.1。

进度与阶段划分见 [B05-进度与验证记录.md](./docs/B05-进度与验证记录.md)。

## 文档

实现前请先读文档。文档是权威来源，代码与文档冲突时应先修文档。

**共用文档**（前后端都读，位于仓库根的 `docs/`）：

| 文档 | 内容 |
| --- | --- |
| [S01-需求文档](../docs/S01-需求文档.md) | 业务需求与验收边界（**业务范围的唯一权威来源**） |
| [S02-HTTP接口契约](../docs/S02-HTTP接口契约.md) | **约定性叙述**：信封、认证、分页、并发与幂等、限流、错误码表 |
| [api/ 逐接口详解](../docs/api/README.md) | 每个端点的请求/响应数据结构（`A01`–`A09`，前端为主） |

**服务端文档**（本目录的 `docs/`）：

| 文档 | 内容 |
| --- | --- |
| [B01-Go服务端架构设计](./docs/B01-Go服务端架构设计.md) | 技术选型、分层职责、依赖方向、**必须规避的历史缺陷** |
| [B02-数据库模型与迁移](./docs/B02-数据库模型与迁移.md) | 表结构权威定义、约束、迁移策略与验收要求 |
| [B03-配置与运行](./docs/B03-配置与运行.md) | 配置键、优先级、密钥边界、启动与迁移命令、备份恢复、部署核对 |
| [B04-设计偏离与差异登记](./docs/B04-设计偏离与差异登记.md) | 设计约定、有意偏离与加强项登记 |
| [B05-进度与验证记录](./docs/B05-进度与验证记录.md) | 阶段状态、验证方法与可复现命令、剩余工作 |

本仓库是需求、契约、数据模型与代码的**唯一权威来源**，不需要回查任何外部仓库。
正文里的路径：跨端引用写相对**仓库根**（如 `backend/docs/B03-配置与运行.md`、`docs/S02-HTTP接口契约.md`），
服务端文档内部引用自家代码写模块内相对路径（`internal/...`）；
编号约定（`S`/`A`/`B`/`F` 三套独立编号）与各自的更新时机见 [B04](./docs/B04-设计偏离与差异登记.md) §7.2。

## 阶段状态（2026-10-12）

| 阶段 | 范围 | 状态 |
| --- | --- | --- |
| 第一阶段 | 基础设施 + Auth | ✅ |
| 第二阶段 | Groups / Projects / Tasks | ✅ |
| 第三阶段 | Users / Plan Views / Attachments / Reminders / **Audit** | ✅ |
| 第四阶段 | 交付收口：端到端验收、备份恢复演练、部署核对 | ✅ 已完成（[backend/docs/B05](./docs/B05-进度与验证记录.md) §5.18 / §6.3.8） |

**邮件通道已打通（2026-10-12）**：服务商由 Outlook 换为**阿里邮箱**
（`smtp.qiye.aliyun.com:465` + 直接 TLS + `AUTH LOGIN`，密码用三方客户端安全密码），
已用真实账号实测投递成功。Outlook 一侧的结论（应用密码被 `535` 拒、服务商收紧为 OAuth2）
保留在 [backend/docs/B03 §3.5.1](./docs/B03-配置与运行.md) 作为教训与判读依据。
凭据只从环境变量注入（`MAIL_USERNAME` / `MAIL_PASSWORD` / `MAIL_FROM_ADDRESS`），绝不入库。

**换机后从这里开始**：[backend/docs/B05 §7.0](./docs/B05-进度与验证记录.md) 有完整的环境恢复步骤、
当前证据基线（测试/冒烟/迁移 head 的期望值）与五条最容易踩的坑。

## 快速开始

> **前置**：Go 工具链需可用（本机未在 PATH 中，位于 `D:\Dev\go\root\bin`）；
> 若默认 `proxy.golang.org` 不可达，先设 `$env:GOPROXY="https://goproxy.cn,direct"`；
> 下面的 `make` 命令需要本机安装 make，未安装时用 [backend/docs/B05 §7.0.1](./docs/B05-进度与验证记录.md) 的等价命令。
> 这些都属于环境问题，见 [backend/docs/B05 §3](./docs/B05-进度与验证记录.md)。
> 所有命令都在 **`backend/`** 目录下执行。

### 1. 启动测试依赖

**一键启动（推荐，日常联调用）**：

```powershell
pwsh -File ./scripts/dev-test-env.ps1                  # 依赖 + 迁移 + 起服务（默认 8000，Ctrl+C 结束）
pwsh -File ./scripts/dev-test-env.ps1 -Port 8001       # 8000 被占用时改端口（脚本会导出 SERVER_PORT）
pwsh -File ./scripts/dev-test-env.ps1 -WithFrontend     # 顺便开一个窗口起前端 dev server
pwsh -File ./scripts/dev-test-env.ps1 -Stop -Port 8001  # 停掉 8001 上的 API 与依赖容器（保留数据卷）
```

它等价于下面三条 `make` 配方（同一套 `TEST_*` 常量），但会等容器 **healthy**、检查端口占用，
并在启动前打印可用地址、测试账号与 Mailpit 面板。**它不跑测试、不跑冒烟、不碰测试数据**，
详见 [B03 §7.2](./docs/B03-配置与运行.md)。`-Port` 现在会真正写入 `SERVER_PORT`，不再只是打印值。

**或者按 `make` 分步执行**（本机已装 GNU Make）：

```powershell
make deps-up
# 等价于：docker compose --env-file docker/test.env -f docker/compose.test.yml up -d
```

依赖栈：PostgreSQL 16（`127.0.0.1:15432`）、Redis 7（`127.0.0.1:16379`）、Mailpit（`127.0.0.1:11025`）。

> 若本机已有同一套 compose 在跑（端口 15432/16379/11025），可直接复用，无需重复启动。

### 2. 迁移数据库

```powershell
$env:DATABASE_URL = "postgres://project_builder_test:project_builder_test_password@127.0.0.1:15432/project_builder_test?sslmode=disable"
go run ./cmd/migrate up
go run ./cmd/migrate version
```

### 3. 启动服务

```powershell
make run-test-config
# 等价于：设置 DATABASE_URL / REDIS_* / SECURITY_SESSION_SECRET / SECURITY_BOOTSTRAP_ADMIN_*
# 以及 SCHEDULER_INTERVAL_SECONDS=5 后 go run ./cmd/api
# 监听 127.0.0.1:8000（端口来自 config.toml，可用 SERVER_PORT 覆盖）
```

### 4. 冒烟验证

```powershell
pwsh -File ./scripts/smoke.ps1 -BaseUrl http://127.0.0.1:8000
```

> **必须与服务同配方**：脚本对"调度器已生成提醒"只轮询 30 秒，因此服务侧的
> `SCHEDULER_INTERVAL_SECONDS` 必须是 5（`make run-test-config` 已包含）。
> 用 `docker/config.test.toml` 启动会得到 201 通过 / 1 失败——它的 scheduler 是关闭的、
> mail 指向 Mailpit，而脚本断言的是"邮件未启用 → skipped"（见 [backend/docs/B05 §7.0.1](./docs/B05-进度与验证记录.md)）。

### 5. 运行测试

```powershell
make test          # 全部测试（单元 + 集成）
make test-race     # 竞态检测
make check         # 格式 + vet
```

集成测试在依赖不可达时会 `skip` 而不是失败，因此无 Docker 的机器上 `go test ./...` 仍然全部通过。
因此核对结果时**必须同时看跳过数**：`SKIP=0` 才说明集成测试真的跑了。

**测试文件位置约定**：单元测试与被测代码**同包、同目录**（Go 官方推荐做法）。
不集中到顶层 `tests/` —— 位于其他目录的测试无法访问包内未导出标识符，
而本仓库有多处测试需要直接验证私有实现细节（`.env` 解析、脱敏语义、错误映射表完整性等）。
完整理由见 [backend/docs/B03](./docs/B03-配置与运行.md) 9.1。

## 目录结构

```text
project-builder/                 # 仓库根
├── docs/                        # 前后端共用的文档（00 需求、04 接口契约）
└── backend/                     # 本目录：Go 服务端（模块根，命令都在这里执行）
    ├── cmd/api/main.go          # 唯一入口：装配依赖、启动 HTTP、优雅关闭
    ├── cmd/migrate/main.go      # 迁移入口：up / down / steps / version / force
    ├── cmd/storage-migrate/     # 附件字节 local ↔ gridfs：copy / verify / rollback
    ├── internal/
    │   ├── config/              # 启动时读取一次，返回不可变配置
    │   ├── platform/            # 基础设施：postgres、redis、mongo、时钟、ID、密码、邮件、对象存储
    │   │   ├── clock/           # 可注入时钟（测试固定时间）
    │   │   ├── idgen/           # UUIDv7 / UUIDv4 / 安全令牌
    │   │   ├── mail/            # Sender 接口 + SMTP 实现 + 未启用实现（+ 显式 tag 的联调程序）
    │   │   └── storage/         # 对象存储四方法 + Local / GridFS + 配置工厂
    │   ├── domain/              # 纯业务规则：无 IO、无 SQL、无 HTTP
    │   ├── listquery/           # 列表分页与排序的唯一实现（SQL 层 LIMIT/OFFSET、白名单、限定 id 次级键）
    │   ├── app/                 # 业务模块：每域 repo.go + service.go + http.go(+ sort.go)
    │   │   ├── apperr/          # PostgreSQL 错误 → 领域错误的统一翻译 + 事务抽象
    │   │   ├── auth/            # 注册/登录/登出/当前用户（额外含 session.go）
    │   │   ├── users/           # 用户资料与管理端点、可见性、按用户查时区
    │   │   ├── groups/          # 工作组、成员、加入申请、所有权转移
    │   │   ├── projects/        # 项目、成员、归档、派生统计
    │   │   ├── tasks/           # 任务、成员、层级、幂等复制
    │   │   ├── planviews/       # 计划视图：个人 / 组内 / 甘特图（只读）
    │   │   ├── attachments/     # 附件：上传/列表/下载/逻辑删除/彻底删除
    │   │   ├── reminders/       # 提醒：生成、周期派发、三端点（额外含 dispatcher.go）
    │   │   └── audit/           # 操作记录：查询端点 + 供各域复用的写入端口
    │   ├── httpapi/             # 跨模块 HTTP：router、middleware、problem、bind、session、cors、ratelimit、types
    │   ├── database/migrations/ # 版本化 SQL 迁移（embed 进二进制）
    │   └── testsupport/         # 集成测试装配、清理与 HTTP 客户端辅助
    ├── docs/                    # 服务端文档（01 架构 → 06 进度；00/04 在仓库根 docs/）
    ├── docker/                  # 测试依赖 compose 与测试配置
    ├── scripts/smoke.ps1        # 端到端冒烟检查（207 项断言）
    ├── config.toml              # 本地开发默认配置（不含敏感值）
    ├── .env.example             # 配置键名与占位符（不含真实值）
    └── Makefile                 # run / test / migrate / check
```

## 关键设计取舍

重写不是平移。以下差异是**有意的**，理由见 [docs/B04-设计偏离与差异登记.md](./docs/B04-设计偏离与差异登记.md)：

1. **删除只为对称而存在的层**：`struct/` 记录对象、`factory/` 依赖工厂、Repository Protocol 与实现的分离都被移除。保留 `domain`（纯规则）与 `app/`（用例 + SQL）两个有实际收益的边界。
2. **显式 SQL 替代 ORM**：所有写操作使用 `UPDATE ... RETURNING` / `INSERT ... RETURNING`，从结构上消除"更新后访问过期实体触发隐式 IO"这一历史缺陷（它曾让所有写端点 500）。
3. **错误映射单点集中**：`domain/errors.go` 定义带语义的错误码，`httpapi/problem.go` 用一张显式表映射状态码与对外文案，并有测试断言"每个错误码都已登记"。早期实现只注册了 500，导致 409/403/404/400/422 全部退化为 500。
4. **生产 CORS 拒绝通配来源**：配置校验在 `APP_ENV=production` 时直接拒绝 `*` 与开发占位会话密钥，启动即失败。
5. **未知字段显式拒绝**：请求体含契约未定义字段返回 422，避免拼错字段名（如 `expected_updated_at`）被静默忽略而退化为 last-write-wins。
6. **集成测试是常规测试的一部分**：真实 PostgreSQL/Redis 上的迁移、约束与端到端用例随 `go test` 一起跑，不再依赖"单元测试通过"作为交付依据。

## 配置

优先级（低 → 高）：

```text
代码默认值 < config.toml < .env < 进程环境变量
```

敏感值（数据库密码、SMTP 凭据、会话密钥）**不得**进入仓库，请通过 `.env` 或部署注入提供。启动日志只输出"是否已配置"，不输出内容。

完整键名见 [docs/B03-配置与运行.md](./docs/B03-配置与运行.md)。

## 许可

内部项目，未声明开源许可。
