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
      // 控制台只有一个入口；升级提示与更新页由外链控制台统一承载。
      input: {
        main: "index.html",
      },
    },
  },
  plugins: [wails("./bindings")],
});
