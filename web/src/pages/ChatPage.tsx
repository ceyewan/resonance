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
 * Telegram 风格：左侧会话列表，右侧聊天区域
 */
export default function ChatPage({ isConnected, isConnecting = false, send }: ChatPageProps) {
  const { user, logout } = useAuthStore();
  const { sessions, currentSession, isLoading, loadSessions, selectSession } = useSession();
  const { getSessionMessages } = useMessageStore();
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const [isNewChatModalOpen, setIsNewChatModalOpen] = useState(false);

  // 加载会话列表
  useEffect(() => {
    loadSessions();
  }, [loadSessions]);

  const messages = currentSession ? getSessionMessages(currentSession.sessionId) : [];

  // 滚动到底部
  const scrollToBottom = useCallback(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, []);

  useEffect(() => {
    scrollToBottom();
  }, [currentSession, messages, scrollToBottom]);

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
      scrollToBottom();
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
    <div className="flex h-full flex-col bg-gray-50 dark:bg-gray-900">
      {/* 顶部导航栏 */}
      <header className="flex h-14 items-center justify-between border-b border-gray-200 bg-white px-4 dark:border-gray-700 dark:bg-gray-800">
        <div className="flex items-center gap-3">
          {/* Logo */}
          <svg
            className="h-6 w-6 text-sky-500"
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
          <span className="text-sm text-gray-600 dark:text-gray-400">
            {user?.nickname || user?.username}
          </span>

          {/* 登出按钮 */}
          <button
            onClick={logout}
            className={cn(
              "rounded-lg px-3 py-1.5 text-sm font-medium transition-colors",
              "text-gray-600 hover:bg-gray-100 hover:text-gray-900",
              "dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-white",
            )}
          >
            登出
          </button>
        </div>
      </header>

      {/* 主内容区 */}
      <div className="flex flex-1 overflow-hidden">
        {/* 左侧会话列表 */}
        <aside className="w-80 border-r border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-800">
          {/* 搜索框和新建按钮 */}
          <div className="border-b border-gray-200 p-3 dark:border-gray-700">
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
                    "w-full rounded-full border border-gray-300 bg-gray-100 py-2 pl-10 pr-4 text-sm",
                    "placeholder-gray-500 focus:border-sky-500 focus:bg-white focus:outline-none focus:ring-2 focus:ring-sky-500/20",
                    "disabled:opacity-50",
                    "dark:border-gray-600 dark:bg-gray-700 dark:text-white dark:placeholder-gray-500",
                  )}
                />
              </div>
              <button
                onClick={() => setIsNewChatModalOpen(true)}
                className={cn(
                  "flex h-9 w-9 shrink-0 items-center justify-center rounded-full",
                  "bg-sky-500 text-white transition-colors hover:bg-sky-600",
                  "focus:outline-none focus:ring-2 focus:ring-sky-500 focus:ring-offset-2",
                )}
                title="新建聊天"
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
          <div className="overflow-y-auto" style={{ maxHeight: "calc(100vh - 8rem)" }}>
            {isLoading ? (
              <div className="flex items-center justify-center p-8">
                <div className="flex items-center gap-2 text-gray-500 dark:text-gray-400">
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
                  className="mb-3 h-12 w-12 text-gray-400"
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
                <p className="text-sm text-gray-500 dark:text-gray-400">暂无对话</p>
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
        <main className="flex-1 flex flex-col bg-white dark:bg-gray-900">
          {!currentSession ? (
            // 空状态
            <div className="flex flex-1 flex-col items-center justify-center text-gray-500 dark:text-gray-400">
              <svg className="mb-4 h-16 w-16" fill="none" stroke="currentColor" viewBox="0 0 24 24">
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
              <div className="flex h-14 items-center justify-between border-b border-gray-200 bg-white px-4 dark:border-gray-700 dark:bg-gray-800">
                <div className="flex items-center gap-3">
                  {/* 头像 */}
                  <div className="flex h-9 w-9 items-center justify-center rounded-full bg-sky-500 text-sm font-semibold text-white">
                    {currentSession.name?.charAt(0)?.toUpperCase() || "?"}
                  </div>
                  <div>
                    <h2 className="text-sm font-semibold text-gray-900 dark:text-white">
                      {currentSession.name}
                    </h2>
                    <p className="text-xs text-gray-500 dark:text-gray-400">
                      {currentSession.type === 2 ? "群聊" : "单聊"}
                    </p>
                  </div>
                </div>

                {/* 更多操作 */}
                <button className="rounded-full p-2 text-gray-500 hover:bg-gray-100 hover:text-gray-700 dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-gray-300">
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
              <div className="flex-1 overflow-y-auto p-4">
                {messages.length === 0 ? (
                  <div className="flex h-full items-center justify-center">
                    <p className="text-sm text-gray-500 dark:text-gray-400">暂无消息，开始聊天吧</p>
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
                    <div ref={messagesEndRef} />
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
      />
    </div>
  );
}
