import { useRef } from "react";
import { useAuthStore } from "@/stores/auth";
import { useWebSocket } from "@/hooks/useWebSocket";
import { useWsMessageHandler } from "@/hooks/useWsMessageHandler";
import { WsPacket } from "@/gen/gateway/v1/packet_pb";
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
  const { isAuthenticated, accessToken } = useAuthStore();

  // 使用 ref 保存 send 函数的引用，供消息处理器使用
  const sendRef = useRef<((packet: WsPacket) => void) | null>(null);

  // 消息处理器（处理 push 和 ack 消息）
  const { handleMessage } = useWsMessageHandler({
    getSend: () => sendRef.current,
  });

  // WebSocket 连接（只在登录后激活）
  const { isConnected, isConnecting, send } = useWebSocket({
    token: accessToken ?? undefined,
    onMessage: handleMessage,
  });

  // 更新 send 引用
  sendRef.current = send;

  return (
    <div className="h-screen w-screen overflow-hidden bg-gray-50 dark:bg-gray-900">
      {!isAuthenticated ? (
        <LoginPage />
      ) : (
        <ChatPage isConnected={isConnected} isConnecting={isConnecting} send={send} />
      )}
    </div>
  );
}

export default App;
