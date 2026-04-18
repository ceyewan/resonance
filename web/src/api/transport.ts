import type { Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";

const AUTHORIZATION_HEADER = "Authorization";
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
    req.header.set(AUTHORIZATION_HEADER, `Bearer ${token}`);
  }
  return await next(req);
};

function resolveBaseUrl(): string {
  const fromEnv = (globalThis as { __RES_API_BASE_URL__?: string })
    .__RES_API_BASE_URL__;
  return fromEnv?.trim() ?? "";
}

export const transport = createConnectTransport({
  baseUrl: resolveBaseUrl(),
  interceptors: [authInterceptor],
});
