import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@gen": new URL("../api/gen/ts", import.meta.url).pathname,
    },
  },
  server: {
    port: 5173,
  },
});
