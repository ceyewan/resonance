import "fake-indexeddb/auto";

import { create } from "@bufbuild/protobuf";
import { ChatEventSchema } from "@gen/common/v1/event_pb";
import { MessageSchema, MessageType } from "@gen/common/v1/message_pb";
import { InboxEventSchema } from "@gen/common/v1/view_pb";
import { StreamBeginSchema } from "@gen/gateway/v1/packet_pb";
import { beforeEach, describe, expect, test } from "vitest";

import { getEventsBySession, setMeta } from "../db/repo";
import { db } from "../db/schema";
import { useAgentStreamStore } from "../stores/agentStream";
import { handleAgentStreamBegin } from "./agentStream";
import { reconcileInboxEvent, reconcileWsEvent } from "./reconcile";

function makeMessageEvent(eventId: bigint, seqId: bigint, sessionId: string, clientMsgId = "") {
  return create(ChatEventSchema, {
    eventId,
    seqId,
    sessionId,
    fromUsername: "bob",
    timestampMs: 1_000n + seqId,
    payload: {
      case: "message",
      value: create(MessageSchema, {
        type: MessageType.TEXT,
        content: `m-${eventId.toString()}`,
        replyToEventId: 0n,
        clientMsgId,
        mentionedUsernames: [],
      }),
    },
  });
}

async function clearDbForTest(): Promise<void> {
  await db.transaction("rw", db.sessions, db.events, db.outbox, db.meta, async () => {
    await db.sessions.clear();
    await db.events.clear();
    await db.outbox.clear();
    await db.meta.clear();
  });
}

beforeEach(async () => {
  useAgentStreamStore.getState().reset();
  await clearDbForTest();
  await setMeta({ key: "me_username", value: "alice" });
});

describe("reconcile", () => {
  test("WS 先到、Inbox 后到同一 event_id，events 表不重复", async () => {
    const sessionId = "s-reconcile-1";
    const event = makeMessageEvent(1_001n, 11n, sessionId);

    await reconcileWsEvent(event);
    await reconcileInboxEvent(
      create(InboxEventSchema, {
        inboxId: 101n,
        event,
      }),
    );

    const rows = await getEventsBySession(sessionId);
    expect(rows).toHaveLength(1);
  });

  test("Inbox 先到、WS 后到同一 event_id，events 表不重复", async () => {
    const sessionId = "s-reconcile-2";
    const event = makeMessageEvent(2_001n, 21n, sessionId);

    await reconcileInboxEvent(
      create(InboxEventSchema, {
        inboxId: 201n,
        event,
      }),
    );
    await reconcileWsEvent(event);

    const rows = await getEventsBySession(sessionId);
    expect(rows).toHaveLength(1);
  });

  test("最终消息先落库成功，再按 client_msg_id 对账移除流式临时态", async () => {
    const sessionId = "s-reconcile-agent";
    handleAgentStreamBegin(
      create(StreamBeginSchema, {
        sessionId,
        streamId: "stream-reconcile",
        runId: "run-reconcile",
        fromUsername: "assistant",
        finalClientMsgId: "agent:run-reconcile:final",
      }),
    );

    await reconcileWsEvent(makeMessageEvent(3_001n, 31n, sessionId, "agent:run-reconcile:final"));

    expect(await getEventsBySession(sessionId)).toHaveLength(1);
    expect(Object.values(useAgentStreamStore.getState().streamsByKey)).toHaveLength(0);
  });
});
