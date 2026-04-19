import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath } from "node:url";
import { dirname } from "node:path";

const __dirname = dirname(fileURLToPath(import.meta.url));

// 开发期兜底：生产由 webserver 模块提供 /runtime-config.js。
// dev 返回空对象，前端 fallback 到空串 baseUrl + proxy，开发同源即可。
function devRuntimeConfigPlugin(): Plugin {
  return {
    name: "resonance-dev-runtime-config",
    configureServer(server) {
      server.middlewares.use("/runtime-config.js", (_req, res) => {
        res.setHeader("Content-Type", "application/javascript; charset=utf-8");
        res.setHeader("Cache-Control", "no-store");
        res.end("window.__RESONANCE_RUNTIME_CONFIG__ = {};\n");
      });
    },
  };
}

export default defineConfig({
  plugins: [react(), tailwindcss(), devRuntimeConfigPlugin()],
  resolve: {
    // src/gen 是指向 api/gen/ts 的软链接，preserveSymlinks 防止 Rollup
    // 将路径展开到真实目录（否则 web/node_modules 的依赖无法被解析）。
    preserveSymlinks: true,
    alias: {
      "@gen": `${__dirname}/src/gen`,
    },
  },
  server: {
    port: 5173,
    fs: { strict: false },
    // Dev 同源代理到本地 gateway：
    // - ConnectRPC：路径形如 /resonance.gateway.v1.AuthService/Login
    // - WebSocket：/ws
    proxy: {
      "^/resonance\\.": {
        target: "http://localhost:8080",
        changeOrigin: true,
      },
      "/ws": {
        target: "http://localhost:8080",
        changeOrigin: true,
        ws: true,
      },
    },
  },
});
