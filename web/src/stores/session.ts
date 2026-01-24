import { create } from "zustand";
import type { PushMessage } from "@/gen/gateway/v1/packet_pb";
import { UpdateReadPositionRequest } from "@/gen/gateway/v1/api_pb";
import { sessionClient } from "@/api/client";
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
  lastReadSeq: number; // 用户的已读水位线
  maxSeqId: number;    // 会话的最新消息序列号 (新增字段，用于更准确计算)
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
  // incrementUnread 废弃，改用统一的 updateLastMessage 或 markAsRead
  markAsRead: (sessionId: string, seqId: number) => Promise<void>;
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

  markAsRead: async (sessionId, seqId) => {
    const { sessions } = get();
    const session = sessions.find((s) => s.sessionId === sessionId);
    if (!session) return;

    // 只有当 seqId 大于当前 lastReadSeq 时才更新
    if (seqId <= session.lastReadSeq) return;

    // 乐观更新
    const newUnreadCount = Math.max(0, session.maxSeqId - seqId);
    set((state) => ({
      sessions: state.sessions.map((s) =>
        s.sessionId === sessionId
          ? { ...s, lastReadSeq: seqId, unreadCount: newUnreadCount }
          : s,
      ),
    }));

    // 异步调用 API
    try {
      const req = new UpdateReadPositionRequest({
        sessionId,
        seqId: BigInt(seqId),
      });
      await sessionClient.updateReadPosition(req);
    } catch (err) {
      console.error("Failed to update read position:", err);
      // 如果失败，可能需要回滚？但在已读场景下，通常不需要严格回滚，下次操作会修正。
    }
  },

  updateLastMessage: (sessionId, message) =>
    set((state) => {
      const timestamp = message.timestamp;
      const msgId = message.msgId;
      const seqId = Number(message.seqId); // 转换为 number 方便计算

      // 更新会话的最后消息，并移到顶部
      const updatedSessions = state.sessions.map((s) => {
        if (s.sessionId === sessionId) {
            // 修正：我们更新 maxSeqId。
            const newMaxSeqId = Math.max(s.maxSeqId || 0, seqId);
            
            // 暂时先只更新消息内容和 maxSeqId。
            // 未读数的增加逻辑需要知道 "是否是自己发的" 和 "是否当前会话"。
            // 让我们保持 App.tsx 控制 unread 的增加，但是改为 "Recalculate" 模式。
            
          return {
            ...s,
            maxSeqId: newMaxSeqId,
            lastMessage: {
              msgId,
              seqId: message.seqId,
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
