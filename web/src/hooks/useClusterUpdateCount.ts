import { useCallback, useEffect, useRef, useState } from 'react'
import { api } from '@/lib/api'
import { nodesToUpdate } from '@/lib/clusterNodes'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'

/** A whole-cluster read for one number in the sidebar: once a minute is enough. */
const POLL_MS = 60_000

/**
 * How many online servers run behind the cluster's version, for the sidebar.
 * Counted from /cluster/overview by nodesToUpdate, the rule the overview's
 * banner uses, so the two cannot disagree on the number — only on when they
 * last looked: the overview is pushed live, this reads once a minute while
 * the tab is shown. 0 while `enabled` is false. A read that fails keeps the
 * last count: a leader that cannot answer for a moment says nothing about
 * who is behind.
 */
export function useClusterUpdateCount(enabled: boolean): number {
  const [count, setCount] = useState(0)

  const load = useCallback(() => {
    if (!enabled) return
    api.getClusterOverview()
      .then((o) => setCount(nodesToUpdate(o).count))
      .catch(() => {})
  }, [enabled])

  // useVisibleInterval reads at mount and whenever the tab comes back, but
  // takes a new callback without starting over: when cluster mode is learned
  // after mount (a first visit), read now rather than a minute from now.
  const wasEnabled = useRef(enabled)
  useEffect(() => {
    if (enabled && !wasEnabled.current) load()
    wasEnabled.current = enabled
  }, [enabled, load])

  useVisibleInterval(load, POLL_MS)
  return enabled ? count : 0
}
