import { StreamFinishReason } from "@gen/gateway/v1/packet_pb";
import { create } from "zustand";

const MAX_STREAMS = 64;
const MAX_FINAL_TOMBSTONES = 256;
const MAX_CONTENT_LENGTH = 256 * 1024;
const STREAM_IDLE_TTL_MS = 2 * 60 * 1000;
const ENDED_STREAM_TTL_MS = 60 * 1000;
const FINAL_TOMBSTONE_TTL_MS = 5 * 60 * 1000;

export type AgentStreamStatus = "streaming" | "ended";

export type AgentStreamBubble = {
  key: string;
  sessionId: string;
  streamId: string;
  runId: string;
  fromUsername: string;
  finalClientMsgId: string;
  legacyParentEventId: string;
  content: string;
  lastSequence: bigint;
  hasSequenceGap: boolean;
  status: AgentStreamStatus;
  finishReason: StreamFinishReason;
  startedAtMs: number;
  updatedAtMs: number;
  endedAtMs: number;
};

export type BeginAgentStreamInput = {
  sessionId: string;
  streamId: string;
  runId: string;
  fromUsername: string;
  finalClientMsgId: string;
  legacyParentEventId: string;
};

export type AppendAgentStreamInput = {
  sessionId: string;
  streamId: string;
  runId: string;
  sequence: bigint;
  delta: string;
};

export type EndAgentStreamInput = {
  sessionId: string;
  streamId: string;
  runId: string;
  sequence: bigint | null;
  finalClientMsgId: string;
  reason: StreamFinishReason;
};

export type ReconcileFinalAgentStreamInput = {
  sessionId: string;
  clientMsgId: string;
  runId: string;
  streamId: string;
  eventId: string;
};

type AgentStreamState = {
  streamsByKey: Record<string, AgentStreamBubble>;
  finalizedRunKeys: Record<string, number>;
  finalizedClientMsgKeys: Record<string, number>;
  begin: (input: BeginAgentStreamInput) => void;
  append: (input: AppendAgentStreamInput) => void;
  end: (input: EndAgentStreamInput) => void;
  reconcileFinal: (input: ReconcileFinalAgentStreamInput) => void;
  pruneExpired: (nowMs?: number) => void;
  reset: () => void;
};

function compositeKey(left: string, right: string): string {
  return JSON.stringify([left, right]);
}

export function agentStreamKey(sessionId: string, streamId: string): string {
  return compositeKey(sessionId, streamId);
}

function normalize(value: string): string {
  return value.trim();
}

function pruneTombstones(entries: Record<string, number>, nowMs: number): Record<string, number> {
  let next = entries;
  for (const [key, recordedAtMs] of Object.entries(entries)) {
    if (nowMs - recordedAtMs <= FINAL_TOMBSTONE_TTL_MS) {
      continue;
    }
    if (next === entries) {
      next = { ...entries };
    }
    delete next[key];
  }
  return next;
}

function limitTombstones(entries: Record<string, number>): Record<string, number> {
  const values = Object.entries(entries);
  if (values.length <= MAX_FINAL_TOMBSTONES) {
    return entries;
  }
  values.sort((left, right) => left[1] - right[1]);
  return Object.fromEntries(values.slice(values.length - MAX_FINAL_TOMBSTONES));
}

function pruneStreams(
  streams: Record<string, AgentStreamBubble>,
  nowMs: number,
): Record<string, AgentStreamBubble> {
  let next = streams;
  for (const [key, stream] of Object.entries(streams)) {
    const ttl = stream.status === "ended" ? ENDED_STREAM_TTL_MS : STREAM_IDLE_TTL_MS;
    if (nowMs - stream.updatedAtMs <= ttl) {
      continue;
    }
    if (next === streams) {
      next = { ...streams };
    }
    delete next[key];
  }
  return next;
}

function limitStreams(
  streams: Record<string, AgentStreamBubble>,
): Record<string, AgentStreamBubble> {
  const entries = Object.entries(streams);
  if (entries.length <= MAX_STREAMS) {
    return streams;
  }

  entries.sort((left, right) => left[1].updatedAtMs - right[1].updatedAtMs);
  return Object.fromEntries(entries.slice(entries.length - MAX_STREAMS));
}

function locateStreamKey(
  streams: Record<string, AgentStreamBubble>,
  sessionId: string,
  streamId: string,
  runId: string,
): string | null {
  if (streamId === "" && runId === "") {
    return null;
  }

  if (sessionId !== "" && streamId !== "") {
    const key = agentStreamKey(sessionId, streamId);
    const stream = streams[key];
    if (stream !== undefined && (runId === "" || stream.runId === runId)) {
      return key;
    }
  }

  const matches = Object.entries(streams).filter(([, stream]) => {
    if (sessionId !== "" && stream.sessionId !== sessionId) {
      return false;
    }
    if (streamId !== "" && stream.streamId !== streamId) {
      return false;
    }
    return runId === "" || stream.runId === runId;
  });
  return matches.length === 1 ? matches[0][0] : null;
}

