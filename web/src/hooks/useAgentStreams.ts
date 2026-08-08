import { useEffect } from "react";
import { useShallow } from "zustand/react/shallow";

import { useAgentStreamStore } from "../stores/agentStream";

const PRUNE_INTERVAL_MS = 15_000;

export function useAgentStreams(sessionId: string | null | undefined) {
  const normalizedSessionId = sessionId?.trim() ?? "";
  const streams = useAgentStreamStore(
    useShallow((state) =>
      Object.values(state.streamsByKey)
        .filter((stream) => stream.sessionId === normalizedSessionId)
        .sort((left, right) => left.startedAtMs - right.startedAtMs),
    ),
  );
  const pruneExpired = useAgentStreamStore((state) => state.pruneExpired);

  useEffect(() => {
    pruneExpired();
    const intervalId = window.setInterval(pruneExpired, PRUNE_INTERVAL_MS);
    return () => {
      window.clearInterval(intervalId);
    };
  }, [pruneExpired]);

  return streams;
}
