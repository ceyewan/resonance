/// <reference types="vite/client" />

/**
 * webserver 模块 /runtime-config.js 注入的全局运行时配置。
 * 对应后端环境变量：
 *   - RESONANCE_WEB_API_BASE_URL → apiBaseUrl（Gateway 对外 API base URL）
 *   - RESONANCE_WEB_WS_BASE_URL  → wsBaseUrl（Gateway WebSocket URL）
 *
 * 开发期 /runtime-config.js 由 Vite dev middleware 兜底返回空对象，
 * 此时 apiBaseUrl / wsBaseUrl 均为空串，依赖 vite.config.ts 里的 server.proxy。
 */
interface ResonanceRuntimeConfig {
  readonly apiBaseUrl?: string;
  readonly wsBaseUrl?: string;
}

interface Window {
  __RESONANCE_RUNTIME_CONFIG__?: ResonanceRuntimeConfig;
}
