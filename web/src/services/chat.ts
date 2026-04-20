import { create } from "@bufbuild/protobuf";
import { MessageSchema, MessageType, type Message } from "@gen/common/v1/message_pb";
import { ChatRequestSchema, WsPacketSchema, type Ack } from "@gen/gateway/v1/packet_pb";

import { appRuntime } from "../app/runtime";
import {
  findPendingEventByClientMsgId,
  getOutboxRowsBySession,
  getSession,
  putEvent,
  upsertSession,
} from "../db/repo";
import type { OutboxRow } from "../db/schema";
import { toBigIntId, toIdString } from "../lib/id";

export type SendMessageInput = {
  sessionId: string;
  content: string;
  replyToEventId?: bigint;
  mentionedUsernames?: string[];
};

export type OutboxStatusSummary = {
  status: OutboxRow["status"];
  retryCount: number;
  updatedAtMs: number;
};

function generateClientMsgId(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  return `cmid_${Date.now()}_${Math.random().toString(16).slice(2)}`;
}

function generatePendingSeqId(): string {
  const seed = BigInt(Date.now()) * 1_000n + BigInt(Math.floor(Math.random() * 1_000));
  return toIdString(-seed);
}

function buildMessage(input: SendMessageInput, clientMsgId: string): Message {
  return create(MessageSchema, {
    type: MessageType.TEXT,
    content: input.content,
    replyToEventId: input.replyToEventId ?? 0n,
    clientMsgId,
    mentionedUsernames: input.mentionedUsernames ?? [],
  });
}

async function insertPendingEvent(sessionId: string, message: Message): Promise<void> {
  const now = BigInt(Date.now());
  await putEvent({
    sessionId,
    seqId: generatePendingSeqId(),
    eventId: "0",
    fromUsername: "",
    timestampMs: toIdString(now),
    payloadCase: "message",
    messageType: message.type,
    content: message.content,
    replyToEventId: toIdString(message.replyToEventId),
    clientMsgId: message.clientMsgId,
    mentionedUsernames: [...message.mentionedUsernames],
    recalled: false,
    edited: false,
    targetEventId: "0",
    readUptoSeqId: "0",
    sessionUpdateKind: 0,
    sessionUpdateNewName: "",
    sessionUpdateNewAvatarUrl: "",
    sessionUpdateAffectedMembers: [],
  });

  const existingSession = await getSession(sessionId);
  if (existingSession !== undefined) {
    await upsertSession({
      ...existingSession,
      lastEventPreview: message.content,
      lastEventTs: toIdString(now),
    });
  }
}

async function sendPreparedMessage(sessionId: string, message: Message): Promise<Ack> {
  const packet = create(WsPacketSchema, {
    clientSeq: "",
    payload: {
      case: "chatRequest",
      value: create(ChatRequestSchema, {
        sessionId,
        message,
      }),
    },
  });

  return appRuntime.getOutbox().send(sessionId, message.clientMsgId, packet);
}

export async function sendTextMessage(input: SendMessageInput): Promise<Ack> {
  const content = input.content.trim();
  if (content === "") {
    throw new Error("Cannot send empty message");
  }

  const message = buildMessage(
    {
      ...input,
      content,
    },
    generateClientMsgId(),
  );
  await insertPendingEvent(input.sessionId, message);
  return sendPreparedMessage(input.sessionId, message);
}

export async function retryPendingMessage(sessionId: string, clientMsgId: string): Promise<Ack> {
  const pending = await findPendingEventByClientMsgId(sessionId, clientMsgId);
  if (pending === undefined || pending.payloadCase !== "message") {
    throw new Error("Pending message not found");
  }

  const message = create(MessageSchema, {
    type: pending.messageType,
    content: pending.content,
    replyToEventId: toBigIntId(pending.replyToEventId),
    clientMsgId: pending.clientMsgId,
    mentionedUsernames: pending.mentionedUsernames,
  });
  return sendPreparedMessage(sessionId, message);
}

export async function getOutboxStatusMap(
  sessionId: string,
): Promise<Record<string, OutboxStatusSummary>> {
  const rows = await getOutboxRowsBySession(sessionId);
  const latest = new Map<string, OutboxStatusSummary>();

  for (const row of rows) {
    if (row.clientMsgId.trim() === "") {
      continue;
    }
    const current = latest.get(row.clientMsgId);
    if (current === undefined || row.updatedAtMs >= current.updatedAtMs) {
      latest.set(row.clientMsgId, {
        status: row.status,
        retryCount: row.retryCount,
        updatedAtMs: row.updatedAtMs,
      });
    }
  }

  return Object.fromEntries(latest.entries());
}
