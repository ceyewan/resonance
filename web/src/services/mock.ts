import { db } from "../db/schema";
import { useAuthStore } from "../stores/auth";
import { appRuntime } from "../app/runtime";
import type { User } from "@gen/common/v1/types_pb";
import { SessionType } from "@gen/common/v1/session_pb";
import { MessageType } from "@gen/common/v1/message_pb";
import { SessionUpdateKind } from "@gen/common/v1/event_pb";

export async function mockLoginAndPopulateDb() {
  const mockToken = "mock-token-123";
  const mockUser: User = {
    $typeName: "resonance.common.v1.User",
    id: "user-1",
    username: "mock_user",
    nickname: "Demo User",
    avatarUrl: "",
    createdAt: "0",
  };

  // 1. Set Auth State (Zustand Store) and LocalStorage to bypass restoreAuthSession
  localStorage.setItem("resonance_access_token", mockToken);
  localStorage.setItem("resonance_current_user", JSON.stringify({
    username: mockUser.username,
    nickname: mockUser.nickname,
    avatarUrl: mockUser.avatarUrl,
  }));

  useAuthStore.getState().setAuthenticated(mockToken, mockUser);

  // 2. Clear existing DB
  await db.transaction("rw", db.sessions, db.events, db.outbox, db.meta, async () => {
    await db.sessions.clear();
    await db.events.clear();
    await db.outbox.clear();
    await db.meta.clear();
  });

  const now = Date.now();

  // 3. Populate Sessions
  await db.sessions.bulkPut([
    {
      sessionId: "session-1",
      name: "Liquid Glass Team",
      type: SessionType.GROUP,
      avatarUrl: "",
      unreadCount: "3",
      lastReadSeq: "10",
      lastEventId: "evt-12",
      lastEventSeqId: "12",
      lastEventTs: String(now),
      lastEventPreview: "This UI looks amazing! 🚀",
      readUptoSeqId: "12",
      readUptoSeqByUser: {},
      draft: "",
    },
    {
      sessionId: "session-2",
      name: "Alice (Designer)",
      type: SessionType.DIRECT,
      avatarUrl: "",
      unreadCount: "0",
      lastReadSeq: "5",
      lastEventId: "evt-5",
      lastEventSeqId: "5",
      lastEventTs: String(now - 3600000), // 1 hour ago
      lastEventPreview: "I'll send the updated mockups later.",
      readUptoSeqId: "5",
      readUptoSeqByUser: {},
      draft: "",
    },
  ]);

  // 4. Populate Events for session-1
  await db.events.bulkPut([
    {
      sessionId: "session-1",
      seqId: "10",
      eventId: "evt-10",
      fromUsername: "alice",
      timestampMs: String(now - 10000),
      payloadCase: "message",
      messageType: MessageType.TEXT,
      content: "Hey everyone, check out the new Abyssal Glass design! The blur effects are super smooth.",
      replyToEventId: "",
      clientMsgId: "cmsg-10",
      mentionedUsernames: [],
      recalled: false,
      edited: false,
      targetEventId: "",
      readUptoSeqId: "",
      sessionUpdateKind: SessionUpdateKind.UNSPECIFIED,
      sessionUpdateNewName: "",
      sessionUpdateNewAvatarUrl: "",
      sessionUpdateAffectedMembers: [],
    },
    {
      sessionId: "session-1",
      seqId: "11",
      eventId: "evt-11",
      fromUsername: "mock_user", // matches our logged in user
      timestampMs: String(now - 5000),
      payloadCase: "message",
      messageType: MessageType.TEXT,
      content: "Wow, the refraction effect is super clean. This feels like staring into a deep ocean. 🌊",
      replyToEventId: "",
      clientMsgId: "cmsg-11",
      mentionedUsernames: [],
      recalled: false,
      edited: false,
      targetEventId: "",
      readUptoSeqId: "",
      sessionUpdateKind: SessionUpdateKind.UNSPECIFIED,
      sessionUpdateNewName: "",
      sessionUpdateNewAvatarUrl: "",
      sessionUpdateAffectedMembers: [],
    },
    {
      sessionId: "session-1",
      seqId: "12",
      eventId: "evt-12",
      fromUsername: "bob",
      timestampMs: String(now),
      payloadCase: "message",
      messageType: MessageType.TEXT,
      content: "This UI looks amazing! 🚀 Wait until you see the interactions in the composer.",
      replyToEventId: "",
      clientMsgId: "cmsg-12",
      mentionedUsernames: [],
      recalled: false,
      edited: false,
      targetEventId: "",
      readUptoSeqId: "",
      sessionUpdateKind: SessionUpdateKind.UNSPECIFIED,
      sessionUpdateNewName: "",
      sessionUpdateNewAvatarUrl: "",
      sessionUpdateAffectedMembers: [],
    },
  ]);

  // 5. Populate Events for session-2
  await db.events.bulkPut([
    {
      sessionId: "session-2",
      seqId: "5",
      eventId: "evt-5",
      fromUsername: "alice",
      timestampMs: String(now - 3600000),
      payloadCase: "message",
      messageType: MessageType.TEXT,
      content: "I'll send the updated mockups later.",
      replyToEventId: "",
      clientMsgId: "cmsg-5",
      mentionedUsernames: [],
      recalled: false,
      edited: false,
      targetEventId: "",
      readUptoSeqId: "",
      sessionUpdateKind: SessionUpdateKind.UNSPECIFIED,
      sessionUpdateNewName: "",
      sessionUpdateNewAvatarUrl: "",
      sessionUpdateAffectedMembers: [],
    },
  ]);

  // 6. Initialize local appRuntime to support outbox interactions
  await appRuntime.start(mockToken);
}
