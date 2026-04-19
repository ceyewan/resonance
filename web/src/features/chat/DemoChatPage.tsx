import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { appRuntime } from "../../app/runtime";
import { useAuthState } from "../../hooks/useAuthState";
import { useConnectionState } from "../../hooks/useConnectionState";
import { useLoadHistory } from "../../hooks/useLoadHistory";
import { useSendMessage } from "../../hooks/useSendMessage";
import { useSessionListLive } from "../../hooks/useSessionListLive";
import { useSessionTimeline } from "../../hooks/useSessionTimeline";
import { logout, restoreAuthSession } from "../../services/auth";

export function DemoChatPage() {
  const [draft, setDraft] = useState("");
  const [actionError, setActionError] = useState("");
  const [selectedSessionId, setSelectedSessionId] = useState("");
  const [busyAction, setBusyAction] = useState("");
  const restoredRef = useRef(false);
  const navigate = useNavigate();

  const auth = useAuthState();
  const connection = useConnectionState();
  const sessions = useSessionListLive() ?? [];
  const timeline = useSessionTimeline(selectedSessionId) ?? [];
  const { send, retry } = useSendMessage();
  const { load } = useLoadHistory();

  useEffect(() => {
    if (restoredRef.current) {
      return;
    }
    restoredRef.current = true;
    void restoreAuthSession().catch(() => {
      // Ignored here, we let the auth state handle redirects
    });
  }, []);

  // Redirect if completely unauthenticated
  useEffect(() => {
    if (!auth.authenticated && !auth.bootstrapping && !auth.bootstrapError) {
      void navigate({ to: "/login" });
    }
  }, [auth, navigate]);

  useEffect(() => {
    if (sessions.length === 0) {
      if (selectedSessionId !== "") {
        setSelectedSessionId("");
      }
      return;
    }

    const selectedStillExists = sessions.some((item) => item.sessionId === selectedSessionId);
    if (!selectedStillExists) {
      setSelectedSessionId(sessions[0]?.sessionId ?? "");
    }
  }, [selectedSessionId, sessions]);

  const selectedSession = useMemo(
    () => sessions.find((item) => item.sessionId === selectedSessionId) ?? null,
    [selectedSessionId, sessions],
  );

  const runAction = async (name: string, fn: () => Promise<void>) => {
    setBusyAction(name);
    setActionError("");
    try {
      await fn();
    } catch (cause: unknown) {
      setActionError(cause instanceof Error ? cause.message : `${name} 执行失败`);
    } finally {
      setBusyAction("");
    }
  };

  const handleLogout = async () => {
    await runAction("logout", logout);
    void navigate({ to: "/login" });
  };

  const onSend = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (selectedSessionId.trim() === "" || draft.trim() === "") {
      return;
    }

    await runAction("send", async () => {
      await send({
        sessionId: selectedSessionId,
        content: draft,
      });
      setDraft("");
    });
  };

  return (
    <main className="mx-auto flex min-h-screen w-full max-w-7xl flex-col gap-6 px-6 py-8">
      <section className="rounded-xl border border-[var(--glass-border)] bg-[var(--glass-surface)] p-6 shadow-sm backdrop-blur-[var(--glass-blur-md)]">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h1 className="text-xl font-semibold">Core Runtime Ready</h1>
            <p className="mt-1 text-sm text-[var(--color-text-muted)]">
              这里展示给 UI/UX 同学可直接复用的运行时状态，不承载最终交互设计。 (S6/S7 进行中)
            </p>
          </div>

          <div className="flex flex-wrap gap-2">
            <button
              className="rounded-md border border-[var(--color-border)] px-3 py-2 text-sm hover:bg-[var(--glass-surface-hover)]"
              onClick={() => void runAction("resync", async () => appRuntime.resync())}
              type="button"
            >
              {busyAction === "resync" ? "同步中..." : "手动同步 Inbox"}
            </button>
            <button
              className="rounded-md border border-[var(--color-border)] px-3 py-2 text-sm hover:bg-[var(--glass-surface-hover)]"
              onClick={() => appRuntime.reconnect()}
              type="button"
            >
              重新连接 WS
            </button>
            <button
              className="rounded-md border border-[var(--color-border)] px-3 py-2 text-sm hover:bg-[var(--glass-surface-hover)]"
              onClick={() => void handleLogout()}
              type="button"
            >
              {busyAction === "logout" ? "退出中..." : "退出登录"}
            </button>
          </div>
        </div>

        <dl className="mt-5 grid gap-3 text-sm md:grid-cols-2 xl:grid-cols-4">
          <div className="rounded-lg bg-black/5 dark:bg-white/5 p-3">
            <dt className="text-[var(--color-text-muted)]">当前用户</dt>
            <dd className="mt-1 font-medium">
              {auth.currentUser?.nickname || auth.currentUser?.username || "-"}
            </dd>
          </div>
          <div className="rounded-lg bg-black/5 dark:bg-white/5 p-3">
            <dt className="text-[var(--color-text-muted)]">连接状态</dt>
            <dd className="mt-1 font-medium">{connection.status}</dd>
          </div>
          <div className="rounded-lg bg-black/5 dark:bg-white/5 p-3">
            <dt className="text-[var(--color-text-muted)]">Inbox 同步</dt>
            <dd className="mt-1 font-medium">
              {connection.inboxSyncing ? "同步中" : "空闲"}
            </dd>
          </div>
          <div className="rounded-lg bg-black/5 dark:bg-white/5 p-3">
            <dt className="text-[var(--color-text-muted)]">会话数量</dt>
            <dd className="mt-1 font-medium">{sessions.length}</dd>
          </div>
        </dl>

        {connection.lastError ? (
          <p className="mt-4 text-sm text-red-500">连接错误: {connection.lastError}</p>
        ) : null}
        {connection.lastInboxSyncError ? (
          <p className="mt-2 text-sm text-red-500">
            Inbox 同步错误: {connection.lastInboxSyncError}
          </p>
        ) : null}
        {actionError ? <p className="mt-2 text-sm text-red-500">{actionError}</p> : null}
      </section>

      <section className="grid gap-6 lg:grid-cols-[320px_minmax(0,1fr)]">
        <aside className="rounded-xl border border-[var(--glass-border)] bg-[var(--glass-surface)] backdrop-blur-[var(--glass-blur-md)] p-4 shadow-sm">
          <div className="flex items-center justify-between gap-2">
            <h2 className="text-lg font-semibold">Session Snapshot</h2>
            <button
              className="rounded-md border border-[var(--color-border)] px-2 py-1 text-xs hover:bg-[var(--glass-surface-hover)]"
              disabled={selectedSessionId === "" || busyAction === "history"}
              onClick={() => {
                if (selectedSessionId === "") {
                  return;
                }
                void runAction("history", async () => {
                  await load(selectedSessionId);
                });
              }}
              type="button"
            >
              {busyAction === "history" ? "加载中..." : "拉最近历史"}
            </button>
          </div>

          <div className="mt-4 space-y-2">
            {sessions.length === 0 ? (
              <p className="text-sm text-[var(--color-text-muted)]">暂无会话，等待同步结果。</p>
            ) : (
              sessions.map((session) => (
                <button
                  className={`block w-full rounded-lg border px-3 py-3 text-left transition-colors ${
                    session.sessionId === selectedSessionId
                      ? "border-[var(--color-primary)] bg-[var(--color-primary)]/10"
                      : "border-[var(--glass-border)] hover:bg-[var(--glass-surface-hover)]"
                  }`}
                  key={session.sessionId}
                  onClick={() => setSelectedSessionId(session.sessionId)}
                  type="button"
                >
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-medium">{session.name || session.sessionId}</span>
                    <span className="text-xs text-[var(--color-text-muted)]">{session.unreadCount}</span>
                  </div>
                  <p className="mt-1 line-clamp-2 text-sm text-[var(--color-text-muted)]">
                    {session.lastEventPreview || "暂无预览"}
                  </p>
                </button>
              ))
            )}
          </div>
        </aside>

        <section className="rounded-xl border border-[var(--glass-border)] bg-[var(--glass-surface)] backdrop-blur-[var(--glass-blur-md)] p-4 shadow-sm flex flex-col h-[600px]">
          <div className="border-b border-[var(--color-border)] pb-4 shrink-0">
            <h2 className="text-lg font-semibold">
              {selectedSession?.name || selectedSession?.sessionId || "选择一个会话"}
            </h2>
            <p className="mt-1 text-sm text-[var(--color-text-muted)]">
              当前 timeline 直接来自 Dexie + outbox 状态聚合，可被正式 UI 复用。
            </p>
          </div>

          <div className="mt-4 space-y-3 flex-1 overflow-y-auto">
            {timeline.length === 0 ? (
              <p className="text-sm text-[var(--color-text-muted)]">暂无消息。</p>
            ) : (
              timeline.map(({ event, sendState }) => (
                <article className="rounded-lg border border-[var(--color-border)] p-3 bg-black/5 dark:bg-white/5" key={`${event.sessionId}:${event.seqId}`}>
                  <div className="flex items-center justify-between gap-2 text-xs text-[var(--color-text-muted)]">
                    <span>{event.fromUsername || "me(local)"}</span>
                    <span>{event.payloadCase}</span>
                  </div>
                  <p className="mt-2 whitespace-pre-wrap text-sm">{event.content || "[空内容]"}</p>
                  {sendState !== null ? (
                    <div className="mt-2 flex items-center justify-between gap-2 text-xs text-[var(--color-text-muted)]">
                      <span>{sendState.status} / retry={sendState.retryCount}</span>
                      {sendState.status === "failed" ? (
                        <button
                          className="rounded-md border border-[var(--color-border)] px-2 py-1"
                          onClick={() => {
                            void runAction("retry", async () => {
                              await retry(event.sessionId, event.clientMsgId);
                            });
                          }}
                          type="button"
                        >
                          重试
                        </button>
                      ) : null}
                    </div>
                  ) : null}
                </article>
              ))
            )}
          </div>

          <form className="mt-6 flex gap-3 border-t border-[var(--color-border)] pt-4 shrink-0" onSubmit={(event) => void onSend(event)}>
            <input
              className="min-w-0 flex-1 rounded-md border border-[var(--glass-border)] bg-[var(--glass-surface)] px-3 py-2 outline-none focus:ring-2 focus:ring-[var(--color-primary)] placeholder:text-[var(--color-text-muted)]"
              disabled={selectedSessionId === "" || busyAction === "send"}
              onChange={(event) => setDraft(event.target.value)}
              placeholder={selectedSessionId === "" ? "先选择会话" : "输入测试消息"}
              value={draft}
            />
            <button
              className="rounded-md bg-[var(--color-primary)] px-4 py-2 text-sm font-medium text-white disabled:opacity-60 hover:bg-[var(--color-primary-hover)] transition-colors"
              disabled={selectedSessionId === "" || draft.trim() === "" || busyAction === "send"}
              type="submit"
            >
              {busyAction === "send" ? "发送中..." : "发送"}
            </button>
          </form>
        </section>
      </section>
    </main>
  );
}
