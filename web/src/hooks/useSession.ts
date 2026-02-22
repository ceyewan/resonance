import { useCallback, useState } from "react";
import { useAuthStore } from "@/stores/auth";
import { useSessionStore } from "@/stores/session";
import { useMessageStore, pushMessageToChatMessage } from "@/stores/message";
import { sessionClient } from "@/api/client";
import type { SessionInfo as ProtoSessionInfo } from "@/gen/gateway/v1/api_pb";
import type { PushMessage } from "@/gen/gateway/v1/packet_pb";
import { ERROR_MESSAGES, UI_CONFIG } from "@/constants";
import type { SessionInfo } from "@/stores/session";
import { saveMessages, saveSessions } from "@/localdb/repository";
import { hydrateStoresFromLocal, syncInboxDelta } from "@/sync/inboxSync";

interface UseSessionReturn {
  // 状态
  sessions: SessionInfo[];
  currentSession: SessionInfo | null;
  isLoading: boolean;
  error: string | null;

  // 操作
  loadSessions: () => Promise<void>;
  loadHistoryMessages: (sessionId: string, limit?: number, beforeSeq?: bigint) => Promise<void>;
  selectSession: (sessionId: string | null) => void;
  clearError: () => void;
}

/**
 * 会话管理 Hook
 * 处理会话列表加载、消息拉取、会话切换
 */
export function useSession(): UseSessionReturn {
  const { user } = useAuthStore();
  const {
    sessions,
    getCurrentSession,
    setCurrentSessionId,
    setSessions,
    setIsLoading,
    setError,
    clearError,
    markAsRead,
  } = useSessionStore();

  const { setMessages, prependMessages, getSessionMessages } = useMessageStore();

  const [isLoading, setIsLoadingState] = useState(false);
  const [error, setErrorState] = useState<string | null>(null);

  // 转换 Protobuf SessionInfo 到前端 SessionInfo
  const convertSessionInfo = (proto: ProtoSessionInfo): SessionInfo => {
    return {
      sessionId: proto.sessionId,
      name: proto.name,
      type: proto.type,
      avatarUrl: proto.avatarUrl || undefined,
      unreadCount: Number(proto.unreadCount),
      lastReadSeq: Number(proto.lastReadSeq),
      maxSeqId: Number(proto.lastReadSeq) + Number(proto.unreadCount),
      lastMessage: proto.lastMessage
        ? {
            msgId: proto.lastMessage.msgId,
            seqId: proto.lastMessage.seqId,
            content: proto.lastMessage.content,
            type: proto.lastMessage.type,
            timestamp: proto.lastMessage.timestamp,
          }
        : undefined,
    };
  };

  // 加载会话列表
  const loadSessions = useCallback(async () => {
    setIsLoadingState(true);
    setIsLoading(true);
    setErrorState(null);
    setError(null);

    try {
      const currentUsername = user?.username;
      if (!currentUsername) {
        throw new Error("unauthorized: missing username");
      }

      const { hasLocalSnapshot } = await hydrateStoresFromLocal(currentUsername);

      if (!hasLocalSnapshot) {
        const response = await sessionClient.getSessionList({});
        const convertedSessions = response.sessions.map(convertSessionInfo);
        setSessions(convertedSessions);
        await saveSessions(currentUsername, convertedSessions);

        // 如果没有当前选中的会话，自动选中第一个
        if (convertedSessions.length > 0 && !getCurrentSession()) {
          setCurrentSessionId(convertedSessions[0].sessionId);
        }
      } else if (!getCurrentSession()) {
        const localSessions = useSessionStore.getState().sessions;
        if (localSessions.length > 0) {
          setCurrentSessionId(localSessions[0].sessionId);
        }
      }

      if (user?.username) {
        syncInboxDelta(user.username).catch((err) => {
          console.error("[useSession] Failed to sync inbox delta:", err);
        });
      }
    } catch (err) {
      const errorMsg = err instanceof Error ? err.message : ERROR_MESSAGES.SESSION_LOAD_FAILED;
      setErrorState(errorMsg);
      setError(errorMsg);
    } finally {
      setIsLoadingState(false);
      setIsLoading(false);
    }
  }, [setSessions, setCurrentSessionId, setIsLoading, setError, getCurrentSession, user?.username]);

  // 加载历史消息
  const loadHistoryMessages = useCallback(
    async (sessionId: string, limit: number = UI_CONFIG.MESSAGES_PAGE_SIZE, beforeSeq?: bigint) => {
      try {
        const response = await sessionClient.getHistoryMessages({
          sessionId,
          limit: BigInt(limit),
          beforeSeq: beforeSeq || BigInt(0),
        });

        const messages = response.messages.map((msg) =>
          pushMessageToChatMessage(msg as PushMessage, user?.username || "", sessionId),
        );

        // 如果指定了 beforeSeq，说明是加载更多，需要前置
        // 否则是首次加载，直接设置
        if (beforeSeq) {
          prependMessages(sessionId, messages);
        } else {
          setMessages(sessionId, messages);
        }
        if (user?.username) {
          await saveMessages(user.username, messages);
        }
      } catch (err) {
        console.error("[useSession] Failed to load recent messages:", err);
        throw err;
      }
    },
    [setMessages, prependMessages, user?.username],
  );

  // 选择会话
  const selectSession = useCallback(
    (sessionId: string | null) => {
      if (sessionId) {
        // 标记已读
        const session = sessions.find((s) => s.sessionId === sessionId);
        if (session) {
          markAsRead(sessionId, session.maxSeqId);
        }

        // 如果该会话还没有消息，加载历史消息
        if (getSessionMessages(sessionId).length === 0) {
          loadHistoryMessages(sessionId).catch(console.error);
        }
      }
      setCurrentSessionId(sessionId);
    },
    [setCurrentSessionId, sessions, markAsRead, getSessionMessages, loadHistoryMessages],
  );

  const clearLocalError = useCallback(() => {
    setErrorState(null);
    clearError();
  }, [clearError]);

  return {
    sessions,
    currentSession: getCurrentSession(),
    isLoading,
    error,
    loadSessions,
    loadHistoryMessages,
    selectSession,
    clearError: clearLocalError,
  };
}

/**
 * 单个会话的 Hook
 * 用于在会话详情页使用
 */
export function useCurrentSession() {
  const { currentSession, loadHistoryMessages } = useSession();
  const { getSessionMessages, isLoading } = useMessageStore();

  const messages = currentSession ? getSessionMessages(currentSession.sessionId) : [];

  const loadMore = useCallback(
    async (limit?: number) => {
      if (!currentSession) return;

      const oldestMessage = messages[0];
      const beforeSeq = oldestMessage ? oldestMessage.seqId : undefined;

      await loadHistoryMessages(currentSession.sessionId, limit, beforeSeq);
    },
    [currentSession, loadHistoryMessages, messages],
  );

  return {
    session: currentSession,
    messages,
    isLoading,
    loadMore,
  };
}
