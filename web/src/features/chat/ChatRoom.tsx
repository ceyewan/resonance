import { FormEvent, useEffect, useMemo, useState, useRef } from "react";
import { useParams } from "@tanstack/react-router";
import { Loader2, SendHorizonal, AlertCircle, RefreshCw } from "lucide-react";

import { GlassCard } from "../../components/GlassCard";
import { useLoadHistory } from "../../hooks/useLoadHistory";
import { useSendMessage } from "../../hooks/useSendMessage";
import { useSessionListLive } from "../../hooks/useSessionListLive";
import { useSessionTimeline } from "../../hooks/useSessionTimeline";
import { SessionDetailPanel } from "../session-detail/SessionDetailPanel";
import { useAuthState } from "../../hooks/useAuthState";

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
  const auth = useAuthState();
  const scrollRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (sessionId) {
      void load(sessionId);
    }
  }, [load, sessionId]);

  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [timeline]);

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
      <GlassCard className="h-full flex flex-col w-full !p-0 relative overflow-hidden" padding="0" cornerRadius={24} enableTilt={false}>
        {/* Header */}
        <header className="px-5 py-3 border-b border-[var(--color-border)] flex items-center justify-between shrink-0 bg-[var(--glass-surface)] backdrop-blur-xl z-20">
          <div className="flex items-center gap-3">
            <div className="w-10 h-10 rounded-full bg-gradient-to-tr from-blue-500/30 to-purple-500/30 border border-[var(--color-border)] shadow-[inset_0_1px_2px_rgba(255,255,255,0.3)] flex items-center justify-center text-[var(--color-text)] font-medium text-base">
              {(session?.name || sessionId).charAt(0).toUpperCase()}
            </div>
            <div>
              <h2 className="text-[17px] font-semibold text-[var(--color-text)] tracking-tight leading-tight">
                {session?.name || sessionId}
              </h2>
              <p className="text-[13px] text-[var(--color-text-muted)] opacity-80 leading-tight mt-0.5">Online</p>
            </div>
          </div>
        </header>

        {/* Message List */}
        <div ref={scrollRef} className="flex-1 overflow-y-auto p-5 space-y-4 custom-scrollbar z-10 relative">
          {timeline.length === 0 ? (
            <div className="flex items-center justify-center h-full">
              <span className="px-4 py-1.5 rounded-full bg-[var(--glass-input-bg)] text-[var(--color-text-muted)] opacity-70 text-sm backdrop-blur-md">No messages yet</span>
            </div>
          ) : (
            timeline.map(({ event, sendState }) => {
              const isMe = event.fromUsername === auth.currentUser?.username || event.fromUsername === "" || !event.fromUsername;
              const bubbleClass = isMe 
                ? "bg-[var(--bubble-self)] text-[var(--color-text)] rounded-tr-sm"
                : "bg-[var(--bubble-other)] text-[var(--color-text)] rounded-tl-sm";

              return (
                <div key={`${event.sessionId}:${event.seqId}`} className={`flex flex-col w-full ${isMe ? 'items-end' : 'items-start'} group`}>
                  <div className={`flex items-end gap-2 max-w-[75%] ${isMe ? 'flex-row' : 'flex-row-reverse'}`}>
                    {/* Status Indicators for my messages */}
                    {isMe && sendState && (
                      <div className="flex flex-col items-center justify-center shrink-0 w-6 h-6 mb-1">
                        {sendState.status === "sending" && <Loader2 className="w-3.5 h-3.5 text-[var(--color-text-muted)] opacity-60 animate-spin" />}
                        {sendState.status === "retrying" && <RefreshCw className="w-3.5 h-3.5 text-[#f97316] animate-spin" />}
                        {sendState.status === "failed" && (
                          <button 
                            onClick={() => void retry(event.sessionId, event.clientMsgId)}
                            className="text-[#ef4444] hover:text-[#f87171] transition-colors"
                            title="Failed to send. Click to retry."
                          >
                            <AlertCircle className="w-4 h-4" />
                          </button>
                        )}
                      </div>
                    )}
                    
                    <div className={`px-4 py-2.5 rounded-[18px] relative overflow-hidden shadow-[0_4px_12px_rgba(0,0,0,0.1),inset_0_1px_1px_rgba(255,255,255,0.2)] border border-[var(--color-border)] backdrop-blur-md ${bubbleClass}`}>
                      {!isMe && event.fromUsername && (
                        <div className="text-[12px] font-medium text-[var(--color-primary)] opacity-90 mb-1">{event.fromUsername}</div>
                      )}
                      <div className="relative z-10 whitespace-pre-wrap break-words text-[15px] leading-relaxed">
                        {event.content || "[empty content]"}
                      </div>
                      <div className="relative z-10 text-[10px] mt-1.5 opacity-60 flex justify-end">
                        {new Date(Number(event.timestampMs)).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
                      </div>
                    </div>
                  </div>
                </div>
              );
            })
          )}
        </div>

        {/* Composer */}
        <form className="shrink-0 p-3 border-t border-[var(--color-border)] bg-[var(--glass-surface)] backdrop-blur-xl z-20 flex gap-3 items-end" onSubmit={(event) => void onSubmit(event)}>
          <div className="flex-1 relative bg-[var(--glass-input-bg)] rounded-[20px] border border-[var(--color-border)] focus-within:border-[var(--color-border)] focus-within:bg-[var(--glass-input-focus-bg)] transition-all">
            <textarea
              className="w-full bg-transparent px-4 py-3 text-[15px] text-[var(--color-text)] placeholder:text-[var(--color-text-muted)] opacity-60 outline-none resize-none max-h-[120px] min-h-[44px]"
              disabled={sending}
              onChange={(event) => setDraft(event.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !e.shiftKey) {
                  e.preventDefault();
                  void onSubmit(e);
                }
              }}
              placeholder="Message..."
              value={draft}
              rows={1}
            />
          </div>
          <button
            className="shrink-0 w-[44px] h-[44px] rounded-full bg-[var(--color-primary)] text-[var(--color-text)] flex items-center justify-center disabled:opacity-50 disabled:bg-[var(--glass-surface-hover)] disabled:text-[var(--color-text-muted)] opacity-60 transition-colors shadow-[0_4px_12px_rgba(0,0,0,0.15)]"
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
