import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, type ApiError } from '@/lib/api'
import { nodeRoleKind, sortNodes, type NodeRoleKind } from '@/lib/clusterNodes'
import { attention, linkHost, nodeContents, totals, type AttentionItem, type BoardNode, type Totals } from '@/lib/running'
import type { ClusterOverview, ClusterStatus, ComposeProjectWithStatus, Container, Metrics } from '@/types/api'

/** How many nodes are asked at once: a large cluster is not asked all together. */
const CONCURRENCY = 3

const FOLDED_KEY = 'sfpanel-running-folded'

export type NodeLoad =
  | { phase: 'loading' }
  | { phase: 'ready'; projects: ComposeProjectWithStatus[]; containers: Container[]; at: number; refreshError?: string }
  | { phase: 'noDocker'; at: number }
  | { phase: 'error'; error: string }

interface LoadJob {
  id: string
  /** The ?node= the reads are sent with; none for the node the page is on. */
  node?: string
  /** Asked for by the operator: a failed node shows it is being asked again. */
  retry?: boolean
}

export interface SectionMetrics {
  cpu: number
  memory: number
  disk: number
}

export interface BoardSection {
  node: BoardNode
  /** Absent for a node that is not asked (it is not online). */
  load?: NodeLoad
  kind?: NodeRoleKind
  metrics?: SectionMetrics
  /** The ?node= for this node's own reads (the host listeners). */
  nodeParam?: string
  retry: () => void
}

export interface BoardHeader {
  loading: boolean
  error: boolean
  updatedAt: number | null
  retry: () => void
}

export interface BoardModel {
  sections: BoardSection[]
  items: AttentionItem[]
  totals: Totals
  header: BoardHeader
  /** The id of the node this browser is attached to, when the cluster said. */
  localId: string
}

const message = (err: unknown) => (err instanceof Error ? err.message : String(err))

/** Docker's routes absent (404), or there and saying Docker is not: no Docker, which is not a failure. */
function noDocker(err: unknown): boolean {
  const e = err as ApiError | undefined
  return e?.status === 404 || e?.code === 'DOCKER_UNAVAILABLE'
}

/** The host name this browser reaches the panel by: the desktop app's server, or the page's own. */
function browserHost(): string {
  if (api.serverUrl) {
    try {
      return new URL(api.serverUrl).hostname
    } catch {
      // A malformed saved address: the page's own host is the better guess.
    }
  }
  return window.location.hostname
}

/**
 * Asks each node for its stacks and containers, a few nodes at a time. Every
 * node's answer lands on its own, so one slow or failing node holds no other
 * back. A node already queued or in flight is not asked twice.
 */
