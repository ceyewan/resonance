import { useCallback, useState } from "react";
import { useSession } from "@/hooks/useSession";
import { SessionItem } from "@/components/SessionItem";
import { NewChatModal } from "@/components/NewChatModal";

interface SessionSidebarProps {
  currentSessionId: string | null;
  onSelectSession: (sessionId: string) => void;
  onSessionCreated: (sessionId: string) => void;
  currentUsername?: string;
}

/**
 * 会话侧边栏
 */
export function SessionSidebar({
  currentSessionId,
  onSelectSession,
  onSessionCreated,
  currentUsername,
}: SessionSidebarProps) {
  const { sessions, isLoading, loadSessions } = useSession();
  const [isNewChatModalOpen, setIsNewChatModalOpen] = useState(false);

  // 处理会话创建后的回调
  const handleSessionCreated = useCallback(
    (sessionId: string) => {
      loadSessions();
      onSessionCreated(sessionId);
    },
    [loadSessions, onSessionCreated],
  );

  return (
    <aside className="lg-glass flex w-full shrink-0 flex-col overflow-hidden rounded-2xl md:w-80">
      {/* 搜索框和新建按钮 */}
      <div className="border-b border-white/35 p-3 dark:border-slate-200/10">
        <div className="flex items-center gap-2">
          <div className="relative flex-1">
            <svg
              className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-gray-400"
              fill="none"
              stroke="currentColor"
              viewBox="0 0 24 24"
            >
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
                d="M21 21l-6-6m2-5a7 7 0 11-14 0 7 7 0 0114 0z"
              />
            </svg>
            <input
              type="text"
              placeholder="搜索"
              disabled
              className="lg-input w-full rounded-full py-2 pl-10 pr-4 text-sm placeholder-slate-500 disabled:opacity-50"
            />
          </div>
          <button
            onClick={() => setIsNewChatModalOpen(true)}
            className="lg-btn-primary flex h-9 w-9 shrink-0 items-center justify-center rounded-full p-0 focus:outline-none focus:ring-2 focus:ring-sky-400/40 focus:ring-offset-2 focus:ring-offset-transparent"
            title="新建聊天"
            aria-label="新建聊天"
          >
            <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
                d="M12 4v16m8-8H4"
              />
            </svg>
          </button>
        </div>
      </div>

      {/* 会话列表 */}
      <div className="min-h-0 flex-1 overflow-y-auto">
        {isLoading ? (
          <div className="flex items-center justify-center p-8">
            <div className="flex items-center gap-2 text-slate-500 dark:text-slate-400">
              <svg className="h-5 w-5 animate-spin" fill="none" viewBox="0 0 24 24">
                <circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4" />
                <path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
              </svg>
              <span className="text-sm">加载中...</span>
            </div>
          </div>
        ) : sessions.length === 0 ? (
          <div className="flex flex-col items-center p-8 text-center">
            <svg className="mb-3 h-12 w-12 text-slate-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path
                strokeLinecap="round"
                strokeLinejoin="round"
                strokeWidth={2}
                d="M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z"
              />
            </svg>
            <p className="text-sm text-slate-500 dark:text-slate-400">暂无对话</p>
          </div>
        ) : (
          sessions.map((session) => (
            <SessionItem
              key={session.sessionId}
              session={session}
              isActive={currentSessionId === session.sessionId}
              onClick={() => onSelectSession(session.sessionId)}
            />
          ))
        )}
      </div>

      {/* 新建聊天弹窗 */}
      <NewChatModal
        isOpen={isNewChatModalOpen}
        onClose={() => setIsNewChatModalOpen(false)}
        onSessionCreated={handleSessionCreated}
        currentUsername={currentUsername}
      />
    </aside>
  );
}
