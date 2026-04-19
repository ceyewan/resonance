import type { PullInboxDeltaResponse } from "@gen/gateway/v1/session_pb";

import type { OutboxManager } from "../api/ws/outbox";
import { getMeta, setMeta } from "../db/repo";
import { useConnectionStore } from "../stores/connection";
import { toBigIntId, toIdString } from "../lib/id";
import { reconcileInboxEvent } from "./reconcile";
import type { InboxEvent } from "@gen/common/v1/view_pb";

export type PullInboxDeltaFn = (request: {
  cursorId: bigint;
  limit: bigint;
}) => Promise<PullInboxDeltaResponse>;

export type InboxSyncManagerOptions = {
  limit?: bigint;
  pullInboxDelta?: PullInboxDeltaFn;
  applyInboxEvent?: (inboxEvent: InboxEvent) => Promise<void>;
};

const DEFAULT_LIMIT = 200n;

export class InboxSyncManager {
  private inFlight: Promise<void> | null = null;
  private readonly limit: bigint;
  private readonly pullInboxDelta: PullInboxDeltaFn;
  private readonly applyInboxEvent: (inboxEvent: InboxEvent) => Promise<void>;

  constructor(options: InboxSyncManagerOptions = {}) {
    this.limit = options.limit ?? DEFAULT_LIMIT;
    this.pullInboxDelta =
      options.pullInboxDelta ??
      (async (request) => {
        const { sessionClient } = await import("../api/clients");
        return sessionClient.pullInboxDelta({
          cursorId: request.cursorId,
          limit: request.limit,
        });
      });
    this.applyInboxEvent = options.applyInboxEvent ?? reconcileInboxEvent;
  }

  run(): Promise<void> {
    if (this.inFlight !== null) {
      return this.inFlight;
    }

    useConnectionStore.getState().startInboxSync();
    const job = this.runInternal()
      .then(() => {
        useConnectionStore.getState().finishInboxSync();
      })
      .catch((cause: unknown) => {
        useConnectionStore
          .getState()
          .failInboxSync(cause instanceof Error ? cause.message : "Inbox sync failed");
        throw cause;
      })
      .finally(() => {
        this.inFlight = null;
      });

    this.inFlight = job;
    return job;
  }

  private async runInternal(): Promise<void> {
    let cursorId = await this.readCursorId();
    while (true) {
      const response = await this.pullInboxDelta({
        cursorId,
        limit: this.limit,
      });

      if (response.events.length === 0) {
        if (response.hasMore) {
          throw new Error("PullInboxDelta returned has_more=true with empty events");
        }
        return;
      }

      for (const item of response.events) {
        if (item.event === undefined) {
          throw new Error(`InboxEvent missing event payload for inbox_id=${toIdString(item.inboxId)}`);
        }
        await this.applyInboxEvent(item);
        cursorId = item.inboxId;
        await this.writeCursorId(cursorId);
      }

      if (!response.hasMore) {
        return;
      }
    }
  }

  private async readCursorId(): Promise<bigint> {
    const raw = await getMeta("inbox_cursor_id");
    if (raw === undefined || raw.trim() === "") {
      return 0n;
    }
    return toBigIntId(raw);
  }

  private async writeCursorId(cursorId: bigint): Promise<void> {
    await setMeta({
      key: "inbox_cursor_id",
      value: toIdString(cursorId),
    });
  }
}

const defaultInboxSyncManager = new InboxSyncManager();

export async function runInboxSync(): Promise<void> {
  await defaultInboxSyncManager.run();
}

export async function runInboxSyncThenFlushOutbox(
  outbox: Pick<OutboxManager, "flushPending">,
): Promise<void> {
  await runInboxSync();
  await outbox.flushPending();
}
