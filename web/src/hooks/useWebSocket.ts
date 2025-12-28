import { useEffect, useRef, useCallback, useState } from "react";
import { WsPacket } from "@/gen/gateway/v1/packet_pb";

interface UseWebSocketOptions {
  url?: string;
  token?: string;
  onOpen?: () => void;
  onClose?: () => void;
  onError?: (error: Error) => void;
  onMessage?: (packet: WsPacket) => void;
}

interface UseWebSocketReturn {
  isConnected: boolean;
  isConnecting: boolean;
  error: Error | null;
  send: (packet: WsPacket) => void;
}

const DEFAULT_WS_URL = `ws://localhost:8081/ws`;

const HEARTBEAT_INTERVAL = 30000; // 30 seconds

export function useWebSocket({
  url = DEFAULT_WS_URL,
  token,
  onOpen,
  onClose,
  onError,
  onMessage,
}: UseWebSocketOptions = {}): UseWebSocketReturn {
  const wsRef = useRef<WebSocket | null>(null);
  const heartbeatRef = useRef<NodeJS.Timeout | null>(null);
  const onMessageRef = useRef(onMessage);
  const onOpenRef = useRef(onOpen);
  const onCloseRef = useRef(onClose);
  const onErrorRef = useRef(onError);

  // 更新回调引用
  useEffect(() => {
    onMessageRef.current = onMessage;
  }, [onMessage]);
  useEffect(() => {
    onOpenRef.current = onOpen;
  }, [onOpen]);
  useEffect(() => {
    onCloseRef.current = onClose;
  }, [onClose]);
  useEffect(() => {
    onErrorRef.current = onError;
  }, [onError]);

  const [isConnected, setIsConnected] = useState(false);
  const [isConnecting, setIsConnecting] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  const clearHeartbeat = useCallback(() => {
    if (heartbeatRef.current) {
      clearInterval(heartbeatRef.current);
      heartbeatRef.current = null;
    }
  }, []);

  const startHeartbeat = useCallback(() => {
    clearHeartbeat();
    heartbeatRef.current = setInterval(() => {
      if (wsRef.current?.readyState === WebSocket.OPEN) {
        const packet = new WsPacket({
          seq: `heartbeat-${Date.now()}`,
        });
        wsRef.current.send(packet.toBinary());
      }
    }, HEARTBEAT_INTERVAL);
  }, [clearHeartbeat]);

  // 连接函数
  useEffect(() => {
    // 没有 token，不连接
    if (!token) {
      if (wsRef.current) {
        wsRef.current.close();
        wsRef.current = null;
      }
      setIsConnected(false);
      setIsConnecting(false);
      return;
    }

    // 已经在连接或已连接，不重复连接
    if (wsRef.current?.readyState === WebSocket.OPEN || wsRef.current?.readyState === WebSocket.CONNECTING) {
      return;
    }

    setIsConnecting(true);
    setError(null);

    // 将 token 作为 URL 参数传递
    const wsUrl = `${url}?token=${encodeURIComponent(token)}`;
    const ws = new WebSocket(wsUrl);
    ws.binaryType = "arraybuffer";
    wsRef.current = ws;

    ws.onopen = () => {
      setIsConnected(true);
      setIsConnecting(false);
      setError(null);
      startHeartbeat();
      onOpenRef.current?.();
    };

    ws.onclose = () => {
      setIsConnected(false);
      setIsConnecting(false);
      clearHeartbeat();
      onCloseRef.current?.();
    };

    ws.onerror = () => {
      const err = new Error("WebSocket connection error");
      setError(err);
      setIsConnecting(false);
      onErrorRef.current?.(err);
    };

    ws.onmessage = (event: MessageEvent) => {
      try {
        const arrayBuffer = event.data as ArrayBuffer;
        const packet = WsPacket.fromBinary(new Uint8Array(arrayBuffer));
        onMessageRef.current?.(packet);
      } catch (err) {
        console.error("[WS] Failed to parse message:", err);
      }
    };

    // 清理函数
    return () => {
      clearHeartbeat();
      if (wsRef.current) {
        wsRef.current.close();
        wsRef.current = null;
      }
    };
  }, [token, url, clearHeartbeat, startHeartbeat]);

  const send = useCallback((packet: WsPacket) => {
    if (!wsRef.current || wsRef.current.readyState !== WebSocket.OPEN) {
      console.warn("[WS] WebSocket is not connected");
      return;
    }

    try {
      const binary = packet.toBinary();
      wsRef.current.send(binary);
    } catch (err) {
      console.error("[WS] Failed to send message:", err);
    }
  }, []);

  return {
    isConnected,
    isConnecting,
    error,
    send,
  };
}
