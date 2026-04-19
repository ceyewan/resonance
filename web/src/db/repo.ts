import Dexie from "dexie";

import {
  type EventRow,
  type MetaRow,
  type OutboxRow,
  type SessionRow,
  db,
} from "./schema";

export async function getSession(sessionId: string): Promise<SessionRow | undefined> {
  return db.sessions.get(sessionId);
}

export async function upsertSession(row: SessionRow): Promise<void> {
  await db.sessions.put(row);
}

export async function putEvent(row: EventRow): Promise<void> {
  await db.events.put(row);
}

export async function deleteEvent(sessionId: string, seqId: string): Promise<void> {
  await db.events.delete([sessionId, seqId]);
}

export async function getEventByEventId(eventId: string): Promise<EventRow | undefined> {
  return db.events.where("eventId").equals(eventId).first();
}

export async function getEventsBySession(sessionId: string): Promise<EventRow[]> {
  return db.events
    .where("[sessionId+timestampMs]")
    .between([sessionId, Dexie.minKey], [sessionId, Dexie.maxKey])
    .toArray();
}

export async function findPendingEventByClientMsgId(
  sessionId: string,
  clientMsgId: string,
  isPending: (seqId: string) => boolean,
): Promise<EventRow | undefined> {
  return db.events
    .where("clientMsgId")
    .equals(clientMsgId)
    .and((row) => row.sessionId === sessionId && isPending(row.seqId))
    .first();
}

export async function setMeta(row: MetaRow): Promise<void> {
  await db.meta.put(row);
}

export async function getMeta(key: string): Promise<string | undefined> {
  const row = await db.meta.get(key);
  return row?.value;
}

export async function enqueueOutbox(row: OutboxRow): Promise<void> {
  await db.outbox.put(row);
}

export async function markOutboxAcked(
  clientSeq: string,
  ackedEventId: string,
  ackedSeqId: string,
): Promise<void> {
  await db.outbox.update(clientSeq, {
    status: "acked",
    ackedEventId,
    ackedSeqId,
    updatedAtMs: Date.now(),
  });
}

export async function getOutboxByStatus(status: OutboxRow["status"]): Promise<OutboxRow[]> {
  return db.outbox.where("status").equals(status).toArray();
}

export async function withRwTransaction<T>(
  fn: () => Promise<T>,
): Promise<T> {
  return db.transaction("rw", db.sessions, db.events, db.outbox, db.meta, fn);
}

export async function clearAll(): Promise<void> {
  await db.transaction("rw", db.sessions, db.events, db.outbox, db.meta, async () => {
    await db.sessions.clear();
    await db.events.clear();
    await db.outbox.clear();
    await db.meta.clear();
  });
}
