# MUI 官方模板副本（留档）

本目录是 **MUI material-ui `v9.5.0`** 官方模板的**原样副本**，自 2026-10-14 起**入库**。

## 为什么入库

模板代码要按项目改造（删社交登录、接自己的路由与主题），因此**不作为 npm 依赖引入**
（MUI 官方也是这个建议），而是复制源码进工程。复制之后有两件事需要它：

1. **逐文件 diff 升级**：MUI 升版本时，按 `docs/F01` §3.1 的对应关系表把本目录与 MUI 新 tag 的
   同名文件对比，而不是整体覆盖；
2. **换机器/多人协作**：没有本目录时，只能再去官方仓库下载一遍。本目录入库后，克隆仓库即可开始。

> **工程里生效的代码是 [`../src/`](../src/)**，不是这里。改这里不会改变应用行为，
> 还会让"来源可追溯"失效（`docs/F01` §6 第 16 条）。要在工程里改动模板代码，
> 请改 `src/` 下的对应文件，并在文件头的 `Modified:` 注释里写清改了什么。

## 内容与来源

| 目录 | 官方路径（tag `v9.5.0`） |
| --- | --- |
| `crud-dashboard/` | `docs/data/material/getting-started/templates/crud-dashboard/` |
| `shared-theme/` | `docs/data/material/getting-started/templates/shared-theme/` |
| `sign-in/` | `docs/data/material/getting-started/templates/sign-in/` |
| `sign-up/` | `docs/data/material/getting-started/templates/sign-up/` |

每个目录里 `.tsx` / `.ts` 是源码，`.js` 是官方同时提供的编译产物（留档齐全，便于对照）。

## 与 `src/` 的对应关系

见 [`../docs/F01-前端架构设计.md`](../docs/F01-前端架构设计.md) §3.1（哪些文件被复制到哪、哪些**不采用**），
以及 [`../docs/F04-设计偏离与差异登记.md`](../docs/F04-设计偏离与差异登记.md) §4（逐文件改造清单）。

关键点：**本项目已不再使用模板的定制主题层**（`shared-theme/themePrimitives.ts` 与两套
`customizations/`），全站统一为 MUI 默认的 Material Design 2（F04 D16）。这些文件留在本目录里
是为了"将来想恢复定制风格时能拿到原样代码"。

## 升级步骤

1. 从 MUI 仓库目标 tag 下载同名四个目录到**临时目录**；
2. 先与本目录 diff，确认上游改了什么；
3. 再按 `docs/F01` §3.1 的对应关系表，把**被采用**的文件与 `src/` 下的改造版逐文件 diff；
4. 更新本目录（覆盖为新的原样副本）与文件头的 `Source:` tag 注释；
5. 在 `docs/F04` 的变更记录里登记这次升级。
