import { useCallback } from "react";

import {
  retryPendingMessage,
  sendEdit,
  sendRecall,
  sendTextMessage,
  type SendMessageInput,
} from "../services/chat";

export function useSendMessage() {
  const send = useCallback(async (input: SendMessageInput) => {
    return sendTextMessage(input);
  }, []);

  const retry = useCallback(async (sessionId: string, clientMsgId: string) => {
    return retryPendingMessage(sessionId, clientMsgId);
  }, []);

  const recall = useCallback(async (sessionId: string, targetEventId: bigint) => {
    return sendRecall(sessionId, targetEventId);
  }, []);

  const edit = useCallback(async (sessionId: string, targetEventId: bigint, newContent: string) => {
    return sendEdit(sessionId, targetEventId, newContent);
  }, []);

  return { send, retry, recall, edit };
}
