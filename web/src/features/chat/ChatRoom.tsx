import { useEffect, useMemo } from "react";
import { useParams } from "@tanstack/react-router";

import { GlassCard } from "../../components/GlassCard";
import { useAutoMarkRead } from "../../hooks/useAutoMarkRead";
import { useLoadHistory } from "../../hooks/useLoadHistory";
import { useSessionListLive } from "../../hooks/useSessionListLive";
import { useSessionTimeline } from "../../hooks/useSessionTimeline";
import { SessionDetailPanel } from "../session-detail/SessionDetailPanel";
import { Composer } from "./Composer";
import { MessageList } from "./MessageList";

export function ChatRoom() {
  const { sessionId } = useParams({ strict: false });
  const sessions = useSessionListLive() ?? [];
  const session = useMemo(
    () => sessions.find((item) => item.sessionId === sessionId) ?? null,
    [sessionId, sessions],
  );
  const timeline = useSessionTimeline(sessionId);
  const { load } = useLoadHistory();

  useEffect(() => {
    if (sessionId) {
      void load(sessionId);
    }
  }, [load, sessionId]);

  useAutoMarkRead(sessionId, timeline, session?.lastReadSeq);

  if (!sessionId) {
    return null;
  }

  return (
    <div className="grid h-full w-full gap-4 lg:grid-cols-[minmax(0,1fr)_320px] xl:grid-cols-[minmax(0,1fr)_360px]">
      <GlassCard
        className="h-full flex flex-col w-full !p-0 relative overflow-hidden"
        cornerRadius={24}
        enableTilt={false}
        padding="0"
      >
        <header className="px-5 py-3 border-b border-[var(--color-border)] flex items-center justify-between shrink-0 bg-[var(--glass-surface)] backdrop-blur-xl z-20">
          <div className="flex items-center gap-3">
            <div className="w-10 h-10 rounded-full bg-gradient-to-tr from-blue-500/30 to-purple-500/30 border border-[var(--color-border)] shadow-[inset_0_1px_2px_rgba(255,255,255,0.3)] flex items-center justify-center text-[var(--color-text)] font-medium text-base">
              {(session?.name || sessionId).charAt(0).toUpperCase()}
            </div>
            <div>
              <h2 className="text-[17px] font-semibold text-[var(--color-text)] tracking-tight leading-tight">
                {session?.name || sessionId}
              </h2>
              <p className="text-[13px] text-[var(--color-text-muted)] opacity-80 leading-tight mt-0.5">
                Online
              </p>
            </div>
          </div>
        </header>

        <MessageList sessionId={sessionId} />

        <Composer sessionId={sessionId} />
      </GlassCard>

      <aside className="hidden lg:block h-full">
        <SessionDetailPanel session={session} />
      </aside>
    </div>
  );
}
