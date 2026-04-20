import { useCallback } from "react";

import { loadHistory, type LoadHistoryOptions } from "../services/session";

export function useLoadHistory() {
  const load = useCallback(async (sessionId: string, options?: LoadHistoryOptions) => {
    return loadHistory(sessionId, options);
  }, []);

  return { load };
}
