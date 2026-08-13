import { useCallback, useEffect, useRef } from 'react'
import { useWebSocket } from '@/hooks/useWebSocket'

interface LogFrame {
  line?: string
  lines?: string[]
}

/**
 * Live-tail WebSocket for /ws/logs. Mount while live mode is on; unmount to
 * disconnect. Reconnection is the shared useWebSocket contract (3s base,
 * doubling, 30s cap, cleanup-guarded); this component adds the log-specific
 * parts: extracting lines from either frame shape and batching them into one
 * flush per animation frame so a high-rate tail doesn't setState per message.
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
  const batchRef = useRef<string[]>([])
  const rafRef = useRef<number | null>(null)
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

  const handleMessage = useCallback(
    (data: LogFrame | string) => {
      if (typeof data === 'string') {
        // Non-JSON frame — the hook hands the raw payload through.
        if (data.trim()) batchRef.current.push(data)
      } else if (data.line !== undefined) {
        batchRef.current.push(data.line)
      } else if (Array.isArray(data.lines)) {
        batchRef.current.push(...data.lines)
      }
      if (rafRef.current === null) {
        rafRef.current = requestAnimationFrame(flush)
      }
    },
    [flush]
  )

  const params = { source }
  const { connected } = useWebSocket<LogFrame | string>({
    url: '/ws/logs',
    params,
    onMessage: handleMessage,
  })

  useEffect(() => {
    onConnectedRef.current(connected)
  }, [connected])

  // Drop anything still buffered when live mode is switched off, and report
  // the disconnect the socket teardown itself can no longer announce.
  useEffect(() => {
    return () => {
      if (rafRef.current !== null) {
        cancelAnimationFrame(rafRef.current)
        rafRef.current = null
      }
      batchRef.current = []
      onConnectedRef.current(false)
    }
  }, [])

  return null
}
