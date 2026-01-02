import { useCallback, useRef } from "react";
import { useAuthStore } from "@/stores/auth";
import { useSessionStore } from "@/stores/session";
import { useMessageStore, pushMessageToChatMessage } from "@/stores/message";
import { useWebSocket } from "@/hooks/useWebSocket";
import { WsPacket } from "@/gen/gateway/v1/packet_pb";
import type { SessionInfo } from "@/stores/session";
import LoginPage from "@/pages/LoginPage";
import ChatPage from "@/pages/ChatPage";

/**
 * 应用入口组件
 * 处理路由、WebSocket 连接和消息路由
 */
function App() {
  const { isAuthenticated, accessToken, user } = useAuthStore();
  const { updateLastMessage, incrementUnread, currentSessionId, getSessionById, upsertSession } = useSessionStore();
  const { addMessage, markAsSent, markAsFailed } = useMessageStore();

  // 使用 ref 保存 send 函数的引用，避免循环依赖
  const sendRef = useRef<((packet: WsPacket) => void) | null>(null);

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

        // 如果有会话元数据，自动创建或更新会话
        if (push.sessionMeta) {
          const { name, type } = push.sessionMeta;
          const existingSession = getSessionById(chatMessage.sessionId);

          if (!existingSession) {
            // 创建新会话
            const newSession: SessionInfo = {
              sessionId: chatMessage.sessionId,
              name: name || chatMessage.fromUsername, // 单聊用对方用户名
              type: type || 1,
              unreadCount: chatMessage.isOwn ? 0 : 1,
              lastReadSeq: 0,
              lastMessage: {
                msgId: BigInt(chatMessage.msgId),
                seqId: chatMessage.seqId,
                content: chatMessage.content,
                type: chatMessage.msgType,
                timestamp: chatMessage.timestamp,
              },
            };
            upsertSession(newSession);
          } else {
            // 更新现有会话
            updateLastMessage(chatMessage.sessionId, push);
          }
        } else {
          // 没有元数据，尝试更新现有会话
          updateLastMessage(chatMessage.sessionId, push);
        }

        // 如果不是当前会话，增加未读数
        if (chatMessage.sessionId !== currentSessionId && !chatMessage.isOwn) {
          incrementUnread(chatMessage.sessionId);
        }

        // 立即发送 Ack 确认（推送消息回执）
        if (sendRef.current) {
          const ackPacket = WsPacket.fromJsonString(
            JSON.stringify({
              seq: `ack-${push.msgId}`,
              ack: {
                refSeq: push.msgId.toString(),
                msgId: push.msgId,
                seqId: push.seqId,
                sessionId: push.sessionId,
              },
            }),
          );
          sendRef.current(ackPacket);
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
    [user, addMessage, updateLastMessage, incrementUnread, currentSessionId, getSessionById, upsertSession, markAsFailed, markAsSent],
  );

  // WebSocket 连接（只在登录后激活）
  const { isConnected, send } = useWebSocket({
    token: accessToken ?? undefined,
    onMessage: handleWsMessage,
  });

  // 更新 send 引用
  sendRef.current = send;

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
