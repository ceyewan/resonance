import { createClient } from "@connectrpc/connect";
import { AuthService } from "@gen/gateway/v1/auth_connect";
import { SessionService } from "@gen/gateway/v1/session_connect";
import { transport } from "./transport";

export const authClient = createClient(AuthService, transport);
export const sessionClient = createClient(SessionService, transport);
