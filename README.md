# 项目/计划管理系统 — Go 服务端

项目/计划管理系统的服务端，由 Python/lihil 版本重构为 **Go 1.26 + Gin + pgx**。

应用面向内部网络，不面向公网开放。

## 当前状态

**第一至第三阶段已完成并经真实依赖验证：地基 + Auth、Groups / Projects / Tasks、Users。**

| 能力 | 状态 |
| --- | --- |
| 工程骨架、配置加载、结构化日志、优雅关闭 | ✅ 已实现 |
| `platform`：PostgreSQL(pgxpool)、Redis、可注入时钟、UUIDv7/安全令牌、Argon2id | ✅ 已实现 |
| `domain`：错误类型体系、用户规则、时间与逾期规则、权限基线 | ✅ 已实现 |
| `httpapi`：requestID、访问日志、panic 恢复、CORS、会话网关、统一信封、错误映射、健康检查 | ✅ 已实现 |
| 初始迁移 `000001_init`（9 张表、CHECK、部分唯一索引、外键删除行为、up/down 可逆） | ✅ 已实现 |
| Auth：注册、登录、登出、当前用户（Argon2id + Redis 会话 + HttpOnly Cookie） | ✅ 已实现 |
| Groups / Projects / Tasks | ✅ 已实现（第二阶段） |
| Users：资料查询/更新、管理员停用、管理员创建/列表/重置密码、按用户时区计算"今天" | ✅ 已实现（第三阶段） |
| Plan Views / Attachments / Reminders / Audit | ⬜ 第三阶段剩余 |

真实依赖验证结果（PostgreSQL 16.15 + Redis 7）：

- `go test ./...` **顶层 232 项通过、0 失败、0 跳过**（含子用例共 634 项），覆盖真实数据库上的迁移可逆性、约束生效性、单一 owner 不变式，以及 Auth/Users/Groups/Projects/Tasks 的端到端用例。
- `scripts/smoke.ps1` 对运行中的服务执行 **117 项断言全部通过**（健康检查、认证全链路、用户资料与管理员端点（含成功路径）、三域 CRUD、分页边界、排序白名单、版本冲突、组合筛选、幂等复制、归档只读）。
- 关键实现经**变异验证**：逐条注入缺陷并确认对应用例确实失败，见 [docs/06](docs/06-迁移进度与验证.md) 5.7。
- **首个管理员**在系统初始化时按 `security.bootstrap_admin_*` 录入（幂等、绝不覆盖既有密码）；本系统面向内部小组，不做管理员分发。见 [docs/03](docs/03-配置与运行.md) 3.6.1。

进度与阶段划分见 [docs/06-迁移进度与验证.md](docs/06-迁移进度与验证.md)。

## 文档

实现前请先读文档。文档是权威来源，代码与文档冲突时应先修文档。

| 文档 | 内容 |
| --- | --- |
| [docs/00-需求文档.md](docs/00-需求文档.md) | 业务需求与验收边界（**业务范围的唯一权威来源**） |
| [docs/01-Go服务端架构设计.md](docs/01-Go服务端架构设计.md) | 技术选型、分层职责、依赖方向、**必须规避的历史缺陷** |
| [docs/02-数据库模型与迁移.md](docs/02-数据库模型与迁移.md) | 表结构权威定义、约束、迁移策略与验收要求 |
| [docs/03-配置与运行.md](docs/03-配置与运行.md) | 配置键、优先级、密钥边界、启动与迁移命令 |
| [docs/04-HTTP接口契约.md](docs/04-HTTP接口契约.md) | 错误信封、分页、并发与幂等、已实现端点的实测行为 |
| [docs/05-迁移映射与差异.md](docs/05-迁移映射与差异.md) | Python → Go 的目录/文件映射与**有意偏离**说明 |
| [docs/06-迁移进度与验证.md](docs/06-迁移进度与验证.md) | 阶段状态、验证方法与可复现命令、剩余工作 |

Python 仓库的文档已全部处置完毕（归并 / 取代 / 废弃），本仓库是文档与代码的唯一主线，
不需要回查旧仓库。逐条处置结论见 [docs/05](docs/05-迁移映射与差异.md) 第 7 节。

## 快速开始

### 1. 启动测试依赖

```powershell
make deps-up
# 等价于：docker compose --env-file docker/test.env -f docker/compose.test.yml up -d
```

依赖栈：PostgreSQL 16（`127.0.0.1:15432`）、Redis 7（`127.0.0.1:16379`）、Mailpit（`127.0.0.1:11025`）。

> 若本机已有 Python 版本在跑的同一套 compose（端口 15432/16379/11025），可直接复用，无需重复启动。

### 2. 迁移数据库

