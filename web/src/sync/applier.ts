import { SessionUpdateKind, type ChatEvent } from "@gen/common/v1/event_pb";
import { SessionType } from "@gen/common/v1/session_pb";

import { toBigIntId, toIdString } from "../lib/id";
import {
  deleteEvent,
  findPendingEventByClientMsgId,
  getEventByEventId,
  getMeta,
  getSession,
  putEvent,
  upsertSession,
  withRwTransaction,
} from "../db/repo";
import type { EventRow, SessionRow } from "../db/schema";

function createDefaultSession(sessionId: string): SessionRow {
  return {
    sessionId,
    name: "",
    type: SessionType.UNSPECIFIED,
    avatarUrl: "",
    unreadCount: "0",
    lastReadSeq: "0",
    lastEventId: "0",
    lastEventSeqId: "0",
    lastEventTs: "0",
    lastEventPreview: "",
    readUptoSeqId: "0",
    readUptoSeqByUser: {},
    draft: "",
  };
}

function messagePreview(event: ChatEvent): string {
  if (event.payload.case === "message") {
    return event.payload.value.content;
  }
  return "";
}

function toEventRow(event: ChatEvent): EventRow {
  const base: EventRow = {
    sessionId: event.sessionId,
    seqId: toIdString(event.seqId),
    eventId: toIdString(event.eventId),
    fromUsername: event.fromUsername,
    timestampMs: toIdString(event.timestampMs),
    payloadCase: "unknown",
    messageType: 0,
    content: "",
    replyToEventId: "0",
    clientMsgId: "",
    mentionedUsernames: [],
    recalled: false,
    edited: false,
    targetEventId: "0",
    readUptoSeqId: "0",
    sessionUpdateKind: 0,
    sessionUpdateNewName: "",
    sessionUpdateNewAvatarUrl: "",
    sessionUpdateAffectedMembers: [],
  };

  const payloadCase = event.payload.case;
  switch (payloadCase) {
    case "message": {
      const payload = event.payload.value;
      return {
        ...base,
        payloadCase: "message",
        messageType: payload.type,
        content: payload.content,
        replyToEventId: toIdString(payload.replyToEventId),
        clientMsgId: payload.clientMsgId,
        mentionedUsernames: payload.mentionedUsernames,
      };
    }
    case "recall":
      return {
        ...base,
        payloadCase: "recall",
        targetEventId: toIdString(event.payload.value.targetEventId),
      };
    case "edit":
      return {
        ...base,
        payloadCase: "edit",
        targetEventId: toIdString(event.payload.value.targetEventId),
        content: event.payload.value.newContent,
      };
    case "readReceipt":
      return {
        ...base,
        payloadCase: "readReceipt",
        readUptoSeqId: toIdString(event.payload.value.readUptoSeqId),
      };
    case "sessionUpdate":
      return {
        ...base,
        payloadCase: "sessionUpdate",
        sessionUpdateKind: event.payload.value.kind,
        sessionUpdateNewName: event.payload.value.newName,
        sessionUpdateNewAvatarUrl: event.payload.value.newAvatarUrl,
        sessionUpdateAffectedMembers: event.payload.value.affectedMembers,
      };
    case undefined:
      return base;
  }

  const unreachable: never = payloadCase;
  throw new Error(`Unhandled payload case: ${String(unreachable)}`);
}

async function applyMessage(event: ChatEvent): Promise<void> {
  const row = toEventRow(event);

  if (row.clientMsgId !== "" && event.seqId >= 0n) {
    const pending = await findPendingEventByClientMsgId(event.sessionId, row.clientMsgId);
    if (pending !== undefined) {
      await deleteEvent(pending.sessionId, pending.seqId);
    }
  }

  await putEvent(row);

  const existing = await getSession(event.sessionId);
  const next = existing ?? createDefaultSession(event.sessionId);
  const meUsername = (await getMeta("me_username")) ?? "";

  const shouldUpdateLastEvent = toBigIntId(row.seqId) > toBigIntId(next.lastEventSeqId);
  if (shouldUpdateLastEvent) {
    next.lastEventId = row.eventId;
    next.lastEventSeqId = row.seqId;
    next.lastEventTs = row.timestampMs;
    next.lastEventPreview = messagePreview(event);
  }

  if (event.fromUsername !== meUsername && event.seqId > toBigIntId(next.lastReadSeq)) {
    next.unreadCount = toIdString(toBigIntId(next.unreadCount) + 1n);
  }

  await upsertSession(next);
}

