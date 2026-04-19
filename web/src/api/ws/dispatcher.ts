import type { ChatEvent } from "@gen/common/v1/event_pb";
import type {
  Ack,
  ChatRequest,
  StreamBegin,
  StreamChunk,
  StreamEnd,
  TypingSignal,
  WsPacket,
} from "@gen/gateway/v1/packet_pb";

export type WsPacketHandlers = {
  onPulse?: () => void;
  onAck?: (ack: Ack) => void;
  onChatRequest?: (req: ChatRequest) => void;
  onEvent?: (event: ChatEvent) => void;
  onStreamBegin?: (msg: StreamBegin) => void;
  onStreamChunk?: (msg: StreamChunk) => void;
  onStreamEnd?: (msg: StreamEnd) => void;
  onTyping?: (signal: TypingSignal) => void;
  onEmpty?: () => void;
};

export function dispatchWsPacket(
  packet: WsPacket,
  handlers: WsPacketHandlers,
): void {
  const payloadCase = packet.payload.case;
  switch (payloadCase) {
    case "pulse":
      handlers.onPulse?.();
      return;
    case "ack":
      handlers.onAck?.(packet.payload.value);
      return;
    case "chatRequest":
      handlers.onChatRequest?.(packet.payload.value);
      return;
    case "event":
      handlers.onEvent?.(packet.payload.value);
      return;
    case "streamBegin":
      handlers.onStreamBegin?.(packet.payload.value);
      return;
    case "streamChunk":
      handlers.onStreamChunk?.(packet.payload.value);
      return;
    case "streamEnd":
      handlers.onStreamEnd?.(packet.payload.value);
      return;
    case "typing":
      handlers.onTyping?.(packet.payload.value);
      return;
    case undefined:
      handlers.onEmpty?.();
      return;
  }

  const unreachable: never = payloadCase;
  throw new Error(`Unhandled WsPacket payload case: ${String(unreachable)}`);
}