```powershell
$env:DATABASE_URL = "postgres://project_builder_test:project_builder_test_password@127.0.0.1:15432/project_builder_test?sslmode=disable"
go run ./cmd/migrate up
go run ./cmd/migrate version
```

### 3. 启动服务

```powershell
make run-test-config
# 等价于：设置 DATABASE_URL / REDIS_* / SECURITY_SESSION_SECRET 后 go run ./cmd/api
```

### 4. 冒烟验证

```powershell
pwsh -File ./scripts/smoke.ps1 -BaseUrl http://127.0.0.1:8001
```

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
完整理由见 [docs/03](docs/03-配置与运行.md) 9.1。

## 目录结构

```text
project-builder/
├── cmd/api/main.go                 # 唯一入口：装配依赖、启动 HTTP、优雅关闭
├── cmd/migrate/main.go             # 迁移入口：up / down / steps / version / force
├── internal/
│   ├── config/                     # 启动时读取一次，返回不可变配置
│   ├── platform/                   # 基础设施：postgres、redis、时钟、ID、密码
│   │   ├── clock/                  # 可注入时钟（测试固定时间）
│   │   └── idgen/                  # UUIDv7 / UUIDv4 / 安全令牌
│   ├── domain/                     # 纯业务规则：无 IO、无 SQL、无 HTTP
│   ├── listquery/                  # 列表分页与排序的唯一实现（SQL 层 LIMIT/OFFSET、白名单、限定 id 次级键）
│   ├── app/                        # 业务模块：每域 repo.go + service.go + http.go(+ sort.go)
│   │   ├── apperr/                 # PostgreSQL 错误 → 领域错误的统一翻译 + 事务抽象
│   │   ├── auth/                   # 注册/登录/登出/当前用户（额外含 session.go）
│   │   ├── users/                  # 用户资料与管理端点、可见性、按用户查时区
│   │   ├── groups/                 # 工作组、成员、加入申请
│   │   ├── projects/               # 项目、成员、归档、派生统计
│   │   └── tasks/                  # 任务、成员、层级、幂等复制
│   ├── httpapi/                    # 跨模块 HTTP：router、middleware、problem、bind、session、cors、types
│   ├── database/migrations/        # 版本化 SQL 迁移（embed 进二进制）
│   └── testsupport/                # 集成测试装配、清理与 HTTP 客户端辅助
├── docs/                           # 设计与契约文档（00 需求 → 06 进度）
├── docker/                         # 测试依赖 compose 与测试配置
├── scripts/smoke.ps1               # 端到端冒烟检查（117 项断言）
├── config.toml                     # 本地开发默认配置（不含敏感值）
├── .env.example                    # 配置键名与占位符（不含真实值）
└── Makefile                        # run / test / migrate / check
```

## 与 Python 版本的关键差异

重写不是平移。以下差异是**有意的**，理由见 [docs/05-迁移映射与差异.md](docs/05-迁移映射与差异.md)：

1. **删除只为对称而存在的层**：`struct/` 记录对象、`factory/` 依赖工厂、Repository Protocol 与实现的分离都被移除。保留 `domain`（纯规则）与 `app/`（用例 + SQL）两个有实际收益的边界。
2. **显式 SQL 替代 ORM**：所有写操作使用 `UPDATE ... RETURNING` / `INSERT ... RETURNING`，从结构上消除 Python 版本最严重的缺陷（更新后访问过期 ORM 属性触发隐式 IO，导致所有写端点 500）。
3. **错误映射单点集中**：`domain/errors.go` 定义带语义的错误码，`httpapi/problem.go` 用一张显式表映射状态码与对外文案，并有测试断言"每个错误码都已登记"。Python 版本只注册了 500，导致 409/403/404/400/422 全部退化为 500。
4. **生产 CORS 拒绝通配来源**：配置校验在 `APP_ENV=production` 时直接拒绝 `*` 与开发占位会话密钥，启动即失败。
5. **未知字段显式拒绝**：请求体含契约未定义字段返回 422，避免拼错字段名（如 `expected_updated_at`）被静默忽略而退化为 last-write-wins。
6. **集成测试是常规测试的一部分**：真实 PostgreSQL/Redis 上的迁移、约束与端到端用例随 `go test` 一起跑，不再依赖"单元测试通过"作为交付依据。

## 配置

优先级（低 → 高）：

```text
代码默认值 < config.toml < .env < 进程环境变量
```

敏感值（数据库密码、SMTP 凭据、会话密钥）**不得**进入仓库，请通过 `.env` 或部署注入提供。启动日志只输出"是否已配置"，不输出内容。

完整键名见 [docs/03-配置与运行.md](docs/03-配置与运行.md)。

## 许可

内部项目，未声明开源许可。
