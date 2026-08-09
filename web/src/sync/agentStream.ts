import type { ChatEvent } from "@gen/common/v1/event_pb";
import type { StreamBegin, StreamChunk, StreamEnd } from "@gen/gateway/v1/packet_pb";

import { toIdString } from "../lib/id";
import { useAgentStreamStore } from "../stores/agentStream";

const AGENT_FINAL_PREFIX = "agent:";
const AGENT_FINAL_SUFFIX = ":final";

function positiveId(value: bigint): string {
  return value > 0n ? toIdString(value) : "";
}

function normalizeBeginIdentity(
  streamIdValue: string,
  runIdValue: string,
  parentEventId: bigint,
): { streamId: string; runId: string; legacyParentEventId: string } {
  const explicitStreamId = streamIdValue.trim();
  const explicitRunId = runIdValue.trim();
  const legacyParentEventId = positiveId(parentEventId);
  const streamId = explicitStreamId || explicitRunId || legacyParentEventId;
  const runId = explicitRunId || explicitStreamId || legacyParentEventId;
  return { streamId, runId, legacyParentEventId };
}

function normalizeEventIdentity(
  streamIdValue: string,
  runIdValue: string,
  parentEventId: bigint,
): { streamId: string; runId: string; legacyParentEventId: string } {
  const streamId = streamIdValue.trim();
  const runId = runIdValue.trim();
  const legacyParentEventId = positiveId(parentEventId);
  if (streamId === "" && runId === "") {
    return {
      streamId: legacyParentEventId,
      runId: legacyParentEventId,
      legacyParentEventId,
    };
  }
  return { streamId, runId, legacyParentEventId };
}

function expectedFinalClientMsgId(explicitValue: string, runId: string): string {
  const explicit = explicitValue.trim();
  if (explicit !== "") {
    return explicit;
  }
  return runId === "" ? "" : `${AGENT_FINAL_PREFIX}${runId}${AGENT_FINAL_SUFFIX}`;
}

function usesCanonicalStreamFields(message: StreamChunk | StreamEnd): boolean {
  return message.streamId.trim() !== "" || message.runId.trim() !== "";
}

export function parseAgentFinalRunId(clientMsgId: string): string {
  const normalized = clientMsgId.trim();
  if (!normalized.startsWith(AGENT_FINAL_PREFIX) || !normalized.endsWith(AGENT_FINAL_SUFFIX)) {
    return "";
  }
  return normalized.slice(AGENT_FINAL_PREFIX.length, -AGENT_FINAL_SUFFIX.length).trim();
}

export function handleAgentStreamBegin(message: StreamBegin): void {
  const identity = normalizeBeginIdentity(message.streamId, message.runId, message.parentEventId);
  useAgentStreamStore.getState().begin({
    sessionId: message.sessionId,
    streamId: identity.streamId,
    runId: identity.runId,
    fromUsername: message.fromUsername,
    finalClientMsgId: expectedFinalClientMsgId(message.finalClientMsgId, identity.runId),
    legacyParentEventId: identity.legacyParentEventId,
  });
}

export function handleAgentStreamChunk(message: StreamChunk): void {
  const identity = normalizeEventIdentity(message.streamId, message.runId, message.parentEventId);
  useAgentStreamStore.getState().append({
    sessionId: message.sessionId,
    streamId: identity.streamId,
    runId: identity.runId,
    sequence: usesCanonicalStreamFields(message)
      ? message.streamSequence
      : BigInt(message.sequence),
    delta: message.delta,
  });
}

export function handleAgentStreamEnd(message: StreamEnd): void {
  const identity = normalizeEventIdentity(message.streamId, message.runId, message.parentEventId);
  useAgentStreamStore.getState().end({
    sessionId: message.sessionId,
    streamId: identity.streamId,
    runId: identity.runId,
    sequence: usesCanonicalStreamFields(message) ? message.streamSequence : null,
    finalClientMsgId: expectedFinalClientMsgId(message.finalClientMsgId, identity.runId),
    reason: message.reason,
  });
}

export function reconcileFinalAgentStream(event: ChatEvent): void {
  if (event.payload.case !== "message") {
    return;
  }

  const clientMsgId = event.payload.value.clientMsgId.trim();
  const runId = parseAgentFinalRunId(clientMsgId);
  useAgentStreamStore.getState().reconcileFinal({
    sessionId: event.sessionId,
    clientMsgId,
    runId,
    streamId: "",
    eventId: positiveId(event.eventId),
  });
}
