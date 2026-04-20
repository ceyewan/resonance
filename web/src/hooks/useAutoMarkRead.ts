import { useEffect, useRef } from "react";

import { toBigIntId } from "../lib/id";
import { markSessionRead } from "../services/session";
import { type TimelineItem } from "./useSessionTimeline";

const THROTTLE_MS = 500;

function pickHighestSeq(timeline: readonly TimelineItem[]): bigint {
  let max = 0n;
  for (const { event } of timeline) {
    const seq = toBigIntId(event.seqId);
    if (seq > max) {
      max = seq;
    }
  }
  return max;
}

export function useAutoMarkRead(
  sessionId: string | null | undefined,
  timeline: readonly TimelineItem[] | undefined,
  lastReadSeq: string | undefined,
) {
  const pendingRef = useRef<number | null>(null);
  const lastDispatchedRef = useRef<bigint>(0n);

  useEffect(() => {
    if (!sessionId || !timeline || timeline.length === 0) {
      return;
    }
    if (typeof document !== "undefined" && document.visibilityState === "hidden") {
      return;
    }

    const highest = pickHighestSeq(timeline);
    if (highest === 0n) {
      return;
    }

    const baseline = lastReadSeq ? toBigIntId(lastReadSeq) : 0n;
    const target = highest > baseline ? highest : baseline;
    if (target <= lastDispatchedRef.current || target <= baseline) {
      return;
    }

    if (pendingRef.current !== null) {
      window.clearTimeout(pendingRef.current);
    }

    pendingRef.current = window.setTimeout(() => {
      pendingRef.current = null;
      lastDispatchedRef.current = target;
      void markSessionRead(sessionId, target).catch(() => {
        lastDispatchedRef.current = 0n;
      });
    }, THROTTLE_MS);

    return () => {
      if (pendingRef.current !== null) {
        window.clearTimeout(pendingRef.current);
        pendingRef.current = null;
      }
    };
  }, [sessionId, timeline, lastReadSeq]);

  useEffect(() => {
    lastDispatchedRef.current = 0n;
  }, [sessionId]);
}
