# 项目/计划管理系统 — 前端

项目/计划管理系统的前端，使用 **Vite 8 + React 19 + TypeScript 7 + MUI 9.5** 实现，
UI 骨架来自 MUI 官方 **CRUD Dashboard** 与 **Sign-in** 模板。

应用部署在内部网络，**不面向公网开放**。

> **本文件是前端的入口文档**（怎么跑、怎么测、代码怎么分层）。
> 仓库级总览见 [../README.md](../README.md)；前后端共用的文档（业务需求、HTTP 接口契约、逐接口详解）在
> [../docs/](../docs/README.md)；前端自己的设计与过程文档在 [docs/](./docs/README.md)。
> **下面所有命令都在本目录（`frontend/`）下执行**——`package.json`、`vite.config.ts`、`src/` 都在这一层。

## 当前状态

**骨架期已完成（2026-10-14），功能期已推进到第 9 步（2026-10-17）**：工程可构建、主题层就位、
`api/` 与 `types/` 覆盖全部端点与类型；登录、概览、项目列表与详情、任务（抽屉 / 表单 / 成员）、
个人计划与组内计划、邮件提醒、审计、附件、用户与工作组管理都已在**真实浏览器 + 真实后端**上验证过；
**只剩权限分支回归与甘特图**。下一步的接续入口（要碰哪些文件、抄哪个模式、验收要点）
见 [F05 §7.4](./docs/F05-进度与验证记录.md)。

| 能力 | 状态 |
| --- | --- |
| 工程骨架：Vite / TS 严格模式 / 路径别名 / 环境变量 / 代理 | ✅ 已实现 |
| 主题：**MUI 默认 Material Design 2** + 亮/暗颜色方案 + 字号上调一档 | ✅ 已实现 |
| `api/`：`client.ts`（Cookie、信封、错误、限流、上传、下载）+ A01–A09 九个域文件 | ✅ 已实现 |
| `types/`：按《F02》全量落地（资源、计划视图投影、请求体；派生字段 `readonly`） | ✅ 已实现 |
| 状态与路由：`auth` / `ui` 两个窄 store、`RequireAuth` / `RequireAdmin` | ✅ 已实现 |
| 布局：Dashboard（Header + **三级可收缩侧边栏**）与 Blank（顶栏 + 内容区） | ✅ 已实现 |
| 登录页：顶栏、加高卡片、"记住登录信息"、注册/忘记密码占位入口 | ✅ 已实现 |
| 联调 Hook：`useVersionedForm` / `useListQuery` / `useApiError` / `usePermission` / `useIdempotencyKey` | ✅ 已实现 |
| 页面：**概览（已接真实数据）**、**项目列表**、**项目详情（新建/编辑项目、任务列表与新建/编辑任务、项目与任务成员管理、任务详情抽屉、附件）**、**个人计划与组内计划**、**邮件提醒**、**审计（仅管理员）**、**用户与工作组管理（仅管理员）** 已实现；甘特图仍待定 | ✅ / ⏳ |
| 功能期进度：第 1–9 步（项目列表 / 项目详情与任务 / 任务与成员管理 / 计划视图 / 概览 / 提醒 / 审计 / 附件 / 用户与工作组管理） | ✅ 已完成并验证（F05 §5.7–5.17） |
| 功能期剩余：权限分支回归、甘特图（待定） | ⏳ 顺序与入口见 F05 §7.2、§7.4 |
| 开发代理：`API_PROXY_TARGET`（默认 8000；本机可用 `.env.development.local` 覆盖） | ✅ 已实现（F03 §4） |
| 自助注册入口、日期选择器 Provider、ESLint、测试 | ⏳ 按需引入（见 F05 §7.3） |

**实测基线**（2026-10-14 骨架期 + 2026-10-17 功能期，命令与输出见 [F05 §5](./docs/F05-进度与验证记录.md)）：

- `pnpm typecheck`、`pnpm build` 零错误；`pnpm dev` 起得来（`http://localhost:5173`）；
- 浏览器实测：登录页 → `POST /auth/login` → `GET /auth/me` → 概览页（走 Vite 代理，HttpOnly Cookie）；
- 依赖版本：React 19.3 / MUI 9.5 / X 组件 9.15 / react-router 8.4 / Vite 8.3 / TypeScript 7.0 / Zod 4.6。
- 功能期各页均为**真实后端 + 真实浏览器（CDP）**实测，并抓取真实请求/响应：项目列表（§5.7）、项目详情与任务抽屉（§5.8）、任务表单与成员（§5.9）、计划视图（§5.10）、概览（§5.11）、提醒（§5.12）、两次样式反馈的修复（§5.13、§5.14）、审计与代理配置（§5.15）、附件（§5.16）、用户与工作组管理（§5.17）。

