# Worklane

**Work Items & Layered Timelines**——面向**内部小组**的项目与计划管理系统：工作组 → 项目 → 任务 → 计划视图，
并带附件、邮件提醒与操作记录（Audit）。

应用部署在内部网络，**不面向公网开放**。品牌重命名、Mongo 对象存储、头像/`/me` 与邮箱重置密码的
2026-10 规划见 [P01](docs/P01-Worklane-规划-2026-10-20.md)（**规划中，未生效**）。

## 仓库结构

```text
project-builder/
├── backend/          # Go 服务端（模块 github.com/myonku/project-builder）
│   ├── cmd/ internal/      # 代码
│   ├── docs/              # **服务端自己的文档**（01 架构 / 02 数据模型 / 03 配置运行 / 04 偏离登记 / 05 进度）
│   ├── docker/ scripts/   # 测试依赖 compose、端到端冒烟脚本
│   ├── Makefile config.toml .env.example
│   └── README.md          # 服务端入口文档（怎么跑、怎么测）
├── frontend/         # 前端工程（Vite + React 19 + TypeScript + MUI 9.5）
│   ├── src/               # theme / api / types / stores / router / layouts / views / components / hooks / utils
│   ├── scripts/           # browser-smoke.mjs（浏览器级冒烟，CDP 驱动）
│   ├── docs/              # **前端自己的文档**（F01 架构 / F02 类型 / F03 运行 / F04 偏离 / F05 进度）
│   ├── .templates/        # MUI 官方模板原样副本（**已入库**，供逐文件 diff 升级；见 .templates/README.md）
│   ├── package.json tsconfig.json vite.config.ts
│   └── README.md          # 前端入口文档（怎么跑、怎么测、代码怎么分层）
└── docs/             # **前后端共用**的文档：S01 需求、S02 接口契约、api/ 逐接口详解
```

**路径约定**：正文里出现的路径一律**相对仓库根**（例如
`backend/docs/B03-配置与运行.md`、`docs/S02-HTTP接口契约.md`），
文档之间的相对链接按 Markdown 惯例相对于所在文件。
服务端的所有命令都要**在 `backend/` 目录下执行**（`config.toml`、`docker/`、`scripts/` 都在那一层）；
前端的所有命令都要在 `frontend/` 目录下执行。

## 文档地图

文档是**权威来源**：代码与文档冲突时先修文档；**改接口先改 `docs/S02-HTTP接口契约.md`**，
再改 `docs/api/`，最后改代码，新增偏离登记到 `backend/docs/B04-设计偏离与差异登记.md`。

**四套编号各自独立**（`S` 共用、`A` 接口详解、`B` 服务端、`F` 前端），
引用写成《S02》§2.4、《B03》§3.5.1 即可：

| 编号     | 位置                                                                              | 内容                                                                                                     | 主要读者           |
| -------- | --------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- | ------------------ |
| S01      | [docs/S01-需求文档.md](docs/S01-需求文档.md)                                       | 业务需求与验收边界（**业务范围的唯一权威来源**）                                                   | 前后端 + 需求方    |
| S02      | [docs/S02-HTTP接口契约.md](docs/S02-HTTP接口契约.md)                               | **约定性叙述**：基础格式、认证、信封、分页、并发与幂等、限流、错误码表、契约加强项                 | **前后端**   |
| P01      | [docs/P01-Worklane-规划-2026-10-20.md](docs/P01-Worklane-规划-2026-10-20.md)       | **2026-10 新规划**：品牌、Mongo/GridFS、头像/`/me`、邮箱重置密码；**规划中，不是生效契约** | 前后端 + 运维      |
| A01–A10 | [docs/api/](docs/api/README.md)                                                    | **逐接口详解**：每个端点的请求/响应数据结构、权限、错误与边界                                      | **前端为主** |
| B01      | [backend/docs/B01-Go服务端架构设计.md](backend/docs/B01-Go服务端架构设计.md)       | 技术选型、分层职责、依赖方向、**必须规避的历史缺陷**                                               | 服务端             |
| B02      | [backend/docs/B02-数据库模型与迁移.md](backend/docs/B02-数据库模型与迁移.md)       | 表结构权威定义、约束、迁移策略                                                                           | 服务端             |
| B03      | [backend/docs/B03-配置与运行.md](backend/docs/B03-配置与运行.md)                   | 配置键与优先级、密钥边界、运行/迁移/备份/部署核对                                                        | 服务端 + 运维      |
| B04      | [backend/docs/B04-设计偏离与差异登记.md](backend/docs/B04-设计偏离与差异登记.md)   | 设计约定、有意偏离与加强项登记、**编号与更新时机**                                                 | 服务端             |
| B05      | [backend/docs/B05-进度与验证记录.md](backend/docs/B05-进度与验证记录.md)           | 阶段状态、验证方法与可复现命令、证据基线、已知坑                                                         | 服务端             |
| F01–F03 | [frontend/docs/](frontend/docs/README.md)                                          | 前端技术选型与目录结构、TypeScript 类型与契约映射、运行/联调/部署                                        | 前端               |
| F04      | [frontend/docs/F04-设计偏离与差异登记.md](frontend/docs/F04-设计偏离与差异登记.md) | 与 F01–F03 的偏离、官方模板改造清单、契约观察                                                           | 前端               |
| F05      | [frontend/docs/F05-进度与验证记录.md](frontend/docs/F05-进度与验证记录.md)         | 前端阶段状态、可复现验证步骤与实测证据、接续指南                                                         | 前端               |

