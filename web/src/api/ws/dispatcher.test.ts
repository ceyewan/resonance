import { create } from "@bufbuild/protobuf";
import { type ChatEvent, ChatEventSchema } from "@gen/common/v1/event_pb";
import { MessageSchema, MessageType } from "@gen/common/v1/message_pb";
import {
  type Ack,
  type ChatRequest,
  type StreamBegin,
  type StreamChunk,
  type StreamEnd,
  type TypingSignal,
  AckSchema,
  ChatRequestSchema,
  PulseSchema,
  StreamBeginSchema,
  StreamChunkSchema,
  StreamEndSchema,
  TypingSignalSchema,
  WsPacketSchema,
} from "@gen/gateway/v1/packet_pb";
import { describe, expect, test, vi } from "vitest";

import { dispatchWsPacket } from "./dispatcher";

describe("dispatchWsPacket", () => {
  test("按 payload.case 分发到对应 handler", () => {
    const onPulse = vi.fn<() => void>();
    const onAck = vi.fn<(ack: Ack) => void>();
    const onChatRequest = vi.fn<(req: ChatRequest) => void>();
    const onEvent = vi.fn<(event: ChatEvent) => void>();
    const onStreamBegin = vi.fn<(msg: StreamBegin) => void>();
    const onStreamChunk = vi.fn<(msg: StreamChunk) => void>();
    const onStreamEnd = vi.fn<(msg: StreamEnd) => void>();
    const onTyping = vi.fn<(signal: TypingSignal) => void>();
    const onEmpty = vi.fn<() => void>();

    const packets = [
      create(WsPacketSchema, {
        clientSeq: "1",
        payload: { case: "pulse", value: create(PulseSchema, {}) },
      }),
      create(WsPacketSchema, {
        clientSeq: "2",
        payload: {
          case: "ack",
          value: create(AckSchema, {
            refClientSeq: "1",
            eventId: 11n,
            seqId: 12n,
            sessionId: "s1",
          }),
        },
      }),
      create(WsPacketSchema, {
        clientSeq: "3",
        payload: {
          case: "chatRequest",
          value: create(ChatRequestSchema, {
            sessionId: "s-chat",
            message: create(MessageSchema, {
              type: MessageType.TEXT,
              content: "hello",
              replyToEventId: 0n,
              clientMsgId: "client-1",
              mentionedUsernames: [],
            }),
          }),
        },
      }),
      create(WsPacketSchema, {
        clientSeq: "4",
        payload: {
          case: "event",
          value: create(ChatEventSchema, {
            eventId: 21n,
            seqId: 22n,
            sessionId: "s2",
            fromUsername: "bob",
            timestampMs: 100n,
            payload: { case: undefined },
          }),
        },
      }),
      create(WsPacketSchema, {
        clientSeq: "5",
        payload: {
          case: "streamBegin",
          value: create(StreamBeginSchema, {
            parentEventId: 31n,
            sessionId: "s3",
            fromUsername: "bot",
          }),
        },
      }),
      create(WsPacketSchema, {
        clientSeq: "6",
        payload: {
          case: "streamChunk",
          value: create(StreamChunkSchema, {
            parentEventId: 31n,
            sequence: 1,
            delta: "A",
          }),
        },
      }),
      create(WsPacketSchema, {
        clientSeq: "7",
        payload: {
          case: "streamEnd",
          value: create(StreamEndSchema, {
            parentEventId: 31n,
            reason: 1,
          }),
        },
      }),
      create(WsPacketSchema, {
        clientSeq: "8",
        payload: {
          case: "typing",
          value: create(TypingSignalSchema, {
            sessionId: "s4",
            fromUsername: "tom",
            isTyping: true,
          }),
        },
      }),
      create(WsPacketSchema, { clientSeq: "9", payload: { case: undefined } }),
    ];

    for (const packet of packets) {
      dispatchWsPacket(packet, {
        onPulse,
        onAck,
        onChatRequest,
        onEvent,
        onStreamBegin,
        onStreamChunk,
        onStreamEnd,
        onTyping,
        onEmpty,
      });
    }

    expect(onPulse).toHaveBeenCalledTimes(1);
    expect(onAck).toHaveBeenCalledTimes(1);
    expect(onChatRequest).toHaveBeenCalledTimes(1);
    expect(onEvent).toHaveBeenCalledTimes(1);
    expect(onStreamBegin).toHaveBeenCalledTimes(1);
    expect(onStreamChunk).toHaveBeenCalledTimes(1);
    expect(onStreamEnd).toHaveBeenCalledTimes(1);
    expect(onTyping).toHaveBeenCalledTimes(1);
    expect(onEmpty).toHaveBeenCalledTimes(1);
  });
});
