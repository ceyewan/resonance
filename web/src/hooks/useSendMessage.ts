import { useCallback } from "react";

import {
  retryPendingMessage,
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

  return { send, retry, recall };
}
