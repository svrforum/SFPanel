import { useEffect, useRef, useCallback, useState } from 'react'
import { api } from '@/lib/api'

interface UseWebSocketOptions<T = unknown> {
  url: string
  onMessage?: (data: T) => void
  autoReconnect?: boolean
  reconnectInterval?: number
}

export function useWebSocket<T = unknown>({ url, onMessage, autoReconnect = true, reconnectInterval = 3000 }: UseWebSocketOptions<T>) {
  const wsRef = useRef<WebSocket | null>(null)
  const [connected, setConnected] = useState(false)
  const onMessageRef = useRef(onMessage)
  const isCleanedUpRef = useRef(false)
  const retryCountRef = useRef(0)
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  // Holds the latest connect callback so the reconnect closure inside
  // ws.onclose can call it without referencing the function inside its own
  // initializer (which trips react-hooks/immutability).
  const connectRef = useRef<() => void>(() => {})

  // Keep onMessage ref in sync without triggering reconnects
  useEffect(() => {
    onMessageRef.current = onMessage
  }, [onMessage])

  const connect = useCallback(() => {
    if (isCleanedUpRef.current) return

    const token = api.getToken()
    if (!token) return

    const wsUrl = api.buildWsUrl(url)
    const ws = new WebSocket(wsUrl)

    ws.onopen = () => {
      setConnected(true)
      retryCountRef.current = 0
    }
    ws.onclose = () => {
      setConnected(false)
      if (autoReconnect && !isCleanedUpRef.current) {
        const delay = Math.min(reconnectInterval * Math.pow(2, retryCountRef.current), 30000)
        retryCountRef.current += 1
        reconnectTimerRef.current = setTimeout(() => connectRef.current(), delay)
      }
    }
    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data) as T
        onMessageRef.current?.(data)
      } catch {
        onMessageRef.current?.(event.data as T)
      }
    }

    wsRef.current = ws
  }, [url, autoReconnect, reconnectInterval])

  // Sync the latest connect into the ref so the close-handler closure
  // always reaches the freshest version (deps may have changed since
  // the original WebSocket instance was created).
  useEffect(() => {
    connectRef.current = connect
  }, [connect])

  useEffect(() => {
    isCleanedUpRef.current = false
    connect()
    return () => {
      isCleanedUpRef.current = true
      if (reconnectTimerRef.current) {
        clearTimeout(reconnectTimerRef.current)
        reconnectTimerRef.current = null
      }
      wsRef.current?.close()
    }
  }, [connect])

  const send = useCallback((data: string | Record<string, unknown>) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(typeof data === 'string' ? data : JSON.stringify(data))
    }
  }, [])

  return { connected, send, ws: wsRef }
}
