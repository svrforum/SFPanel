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

  const connect = useCallback(() => {
    const token = api.getToken()
    if (!token) return

    const wsUrl = `${window.location.protocol === 'https:' ? 'wss:' : 'ws:'}//${window.location.host}${url}?token=${token}`
    const ws = new WebSocket(wsUrl)

    ws.onopen = () => setConnected(true)
    ws.onclose = () => {
      setConnected(false)
      if (autoReconnect) {
        setTimeout(connect, reconnectInterval)
      }
    }
    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data)
        onMessage?.(data)
      } catch {
        onMessage?.(event.data)
      }
    }

    wsRef.current = ws
  }, [url, onMessage, autoReconnect, reconnectInterval])

  useEffect(() => {
    connect()
    return () => {
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
