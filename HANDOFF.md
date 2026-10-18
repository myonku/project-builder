# 交接说明（2026-10-18）

本文是**换设备续作的第一入口**：记录当前状态、如何在新机器上跑起来、测试账号/数据、
剩余工作和已知边界。更细的实现证据在
[F05](frontend/docs/F05-进度与验证记录.md)，接口契约在
[S01](docs/S01-需求文档.md)、[S02](docs/S02-HTTP接口契约.md) 与 [api/](docs/api/README.md)。

## 1. 当前状态（一句话）

- **服务端**：已完成并经真实依赖验证（59 个端点、迁移 head=5、`go test` 323 项通过、冒烟 207 项断言通过）。
- **前端**：骨架期 + **功能期第 1–10 步全部完成**；第 10 步权限回归见
  [F05 §5.20](frontend/docs/F05-进度与验证记录.md)。
- **剩余唯一功能项**：项目甘特图（交互方案未定，明确不阻塞其它工作）。
- **唯一已知契约偏差**：非项目可见者上传项目附件返回 `attachment_not_found`，
  A07 写的是 `project_not_found`（F04 C6），不影响现有 UI，留待后端修复。
- 前端工作区在交接前应为 **clean**（最后一次提交见 `git log -1`）。

## 2. 新设备启动清单

### 2.1 需要的环境

| 依赖 | 版本/说明 |
| --- | --- |
| Windows + PowerShell 7 | 当前开发机为 Windows 11 + PowerShell 7.6.7 |
| Node.js / pnpm | Node 24.11.1 / pnpm 11.7.0 |
| Go | 1.26.1 |
| Docker Desktop | PostgreSQL 16（容器端口 **15432**）、Redis 7（**16379**）、Mailpit（**11025 / 18025**） |
| Chrome | 前端浏览器回归/截图用；直接看页面用普通浏览器即可 |
| GNU Make | 可选；没有 make 时用 `backend/scripts/dev-test-env.ps1` |

### 2.2 拉起依赖 + 后端 + 前端

```powershell
# 仓库根
pwsh -File ./backend/scripts/dev-test-env.ps1 -Port 18000

# 另开一个终端
cd frontend
pnpm install              # 第一次或依赖变更后
pnpm dev --host 127.0.0.1
```

打开 <http://127.0.0.1:5173>，用下面的测试账号登录。

**本机 8000 端口问题**：Windows 的保留端口区间曾包含 8000（`7960–8059`），直接绑定会
`WSAEACCES`。因此本机默认用 **18000**，前端通过 `frontend/.env.development.local` 覆盖：

```dotenv
API_PROXY_TARGET=http://127.0.0.1:18000
```

这个文件**不入库**（`.env.*.local` 已忽略）。到 8000 正常的机器上可以不创建它，默认仍是
`http://127.0.0.1:8000`。`-Port` 脚本会导出 `SERVER_PORT`，不会再出现“脚本说 18000、实际监听 8000”。

### 2.3 停止

```powershell
# 停前端：Ctrl+C 结束 pnpm dev；停后端：Ctrl+C 结束 API
pwsh -File ./backend/scripts/dev-test-env.ps1 -Stop
```

`-Stop` 只停依赖容器；API / 前端由各自终端结束。交接前已释放 18000 / 5173 / 9333。

### 2.4 常用验证

```powershell
cd frontend
pnpm typecheck
pnpm build
```

构建会提示入口 chunk >500 kB（约 794 kB / gzip 249 kB），这是已知情况（`useApiError` 共享 chunk
并入入口），不是构建失败。**部署必须开 gzip/brotli**。

`make test` 会 **TRUNCATE 业务表（含引导管理员）**；跑完必须重启 API 并等待引导管理员重建，
否则无法登录。不要在没有确认数据可清空时运行它。日常联调用 `dev-test-env.ps1` 即可，
它**不跑测试、不碰测试数据**。

## 3. 测试账号与测试数据

> 以下仅存在于 **development/test** 数据库，绝不能用于生产。

| 账号 | 密码 | 角色/用途 |
| --- | --- | --- |
| `bootstrap_admin` | `bootstrap_admin_test_password` | 系统管理员；可看全部项目/组、审计、管理页 |
| `audit_member` | `audit_member_test_password` | `审计验证组` 活跃成员，**非** `审计验证项目` 成员；组内只读共享视角 |
| `perm_member` | `perm_member_password` | 第 10 步新建；`审计验证组` 成员 + `审计验证项目` 普通成员；项目普通成员视角 |
| `step9_user` | `step9_user_final_password` | `第9步验证组（最终）` 所有者；用户/组管理联调 |
| `step9_join_user` | `step9_join_user_password` | 与 `审计验证组` 无关的非成员；非成员 404 视角 |

主要测试数据：

| 数据 | ID / 说明 |
| --- | --- |
| `审计验证项目（已更新）` | `01a148e6-87ad-781d-a019-2c4e0528db22`，`group_readonly`，归属 `审计验证组` |
| `审计验证组` | `01a148e6-871f-7fdf-b920-b43605453d01`，所有者 `bootstrap_admin` |
| `第9步验证组（最终）` | `01a1490f-f493-7aa9-be4b-2bcad1e4c9bd`，所有者 `step9_user` |
| `附件验证任务` | `01a148fa-ab92-739f-82d7-ca78f41aaed0`，已共享，用于权限/附件回归 |

