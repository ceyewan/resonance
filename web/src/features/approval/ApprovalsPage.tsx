import { useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { Check, ChevronDown, RefreshCw, ShieldCheck, X } from "lucide-react";

import { GlassCard } from "../../components/GlassCard";
import { WallpaperBackground } from "../../components/WallpaperBackground";
import { useApprovals } from "../../hooks/useApprovals";
import { useAuthGuard } from "../../hooks/useAuthGuard";
import {
  AgentApprovalDecision,
  AgentApprovalStatus,
  type AgentApproval,
} from "../../services/approval";

const statusOptions = [
  AgentApprovalStatus.PENDING,
  AgentApprovalStatus.APPROVED,
  AgentApprovalStatus.REJECTED,
  AgentApprovalStatus.REVOKED,
  AgentApprovalStatus.EXPIRED,
  AgentApprovalStatus.UNSPECIFIED,
];

export function ApprovalsPage() {
  const auth = useAuthGuard();
  const navigate = useNavigate();
  const [status, setStatus] = useState(AgentApprovalStatus.PENDING);
  const [reasons, setReasons] = useState<Record<string, string>>({});
  const approvals = useApprovals(status);
  const isIAMAdminHint = auth.roles.includes("iam-admin");
  const canReadHint = isIAMAdminHint && auth.scopes.includes("agent:approval:read");
  const canDecideHint = isIAMAdminHint && auth.scopes.includes("agent:approval:decide");

  if (auth.bootstrapping) {
    return (
      <WallpaperBackground>
        <div className="flex h-screen items-center justify-center text-sm text-[var(--color-text-muted)] opacity-70">
          Loading...
        </div>
      </WallpaperBackground>
    );
  }

  return (
    <WallpaperBackground>
      <main className="mx-auto flex h-screen w-full max-w-[1180px] flex-col gap-4 p-4 md:p-6">
        <GlassCard className="shrink-0" padding="24px" cornerRadius={24} enableTilt={false}>
          <div className="flex flex-wrap items-start justify-between gap-4">
            <div>
              <h1 className="flex items-center gap-2 text-[26px] font-semibold text-[var(--color-text)]">
                <ShieldCheck className="h-6 w-6 text-[var(--color-primary)]" />
                Agent 审批中心
              </h1>
              <p className="mt-2 max-w-3xl text-[13px] text-[var(--color-text-muted)] opacity-80">
                页面仅展示脱敏参数摘要。租户、当前角色、Scope、过期状态与版本冲突均由服务端实时判定；本地权限信息只作为入口提示。
              </p>
              <p className="mt-2 text-[12px] text-[var(--color-text-muted)] opacity-70">
                登录提示：{canReadHint ? "可能可读取审批" : "可能缺少 approval read"}；
                {canDecideHint ? "可能可作出决定" : "可能缺少 approval decide"}
              </p>
            </div>
            <div className="flex gap-2">
              <button
                className="rounded-full border border-[var(--color-border)] bg-[var(--glass-surface)] px-4 py-2 text-sm text-[var(--color-text)] hover:bg-[var(--glass-surface-hover)] disabled:opacity-50"
                disabled={approvals.loading}
                onClick={() => void approvals.refresh()}
                type="button"
              >
                <RefreshCw className="mr-2 inline h-4 w-4" />
                刷新
              </button>
              <button
                className="rounded-full border border-[var(--color-border)] bg-[var(--glass-surface)] px-4 py-2 text-sm text-[var(--color-text)] hover:bg-[var(--glass-surface-hover)]"
                onClick={() => void navigate({ to: "/settings" })}
                type="button"
              >
                返回设置
              </button>
            </div>
          </div>

          <div className="mt-5 flex flex-wrap gap-2">
            {statusOptions.map((option) => (
              <button
                className={`rounded-full border px-4 py-2 text-[13px] transition-colors ${
                  status === option
                    ? "border-[var(--color-primary-border)] bg-[var(--color-primary)] text-[var(--color-text)]"
                    : "border-[var(--color-border)] bg-[var(--glass-surface)] text-[var(--color-text-muted)] hover:bg-[var(--glass-surface-hover)]"
                }`}
                key={option}
                onClick={() => setStatus(option)}
                type="button"
              >
                {statusLabel(option)}
              </button>
            ))}
          </div>
        </GlassCard>

        <GlassCard
          className="min-h-0 flex-1 overflow-hidden"
          padding="0"
          cornerRadius={24}
          enableTilt={false}
        >
          <div className="h-full overflow-y-auto p-4 md:p-6 custom-scrollbar">
            {approvals.error !== "" ? (
              <div className="mb-4 rounded-[16px] border border-red-400/30 bg-red-400/10 px-4 py-3 text-sm text-red-200">
                {approvals.error}
              </div>
            ) : null}
            {approvals.notice !== "" ? (
              <div className="mb-4 rounded-[16px] border border-green-400/30 bg-green-400/10 px-4 py-3 text-sm text-green-100">
                {approvals.notice}
              </div>
            ) : null}

            {approvals.loading ? (
              <p className="py-12 text-center text-sm text-[var(--color-text-muted)] opacity-70">
                正在加载审批...
              </p>
            ) : approvals.approvals.length === 0 ? (
              <p className="py-12 text-center text-sm text-[var(--color-text-muted)] opacity-70">
                当前筛选条件下没有可显示的审批。
              </p>
            ) : (
              <div className="space-y-4">
                {approvals.approvals.map((approval) => {
                  const locallyExpired = approval.expiresAtMs <= BigInt(Date.now());
                  const pending = approval.status === AgentApprovalStatus.PENDING;
                  const reason = reasons[approval.callId] ?? "";
                  return (
                    <article
                      className="rounded-[20px] border border-[var(--color-border)] bg-[var(--glass-surface)] p-5 shadow-sm"
                      key={approval.callId}
                    >
                      <div className="flex flex-wrap items-start justify-between gap-3">
                        <div>
                          <p className="text-[16px] font-semibold text-[var(--color-text)]">
                            {approval.toolName}
                          </p>
                          <p className="mt-1 text-[12px] text-[var(--color-text-muted)] opacity-70">
                            请求人 @{approval.requesterId} · Call {approval.callId}
                          </p>
                        </div>
                        <span
                          className={`rounded-full px-3 py-1 text-[12px] ${statusClass(approval, locallyExpired)}`}
                        >
                          {locallyExpired && approval.status === AgentApprovalStatus.PENDING
                            ? "可能已过期，待服务端确认"
                            : statusLabel(approval.status)}
                        </span>
                      </div>

                      <div className="mt-4 rounded-[14px] border border-[var(--color-border)] bg-[var(--glass-input-bg)] p-4">
                        <p className="text-[11px] uppercase tracking-[0.12em] text-[var(--color-text-muted)] opacity-70">
                          Args summary
                        </p>
                        <p className="mt-2 whitespace-pre-wrap break-words text-[14px] text-[var(--color-text)]">
                          {approval.argsSummary}
                        </p>
                      </div>

                      <dl className="mt-4 grid gap-2 text-[12px] text-[var(--color-text-muted)] md:grid-cols-2">
                        <div>
                          <dt className="opacity-60">Args hash</dt>
                          <dd className="mt-1 break-all font-mono text-[11px] text-[var(--color-text)] opacity-80">
                            {approval.argsHash}
                          </dd>
                        </div>
                        <div className="space-y-1">
                          <div>Version: {approval.version.toString()}</div>
                          <div>创建：{formatTime(approval.createdAtMs)}</div>
                          <div>过期：{formatTime(approval.expiresAtMs)}</div>
                        </div>
                      </dl>

                      {approval.decisionReason !== "" ? (
                        <p className="mt-3 text-[12px] text-[var(--color-text-muted)]">
                          决定理由：{approval.decisionReason}
                        </p>
                      ) : null}

                      {pending && canDecideHint ? (
                        <div className="mt-5 border-t border-[var(--color-border)] pt-4">
                          <label className="text-[12px] text-[var(--color-text-muted)]">
                            审批理由（最多 512 字节）
                            <textarea
                              className="mt-2 min-h-20 w-full resize-y rounded-[14px] border border-[var(--color-border)] bg-[var(--glass-input-bg)] p-3 text-[14px] text-[var(--color-text)] outline-none focus:bg-[var(--glass-input-focus-bg)]"
                              maxLength={512}
                              onChange={(event) =>
                                setReasons((current) => ({
                                  ...current,
                                  [approval.callId]: event.target.value,
                                }))
                              }
                              placeholder="记录批准或拒绝依据"
                              value={reason}
                            />
                          </label>
                          <div className="mt-3 flex flex-wrap gap-2">
                            <DecisionButton
                              approval={approval}
                              busy={approvals.decidingCallId === approval.callId}
                              decision={AgentApprovalDecision.APPROVE}
                              onDecide={() =>
                                void approvals.decide(
                                  approval,
                                  AgentApprovalDecision.APPROVE,
                                  reason,
                                )
                              }
                            />
                            <DecisionButton
                              approval={approval}
                              busy={approvals.decidingCallId === approval.callId}
                              decision={AgentApprovalDecision.REJECT}
                              onDecide={() =>
                                void approvals.decide(
                                  approval,
                                  AgentApprovalDecision.REJECT,
                                  reason,
                                )
                              }
                            />
                          </div>
                        </div>
                      ) : pending ? (
                        <p className="mt-4 text-[12px] text-amber-200/80">
                          本地登录信息未提示 approval decide 权限；服务端权限仍是最终事实。
                        </p>
                      ) : null}
                    </article>
                  );
                })}
              </div>
            )}

            {approvals.nextBeforeId > 0n ? (
              <div className="mt-6 text-center">
                <button
                  className="rounded-full border border-[var(--color-border)] bg-[var(--glass-surface)] px-5 py-2.5 text-sm text-[var(--color-text)] hover:bg-[var(--glass-surface-hover)] disabled:opacity-50"
                  disabled={approvals.loadingMore}
                  onClick={() => void approvals.loadMore()}
                  type="button"
                >
                  <ChevronDown className="mr-2 inline h-4 w-4" />
                  {approvals.loadingMore ? "加载中..." : "加载更多"}
                </button>
              </div>
            ) : null}
          </div>
        </GlassCard>
      </main>
    </WallpaperBackground>
  );
}

function DecisionButton(props: {
  approval: AgentApproval;
  decision: AgentApprovalDecision;
  busy: boolean;
  onDecide: () => void;
}) {
  const approve = props.decision === AgentApprovalDecision.APPROVE;
  return (
    <button
      aria-label={`${approve ? "Approve" : "Reject"} ${props.approval.callId}`}
      className={`rounded-full border px-5 py-2 text-[13px] font-medium disabled:opacity-50 ${
        approve
          ? "border-green-400/30 bg-green-400/10 text-green-100 hover:bg-green-400/20"
          : "border-red-400/30 bg-red-400/10 text-red-100 hover:bg-red-400/20"
      }`}
      disabled={props.busy}
      onClick={props.onDecide}
      type="button"
    >
      {approve ? (
        <Check className="mr-1.5 inline h-4 w-4" />
      ) : (
        <X className="mr-1.5 inline h-4 w-4" />
      )}
      {props.busy ? "提交中..." : approve ? "批准" : "拒绝"}
    </button>
  );
}

function statusLabel(status: AgentApprovalStatus): string {
  switch (status) {
    case AgentApprovalStatus.PENDING:
      return "待审批";
    case AgentApprovalStatus.APPROVED:
      return "已批准";
    case AgentApprovalStatus.REJECTED:
      return "已拒绝";
    case AgentApprovalStatus.REVOKED:
      return "已撤销";
    case AgentApprovalStatus.EXPIRED:
      return "已过期";
    default:
      return "全部";
  }
}

function statusClass(approval: AgentApproval, expired: boolean): string {
  if (expired && approval.status === AgentApprovalStatus.PENDING) {
    return "bg-amber-400/10 text-amber-100";
  }
  switch (approval.status) {
    case AgentApprovalStatus.PENDING:
      return "bg-blue-400/10 text-blue-100";
    case AgentApprovalStatus.APPROVED:
      return "bg-green-400/10 text-green-100";
    case AgentApprovalStatus.REJECTED:
      return "bg-red-400/10 text-red-100";
    default:
      return "bg-[var(--glass-input-bg)] text-[var(--color-text-muted)]";
  }
}

function formatTime(value: bigint): string {
  if (value <= 0n) {
    return "-";
  }
  return new Date(Number(value)).toLocaleString();
}
