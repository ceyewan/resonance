import type { Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";

const ACCESS_TOKEN_KEY = "resonance_access_token";

export function getAccessToken(): string | null {
  return localStorage.getItem(ACCESS_TOKEN_KEY);
}

export function setAccessToken(token: string): void {
  localStorage.setItem(ACCESS_TOKEN_KEY, token);
}

export function clearAccessToken(): void {
  localStorage.removeItem(ACCESS_TOKEN_KEY);
}

const authInterceptor: Interceptor = (next) => async (req) => {
  const token = getAccessToken();
  if (token) {
    req.header.set("Authorization", `Bearer ${token}`);
  }
  return await next(req);
};

// Runtime config 由 webserver 模块的 /runtime-config.js 端点注入（生产）。
// 开发期同源 + Vite proxy，fallback 空串即可。
const apiBaseUrl = window.__RESONANCE_RUNTIME_CONFIG__?.apiBaseUrl?.trim() ?? "";

export const transport = createConnectTransport({
  baseUrl: apiBaseUrl,
  interceptors: [authInterceptor],
});
