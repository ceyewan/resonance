import { useEffect, useRef, useCallback, useState } from "react";
import { WsPacket } from "@/gen/gateway/v1/packet_pb";
import { WS_CONFIG } from "@/constants";
import { defaultWsBaseUrl, runtimeWsBaseUrl } from "@/config/runtime";

interface UseWebSocketOptions {
  url?: string;
  token?: string;
  onOpen?: () => void;
  onClose?: (event: CloseEvent) => void;
  onError?: (error: Error) => void;
  onMessage?: (packet: WsPacket) => void;
}

interface UseWebSocketReturn {
  isConnected: boolean;
  isConnecting: boolean;
  error: Error | null;
  send: (packet: WsPacket) => void;
  disconnect: () => void;
  reconnect: () => void;
}

const DEFAULT_WS_URL = runtimeWsBaseUrl || import.meta.env.VITE_WS_BASE_URL || defaultWsBaseUrl();

/**
 * WebSocket Hook
 * 管理 WebSocket 连接、心跳、消息收发
 */
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
  const reconnectTimeoutRef = useRef<NodeJS.Timeout | null>(null);
  const reconnectAttemptsRef = useRef(0);

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

  // 清除心跳
  const clearHeartbeat = useCallback(() => {
    if (heartbeatRef.current) {
      clearInterval(heartbeatRef.current);
      heartbeatRef.current = null;
    }
  }, []);

  // 启动心跳
  const startHeartbeat = useCallback(() => {
    clearHeartbeat();
    heartbeatRef.current = setInterval(() => {
      if (wsRef.current?.readyState === WebSocket.OPEN) {
        // 发送心跳包（空的 Pulse 消息）
        // 使用 fromJsonString 来正确设置 oneof 字段
        const packet = WsPacket.fromJsonString(
          JSON.stringify({
            seq: `heartbeat-${Date.now()}`,
            pulse: {},
          }),
        );
        try {
          wsRef.current.send(packet.toBinary());
        } catch (err) {
          console.error("[WS] Failed to send heartbeat:", err);
        }
      }
    }, WS_CONFIG.HEARTBEAT_INTERVAL);
  }, [clearHeartbeat]);

  // 清除重连定时器
  const clearReconnectTimeout = useCallback(() => {
    if (reconnectTimeoutRef.current) {
      clearTimeout(reconnectTimeoutRef.current);
      reconnectTimeoutRef.current = null;
    }
  }, []);

  // 连接 WebSocket
  const connect = useCallback(() => {
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
    if (
      wsRef.current?.readyState === WebSocket.OPEN ||
      wsRef.current?.readyState === WebSocket.CONNECTING
    ) {
      return;
    }

    setIsConnecting(true);
    setError(null);

    // 构建 WebSocket URL，将 token 作为参数传递
    const wsUrl = new URL(url);
    wsUrl.searchParams.set(WS_CONFIG.TOKEN_PARAM, token);
    const wsUrlString = wsUrl.toString();

    const ws = new WebSocket(wsUrlString);
    ws.binaryType = "arraybuffer";
    wsRef.current = ws;

    ws.onopen = () => {
      console.log("[WS] Connected");
      setIsConnected(true);
      setIsConnecting(false);
      setError(null);
      reconnectAttemptsRef.current = 0;
      startHeartbeat();
      onOpenRef.current?.();
    };

    ws.onclose = (event: CloseEvent) => {
      console.log("[WS] Disconnected", event.code, event.reason);
      setIsConnected(false);
      setIsConnecting(false);
      clearHeartbeat();

      onCloseRef.current?.(event);

      // 尝试自动重连（非正常关闭时）
      if (
        token &&
        !event.wasClean &&
        reconnectAttemptsRef.current < WS_CONFIG.MAX_RECONNECT_ATTEMPTS
      ) {
        reconnectAttemptsRef.current++;
        console.log(`[WS] Reconnecting... Attempt ${reconnectAttemptsRef.current}`);
        reconnectTimeoutRef.current = setTimeout(() => {
          connect();
        }, WS_CONFIG.RECONNECT_DELAY);
      }
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
  }, [token, url, clearHeartbeat, startHeartbeat]);

  // 主动断开连接
  const disconnect = useCallback(() => {
    clearReconnectTimeout();
    clearHeartbeat();
    reconnectAttemptsRef.current = WS_CONFIG.MAX_RECONNECT_ATTEMPTS; // 阻止自动重连
    if (wsRef.current) {
      wsRef.current.close(1000, "User disconnected");
      wsRef.current = null;
    }
    setIsConnected(false);
    setIsConnecting(false);
  }, [clearHeartbeat, clearReconnectTimeout]);

  // 手动重连
  const reconnect = useCallback(() => {
    disconnect();
    reconnectAttemptsRef.current = 0;
    connect();
  }, [disconnect, connect]);

  // token 变化时重新连接
  useEffect(() => {
    connect();
    return () => {
      clearHeartbeat();
      clearReconnectTimeout();
      if (wsRef.current) {
        wsRef.current.close();
        wsRef.current = null;
      }
    };
  }, [token, connect, clearHeartbeat, clearReconnectTimeout]);

  // 发送消息
  const send = useCallback((packet: WsPacket) => {
    if (!wsRef.current || wsRef.current.readyState !== WebSocket.OPEN) {
      console.warn("[WS] WebSocket is not connected");
      return false;
    }

    try {
      const binary = packet.toBinary();
      wsRef.current.send(binary);
      return true;
    } catch (err) {
      console.error("[WS] Failed to send message:", err);
      return false;
    }
  }, []);

  return {
    isConnected,
    isConnecting,
    error,
    send,
    disconnect,
    reconnect,
  };
}
