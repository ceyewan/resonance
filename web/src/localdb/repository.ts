import { db, SYNC_KEYS, type DBMessage, type DBSession } from "@/localdb/db";
import type { ChatMessage } from "@/stores/message";
import type { SessionInfo } from "@/stores/session";

function toDBSession(ownerUsername: string, session: SessionInfo): DBSession {
  return {
    ownerUsername,
    sessionId: session.sessionId,
    name: session.name,
    type: Number(session.type),
    avatarUrl: session.avatarUrl,
    unreadCount: session.unreadCount,
    lastReadSeq: session.lastReadSeq,
    maxSeqId: session.maxSeqId,
    lastMessage: session.lastMessage
      ? {
          msgId: session.lastMessage.msgId.toString(),
          seqId: session.lastMessage.seqId.toString(),
          content: session.lastMessage.content,
          type: session.lastMessage.type,
          timestamp: session.lastMessage.timestamp.toString(),
        }
      : undefined,
    updatedAt: Date.now(),
  };
}

function fromDBSession(item: DBSession): SessionInfo {
  return {
    sessionId: item.sessionId,
    name: item.name,
    type: item.type,
    avatarUrl: item.avatarUrl,
    unreadCount: item.unreadCount,
    lastReadSeq: item.lastReadSeq,
    maxSeqId: item.maxSeqId,
    lastMessage: item.lastMessage
      ? {
          msgId: BigInt(item.lastMessage.msgId),
          seqId: BigInt(item.lastMessage.seqId),
          content: item.lastMessage.content,
          type: item.lastMessage.type,
          timestamp: BigInt(item.lastMessage.timestamp),
        }
      : undefined,
  };
}

function toDBMessage(ownerUsername: string, message: ChatMessage): DBMessage {
  const seq = message.seqId.toString();
  return {
    key: `${ownerUsername}:${message.sessionId}:${seq}`,
    ownerUsername,
    sessionId: message.sessionId,
    seqId: seq,
    msgId: message.msgId,
    fromUsername: message.fromUsername,
    toUsername: message.toUsername,
    content: message.content,
    msgType: message.msgType,
    timestamp: message.timestamp.toString(),
    isOwn: message.isOwn,
    status: message.status,
  };
}

function fromDBMessage(item: DBMessage): ChatMessage {
  return {
    msgId: item.msgId,
    sessionId: item.sessionId,
    seqId: BigInt(item.seqId),
    fromUsername: item.fromUsername,
    toUsername: item.toUsername,
    content: item.content,
    msgType: item.msgType,
    timestamp: BigInt(item.timestamp),
    isOwn: item.isOwn,
    status: item.status,
  };
}

function getInboxCursorKey(username: string): string {
  return `${SYNC_KEYS.INBOX_CURSOR}:${username}`;
}

export async function saveSessions(username: string, sessions: SessionInfo[]): Promise<void> {
  if (sessions.length === 0) return;
  const rows = sessions.map((item) => toDBSession(username, item));
  await db.sessions.bulkPut(rows);
}

export async function saveSession(username: string, session: SessionInfo): Promise<void> {
  await db.sessions.put(toDBSession(username, session));
}

export async function loadSessions(username: string): Promise<SessionInfo[]> {
  const rows = await db.sessions.where("ownerUsername").equals(username).sortBy("updatedAt");
  rows.reverse();
  return rows.map(fromDBSession);
}

export async function saveMessages(username: string, messages: ChatMessage[]): Promise<void> {
  if (messages.length === 0) return;
  const rows = messages.map((item) => toDBMessage(username, item));
  await db.messages.bulkPut(rows);
}

export async function saveMessage(username: string, message: ChatMessage): Promise<void> {
  await db.messages.put(toDBMessage(username, message));
}

export async function loadAllMessagesGrouped(username: string): Promise<Record<string, ChatMessage[]>> {
  const rows = await db.messages.where("ownerUsername").equals(username).toArray();
  const grouped: Record<string, ChatMessage[]> = {};

  for (const row of rows) {
    const msg = fromDBMessage(row);
    if (!grouped[msg.sessionId]) {
      grouped[msg.sessionId] = [];
    }
    grouped[msg.sessionId].push(msg);
  }

  for (const sessionId in grouped) {
    grouped[sessionId].sort((a, b) => {
      if (a.seqId < b.seqId) return -1;
      if (a.seqId > b.seqId) return 1;
      return 0;
    });
  }

  return grouped;
}

export async function loadSessionMessages(username: string, sessionId: string): Promise<ChatMessage[]> {
  const rows = await db.messages
    .where("[ownerUsername+sessionId]")
    .equals([username, sessionId])
    .toArray();
  const messages = rows.map(fromDBMessage);
  messages.sort((a, b) => {
    if (a.seqId < b.seqId) return -1;
    if (a.seqId > b.seqId) return 1;
    return 0;
  });
  return messages;
}

export async function getInboxCursor(username: string): Promise<bigint> {
  const item = await db.syncState.get(getInboxCursorKey(username));
  if (!item) return 0n;
  try {
    return BigInt(item.value);
  } catch (error) {
    console.warn("[localdb] invalid inbox cursor, fallback to 0", {
      username,
      value: item.value,
      error,
    });
    return 0n;
  }
}

export async function setInboxCursor(username: string, cursor: bigint): Promise<void> {
  await db.syncState.put({
    key: getInboxCursorKey(username),
    value: cursor.toString(),
    updatedAt: Date.now(),
  });
}

export async function hydrateLocalSnapshot(username: string): Promise<{
  sessions: SessionInfo[];
  messagesBySession: Record<string, ChatMessage[]>;
  cursor: bigint;
}> {
  const [sessions, messagesBySession, cursor] = await Promise.all([
    loadSessions(username),
    loadAllMessagesGrouped(username),
    getInboxCursor(username),
  ]);

  return { sessions, messagesBySession, cursor };
}
