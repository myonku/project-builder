/**
 * 前端入口脚本。
 *
 * 字体走 `@fontsource/roboto`（**自托管、零外网请求**，F03 §5.3）：
 * `index.html` 里不得出现指向 Google Fonts 的 `<link>`，否则内部网络部署会出现
 * "首屏拉不到字体 → 回退系统字体"的观感不一致与对外请求。
 *
 * 只引入 **latin / latin-ext** 两个子集：`@fontsource/roboto/400.css` 这种写法会把
 * cyrillic / greek / vietnamese / math / symbols 全部打进产物（多出数百 KB 无用字体）。
 * Roboto 本身不含中文字形，中文由系统字体栈回退渲染，这是字体选型时已知的取舍。
 */

import '@fontsource/roboto/latin-300.css';
import '@fontsource/roboto/latin-400.css';
import '@fontsource/roboto/latin-500.css';
import '@fontsource/roboto/latin-700.css';
import '@fontsource/roboto/latin-ext-300.css';
import '@fontsource/roboto/latin-ext-400.css';
import '@fontsource/roboto/latin-ext-500.css';
import '@fontsource/roboto/latin-ext-700.css';
import './styles/index.css';

import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import App from './App';

const container = document.getElementById('root');
if (!container) {
  throw new Error('缺少 #root 挂载点：请检查 index.html。');
}

createRoot(container).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
