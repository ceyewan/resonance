import { type EventRow } from "../../db/schema";
import { type OutboxStatusSummary } from "../../services/chat";
import { useSendMessage } from "../../hooks/useSendMessage";
import { useAuthState } from "../../hooks/useAuthState";
import { Loader2, AlertCircle, RefreshCw } from "lucide-react";

interface MessageBubbleProps {
  event: EventRow;
  sendState: OutboxStatusSummary | null;
}

export function MessageBubble({ event, sendState }: MessageBubbleProps) {
  const auth = useAuthState();
  const { retry } = useSendMessage();
  
  const isMe = event.fromUsername === auth.currentUser?.username || event.fromUsername === "" || !event.fromUsername;

  const bubbleClass = isMe 
    ? "bg-[var(--color-primary)]/80 text-white ml-auto rounded-tr-sm shadow-[inset_0_1px_2px_rgba(255,255,255,0.4),0_4px_12px_rgba(0,0,0,0.15)] border border-white/20 backdrop-blur-md"
    : "bg-white/10 text-white/90 mr-auto rounded-tl-sm shadow-[inset_0_1px_1px_rgba(255,255,255,0.2),0_4px_12px_rgba(0,0,0,0.1)] border border-white/10 backdrop-blur-md";

  return (
    <div className={`flex flex-col w-full ${isMe ? 'items-end' : 'items-start'} group`}>
      <div className={`flex items-end gap-2 max-w-[75%] ${isMe ? 'flex-row' : 'flex-row-reverse'}`}>
        {/* Status Indicators for my messages */}
        {isMe && sendState && (
          <div className="flex flex-col items-center justify-center shrink-0 w-6 h-6 mb-1">
            {sendState.status === "sending" && <Loader2 className="w-3.5 h-3.5 text-white/40 animate-spin" />}
            {sendState.status === "retrying" && <RefreshCw className="w-3.5 h-3.5 text-orange-400 animate-spin" />}
            {sendState.status === "failed" && (
              <button 
                onClick={() => void retry(event.sessionId, event.clientMsgId)}
                className="text-red-400 hover:text-red-300 transition-colors"
                title="Failed to send. Click to retry."
              >
                <AlertCircle className="w-4 h-4" />
              </button>
            )}
          </div>
        )}
        
        <div className={`px-4 py-2.5 rounded-2xl relative overflow-hidden ${bubbleClass}`}>
          {/* Glass Specular Highlights */}
          <div className="absolute inset-0 pointer-events-none rounded-2xl bg-gradient-to-br from-white/10 to-transparent" />
          
          <div className="relative z-10 whitespace-pre-wrap break-words text-[15px] leading-relaxed">
            {event.content || "[Empty]"}
          </div>
          <div className="relative z-10 text-[10px] mt-1 opacity-50 flex justify-end">
            {new Date(Number(event.timestampMs)).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
          </div>
        </div>
      </div>
    </div>
  );
}
