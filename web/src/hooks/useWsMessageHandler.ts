import { useCallback, useRef } from "react";
import { useAuthStore } from "@/stores/auth";
import { useSessionStore } from "@/stores/session";
import { useMessageStore, pushMessageToChatMessage } from "@/stores/message";
import { WsPacket } from "@/gen/gateway/v1/packet_pb";
import type { SessionInfo } from "@/stores/session";

interface UseWsMessageHandlerOptions {
  /**
   * 获取 WebSocket 发送函数的回调
   * 用于发送 ACK 确认消息
   */
  getSend: () => ((packet: WsPacket) => void) | null;
}

/**
 * WebSocket 消息处理器 Hook
 *
 * 处理来自服务器的两种消息类型：
 * 1. push - 推送消息（新消息）
 * 2. ack - 消息确认（发送回执）
 *
 * 职责：
 * - 解析 WebSocket 消息包
 * - 将 Protobuf 消息转换为前端格式
 * - 更新消息存储
 * - 自动创建/更新会话
 * - 计算未读数
 * - 发送 ACK 确认
 */
export function useWsMessageHandler({ getSend }: UseWsMessageHandlerOptions) {
  const { user } = useAuthStore();
  const {
    updateLastMessage,
    markAsRead,
    updateSession,
    currentSessionId,
    getSessionById,
    upsertSession,
  } = useSessionStore();
  const { addMessage, markAsSent, markAsFailed } = useMessageStore();

  /**
   * 处理推送消息（新消息）
   */
  const handlePush = useCallback(
    (push: any) => {
      console.log("[WsMessageHandler] Received push message:", push);

      // 转换为前端消息格式
      const chatMessage = pushMessageToChatMessage(push, user?.username || "");

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
            maxSeqId: Number(chatMessage.seqId),
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

      // 处理未读数
      const session = getSessionById(chatMessage.sessionId);
      if (session) {
        if (chatMessage.sessionId !== currentSessionId && !chatMessage.isOwn) {
          // 如果不是当前会话，使用水位线计算未读数
          const newUnread = Math.max(0, Number(chatMessage.seqId) - session.lastReadSeq);
          updateSession(chatMessage.sessionId, { unreadCount: newUnread });
        } else if (chatMessage.sessionId === currentSessionId && !chatMessage.isOwn) {
          // 如果是当前会话，自动标记已读
          markAsRead(chatMessage.sessionId, Number(chatMessage.seqId));
        }
      }

      // 立即发送 Ack 确认（推送消息回执）
      const send = getSend();
      if (send) {
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
        send(ackPacket);
      }
    },
    [
      user,
      addMessage,
      getSessionById,
      upsertSession,
      updateLastMessage,
      updateSession,
      currentSessionId,
      markAsRead,
      getSend,
    ],
  );

  /**
   * 处理消息确认（ACK）
   * 用于将待发送消息标记为已发送或失败
   */
  const handleAck = useCallback(
    (ack: any) => {
      if (!ack.refSeq) {
        return;
      }

      if (ack.error) {
        markAsFailed(ack.refSeq);
        return;
      }

      const seqId = typeof ack.seqId === "bigint" ? ack.seqId : BigInt(ack.seqId ?? 0);
      const msgId =
        typeof ack.msgId === "bigint" ? ack.msgId.toString() : String(ack.msgId ?? ack.refSeq);
      markAsSent(ack.refSeq, msgId, seqId);
    },
    [markAsFailed, markAsSent],
  );

  /**
   * 处理 WebSocket 消息
   * 根据消息类型分发到不同的处理器
   */
  const handleMessage = useCallback(
    (packet: WsPacket) => {
      const { payload } = packet;

      if (payload.case === "push") {
        handlePush(payload.value);
      } else if (payload.case === "ack") {
        handleAck(payload.value);
      }
    },
    [handlePush, handleAck],
  );

  return { handleMessage };
}
