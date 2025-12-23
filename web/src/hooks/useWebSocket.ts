import { useEffect, useRef, useCallback, useState } from 'react'
import { WsPacket } from '@/gen/gateway/v1/packet_pb'

interface UseWebSocketOptions {
  url?: string
  onOpen?: () => void
  onClose?: () => void
  onError?: (error: Error) => void
  onMessage?: (packet: WsPacket) => void
}

interface UseWebSocketReturn {
  isConnected: boolean
  isConnecting: boolean
  error: Error | null
  send: (packet: WsPacket) => void
  connect: () => void
  disconnect: () => void
}

const DEFAULT_WS_URL = `ws://${import.meta.env.VITE_WS_HOST || 'localhost'}:${import.meta.env.VITE_WS_PORT || '8080'}/ws`

const HEARTBEAT_INTERVAL = 30000 // 30 seconds
const RECONNECT_DELAY = 3000 // 3 seconds
const MAX_RECONNECT_ATTEMPTS = 10

export function useWebSocket({
  url = DEFAULT_WS_URL,
  onOpen,
  onClose,
  onError,
  onMessage,
}: UseWebSocketOptions = {}): UseWebSocketReturn {
  const wsRef = useRef<WebSocket | null>(null)
  const heartbeatRef = useRef<NodeJS.Timeout | null>(null)
  const reconnectAttemptsRef = useRef(0)
  const reconnectTimeoutRef = useRef<NodeJS.Timeout | null>(null)

  const [isConnected, setIsConnected] = useState(false)
  const [isConnecting, setIsConnecting] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  const clearHeartbeat = useCallback(() => {
    if (heartbeatRef.current) {
      clearInterval(heartbeatRef.current)
      heartbeatRef.current = null
    }
  }, [])

  const startHeartbeat = useCallback(() => {
    clearHeartbeat()
    heartbeatRef.current = setInterval(() => {
      if (wsRef.current?.readyState === WebSocket.OPEN) {
        const packet = new WsPacket({
          seq: `heartbeat-${Date.now()}`,
        })
        wsRef.current.send(packet.toBinary())
      }
    }, HEARTBEAT_INTERVAL)
  }, [clearHeartbeat])

  const handleMessage = useCallback(
    (event: MessageEvent) => {
      try {
        const arrayBuffer = event.data as ArrayBuffer
        const packet = WsPacket.fromBinary(new Uint8Array(arrayBuffer))
        onMessage?.(packet)
      } catch (err) {
        console.error('[WS] Failed to parse message:', err)
        setError(err instanceof Error ? err : new Error('Failed to parse WebSocket message'))
      }
    },
    [onMessage]
  )

  const handleOpen = useCallback(() => {
    setIsConnected(true)
    setIsConnecting(false)
    setError(null)
    reconnectAttemptsRef.current = 0
    startHeartbeat()
    onOpen?.()
  }, [onOpen, startHeartbeat])

  const handleClose = useCallback(() => {
    setIsConnected(false)
    clearHeartbeat()
    onClose?.()
  }, [onClose, clearHeartbeat])

  const handleError = useCallback(
    (event: Event) => {
      const err = new Error('WebSocket connection error')
      setError(err)
      onError?.(err)
    },
    [onError]
  )

  const connect = useCallback(() => {
    if (isConnected || isConnecting || wsRef.current?.readyState === WebSocket.OPEN) {
      return
    }

    setIsConnecting(true)
    setError(null)

    try {
      wsRef.current = new WebSocket(url)
      wsRef.current.binaryType = 'arraybuffer'

      wsRef.current.addEventListener('open', handleOpen)
      wsRef.current.addEventListener('close', handleClose)
      wsRef.current.addEventListener('error', handleError)
      wsRef.current.addEventListener('message', handleMessage)
    } catch (err) {
      const error = err instanceof Error ? err : new Error('Failed to create WebSocket')
      setError(error)
      setIsConnecting(false)
      onError?.(error)
    }
  }, [url, isConnected, isConnecting, handleOpen, handleClose, handleError, handleMessage, onError])

  const disconnect = useCallback(() => {
    clearHeartbeat()
    if (reconnectTimeoutRef.current) {
      clearTimeout(reconnectTimeoutRef.current)
      reconnectTimeoutRef.current = null
    }
    if (wsRef.current) {
      wsRef.current.close()
      wsRef.current = null
    }
    setIsConnected(false)
    setIsConnecting(false)
  }, [clearHeartbeat])

  const send = useCallback(
    (packet: WsPacket) => {
      if (!wsRef.current || wsRef.current.readyState !== WebSocket.OPEN) {
        console.warn('[WS] WebSocket is not connected')
        return
      }

      try {
        const binary = packet.toBinary()
        wsRef.current.send(binary)
      } catch (err) {
        console.error('[WS] Failed to send message:', err)
      }
    },
    []
  )

  // Auto-reconnect on close
  useEffect(() => {
    if (!isConnected && !isConnecting && wsRef.current?.readyState !== WebSocket.CONNECTING) {
      if (reconnectAttemptsRef.current < MAX_RECONNECT_ATTEMPTS) {
        reconnectTimeoutRef.current = setTimeout(() => {
          reconnectAttemptsRef.current++
          connect()
        }, RECONNECT_DELAY)
      }
    }

    return () => {
      if (reconnectTimeoutRef.current) {
        clearTimeout(reconnectTimeoutRef.current)
      }
    }
  }, [isConnected, isConnecting, connect])

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      disconnect()
    }
  }, [disconnect])

  return {
    isConnected,
    isConnecting,
    error,
    send,
    connect,
    disconnect,
  }
}
