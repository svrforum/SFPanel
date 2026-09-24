import { useCallback, useEffect, useState } from 'react'

export interface ResourceStatus {
  loading: boolean
  error: boolean
  updatedAt: number | null
  retry: () => void
}

/** Keep the last successful snapshot on errors; never mistake a failure for an empty result. */
export function usePolledResource<T>(loader: () => Promise<T>, intervalMs: number, enabled = true) {
  const [attempt, setAttempt] = useState(0)
  const [state, setState] = useState<{
    loader: typeof loader
    data?: T
    loading: boolean
    error: boolean
    updatedAt: number | null
  }>({ loader, loading: true, error: false, updatedAt: null })
  const retry = useCallback(() => setAttempt((n) => n + 1), [])

  useEffect(() => {
    if (!enabled) return
    let cancelled = false
    let pending = false
    const load = async () => {
      if (document.hidden || pending || cancelled) return
      pending = true
      // Defer the state transition as well as the request, including StrictMode's probe mount.
      await Promise.resolve()
      if (cancelled) return
      setState((prev) => prev.loader === loader
        ? { ...prev, loading: true }
        : { loader, loading: true, error: false, updatedAt: null })
      try {
        const data = await loader()
        if (!cancelled) setState({ loader, data, loading: false, error: false, updatedAt: Date.now() })
      } catch {
        if (!cancelled) setState((prev) => ({ ...prev, loading: false, error: true }))
      } finally {
        pending = false
      }
    }
    void load()
    const timer = window.setInterval(load, intervalMs)
    document.addEventListener('visibilitychange', load)
    return () => {
      cancelled = true
      window.clearInterval(timer)
      document.removeEventListener('visibilitychange', load)
    }
  }, [loader, intervalMs, enabled, attempt])

  // A different range/node must not briefly present the previous query's data as its own.
  const current = state.loader === loader ? state : { data: undefined, loading: true, error: false, updatedAt: null }
  return { data: current.data, loading: current.loading, error: current.error, updatedAt: current.updatedAt, retry }
}
