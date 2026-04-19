import type { WsPacket } from "@gen/gateway/v1/packet_pb";

export type WsPacketHandlers = {
  onPulse?: (packet: WsPacket) => void;
  onAck?: (packet: WsPacket) => void;
  onChatRequest?: (packet: WsPacket) => void;
  onEvent?: (packet: WsPacket) => void;
  onStreamBegin?: (packet: WsPacket) => void;
  onStreamChunk?: (packet: WsPacket) => void;
  onStreamEnd?: (packet: WsPacket) => void;
  onTyping?: (packet: WsPacket) => void;
  onEmpty?: (packet: WsPacket) => void;
};

export function dispatchWsPacket(
  packet: WsPacket,
  handlers: WsPacketHandlers,
): void {
  const payloadCase = packet.payload.case;
  switch (payloadCase) {
    case "pulse":
      handlers.onPulse?.(packet);
      return;
    case "ack":
      handlers.onAck?.(packet);
      return;
    case "chatRequest":
      handlers.onChatRequest?.(packet);
      return;
    case "event":
      handlers.onEvent?.(packet);
      return;
    case "streamBegin":
      handlers.onStreamBegin?.(packet);
      return;
    case "streamChunk":
      handlers.onStreamChunk?.(packet);
      return;
    case "streamEnd":
      handlers.onStreamEnd?.(packet);
      return;
    case "typing":
      handlers.onTyping?.(packet);
      return;
    case undefined:
      handlers.onEmpty?.(packet);
      return;
  }

  const unreachable: never = payloadCase;
  throw new Error(`Unhandled WsPacket payload case: ${String(unreachable)}`);
}
