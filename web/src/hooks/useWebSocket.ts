import { useEffect, useRef, useCallback, useState } from 'react'
import { api } from '@/lib/api'

interface UseWebSocketOptions {
  url: string
  onMessage?: (data: any) => void
  autoReconnect?: boolean
  reconnectInterval?: number
}

export function useWebSocket({ url, onMessage, autoReconnect = true, reconnectInterval = 3000 }: UseWebSocketOptions) {
  const wsRef = useRef<WebSocket | null>(null)
  const [connected, setConnected] = useState(false)
  const onMessageRef = useRef(onMessage)
  const isCleanedUpRef = useRef(false)
  const retryCountRef = useRef(0)

  // Keep onMessage ref in sync without triggering reconnects
  useEffect(() => {
    onMessageRef.current = onMessage
  }, [onMessage])

  const connect = useCallback(() => {
    if (isCleanedUpRef.current) return

    const token = api.getToken()
    if (!token) return

    const wsUrl = `${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}${url}?token=${token}`
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
        setTimeout(connect, delay)
      }
    }
    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data)
        onMessageRef.current?.(data)
      } catch {
        onMessageRef.current?.(event.data)
      }
    }

    wsRef.current = ws
  }, [url, autoReconnect, reconnectInterval])

  useEffect(() => {
    isCleanedUpRef.current = false
    connect()
    return () => {
      isCleanedUpRef.current = true
      wsRef.current?.close()
    }
  }, [connect])

  const send = useCallback((data: any) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(typeof data === 'string' ? data : JSON.stringify(data))
    }
  }, [])

  return { connected, send, ws: wsRef }
}
