import { createClient } from "@connectrpc/connect";
import { AuthService } from "@gen/gateway/v1/auth_pb";
import { AgentApprovalService } from "@gen/gateway/v1/agent_approval_pb";
import { SessionService } from "@gen/gateway/v1/session_pb";
import { transport } from "./transport";

export const authClient = createClient(AuthService, transport);
export const sessionClient = createClient(SessionService, transport);
export const approvalClient = createClient(AgentApprovalService, transport);
