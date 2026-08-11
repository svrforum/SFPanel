import { useCallback, useEffect, useRef } from 'react'
import { api } from '@/lib/api'

/**
 * Live-tail WebSocket for /ws/logs. Mount while live mode is on; unmount to
 * disconnect. Incoming lines are batched and flushed once per animation
 * frame so high-rate logs don't trigger a setState per message, and the
 * connection auto-reconnects with the same exponential backoff contract as
 * hooks/useWebSocket (3s base, doubling, 30s cap, cleanup-flag guarded).
 *
 * We can't sit on useWebSocket itself: it resolves its `url` through
 * api.buildWsUrl(path) with no way to add query params, and /ws/logs takes
 * the log source as ?source=. If the hook grows a params option this
 * component should collapse onto it.
 */
export function LiveLogSocket({
  source,
  onLines,
  onConnectedChange,
}: {
  source: string
  onLines: (batch: string[]) => void
  onConnectedChange: (connected: boolean) => void
}) {
  const wsRef = useRef<WebSocket | null>(null)
  const batchRef = useRef<string[]>([])
  const rafRef = useRef<number | null>(null)
  const cleanedUpRef = useRef(false)
  const retryRef = useRef(0)
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const onLinesRef = useRef(onLines)
  const onConnectedRef = useRef(onConnectedChange)

  // Keep callback refs in sync without re-running the connection effect
  useEffect(() => {
    onLinesRef.current = onLines
  }, [onLines])
  useEffect(() => {
    onConnectedRef.current = onConnectedChange
  }, [onConnectedChange])

  // Flush batched lines into the page in a single rAF tick
  const flush = useCallback(() => {
    rafRef.current = null
    const batch = batchRef.current
    if (batch.length === 0) return
    batchRef.current = []
    onLinesRef.current(batch)
  }, [])

  useEffect(() => {
    cleanedUpRef.current = false
    retryRef.current = 0

    const armReconnect = () => {
      const delay = Math.min(3000 * Math.pow(2, retryRef.current), 30000)
      retryRef.current += 1
      if (timerRef.current) clearTimeout(timerRef.current)
      timerRef.current = setTimeout(() => {
        timerRef.current = null
        connect()
      }, delay)
    }

    const connect = () => {
      if (cleanedUpRef.current) return
      api
        .buildWsUrl('/ws/logs', { source })
        .then((wsUrl) => {
          if (cleanedUpRef.current) return
          const ws = new WebSocket(wsUrl)

          ws.onopen = () => {
            retryRef.current = 0
            onConnectedRef.current(true)
          }

          ws.onclose = () => {
            onConnectedRef.current(false)
            if (!cleanedUpRef.current) armReconnect()
          }

          ws.onmessage = (event) => {
            // Accumulate into the batch buffer
            try {
              const data = JSON.parse(event.data)
              if (data.line !== undefined) {
                batchRef.current.push(data.line)
              } else if (Array.isArray(data.lines)) {
                batchRef.current.push(...data.lines)
              }
            } catch {
              if (typeof event.data === 'string' && event.data.trim()) {
                batchRef.current.push(event.data)
              }
            }
            // Schedule a single flush per animation frame
            if (rafRef.current === null) {
              rafRef.current = requestAnimationFrame(flush)
            }
          }

          wsRef.current = ws
        })
        .catch(() => {
          // Ticket mint failed — retry with backoff like a dropped socket.
          if (!cleanedUpRef.current) armReconnect()
        })
    }

    connect()

    return () => {
      cleanedUpRef.current = true
      if (timerRef.current) {
        clearTimeout(timerRef.current)
        timerRef.current = null
      }
      if (rafRef.current !== null) {
        cancelAnimationFrame(rafRef.current)
        rafRef.current = null
      }
      batchRef.current = []
      wsRef.current?.close()
      wsRef.current = null
      onConnectedRef.current(false)
    }
  }, [source, flush])

  return null
}
