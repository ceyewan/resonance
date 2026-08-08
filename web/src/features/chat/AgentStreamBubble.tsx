import { Loader2 } from "lucide-react";
import { StreamFinishReason } from "@gen/gateway/v1/packet_pb";

import type { AgentStreamBubble as AgentStreamBubbleModel } from "../../stores/agentStream";

interface AgentStreamBubbleProps {
  stream: AgentStreamBubbleModel;
}

function endedStatus(reason: StreamFinishReason): string {
  switch (reason) {
    case StreamFinishReason.ERROR:
      return "生成中断";
    case StreamFinishReason.LENGTH:
      return "已达到长度限制";
    case StreamFinishReason.TOOL_CALL:
      return "正在处理后续步骤…";
    case StreamFinishReason.STOP:
    case StreamFinishReason.UNSPECIFIED:
      return "正在同步最终消息…";
    default:
      return "流式响应已结束";
  }
}

export function AgentStreamBubble({ stream }: AgentStreamBubbleProps) {
  const isStreaming = stream.status === "streaming";
  const statusText = isStreaming ? "正在生成…" : endedStatus(stream.finishReason);

  return (
    <div className="flex flex-col w-full items-start" aria-live="polite">
      <div className="flex items-end gap-2 max-w-[75%] flex-row-reverse">
        <div className="px-4 py-2.5 rounded-[18px] rounded-tl-sm relative overflow-hidden shadow-[0_4px_12px_rgba(0,0,0,0.1),inset_0_1px_1px_rgba(255,255,255,0.2)] border border-[var(--color-border)] backdrop-blur-md bg-[var(--bubble-other)] text-[var(--color-text)]">
          {stream.fromUsername ? (
            <div className="text-[12px] font-medium text-[var(--color-primary)] opacity-90 mb-1">
              {stream.fromUsername}
            </div>
          ) : null}
          {stream.content ? (
            <div className="relative z-10 whitespace-pre-wrap break-words text-[15px] leading-relaxed">
              {stream.content}
              {isStreaming ? <span className="inline-block ml-0.5 animate-pulse">▍</span> : null}
            </div>
          ) : null}
          <div className="relative z-10 mt-1.5 flex items-center gap-1.5 text-[10px] opacity-60">
            {isStreaming ? <Loader2 className="w-3 h-3 animate-spin" aria-hidden="true" /> : null}
            <span>{statusText}</span>
            <span>
              {new Date(stream.startedAtMs).toLocaleTimeString([], {
                hour: "2-digit",
                minute: "2-digit",
              })}
            </span>
          </div>
        </div>
      </div>
    </div>
  );
}