## 文档

实现前请先读文档。文档是权威来源，代码与文档冲突时应先修文档。

**共用文档**（前后端都读，位于仓库根的 `docs/`）：

| 文档 | 内容 |
| --- | --- |
| [S01-需求文档](../docs/S01-需求文档.md) | 业务需求与验收边界（**业务范围的唯一权威来源**） |
| [S02-HTTP接口契约](../docs/S02-HTTP接口契约.md) | **约定性叙述**：信封、认证、分页、并发与幂等、限流、错误码表 |
| [api/ 逐接口详解](../docs/api/README.md) | 每个端点的请求/响应数据结构（`A01`–`A09`，**前端为主**） |

**前端文档**（本目录的 `docs/`）：

| 文档 | 内容 |
| --- | --- |
| [F01-前端架构设计](./docs/F01-前端架构设计.md) | 技术选型、目录结构、状态分层、与契约的对接约定、**必须规避的坑** |
| [F02-数据对象模型](./docs/F02-数据对象模型.md) | TypeScript 类型、请求体类型、端点映射、派生字段总表 |
| [F03-运行与联调](./docs/F03-运行与联调.md) | 环境要求、`VITE_*`、代理、脚本命令、联调顺序、同源部署 |
| [F04-设计偏离与差异登记](./docs/F04-设计偏离与差异登记.md) | 与 F01–F03 的偏离（`D` 编号）、模板改造清单、契约观察（`C` 编号） |
| [F05-进度与验证记录](./docs/F05-进度与验证记录.md) | 阶段状态、验证环境、可复现步骤与实测证据、接续指南 |

> **改接口的顺序**：先改 `../docs/S02` → 再改 `../docs/api/` → 最后改代码；
> 前端实现侧的取舍登记到 F04，不要把单端的推进过程写进共用文档。

## 快速开始

> **前置**：Node.js ≥ 20.10（实测 24.x）、pnpm（实测 11.x）；后端已在 `127.0.0.1:8000` 运行
> （起法见 [../backend/README.md](../backend/README.md) 或 [F03 §9](./docs/F03-运行与联调.md)）。
> 所有命令都在 **`frontend/`** 目录下执行。

```powershell
pnpm install          # 安装依赖（首次需要能访问 npm registry；运行时不依赖外网）
pnpm dev              # 开发服务器 http://localhost:5173，带 /api 与 /healthz 代理
```

登录用后端的引导管理员（测试配置内置：`bootstrap_admin`，口令见 [F03 §9](./docs/F03-运行与联调.md)）。

### 浏览器级冒烟（可选）

用一个带远程调试端口的 Chrome 驱动真实浏览器走一遍"登录 → 概览"并截图：

```powershell
chrome --headless=new --remote-debugging-port=9333 --user-data-dir=$env:TEMP\pb-chrome about:blank
node scripts/browser-smoke.mjs 9333 http://127.0.0.1:5173/login bootstrap_admin <口令> .verify/dashboard/dashboard.png
```

产物落在 `.verify/<域>/`（已 gitignore，按页面域分目录）；脚本本身随仓库走。详见 [F03 §9.2](./docs/F03-运行与联调.md)。

## 脚本命令

| 命令 | 作用 |
| --- | --- |
| `pnpm dev` | 启动 Vite 开发服务器（`5173`），带 `/api` 代理 |
| `pnpm build` | `tsc -b && vite build`，类型检查 + 生产构建到 `dist/` |
| `pnpm preview` | 本地预览 `dist/`（**不带代理**，联调请用 `dev`） |
| `pnpm typecheck` | `tsc --noEmit`，提交前使用 |

**骨架期明确不引入**：ESLint / Prettier、单元测试与 E2E、提交钩子——等代码成规模后一次性接入（理由见 F01 §2.1、F03 §6）。

## 目录结构

