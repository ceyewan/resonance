import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { fileURLToPath } from "node:url";
import { dirname } from "node:path";

const __dirname = dirname(fileURLToPath(import.meta.url));

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    // src/gen 是指向 api/gen/ts 的软链接，preserveSymlinks 防止 Rollup
    // 将路径展开到真实目录（否则 web/node_modules 的依赖无法被解析）
    preserveSymlinks: true,
    alias: {
      "@gen": `${__dirname}/src/gen`,
    },
  },
  server: {
    port: 5173,
    fs: { strict: false },
  },
  optimizeDeps: {
    exclude: ["@bufbuild/protobuf"],
  },
});
