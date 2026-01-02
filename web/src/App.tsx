import { useCallback } from "react";
import { useAuthStore } from "@/stores/auth";
import { useSessionStore } from "@/stores/session";
import { useMessageStore, pushMessageToChatMessage } from "@/stores/message";
import { useWebSocket } from "@/hooks/useWebSocket";
import LoginPage from "@/pages/LoginPage";
import ChatPage from "@/pages/ChatPage";

/**
 * 应用入口组件
 * 处理路由、WebSocket 连接和消息路由
 */
function App() {
  const { isAuthenticated, accessToken, user } = useAuthStore();
  const { updateLastMessage, incrementUnread, currentSessionId } = useSessionStore();
  const { addMessage, markAsSent, markAsFailed } = useMessageStore();

  // WebSocket 消息处理
  const handleWsMessage = useCallback(
    (packet: any) => {
      const { payload } = packet;

      // 处理服务器推送的消息
      if (payload.case === "push") {
        const push = payload.value;
        console.log("[App] Received push message:", push);

        // 转换为前端消息格式
        const chatMessage = pushMessageToChatMessage(
          push,
          user?.username || "",
        );

        // 添加到消息列表
        addMessage(chatMessage);

        // 更新会话的最后消息
        updateLastMessage(chatMessage.sessionId, push);

        // 如果不是当前会话，增加未读数
        if (chatMessage.sessionId !== currentSessionId && !chatMessage.isOwn) {
          incrementUnread(chatMessage.sessionId);
        }
      }
      // 处理消息确认（用于将待发送消息标记为已发送）
      else if (payload.case === "ack") {
        const ack = payload.value;
        if (!ack.refSeq) {
          return;
        }
        if (ack.error) {
          markAsFailed(ack.refSeq);
          return;
        }
        const seqId = typeof ack.seqId === "bigint" ? ack.seqId : BigInt(ack.seqId ?? 0);
        const msgId = typeof ack.msgId === "bigint" ? ack.msgId.toString() : String(ack.msgId ?? ack.refSeq);
        markAsSent(ack.refSeq, msgId, seqId);
      }
    },
    [user, addMessage, updateLastMessage, incrementUnread, currentSessionId, markAsFailed, markAsSent],
  );

  // WebSocket 连接（只在登录后激活）
  const { isConnected, send } = useWebSocket({
    token: accessToken ?? undefined,
    onMessage: handleWsMessage,
  });

  return (
    <div className="h-screen w-screen overflow-hidden bg-gray-50 dark:bg-gray-900">
      {!isAuthenticated ? (
        <LoginPage />
      ) : (
        <ChatPage isConnected={isConnected} send={send} />
      )}
    </div>
  );
}

export default App;
