import "fake-indexeddb/auto";

import { create } from "@bufbuild/protobuf";
import { MessageSchema, MessageType } from "@gen/common/v1/message_pb";
import {
  AckSchema,
  ChatRequestSchema,
  WsPacketSchema,
  type Ack,
  type WsPacket,
} from "@gen/gateway/v1/packet_pb";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { getOutbox } from "../../db/repo";
import { db } from "../../db/schema";
import { OutboxManager } from "./outbox";

type PacketListener = (packet: WsPacket) => void;

class FakeWsClient {
  sentPackets: WsPacket[] = [];
  open = true;
  throwOnSend = false;
  private readonly listeners = new Set<PacketListener>();

  isOpen(): boolean {
    return this.open;
  }

  send(packet: WsPacket): void {
    if (this.throwOnSend) {
      throw new Error("send failed");
    }
    this.sentPackets.push(packet);
  }

  onPacket(listener: PacketListener): () => void {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  }

  emitAck(refClientSeq: string): void {
    const packet = create(WsPacketSchema, {
      clientSeq: "",
      payload: {
        case: "ack",
        value: create(AckSchema, {
          refClientSeq,
          eventId: 101n,
          seqId: 201n,
          sessionId: "s-ack",
        }),
      },
    });
    for (const listener of this.listeners) {
      listener(packet);
    }
  }
}

function createChatRequestPacket(): WsPacket {
  return create(WsPacketSchema, {
    clientSeq: "",
    payload: {
      case: "chatRequest",
      value: create(ChatRequestSchema, {
        sessionId: "s1",
        message: create(MessageSchema, {
          type: MessageType.TEXT,
          content: "hello",
          replyToEventId: 0n,
          clientMsgId: "cmid-1",
          mentionedUsernames: [],
        }),
      }),
    },
  });
}

async function clearDbForTest(): Promise<void> {
  await db.transaction("rw", db.sessions, db.events, db.outbox, db.meta, async () => {
    await db.sessions.clear();
    await db.events.clear();
    await db.outbox.clear();
    await db.meta.clear();
  });
}

async function waitForSentPackets(ws: FakeWsClient, expectedCount: number): Promise<void> {
  for (let i = 0; i < 50; i += 1) {
    if (ws.sentPackets.length >= expectedCount) {
      return;
    }
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(1);
  }
  throw new Error(`Expected at least ${expectedCount} sent packets, got ${ws.sentPackets.length}`);
}

