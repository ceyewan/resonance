import { createConnectTransport } from "@connectrpc/connect-web";
import { createPromiseClient } from "@connectrpc/connect";
import { AuthService, SessionService } from "@/gen/gateway/v1/api_connect";
import { useAuthStore } from "@/stores/auth";

const baseUrl = import.meta.env.VITE_API_BASE_URL || "http://localhost:8080";

// 创建带 token 的 transport
export const transport = createConnectTransport({
  baseUrl,
  // 添加拦截器，在每个请求中携带 token
  interceptors: [
    (next) => async (req) => {
      const token = useAuthStore.getState().accessToken;
      if (token) {
        req.header.set("Authorization", token);
      }
      return await next(req);
    },
  ],
});

export const authClient = createPromiseClient(AuthService, transport);

// SessionService 需要 token，使用带认证的 transport
export const sessionClient = createPromiseClient(SessionService, transport);
