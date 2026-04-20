import { useLiveQuery } from "dexie-react-hooks";

import { getEventsBySession } from "../db/repo";
import { type EventRow } from "../db/schema";
import { toBigIntId } from "../lib/id";
import { getOutboxStatusMap, type OutboxStatusSummary } from "../services/chat";

export type TimelineItem = {
  event: EventRow;
  sendState: OutboxStatusSummary | null;
};

export function useSessionTimeline(sessionId: string | null | undefined) {
  return useLiveQuery(async () => {
    if (sessionId === undefined || sessionId === null || sessionId.trim() === "") {
      return [] as TimelineItem[];
    }

    const [events, outboxStatusMap] = await Promise.all([
      getEventsBySession(sessionId),
      getOutboxStatusMap(sessionId),
    ]);

    const timeline = events.map((event) => ({
      event,
      sendState:
        event.clientMsgId.trim() === "" ? null : (outboxStatusMap[event.clientMsgId] ?? null),
    }));

    timeline.sort((left, right) => {
      const tsDelta = toBigIntId(left.event.timestampMs) - toBigIntId(right.event.timestampMs);
      if (tsDelta > 0n) {
        return 1;
      }
      if (tsDelta < 0n) {
        return -1;
      }
      return left.event.seqId.localeCompare(right.event.seqId);
    });
    return timeline;
  }, [sessionId]);
}
