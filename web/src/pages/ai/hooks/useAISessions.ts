import { useCallback, useState } from 'react'
import { api } from '@/lib/api'
import type { AISession } from '@/types/api'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'

const POLL_MS = 5000

/**
 * The session list, refreshed every 5 s while the page is visible. A hidden
 * tab stops polling entirely — `useVisibleInterval` clears the timer and
 * refreshes again the moment the tab comes back (one list-windows fork per
 * poll on the host is fine while someone is looking, pointless when nobody
 * is).
 */
export function useAISessions() {
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

  useVisibleInterval(() => { void refresh() }, POLL_MS)

  return { sessions, loaded, refresh }
}
