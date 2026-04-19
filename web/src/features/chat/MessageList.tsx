import { useSessionTimeline } from "../../hooks/useSessionTimeline";
import { MessageBubble } from "./MessageBubble";
import { useEffect, useRef } from "react";

interface MessageListProps {
  sessionId: string;
}

export function MessageList({ sessionId }: MessageListProps) {
  const timeline = useSessionTimeline(sessionId) ?? [];
  const containerRef = useRef<HTMLDivElement>(null);

  // Auto scroll to bottom
  useEffect(() => {
    if (containerRef.current) {
      containerRef.current.scrollTop = containerRef.current.scrollHeight;
    }
  }, [timeline]);

  return (
    <div 
      ref={containerRef}
      className="absolute inset-0 overflow-y-auto px-6 py-6 flex flex-col gap-4 custom-scrollbar"
    >
      {timeline.map(({ event, sendState }) => (
        <MessageBubble 
          key={`${event.sessionId}:${event.seqId || event.clientMsgId}`} 
          event={event} 
          sendState={sendState} 
        />
      ))}
    </div>
  );
}
