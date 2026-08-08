import { Code, ConnectError } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import {
  AgentApprovalDecision,
  AgentApprovalSchema,
  AgentApprovalStatus,
} from "@gen/gateway/v1/agent_approval_pb";
import { beforeEach, describe, expect, test, vi } from "vitest";

const client = vi.hoisted(() => ({
  getApproval: vi.fn(),
  listApprovals: vi.fn(),
  decideApproval: vi.fn(),
}));

vi.mock("../api/clients", () => ({ approvalClient: client }));

import { approvalErrorMessage, decideApproval, getApproval, listApprovals } from "./approval";

function approval() {
  return create(AgentApprovalSchema, {
    id: 7n,
    callId: "call-7",
    runId: "run-7",
    toolName: "disable_tenant_user",
    requesterId: "requester",
    argsHash: "a".repeat(64),
    argsSummary: "Disable user bob",
    status: AgentApprovalStatus.PENDING,
    version: 3n,
    expiresAtMs: 1_900_000_000_000n,
  });
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("approval service", () => {
  test("列表请求只有筛选与游标，不允许页面提供 tenant", async () => {
    client.listApprovals.mockResolvedValue({ approvals: [approval()], nextBeforeId: 5n });

    const page = await listApprovals({
      status: AgentApprovalStatus.PENDING,
      beforeId: 20n,
      pageSize: 10,
    });

    expect(client.listApprovals).toHaveBeenCalledWith({
      status: AgentApprovalStatus.PENDING,
      beforeId: 20n,
      pageSize: 10,
    });
    const request = client.listApprovals.mock.calls[0]?.[0] as Record<string, unknown>;
    expect(request).not.toHaveProperty("tenantId");
    expect(request).not.toHaveProperty("tenant_id");
    expect(page.nextBeforeId).toBe(5n);
    expect(page.approvals[0]?.argsSummary).toBe("Disable user bob");
    expect(page.approvals[0]).not.toHaveProperty("frozenArgs");
  });

  test("决定请求绑定服务端返回的 args_hash、expected_version 与 reason", async () => {
    const current = approval();
    const decided = create(AgentApprovalSchema, {
      ...current,
      status: AgentApprovalStatus.APPROVED,
      decision: AgentApprovalDecision.APPROVE,
      version: 4n,
    });
    client.decideApproval.mockResolvedValue({ approval: decided, changed: true });

    const result = await decideApproval({
      approval: current,
      decision: AgentApprovalDecision.APPROVE,
      reason: "  reviewed  ",
    });

    expect(client.decideApproval).toHaveBeenCalledWith({
      callId: "call-7",
      argsHash: "a".repeat(64),
      expectedVersion: 3n,
      decision: AgentApprovalDecision.APPROVE,
      reason: "reviewed",
    });
    expect(result.changed).toBe(true);
    expect(result.approval.version).toBe(4n);
  });

  test("重复提交保留服务端 changed=false 的当前事实", async () => {
    const current = approval();
    client.decideApproval.mockResolvedValue({ approval: current, changed: false });

    const first = await decideApproval({
      approval: current,
      decision: AgentApprovalDecision.REJECT,
      reason: "duplicate",
    });
    const second = await decideApproval({
      approval: current,
      decision: AgentApprovalDecision.REJECT,
      reason: "duplicate",
    });

    expect(first.changed).toBe(false);
    expect(second.changed).toBe(false);
    expect(client.decideApproval).toHaveBeenCalledTimes(2);
  });

  test("Get 请求同样没有 tenant，并只返回脱敏 DTO", async () => {
    client.getApproval.mockResolvedValue({ approval: approval() });
    const result = await getApproval(" call-7 ");

    expect(client.getApproval).toHaveBeenCalledWith({ callId: "call-7" });
    expect(result.argsSummary).toBe("Disable user bob");
    expect(result).not.toHaveProperty("rawArgs");
  });

  test.each([
    [Code.PermissionDenied, "权限不足或已被撤销"],
    [Code.FailedPrecondition, "已过期或不再处于待审批状态"],
    [Code.Aborted, "其他管理员更新"],
  ])("将降权、过期与版本冲突转换为明确状态 (%s)", (code, message) => {
    expect(approvalErrorMessage(new ConnectError("server detail", code))).toContain(message);
  });
});
