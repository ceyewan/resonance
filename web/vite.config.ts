import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import { fileURLToPath } from "url";
import { dirname } from "path";

const __dirname = dirname(fileURLToPath(import.meta.url));

// https://vitejs.dev/config/
export default defineConfig({
  plugins: [react()],
  resolve: {
    // 生成的 Protobuf 代码通过软链接暴露在 src/gen 下，避免被解析到真实路径
    // （../api/gen/ts），否则 Rollup 无法在 web/node_modules 中找到依赖。
    preserveSymlinks: true,
    alias: {
      "@": `${__dirname}/src`,
    },
  },
  server: {
    port: 5173,
    open: true,
    fs: {
      // 允许访问软链接指向的目录
      strict: false,
    },
  },
  build: {
    outDir: "dist",
    sourcemap: true,
    commonjsOptions: {
      transformMixedEsModules: true,
    },
    rollupOptions: {
      onwarn(warning, warn) {
        // 忽略 "Use of eval" 警告（来自 Protobuf 生成代码）
        if (warning.code === "EVAL") return;
        // 忽略 "Circular dependency" 警告
        if (warning.code === "CIRCULAR_DEPENDENCY") return;
        warn(warning);
      },
    },
  },
  optimizeDeps: {
    exclude: ["@bufbuild/protobuf"],
  },
});
