import { cn } from "@/lib/cn";
import { getAvatarColor, getAvatarInitial } from "@/lib/avatar";
import type { ChatMessage } from "@/stores/message";
import { TIME_FORMAT } from "@/constants";

interface MessageBubbleProps {
  message: ChatMessage;
  isOwn: boolean;
  showAvatar?: boolean;
  senderName?: string;
  senderAvatarUrl?: string;
}

/**
 * 消息气泡组件
 * Liquid Glass 设计风格
 */
export function MessageBubble({
  message,
  isOwn,
  showAvatar = true,
  senderName,
  senderAvatarUrl,
}: MessageBubbleProps) {
  // 格式化时间
  const formatTime = (timestamp: bigint) => {
    // 后端发送的是秒级时间戳，需要转换为毫秒
    const date = new Date(Number(timestamp) * 1000);
    return date.toLocaleTimeString("zh-CN", TIME_FORMAT.MESSAGE_TIME as any);
  };

  // 头像颜色
  const avatarColor = getAvatarColor(senderName || "");

  // 消息状态图标
  const renderStatusIcon = () => {
    if (!isOwn) return null;

    switch (message.status) {
      case "sending":
        return (
          <svg className="h-4 w-4 animate-spin" fill="none" viewBox="0 0 24 24">
            <circle
              className="opacity-25"
              cx="12"
              cy="12"
              r="10"
              stroke="currentColor"
              strokeWidth="4"
            />
            <path
              className="opacity-75"
              fill="currentColor"
              d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
            />
          </svg>
        );
      case "failed":
        return (
          <svg className="h-4 w-4 text-red-400" fill="currentColor" viewBox="0 0 20 20">
            <path
              fillRule="evenodd"
              d="M10 18a8 8 0 100-16 8 8 0 000 16zM8.707 7.293a1 1 0 00-1.414 1.414L8.586 10l-1.293 1.293a1 1 0 101.414 1.414L10 11.414l1.293 1.293a1 1 0 001.414-1.414L11.414 10l1.293-1.293a1 1 0 00-1.414-1.414L10 8.586 8.707 7.293z"
              clipRule="evenodd"
            />
          </svg>
        );
      case "sent":
      default:
        return (
          <svg className="h-4 w-4 text-sky-200" fill="currentColor" viewBox="0 0 20 20">
            <path d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.707-9.293a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z" />
          </svg>
        );
    }
  };

  // 渲染消息内容
  const renderContent = () => {
    switch (message.msgType) {
      case "image":
        return (
          <div className="overflow-hidden rounded-lg">
            <img src={message.content} alt="图片" className="max-w-full rounded-lg" />
          </div>
        );
      case "file":
        return (
          <div className="flex items-center gap-3">
            <svg className="h-8 w-8" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
                d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z"
              />
            </svg>
            <span className="underline">{message.content}</span>
          </div>
        );
      case "audio":
        return (
          <div className="flex items-center gap-2">
            <svg className="h-6 w-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
                d="M19 11a7 7 0 01-7 7m0 0a7 7 0 01-7-7m7 7v4m0 0H8m4 0h4m-4-8a3 3 0 01-3-3V5a3 3 0 116 0v6a3 3 0 01-3 3z"
              />
            </svg>
            <span className="text-sm">语音消息</span>
          </div>
        );
      case "system":
        return (
          <div className="text-center text-sm text-gray-500 dark:text-gray-400">
            {message.content}
          </div>
        );
      default:
        return <p className="whitespace-pre-wrap break-words">{message.content}</p>;
    }
  };

  if (message.msgType === "system") {
    return (
      <div className="flex justify-center">
        <div className="lg-bubble-system rounded-full px-3 py-1 text-xs">
          {message.content}
        </div>
      </div>
    );
  }

  return (
    <div className={cn("flex gap-2", isOwn ? "flex-row-reverse" : "flex-row")}>
      {/* 头像 */}
      {showAvatar && !isOwn && (
        <div
          className={cn(
            "flex h-8 w-8 flex-shrink-0 items-center justify-center rounded-full text-sm font-medium text-white shadow-[0_8px_12px_-8px_rgba(15,23,42,0.75)]",
            senderAvatarUrl ? "bg-gray-300 dark:bg-gray-600" : avatarColor,
          )}
        >
          {senderAvatarUrl ? (
            <img
              src={senderAvatarUrl}
              alt={senderName}
              className="h-full w-full rounded-full object-cover"
            />
          ) : (
            getAvatarInitial(senderName || "")
          )}
        </div>
      )}

      {/* 消息内容 */}
      <div className={cn("flex max-w-[75%] flex-col", isOwn ? "items-end" : "items-start")}>
        {/* 发送者名称（仅群聊且非己方消息显示） */}
        {!isOwn && showAvatar && senderName && (
          <span className="mb-1 text-xs text-slate-500 dark:text-slate-400">{senderName}</span>
        )}

        {/* 气泡 */}
        <div
          className={cn(
            "rounded-2xl px-3 py-2 transition-all duration-300",
            isOwn ? "lg-bubble-own text-white" : "lg-bubble-other",
          )}
        >
          {renderContent()}
        </div>

        {/* 时间和状态 */}
        <div
          className={cn(
            "mt-0.5 flex items-center gap-1 text-xs",
            isOwn ? "text-sky-600 dark:text-sky-300" : "text-slate-400 dark:text-slate-500",
          )}
        >
          <span>{formatTime(message.timestamp)}</span>
          {isOwn && renderStatusIcon()}
        </div>
      </div>
    </div>
  );
}