const initialState = {
  streamsByKey: {} as Record<string, AgentStreamBubble>,
  finalizedRunKeys: {} as Record<string, number>,
  finalizedClientMsgKeys: {} as Record<string, number>,
};

export const useAgentStreamStore = create<AgentStreamState>((set) => ({
  ...initialState,
  begin: (rawInput) => {
    const nowMs = Date.now();
    const input = {
      ...rawInput,
      sessionId: normalize(rawInput.sessionId),
      streamId: normalize(rawInput.streamId),
      runId: normalize(rawInput.runId),
      fromUsername: normalize(rawInput.fromUsername),
      finalClientMsgId: normalize(rawInput.finalClientMsgId),
      legacyParentEventId: normalize(rawInput.legacyParentEventId),
    };
    if (input.sessionId === "" || input.streamId === "" || input.runId === "") {
      return;
    }

    set((state) => {
      const finalizedRunKeys = pruneTombstones(state.finalizedRunKeys, nowMs);
      const finalizedClientMsgKeys = pruneTombstones(state.finalizedClientMsgKeys, nowMs);
      if (
        finalizedRunKeys[compositeKey(input.sessionId, input.runId)] !== undefined ||
        (input.finalClientMsgId !== "" &&
          finalizedClientMsgKeys[compositeKey(input.sessionId, input.finalClientMsgId)] !==
            undefined)
      ) {
        return {
          ...state,
          finalizedRunKeys,
          finalizedClientMsgKeys,
        };
      }

      const streamsByKey = pruneStreams(state.streamsByKey, nowMs);
      const key = agentStreamKey(input.sessionId, input.streamId);
      const current = streamsByKey[key];
      if (current?.status === "ended" || (current !== undefined && current.runId !== input.runId)) {
        return {
          ...state,
          streamsByKey,
          finalizedRunKeys,
          finalizedClientMsgKeys,
        };
      }

      const next: AgentStreamBubble =
        current === undefined
          ? {
              key,
              sessionId: input.sessionId,
              streamId: input.streamId,
              runId: input.runId,
              fromUsername: input.fromUsername,
              finalClientMsgId: input.finalClientMsgId,
              legacyParentEventId: input.legacyParentEventId,
              content: "",
              lastSequence: -1n,
              hasSequenceGap: false,
              status: "streaming",
              finishReason: StreamFinishReason.UNSPECIFIED,
              startedAtMs: nowMs,
              updatedAtMs: nowMs,
              endedAtMs: 0,
            }
          : {
              ...current,
              fromUsername: input.fromUsername || current.fromUsername,
              finalClientMsgId: input.finalClientMsgId || current.finalClientMsgId,
              legacyParentEventId: input.legacyParentEventId || current.legacyParentEventId,
              updatedAtMs: nowMs,
            };

      return {
        streamsByKey: limitStreams({ ...streamsByKey, [key]: next }),
        finalizedRunKeys,
        finalizedClientMsgKeys,
      };
    });
  },
  append: (rawInput) => {
    const nowMs = Date.now();
    const input = {
      ...rawInput,
      sessionId: normalize(rawInput.sessionId),
      streamId: normalize(rawInput.streamId),
      runId: normalize(rawInput.runId),
    };
    if (input.sequence < 0n) {
      return;
    }

    set((state) => {
      const streamsByKey = pruneStreams(state.streamsByKey, nowMs);
      const key = locateStreamKey(streamsByKey, input.sessionId, input.streamId, input.runId);
      if (key === null) {
        return streamsByKey === state.streamsByKey ? state : { ...state, streamsByKey };
      }

      const current = streamsByKey[key];
      if (
        current === undefined ||
        current.status !== "streaming" ||
        input.sequence <= current.lastSequence
      ) {
        return streamsByKey === state.streamsByKey ? state : { ...state, streamsByKey };
      }

      const combined = `${current.content}${input.delta}`;
      return {
        ...state,
        streamsByKey: {
          ...streamsByKey,
          [key]: {
            ...current,
            content: combined.slice(0, MAX_CONTENT_LENGTH),
            lastSequence: input.sequence,
            hasSequenceGap:
              current.hasSequenceGap ||
              (current.lastSequence >= 0n && input.sequence > current.lastSequence + 1n),
            updatedAtMs: nowMs,
          },
        },
      };
    });
  },
  end: (rawInput) => {
    const nowMs = Date.now();
    const input = {
      ...rawInput,
      sessionId: normalize(rawInput.sessionId),
      streamId: normalize(rawInput.streamId),
      runId: normalize(rawInput.runId),
      finalClientMsgId: normalize(rawInput.finalClientMsgId),
    };

    set((state) => {
      const streamsByKey = pruneStreams(state.streamsByKey, nowMs);
      const key = locateStreamKey(streamsByKey, input.sessionId, input.streamId, input.runId);
      if (key === null) {
        return streamsByKey === state.streamsByKey ? state : { ...state, streamsByKey };
      }

      const current = streamsByKey[key];
      if (
        current === undefined ||
        current.status !== "streaming" ||
        (input.sequence !== null && input.sequence <= current.lastSequence)
      ) {
        return streamsByKey === state.streamsByKey ? state : { ...state, streamsByKey };
      }

      return {
        ...state,
        streamsByKey: {
          ...streamsByKey,
          [key]: {
            ...current,
            finalClientMsgId: input.finalClientMsgId || current.finalClientMsgId,
            lastSequence: input.sequence ?? current.lastSequence,
            status: "ended",
            finishReason: input.reason,
            updatedAtMs: nowMs,
            endedAtMs: nowMs,
          },
        },
      };
    });
  },
  reconcileFinal: (rawInput) => {
    const nowMs = Date.now();
    const input = {
      sessionId: normalize(rawInput.sessionId),
      clientMsgId: normalize(rawInput.clientMsgId),
      runId: normalize(rawInput.runId),
      streamId: normalize(rawInput.streamId),
      eventId: normalize(rawInput.eventId),
    };
    if (input.sessionId === "") {
      return;
    }

    set((state) => {
      const prunedStreams = pruneStreams(state.streamsByKey, nowMs);
      let streamsByKey = prunedStreams;
      const matchedRunIds = new Set<string>();
      let matched = false;
      for (const [key, stream] of Object.entries(prunedStreams)) {
        if (stream.sessionId !== input.sessionId) {
          continue;
        }
        const matches =
          (input.clientMsgId !== "" && stream.finalClientMsgId === input.clientMsgId) ||
          (input.runId !== "" && stream.runId === input.runId) ||
          (input.streamId !== "" && stream.streamId === input.streamId) ||
          (input.eventId !== "" && stream.legacyParentEventId === input.eventId);
        if (matches) {
          if (!matched) {
            streamsByKey = { ...prunedStreams };
          }
          matched = true;
          matchedRunIds.add(stream.runId);
          delete streamsByKey[key];
        }
      }

      let finalizedRunKeys = pruneTombstones(state.finalizedRunKeys, nowMs);
      if (input.runId !== "") {
        matchedRunIds.add(input.runId);
      }
      if (matchedRunIds.size > 0) {
        finalizedRunKeys = { ...finalizedRunKeys };
        for (const matchedRunId of matchedRunIds) {
          finalizedRunKeys[compositeKey(input.sessionId, matchedRunId)] = nowMs;
        }
        finalizedRunKeys = limitTombstones(finalizedRunKeys);
      }

      let finalizedClientMsgKeys = pruneTombstones(state.finalizedClientMsgKeys, nowMs);
      if (input.clientMsgId !== "" && (matched || input.runId !== "")) {
        finalizedClientMsgKeys = limitTombstones({
          ...finalizedClientMsgKeys,
          [compositeKey(input.sessionId, input.clientMsgId)]: nowMs,
        });
      }

      if (
        streamsByKey === state.streamsByKey &&
        finalizedRunKeys === state.finalizedRunKeys &&
        finalizedClientMsgKeys === state.finalizedClientMsgKeys
      ) {
        return state;
      }
      return {
        streamsByKey,
        finalizedRunKeys,
        finalizedClientMsgKeys,
      };
    });
  },
  pruneExpired: (nowMs = Date.now()) => {
    set((state) => {
      const streamsByKey = pruneStreams(state.streamsByKey, nowMs);
      const finalizedRunKeys = pruneTombstones(state.finalizedRunKeys, nowMs);
      const finalizedClientMsgKeys = pruneTombstones(state.finalizedClientMsgKeys, nowMs);
      if (
        streamsByKey === state.streamsByKey &&
        finalizedRunKeys === state.finalizedRunKeys &&
        finalizedClientMsgKeys === state.finalizedClientMsgKeys
      ) {
        return state;
      }
      return { streamsByKey, finalizedRunKeys, finalizedClientMsgKeys };
    });
  },
  reset: () => {
    set(initialState);
  },
}));
