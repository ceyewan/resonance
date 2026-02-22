import { useCallback, useEffect } from "react";
import { useSession } from "@/hooks/useSession";
import { useAuthStore } from "@/stores/auth";
import { WsPacket } from "@/gen/gateway/v1/packet_pb";
import { ChatHeader, ChatArea, SessionSidebar } from "@/components/Chat";

interface ChatPageProps {
  isConnected: boolean;
  isConnecting?: boolean;
  send: (packet: WsPacket) => void;
}

/**
 * 聊天主页面
 * 使用 Liquid Glass 设计风格
 * 拆分为子组件以提高可维护性
 */
export default function ChatPage({ isConnected, isConnecting = false, send }: ChatPageProps) {
  const { user } = useAuthStore();
  const { currentSession, loadSessions, selectSession } = useSession();

  // 加载会话列表
  useEffect(() => {
    loadSessions();
  }, [loadSessions]);

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
      loadSessions().then(() => {
        selectSession(sessionId);
      });
    },
    [loadSessions, selectSession],
  );

  return (
    <div className="relative flex h-full flex-col overflow-hidden px-3 pb-3 pt-3 md:px-4 md:pt-4">
      {/* 顶部导航栏 */}
      <ChatHeader isConnected={isConnected} isConnecting={isConnecting} />

      {/* 主内容区 */}
      <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-hidden md:flex-row">
        {/* 左侧会话列表 */}
        <SessionSidebar
          currentSessionId={currentSession?.sessionId ?? null}
          onSelectSession={handleSelectSession}
          onSessionCreated={handleSessionCreated}
          currentUsername={user?.username}
        />

        {/* 右侧聊天区域 */}
        <ChatArea currentSession={currentSession} isConnected={isConnected} send={send} />
      </div>
    </div>
  );
}
