/**
 * 日期组件的本地化容器（`LocalizationProvider` + dayjs 适配器 + 中文语言包）。
 *
 * **为什么不做成 App 顶层的全局 Provider**：日期组件只在"项目表单"这类懒加载页面里用得到，
 * 而把它挂在 `App.tsx` 会让**登录页首屏**也下载整套日期组件与 dayjs——
 * 实测首屏因此多出 **91.7 kB（gzip）**。所以这里把它做成一个可包裹组件，
 * **谁需要日期组件，谁在自己的（懒加载）子树里包一层**（F04 D25）。
 *
 * 挂载位置不受弹层影响：MUI 的日期面板虽然 portal 到 `body`，
 * 但它读的是 React 树上的 context，因此包在对话框内部同样生效。
 */

import * as React from 'react';
import 'dayjs/locale/zh-cn';

import { AdapterDayjs } from '@mui/x-date-pickers/AdapterDayjs';
import { LocalizationProvider } from '@mui/x-date-pickers/LocalizationProvider';
import { zhCN } from '@mui/x-date-pickers/locales';

export interface DateLocalizationProviderProps {
  children: React.ReactNode;
}

export default function DateLocalizationProvider({ children }: DateLocalizationProviderProps) {
  return (
    <LocalizationProvider
      dateAdapter={AdapterDayjs}
      // 面板的中文（周首日、月份名）与输入框/工具栏文案
      adapterLocale="zh-cn"
      localeText={zhCN.components.MuiLocalizationProvider.defaultProps.localeText}
    >
      {children}
    </LocalizationProvider>
  );
}