async function applyRecall(event: ChatEvent): Promise<void> {
  const row = toEventRow(event);
  await putEvent(row);

  const target = await getEventByEventId(row.targetEventId);
  if (target === undefined) {
    return;
  }

  await putEvent({
    ...target,
    recalled: true,
  });
}

async function applyEdit(event: ChatEvent): Promise<void> {
  const row = toEventRow(event);
  await putEvent(row);

  const target = await getEventByEventId(row.targetEventId);
  if (target === undefined) {
    return;
  }

  await putEvent({
    ...target,
    content: row.content,
    edited: true,
  });
}

async function applyReadReceipt(event: ChatEvent): Promise<void> {
  const row = toEventRow(event);
  await putEvent(row);

  const existing = await getSession(event.sessionId);
  const next = existing ?? createDefaultSession(event.sessionId);
  const readUptoSeqId = row.readUptoSeqId;
  const meUsername = (await getMeta("me_username")) ?? "";

  if (next.type === SessionType.GROUP) {
    next.readUptoSeqByUser = {
      ...next.readUptoSeqByUser,
      [event.fromUsername]: readUptoSeqId,
    };
  } else {
    next.readUptoSeqId = readUptoSeqId;
  }
  if (event.fromUsername === meUsername) {
    const currentLastRead = toBigIntId(next.lastReadSeq);
    const incomingLastRead = toBigIntId(readUptoSeqId);
    if (incomingLastRead > currentLastRead) {
      next.lastReadSeq = toIdString(incomingLastRead);
    }
  }

  await upsertSession(next);
}

function applySessionUpdateByKind(
  session: SessionRow,
  kind: SessionUpdateKind,
  newName: string,
  newAvatarUrl: string,
): SessionRow {
  switch (kind) {
    case SessionUpdateKind.UNSPECIFIED:
      return session;
    case SessionUpdateKind.NAME:
      return { ...session, name: newName };
    case SessionUpdateKind.AVATAR:
      return { ...session, avatarUrl: newAvatarUrl };
    case SessionUpdateKind.MEMBER_ADD:
      return session;
    case SessionUpdateKind.MEMBER_KICK:
      return session;
  }

  const unreachable: never = kind;
  throw new Error(`Unhandled session update kind: ${String(unreachable)}`);
}

async function applySessionUpdate(event: ChatEvent): Promise<void> {
  const row = toEventRow(event);
  await putEvent(row);

  if (event.payload.case !== "sessionUpdate") {
    return;
  }

  const existing = await getSession(event.sessionId);
  const base = existing ?? createDefaultSession(event.sessionId);
  const next = applySessionUpdateByKind(
    base,
    event.payload.value.kind,
    event.payload.value.newName,
    event.payload.value.newAvatarUrl,
  );

  await upsertSession(next);
}

export async function applyEvent(event: ChatEvent): Promise<void> {
  await withRwTransaction(async () => {
    const eventId = toIdString(event.eventId);
    const existing = await getEventByEventId(eventId);
    if (existing !== undefined) {
      return;
    }

    const payloadCase = event.payload.case;
    switch (payloadCase) {
      case "message":
        await applyMessage(event);
        return;
      case "recall":
        await applyRecall(event);
        return;
      case "edit":
        await applyEdit(event);
        return;
      case "readReceipt":
        await applyReadReceipt(event);
        return;
      case "sessionUpdate":
        await applySessionUpdate(event);
        return;
      case undefined:
        return;
    }

    const unreachable: never = payloadCase;
    throw new Error(`Unhandled payload case: ${String(unreachable)}`);
  });
}
