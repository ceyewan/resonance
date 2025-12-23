import { create } from "zustand";

export interface ChatMessage {
  msgId: string;
  sessionId: string;
  senderId: string;
  senderName: string;
  senderAvatar?: string;
  content: string;
  type: "text" | "image" | "file" | "system";
  timestamp: number;
  status: "sending" | "sent" | "failed";
  isOwn: boolean;
}

interface MessageState {
  messages: Record<string, ChatMessage[]>; // sessionId -> messages
  isLoading: boolean;
  error: string | null;

  getSessionMessages: (sessionId: string) => ChatMessage[];
  addMessage: (message: ChatMessage) => void;
  addMessages: (sessionId: string, messages: ChatMessage[]) => void;
  updateMessage: (msgId: string, updates: Partial<ChatMessage>) => void;
  removeMessage: (msgId: string) => void;
  clearSessionMessages: (sessionId: string) => void;
  setIsLoading: (loading: boolean) => void;
  setError: (error: string | null) => void;
  reset: () => void;
}

export const useMessageStore = create<MessageState>((set, get) => ({
  messages: {},
  isLoading: false,
  error: null,

  getSessionMessages: (sessionId) => {
    return get().messages[sessionId] || [];
  },

  addMessage: (message) =>
    set((state) => {
      const sessionId = message.sessionId;
      const messages = state.messages[sessionId] || [];
      return {
        messages: {
          ...state.messages,
          [sessionId]: [...messages, message],
        },
      };
    }),

  addMessages: (sessionId, newMessages) =>
    set((state) => {
      const existing = state.messages[sessionId] || [];
      return {
        messages: {
          ...state.messages,
          [sessionId]: [...existing, ...newMessages],
        },
      };
    }),

  updateMessage: (msgId, updates) =>
    set((state) => {
      const newMessages = { ...state.messages };
      for (const sessionId in newMessages) {
        newMessages[sessionId] = newMessages[sessionId].map((msg) =>
          msg.msgId === msgId ? { ...msg, ...updates } : msg,
        );
      }
      return { messages: newMessages };
    }),

  removeMessage: (msgId) =>
    set((state) => {
      const newMessages = { ...state.messages };
      for (const sessionId in newMessages) {
        newMessages[sessionId] = newMessages[sessionId].filter(
          (msg) => msg.msgId !== msgId,
        );
      }
      return { messages: newMessages };
    }),

  clearSessionMessages: (sessionId) =>
    set((state) => {
      const newMessages = { ...state.messages };
      delete newMessages[sessionId];
      return { messages: newMessages };
    }),

  setIsLoading: (loading) =>
    set({
      isLoading: loading,
    }),

  setError: (error) =>
    set({
      error,
    }),

  reset: () =>
    set({
      messages: {},
      isLoading: false,
      error: null,
    }),
}));
