import { AlertCircle, Loader2, RefreshCw, Undo2 } from "lucide-react";
import { useCallback, useState } from "react";

import { type EventRow } from "../../db/schema";
import { useAuthState } from "../../hooks/useAuthState";
import { useSendMessage } from "../../hooks/useSendMessage";
import { toBigIntId } from "../../lib/id";
import { type OutboxStatusSummary } from "../../services/chat";

const RECALL_WINDOW_MS = 2 * 60 * 1000;

interface MessageBubbleProps {
  event: EventRow;
  sendState: OutboxStatusSummary | null;
}

export function MessageBubble({ event, sendState }: MessageBubbleProps) {
  const auth = useAuthState();
  const { retry, recall } = useSendMessage();
  const [menuOpen, setMenuOpen] = useState(false);

  const isMe =
    event.fromUsername === auth.currentUser?.username ||
    event.fromUsername === "" ||
    !event.fromUsername;

  const canRecall =
    isMe &&
    event.payloadCase === "message" &&
    !event.recalled &&
    event.eventId !== "0" &&
    Date.now() - Number(event.timestampMs) <= RECALL_WINDOW_MS;

  const handleRecall = useCallback(async () => {
    setMenuOpen(false);
    await recall(event.sessionId, toBigIntId(event.eventId));
  }, [recall, event.sessionId, event.eventId]);

  // 已撤回：统一显示灰色占位
  if (event.recalled) {
    return (
      <div className={`flex flex-col w-full ${isMe ? "items-end" : "items-start"}`}>
        <div className="px-4 py-2 rounded-[18px] bg-[var(--glass-input-bg)] border border-[var(--color-border)] text-[var(--color-text-muted)] text-[13px] italic opacity-70 select-none">
          {isMe ? "你撤回了一条消息" : `${event.fromUsername || "对方"} 撤回了一条消息`}
        </div>
      </div>
    );
  }

  const bubbleClass = isMe
    ? "bg-[var(--bubble-self)] text-[var(--color-text)] rounded-tr-sm"
    : "bg-[var(--bubble-other)] text-[var(--color-text)] rounded-tl-sm";

  return (
    <div
      className={`flex flex-col w-full ${isMe ? "items-end" : "items-start"} group`}
      onContextMenu={(e) => {
        if (canRecall) {
          e.preventDefault();
          setMenuOpen(true);
        }
      }}
      onBlur={() => setMenuOpen(false)}
      tabIndex={-1}
    >
      <div className={`flex items-end gap-2 max-w-[75%] ${isMe ? "flex-row" : "flex-row-reverse"}`}>
        {isMe && sendState ? (
          <div className="flex flex-col items-center justify-center shrink-0 w-6 h-6 mb-1">
            {sendState.status === "sending" ? (
              <Loader2 className="w-3.5 h-3.5 text-[var(--color-text-muted)] opacity-60 animate-spin" />
            ) : null}
            {sendState.status === "retrying" ? (
              <RefreshCw className="w-3.5 h-3.5 text-[#f97316] animate-spin" />
            ) : null}
            {sendState.status === "failed" ? (
              <button
                className="text-[#ef4444] hover:text-[#f87171] transition-colors"
                onClick={() => void retry(event.sessionId, event.clientMsgId)}
                title="Failed to send. Click to retry."
                type="button"
              >
                <AlertCircle className="w-4 h-4" />
              </button>
            ) : null}
          </div>
        ) : null}

        <div className="relative">
          <div
            className={`px-4 py-2.5 rounded-[18px] relative overflow-hidden shadow-[0_4px_12px_rgba(0,0,0,0.1),inset_0_1px_1px_rgba(255,255,255,0.2)] border border-[var(--color-border)] backdrop-blur-md ${bubbleClass}`}
          >
            {!isMe && event.fromUsername ? (
              <div className="text-[12px] font-medium text-[var(--color-primary)] opacity-90 mb-1">
                {event.fromUsername}
              </div>
            ) : null}
            <div className="relative z-10 whitespace-pre-wrap break-words text-[15px] leading-relaxed">
              {event.content || "[empty content]"}
            </div>
            <div className="relative z-10 text-[10px] mt-1.5 opacity-60 flex justify-end">
              {new Date(Number(event.timestampMs)).toLocaleTimeString([], {
                hour: "2-digit",
                minute: "2-digit",
              })}
            </div>
          </div>

          {/* 撤回菜单 */}
          {menuOpen ? (
            <div
              className={`absolute z-50 top-0 ${isMe ? "right-full mr-2" : "left-full ml-2"} bg-[var(--glass-bg)] border border-[var(--color-border)] rounded-xl shadow-xl backdrop-blur-md overflow-hidden`}
              onMouseLeave={() => setMenuOpen(false)}
            >
              <button
                type="button"
                className="flex items-center gap-2 px-4 py-2.5 text-sm text-[var(--color-text)] hover:bg-[var(--glass-input-bg)] w-full whitespace-nowrap transition-colors"
                onClick={() => void handleRecall()}
              >
                <Undo2 className="w-3.5 h-3.5 opacity-70" />
                撤回消息
              </button>
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}
