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
  // Why the last ask failed. Before a first list has come, the page shows it
  // instead of "No sessions yet", which is not what is known.
  const [error, setError] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    try {
      setSessions(await api.getAISessions())
      setLoaded(true)
      setError(null)
    } catch (err: unknown) {
      // Keep the last good list; the next tick retries.
      setError(err instanceof Error ? err.message : '')
    }
  }, [])

  useVisibleInterval(() => { void refresh() }, POLL_MS)

  return { sessions, loaded, error, refresh }
}
