import Dexie, { type EntityTable } from "dexie";
import type { SessionUpdateKind } from "@gen/common/v1/event_pb";
import type { MessageType } from "@gen/common/v1/message_pb";
import type { SessionType } from "@gen/common/v1/session_pb";

export type SessionRow = {
  sessionId: string;
  name: string;
  type: SessionType;
  avatarUrl: string;
  unreadCount: string;
  lastReadSeq: string;
  lastEventId: string;
  lastEventSeqId: string;
  lastEventTs: string;
  lastEventPreview: string;
  readUptoSeqId: string;
  readUptoSeqByUser: Record<string, string>;
  draft: string;
};

export type EventPayloadCase =
  | "message"
  | "recall"
  | "edit"
  | "readReceipt"
  | "sessionUpdate"
  | "unknown";

export type EventRow = {
  sessionId: string;
  seqId: string;
  eventId: string;
  fromUsername: string;
  timestampMs: string;
  payloadCase: EventPayloadCase;
  messageType: MessageType;
  content: string;
  replyToEventId: string;
  clientMsgId: string;
  mentionedUsernames: string[];
  recalled: boolean;
  edited: boolean;
  targetEventId: string;
  readUptoSeqId: string;
  sessionUpdateKind: SessionUpdateKind;
  sessionUpdateNewName: string;
  sessionUpdateNewAvatarUrl: string;
  sessionUpdateAffectedMembers: string[];
};

export type OutboxStatus = "sending" | "retrying" | "acked" | "failed";

export type OutboxRow = {
  clientSeq: string;
  sessionId: string;
  clientMsgId: string;
  status: OutboxStatus;
  payloadJson: string;
  retryCount: number;
  createdAtMs: number;
  updatedAtMs: number;
  ackedEventId: string;
  ackedSeqId: string;
};

export type MetaRow = {
  key: string;
  value: string;
};

export class ResonanceDb extends Dexie {
  sessions!: EntityTable<SessionRow, "sessionId">;
  events!: EntityTable<EventRow, [string, string]>;
  outbox!: EntityTable<OutboxRow, "clientSeq">;
  meta!: EntityTable<MetaRow, "key">;

  constructor(name = "resonance") {
    super(name);
    this.version(1).stores({
      sessions: "sessionId, type, lastEventTs",
      events:
        "[sessionId+seqId], eventId, [sessionId+timestampMs], clientMsgId",
      outbox: "clientSeq, sessionId, status",
      meta: "key",
    });
  }
}

export const db = new ResonanceDb();
