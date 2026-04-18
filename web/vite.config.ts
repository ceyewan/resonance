import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { resolve } from "path";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@gen": resolve(__dirname, "../api/gen/ts"),
    },
  },
  server: {
    port: 5173,
  },
});
