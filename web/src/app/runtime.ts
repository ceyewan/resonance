import type { ChatEvent } from "@gen/common/v1/event_pb";

import { dispatchWsPacket } from "../api/ws/dispatcher";
import { OutboxManager } from "../api/ws/outbox";
import { WsClient, buildWsUrl, type WsConnectionStatus } from "../api/ws/client";
import { useAgentStreamStore } from "../stores/agentStream";
import { useConnectionStore } from "../stores/connection";
import {
  handleAgentStreamBegin,
  handleAgentStreamChunk,
  handleAgentStreamEnd,
} from "../sync/agentStream";
import { runInboxSyncThenFlushOutbox } from "../sync/inbox";
import { reconcileWsEvent } from "../sync/reconcile";
import { syncSessionList } from "../services/session";

export type RuntimeWsClient = Pick<
  WsClient,
  "connect" | "disconnect" | "isOpen" | "onError" | "onPacket" | "onStatus" | "send"
>;

export type AppRuntimeOptions = {
  createWsClient?: (token: string) => RuntimeWsClient;
  handleWsEvent?: (event: ChatEvent) => Promise<void>;
  runInboxSyncThenFlushOutbox?: (outbox: Pick<OutboxManager, "flushPending">) => Promise<void>;
  syncSessionList?: () => Promise<unknown>;
};

export class AppRuntime {
  private startInFlight: Promise<void> | null = null;
  private startToken = "";
  private ws: RuntimeWsClient | null = null;
  private outbox: OutboxManager | null = null;
  private currentToken = "";
  private readonly unsubscribers: Array<() => void> = [];
  private readonly createWsClient: (token: string) => RuntimeWsClient;
  private readonly handleWsEvent: (event: ChatEvent) => Promise<void>;
  private readonly syncInboxThenFlush: (
    outbox: Pick<OutboxManager, "flushPending">,
  ) => Promise<void>;
  private readonly syncSessionList: () => Promise<unknown>;

  constructor(options: AppRuntimeOptions = {}) {
    this.createWsClient =
      options.createWsClient ??
      ((token) =>
        new WsClient({
          url: buildWsUrl(token),
        }));
    this.handleWsEvent = options.handleWsEvent ?? reconcileWsEvent;
    this.syncInboxThenFlush = options.runInboxSyncThenFlushOutbox ?? runInboxSyncThenFlushOutbox;
    this.syncSessionList = options.syncSessionList ?? syncSessionList;
  }

  async start(token: string): Promise<void> {
    if (token.trim() === "") {
      throw new Error("Cannot start runtime without access token");
    }
    if (this.startInFlight !== null && this.startToken === token) {
      return this.startInFlight;
    }
    if (this.ws !== null && this.currentToken === token) {
      return;
    }

    const job = (async () => {
      this.stop();
      await this.syncSessionList();

      const ws = this.createWsClient(token);
      const outbox = new OutboxManager(ws);
      this.ws = ws;
      this.outbox = outbox;
      this.currentToken = token;
      this.bindWs(ws, outbox);
      ws.connect();
    })().finally(() => {
      this.startInFlight = null;
      this.startToken = "";
    });

    this.startInFlight = job;
    this.startToken = token;
    await job;
  }

  stop(): void {
    for (const unsubscribe of this.unsubscribers.splice(0)) {
      unsubscribe();
    }
    this.ws?.disconnect();
    this.ws = null;
    this.outbox = null;
    this.currentToken = "";
    this.startInFlight = null;
    this.startToken = "";
    useAgentStreamStore.getState().reset();
    useConnectionStore.getState().reset();
  }

  reconnect(): void {
    this.ws?.connect();
  }

  async resync(): Promise<void> {
    if (this.outbox === null) {
      throw new Error("Runtime is not started");
    }
    await this.syncInboxThenFlush(this.outbox);
  }

  getOutbox(): OutboxManager {
    if (this.outbox === null) {
      throw new Error("Runtime is not started");
    }
    return this.outbox;
  }

  isStarted(): boolean {
    return this.ws !== null && this.outbox !== null;
  }

  private bindWs(ws: RuntimeWsClient, outbox: OutboxManager): void {
    this.unsubscribers.push(
      ws.onPacket((packet) => {
        dispatchWsPacket(packet, {
          onEvent: (event) => {
            void this.handleWsEvent(event).catch((cause: unknown) => {
              useAgentStreamStore.getState().reset();
              useConnectionStore
                .getState()
                .setOffline(
                  cause instanceof Error ? cause.message : "Failed to handle WsPacket event",
                );
            });
          },
          onStreamBegin: handleAgentStreamBegin,
          onStreamChunk: handleAgentStreamChunk,
          onStreamEnd: handleAgentStreamEnd,
        });
      }),
      ws.onStatus((status) => {
        this.handleWsStatus(status, outbox);
      }),
      ws.onError((error) => {
        useAgentStreamStore.getState().reset();
        useConnectionStore.getState().setOffline(error.message);
      }),
    );
  }

  private handleWsStatus(status: WsConnectionStatus, outbox: OutboxManager): void {
    const store = useConnectionStore.getState();
    switch (status) {
      case "idle":
        useAgentStreamStore.getState().reset();
        store.reset();
        return;
      case "connecting":
        store.setConnecting();
        return;
      case "open":
        store.setOpen();
        void this.syncInboxThenFlush(outbox).catch((cause: unknown) => {
          store.failInboxSync(cause instanceof Error ? cause.message : "Inbox sync failed");
        });
        return;
      case "offline":
        useAgentStreamStore.getState().reset();
        store.setOffline("WebSocket disconnected");
        return;
    }

    const unreachable: never = status;
    throw new Error(`Unhandled WebSocket status: ${String(unreachable)}`);
  }
}

export const appRuntime = new AppRuntime();
