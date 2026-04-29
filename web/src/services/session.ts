import type { ChatEvent } from "@gen/common/v1/event_pb";
import type { SessionInfo } from "@gen/common/v1/view_pb";
import { SessionType } from "@gen/common/v1/session_pb";

import { getSession, getSessions, replaceSessions, setMeta, upsertSession } from "../db/repo";
import type { SessionRow } from "../db/schema";
import { toBigIntId, toIdString } from "../lib/id";
import { applyEvent } from "../sync/applier";

function createEmptySessionRow(sessionId: string): SessionRow {
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

function previewFromEvent(event: ChatEvent | undefined): string {
  if (event === undefined) {
    return "";
  }

  switch (event.payload.case) {
    case "message":
      return event.payload.value.content;
    case "recall":
      return "[消息已撤回]";
    case "edit":
      return event.payload.value.newContent;
    case "readReceipt":
      return "";
    case "sessionUpdate":
      return event.payload.value.newName;
    case undefined:
      return "";
  }
}

function normalizeUnreadCount(value: bigint): string {
  return toIdString(value > 0n ? value : 0n);
}

function toSessionRow(snapshot: SessionInfo, existing: SessionRow | undefined): SessionRow {
  const base = existing ?? createEmptySessionRow(snapshot.sessionId);
  const lastEvent = snapshot.lastEvent;

  return {
    ...base,
    sessionId: snapshot.sessionId,
    name: snapshot.name,
    type: snapshot.type,
    avatarUrl: snapshot.avatarUrl,
    unreadCount: normalizeUnreadCount(snapshot.unreadCount),
    lastReadSeq: toIdString(snapshot.lastReadSeq),
    lastEventId: lastEvent === undefined ? "0" : toIdString(lastEvent.eventId),
    lastEventSeqId: lastEvent === undefined ? "0" : toIdString(lastEvent.seqId),
    lastEventTs: lastEvent === undefined ? "0" : toIdString(lastEvent.timestampMs),
    lastEventPreview: previewFromEvent(lastEvent),
  };
}

export async function syncSessionList(): Promise<SessionRow[]> {
  const [{ sessionClient }, existingRows] = await Promise.all([
    import("../api/clients"),
    getSessions(),
  ]);
  const existingById = new Map(existingRows.map((row) => [row.sessionId, row]));
  const { getAccessToken } = await import("../api/transport");
  if (getAccessToken() === "mock-token-123") {
    return existingRows;
  }
  const response = await sessionClient.getSessionList({});
  const nextRows = response.sessions.map((snapshot) =>
    toSessionRow(snapshot, existingById.get(snapshot.sessionId)),
  );

  await replaceSessions(nextRows);
  return nextRows;
}

export type LoadHistoryOptions = {
  limit?: bigint;
  beforeSeqId?: bigint;
};

const DEFAULT_HISTORY_LIMIT = 50n;

export async function loadHistory(
  sessionId: string,
  options: LoadHistoryOptions = {},
): Promise<number> {
  const { getAccessToken } = await import("../api/transport");
  if (getAccessToken() === "mock-token-123") {
    return 0;
  }
  const { sessionClient } = await import("../api/clients");
  const response = await sessionClient.getHistoryEvents({
    sessionId,
    limit: options.limit ?? DEFAULT_HISTORY_LIMIT,
    beforeSeq: options.beforeSeqId ?? 0n,
  });

  for (const event of response.events) {
    await applyEvent(event);
  }
  return response.events.length;
}

export async function markSessionRead(sessionId: string, seqId: bigint): Promise<void> {
  const { sessionClient } = await import("../api/clients");
  const response = await sessionClient.updateReadPosition({
    sessionId,
    seqId,
  });

  const existing = await getSession(sessionId);
  if (existing === undefined) {
    return;
  }

  const nextLastRead = toBigIntId(existing.lastReadSeq);
  const resolvedSeqId = seqId > nextLastRead ? seqId : nextLastRead;
  await upsertSession({
    ...existing,
    lastReadSeq: toIdString(resolvedSeqId),
    unreadCount: toIdString(response.unreadCount),
  });
}

export async function persistCurrentUsername(username: string): Promise<void> {
  await setMeta({
    key: "me_username",
    value: username,
  });
}