第 10 步的权限探针创建的任务已删除、附件已彻底删除、任务描述已恢复；审计表仍会多几条
`权限探针` 记录，这是验证的正常痕迹，不影响功能。

## 4. 代码与文档入口

| 目标 | 先读 |
| --- | --- |
| 页面/组件/路由结构 | [F01](frontend/docs/F01-前端架构设计.md) |
| 数据对象模型 | [F02](frontend/docs/F02-数据对象模型.md) |
| 启动、联调、部署 | [F03](frontend/docs/F03-运行与联调.md) |
| 设计取舍/偏离 | [F04](frontend/docs/F04-设计偏离与差异登记.md) |
| **逐页证据与接续指南** | [F05](frontend/docs/F05-进度与验证记录.md)（§7.2 顺序、§7.4 入口） |
| 接口权限/错误码 | [A04](docs/api/A04-projects.md)、[A05](docs/api/A05-tasks.md)、[A07](docs/api/A07-attachments.md) |
| 服务端运行/验证 | [B03](backend/docs/B03-配置与运行.md)、[B05](backend/docs/B05-进度与验证记录.md) |

几个最容易复用的约定：

- 列表页：`useListQuery` + DataGrid 服务器模式，见 F01 §5.9；筛选区用
  `filterRowSx` / `filterItemSx`（`filterRowSx(..., true)` 用于 helper 高度不一致的顶部对齐）。
- 编辑：`useVersionedForm` 管 `expected_updated_at` 与 409；**异步加载后再 `reset()` 的字段
  用受控 `Controller`**，不要用非受控 `register`（F01 §6 第 39 条）。
- 权限：`usePermission` 的 `canManageProject` / `canWriteTasksOfProject` /
  `canWriteProjectContent` 各有不同口径，不要互相替代；直接看 A04/A05/A07 的权限速查。
- 工作组 `PATCH` 的成员投影不是调用者的，保存后要重新 `GET`（F01 §6 第 37 条、F04 C5）。
- 时间：审计用 `DateTimePicker`，按账号时区转 RFC3339；计划用 `DatePicker` 自然日。
- 404/403：A04 的读路径“不可见 = 404”，写路径项目更新/归档“不可见 = 403”、成员管理“无权 = 404”；
  UI 已按接口分别处理，不要统一改成一种。

## 5. 剩余工作与建议顺序

### 5.1 项目甘特图（唯一剩余功能项）

- 接口已就绪：`GET /plan-views/projects/{id}/gantt`；类型/API 在
  `frontend/src/api/planViews.ts`，行模型参考 `components/plan/planRows.ts`。
- 先定交互：时间尺度（周/月）、是否拖拽改日期（对应 `PATCH /tasks/{id}/dates`）、层级折叠。
- 再定实现：自研 SVG 或引入 `frappe-gantt`（当前未安装，需类型声明）；见 F04 D3。
- 验收至少覆盖：项目/任务层级、共享/只读、日期拖拽失败回滚、窄屏、归档只读。

### 5.2 可选的后端修复

- **C6**：`POST /projects/{id}/attachments` 对不可见项目返回 `attachment_not_found`，
  A07 要求 `project_not_found`。修复点在 `backend/internal/app/attachments/service.go` 的
  `Upload` / `resolveContainer` 错误映射；修完补一个“非成员上传 → 404 project_not_found”的测试。
- **并发附件删除/彻底删除 409**：F05 §7.3 #17，仍未复现；需要两个会话并发操作。
- 其余服务端验证基线已全绿，不需要重跑全量，除非改后端。

### 5.3 前端可选改进

- 入口 chunk >500 kB 警告；如要优化，可在 `vite.config.ts` 里做 vendor 拆分，但先确认
  不破坏按路由懒加载与首屏体积。
- 测试目前以 F05 的浏览器脚本 + 手工回归为主，没有 ESLint/单测；新页面优先复用现有模式，
  不要先引入大依赖。
- `F05 §7.3` 还列了若干“按需再补”的已知项（用户列表无文本筛选、成员/申请只加载前 100 条等）。

## 6. 新设备上的第一次验收

建议按顺序做，全部通过再开始新功能：

1. `docker compose` 依赖健康；`dev-test-env.ps1 -Port 18000` 能起 API，`GET /healthz` 为 ok。
2. `frontend/.env.development.local` 指向 18000；`pnpm dev` 打开 5173。
3. `bootstrap_admin` 登录；概览/项目/计划/提醒/审计/用户/工作组各打开一次。
4. `perm_member` 登录：项目详情能看、新建任务/上传附件/任务编辑可用、项目编辑/归档禁用。
5. `audit_member` 登录：同一项目只读、任务编辑/上传禁用。
6. `step9_join_user` 登录：项目列表为空，直开项目 URL 显示“对象不存在或你没有访问权限”。
7. `pnpm typecheck` / `pnpm build` 均通过；浏览器控制台无 exception。

第 10 步的完整记录、响应码和截图见
[F05 §5.20](frontend/docs/F05-进度与验证记录.md)；本轮之前的页面证据见 §5.7–§5.19。

## 7. 交接检查清单

- [x] 功能期第 1–10 步完成并验证（F05 §5.7–§5.20）
- [x] 三类权限账号可登录，测试数据/ID 记录在本文
- [x] 启动/停止/端口/代理/`-Port` 说明
- [x] 剩余工作：甘特图、C6、并发附件 409
- [x] 文档入口与代码入口
- [x] git 工作区 clean（最后一次提交见 `git log -1`）
