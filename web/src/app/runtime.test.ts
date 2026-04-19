import { create } from "@bufbuild/protobuf";
import { ChatEventSchema } from "@gen/common/v1/event_pb";
import { MessageSchema, MessageType } from "@gen/common/v1/message_pb";
import { WsPacketSchema, type WsPacket } from "@gen/gateway/v1/packet_pb";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { useConnectionStore } from "../stores/connection";
import { AppRuntime } from "./runtime";

type PacketListener = (packet: WsPacket) => void;
type StatusListener = (status: "idle" | "connecting" | "open" | "offline") => void;
type ErrorListener = (error: Error) => void;

class FakeWsClient {
  connected = false;
  readonly packetListeners = new Set<PacketListener>();
  readonly statusListeners = new Set<StatusListener>();
  readonly errorListeners = new Set<ErrorListener>();

  connect(): void {
    this.connected = true;
    this.emitStatus("connecting");
  }

  disconnect(): void {
    this.connected = false;
    this.emitStatus("idle");
  }

  isOpen(): boolean {
    return this.connected;
  }

  send(): void {}

  onPacket(listener: PacketListener): () => void {
    this.packetListeners.add(listener);
    return () => {
      this.packetListeners.delete(listener);
    };
  }

  onStatus(listener: StatusListener): () => void {
    this.statusListeners.add(listener);
    return () => {
      this.statusListeners.delete(listener);
    };
  }

  onError(listener: ErrorListener): () => void {
    this.errorListeners.add(listener);
    return () => {
      this.errorListeners.delete(listener);
    };
  }

  emitPacket(packet: WsPacket): void {
    for (const listener of this.packetListeners) {
      listener(packet);
    }
  }

  emitStatus(status: "idle" | "connecting" | "open" | "offline"): void {
    if (status === "open") {
      this.connected = true;
    }
    if (status === "offline" || status === "idle") {
      this.connected = false;
    }
    for (const listener of this.statusListeners) {
      listener(status);
    }
  }
}

function makeEventPacket() {
  return create(WsPacketSchema, {
    clientSeq: "",
    payload: {
      case: "event",
      value: create(ChatEventSchema, {
        eventId: 1n,
        seqId: 1n,
        sessionId: "s-runtime",
        fromUsername: "bob",
        timestampMs: 1000n,
        payload: {
          case: "message",
          value: create(MessageSchema, {
            type: MessageType.TEXT,
            content: "hello",
            replyToEventId: 0n,
            clientMsgId: "",
            mentionedUsernames: [],
          }),
        },
      }),
    },
  });
}

beforeEach(() => {
  useConnectionStore.getState().reset();
});

describe("AppRuntime", () => {
  test("start 先同步 session list，再建立 WS 连接；open 后触发 inbox+outbox 链路", async () => {
    const fakeWs = new FakeWsClient();
    const steps: string[] = [];
    const runtime = new AppRuntime({
      createWsClient: () => fakeWs,
      syncSessionList: () => {
        steps.push("sync");
        return Promise.resolve();
      },
      runInboxSyncThenFlushOutbox: () => {
        steps.push("sync-inbox");
        return Promise.resolve();
      },
    });

    await runtime.start("token-1");
    expect(steps).toEqual(["sync"]);
    expect(useConnectionStore.getState().status).toBe("connecting");

    fakeWs.emitStatus("open");
    await Promise.resolve();

    expect(steps).toEqual(["sync", "sync-inbox"]);
    expect(useConnectionStore.getState().status).toBe("open");
  });

  test("收到 WS event 时转交给 handleWsEvent", async () => {
    const fakeWs = new FakeWsClient();
    const handleWsEvent = vi.fn(async () => {});
    const runtime = new AppRuntime({
      createWsClient: () => fakeWs,
      syncSessionList: async () => {},
      handleWsEvent,
      runInboxSyncThenFlushOutbox: async () => {},
    });

    await runtime.start("token-2");
    fakeWs.emitPacket(makeEventPacket());
    await Promise.resolve();

    expect(handleWsEvent).toHaveBeenCalledTimes(1);
  });

  test("handleWsEvent 抛错时写入连接错误而不是产生未处理 rejection", async () => {
    const fakeWs = new FakeWsClient();
    const runtime = new AppRuntime({
      createWsClient: () => fakeWs,
      syncSessionList: () => Promise.resolve(),
      handleWsEvent: () => Promise.reject(new Error("event failed")),
      runInboxSyncThenFlushOutbox: () => Promise.resolve(),
    });

    await runtime.start("token-3");
    fakeWs.emitPacket(makeEventPacket());
    await Promise.resolve();
    await Promise.resolve();

    expect(useConnectionStore.getState().status).toBe("offline");
    expect(useConnectionStore.getState().lastError).toBe("event failed");
  });
});
