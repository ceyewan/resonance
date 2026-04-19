import "fake-indexeddb/auto";

import { create } from "@bufbuild/protobuf";
import {
  ChatEventSchema,
  MessageEditSchema,
  MessageRecallSchema,
  ReadReceiptSchema,
  SessionUpdateKind,
  SessionUpdateSchema,
} from "@gen/common/v1/event_pb";
import { MessageSchema, MessageType } from "@gen/common/v1/message_pb";
import { SessionType } from "@gen/common/v1/session_pb";
import { beforeEach, describe, expect, test } from "vitest";

import { clearAll, getEventsBySession, getSession, putEvent, setMeta, upsertSession } from "../db/repo";
import type { EventRow, SessionRow } from "../db/schema";
import { toIdString } from "../lib/id";
import { applyEvent } from "./applier";

function makeBaseSession(sessionId: string, type: SessionType): SessionRow {
  return {
    sessionId,
    name: "session",
    type,
    avatarUrl: "",
    unreadCount: "0",
    lastReadSeq: "0",
    lastEventId: "0",
    lastEventSeqId: "0",
    lastEventTs: "0",
    lastEventPreview: "",
    readUptoSeqId: "0",
    readUptoSeqByUser: {},
    draft: "",
  };
}

function makeMessageEvent(params: {
  eventId: bigint;
  seqId: bigint;
  sessionId: string;
  fromUsername: string;
  timestampMs: bigint;
  clientMsgId?: string;
  content?: string;
}) {
  return create(ChatEventSchema, {
    eventId: params.eventId,
    seqId: params.seqId,
    sessionId: params.sessionId,
    fromUsername: params.fromUsername,
    timestampMs: params.timestampMs,
    payload: {
      case: "message",
      value: create(MessageSchema, {
        type: MessageType.TEXT,
        content: params.content ?? "hello",
        replyToEventId: 0n,
        clientMsgId: params.clientMsgId ?? "",
        mentionedUsernames: [],
      }),
    },
  });
}

beforeEach(async () => {
  await clearAll();
  await setMeta({ key: "me_username", value: "alice" });
});

