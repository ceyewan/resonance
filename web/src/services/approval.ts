import { Code, ConnectError } from "@connectrpc/connect";
import type { AgentApproval } from "@gen/gateway/v1/agent_approval_pb";
import { AgentApprovalDecision, AgentApprovalStatus } from "@gen/gateway/v1/agent_approval_pb";

import { approvalClient } from "../api/clients";

export type ApprovalPage = {
  approvals: AgentApproval[];
  nextBeforeId: bigint;
};

export type ApprovalDecisionResult = {
  approval: AgentApproval;
  changed: boolean;
};

export async function getApproval(callId: string): Promise<AgentApproval> {
  const normalizedCallId = requiredCallId(callId);
  const response = await approvalClient.getApproval({ callId: normalizedCallId });
  if (response.approval === undefined) {
    throw new Error("Approval response is missing");
  }
  return response.approval;
}

export async function listApprovals(input: {
  status: AgentApprovalStatus;
  beforeId?: bigint;
  pageSize?: number;
}): Promise<ApprovalPage> {
  const beforeId = input.beforeId ?? 0n;
  const pageSize = input.pageSize ?? 20;
  if (beforeId < 0n || pageSize < 1 || pageSize > 100) {
    throw new Error("Invalid approval page");
  }
  const response = await approvalClient.listApprovals({
    status: input.status,
    beforeId,
    pageSize,
  });
  return { approvals: response.approvals, nextBeforeId: response.nextBeforeId };
}

export async function decideApproval(input: {
  approval: AgentApproval;
  decision: AgentApprovalDecision;
  reason: string;
}): Promise<ApprovalDecisionResult> {
  const { approval, decision } = input;
  const callId = requiredCallId(approval.callId);
  if (!/^[0-9a-f]{64}$/.test(approval.argsHash)) {
    throw new Error("Approval binding hash is invalid");
  }
  if (approval.version <= 0n) {
    throw new Error("Approval version is invalid");
  }
  if (decision !== AgentApprovalDecision.APPROVE && decision !== AgentApprovalDecision.REJECT) {
    throw new Error("Approval decision is invalid");
  }
  const reason = input.reason.trim();
  if (new TextEncoder().encode(reason).byteLength > 512) {
    throw new Error("Decision reason must not exceed 512 bytes");
  }
  const response = await approvalClient.decideApproval({
    callId,
    argsHash: approval.argsHash,
    expectedVersion: approval.version,
    decision,
    reason,
  });
  if (response.approval === undefined) {
    throw new Error("Approval response is missing");
  }
  return { approval: response.approval, changed: response.changed };
}

export function approvalErrorCode(cause: unknown): Code {
  return ConnectError.from(cause).code;
}

export function approvalErrorMessage(cause: unknown): string {
  switch (approvalErrorCode(cause)) {
    case Code.Unauthenticated:
      return "登录状态已失效，请重新登录。";
    case Code.PermissionDenied:
      return "当前审批权限不足或已被撤销。";
    case Code.NotFound:
      return "审批不存在，或不属于当前租户。";
    case Code.FailedPrecondition:
      return "审批已过期或不再处于待审批状态，请刷新。";
    case Code.Aborted:
      return "审批已被其他管理员更新，请刷新后重试。";
    case Code.InvalidArgument:
      return "审批请求无效，请刷新后重试。";
    case Code.Unavailable:
      return "审批服务暂时不可用，请稍后重试。";
    default:
      return cause instanceof Error ? cause.message : "审批操作失败。";
  }
}

function requiredCallId(callId: string): string {
  const normalized = callId.trim();
  if (normalized === "" || normalized.length > 128) {
    throw new Error("Approval call ID is invalid");
  }
  return normalized;
}

export { AgentApprovalDecision, AgentApprovalStatus };
export type { AgentApproval };
