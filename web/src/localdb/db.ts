import Dexie, { type Table } from "dexie";

export interface DBSyncState {
  key: string;
  value: string;
  updatedAt: number;
}

export interface DBSession {
  ownerUsername: string;
  sessionId: string;
  name: string;
  type: number;
  avatarUrl?: string;
  unreadCount: number;
  lastReadSeq: number;
  maxSeqId: number;
  lastMessage?: {
    msgId: string;
    seqId: string;
    content: string;
    type: string;
    timestamp: string;
  };
  updatedAt: number;
}

export interface DBMessage {
  key: string; // `${ownerUsername}:${sessionId}:${seqId}`
  ownerUsername: string;
  sessionId: string;
  seqId: string;
  msgId: string;
  fromUsername: string;
  toUsername?: string;
  content: string;
  msgType: string;
  timestamp: string;
  isOwn: boolean;
  status: "sending" | "sent" | "failed";
}

class ResonanceDB extends Dexie {
  sessions!: Table<DBSession, [string, string]>;
  messages!: Table<DBMessage, string>;
  syncState!: Table<DBSyncState, string>;

  constructor() {
    super("resonance_local_db");

    this.version(1).stores({
      sessions: "&sessionId, updatedAt, maxSeqId",
      messages: "&key, sessionId, timestamp, msgId",
      syncState: "&key, updatedAt",
    });

    this.version(2).stores({
      sessions: "&[ownerUsername+sessionId], ownerUsername, updatedAt, maxSeqId",
      messages: "&key, ownerUsername, [ownerUsername+sessionId], sessionId, timestamp, msgId",
      syncState: "&key, updatedAt",
    });
  }
}

export const db = new ResonanceDB();

export const SYNC_KEYS = {
  INBOX_CURSOR: "inbox_cursor",
} as const;
