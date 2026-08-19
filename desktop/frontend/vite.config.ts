import { defineConfig } from "vite";
import wails from "@wailsio/runtime/plugins/vite";

// https://vitejs.dev/config/
export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  build: {
    rollupOptions: {
      // 多页入口。缺了这段，upgrade.html 不会被打进 dist，而 go:embed
      // all:frontend/dist 照样能编过——症状是窗口打开后 404 一片空白，
      // 且没有任何构建期报错
      input: {
        main: "index.html",
        upgrade: "upgrade.html",
      },
    },
  },
  plugins: [wails("./bindings")],
});
