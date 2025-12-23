import { create } from "zustand";

export interface SessionInfo {
  sessionId: string;
  userId: string;
  userName: string;
  userAvatar?: string;
  isGroup: boolean;
  groupName?: string;
  groupAvatar?: string;
  unreadCount: number;
  lastMessage?: string;
  lastMessageTime?: number;
}

interface SessionState {
  sessions: SessionInfo[];
  currentSession: SessionInfo | null;
  isLoading: boolean;

  setSessions: (sessions: SessionInfo[]) => void;
  setCurrentSession: (session: SessionInfo | null) => void;
  addSession: (session: SessionInfo) => void;
  updateSession: (sessionId: string, updates: Partial<SessionInfo>) => void;
  removeSession: (sessionId: string) => void;
  setIsLoading: (loading: boolean) => void;
  reset: () => void;
}

export const useSessionStore = create<SessionState>((set) => ({
  sessions: [],
  currentSession: null,
  isLoading: false,

  setSessions: (sessions) =>
    set({
      sessions,
    }),

  setCurrentSession: (session) =>
    set({
      currentSession: session,
    }),

  addSession: (session) =>
    set((state) => {
      const exists = state.sessions.find(
        (s) => s.sessionId === session.sessionId,
      );
      if (exists) return state;
      return {
        sessions: [session, ...state.sessions],
      };
    }),

  updateSession: (sessionId, updates) =>
    set((state) => ({
      sessions: state.sessions.map((s) =>
        s.sessionId === sessionId ? { ...s, ...updates } : s,
      ),
      currentSession:
        state.currentSession?.sessionId === sessionId
          ? { ...state.currentSession, ...updates }
          : state.currentSession,
    })),

  removeSession: (sessionId) =>
    set((state) => ({
      sessions: state.sessions.filter((s) => s.sessionId !== sessionId),
      currentSession:
        state.currentSession?.sessionId === sessionId
          ? null
          : state.currentSession,
    })),

  setIsLoading: (loading) =>
    set({
      isLoading: loading,
    }),

  reset: () =>
    set({
      sessions: [],
      currentSession: null,
      isLoading: false,
    }),
}));
