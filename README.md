# Worklane

**Work Items & Layered Timelines**：面向内部小组的项目与计划管理系统，按工作组 → 项目 → 最多三层任务组织协作，提供计划视图、附件、提醒和操作记录。应用部署于内部网络。

## 当前交付基线

截至 2026-10-27，层级任务汇总已合并到 `main`：HTTP **69 个端点**（65 业务 + 4 健康检查），数据库迁移 head **10**。

- 项目、任务、成员与权限、个人/组内计划、只读甘特图、提醒、审计、账号维护及注册审核。
- 工作组自助搜索、申请、审核，以及“我的组”基本资料和成员只读详情。
- 叶子任务手工维护状态/进度，父任务按直接下级等权逐层汇总，项目只汇总有效根任务。无任务项目可手工维护；归档及解除归档作为独立管理操作。
- 个人资料、头像上传/移除、圆形裁剪及独立缩放/旋转；邮箱验证密码重置及会话失效。
- 项目/任务附件、常见格式的浏览器本地预览、本人附件集中查询与两步删除。
- PostgreSQL 保存业务事实，Redis 管会话；附件/头像字节使用 local（默认）或 MongoDB GridFS。

最近一次完整验收记录（2026-10-27）：后端 **360 顶层 / 961 含子用例**通过，0 失败、0 测试跳过；HTTP 冒烟 **207/207**；前端生产构建与桌面/390px 手机浏览器验收通过。命令、运行环境与证据见 [B05](backend/docs/B05-进度与验证记录.md) 和 [F05](frontend/docs/F05-进度与验证记录.md)。这些是工程验收记录，不表示已完成生产发布。

## 仓库结构

```text
project-builder/
├── backend/       # Go 1.26、Gin、pgx；cmd/、internal/、迁移、Docker 测试依赖和脚本
├── frontend/      # Vite 8、React 19、TypeScript 7、MUI 9.5；src/、scripts/、docs/
└── docs/          # 共用需求、HTTP 契约、逐接口详解；archive/ 保留已完成计划
```

后端命令在 `backend/` 执行，前端命令在 `frontend/` 执行。运行入口见 [后端 README](backend/README.md)、[前端 README](frontend/README.md)。

## 文档入口

[文档总览](docs/README.md) 负责索引当前规范、交付边界与后续增强。P00/P01 已归档，历史计划不再作为现行设计或待办入口。

| 文档 | 责任 |
| --- | --- |
| [S01 需求](docs/S01-需求文档.md) | 业务范围、层级汇总和权限边界 |
| [S02 HTTP 契约](docs/S02-HTTP接口契约.md) | 信封、认证、分页、错误、并发与幂等 |
| [A01–A10 接口详解](docs/api/README.md) | 请求、响应字段及接口权限 |
| [B01 架构](backend/docs/B01-Go服务端架构设计.md)、[B02 数据模型](backend/docs/B02-数据库模型与迁移.md)、[B03 运行](backend/docs/B03-配置与运行.md) | 后端实现约束、迁移、备份及发布 |
| [B04 取舍](backend/docs/B04-设计偏离与差异登记.md)、[B05 验收](backend/docs/B05-进度与验证记录.md) | 后端决策与按日期保留的证据 |
| [F01–F05](frontend/docs/README.md) | 前端架构、类型、联调、取舍及验收 |

修改业务或接口时，先更新 S01/S02 及对应 A 文档，再实施；实现取舍和证据归 B/F。正文代码路径相对仓库根，Markdown 链接相对所在文件。

## 本地运行与验证

依赖栈使用 `backend/docker/compose.test.yml`。在 `backend/`：

```powershell
pwsh -NoProfile -File scripts/dev-test-env.ps1
```

该脚本启动依赖、迁移和 API，不执行测试。前端另在 `frontend/` 运行 `pnpm dev`；需要修改 API 代理目标时使用 `.env.development.local` 的 `API_PROXY_TARGET`。配置和端口以 [B03](backend/docs/B03-配置与运行.md)、[F03](frontend/docs/F03-运行与联调.md) 为准。

测试会清理指定测试库，务必使用独立测试数据库；后端串行运行 `go test ./... -count=1 -p 1`，前端运行 `pnpm typecheck` / `pnpm build`。GridFS 用例需配置 `TEST_MONGO_URI` 和 `TEST_MONGO_DATABASE`；完整配方见 B05/F03。

## 迁移和发布

层级汇总需要先应用 `000010_hierarchical_rollup`，再更新后端，最后发布完整前端 `dist`。迁移会回填历史汇总并推进相关版本，旧页面保存前应刷新。

`000010` 回退须停写、先备份：既有对象的状态/进度会恢复为迁移前快照，覆盖迁移后对这些字段的修改；新增对象保留回退时有效值。具体步骤与边界见 B02/B03。

## 后续增强

甘特图拖拽改期/更多筛选、邮件异步发送/出站表仍是未排期可选增强。任务恢复 HTTP 入口未新增；存储层恢复会重新计入汇总。

## 许可

内部项目，未声明开源许可。
