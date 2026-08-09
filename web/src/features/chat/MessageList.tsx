import { useEffect, useMemo, useRef } from "react";

import { useAgentStreams } from "../../hooks/useAgentStreams";
import { useSessionListLive } from "../../hooks/useSessionListLive";
import { useSessionTimeline } from "../../hooks/useSessionTimeline";
import { AgentStreamBubble } from "./AgentStreamBubble";
import { MessageBubble } from "./MessageBubble";

interface MessageListProps {
  sessionId: string;
}

export function MessageList({ sessionId }: MessageListProps) {
  const timeline = useSessionTimeline(sessionId) ?? [];
  const agentStreams = useAgentStreams(sessionId);
  const sessions = useSessionListLive() ?? [];
  const session = useMemo(
    () => sessions.find((item) => item.sessionId === sessionId) ?? null,
    [sessionId, sessions],
  );
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (containerRef.current) {
      containerRef.current.scrollTop = containerRef.current.scrollHeight;
    }
  }, [agentStreams, timeline]);

  return (
    <div
      ref={containerRef}
      className="flex-1 overflow-y-auto p-5 space-y-4 custom-scrollbar z-10 relative"
    >
      {timeline.length === 0 && agentStreams.length === 0 ? (
        <div className="flex items-center justify-center h-full">
          <span className="px-4 py-1.5 rounded-full bg-[var(--glass-input-bg)] text-[var(--color-text-muted)] opacity-70 text-sm backdrop-blur-md">
            No messages yet
          </span>
        </div>
      ) : null}
      {timeline
        .filter(
          ({ event }) => event.payloadCase !== "recall" && event.payloadCase !== "readReceipt",
        )
        .map(({ event, sendState }) => (
          <MessageBubble
            key={`${event.sessionId}:${event.seqId || event.clientMsgId}`}
            event={event}
            sendState={sendState}
            session={session}
          />
        ))}
      {agentStreams.map((stream) => (
        <AgentStreamBubble key={stream.key} stream={stream} />
      ))}
    </div>
  );
}
