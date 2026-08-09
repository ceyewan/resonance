import { useCallback, useEffect, useState } from "react";
import { Code } from "@connectrpc/connect";

import {
  AgentApprovalDecision,
  AgentApprovalStatus,
  approvalErrorCode,
  approvalErrorMessage,
  decideApproval,
  listApprovals,
  type AgentApproval,
} from "../services/approval";

const PAGE_SIZE = 20;

export function useApprovals(status: AgentApprovalStatus) {
  const [approvals, setApprovals] = useState<AgentApproval[]>([]);
  const [nextBeforeId, setNextBeforeId] = useState(0n);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [decidingCallId, setDecidingCallId] = useState("");
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  const loadFirstPage = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const page = await listApprovals({ status, pageSize: PAGE_SIZE });
      setApprovals(page.approvals);
      setNextBeforeId(page.nextBeforeId);
    } catch (cause: unknown) {
      if (isAuthorizationLoss(cause)) {
        // Never leave a stale privileged list visible after a downgrade.
        setApprovals([]);
        setNextBeforeId(0n);
      }
      setError(approvalErrorMessage(cause));
    } finally {
      setLoading(false);
    }
  }, [status]);

  useEffect(() => {
    void loadFirstPage();
  }, [loadFirstPage]);

  const loadMore = useCallback(async () => {
    if (loadingMore || nextBeforeId <= 0n) {
      return;
    }
    setLoadingMore(true);
    setError("");
    try {
      const page = await listApprovals({
        status,
        beforeId: nextBeforeId,
        pageSize: PAGE_SIZE,
      });
      setApprovals((current) => {
        const seen = new Set(current.map((approval) => approval.callId));
        return [...current, ...page.approvals.filter((approval) => !seen.has(approval.callId))];
      });
      setNextBeforeId(page.nextBeforeId);
    } catch (cause: unknown) {
      if (isAuthorizationLoss(cause)) {
        setApprovals([]);
        setNextBeforeId(0n);
      }
      setError(approvalErrorMessage(cause));
    } finally {
      setLoadingMore(false);
    }
  }, [loadingMore, nextBeforeId, status]);

  const decide = useCallback(
    async (approval: AgentApproval, decision: AgentApprovalDecision, reason: string) => {
      setDecidingCallId(approval.callId);
      setError("");
      setNotice("");
      try {
        const result = await decideApproval({ approval, decision, reason });
        setApprovals((current) => {
          if (status !== AgentApprovalStatus.UNSPECIFIED && result.approval.status !== status) {
            return current.filter((item) => item.callId !== result.approval.callId);
          }
          return current.map((item) =>
            item.callId === result.approval.callId ? result.approval : item,
          );
        });
        setNotice(result.changed ? "审批决定已提交。" : "该审批已处理，已显示当前事实。");
      } catch (cause: unknown) {
        const code = approvalErrorCode(cause);
        if (code === Code.PermissionDenied || code === Code.Unauthenticated) {
          setApprovals([]);
          setNextBeforeId(0n);
        } else if (code === Code.Aborted || code === Code.FailedPrecondition) {
          await loadFirstPage();
        }
        setError(approvalErrorMessage(cause));
      } finally {
        setDecidingCallId("");
      }
    },
    [loadFirstPage, status],
  );

  return {
    approvals,
    nextBeforeId,
    loading,
    loadingMore,
    decidingCallId,
    error,
    notice,
    refresh: loadFirstPage,
    loadMore,
    decide,
  };
}

function isAuthorizationLoss(cause: unknown): boolean {
  const code = approvalErrorCode(cause);
  return code === Code.PermissionDenied || code === Code.Unauthenticated;
}
