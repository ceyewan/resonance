import { create } from "@bufbuild/protobuf";
import { ChatEventSchema } from "@gen/common/v1/event_pb";
import { MessageSchema, MessageType } from "@gen/common/v1/message_pb";
import {
  StreamBeginSchema,
  StreamChunkSchema,
  StreamEndSchema,
  StreamFinishReason,
} from "@gen/gateway/v1/packet_pb";
import { beforeEach, describe, expect, test, vi } from "vitest";

import { useAgentStreamStore } from "../stores/agentStream";
import {
  handleAgentStreamBegin,
  handleAgentStreamChunk,
  handleAgentStreamEnd,
  parseAgentFinalRunId,
  reconcileFinalAgentStream,
} from "./agentStream";

function currentStreams() {
  return Object.values(useAgentStreamStore.getState().streamsByKey);
}

beforeEach(() => {
  vi.useRealTimers();
  useAgentStreamStore.getState().reset();
});

describe("agent stream reconciliation", () => {
  test("按单调 stream_sequence 追加文本，丢弃重复、乱序和 End 后的 chunk", () => {
    handleAgentStreamBegin(
      create(StreamBeginSchema, {
        sessionId: "session-1",
        streamId: "stream-1",
        runId: "run-1",
        fromUsername: "assistant",
        finalClientMsgId: "agent:run-1:final",
      }),
    );
    handleAgentStreamChunk(
      create(StreamChunkSchema, {
        sessionId: "session-1",
        streamId: "stream-1",
        runId: "run-1",
        streamSequence: 2n,
        delta: "A",
      }),
    );
    handleAgentStreamChunk(
      create(StreamChunkSchema, {
        sessionId: "session-1",
        streamId: "stream-1",
        runId: "run-1",
        streamSequence: 2n,
        delta: "duplicate",
      }),
    );
    handleAgentStreamChunk(
      create(StreamChunkSchema, {
        sessionId: "session-1",
        streamId: "stream-1",
        runId: "run-1",
        streamSequence: 1n,
        delta: "old",
      }),
    );
    handleAgentStreamChunk(
      create(StreamChunkSchema, {
        sessionId: "session-1",
        streamId: "stream-1",
        runId: "run-1",
        streamSequence: 4n,
        delta: "C",
      }),
    );

    expect(currentStreams()).toMatchObject([
      {
        content: "AC",
        lastSequence: 4n,
        hasSequenceGap: true,
        status: "streaming",
      },
    ]);

    handleAgentStreamEnd(
      create(StreamEndSchema, {
        sessionId: "session-1",
        streamId: "stream-1",
        runId: "run-1",
        streamSequence: 3n,
        reason: StreamFinishReason.STOP,
      }),
    );
    expect(currentStreams()[0]?.status).toBe("streaming");

    handleAgentStreamEnd(
      create(StreamEndSchema, {
        sessionId: "session-1",
        streamId: "stream-1",
        runId: "run-1",
        streamSequence: 5n,
        reason: StreamFinishReason.STOP,
      }),
    );
    handleAgentStreamChunk(
      create(StreamChunkSchema, {
        sessionId: "session-1",
        streamId: "stream-1",
        runId: "run-1",
        streamSequence: 6n,
        delta: "after-end",
      }),
    );
    expect(currentStreams()).toMatchObject([{ content: "AC", lastSequence: 5n, status: "ended" }]);
  });

  test("最终 ChatEvent 按 client_msg_id/run_id 移除临时气泡并阻止迟到 Begin", () => {
    handleAgentStreamBegin(
      create(StreamBeginSchema, {
        sessionId: "session-2",
        streamId: "stream-2",
        runId: "run:with:colon",
        fromUsername: "assistant",
        finalClientMsgId: "agent:run:with:colon:final",
      }),
    );
    reconcileFinalAgentStream(
      create(ChatEventSchema, {
        eventId: 900n,
        sessionId: "session-2",
        payload: {
          case: "message",
          value: create(MessageSchema, {
            type: MessageType.TEXT,
            content: "final",
            clientMsgId: "agent:run:with:colon:final",
          }),
        },
      }),
    );
    expect(currentStreams()).toHaveLength(0);
    expect(parseAgentFinalRunId("agent:run:with:colon:final")).toBe("run:with:colon");

    handleAgentStreamBegin(
      create(StreamBeginSchema, {
        sessionId: "session-2",
        streamId: "stream-late",
        runId: "run:with:colon",
        fromUsername: "assistant",
      }),
    );
    expect(currentStreams()).toHaveLength(0);
  });

  test("兼容旧 parent_event_id/sequence，并用最终 event_id 对账", () => {
    handleAgentStreamBegin(
      create(StreamBeginSchema, {
        parentEventId: 31n,
        sessionId: "session-legacy",
        fromUsername: "assistant",
      }),
    );
    handleAgentStreamChunk(
      create(StreamChunkSchema, { parentEventId: 31n, sequence: 0, delta: "legacy" }),
    );
    handleAgentStreamChunk(
      create(StreamChunkSchema, { parentEventId: 31n, sequence: 0, delta: "duplicate" }),
    );
    handleAgentStreamEnd(
      create(StreamEndSchema, {
        parentEventId: 31n,
        reason: StreamFinishReason.STOP,
      }),
    );

    expect(currentStreams()).toMatchObject([
      { content: "legacy", lastSequence: 0n, status: "ended" },
    ]);

    reconcileFinalAgentStream(
      create(ChatEventSchema, {
        eventId: 31n,
        sessionId: "session-legacy",
        payload: {
          case: "message",
          value: create(MessageSchema, { type: MessageType.TEXT, content: "final" }),
        },
      }),
    );
    expect(currentStreams()).toHaveLength(0);
  });

  test("缺少 session 的 chunk 在 stream_id 冲突时失败关闭，不串写其他会话", () => {
    for (const sessionId of ["session-a", "session-b"]) {
      handleAgentStreamBegin(
        create(StreamBeginSchema, {
          sessionId,
          streamId: "shared-stream",
          runId: "shared-run",
          fromUsername: "assistant",
        }),
      );
    }

    handleAgentStreamChunk(
      create(StreamChunkSchema, {
        streamId: "shared-stream",
        runId: "shared-run",
        streamSequence: 1n,
        delta: "ambiguous",
      }),
    );
    expect(currentStreams().map((stream) => stream.content)).toEqual(["", ""]);

    handleAgentStreamChunk(
      create(StreamChunkSchema, {
        sessionId: "session-a",
        streamId: "shared-stream",
        runId: "shared-run",
        streamSequence: 1n,
        delta: "only-a",
      }),
    );
    expect(currentStreams().find((stream) => stream.sessionId === "session-a")?.content).toBe(
      "only-a",
    );
    expect(currentStreams().find((stream) => stream.sessionId === "session-b")?.content).toBe("");
  });

  test("只有 run_id 的 canonical chunk 仍可在 session 内准确关联", () => {
    handleAgentStreamBegin(
      create(StreamBeginSchema, {
        sessionId: "session-run-only",
        streamId: "stream-run-only",
        runId: "run-only",
        fromUsername: "assistant",
      }),
    );
    handleAgentStreamChunk(
      create(StreamChunkSchema, {
        sessionId: "session-run-only",
        runId: "run-only",
        streamSequence: 1n,
        delta: "matched",
      }),
    );

    expect(currentStreams()[0]?.content).toBe("matched");
  });
});
