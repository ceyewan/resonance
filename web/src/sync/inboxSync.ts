import { sessionClient } from "@/api/client";
import type { PushMessage } from "@/gen/gateway/v1/packet_pb";
import { useMessageStore, pushMessageToChatMessage } from "@/stores/message";
import { useSessionStore, type SessionInfo } from "@/stores/session";
import {
  getInboxCursor,
  hydrateLocalSnapshot,
  saveMessage,
  saveSession,
  saveSessions,
  setInboxCursor,
} from "@/localdb/repository";

let syncInFlight: Promise<void> | null = null;

function buildSessionFromPush(push: PushMessage, currentUsername: string): SessionInfo {
  const fallbackName = push.fromUsername === currentUsername ? push.sessionId : push.fromUsername;
  return {
    sessionId: push.sessionId,
    name: push.sessionMeta?.name || fallbackName,
    type: push.sessionMeta?.type || 1,
    unreadCount: push.fromUsername === currentUsername ? 0 : 1,
    lastReadSeq: 0,
    maxSeqId: Number(push.seqId),
    lastMessage: {
      msgId: push.msgId,
      seqId: push.seqId,
      content: push.content,
      type: push.type,
      timestamp: push.timestamp,
    },
  };
}

export function shouldTriggerGapCompensation(push: PushMessage): boolean {
  const session = useSessionStore.getState().getSessionById(push.sessionId);
  if (!session) {
    return false;
  }

  const incomingSeq = push.seqId;
  const localMaxSeq = BigInt(session.maxSeqId);
  const expectedNext = localMaxSeq + 1n;

  // 仅在收到未来序列（中间有缺口）时触发补偿。
  // 重复消息/历史消息（<= localMaxSeq）不触发。
  return incomingSeq > expectedNext;
}

export async function hydrateStoresFromLocal(): Promise<{ hasLocalSnapshot: boolean; cursor: bigint }> {
  const { sessions, messagesBySession, cursor } = await hydrateLocalSnapshot();

  const sessionState = useSessionStore.getState();
  const messageState = useMessageStore.getState();

  if (sessions.length > 0) {
    sessionState.setSessions(sessions);
    if (!sessionState.currentSessionId) {
      sessionState.setCurrentSessionId(sessions[0].sessionId);
    }
  }

  for (const sessionId of Object.keys(messagesBySession)) {
    messageState.setMessages(sessionId, messagesBySession[sessionId]);
  }

  return {
    hasLocalSnapshot: sessions.length > 0 || cursor > 0n,
    cursor,
  };
}

export async function applyIncomingPush(push: PushMessage, currentUsername: string): Promise<void> {
  const sessionStore = useSessionStore.getState();
  const messageStore = useMessageStore.getState();

  const chatMessage = pushMessageToChatMessage(push, currentUsername);
  messageStore.addMessage(chatMessage);
  await saveMessage(chatMessage);

  const existingSession = sessionStore.getSessionById(chatMessage.sessionId);
  if (!existingSession) {
    const newSession = buildSessionFromPush(push, currentUsername);
    sessionStore.upsertSession(newSession);
    await saveSession(newSession);
  } else {
    sessionStore.updateLastMessage(chatMessage.sessionId, push);

    const updated = sessionStore.getSessionById(chatMessage.sessionId);
    if (updated) {
      await saveSession(updated);
    }
  }

  const latestSession = sessionStore.getSessionById(chatMessage.sessionId);
  if (!latestSession) {
    return;
  }

  if (chatMessage.sessionId !== sessionStore.currentSessionId && !chatMessage.isOwn) {
    const newUnread = Math.max(0, Number(chatMessage.seqId) - latestSession.lastReadSeq);
    sessionStore.updateSession(chatMessage.sessionId, { unreadCount: newUnread });
    const unreadUpdated = sessionStore.getSessionById(chatMessage.sessionId);
    if (unreadUpdated) {
      await saveSession(unreadUpdated);
    }
  } else if (chatMessage.sessionId === sessionStore.currentSessionId && !chatMessage.isOwn) {
    await sessionStore.markAsRead(chatMessage.sessionId, Number(chatMessage.seqId));
    const readUpdated = sessionStore.getSessionById(chatMessage.sessionId);
    if (readUpdated) {
      await saveSession(readUpdated);
    }
  }
}

export async function syncInboxDelta(currentUsername: string, pageSize: number = 200): Promise<void> {
  if (syncInFlight) {
    return syncInFlight;
  }

  syncInFlight = (async () => {
  let cursor = await getInboxCursor();

  for (;;) {
    const resp = await sessionClient.pullInboxDelta({
      cursorId: cursor,
      limit: BigInt(pageSize),
    });

    if (resp.events.length === 0) {
      break;
    }

    for (const event of resp.events) {
      if (!event.message) continue;
      await applyIncomingPush(event.message, currentUsername);

      const inboxID = BigInt(event.inboxId);
      if (inboxID > cursor) {
        cursor = inboxID;
      }
    }

    const sessionSnapshots = useSessionStore.getState().sessions;
    await saveSessions(sessionSnapshots);

    await setInboxCursor(cursor);

    if (!resp.hasMore) {
      break;
    }
  }
  })();

  try {
    await syncInFlight;
  } finally {
    syncInFlight = null;
  }
}
