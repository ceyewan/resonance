import { useCallback } from "react";

import { retryPendingMessage, sendTextMessage, type SendMessageInput } from "../services/chat";

export function useSendMessage() {
  const send = useCallback(async (input: SendMessageInput) => {
    return sendTextMessage(input);
  }, []);

  const retry = useCallback(async (sessionId: string, clientMsgId: string) => {
    return retryPendingMessage(sessionId, clientMsgId);
  }, []);

  return { send, retry };
}
