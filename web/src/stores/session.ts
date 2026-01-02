import { create } from "zustand";
import type { PushMessage } from "@/gen/gateway/v1/packet_pb";
import { SESSION_TYPES } from "@/constants";

/**
 * 会话信息
 * 对应 protobuf resonance.gateway.v1.SessionInfo
 */
export interface SessionInfo {
  sessionId: string;
  name: string;
  type: keyof typeof SESSION_TYPES | number;
  avatarUrl?: string;
  unreadCount: number;
  lastReadSeq: number;
  lastMessage?: {
    msgId: bigint;
    seqId: bigint;
    content: string;
    type: string;
    timestamp: bigint;
  };
}

interface SessionState {
  // 状态
  sessions: SessionInfo[];
  currentSessionId: string | null;
  isLoading: boolean;
  error: string | null;

  // Selectors
  getCurrentSession: () => SessionInfo | null;
  getSessionById: (sessionId: string) => SessionInfo | undefined;
  getTotalUnreadCount: () => number;

  // Actions
  setSessions: (sessions: SessionInfo[]) => void;
  upsertSession: (session: SessionInfo) => void;
  updateSession: (sessionId: string, updates: Partial<SessionInfo>) => void;
  removeSession: (sessionId: string) => void;
  setCurrentSessionId: (sessionId: string | null) => void;
  incrementUnread: (sessionId: string, amount?: number) => void;
  clearUnread: (sessionId: string) => void;
  updateLastMessage: (sessionId: string, message: PushMessage) => void;
  setIsLoading: (loading: boolean) => void;
  setError: (error: string | null) => void;
  clearError: () => void;
  reset: () => void;
}

export const useSessionStore = create<SessionState>((set, get) => ({
  // 初始状态
  sessions: [],
  currentSessionId: null,
  isLoading: false,
  error: null,

  // Selectors
  getCurrentSession: () => {
    const { sessions, currentSessionId } = get();
    return sessions.find((s) => s.sessionId === currentSessionId) || null;
  },

  getSessionById: (sessionId) => {
    return get().sessions.find((s) => s.sessionId === sessionId);
  },

  getTotalUnreadCount: () => {
    return get().sessions.reduce((sum, s) => sum + s.unreadCount, 0);
  },

  // Actions
  setSessions: (sessions) =>
    set({
      sessions,
    }),

  upsertSession: (session) =>
    set((state) => {
      const exists = state.sessions.find(
        (s) => s.sessionId === session.sessionId,
      );
      if (exists) {
        // 更新现有会话
        return {
          sessions: state.sessions.map((s) =>
            s.sessionId === session.sessionId
              ? { ...s, ...session }
              : s,
          ),
        };
      }
      // 添加新会话到顶部
      return {
        sessions: [session, ...state.sessions],
      };
    }),

  updateSession: (sessionId, updates) =>
    set((state) => {
      const sessions = state.sessions.map((s) =>
        s.sessionId === sessionId ? { ...s, ...updates } : s,
      );
      return { sessions };
    }),

  removeSession: (sessionId) =>
    set((state) => ({
      sessions: state.sessions.filter((s) => s.sessionId !== sessionId),
      currentSessionId:
        state.currentSessionId === sessionId
          ? null
          : state.currentSessionId,
    })),

  setCurrentSessionId: (sessionId) =>
    set({
      currentSessionId: sessionId,
    }),

  incrementUnread: (sessionId, amount = 1) =>
    set((state) => ({
      sessions: state.sessions.map((s) =>
        s.sessionId === sessionId
          ? { ...s, unreadCount: s.unreadCount + amount }
          : s,
      ),
    })),

  clearUnread: (sessionId) =>
    set((state) => ({
      sessions: state.sessions.map((s) =>
        s.sessionId === sessionId
          ? { ...s, unreadCount: 0 }
          : s,
      ),
    })),

  updateLastMessage: (sessionId, message) =>
    set((state) => {
      const timestamp = message.timestamp;
      const msgId = message.msgId;
      const seqId = message.seqId;

      // 更新会话的最后消息，并移到顶部
      const updatedSessions = state.sessions.map((s) => {
        if (s.sessionId === sessionId) {
          return {
            ...s,
            lastMessage: {
              msgId,
              seqId,
              content: message.content,
              type: message.type,
              timestamp,
            },
          };
        }
        return s;
      });

      // 将有新消息的会话移到顶部
      const targetSession = updatedSessions.find(
        (s) => s.sessionId === sessionId,
      );
      if (!targetSession) {
        return { sessions: updatedSessions };
      }

      const otherSessions = updatedSessions.filter(
        (s) => s.sessionId !== sessionId,
      );

      return {
        sessions: [targetSession, ...otherSessions],
      };
    }),

  setIsLoading: (isLoading) =>
    set({
      isLoading,
    }),

  setError: (error) =>
    set({
      error,
    }),

  clearError: () =>
    set({
      error: null,
    }),

  reset: () =>
    set({
      sessions: [],
      currentSessionId: null,
      isLoading: false,
      error: null,
    }),
}));