describe("applyEvent", () => {
  test("a) Message 正常插入并更新会话与未读", async () => {
    const sessionId = "s-message-ok";
    await upsertSession(makeBaseSession(sessionId, SessionType.DIRECT));

    await applyEvent(
      makeMessageEvent({
        eventId: 1001n,
        seqId: 1n,
        sessionId,
        fromUsername: "bob",
        timestampMs: 10001n,
      }),
    );

    const events = await getEventsBySession(sessionId);
    expect(events).toHaveLength(1);

    const session = await getSession(sessionId);
    expect(session?.lastEventSeqId).toBe("1");
    expect(session?.unreadCount).toBe("1");
  });

  test("b) 同一 event_id 二次到达幂等", async () => {
    const sessionId = "s-idempotent";
    await upsertSession(makeBaseSession(sessionId, SessionType.DIRECT));

    const event = makeMessageEvent({
      eventId: 2001n,
      seqId: 2n,
      sessionId,
      fromUsername: "bob",
      timestampMs: 20001n,
    });

    await applyEvent(event);
    await applyEvent(event);

    const events = await getEventsBySession(sessionId);
    expect(events).toHaveLength(1);

    const session = await getSession(sessionId);
    expect(session?.unreadCount).toBe("1");
  });

  test("c) 乱序到达时 last_event 保持更大 seq", async () => {
    const sessionId = "s-out-of-order";
    await upsertSession(makeBaseSession(sessionId, SessionType.DIRECT));

    await applyEvent(
      makeMessageEvent({
        eventId: 3001n,
        seqId: 10n,
        sessionId,
        fromUsername: "bob",
        timestampMs: 30010n,
      }),
    );
    await applyEvent(
      makeMessageEvent({
        eventId: 3002n,
        seqId: 5n,
        sessionId,
        fromUsername: "bob",
        timestampMs: 30005n,
      }),
    );

    const events = await getEventsBySession(sessionId);
    expect(events).toHaveLength(2);

    const session = await getSession(sessionId);
    expect(session?.lastEventSeqId).toBe("10");
  });

  test("d) pending 覆盖: seq<0 的占位被正式事件替换", async () => {
    const sessionId = "s-pending-replace";
    await upsertSession(makeBaseSession(sessionId, SessionType.DIRECT));

    const pendingRow: EventRow = {
      sessionId,
      seqId: "-1",
      eventId: "-1",
      fromUsername: "alice",
      timestampMs: "40000",
      payloadCase: "message",
      messageType: MessageType.TEXT,
      content: "pending",
      replyToEventId: "0",
      clientMsgId: "client-x",
      mentionedUsernames: [],
      recalled: false,
      edited: false,
      targetEventId: "0",
      readUptoSeqId: "0",
      sessionUpdateKind: 0,
      sessionUpdateNewName: "",
      sessionUpdateNewAvatarUrl: "",
      sessionUpdateAffectedMembers: [],
    };

    await putEvent(pendingRow);

    await applyEvent(
      makeMessageEvent({
        eventId: 4001n,
        seqId: 42n,
        sessionId,
        fromUsername: "alice",
        timestampMs: 40042n,
        clientMsgId: "client-x",
        content: "final",
      }),
    );

    const events = await getEventsBySession(sessionId);
    expect(events).toHaveLength(1);
    expect(events[0]?.seqId).toBe("42");
    expect(events[0]?.eventId).toBe("4001");
  });

  test("e) MessageRecall 只软删 recalled=true", async () => {
    const sessionId = "s-recall";
    await upsertSession(makeBaseSession(sessionId, SessionType.DIRECT));

    const message = makeMessageEvent({
      eventId: 5001n,
      seqId: 1n,
      sessionId,
      fromUsername: "bob",
      timestampMs: 50001n,
      content: "to recall",
    });
    await applyEvent(message);

    const recall = create(ChatEventSchema, {
      eventId: 5002n,
      seqId: 2n,
      sessionId,
      fromUsername: "bob",
      timestampMs: 50002n,
      payload: {
        case: "recall",
        value: create(MessageRecallSchema, {
          targetEventId: 5001n,
        }),
      },
    });
    await applyEvent(recall);

    const events = await getEventsBySession(sessionId);
    const recalledTarget = events.find((item) => item.eventId === toIdString(5001n));
    expect(recalledTarget?.recalled).toBe(true);
  });

  test("f) MessageEdit 覆盖 content 并 edited=true", async () => {
    const sessionId = "s-edit";
    await upsertSession(makeBaseSession(sessionId, SessionType.DIRECT));

    const message = makeMessageEvent({
      eventId: 6001n,
      seqId: 1n,
      sessionId,
      fromUsername: "bob",
      timestampMs: 60001n,
      content: "before",
    });
    await applyEvent(message);

    const edit = create(ChatEventSchema, {
      eventId: 6002n,
      seqId: 2n,
      sessionId,
      fromUsername: "bob",
      timestampMs: 60002n,
      payload: {
        case: "edit",
        value: create(MessageEditSchema, {
          targetEventId: 6001n,
          newContent: "after",
        }),
      },
    });
    await applyEvent(edit);

    const events = await getEventsBySession(sessionId);
    const editedTarget = events.find((item) => item.eventId === toIdString(6001n));
    expect(editedTarget?.content).toBe("after");
    expect(editedTarget?.edited).toBe(true);
  });

  test("g1) ReadReceipt 单聊更新 read_upto_seq_id", async () => {
    const sessionId = "s-read-direct";
    await upsertSession(makeBaseSession(sessionId, SessionType.DIRECT));

    const receipt = create(ChatEventSchema, {
      eventId: 7001n,
      seqId: 7n,
      sessionId,
      fromUsername: "bob",
      timestampMs: 70001n,
      payload: {
        case: "readReceipt",
        value: create(ReadReceiptSchema, {
          readUptoSeqId: 88n,
        }),
      },
    });
    await applyEvent(receipt);

    const session = await getSession(sessionId);
    expect(session?.readUptoSeqId).toBe("88");
  });

  test("g2) ReadReceipt 群聊更新 read_upto_seq_id map", async () => {
    const sessionId = "s-read-group";
    await upsertSession(makeBaseSession(sessionId, SessionType.GROUP));

    const receipt = create(ChatEventSchema, {
      eventId: 7101n,
      seqId: 7n,
      sessionId,
      fromUsername: "bob",
      timestampMs: 71001n,
      payload: {
        case: "readReceipt",
        value: create(ReadReceiptSchema, {
          readUptoSeqId: 99n,
        }),
      },
    });
    await applyEvent(receipt);

    const session = await getSession(sessionId);
    expect(session?.readUptoSeqByUser.bob).toBe("99");
  });

  test("h) SessionUpdate 覆盖会话元信息", async () => {
    const sessionId = "s-session-update";
    await upsertSession(makeBaseSession(sessionId, SessionType.GROUP));

    const updateName = create(ChatEventSchema, {
      eventId: 8001n,
      seqId: 1n,
      sessionId,
      fromUsername: "bob",
      timestampMs: 80001n,
      payload: {
        case: "sessionUpdate",
        value: create(SessionUpdateSchema, {
          kind: SessionUpdateKind.NAME,
          newName: "new-group-name",
          newAvatarUrl: "",
          affectedMembers: [],
        }),
      },
    });

    const updateAvatar = create(ChatEventSchema, {
      eventId: 8002n,
      seqId: 2n,
      sessionId,
      fromUsername: "bob",
      timestampMs: 80002n,
      payload: {
        case: "sessionUpdate",
        value: create(SessionUpdateSchema, {
          kind: SessionUpdateKind.AVATAR,
          newName: "",
          newAvatarUrl: "https://example.com/a.png",
          affectedMembers: [],
        }),
      },
    });

    await applyEvent(updateName);
    await applyEvent(updateAvatar);

    const session = await getSession(sessionId);
    expect(session?.name).toBe("new-group-name");
    expect(session?.avatarUrl).toBe("https://example.com/a.png");
  });

  test("i) Recall/Edit 目标不存在时静默跳过", async () => {
    const sessionId = "s-missing-target";
    await upsertSession(makeBaseSession(sessionId, SessionType.DIRECT));

    const recall = create(ChatEventSchema, {
      eventId: 9001n,
      seqId: 1n,
      sessionId,
      fromUsername: "bob",
      timestampMs: 90001n,
      payload: {
        case: "recall",
        value: create(MessageRecallSchema, {
          targetEventId: 99991n,
        }),
      },
    });

    const edit = create(ChatEventSchema, {
      eventId: 9002n,
      seqId: 2n,
      sessionId,
      fromUsername: "bob",
      timestampMs: 90002n,
      payload: {
        case: "edit",
        value: create(MessageEditSchema, {
          targetEventId: 99992n,
          newContent: "noop",
        }),
      },
    });

    await expect(applyEvent(recall)).resolves.toBeUndefined();
    await expect(applyEvent(edit)).resolves.toBeUndefined();
  });
});
