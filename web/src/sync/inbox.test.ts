import "fake-indexeddb/auto";

import { create } from "@bufbuild/protobuf";
import { ChatEventSchema } from "@gen/common/v1/event_pb";
import { MessageSchema, MessageType } from "@gen/common/v1/message_pb";
import { InboxEventSchema, type InboxEvent } from "@gen/common/v1/view_pb";
import {
  PullInboxDeltaResponseSchema,
  type PullInboxDeltaResponse,
} from "@gen/gateway/v1/session_pb";
import { beforeEach, describe, expect, test } from "vitest";

import { getEventsBySession, getMeta, setMeta } from "../db/repo";
import { db } from "../db/schema";
import { useConnectionStore } from "../stores/connection";
import { toBigIntId } from "../lib/id";
import { reconcileInboxEvent } from "./reconcile";
import { InboxSyncManager } from "./inbox";

function makeMessageEvent(eventId: bigint, seqId: bigint, sessionId: string) {
  return create(ChatEventSchema, {
    eventId,
    seqId,
    sessionId,
    fromUsername: "bob",
    timestampMs: 10_000n + seqId,
    payload: {
      case: "message",
      value: create(MessageSchema, {
        type: MessageType.TEXT,
        content: `e-${eventId.toString()}`,
        replyToEventId: 0n,
        clientMsgId: "",
        mentionedUsernames: [],
      }),
    },
  });
}

function makeInboxEvent(inboxId: bigint, eventId: bigint, seqId: bigint, sessionId: string): InboxEvent {
  return create(InboxEventSchema, {
    inboxId,
    event: makeMessageEvent(eventId, seqId, sessionId),
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

function createDeferred<T>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

beforeEach(async () => {
  await clearDbForTest();
  await setMeta({ key: "me_username", value: "alice" });
  useConnectionStore.getState().reset();
});

describe("InboxSyncManager", () => {
  test("a) cursor=0 首次拉取，多页直到 has_more=false", async () => {
    const sessionId = "s-inbox-paged";
    let callCount = 0;
    const manager = new InboxSyncManager({
      limit: 2n,
      pullInboxDelta: ({ cursorId }) => {
        callCount += 1;
        if (cursorId === 0n) {
          return Promise.resolve(
            create(PullInboxDeltaResponseSchema, {
              events: [
                makeInboxEvent(1n, 1_001n, 11n, sessionId),
                makeInboxEvent(2n, 1_002n, 12n, sessionId),
              ],
              nextCursorId: 2n,
              hasMore: true,
            }),
          );
        }
        return Promise.resolve(
          create(PullInboxDeltaResponseSchema, {
            events: [makeInboxEvent(3n, 1_003n, 13n, sessionId)],
            nextCursorId: 3n,
            hasMore: false,
          }),
        );
      },
    });

    await manager.run();

    expect(callCount).toBe(2);
    const rows = await getEventsBySession(sessionId);
    expect(rows).toHaveLength(3);
    expect(await getMeta("inbox_cursor_id")).toBe("3");
  });

  test("c) applyEvent 失败时 cursor 不前移到失败事件", async () => {
    const sessionId = "s-inbox-fail";
    const first = makeInboxEvent(10n, 2_001n, 21n, sessionId);
    const second = makeInboxEvent(11n, 2_002n, 22n, sessionId);

    const manager = new InboxSyncManager({
      pullInboxDelta: () =>
        Promise.resolve(
          create(PullInboxDeltaResponseSchema, {
            events: [first, second],
            nextCursorId: 11n,
            hasMore: false,
          }),
        ),
      applyInboxEvent: async (item) => {
        if (item.inboxId === 11n) {
          throw new Error("boom");
        }
        await reconcileInboxEvent(item);
      },
    });

    await expect(manager.run()).rejects.toThrow("boom");
    expect(await getMeta("inbox_cursor_id")).toBe("10");
    const rows = await getEventsBySession(sessionId);
    expect(rows).toHaveLength(1);
  });

  test("d) 并发调用 run() 只执行一轮实际拉取", async () => {
    const sessionId = "s-inbox-concurrent";
    const deferred = createDeferred<PullInboxDeltaResponse>();
    let callCount = 0;
    const manager = new InboxSyncManager({
      pullInboxDelta: () => {
        callCount += 1;
        return deferred.promise;
      },
    });

    const p1 = manager.run();
    const p2 = manager.run();

    deferred.resolve(
      create(PullInboxDeltaResponseSchema, {
        events: [makeInboxEvent(20n, 3_001n, 31n, sessionId)],
        nextCursorId: 20n,
        hasMore: false,
      }),
    );

    await Promise.all([p1, p2]);
    expect(callCount).toBe(1);
    expect(await getMeta("inbox_cursor_id")).toBe("20");
    expect(useConnectionStore.getState().inboxSyncing).toBe(false);
  });

  test("e) 空响应且 has_more=false 时直接返回", async () => {
    const manager = new InboxSyncManager({
      pullInboxDelta: () =>
        Promise.resolve(
          create(PullInboxDeltaResponseSchema, {
            events: [],
            nextCursorId: 0n,
            hasMore: false,
          }),
        ),
    });

    await manager.run();
    expect(await getMeta("inbox_cursor_id")).toBeUndefined();
  });

  test("cursor 存在时应从已有 cursor 继续拉取", async () => {
    await setMeta({ key: "inbox_cursor_id", value: "99" });
    const seenCursor: bigint[] = [];
    const manager = new InboxSyncManager({
      pullInboxDelta: ({ cursorId }) => {
        seenCursor.push(cursorId);
        return Promise.resolve(
          create(PullInboxDeltaResponseSchema, {
            events: [],
            nextCursorId: cursorId,
            hasMore: false,
          }),
        );
      },
    });

    await manager.run();
    expect(seenCursor).toEqual([toBigIntId("99")]);
  });
});
