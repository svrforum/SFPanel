import { useCallback, useEffect, useState } from 'react'
import { api } from '@/lib/api'
import type { AISession } from '@/types/api'

const POLL_MS = 5000

/**
 * The session list, refreshed every 5 s while the page is visible. A
 * hidden tab stops polling entirely (one list-windows fork per poll on the
 * host is fine while someone is looking, pointless when nobody is).
 */
export function useAISessions(enabled: boolean) {
  const [sessions, setSessions] = useState<AISession[]>([])
  const [loaded, setLoaded] = useState(false)

  const refresh = useCallback(async () => {
    try {
      setSessions(await api.getAISessions())
      setLoaded(true)
    } catch {
      // Keep the last good list; the next tick retries.
    }
  }, [])

  useEffect(() => {
    if (!enabled) return
    const tick = () => {
      if (document.visibilityState === 'visible') void refresh()
    }
    tick()
    const timer = window.setInterval(tick, POLL_MS)
    document.addEventListener('visibilitychange', tick)
    return () => {
      window.clearInterval(timer)
      document.removeEventListener('visibilitychange', tick)
    }
  }, [enabled, refresh])

  return { sessions, loaded, refresh }
}
