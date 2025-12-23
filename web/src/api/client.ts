import { createConnectTransport } from "@connectrpc/connect-web";
import { createPromiseClient } from "@connectrpc/connect";
import { AuthService, SessionService } from "@/gen/gateway/v1/api_connect";

const baseUrl = import.meta.env.VITE_API_BASE_URL || "http://localhost:8080";

export const transport = createConnectTransport({
  baseUrl,
});

export const authClient = createPromiseClient(AuthService, transport);
export const sessionClient = createPromiseClient(SessionService, transport);
