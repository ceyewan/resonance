import { fromBinary, toBinary } from "@bufbuild/protobuf";
import type { Ack, WsPacket } from "@gen/gateway/v1/packet_pb";
import { WsPacketSchema } from "@gen/gateway/v1/packet_pb";

import {
  enqueueOutbox,
  getPendingOutboxRows,
  markOutboxAcked,
  markOutboxFailed,
  markOutboxRetrying,
} from "../../db/repo";
import type { OutboxRow } from "../../db/schema";
import { toIdString } from "../../lib/id";
import { dispatchWsPacket } from "./dispatcher";
import type { WsClient } from "./client";

type PendingEntry = {
  row: OutboxRow;
  packet: WsPacket;
  timeoutId: ReturnType<typeof setTimeout> | null;
  resolve: (ack: Ack) => void;
  reject: (error: OutboxError) => void;
};

const ACK_TIMEOUT_MS = 5_000;
const MAX_RETRY_COUNT = 3;

export class OutboxError extends Error {
  readonly clientSeq: string;

  constructor(message: string, clientSeq: string) {
    super(message);
    this.name = "OutboxError";
    this.clientSeq = clientSeq;
  }
}

export class OutboxManager {
  private readonly pending = new Map<string, PendingEntry>();

  constructor(private readonly ws: Pick<WsClient, "isOpen" | "send" | "onPacket">) {
    this.ws.onPacket((packet) => {
      dispatchWsPacket(packet, {
        onAck: (ack) => {
          this.ack(ack.refClientSeq, ack);
        },
      });
    });
  }

  async send(sessionId: string, clientMsgId: string, packet: WsPacket): Promise<Ack> {
    const clientSeq = generateClientSeq();
    const packetToSend: WsPacket = {
      ...packet,
      clientSeq,
    };
    const now = Date.now();
    const row: OutboxRow = {
      clientSeq,
      sessionId,
      clientMsgId,
      status: "sending",
      payloadJson: serializePacket(packetToSend),
      retryCount: 0,
      createdAtMs: now,
      updatedAtMs: now,
      ackedEventId: "",
      ackedSeqId: "",
    };
    await enqueueOutbox(row);

    return new Promise<Ack>((resolve, reject) => {
      const entry: PendingEntry = {
        row,
        packet: packetToSend,
        timeoutId: null,
        resolve,
        reject,
      };
      this.pending.set(clientSeq, entry);
      this.trySend(entry);
    });
  }

  ack(refClientSeq: string, ackPayload: Ack): void {
    const entry = this.pending.get(refClientSeq);
    if (entry === undefined) {
      return;
    }
    this.clearTimeout(entry);
    this.pending.delete(refClientSeq);
    void (async () => {
      try {
        await markOutboxAcked(
          refClientSeq,
          toIdString(ackPayload.eventId),
          toIdString(ackPayload.seqId),
        );
        entry.resolve(ackPayload);
      } catch (cause) {
        entry.reject(
          new OutboxError(
            cause instanceof Error ? cause.message : "Failed to persist outbox ack",
            refClientSeq,
          ),
        );
      }
    })();
  }

  async flushPending(): Promise<void> {
    if (!this.ws.isOpen()) {
      return;
    }

    const rows = await getPendingOutboxRows();
    for (const row of rows) {
      const entry = this.pending.get(row.clientSeq) ?? this.restorePendingEntry(row);
      this.trySend(entry);
    }
  }

  private restorePendingEntry(row: OutboxRow): PendingEntry {
    const entry: PendingEntry = {
      row,
      packet: deserializePacket(row.payloadJson),
      timeoutId: null,
      resolve: () => {},
      reject: () => {},
    };
    this.pending.set(row.clientSeq, entry);
    return entry;
  }

  private trySend(entry: PendingEntry): boolean {
    if (entry.timeoutId !== null || !this.ws.isOpen()) {
      return false;
    }

    try {
      this.ws.send(entry.packet);
      this.armTimeout(entry);
      return true;
    } catch {
      return false;
    }
  }

  private armTimeout(entry: PendingEntry): void {
    this.clearTimeout(entry);
    entry.timeoutId = setTimeout(() => {
      this.handleTimeout(entry);
    }, ACK_TIMEOUT_MS);
  }

  private handleTimeout(entry: PendingEntry): void {
    const current = this.pending.get(entry.row.clientSeq);
    if (current === undefined) {
      return;
    }
    this.clearTimeout(entry);

    if (entry.row.retryCount >= MAX_RETRY_COUNT) {
      this.pending.delete(entry.row.clientSeq);
      const finalRetryCount = entry.row.retryCount;
      void markOutboxFailed(entry.row.clientSeq, finalRetryCount);
      entry.reject(
        new OutboxError(`Ack timeout after ${MAX_RETRY_COUNT} retries`, entry.row.clientSeq),
      );
      return;
    }

    entry.row.retryCount += 1;
    entry.row.status = "retrying";
    entry.row.updatedAtMs = Date.now();
    void markOutboxRetrying(entry.row.clientSeq, entry.row.retryCount);
    this.trySend(entry);
  }

  private clearTimeout(entry: PendingEntry): void {
    if (entry.timeoutId !== null) {
      clearTimeout(entry.timeoutId);
      entry.timeoutId = null;
    }
  }
}

function serializePacket(packet: WsPacket): string {
  const bytes = toBinary(WsPacketSchema, packet);
  return JSON.stringify(Array.from(bytes));
}

function deserializePacket(payloadJson: string): WsPacket {
  const raw = JSON.parse(payloadJson) as number[];
  const bytes = Uint8Array.from(raw);
  return fromBinary(WsPacketSchema, bytes);
}

function generateClientSeq(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `cs_${Date.now()}_${Math.random().toString(16).slice(2)}`;
}
