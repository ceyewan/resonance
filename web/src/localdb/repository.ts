import { db, SYNC_KEYS, type DBMessage, type DBSession } from "@/localdb/db";
import type { ChatMessage } from "@/stores/message";
import type { SessionInfo } from "@/stores/session";

function toDBSession(session: SessionInfo): DBSession {
  return {
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

function toDBMessage(message: ChatMessage): DBMessage {
  const seq = message.seqId.toString();
  return {
    key: `${message.sessionId}:${seq}`,
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

export async function saveSessions(sessions: SessionInfo[]): Promise<void> {
  if (sessions.length === 0) return;
  const rows = sessions.map(toDBSession);
  await db.sessions.bulkPut(rows);
}

export async function saveSession(session: SessionInfo): Promise<void> {
  await db.sessions.put(toDBSession(session));
}

export async function loadSessions(): Promise<SessionInfo[]> {
  const rows = await db.sessions.orderBy("updatedAt").reverse().toArray();
  return rows.map(fromDBSession);
}

export async function saveMessages(messages: ChatMessage[]): Promise<void> {
  if (messages.length === 0) return;
  const rows = messages.map(toDBMessage);
  await db.messages.bulkPut(rows);
}

export async function saveMessage(message: ChatMessage): Promise<void> {
  await db.messages.put(toDBMessage(message));
}

export async function loadAllMessagesGrouped(): Promise<Record<string, ChatMessage[]>> {
  const rows = await db.messages.toArray();
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

export async function loadSessionMessages(sessionId: string): Promise<ChatMessage[]> {
  const rows = await db.messages.where("sessionId").equals(sessionId).toArray();
  const messages = rows.map(fromDBMessage);
  messages.sort((a, b) => {
    if (a.seqId < b.seqId) return -1;
    if (a.seqId > b.seqId) return 1;
    return 0;
  });
  return messages;
}

export async function getInboxCursor(): Promise<bigint> {
  const item = await db.syncState.get(SYNC_KEYS.INBOX_CURSOR);
  if (!item) return 0n;
  try {
    return BigInt(item.value);
  } catch {
    return 0n;
  }
}

export async function setInboxCursor(cursor: bigint): Promise<void> {
  await db.syncState.put({
    key: SYNC_KEYS.INBOX_CURSOR,
    value: cursor.toString(),
    updatedAt: Date.now(),
  });
}

export async function hydrateLocalSnapshot(): Promise<{
  sessions: SessionInfo[];
  messagesBySession: Record<string, ChatMessage[]>;
  cursor: bigint;
}> {
  const [sessions, messagesBySession, cursor] = await Promise.all([
    loadSessions(),
    loadAllMessagesGrouped(),
    getInboxCursor(),
  ]);

  return { sessions, messagesBySession, cursor };
}
