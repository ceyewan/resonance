import { create } from "zustand";
import type { PushMessage } from "@/gen/gateway/v1/packet_pb";
import { MESSAGE_STATUS, DEFAULTS } from "@/constants";

/**
 * 聊天消息
 * 对应 protobuf resonance.gateway.v1.PushMessage
 */
export interface ChatMessage {
  // 临时消息 ID（本地生成）或服务器返回的 msgId
  msgId: string;
  // 会话 ID
  sessionId: string;
  // 序列 ID（会话内逻辑时钟）
  seqId: bigint;
  // 发送者用户名
  fromUsername: string;
  // 接收者用户名（群聊时为空）
  toUsername?: string;
  // 消息内容
  content: string;
  // 消息类型
  msgType: string;
  // 时间戳
  timestamp: bigint;
  // 是否为自己发送的消息
  isOwn: boolean;
  // 发送状态
  status: "sending" | "sent" | "failed";
}

interface MessageState {
  // 状态
  // Map: sessionId -> 消息列表
  messages: Record<string, ChatMessage[]>;
  isLoading: boolean;
  error: string | null;

  // Selectors
  getSessionMessages: (sessionId: string) => ChatMessage[];
  getMessageById: (msgId: string) => ChatMessage | undefined;
  getUnreadCount: (sessionId: string, lastReadSeq: bigint) => number;

  // Actions
  setMessages: (sessionId: string, messages: ChatMessage[]) => void;
  addMessage: (message: ChatMessage) => void;
  addMessages: (sessionId: string, messages: ChatMessage[]) => void;
  prependMessages: (sessionId: string, messages: ChatMessage[]) => void;
  updateMessage: (msgId: string, updates: Partial<ChatMessage>) => void;
  updateMessageBySeqId: (sessionId: string, seqId: bigint, updates: Partial<ChatMessage>) => void;
  removeMessage: (msgId: string) => void;
  clearSessionMessages: (sessionId: string) => void;
  markAsSent: (tempId: string, realMsgId: string, seqId: bigint) => void;
  markAsFailed: (msgId: string) => void;
  setIsLoading: (loading: boolean) => void;
  setError: (error: string | null) => void;
  reset: () => void;
}

export const useMessageStore = create<MessageState>((set, get) => ({
  // 初始状态
  messages: {},
  isLoading: false,
  error: null,

  // Selectors
  getSessionMessages: (sessionId) => {
    return get().messages[sessionId] || [];
  },

  getMessageById: (msgId) => {
    const state = get();
    for (const sessionId in state.messages) {
      const msg = state.messages[sessionId].find((m) => m.msgId === msgId);
      if (msg) return msg;
    }
    return undefined;
  },

  getUnreadCount: (sessionId, lastReadSeq) => {
    const messages = get().messages[sessionId] || [];
    return messages.filter((m) => m.seqId > lastReadSeq && !m.isOwn).length;
  },

  // Actions
  setMessages: (sessionId, messages) =>
    set((state) => ({
      messages: {
        ...state.messages,
        [sessionId]: messages,
      },
    })),

  addMessage: (message) =>
    set((state) => {
      const sessionId = message.sessionId;
      const existing = state.messages[sessionId] || [];

      // 检查是否已存在（根据 msgId 或 seqId）
      const exists = existing.find(
        (m) => m.msgId === message.msgId || m.seqId === message.seqId,
      );
      if (exists) {
        // 更新现有消息
        return {
          messages: {
            ...state.messages,
            [sessionId]: existing.map((m) =>
              m.msgId === message.msgId || m.seqId === message.seqId
                ? { ...m, ...message }
                : m,
            ),
          },
        };
      }

      return {
        messages: {
          ...state.messages,
          [sessionId]: [...existing, message],
        },
      };
    }),

  addMessages: (sessionId, newMessages) =>
    set((state) => {
      const existing = state.messages[sessionId] || [];
      // 去重并追加
      const existingIds = new Set(existing.map((m) => m.msgId));
      const uniqueNewMessages = newMessages.filter(
        (m) => !existingIds.has(m.msgId),
      );
      return {
        messages: {
          ...state.messages,
          [sessionId]: [...existing, ...uniqueNewMessages],
        },
      };
    }),

  prependMessages: (sessionId, newMessages) =>
    set((state) => {
      const existing = state.messages[sessionId] || [];
      // 去重并前置（用于加载历史消息）
      const existingIds = new Set(existing.map((m) => m.msgId));
      const uniqueNewMessages = newMessages.filter(
        (m) => !existingIds.has(m.msgId),
      );
      return {
        messages: {
          ...state.messages,
          [sessionId]: [...uniqueNewMessages, ...existing],
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

  updateMessageBySeqId: (sessionId, seqId, updates) =>
    set((state) => {
      const messages = state.messages[sessionId] || [];
      return {
        messages: {
          ...state.messages,
          [sessionId]: messages.map((msg) =>
            msg.seqId === seqId ? { ...msg, ...updates } : msg,
          ),
        },
      };
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

  markAsSent: (tempId, realMsgId, seqId) =>
    set((state) => {
      const newMessages = { ...state.messages };
      for (const sessionId in newMessages) {
        newMessages[sessionId] = newMessages[sessionId].map((msg) =>
          msg.msgId === tempId
            ? { ...msg, msgId: realMsgId, seqId, status: MESSAGE_STATUS.SENT }
            : msg,
        );
      }
      return { messages: newMessages };
    }),

  markAsFailed: (msgId) =>
    set((state) => {
      const newMessages = { ...state.messages };
      for (const sessionId in newMessages) {
        newMessages[sessionId] = newMessages[sessionId].map((msg) =>
          msg.msgId === msgId ? { ...msg, status: MESSAGE_STATUS.FAILED } : msg,
        );
      }
      return { messages: newMessages };
    }),

  setIsLoading: (isLoading) =>
    set({
      isLoading,
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

/**
 * 生成临时消息 ID
 */
export function generateTempId(): string {
  return `${DEFAULTS.TEMP_ID_PREFIX}${Date.now()}-${Math.random().toString(36).slice(2, 9)}`;
}

/**
 * 将 PushMessage 转换为 ChatMessage
 */
export function pushMessageToChatMessage(
  push: PushMessage,
  currentUsername: string,
  sessionId?: string,
): ChatMessage {
  return {
    msgId: push.msgId.toString(),
    sessionId: sessionId || push.sessionId,
    seqId: push.seqId,
    fromUsername: push.fromUsername,
    toUsername: push.toUsername,
    content: push.content,
    msgType: push.type,
    timestamp: push.timestamp,
    isOwn: push.fromUsername === currentUsername,
    status: MESSAGE_STATUS.SENT,
  };
}

/**
 * 创建待发送的消息对象
 */
export function createPendingMessage(
  sessionId: string,
  content: string,
  username: string,
  msgType: string = "text",
): ChatMessage {
  return {
    msgId: generateTempId(),
    sessionId,
    seqId: 0n,
    fromUsername: username,
    content,
    msgType,
    timestamp: BigInt(Math.floor(Date.now() / 1000)), // 秒级时间戳，与后端一致
    isOwn: true,
    status: MESSAGE_STATUS.SENDING,
  };
}
