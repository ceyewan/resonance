import { cn } from "@/lib/cn";
import type { SessionInfo } from "@/stores/session";
import { TIME_FORMAT, DEFAULTS } from "@/constants";

interface SessionItemProps {
  session: SessionInfo;
  isActive: boolean;
  onClick: () => void;
}

/**
 * 会话列表项组件
 * Telegram 风格
 */
export function SessionItem({ session, isActive, onClick }: SessionItemProps) {
  // 获取显示名称
  const displayName = session.name || DEFAULTS.GROUP_NAME;

  // 获取头像首字母
  const getAvatarInitial = (name: string) => {
    return name?.charAt(0)?.toUpperCase() || "?";
  };

  // 格式化最后消息时间
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

  // 格式化最后消息内容
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
      onClick={onClick}
      className={cn(
        "flex cursor-pointer items-center gap-3 px-4 py-3 transition-colors",
        isActive
          ? "bg-sky-500 dark:bg-sky-600"
          : "hover:bg-gray-100 dark:hover:bg-gray-800",
      )}
    >
      {/* 头像 */}
      <div
        className={cn(
          "flex h-12 w-12 flex-shrink-0 items-center justify-center rounded-full text-lg font-semibold text-white",
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
              "truncate text-sm font-semibold",
              isActive
                ? "text-white"
                : "text-gray-900 dark:text-gray-100",
            )}
          >
            {displayName}
          </h3>
          <div className="flex items-center gap-2">
            {session.lastMessage?.timestamp ? (
              <span
                className={cn(
                  "text-xs",
                  isActive
                    ? "text-sky-100"
                    : "text-gray-500 dark:text-gray-400",
                )}
              >
                {formatTime(session.lastMessage.timestamp)}
              </span>
            ) : null}
            {session.unreadCount > 0 ? (
              <span
                className={cn(
                  "flex h-5 min-w-5 items-center justify-center rounded-full px-1.5 text-xs font-semibold",
                  isActive
                    ? "bg-white text-sky-500"
                    : "bg-sky-500 text-white",
                )}
              >
                {session.unreadCount > 99 ? "99+" : session.unreadCount}
              </span>
            ) : null}
          </div>
        </div>
        <p
          className={cn(
            "mt-0.5 truncate text-sm",
            isActive
              ? "text-sky-100"
              : "text-gray-500 dark:text-gray-400",
          )}
        >
          {formatLastMessage(
            session.lastMessage?.content,
            session.lastMessage?.type,
          )}
        </p>
      </div>
    </div>
  );
}

// 根据名称生成头像颜色
function getAvatarColor(name: string): string {
  const colors = [
    "bg-red-500",
    "bg-orange-500",
    "bg-amber-500",
    "bg-green-500",
    "bg-emerald-500",
    "bg-teal-500",
    "bg-cyan-500",
    "bg-sky-500",
    "bg-blue-500",
    "bg-indigo-500",
    "bg-violet-500",
    "bg-purple-500",
    "bg-fuchsia-500",
    "bg-pink-500",
    "bg-rose-500",
  ];

  let hash = 0;
  for (let i = 0; i < name.length; i++) {
    hash = name.charCodeAt(i) + ((hash << 5) - hash);
  }

  const index = Math.abs(hash) % colors.length;
  return colors[index];
}
