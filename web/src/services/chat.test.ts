import "fake-indexeddb/auto";

import { beforeEach, describe, expect, test, vi } from "vitest";

import { appRuntime } from "../app/runtime";
import { getEventsBySession } from "../db/repo";
import { db } from "../db/schema";
import { retryPendingMessage, sendTextMessage } from "./chat";

async function clearDbForTest(): Promise<void> {
  await db.transaction("rw", db.sessions, db.events, db.outbox, db.meta, async () => {
    await db.sessions.clear();
    await db.events.clear();
    await db.outbox.clear();
    await db.meta.clear();
  });
}

beforeEach(async () => {
  await clearDbForTest();
});

describe("chat service", () => {
  test("sendTextMessage 会写入 pending event 并复用同一 client_msg_id 发包", async () => {
    const send = vi.fn(() =>
      Promise.resolve({
        refClientSeq: "c1",
        eventId: 1n,
        seqId: 1n,
        sessionId: "s-chat",
      }),
    );
    vi.spyOn(appRuntime, "getOutbox").mockReturnValue({ send } as never);

    await sendTextMessage({
      sessionId: "s-chat",
      content: "hello",
    });

    const events = await getEventsBySession("s-chat");
    expect(events).toHaveLength(1);
    expect(events[0]?.clientMsgId).not.toBe("");
    expect(events[0]?.seqId.startsWith("-")).toBe(true);
    expect(send).toHaveBeenCalledWith("s-chat", events[0]?.clientMsgId, expect.anything());
  });

  test("retryPendingMessage 复用已有 pending event 的 client_msg_id", async () => {
    const send = vi.fn(() =>
      Promise.resolve({
        refClientSeq: "c2",
        eventId: 2n,
        seqId: 2n,
        sessionId: "s-retry",
      }),
    );
    vi.spyOn(appRuntime, "getOutbox").mockReturnValue({ send } as never);

    await sendTextMessage({
      sessionId: "s-retry",
      content: "retry me",
    });
    const events = await getEventsBySession("s-retry");
    const clientMsgId = events[0]?.clientMsgId ?? "";

    await retryPendingMessage("s-retry", clientMsgId);

    expect(send).toHaveBeenNthCalledWith(1, "s-retry", clientMsgId, expect.anything());
    expect(send).toHaveBeenNthCalledWith(2, "s-retry", clientMsgId, expect.anything());
    expect(await getEventsBySession("s-retry")).toHaveLength(1);
  });
});
