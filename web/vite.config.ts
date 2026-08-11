import { fileURLToPath, URL } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

// agentdTarget 是 dev server 反代的目标地址，用环境变量覆盖，默认本机 agentd。
//
// 为什么默认是 127.0.0.1:7777 而不是写死：agentd 的监听地址（cfg.Listen）与
// 端口都可由配置改变，反代地址必须是开发时可调的（AGENTD_URL=http://host:port
// npm run dev）。默认值覆盖「什么都没配」的常见场景。
const agentdTarget = process.env.AGENTD_URL ?? 'http://127.0.0.1:7777'

// 为什么反代不加 changeOrigin：agentd 的 Host 白名单（hostguard.go）与
// coder/websocket 的默认 Origin 校验（accept.go:239 的 r.Host == u.Host）都要求
// 反代把浏览器的 Host 原样转发——浏览器访问的是 localhost:5173，白名单里有
// localhost，Origin 校验也拿 localhost:5173 对比，两端都命中。若 changeOrigin
// 把 Host 改写成 127.0.0.1:7777，WS 握手的 Origin(localhost:5173) 与
// Host(127.0.0.1:7777) 不再相等，握手会 403 失败。
//
// /console 反代的闭环（鉴权全靠它）：浏览器打开 localhost:5173/console?ticket=…
// → vite 转给 agentd → agentd 原子消费 ticket、Set-Cookie（host-only、不按端口
// 隔离，落在 localhost）→ 302 到 / → 浏览器回到 5173 的 / 已是登录态，后续
// /api 与 /ws 请求都带上 cookie。
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  server: {
    proxy: {
      '/api': { target: agentdTarget },
      '/ws': { target: agentdTarget, ws: true },
      '/console': { target: agentdTarget },
    },
  },
  test: {
    environment: 'jsdom',
  },
})