**分工原则**：共用文档（`docs/`）只写"两端共同的约定与业务事实"，**不写任何一端的推进过程**；
推进过程、实现取舍、验证证据都留在各自工程目录内（服务端在 `backend/docs/B05`，前端在 `frontend/docs/F05`，取舍登记在 `frontend/docs/F04`）。

## 当前状态

**服务端**：四个阶段（地基 + Auth → Groups/Projects/Tasks → Users/Plan Views/Attachments/Reminders/Audit → 交付收口）
**全部完成并经真实依赖验证**：

- 已注册端点 **61 个**；数据库迁移 head = **7**（`000001`–`000007`，均 up/down 可逆；000007 为附件存储账本）。
- 服务端测试：`go test ./... -count=1 -p 1` 全量通过（新增注册审核集成测试；历史基线 323 项 + 本轮用例）。
- 端到端冒烟：`scripts/smoke.ps1` → **207 项断言全部通过**（含审计的可见性、脱敏与全链路可追溯）。
- 关键实现经**变异验证**（54 处注入缺陷全部被拦）。

服务端的运行方式、验证基线与环境恢复步骤见 [backend/README.md](backend/README.md)
与 [backend/docs/B05 §7.0](backend/docs/B05-进度与验证记录.md)；生产部署前的核对清单见
[backend/docs/B03 §7.5](backend/docs/B03-配置与运行.md)。

**前端**：**骨架期已完成（2026-10-14），功能期第 1–11 步已完成（2026-10-19）**：

- 工程可构建：`pnpm typecheck` / `pnpm build` 零错误；起环境用 `backend/scripts/dev-test-env.ps1`
  （依赖 + 迁移 + 服务，`-WithFrontend` 顺便起前端，`-Stop` 收尾；**不跑测试、不碰测试数据**）；
- **样式统一为 MUI 默认的 Material Design 2**，字号在 MD2 阶梯上调一档；首屏（登录页）实测传输约 **249 kB（gzip）**，
  表格与日期组件随路由按需加载（部署必须开 gzip/brotli，F03 §8）；
- **开发代理目标可配置**：`API_PROXY_TARGET`（默认 `http://127.0.0.1:8000`）；如果某台机器的 8000
  落在 Windows 保留端口区间，用 `.env.development.local` 指到其它端口即可，不用改源码（F03 §4）；
- **已实现并逐页验证**（真实后端 + 真实浏览器，含请求抓包与截图）：登录、概览（真实数据）、项目列表、
  项目详情（版本协议编辑 / 归档只读 / 任务列表 / 成员管理）、任务（抽屉 / 表单 / 成员）、
  个人计划与组内计划、**视图 / 甘特图**（项目锚点与成员锚点两类只读图；日/周/月、折叠、
  成员筛选，拖拽改期延后）、邮件提醒（详情 + 重试）、审计（仅管理员 / 分页排序筛选 / 详情抽屉）、
  附件（项目/任务：上传 / 下载 / 逻辑删除 / 彻底删除）、用户与工作组管理（A02/A03：账号维护、
  成员软移除/恢复、所有权转移、加入申请审核）、**自助注册申请与管理员审核**（A10：公共 `/register` +
  管理员 `/admin/registration-requests`；通过后才创建账号）；
- **旧计划剩余可选项**：甘特图第二版（拖拽改期、更多筛选）；另有一个附件 404 错误码口径偏差（F04 C6）留待后端修复；
- **2026-10 新规划（部分完成）**：Worklane 前端品牌、`/me` 个人资料页、MongoDB/GridFS 对象存储已落地；
  头像上传、邮箱验证重置密码仍未实现；
  评估、默认决策、迁移/回退与阶段顺序见 [P01](docs/P01-Worklane-规划-2026-10-20.md)；
- 已完成的权限矩阵与证据见 [F05 §5.20](frontend/docs/F05-进度与验证记录.md)；
- 接续入口、页面模式与公共件见 [F05 §7.4](frontend/docs/F05-进度与验证记录.md)。

前端的运行方式与验证证据见 [frontend/README.md](frontend/README.md)（入口文档）、
[frontend/docs/F03](frontend/docs/F03-运行与联调.md) 与 [frontend/docs/F05](frontend/docs/F05-进度与验证记录.md)。

## 许可

内部项目，未声明开源许可。
