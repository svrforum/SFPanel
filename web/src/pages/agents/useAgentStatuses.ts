import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'
import { api, type ApiError } from '@/lib/api'
import { sortNodes } from '@/lib/clusterNodes'
import type { AgentStatus, ClusterNode } from '@/types/api'

/** How many nodes are asked at once: each answer waits on a TCP check of up to 3 s. */
const CONCURRENCY = 3

const REFRESH_MS = 30_000

/** absent: the node's panel is older than the route (404). */
export type StatusLoad =
  | { phase: 'loading' }
  | { phase: 'ready'; status: AgentStatus; at: number; refreshError?: string }
  | { phase: 'error'; error: string }
  | { phase: 'absent' }

interface Job {
  id: string
  /** Asked for by the operator: a failed node shows it is being asked again. */
  retry?: boolean
}

const message = (err: unknown) => (err instanceof Error ? err.message : String(err))

/**
 * Every node's agents, each read on that node: the node list, then the online
 * nodes a few at a time, again every 30 s while the page is visible. Each
 * answer lands on its own; a node already queued or in flight is not asked
 * twice, and nothing is asked once the page is left.
 */
export function useAgentStatuses(): {
  nodes: ClusterNode[]
  leaderId: string
  loads: Record<string, StatusLoad>
  overviewError: string | null
  refresh: () => void
  retry: (id: string) => void
} {
  const [overview, setOverview] = useState<{ nodes: ClusterNode[]; leaderId: string } | null>(null)
  const [overviewError, setOverviewError] = useState<string | null>(null)
  const [loads, setLoads] = useState<Record<string, StatusLoad>>({})
  const queue = useRef<Job[]>([])
  const pending = useRef(new Set<string>())
  const workers = useRef(0)
  const mounted = useRef(true)

  useEffect(() => {
    mounted.current = true
    const queued = queue
    const waiting = pending
    return () => {
      mounted.current = false
      // Not yet started: dropped, and free to be asked again on a remount.
      for (const job of queued.current) waiting.current.delete(job.id)
      queued.current = []
    }
  }, [])

  const run = useCallback(async (job: Job) => {
    // A periodic refresh redraws nothing until it has an answer; only a first
    // load, or a retry the operator pressed, shows that it is waiting.
    setLoads((prev) => (!prev[job.id] || (job.retry && prev[job.id].phase === 'error') ? { ...prev, [job.id]: { phase: 'loading' } } : prev))
    try {
      const status = await api.getAgentStatus(job.id)
      if (!mounted.current) return
      setLoads((prev) => ({ ...prev, [job.id]: { phase: 'ready', status, at: Date.now() } }))
    } catch (err) {
      if (!mounted.current) return
      if ((err as ApiError | undefined)?.status === 404) {
        setLoads((prev) => ({ ...prev, [job.id]: { phase: 'absent' } }))
        return
      }
      // A refresh that fails keeps what was last seen, and says so; a first
      // load that fails is an error, never an agent that is not there.
      setLoads((prev) => {
        const was = prev[job.id]
        return { ...prev, [job.id]: was?.phase === 'ready' ? { ...was, refreshError: message(err) } : { phase: 'error', error: message(err) } }
      })
    }
  }, [])

  const drain = useCallback(async () => {
    try {
      for (let job = queue.current.shift(); job; job = queue.current.shift()) {
        try {
          await run(job)
        } finally {
          pending.current.delete(job.id)
        }
      }
    } finally {
      // In the same turn as the empty queue was seen, so a job queued just
      // after still finds a free worker.
      workers.current--
    }
  }, [run])

  const enqueue = useCallback((jobs: Job[]) => {
    // A node list that answers after the page was left asks no one.
    if (!mounted.current) return
    for (const job of jobs) {
      if (pending.current.has(job.id)) continue
      pending.current.add(job.id)
      queue.current.push(job)
    }
    while (workers.current < CONCURRENCY && queue.current.length > 0) {
      workers.current++
      void drain()
    }
  }, [drain])

  const refresh = useCallback(() => {
    api.getClusterOverview()
      .then((data) => {
        if (!mounted.current) return
        setOverview({ nodes: data.nodes ?? [], leaderId: data.leader_id })
        setOverviewError(null)
        enqueue((data.nodes ?? []).filter((n) => n.status === 'online').map((n) => ({ id: n.id })))
      })
      // A list seen before stays, with the error beside it.
      .catch((err: unknown) => { if (mounted.current) setOverviewError(message(err)) })
  }, [enqueue])

  useVisibleInterval(refresh, REFRESH_MS)

  const retry = useCallback((id: string) => enqueue([{ id, retry: true }]), [enqueue])
  const nodes = useMemo(() => (overview ? sortNodes(overview.nodes, overview.leaderId) : []), [overview])

  return { nodes, leaderId: overview?.leaderId ?? '', loads, overviewError, refresh, retry }
}
