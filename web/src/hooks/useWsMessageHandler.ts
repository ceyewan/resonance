import { useCallback } from "react";
import { useAuthStore } from "@/stores/auth";
import { useMessageStore } from "@/stores/message";
import { WsPacket } from "@/gen/gateway/v1/packet_pb";
import { applyIncomingPush, shouldTriggerGapCompensation, syncInboxDelta } from "@/sync/inboxSync";

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
  const { markAsSent, markAsFailed } = useMessageStore();

  /**
   * 处理推送消息（新消息）
   */
  const handlePush = useCallback(
    (push: any) => {
      console.log("[WsMessageHandler] Received push message:", push);
      const username = user?.username || "";
      const shouldCompensate = shouldTriggerGapCompensation(push);

      void applyIncomingPush(push, username)
        .then(() => {
          if (shouldCompensate && username) {
            void syncInboxDelta(username).catch((err) => {
              console.error("[WsMessageHandler] Failed to run compensation sync:", err);
            });
          }
        })
        .catch((err) => {
          console.error("[WsMessageHandler] Failed to apply incoming push:", err);
        });

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
