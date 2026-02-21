import { useEffect, useCallback, useRef, useState } from "react";
import { useAuthStore } from "@/stores/auth";
import { useSession } from "@/hooks/useSession";
import { useMessageStore, createPendingMessage } from "@/stores/message";
import { WsPacket } from "@/gen/gateway/v1/packet_pb";
import { cn } from "@/lib/cn";
import { ConnectionStatus } from "@/components/ConnectionStatus";
import { SessionItem } from "@/components/SessionItem";
import { MessageBubble } from "@/components/MessageBubble";
import { ChatInput } from "@/components/ChatInput";
import { NewChatModal } from "@/components/NewChatModal";
import { MESSAGE_TYPES } from "@/constants";

interface ChatPageProps {
  isConnected: boolean;
  isConnecting?: boolean;
  send: (packet: WsPacket) => void;
}

/**
 * 聊天主页面
 * Liquid Glass 设计风格
 */
export default function ChatPage({ isConnected, isConnecting = false, send }: ChatPageProps) {
  const { user, logout } = useAuthStore();
  const { sessions, currentSession, isLoading, loadSessions, selectSession } = useSession();
  const { getSessionMessages } = useMessageStore();
  const messagesContainerRef = useRef<HTMLDivElement>(null);
  const [isNewChatModalOpen, setIsNewChatModalOpen] = useState(false);

  // 加载会话列表
  useEffect(() => {
    loadSessions();
  }, [loadSessions]);

  const messages = currentSession ? getSessionMessages(currentSession.sessionId) : [];
  const prevMessageCountRef = useRef(0);
  const prevSessionIdRef = useRef<string | null>(null);

  // 仅滚动消息容器，避免触发页面级滚动
  const scrollToBottom = useCallback((behavior: ScrollBehavior = "auto") => {
    const container = messagesContainerRef.current;
    if (!container) return;
    container.scrollTo({
      top: container.scrollHeight,
      behavior,
    });
  }, []);

  // 仅在会话切换或新增消息时滚动，避免因会话对象引用变化导致抖动
  useEffect(() => {
    const sessionId = currentSession?.sessionId ?? null;
    const currentCount = messages.length;
    const sessionChanged = sessionId !== prevSessionIdRef.current;
    const hasNewMessage = currentCount > prevMessageCountRef.current;

    if (sessionChanged || hasNewMessage) {
      scrollToBottom(sessionChanged ? "auto" : "smooth");
    }

    prevSessionIdRef.current = sessionId;
    prevMessageCountRef.current = currentCount;
  }, [currentSession?.sessionId, messages.length, scrollToBottom]);

  // 发送消息
  const handleSendMessage = useCallback(
    (content: string) => {
      if (!currentSession || !isConnected) return;

      // 创建待发送的消息对象（用于乐观更新）
      const pendingMessage = createPendingMessage(
        currentSession.sessionId,
        content,
        user?.username || "",
        MESSAGE_TYPES.TEXT,
      );

      // 立即添加到消息列表
      useMessageStore.getState().addMessage(pendingMessage);

      // 创建 WebSocket 消息包（使用 fromJsonString 来正确设置 oneof 字段）
      const packet = WsPacket.fromJsonString(
        JSON.stringify({
          seq: pendingMessage.msgId,
          chat: {
            sessionId: currentSession.sessionId,
            content,
            type: MESSAGE_TYPES.TEXT,
          },
        }),
      );

      send(packet);
      scrollToBottom("smooth");
    },
    [currentSession, isConnected, user, send, scrollToBottom],
  );

  // 处理会话选择
  const handleSelectSession = useCallback(
    (sessionId: string) => {
      selectSession(sessionId);
    },
    [selectSession],
  );

  // 处理会话创建后的回调
  const handleSessionCreated = useCallback(
    (sessionId: string) => {
      // 刷新会话列表并选中新建的会话
      loadSessions().then(() => {
        selectSession(sessionId);
      });
    },
    [loadSessions, selectSession],
  );

  return (
    <div className="relative flex h-full flex-col overflow-hidden px-3 pb-3 pt-3 md:px-4 md:pt-4">
      {/* 顶部导航栏 */}
      <header className="lg-glass-2 lg-glow-border sticky top-0 z-40 mb-3 flex h-14 shrink-0 items-center justify-between rounded-2xl border border-slate-300/70 px-4 dark:border-slate-600/45">
        <div className="flex items-center gap-3">
          {/* Logo */}
          <svg
            className="h-6 w-6 text-sky-500 drop-shadow-[0_10px_18px_rgba(2,132,199,0.42)]"
            fill="none"
            stroke="currentColor"
            viewBox="0 0 24 24"
          >
            <path
              strokeLinecap="round"
              strokeLinejoin="round"
              strokeWidth={2}
              d="M12 19l9 2-9-18-9 18 9-2zm0 0v-8"
            />
          </svg>
          <h1 className="text-lg font-semibold text-gray-900 dark:text-white">Resonance</h1>
          <ConnectionStatus isConnected={isConnected} isConnecting={isConnecting} />
        </div>

        <div className="flex items-center gap-3">
          {/* 用户信息 */}
          <span className="text-sm text-slate-600 dark:text-slate-300">
            {user?.nickname || user?.username}
          </span>

          {/* 登出按钮 */}
          <button
            onClick={logout}
            className={cn(
              "rounded-xl px-3 py-1.5 text-sm font-medium transition-all duration-200",
              "text-slate-600 hover:bg-white/45 hover:text-slate-900 hover:shadow-md",
              "dark:text-slate-300 dark:hover:bg-slate-700/55 dark:hover:text-white",
            )}
          >
            登出
          </button>
        </div>
      </header>

      {/* 主内容区 */}
      <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-hidden md:flex-row">
        {/* 左侧会话列表 */}
        <aside className="lg-glass-1 lg-glow-border flex w-full shrink-0 flex-col overflow-hidden rounded-2xl border border-slate-300/70 dark:border-slate-600/45 md:w-80">
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
                  className={cn(
                    "lg-input w-full rounded-full py-2 pl-10 pr-4 text-sm",
                    "placeholder-slate-500",
                    "disabled:opacity-50",
                  )}
                />
              </div>
              <button
                onClick={() => setIsNewChatModalOpen(true)}
                className={cn(
                  "lg-btn-primary flex h-9 w-9 shrink-0 items-center justify-center rounded-full p-0",
                  "focus:outline-none focus:ring-2 focus:ring-sky-400/40 focus:ring-offset-2 focus:ring-offset-transparent",
                )}
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
                  <span className="text-sm">加载中...</span>
                </div>
              </div>
            ) : sessions.length === 0 ? (
              <div className="flex flex-col items-center p-8 text-center">
                <svg
                  className="mb-3 h-12 w-12 text-slate-400"
                  fill="none"
                  stroke="currentColor"
                  viewBox="0 0 24 24"
                >
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
                  isActive={currentSession?.sessionId === session.sessionId}
                  onClick={() => handleSelectSession(session.sessionId)}
                />
              ))
            )}
          </div>
        </aside>

        {/* 右侧聊天区域 */}
        <main className="lg-glass-1 lg-glow-border flex min-h-0 flex-1 flex-col overflow-hidden rounded-2xl border border-slate-300/70 dark:border-slate-600/45">
          {!currentSession ? (
            // 空状态
            <div className="flex flex-1 flex-col items-center justify-center text-slate-500 dark:text-slate-400">
              <svg className="mb-4 h-16 w-16 opacity-60" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  strokeWidth={1.5}
                  d="M8 12h.01M12 12h.01M16 12h.01M21 12c0 4.418-4.03 8-9 8a9.863 9.863 0 01-4.255-.949L3 20l1.395-3.72C3.512 15.042 3 13.574 3 12c0-4.418 4.03-8 9-8s9 3.582 9 8z"
                />
              </svg>
              <p className="text-lg">选择一个对话开始聊天</p>
            </div>
          ) : (
            <>
              {/* 聊天头部 */}
              <div className="flex h-14 items-center justify-between border-b border-white/35 px-4 dark:border-slate-200/10">
                <div className="flex items-center gap-3">
                  {/* 头像 */}
                  <div className="flex h-9 w-9 items-center justify-center rounded-full bg-gradient-to-br from-sky-400 to-sky-600 text-sm font-semibold text-white shadow-[0_10px_16px_-10px_rgba(2,132,199,0.85)]">
                    {currentSession.name?.charAt(0)?.toUpperCase() || "?"}
                  </div>
                  <div>
                    <h2 className="text-sm font-semibold text-slate-900 dark:text-white">
                      {currentSession.name}
                    </h2>
                    <p className="text-xs text-slate-500 dark:text-slate-400">
                      {currentSession.type === 2 ? "群聊" : "单聊"}
                    </p>
                  </div>
                </div>

                {/* 更多操作 */}
                <button aria-label="更多操作" title="更多操作" className="rounded-full p-2 text-slate-500 transition-all duration-200 hover:bg-white/45 hover:text-slate-700 hover:shadow-sm dark:text-slate-300 dark:hover:bg-slate-700/55 dark:hover:text-slate-100">
                  <svg className="h-5 w-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path
                      strokeLinecap="round"
                      strokeLinejoin="round"
                      strokeWidth={2}
                      d="M12 5v.01M12 12v.01M12 19v.01M12 6a1 1 0 110-2 1 1 0 010 2zm0 7a1 1 0 110-2 1 1 0 010 2zm0 7a1 1 0 110-2 1 1 0 010 2z"
                    />
                  </svg>
                </button>
              </div>

              {/* 消息列表 */}
              <div
                ref={messagesContainerRef}
                className="flex-1 overflow-y-auto border-y border-slate-300/55 bg-white/20 p-4 dark:border-slate-700/45 dark:bg-slate-900/20 md:p-5"
              >
                {messages.length === 0 ? (
                  <div className="flex h-full items-center justify-center">
                    <p className="text-sm text-slate-500 dark:text-slate-400">暂无消息，开始聊天吧</p>
                  </div>
                ) : (
                  <div className="space-y-4">
                    {messages.map((message) => (
                      <MessageBubble
                        key={message.msgId}
                        message={message}
                        isOwn={message.isOwn}
                        senderName={message.fromUsername}
                      />
                    ))}
                  </div>
                )}
              </div>

              {/* 输入框 */}
              <ChatInput
                disabled={!isConnected}
                onSend={handleSendMessage}
                placeholder={!isConnected ? "连接已断开..." : "输入消息..."}
              />
            </>
          )}
        </main>
      </div>

      {/* 新建聊天弹窗 */}
      <NewChatModal
        isOpen={isNewChatModalOpen}
        onClose={() => setIsNewChatModalOpen(false)}
        onSessionCreated={handleSessionCreated}
        currentUsername={user?.username}
      />
    </div>
  );
}