function useNodeLoads() {
  const [loads, setLoads] = useState<Record<string, NodeLoad>>({})
  const queue = useRef<LoadJob[]>([])
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

  const run = useCallback(async (job: LoadJob) => {
    // A periodic refresh redraws nothing until it has an answer; only a first
    // load, or a retry the operator pressed, shows that it is waiting.
    setLoads((prev) => (!prev[job.id] || (job.retry && prev[job.id].phase === 'error') ? { ...prev, [job.id]: { phase: 'loading' } } : prev))
    try {
      const [projects, containers] = await Promise.all([api.getComposeProjects(job.node), api.getContainers(job.node)])
      if (!mounted.current) return
      setLoads((prev) => ({ ...prev, [job.id]: { phase: 'ready', projects: projects ?? [], containers: containers ?? [], at: Date.now() } }))
    } catch (err) {
      if (!mounted.current) return
      if (noDocker(err)) {
        setLoads((prev) => ({ ...prev, [job.id]: { phase: 'noDocker', at: Date.now() } }))
        return
      }
      // A refresh that fails keeps what was last seen, and says so; a first
      // load that fails is an error, never an empty node.
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

  const enqueue = useCallback((jobs: LoadJob[]) => {
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

  return { loads, enqueue }
}

function latest(loads: (NodeLoad | undefined)[]): number | null {
  const times = loads.map((l) => (l && (l.phase === 'ready' || l.phase === 'noDocker') ? l.at : 0)).filter(Boolean)
  return times.length ? Math.max(...times) : null
}

export type ClusterBoardState =
  | { phase: 'loading' }
  | { phase: 'standalone' }
  | { phase: 'error'; error: string; retry: () => void }
  | { phase: 'ready'; board: BoardModel; clusterName: string }

/** Every node of the cluster: the datacenter scope. */
export function useClusterBoard(): { state: ClusterBoardState; refresh: () => void } {
  const { loads, enqueue } = useNodeLoads()
  const [status, setStatus] = useState<ClusterStatus | null>(null)
  const [overview, setOverview] = useState<{ data: ClusterOverview | null; error: string | null; loading: boolean }>({ data: null, error: null, loading: true })

  useEffect(() => {
    let stale = false
    api.getClusterStatus()
      .then((s) => { if (!stale) setStatus(s) })
      .catch(() => { /* the overview's own answer says what there is to say */ })
    return () => { stale = true }
  }, [])

  const refresh = useCallback(() => {
    setOverview((prev) => ({ ...prev, loading: true }))
    api.getClusterOverview()
      .then((data) => {
        setOverview({ data, error: null, loading: false })
        enqueue(data.nodes.filter((n) => n.status === 'online').map((n) => ({ id: n.id, node: n.id })))
      })
      .catch((err: unknown) => setOverview((prev) => ({ ...prev, error: message(err), loading: false })))
  }, [enqueue])

  const localId = status?.local_id ?? ''
  const board = useMemo<BoardModel | null>(() => {
    const data = overview.data
    if (!data) return null
    const host = browserHost()
    const metrics = new Map((data.metrics ?? []).map((m) => [m.node_id, m]))
    const sections: BoardSection[] = sortNodes(data.nodes, data.leader_id).map((n) => {
      const load = n.status === 'online' ? loads[n.id] : undefined
      const contents = load?.phase === 'ready' ? nodeContents(load.projects, load.containers, linkHost(n.id === localId, n.api_address, host)) : null
      const m = n.status === 'online' ? metrics.get(n.id) : undefined
      return {
        node: { id: n.id, name: n.name, status: n.status, lastSeen: n.last_seen, contents, failed: load?.phase === 'error' },
        load,
        kind: nodeRoleKind(n, data.leader_id),
        metrics: m ? { cpu: m.cpu_percent, memory: m.memory_percent, disk: m.disk_percent } : undefined,
        nodeParam: n.id,
        retry: () => enqueue([{ id: n.id, node: n.id, retry: true }]),
      }
    })
    const nodes = sections.map((s) => s.node)
    const items = attention(nodes)
    return {
      sections,
      items,
      totals: totals(nodes, items),
      header: { loading: overview.loading, error: !!overview.error, updatedAt: latest(sections.map((s) => s.load)), retry: refresh },
      localId,
    }
  }, [overview, loads, localId, enqueue, refresh])

  let state: ClusterBoardState
  if (status && !status.enabled) state = { phase: 'standalone' }
  else if (board && overview.data) state = { phase: 'ready', board, clusterName: overview.data.name }
  else if (overview.error) state = { phase: 'error', error: overview.error, retry: refresh }
  else state = { phase: 'loading' }
  return { state, refresh }
}

/**
 * The node the node menu is on: the server itself when there is no cluster or
 * the local node is selected, otherwise the selected node through ?node=.
 */
export function useNodeBoard(): { board: BoardModel; host: string; metrics: Metrics | null; refresh: () => void } {
  const { loads, enqueue } = useNodeLoads()
  const current = api.currentNode
  const [system, setSystem] = useState<{ host: string; metrics: Metrics | null }>({ host: '', metrics: null })
  // The selected node is reached at its advertised address; the node this
  // browser is on, by the name the browser already uses. Undefined until the
  // node list has said which one the selection is.
  const [linkTo, setLinkTo] = useState<string | null | undefined>(() => (current ? undefined : linkHost(true, undefined, browserHost())))

  useEffect(() => {
    if (!current) return
    let stale = false
    api.getClusterNodes()
      .then((r) => {
        if (stale) return
        const node = r.nodes.find((n) => n.id === current)
        setLinkTo(linkHost(current === r.local_id, node?.api_address, browserHost()))
      })
      .catch(() => { if (!stale) setLinkTo(null) })
    return () => { stale = true }
  }, [current])

  const refresh = useCallback(() => {
    api.getDashboardOverview()
      .then((o) => setSystem({ host: o.host?.hostname ?? '', metrics: o.metrics ?? null }))
      // The host's name and numbers only label the page; the board stands without them.
      .catch(() => {})
    enqueue([{ id: 'self' }])
  }, [enqueue])

  const board = useMemo<BoardModel>(() => {
    const load = loads.self
    const contents = load?.phase === 'ready' ? nodeContents(load.projects, load.containers, linkTo ?? null) : null
    const node: BoardNode = { id: current ?? 'self', name: system.host, status: 'online', contents, failed: load?.phase === 'error' }
    const items = attention([node])
    return {
      sections: [{ node, load: load ?? { phase: 'loading' }, retry: () => enqueue([{ id: 'self', retry: true }]) }],
      items,
      totals: totals([node], items),
      header: { loading: load?.phase === 'loading', error: false, updatedAt: latest([load]), retry: refresh },
      localId: '',
    }
  }, [loads, linkTo, system.host, current, enqueue, refresh])

  return { board, host: system.host, metrics: system.metrics, refresh }
}

function readFolded(): string[] {
  try {
    const saved: unknown = JSON.parse(localStorage.getItem(FOLDED_KEY) || '[]')
    return Array.isArray(saved) ? saved.filter((id): id is string => typeof id === 'string') : []
  } catch {
    return []
  }
}

/** Which node sections are folded, kept across visits. */
export function useFolded(): [ReadonlySet<string>, (id: string) => void] {
  const [folded, setFolded] = useState(() => new Set(readFolded()))
  const toggle = useCallback((id: string) => {
    setFolded((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      try {
        localStorage.setItem(FOLDED_KEY, JSON.stringify([...next]))
      } catch {
        // Storage refused: the fold still holds for this visit.
      }
      return next
    })
  }, [])
  return [folded, toggle]
}
