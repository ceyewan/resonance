import { cn } from "@/lib/cn";
import { getAvatarColor, getAvatarInitial } from "@/lib/avatar";
import type { SessionInfo } from "@/stores/session";
import { TIME_FORMAT, DEFAULTS } from "@/constants";

interface SessionItemProps {
  session: SessionInfo;
  isActive: boolean;
  onClick: () => void;
}

/**
 * 会话列表项组件
 * Liquid Glass T1 级实现 - 纯 CSS 模糊方案
 */
export function SessionItem({ session, isActive, onClick }: SessionItemProps) {
  const displayName = session.name || DEFAULTS.GROUP_NAME;

  const formatTime = (timestamp?: bigint) => {
    if (!timestamp) return "";
    const date = new Date(Number(timestamp) / 1000000);
    const now = new Date();
    const isToday = date.toDateString() === now.toDateString();

    if (isToday) {
      return date.toLocaleTimeString("zh-CN", TIME_FORMAT.MESSAGE_TIME as any);
    }
    return date.toLocaleDateString("zh-CN", TIME_FORMAT.SESSION_TIME as any);
  };

  const formatLastMessage = (content?: string, type?: string) => {
    if (!content) return "暂无消息";
    switch (type) {
      case "image":
        return "[图片]";
      case "file":
        return "[文件]";
      case "audio":
        return "[语音]";
      case "video":
        return "[视频]";
      default:
        return content;
    }
  };

  const avatarColor = getAvatarColor(displayName);

  return (
    <div
      role="button"
      tabIndex={0}
      onClick={onClick}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onClick();
        }
      }}
      aria-selected={isActive}
      aria-label={`会话: ${displayName}`}
      className={cn(
        "lg-session-item",
        isActive && "lg-session-item-active",
      )}
    >
      {/* 头像 */}
      <div
        className={cn(
          "flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-full text-lg font-semibold text-white shadow-[0_10px_14px_-10px_rgba(15,23,42,0.8)]",
          avatarColor,
        )}
      >
        {session.avatarUrl ? (
          <img
            src={session.avatarUrl}
            alt={displayName}
            className="h-full w-full rounded-full object-cover"
          />
        ) : (
          getAvatarInitial(displayName)
        )}
      </div>

      {/* 内容 */}
      <div className="min-w-0 flex-1">
        <div className="flex items-center justify-between">
          <h3
            className={cn(
              "lg-session-title truncate text-sm font-semibold transition-colors",
              isActive ? "text-white" : "text-slate-900 dark:text-slate-100",
            )}
          >
            {displayName}
          </h3>
          <div className="flex items-center gap-2">
            {session.lastMessage?.timestamp !== undefined && (
              <span
                className={cn(
                  "lg-session-meta text-xs transition-colors",
                  isActive ? "text-white/90" : "text-slate-500 dark:text-slate-400",
                )}
              >
                {formatTime(session.lastMessage.timestamp)}
              </span>
            )}
            {session.unreadCount > 0 && (
              <span
                className={cn(
                  "flex h-5 min-w-5 items-center justify-center rounded-full px-1.5 text-xs font-semibold shadow-sm",
                  isActive
                    ? "bg-white/95 text-sky-600 shadow-[0_4px_12px_-4px_rgba(255,255,255,0.5)]"
                    : "bg-sky-500 text-white shadow-[0_4px_12px_-4px_rgba(14,165,233,0.6)]",
                )}
              >
                {session.unreadCount > 99 ? "99+" : session.unreadCount}
              </span>
            )}
          </div>
        </div>
        <p
          className={cn(
            "lg-session-meta mt-0.5 truncate text-sm transition-colors",
            isActive ? "text-white/85" : "text-slate-500 dark:text-slate-400",
          )}
        >
          {formatLastMessage(session.lastMessage?.content, session.lastMessage?.type)}
        </p>
      </div>
    </div>
  );
}
