/**
 * 全局主题：**MUI 默认的 Material Design 2 风格**。
 *
 * ## 为什么用 MD2 默认主题，而不是官方模板的 `themePrimitives`
 *
 * MUI 的默认主题本身就是 **Material Design 2**（`styles/createTypography.js` 的实现注释直接指向
 * `m2.material.io`，默认字体栈是 `"Roboto", "Helvetica", "Arial", sans-serif`，圆角 4px、
 * 25 级标准阴影、标准组件形态）。官方模板带的 `shared-theme/themePrimitives.ts` 是
 * **MUI 文档站自己的定制风格**：Inter 字体、8px 圆角、自定义阴影、自定义调色板，
 * 外加 10 组组件覆盖（inputs / dataDisplay / feedback / navigation / surfaces / dataGrid /
 * datePickers / formInput / sidebar / button）。
 *
 * 本项目统一采用 **MD2 默认风格**（F04 D16），因此：
 * - 不引入 `themePrimitives`，也不引入任何组件覆盖；
 * - 字体交给 MD2 的默认字体栈（`Roboto` 已由 `@fontsource/roboto` 自托管，见 F03 §5.3）；
 * - 需要"看起来像 MUI"的地方，直接拿默认值，不必逐项抄令牌。
 *
 * ## 本项目在 MD2 之上只做两件事
 *
 * 1. **打开亮/暗两套颜色方案**（`colorSchemes` + `cssVariables`），供顶栏的明暗切换使用；
 * 2. **把正文与控件的字号上调一档**（`typography`）：MD2 的默认阶梯偏紧凑
 *    （body1 16px / body2 14px / caption 12px），内部工具以桌面端长时间阅读为主，
 *    这里统一放大一到两像素。
 *
 * 组件里**不要**再各自写 `createTheme`，也不要各自放大字号（F01 §6 第 13 条）：
 * 主题只在本文档对应的这一个文件里创建，`App.tsx` 在最外层包裹。
 */

import * as React from 'react';
import { ThemeProvider, createTheme } from '@mui/material/styles';
import type { ThemeOptions } from '@mui/material/styles';

/**
 * Material Design 2 的字号阶梯（html 16px）与本项目取值的对照：
 *
 * | variant     | MD2 默认 | 本项目      | 用途                        |
 * | ----------- | -------- | ----------- | --------------------------- |
 * | `h4`        | 34px     | 34px（不变）| 页面标题（`PageContainer`） |
 * | `h5`        | 24px     | **28px**    | 卡片标题                    |
 * | `h6`        | 20px     | **24px**    | 分区标题                    |
 * | `subtitle1` | 16px     | **18px**    | 次级标题                    |
 * | `subtitle2` | 14px     | **16px**    | 小标题 / 字段名             |
 * | `body1`     | 16px     | **18px**    | 正文                        |
 * | `body2`     | 14px     | **16px**    | 密集行（列表、表格）        |
 * | `button`    | 14px     | **16px**    | 按钮                        |
 * | `caption`   | 12px     | **14px**    | 辅助说明                    |
 *
 * `createTypography` 对 `typography` 是**深合并**，所以这里只写 `fontSize`
 * 也能保留 MD2 的 `fontWeight` / `lineHeight` / `letterSpacing`。
 */
export const typographyScale: ThemeOptions['typography'] = {
  h5: { fontSize: '1.75rem' },
  h6: { fontSize: '1.5rem' },
  subtitle1: { fontSize: '1.125rem' },
  subtitle2: { fontSize: '1rem' },
  body1: { fontSize: '1.125rem' },
  body2: { fontSize: '1rem' },
  button: { fontSize: '1rem' },
  caption: { fontSize: '0.875rem' },
};

/**
 * MD2 默认主题 + 亮/暗颜色方案。
 *
 * `colorSchemeSelector: 'data-mui-color-scheme'`：MUI 把当前颜色方案写到 `<html>` 的属性上，
 * `useColorScheme()`（顶栏的明暗切换）与 `theme.getColorSchemeSelector('dark')` 都依赖它。
 * 不设 `cssVarPrefix`，用默认前缀 `mui`（定制模板用的 `template` 前缀随定制层一起移除）。
 */
export const themeOptions: ThemeOptions = {
  cssVariables: { colorSchemeSelector: 'data-mui-color-scheme' },
  colorSchemes: { light: true, dark: true },
  typography: typographyScale,
};

// 主题只创建一次：放在模块作用域，避免每次渲染重建
const theme = createTheme(themeOptions);

export interface AppThemeProps {
  children: React.ReactNode;
}

export default function AppTheme({ children }: AppThemeProps) {
  return (
    <ThemeProvider theme={theme} disableTransitionOnChange>
      {children}
    </ThemeProvider>
  );
}