```text
frontend/                        # 本目录：命令都在这里执行
├── index.html                   # 入口 HTML（标题取 VITE_APP_TITLE；不含任何外网字体链接）
├── vite.config.ts               # 插件、@ 别名、/api 与 /healthz 代理
├── tsconfig.json                # 严格模式 + verbatimModuleSyntax + @/* 别名
├── .env.development / .env.production
├── scripts/browser-smoke.mjs    # 浏览器级冒烟（CDP 驱动，走登录 → 概览并截图）
├── docs/                        # 前端文档（F01 架构 → F05 进度）
└── src/
    ├── main.tsx  App.tsx        # 入口：字体 + 全局样式；主题 → 通知 → 对话框 → 路由
    ├── theme/                   # AppTheme.tsx：MUI 默认 Material Design 2 + 亮/暗 + 字号
    ├── api/                     # client.ts（唯一 fetch 出口）+ 按 A01–A09 分域的接口文件
    ├── types/                   # 领域类型与请求体（只放类型，不 import 运行时代码）
    ├── stores/                  # 窄状态：auth（当前用户）、ui（当前工作组）
    ├── router/                  # 路由表（按路由懒加载）+ RequireAuth / RequireAdmin
    ├── layouts/                 # Dashboard 布局（Header / 三级可收缩 Sidebar）+ Blank 顶栏布局
    ├── views/                   # 一个 view = 一条路由（概览/项目/计划/提醒/审计/管理均已接通）
    ├── components/common/       # PageContainer / StatusTag / CellText / ProgressCell / DataTooltip / filterLayout / TruncatedText / 品牌区 / 加载态
    ├── components/project/      # 项目表单、成员面板、展示元数据
    ├── components/task/         # 任务表单、任务抽屉、任务成员面板、展示元数据
    ├── components/plan/         # 计划视图：行模型 / 筛选栏 / 汇总卡片 / 表格
    ├── components/reminder/     # 提醒：状态元数据、详情抽屉
    ├── components/audit/        # 审计：动作/对象类型元数据、详情抽屉
    ├── components/attachment/   # 附件：项目/任务共用的列表 + 上传 / 下载 / 两步删除面板
    ├── components/user/         # 用户：创建/编辑/重置密码表单与展示元数据
    ├── components/group/        # 工作组：表单、详情抽屉、成员面板、加入申请审核
    ├── hooks/                   # 模板 Hook（useDialogs / useNotifications）+ 本项目自研 Hook
    ├── utils/                   # datetime（原样透传）/ pagination（0/1 基换算）/ storage / uuid
    └── styles/index.css
```

## 关键设计取舍

以下几条是**有意为之**，详细理由与登记编号见 [F04](./docs/F04-设计偏离与差异登记.md)：

1. **样式统一为 MUI 默认的 Material Design 2**：不用官方模板那套定制主题（Inter 字体、8px 圆角、自定义阴影与 10 组组件覆盖），`src/theme/` 只有一个 `AppTheme.tsx`，在 MD2 默认值之上只做两件事——打开亮/暗颜色方案、把正文与控件字号上调一档（body1 18px）。页面里**不要**各自调字号或主题色。
2. **`api/client.ts` 是唯一直接使用 `fetch` 的地方**：Cookie、信封解包、错误封装、限流头、上传下载全部收口；各域文件只拼路径与类型，**不加工字段**（`updated_at` 必须原样字符串返回）。
3. **业务数据不进 store**：项目/任务是"按 id、按筛选条件"取的数据，进全局 store 会立刻出现缓存新鲜度问题，让 `expected_updated_at` 提交必然 409。视图层用 Hook 加载、局部保存。
4. **版本协议集中在一个 Hook 里**：`useVersionedForm` 负责缓存 `updated_at`、注入 `expected_updated_at`、并在 `409 version_conflict` 时弹窗让用户选"放弃并刷新 / 保留并重试"，**不自动重试、不静默合并**。
5. **错误映射集中**：只有 `useApiError` 做"状态码 → 文案/动作"的映射（401 跳登录、403 提示权限不足、404 统一为"不存在或不可见"……）。
6. **筛选不走 DataGrid 的 `filterModel`**：后端没有对应的表达式协议，筛选由页面自己维护；排序才走白名单表达式。
7. **字体自托管且只引 latin / latin-ext 子集**：运行时零外网请求；不分子集会多打进 50 个字体文件。
8. **甘特图依赖暂缓引入**：`frappe-gantt` 不带类型声明，骨架期没有真实拖拽交互可验证，到甘特图功能开始时装（F04 D3）。
9. **模板副本只作留档**：`src/` 是权威代码；`.templates/`（**已入库**，见其中的 `README.md`）仅用于按官方 tag 逐文件 diff 升级，换机器时不必再去官方仓库取。
10. **表格单元格里的内容有硬约束**：文本用 `CellText`（行内 `<span>`，否则会被顶到单元格顶部）、
    "进度条 + 文字"用 `ProgressCell`（块级容器要自己撑满单元格）、悬浮提示用 `DataTooltip`/`textCell`
    （**写了 `renderCell` 才会去掉 DataGrid 自动加的原生 `title`**）。三条都来自实测踩坑，见 F01 §6 第 22、28、34、35 条。
