import { useEffect, useRef, useState } from "react";
import { useMessageStore, createPendingMessage } from "@/stores/message";
import { useAuthStore } from "@/stores/auth";
import { WsPacket } from "@/gen/gateway/v1/packet_pb";
import { MESSAGE_TYPES } from "@/constants";
import { MessageBubble } from "@/components/MessageBubble";
import { ChatInput } from "@/components/ChatInput";
import type { SessionInfo } from "@/stores/session";

interface ChatAreaProps {
  currentSession: SessionInfo | null;
  isConnected: boolean;
  send: (packet: WsPacket) => void;
}

/**
 * 聊天区域（消息列表 + 输入框）
 */
export function ChatArea({ currentSession, isConnected, send }: ChatAreaProps) {
  const { user } = useAuthStore();
  const { getSessionMessages } = useMessageStore();
  const messagesContainerRef = useRef<HTMLDivElement>(null);
  const [prevSessionId, setPrevSessionId] = useState<string | null>(null);
  const [prevMessageCount, setPrevMessageCount] = useState(0);

  const messages = currentSession ? getSessionMessages(currentSession.sessionId) : [];

  // 滚动到底部
  const scrollToBottom = (behavior: ScrollBehavior = "auto") => {
    const container = messagesContainerRef.current;
    if (!container) return;
    container.scrollTo({
      top: container.scrollHeight,
      behavior,
    });
  };

  // 仅在会话切换或新增消息时滚动
  useEffect(() => {
    const sessionId = currentSession?.sessionId ?? null;
    const currentCount = messages.length;
    const sessionChanged = sessionId !== prevSessionId;
    const hasNewMessage = currentCount > prevMessageCount;

    if (sessionChanged || hasNewMessage) {
      scrollToBottom(sessionChanged ? "auto" : "smooth");
    }

    setPrevSessionId(sessionId);
    setPrevMessageCount(currentCount);
  }, [currentSession?.sessionId, messages.length]);

  // 发送消息
  const handleSendMessage = (content: string) => {
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

    // 创建 WebSocket 消息包
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
  };

  // 空状态
  if (!currentSession) {
    return (
      <main className="lg-glass flex min-h-0 flex-1 flex-col overflow-hidden rounded-2xl">
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
      </main>
    );
  }

  return (
    <main className="lg-glass flex min-h-0 flex-1 flex-col overflow-hidden rounded-2xl">
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
        <button
          aria-label="更多操作"
          title="更多操作"
          className="rounded-full p-2 text-slate-500 transition-all duration-200 hover:bg-white/45 hover:text-slate-700 hover:shadow-sm dark:text-slate-300 dark:hover:bg-slate-700/55 dark:hover:text-slate-100"
        >
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
    </main>
  );
}
