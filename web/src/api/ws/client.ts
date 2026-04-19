import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { PulseSchema, WsPacketSchema, type WsPacket } from "@gen/gateway/v1/packet_pb";

export type WsConnectionStatus = "idle" | "connecting" | "open" | "offline";

type PacketListener = (packet: WsPacket) => void;
type StatusListener = (status: WsConnectionStatus) => void;
type ErrorListener = (error: Error) => void;

export type WsClientOptions = {
  url?: string;
  heartbeatIntervalMs?: number;
  reconnectBaseDelayMs?: number;
  reconnectMaxDelayMs?: number;
  createSocket?: (url: string) => WebSocket;
};

export class WsClient {
  private readonly url: string;
  private readonly heartbeatIntervalMs: number;
  private readonly reconnectBaseDelayMs: number;
  private readonly reconnectMaxDelayMs: number;
  private readonly createSocket: (url: string) => WebSocket;
  private socket: WebSocket | null = null;
  private heartbeatTimer: ReturnType<typeof setInterval> | null = null;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private reconnectAttempts = 0;
  private manualClosed = false;
  private status: WsConnectionStatus = "idle";
  private readonly packetListeners = new Set<PacketListener>();
  private readonly statusListeners = new Set<StatusListener>();
  private readonly errorListeners = new Set<ErrorListener>();

  constructor(options: WsClientOptions = {}) {
    this.url = options.url ?? getWsBaseUrl();
    this.heartbeatIntervalMs = options.heartbeatIntervalMs ?? 20_000;
    this.reconnectBaseDelayMs = options.reconnectBaseDelayMs ?? 1_000;
    this.reconnectMaxDelayMs = options.reconnectMaxDelayMs ?? 30_000;
    this.createSocket = options.createSocket ?? ((url) => new WebSocket(url));
  }

  connect(): void {
    if (this.socket?.readyState === WebSocket.OPEN || this.socket?.readyState === WebSocket.CONNECTING) {
      return;
    }
    this.manualClosed = false;
    this.openSocket();
  }

  disconnect(): void {
    this.manualClosed = true;
    this.clearHeartbeat();
    this.clearReconnectTimer();

    const socket = this.socket;
    this.socket = null;
    if (socket !== null && socket.readyState <= WebSocket.OPEN) {
      socket.close();
    }
    this.setStatus("idle");
  }

  isOpen(): boolean {
    return this.socket?.readyState === WebSocket.OPEN;
  }

  send(packet: WsPacket): void {
    if (!this.isOpen() || this.socket === null) {
      throw new Error("WebSocket is not open");
    }
    this.socket.send(toBinary(WsPacketSchema, packet));
  }

  onPacket(listener: PacketListener): () => void {
    this.packetListeners.add(listener);
    return () => {
      this.packetListeners.delete(listener);
    };
  }

  onStatus(listener: StatusListener): () => void {
    this.statusListeners.add(listener);
    return () => {
      this.statusListeners.delete(listener);
    };
  }

  onError(listener: ErrorListener): () => void {
    this.errorListeners.add(listener);
    return () => {
      this.errorListeners.delete(listener);
    };
  }

  private openSocket(): void {
    this.clearReconnectTimer();
    this.setStatus("connecting");

    const socket = this.createSocket(this.url);
    socket.binaryType = "arraybuffer";
    this.socket = socket;

    socket.onopen = () => {
      this.reconnectAttempts = 0;
      this.setStatus("open");
      this.startHeartbeat();
    };

    socket.onmessage = (event) => {
      this.handleMessage(event.data);
    };

    socket.onerror = () => {
      this.emitError(new Error("WebSocket error"));
    };

    socket.onclose = () => {
      this.clearHeartbeat();
      this.socket = null;
      if (this.manualClosed) {
        this.setStatus("idle");
        return;
      }
      this.setStatus("offline");
      this.scheduleReconnect();
    };
  }

  private startHeartbeat(): void {
    this.clearHeartbeat();
    this.heartbeatTimer = setInterval(() => {
      if (!this.isOpen() || this.socket === null) {
        return;
      }
      const pulsePacket = create(WsPacketSchema, {
        clientSeq: "",
        payload: {
          case: "pulse",
          value: create(PulseSchema, {}),
        },
      });
      this.socket.send(toBinary(WsPacketSchema, pulsePacket));
    }, this.heartbeatIntervalMs);
  }

  private clearHeartbeat(): void {
    if (this.heartbeatTimer !== null) {
      clearInterval(this.heartbeatTimer);
      this.heartbeatTimer = null;
    }
  }

  private scheduleReconnect(): void {
    if (this.reconnectTimer !== null) {
      return;
    }
    const delay = Math.min(
      this.reconnectMaxDelayMs,
      this.reconnectBaseDelayMs * 2 ** this.reconnectAttempts,
    );
    this.reconnectAttempts += 1;
    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      if (!this.manualClosed) {
        this.openSocket();
      }
    }, delay);
  }

  private clearReconnectTimer(): void {
    if (this.reconnectTimer !== null) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }

  private handleMessage(data: unknown): void {
    if (data instanceof ArrayBuffer) {
      this.emitPacketFromBytes(new Uint8Array(data));
      return;
    }
    if (data instanceof Uint8Array) {
      this.emitPacketFromBytes(data);
      return;
    }
    if (typeof Blob !== "undefined" && data instanceof Blob) {
      void this.handleBlobMessage(data);
    }
  }

  private async handleBlobMessage(blob: Blob): Promise<void> {
    try {
      const arrayBuffer = await blob.arrayBuffer();
      this.emitPacketFromBytes(new Uint8Array(arrayBuffer));
    } catch (cause) {
      this.emitError(normalizeError(cause, "Failed to decode WebSocket blob message"));
    }
  }

  private emitPacketFromBytes(bytes: Uint8Array): void {
    try {
      const packet = fromBinary(WsPacketSchema, bytes);
      for (const listener of this.packetListeners) {
        listener(packet);
      }
    } catch (cause) {
      this.emitError(normalizeError(cause, "Failed to decode WsPacket"));
    }
  }

  private setStatus(status: WsConnectionStatus): void {
    if (this.status === status) {
      return;
    }
    this.status = status;
    for (const listener of this.statusListeners) {
      listener(status);
    }
  }

  private emitError(error: Error): void {
    for (const listener of this.errorListeners) {
      listener(error);
    }
  }
}

function normalizeError(cause: unknown, fallbackMessage: string): Error {
  if (cause instanceof Error) {
    return cause;
  }
  return new Error(fallbackMessage);
}

function getWsBaseUrl(): string {
  const runtimeUrl = window.__RESONANCE_RUNTIME_CONFIG__?.wsBaseUrl?.trim();
  if (runtimeUrl !== undefined && runtimeUrl !== "") {
    return runtimeUrl;
  }
  const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
  return `${protocol}//${window.location.host}/ws`;
}
