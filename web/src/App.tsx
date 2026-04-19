import { FormEvent, useEffect, useMemo, useRef, useState } from "react";

import { appRuntime } from "./app/runtime";
import { useAuthState } from "./hooks/useAuthState";
import { useConnectionState } from "./hooks/useConnectionState";
import { useLoadHistory } from "./hooks/useLoadHistory";
import { useSendMessage } from "./hooks/useSendMessage";
import { useSessionListLive } from "./hooks/useSessionListLive";
import { useSessionTimeline } from "./hooks/useSessionTimeline";
import { login, logout, restoreAuthSession } from "./services/auth";

export default function App() {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [draft, setDraft] = useState("");
  const [authLoading, setAuthLoading] = useState(false);
  const [authError, setAuthError] = useState("");
  const [actionError, setActionError] = useState("");
  const [selectedSessionId, setSelectedSessionId] = useState("");
  const [busyAction, setBusyAction] = useState("");
  const restoredRef = useRef(false);

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
    void restoreAuthSession().catch((cause: unknown) => {
      setAuthError(cause instanceof Error ? cause.message : "恢复登录态失败");
    });
  }, []);

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

  const onSubmitLogin = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setAuthLoading(true);
    setAuthError("");

    try {
      await login(username, password);
      setPassword("");
    } catch (cause: unknown) {
      setAuthError(cause instanceof Error ? cause.message : "登录失败");
    } finally {
      setAuthLoading(false);
    }
  };

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

  if (!auth.authenticated) {
    return (
      <main className="mx-auto flex min-h-screen w-full max-w-xl items-center px-6">
        <section className="w-full rounded-xl border border-(--color-border) p-6 shadow-sm">
          <h1 className="text-xl font-semibold">Runtime Smoke Demo</h1>
          <p className="mt-1 text-sm text-(--color-text-muted)">
            该页面用于验证 auth/bootstrap/ws/inbox/outbox 主链路，不是最终 UI。
          </p>

          <form className="mt-5 space-y-4" onSubmit={(event) => void onSubmitLogin(event)}>
            <label className="block">
              <span className="mb-1 block text-sm">Username</span>
              <input
                autoComplete="username"
                className="w-full rounded-md border border-(--color-border) px-3 py-2 outline-none focus:ring-2 focus:ring-(--color-primary)"
                disabled={authLoading}
                onChange={(event) => setUsername(event.target.value)}
                placeholder="alice"
                value={username}
              />
            </label>

            <label className="block">
              <span className="mb-1 block text-sm">Password</span>
              <input
                autoComplete="current-password"
                className="w-full rounded-md border border-(--color-border) px-3 py-2 outline-none focus:ring-2 focus:ring-(--color-primary)"
                disabled={authLoading}
                onChange={(event) => setPassword(event.target.value)}
                placeholder="******"
                type="password"
                value={password}
              />
            </label>

            <button
              className="rounded-md bg-(--color-primary) px-4 py-2 text-sm font-medium text-white disabled:opacity-60"
              disabled={authLoading || !username.trim() || !password}
              type="submit"
            >
              {authLoading ? "登录中..." : "登录并启动 Runtime"}
            </button>
          </form>

          {auth.bootstrapping ? (
            <p className="mt-4 text-sm text-(--color-text-muted)">正在恢复登录态...</p>
          ) : null}
          {authError ? <p className="mt-4 text-sm text-red-600">{authError}</p> : null}
          {auth.bootstrapError ? (
            <p className="mt-4 text-sm text-red-600">{auth.bootstrapError}</p>
          ) : null}
        </section>
      </main>
    );
  }

  return (
    <main className="mx-auto flex min-h-screen w-full max-w-7xl flex-col gap-6 px-6 py-8">
      <section className="rounded-xl border border-(--color-border) p-6 shadow-sm">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h1 className="text-xl font-semibold">Core Runtime Ready</h1>
            <p className="mt-1 text-sm text-(--color-text-muted)">
              这里展示给 UI/UX 同学可直接复用的运行时状态，不承载最终交互设计。
            </p>
          </div>

          <div className="flex flex-wrap gap-2">
            <button
              className="rounded-md border border-(--color-border) px-3 py-2 text-sm"
              onClick={() => void runAction("resync", async () => appRuntime.resync())}
              type="button"
            >
              {busyAction === "resync" ? "同步中..." : "手动同步 Inbox"}
            </button>
            <button
              className="rounded-md border border-(--color-border) px-3 py-2 text-sm"
              onClick={() => appRuntime.reconnect()}
              type="button"
            >
              重新连接 WS
            </button>
            <button
              className="rounded-md border border-(--color-border) px-3 py-2 text-sm"
              onClick={() => void runAction("logout", logout)}
              type="button"
            >
              {busyAction === "logout" ? "退出中..." : "退出登录"}
            </button>
          </div>
        </div>

        <dl className="mt-5 grid gap-3 text-sm md:grid-cols-2 xl:grid-cols-4">
          <div className="rounded-lg bg-black/3 p-3">
            <dt className="text-(--color-text-muted)">当前用户</dt>
            <dd className="mt-1 font-medium">
              {auth.currentUser?.nickname || auth.currentUser?.username || "-"}
            </dd>
          </div>
          <div className="rounded-lg bg-black/3 p-3">
            <dt className="text-(--color-text-muted)">连接状态</dt>
            <dd className="mt-1 font-medium">{connection.status}</dd>
          </div>
          <div className="rounded-lg bg-black/3 p-3">
            <dt className="text-(--color-text-muted)">Inbox 同步</dt>
            <dd className="mt-1 font-medium">
              {connection.inboxSyncing ? "同步中" : "空闲"}
            </dd>
          </div>
          <div className="rounded-lg bg-black/3 p-3">
            <dt className="text-(--color-text-muted)">会话数量</dt>
            <dd className="mt-1 font-medium">{sessions.length}</dd>
          </div>
        </dl>

        {connection.lastError ? (
          <p className="mt-4 text-sm text-red-600">连接错误: {connection.lastError}</p>
        ) : null}
        {connection.lastInboxSyncError ? (
          <p className="mt-2 text-sm text-red-600">
            Inbox 同步错误: {connection.lastInboxSyncError}
          </p>
        ) : null}
        {actionError ? <p className="mt-2 text-sm text-red-600">{actionError}</p> : null}
      </section>

      <section className="grid gap-6 lg:grid-cols-[320px_minmax(0,1fr)]">
        <aside className="rounded-xl border border-(--color-border) p-4 shadow-sm">
          <div className="flex items-center justify-between gap-2">
            <h2 className="text-lg font-semibold">Session Snapshot</h2>
            <button
              className="rounded-md border border-(--color-border) px-2 py-1 text-xs"
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
              <p className="text-sm text-(--color-text-muted)">暂无会话，等待同步结果。</p>
            ) : (
              sessions.map((session) => (
                <button
                  className={`block w-full rounded-lg border px-3 py-3 text-left ${session.sessionId === selectedSessionId ? "border-(--color-primary) bg-(--color-primary)/10" : "border-(--color-border)"}`}
                  key={session.sessionId}
                  onClick={() => setSelectedSessionId(session.sessionId)}
                  type="button"
                >
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-medium">{session.name || session.sessionId}</span>
                    <span className="text-xs text-(--color-text-muted)">{session.unreadCount}</span>
                  </div>
                  <p className="mt-1 line-clamp-2 text-sm text-(--color-text-muted)">
                    {session.lastEventPreview || "暂无预览"}
                  </p>
                </button>
              ))
            )}
          </div>
        </aside>

        <section className="rounded-xl border border-(--color-border) p-4 shadow-sm">
          <div className="border-b border-(--color-border) pb-4">
            <h2 className="text-lg font-semibold">
              {selectedSession?.name || selectedSession?.sessionId || "选择一个会话"}
            </h2>
            <p className="mt-1 text-sm text-(--color-text-muted)">
              当前 timeline 直接来自 Dexie + outbox 状态聚合，可被正式 UI 复用。
            </p>
          </div>

          <div className="mt-4 space-y-3">
            {timeline.length === 0 ? (
              <p className="text-sm text-(--color-text-muted)">暂无消息。</p>
            ) : (
              timeline.map(({ event, sendState }) => (
                <article className="rounded-lg border border-(--color-border) p-3" key={`${event.sessionId}:${event.seqId}`}>
                  <div className="flex items-center justify-between gap-2 text-xs text-(--color-text-muted)">
                    <span>{event.fromUsername || "me(local)"}</span>
                    <span>{event.payloadCase}</span>
                  </div>
                  <p className="mt-2 whitespace-pre-wrap text-sm">{event.content || "[空内容]"}</p>
                  {sendState !== null ? (
                    <div className="mt-2 flex items-center justify-between gap-2 text-xs text-(--color-text-muted)">
                      <span>{sendState.status} / retry={sendState.retryCount}</span>
                      {sendState.status === "failed" ? (
                        <button
                          className="rounded-md border border-(--color-border) px-2 py-1"
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

          <form className="mt-6 flex gap-3 border-t border-(--color-border) pt-4" onSubmit={(event) => void onSend(event)}>
            <input
              className="min-w-0 flex-1 rounded-md border border-(--color-border) px-3 py-2 outline-none focus:ring-2 focus:ring-(--color-primary)"
              disabled={selectedSessionId === "" || busyAction === "send"}
              onChange={(event) => setDraft(event.target.value)}
              placeholder={selectedSessionId === "" ? "先选择会话" : "输入测试消息"}
              value={draft}
            />
            <button
              className="rounded-md bg-(--color-primary) px-4 py-2 text-sm font-medium text-white disabled:opacity-60"
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
