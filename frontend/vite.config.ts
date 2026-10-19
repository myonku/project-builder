import { fileURLToPath } from 'node:url';
import react from '@vitejs/plugin-react';
import { defineConfig, loadEnv } from 'vite';

// 开发期前端 5173、后端默认 8000；用代理让浏览器眼里前后端同源（见 frontend/docs/F03 §4）。
//
// `API_PROXY_TARGET` 是**只给 Vite dev server 用**的 Node 侧配置，刻意不加 `VITE_` 前缀：
//  - `VITE_*` 会被 Vite 暴露给浏览器并可能进入构建产物，内部地址不应这样用（F03 §3）；
//  - 8000 被占用时，可在 `frontend/.env.development.local` 里覆盖为其它端口
//    （例如 `API_PROXY_TARGET=http://127.0.0.1:8001`），不必改代码，也不影响其它机器。
const DEFAULT_PROXY_TARGET = 'http://127.0.0.1:8000';

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '');
  const proxyTarget = env.API_PROXY_TARGET?.trim() || DEFAULT_PROXY_TARGET;

  return {
    plugins: [react()],
    resolve: {
      alias: {
        '@': fileURLToPath(new URL('./src', import.meta.url)),
      },
    },
    server: {
      port: 5173,
      proxy: {
        '/api': { target: proxyTarget, changeOrigin: true },
        '/healthz': { target: proxyTarget, changeOrigin: true },
      },
    },
    build: {
      outDir: 'dist',
      sourcemap: false,
    },
  };
});