11. **筛选区版式收在 `components/common/filterLayout.ts`**：行容器用 `Box` + `flex/gap`（不要用 `Stack` 的 `spacing`，
    折行后左边缘会错位）；需要"每行铺满"的那一层用 `filterItemSx(min, max)` 的可变宽度。
    项目列表、计划视图、提醒列表、审计共用同一份实现（F01 §5.10 第 8、9 条）。
12. **计划视图是第二类页面**（只读、不分页、按项目分组）：照 `F01 §5.10` 的 9 条抄，
    不要套列表页那套分页/排序状态机；概览页也复用同一份数据（`usePlanView` + `PlanSummaryTiles`），
    因此概览与计划视图永远不会互相矛盾。
13. **开发代理目标不硬编码**：`vite.config.ts` 从 `API_PROXY_TARGET` 读取（默认 `http://127.0.0.1:8000`），
    本机端口被占用时在 `.env.development.local` 覆盖即可。它**刻意不加 `VITE_` 前缀**，
    避免内部地址被暴露给浏览器或进入构建产物（F03 §3/§4）。
14. **附件面板不引入第二张 DataGrid**：项目详情与任务抽屉共用 `components/attachment/AttachmentPanel.tsx`，
    沿用成员面板的"列表 + 动作"结构，轻量分页 10 条/页；逻辑删除与彻底删除保持**两步**，
    上传 / 下载 / 删除按钮按 `canWriteProjectContent` / `canEdit` 提前禁用（A07、F05 §5.16）。
15. **管理域拆成"两个列表 + 一个详情抽屉 + 三个表单"**：用户/工作组列表复用 §5.9；工作组详情用
    `?groupId=` 抽屉内的成员/加入申请标签页；用户编辑与组编辑复用 `useVersionedForm`，成员软移除/恢复
    与加入申请审核都是独立动作。组更新响应不带调用者成员投影，保存后必须重新 GET（F01 §6 第 37 条、F04 C5）。

## 已知项

- 首屏（登录页）实测传输约 **249 kB（gzip）**；项目列表/详情页各约 **444 kB**——表格与日期组件随路由按需加载。原始 JS 约 1.7 MB，**部署时必须开 gzip/brotli**（F04 D23/D25、F03 §8）；
- 顶栏的颜色模式是"图标切换亮/暗"，**没有回到"跟随系统"的入口**（初始默认仍是 System）；
- 中文由系统字体栈回退渲染（Roboto 不含中文字形），中文字体观感取决于客户端系统；
- 表格横向会有少量溢出：项目列表约 15px、提醒列表约 23px（`scrollWidth - clientWidth`），截图里看不到滚动条轨道，属 DataGrid flex 列 + 滚动条的既有行为；
- 单元格悬浮提示的"白底黑字"是**有意固定**的（贴近原生 title 观感），因此在**暗色模式下会有一块亮色浮层**；要跟随主题只改 `DataTooltip` 里的 `dataTooltipSlotProps` 一处（F04 D33）；
- 计划视图**不提供排序**，且"状态"筛选只作用于**当前页**（接口没有 `status` 参数），界面上已明示范围（F01 §5.10、F04 D31）；
- 本机 8000 若落在 Windows 保留端口区间，开发代理可用 `API_PROXY_TARGET` 覆盖（默认仍是 8000，F03 §4）；**不要改 `vite.config.ts` 的默认值**；
- 审计 `changes` 的自由文本字段可能显示 `<由请求提供>`（服务端不把描述类正文写进记录），详情抽屉里已解释，见 F04 C4；
- 附件面板默认每页 10 条；逻辑删除只改可见性、**不释放配额**，要释放必须再执行"彻底删除"；文件大小上限与扩展名白名单以服务端 `storage.*` 配置为准（A07、F05 §5.16）；
- 用户管理只提供 `include_disabled` / 分页 / 排序（A02 没有文本筛选）；工作组成员与加入申请面板一次加载前 100 条，超过时界面会提示（F05 §7.3 第 18 条）；
- 审计的时间筛选用与计划页同风格的 `DateTimePicker`，保留到分钟的精度（A09 仍接收 RFC3339）；
- 未实现页面会明确标注"骨架期占位"并列出计划对接的接口，不伪装成已实现（《S02》§6 第 4 条的同一条原则）。

完整清单与处理时机见 [F05 §7.3](./docs/F05-进度与验证记录.md)。

## 许可

内部项目，未声明开源许可。
