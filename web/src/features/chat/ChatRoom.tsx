import { FormEvent, useEffect, useMemo, useState } from "react";
import { useParams } from "@tanstack/react-router";
import { Loader2, SendHorizonal } from "lucide-react";

import { GlassCard } from "../../components/GlassCard";
import { useLoadHistory } from "../../hooks/useLoadHistory";
import { useSendMessage } from "../../hooks/useSendMessage";
import { useSessionListLive } from "../../hooks/useSessionListLive";
import { useSessionTimeline } from "../../hooks/useSessionTimeline";
import { SessionDetailPanel } from "../session-detail/SessionDetailPanel";

export function ChatRoom() {
  const { sessionId } = useParams({ strict: false });
  const sessions = useSessionListLive() ?? [];
  const session = useMemo(
    () => sessions.find((item) => item.sessionId === sessionId) ?? null,
    [sessionId, sessions],
  );
  const timeline = useSessionTimeline(sessionId) ?? [];
  const { load } = useLoadHistory();
  const { send, retry } = useSendMessage();
  const [draft, setDraft] = useState("");
  const [sending, setSending] = useState(false);

  useEffect(() => {
    if (sessionId) {
      void load(sessionId);
    }
  }, [load, sessionId]);

  if (!sessionId) {
    return null;
  }

  const onSubmit = async (event: FormEvent) => {
    event.preventDefault();
    if (draft.trim() === "" || sending) {
      return;
    }

    setSending(true);
    try {
      await send({
        sessionId,
        content: draft,
      });
      setDraft("");
    } finally {
      setSending(false);
    }
  };

  return (
    <div className="grid h-full w-full gap-4 lg:grid-cols-[minmax(0,1fr)_320px] xl:grid-cols-[minmax(0,1fr)_360px]">
      <GlassCard className="h-full flex flex-col w-full !p-0" padding="0" cornerRadius={24} enableTilt={false}>
        <header className="px-6 py-4 border-b border-white/5 flex items-center justify-between shrink-0 bg-white/[0.02] backdrop-blur-md rounded-t-[24px]">
          <div className="flex items-center gap-3">
            <div className="w-10 h-10 rounded-full bg-gradient-to-tr from-blue-500/20 to-purple-500/20 border border-white/10 shadow-[inset_0_1px_1px_rgba(255,255,255,0.2)] flex items-center justify-center">
              <span className="text-white/80 font-medium text-sm">
                {(session?.name || sessionId).charAt(0).toUpperCase()}
              </span>
            </div>
            <div>
              <h2 className="text-lg font-medium text-white/90 tracking-tight leading-tight">
                {session?.name || sessionId}
              </h2>
              <p className="text-[13px] text-white/40 leading-tight">Timeline skeleton for UI layer</p>
            </div>
          </div>
        </header>

        <div className="flex-1 overflow-y-auto p-6 space-y-4 custom-scrollbar">
          {timeline.length === 0 ? (
            <p className="text-sm text-white/45">No messages yet.</p>
          ) : (
            timeline.map(({ event, sendState }) => (
              <article key={`${event.sessionId}:${event.seqId}`} className="rounded-[18px] border border-white/8 bg-white/[0.03] p-4 space-y-2">
                <div className="flex items-center justify-between gap-2 text-xs text-white/40">
                  <span>{event.fromUsername || "me(local)"}</span>
                  <span>{event.payloadCase}</span>
                </div>
                <p className="text-sm leading-relaxed text-white/88 whitespace-pre-wrap">{event.content || "[empty content]"}</p>
                {sendState !== null ? (
                  <div className="flex items-center justify-between gap-2 text-xs text-white/45">
                    <span>{sendState.status} / retry={sendState.retryCount}</span>
                    {sendState.status === "failed" ? (
                      <button
                        className="rounded-full border border-white/10 px-3 py-1 hover:bg-white/5"
                        onClick={() => {
                          void retry(event.sessionId, event.clientMsgId);
                        }}
                        type="button"
                      >
                        Retry
                      </button>
                    ) : null}
                  </div>
                ) : null}
              </article>
            ))
          )}
        </div>

        <form className="shrink-0 p-4 border-t border-white/5 bg-white/[0.01] rounded-b-[24px] flex gap-3 items-center" onSubmit={(event) => void onSubmit(event)}>
          <input
            className="min-w-0 flex-1 rounded-[16px] border border-white/10 bg-white/[0.03] px-4 py-3 text-sm text-white/85 placeholder:text-white/30 outline-none focus:border-white/20"
            disabled={sending}
            onChange={(event) => setDraft(event.target.value)}
            placeholder="Message..."
            value={draft}
          />
          <button
            className="shrink-0 w-11 h-11 rounded-full bg-[var(--color-primary)] text-white flex items-center justify-center disabled:opacity-50"
            disabled={draft.trim() === "" || sending}
            type="submit"
          >
            {sending ? <Loader2 className="w-5 h-5 animate-spin" /> : <SendHorizonal className="w-5 h-5 ml-[-2px]" />}
          </button>
        </form>
      </GlassCard>

      <aside className="hidden lg:block h-full">
        <SessionDetailPanel session={session} />
      </aside>
    </div>
  );
}
