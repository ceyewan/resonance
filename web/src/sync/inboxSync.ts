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

const syncInFlight = new Map<string, Promise<void>>();
const syncRetryState = new Map<string, { failures: number; nextAllowedAt: number }>();
const BASE_RETRY_DELAY_MS = 1000;
const MAX_RETRY_DELAY_MS = 30000;
interface ApplyIncomingOptions {
  suppressReadReceipt?: boolean;
}

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

export async function hydrateStoresFromLocal(
  currentUsername: string,
): Promise<{ hasLocalSnapshot: boolean; cursor: bigint }> {
  const { sessions, messagesBySession, cursor } = await hydrateLocalSnapshot(currentUsername);

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

export async function applyIncomingPush(
  push: PushMessage,
  currentUsername: string,
  options: ApplyIncomingOptions = {},
): Promise<void> {
  const sessionStore = useSessionStore.getState();
  const messageStore = useMessageStore.getState();

  const chatMessage = pushMessageToChatMessage(push, currentUsername);
  messageStore.addMessage(chatMessage);
  await saveMessage(currentUsername, chatMessage);

  const existingSession = sessionStore.getSessionById(chatMessage.sessionId);
  if (!existingSession) {
    const newSession = buildSessionFromPush(push, currentUsername);
    sessionStore.upsertSession(newSession);
    await saveSession(currentUsername, newSession);
  } else {
    sessionStore.updateLastMessage(chatMessage.sessionId, push);

    const updated = sessionStore.getSessionById(chatMessage.sessionId);
    if (updated) {
      await saveSession(currentUsername, updated);
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
      await saveSession(currentUsername, unreadUpdated);
    }
  } else if (chatMessage.sessionId === sessionStore.currentSessionId && !chatMessage.isOwn) {
    if (options.suppressReadReceipt) {
      sessionStore.updateSession(chatMessage.sessionId, {
        lastReadSeq: Number(chatMessage.seqId),
        unreadCount: Math.max(0, latestSession.maxSeqId - Number(chatMessage.seqId)),
      });
      const readUpdated = sessionStore.getSessionById(chatMessage.sessionId);
      if (readUpdated) {
        await saveSession(currentUsername, readUpdated);
      }
    } else {
      await sessionStore.markAsRead(chatMessage.sessionId, Number(chatMessage.seqId));
      const readUpdated = sessionStore.getSessionById(chatMessage.sessionId);
      if (readUpdated) {
        await saveSession(currentUsername, readUpdated);
      }
    }
  }
}

export async function syncInboxDelta(currentUsername: string, pageSize: number = 200): Promise<void> {
  const retry = syncRetryState.get(currentUsername);
  if (retry && Date.now() < retry.nextAllowedAt) {
    return;
  }

  const existing = syncInFlight.get(currentUsername);
  if (existing) {
    return existing;
  }

  const running = (async () => {
    let cursor = await getInboxCursor(currentUsername);
    const pendingReadReceipts = new Map<string, number>();

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
        await applyIncomingPush(event.message, currentUsername, { suppressReadReceipt: true });

        const currentSessionId = useSessionStore.getState().currentSessionId;
        if (event.message.sessionId === currentSessionId && event.message.fromUsername !== currentUsername) {
          const seq = Number(event.message.seqId);
          const existingSeq = pendingReadReceipts.get(event.message.sessionId) ?? 0;
          pendingReadReceipts.set(event.message.sessionId, Math.max(existingSeq, seq));
        }

        const inboxID = BigInt(event.inboxId);
        if (inboxID > cursor) {
          cursor = inboxID;
        }
      }

      const sessionSnapshots = useSessionStore.getState().sessions;
      await saveSessions(currentUsername, sessionSnapshots);

      await setInboxCursor(currentUsername, cursor);

      if (!resp.hasMore) {
        break;
      }
    }

    if (pendingReadReceipts.size > 0) {
      const sessionStore = useSessionStore.getState();
      for (const [sessionID, seq] of pendingReadReceipts) {
        await sessionStore.markAsRead(sessionID, seq);
      }
      await saveSessions(currentUsername, sessionStore.sessions);
    }
  })();
  syncInFlight.set(currentUsername, running);

  try {
    await running;
    syncRetryState.delete(currentUsername);
  } catch (err) {
    const prevFailures = syncRetryState.get(currentUsername)?.failures ?? 0;
    const failures = prevFailures + 1;
    const delay = Math.min(BASE_RETRY_DELAY_MS * 2 ** (failures - 1), MAX_RETRY_DELAY_MS);
    syncRetryState.set(currentUsername, {
      failures,
      nextAllowedAt: Date.now() + delay,
    });
    throw err;
  } finally {
    if (syncInFlight.get(currentUsername) === running) {
      syncInFlight.delete(currentUsername);
    }
  }
}
