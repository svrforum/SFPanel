import { useCallback, useEffect, useState } from 'react'
import { api } from '@/lib/api'

/** Poll visible pages, retain successful snapshots, and retire requests on node changes. */
export function useDockerResource<T>(loader: () => Promise<T[]>, intervalMs = 30000) {
  const node = api.currentNode
  const [refreshIndex, setRefreshIndex] = useState(0)
  const [state, setState] = useState<{ node: string | null; data: T[]; loading: boolean; error: string | null; updatedAt: number | null }>({ node, data: [], loading: true, error: null, updatedAt: null })
  const refresh = useCallback(() => { setRefreshIndex(value => value + 1) }, [])
  useEffect(() => {
    let disposed = false
    let pending = false
    const load = async () => {
      if (disposed || pending || document.hidden) return
      pending = true
      await Promise.resolve()
      if (disposed) return
      setState(previous => previous.node === node ? { ...previous, loading: true } : { node, data: [], loading: true, error: null, updatedAt: null })
      try {
        const data = await loader()
        if (!disposed) setState({ node, data: data || [], loading: false, error: null, updatedAt: Date.now() })
      } catch (err) {
        if (!disposed) setState(previous => ({ ...previous, loading: false, error: err instanceof Error ? err.message : String(err) }))
      } finally { pending = false }
    }
    void load()
    const timer = window.setInterval(load, intervalMs)
    document.addEventListener('visibilitychange', load)
    window.addEventListener('docker-resources-changed', refresh)
    return () => {
      disposed = true
      window.clearInterval(timer)
      document.removeEventListener('visibilitychange', load)
      window.removeEventListener('docker-resources-changed', refresh)
    }
  }, [loader, node, refreshIndex, intervalMs, refresh])
  const current = state.node === node ? state : { data: [], loading: true, error: null, updatedAt: null }
  return { ...current, refresh }
}