describe("OutboxManager", () => {
  beforeEach(async () => {
    await clearDbForTest();
    vi.useFakeTimers({
      toFake: ["setTimeout", "clearTimeout"],
    });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  test("1) 正常 ACK：send resolve 且 outbox.status=acked", async () => {
    const ws = new FakeWsClient();
    const outbox = new OutboxManager(ws);

    const promise = outbox.send("s1", "cmid-1", createChatRequestPacket());
    await waitForSentPackets(ws, 1);

    const clientSeq = ws.sentPackets[0]?.clientSeq;
    expect(clientSeq).toBeTruthy();

    ws.emitAck(clientSeq ?? "");
    const ack = await promise;
    expect(ack.eventId).toBe(101n);

    await vi.advanceTimersByTimeAsync(0);
    const row = await getOutbox(clientSeq ?? "");
    expect(row?.status).toBe("acked");
  });

  test("2) ACK 超时一次后重发并成功", async () => {
    const ws = new FakeWsClient();
    const outbox = new OutboxManager(ws);

    const promise = outbox.send("s2", "cmid-2", createChatRequestPacket());
    await waitForSentPackets(ws, 1);

    const clientSeq = ws.sentPackets[0]?.clientSeq ?? "";
    await vi.advanceTimersByTimeAsync(5_000);
    await vi.advanceTimersByTimeAsync(0);

    expect(ws.sentPackets).toHaveLength(2);
    const retryRow = await getOutbox(clientSeq);
    expect(retryRow?.status).toBe("retrying");
    expect(retryRow?.retryCount).toBe(1);

    ws.emitAck(clientSeq);
    await expect(promise).resolves.toMatchObject<Ack>({ refClientSeq: clientSeq });

    await vi.advanceTimersByTimeAsync(0);
    const ackedRow = await getOutbox(clientSeq);
    expect(ackedRow?.status).toBe("acked");
  });

  test("3) 两次重发后第 3 次发送收到 ACK", async () => {
    const ws = new FakeWsClient();
    const outbox = new OutboxManager(ws);

    const promise = outbox.send("s3", "cmid-3", createChatRequestPacket());
    await waitForSentPackets(ws, 1);

    const clientSeq = ws.sentPackets[0]?.clientSeq ?? "";
    await vi.advanceTimersByTimeAsync(10_000);
    await vi.advanceTimersByTimeAsync(0);

    expect(ws.sentPackets).toHaveLength(3);
    const retryRow = await getOutbox(clientSeq);
    expect(retryRow?.retryCount).toBe(2);

    ws.emitAck(clientSeq);
    await expect(promise).resolves.toMatchObject<Ack>({ refClientSeq: clientSeq });
  });

  test("4) 3 次重发均失败：最终 failed 并抛 OutboxError", async () => {
    const ws = new FakeWsClient();
    const outbox = new OutboxManager(ws);

    const promise = outbox.send("s4", "cmid-4", createChatRequestPacket());
    await waitForSentPackets(ws, 1);
    const clientSeq = ws.sentPackets[0]?.clientSeq ?? "";
    const rejection = expect(promise).rejects.toMatchObject({
      name: "OutboxError",
      clientSeq,
    });

    await vi.advanceTimersByTimeAsync(20_000);
    await vi.advanceTimersByTimeAsync(0);

    await rejection;

    expect(ws.sentPackets).toHaveLength(4);
    const failedRow = await getOutbox(clientSeq);
    expect(failedRow?.status).toBe("failed");
    expect(failedRow?.retryCount).toBe(3);
  });

  test("5) 连接未打开时只入队，重连 flush 后发送并收到 ACK", async () => {
    const ws = new FakeWsClient();
    ws.open = false;
    const outbox = new OutboxManager(ws);

    const promise = outbox.send("s5", "cmid-5", createChatRequestPacket());
    await vi.advanceTimersByTimeAsync(20_000);

    expect(ws.sentPackets).toHaveLength(0);
    const queuedRows = await db.outbox.toArray();
    expect(queuedRows).toHaveLength(1);
    expect(queuedRows[0]?.status).toBe("sending");
    expect(queuedRows[0]?.retryCount).toBe(0);

    ws.open = true;
    await outbox.flushPending();
    await waitForSentPackets(ws, 1);

    const clientSeq = ws.sentPackets[0]?.clientSeq ?? "";
    ws.emitAck(clientSeq);
    await expect(promise).resolves.toMatchObject<Ack>({ refClientSeq: clientSeq });
  });

  test("6) ws.send() 抛错时不立即耗尽重试，恢复后 flush 可继续发送", async () => {
    const ws = new FakeWsClient();
    ws.throwOnSend = true;
    const outbox = new OutboxManager(ws);

    const promise = outbox.send("s6", "cmid-6", createChatRequestPacket());
    await vi.advanceTimersByTimeAsync(20_000);

    expect(ws.sentPackets).toHaveLength(0);
    const queuedRows = await db.outbox.toArray();
    expect(queuedRows).toHaveLength(1);
    expect(queuedRows[0]?.status).toBe("sending");
    expect(queuedRows[0]?.retryCount).toBe(0);

    ws.throwOnSend = false;
    await outbox.flushPending();
    await waitForSentPackets(ws, 1);

    const clientSeq = ws.sentPackets[0]?.clientSeq ?? "";
    ws.emitAck(clientSeq);
    await expect(promise).resolves.toMatchObject<Ack>({ refClientSeq: clientSeq });
  });
});
