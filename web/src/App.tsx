import { useRef } from "react";
import { useAuthStore } from "@/stores/auth";
import { useWebSocket } from "@/hooks/useWebSocket";
import { useWsMessageHandler } from "@/hooks/useWsMessageHandler";
import { WsPacket } from "@/gen/gateway/v1/packet_pb";
import { syncInboxDelta } from "@/sync/inboxSync";
import LoginPage from "@/pages/LoginPage";
import ChatPage from "@/pages/ChatPage";

/**
 * 应用入口组件
 *
 * 职责：
 * - 路由控制（登录页 vs 聊天页）
 * - WebSocket 连接管理
 * - 连接状态传递给子组件
 */
function App() {
  const { isAuthenticated, accessToken, user } = useAuthStore();

  // 使用 ref 保存 send 函数的引用，供消息处理器使用
  const sendRef = useRef<((packet: WsPacket) => void) | null>(null);

  // 消息处理器（处理 push 和 ack 消息）
  const { handleMessage } = useWsMessageHandler({
    getSend: () => sendRef.current,
  });

  // WebSocket 连接（只在登录后激活）
  const { isConnected, isConnecting, send } = useWebSocket({
    token: accessToken ?? undefined,
    onOpen: () => {
      if (user?.username) {
        syncInboxDelta(user.username).catch((err) => {
          console.error("[App] Failed to sync inbox delta on ws open:", err);
        });
      }
    },
    onMessage: handleMessage,
  });

  // 更新 send 引用
  sendRef.current = send;

  return (
    <div className="relative h-[var(--app-height)] w-full overflow-hidden">
      <div className="pointer-events-none absolute -left-24 -top-28 h-72 w-72 rounded-full bg-sky-400/30 blur-3xl dark:bg-sky-500/20" />
      <div className="pointer-events-none absolute -right-28 top-8 h-80 w-80 rounded-full bg-blue-400/20 blur-3xl dark:bg-blue-500/15" />
      <div className="pointer-events-none absolute bottom-[-140px] left-1/3 h-80 w-80 rounded-full bg-cyan-300/20 blur-3xl dark:bg-cyan-500/10" />

      {!isAuthenticated ? (
        <LoginPage />
      ) : (
        <ChatPage isConnected={isConnected} isConnecting={isConnecting} send={send} />
      )}
    </div>
  );
}

export default App;
