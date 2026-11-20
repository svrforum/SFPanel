import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, type ApiError } from '@/lib/api'
import { nodeRoleKind, sortNodes, type NodeRoleKind } from '@/lib/clusterNodes'
import {
  attention, guestKey, guestServers, linkHost, nodeContents, runningGuests, totals,
  type AttentionItem, type BoardNode, type GuestHost, type RunningGuest, type Totals,
} from '@/lib/running'
import type { ClusterNode, ClusterOverview, ClusterStatus, ComposeProjectWithStatus, Container, Metrics, PveGuest } from '@/types/api'

/** How many nodes are asked at once: a large cluster is not asked all together. */
const CONCURRENCY = 3

const FOLDED_KEY = 'sfpanel-running-folded'
const VIEW_KEY = 'sfpanel-running-view'

export type NodeLoad =
  | { phase: 'loading' }
  | { phase: 'ready'; projects: ComposeProjectWithStatus[]; containers: Container[]; at: number; refreshError?: string }
  | { phase: 'noDocker'; at: number }
  | { phase: 'error'; error: string }

/** A node's Proxmox guests. A node that has no such route (404) has no load at all. */
export type GuestLoad =
  | { phase: 'ready'; list: PveGuest[]; at: number; refreshError?: string }
  | { phase: 'error'; error: string }

interface LoadJob {
  id: string
  /** The ?node= the reads are sent with; none for the node the page is on. */
  node?: string
  /** Asked for by the operator: a failed node shows it is being asked again. */
  retry?: boolean
}

/** A node that a guest is, as its tile names it. */
export interface GuestServer {
  id: string
  name: string
}

/**
 * A node's virtual machines and LXC containers: there when the node listed
 * some, or could not list them. Absent for a node that is no Proxmox host, or
 * that has not answered.
 */
export interface GuestGroup {
  load: GuestLoad
  guests: RunningGuest[]
  /** The cluster node each guest is, by vmid. */
  servers: ReadonlyMap<number, GuestServer>
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
  guests?: GuestGroup
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
 * Asks each node for its stacks, containers and Proxmox guests, a few nodes at
 * a time. Every node's answer lands on its own, so one slow or failing node
 * holds no other back, and the guests on their own too: a node whose guests
 * could not be listed still shows its stacks. A node already queued or in
 * flight is not asked twice.
 */
function useNodeLoads() {
  const [loads, setLoads] = useState<Record<string, NodeLoad>>({})
  const [guestLoads, setGuestLoads] = useState<Record<string, GuestLoad>>({})
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
    const docker = async () => {
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
    }
    const guests = async () => {
      try {
        const list = await api.getGuests(job.node)
        if (!mounted.current) return
        setGuestLoads((prev) => ({ ...prev, [job.id]: { phase: 'ready', list: Array.isArray(list) ? list : [], at: Date.now() } }))
      } catch (err) {
        if (!mounted.current) return
        setGuestLoads((prev) => {
          const next = { ...prev }
          // A node from before the route has no guests to show, which is no failure.
          if ((err as ApiError | undefined)?.status === 404) delete next[job.id]
          else {
            // As with the stacks: what was seen stays, saying it is old; a
            // list never seen is an error, never "no guests".
            const was = prev[job.id]
            next[job.id] = was?.phase === 'ready' ? { ...was, refreshError: message(err) } : { phase: 'error', error: message(err) }
          }
          return next
        })
      }
    }
    // A periodic refresh redraws nothing until it has an answer; only a first
    // load, or a retry the operator pressed, shows that it is waiting.
    setLoads((prev) => (!prev[job.id] || (job.retry && prev[job.id].phase === 'error') ? { ...prev, [job.id]: { phase: 'loading' } } : prev))
    await Promise.all([docker(), guests()])
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

  return { loads, guestLoads, enqueue }
}

/**
 * The NIC addresses of the given nodes, asked only while `nodes` is not null
 * (some node has guests), a few at a time. An answer is kept for the visit;
 * a node that did not answer is asked again with the next round, and until
 * then is no guest of anyone, which is all a failed read means here.
 */
function useInterfaces(nodes: string[] | null, round: number): ReadonlyMap<string, string[]> {
  const [known, setKnown] = useState<ReadonlyMap<string, string[]>>(() => new Map())
  // Answered or in flight, so not asked again; a failed read is let go.
  const taken = useRef(new Set<string>())
  const key = nodes ? nodes.join('\n') : null

  useEffect(() => {
    if (key === null) return
    const takenNow = taken.current
    const todo = key.split('\n').filter((id) => id && !takenNow.has(id))
    for (const id of todo) takenNow.add(id)
    const next = async (): Promise<void> => {
      const id = todo.shift()
      if (id === undefined) return
      try {
        const list = await api.getNetworkInterfaces(id)
        const macs = (Array.isArray(list) ? list : []).map((i) => i.mac_address).filter(Boolean)
        setKnown((prev) => new Map(prev).set(id, macs))
      } catch {
        takenNow.delete(id)
      }
      return next()
    }
    for (let i = 0; i < CONCURRENCY; i++) void next()
    return () => {
      // Not yet asked: let go, for the next run to ask. One in flight lands.
      for (const id of todo) takenNow.delete(id)
      todo.length = 0
    }
  }, [key, round])

  return known
}

/**
 * The guest group of each node that has one, with the node each guest is.
 * `hosts` are the loaded nodes in board order; `names` the cluster's nodes.
 */
function guestGroups(
  hosts: { id: string; load: GuestLoad | undefined }[],
  interfaces: ReadonlyMap<string, string[]>,
  names: ReadonlyMap<string, string>,
): Map<string, GuestGroup> {
  const lists: GuestHost[] = hosts
    .filter((h) => h.load?.phase === 'ready')
    .map((h) => ({ nodeId: h.id, guests: runningGuests((h.load as Extract<GuestLoad, { phase: 'ready' }>).list) }))
  const matched = guestServers(lists, interfaces)
  const groups = new Map<string, GuestGroup>()
  for (const h of hosts) {
    if (!h.load) continue
    const guests = lists.find((l) => l.nodeId === h.id)?.guests ?? []
    // A node that listed none is no Proxmox host, or one with nothing on it.
    if (h.load.phase === 'ready' && guests.length === 0) continue
    const servers = new Map<number, GuestServer>()
    for (const g of guests) {
      const id = matched.get(guestKey(h.id, g.vmid))
      if (id) servers.set(g.vmid, { id, name: names.get(id) ?? id })
    }
    groups.set(h.id, { load: h.load, guests, servers })
  }
  return groups
}

const hasGuests = (loads: Record<string, GuestLoad>) => Object.values(loads).some((l) => l.phase === 'ready' && l.list.length > 0)

function latest(loads: (NodeLoad | undefined)[]): number | null {
  const times = loads.map((l) => (l && (l.phase === 'ready' || l.phase === 'noDocker') ? l.at : 0)).filter(Boolean)
  return times.length ? Math.max(...times) : null
}

export type ClusterBoardState =
  | { phase: 'loading' }
  | { phase: 'standalone' }
  | { phase: 'error'; error: string }
  | { phase: 'ready'; board: BoardModel }

/** Every node of the cluster: the datacenter scope. */
export function useClusterBoard(): { state: ClusterBoardState; refresh: () => void } {
  const { loads, guestLoads, enqueue } = useNodeLoads()
  const [status, setStatus] = useState<ClusterStatus | null>(null)
  const [overview, setOverview] = useState<{ data: ClusterOverview | null; error: string | null; loading: boolean }>({ data: null, error: null, loading: true })
  const [round, setRound] = useState(0)

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
        setRound((r) => r + 1)
        enqueue(data.nodes.filter((n) => n.status === 'online').map((n) => ({ id: n.id, node: n.id })))
      })
      .catch((err: unknown) => setOverview((prev) => ({ ...prev, error: message(err), loading: false })))
  }, [enqueue])

  const localId = status?.local_id ?? ''
  const online = useMemo(() => overview.data?.nodes.filter((n) => n.status === 'online').map((n) => n.id) ?? [], [overview.data])
  const interfaces = useInterfaces(hasGuests(guestLoads) ? online : null, round)
  const board = useMemo<BoardModel | null>(() => {
    const data = overview.data
    if (!data) return null
    const host = browserHost()
    const metrics = new Map((data.metrics ?? []).map((m) => [m.node_id, m]))
    const ordered = sortNodes(data.nodes, data.leader_id)
    const groups = guestGroups(
      ordered.map((n) => ({ id: n.id, load: n.status === 'online' ? guestLoads[n.id] : undefined })),
      interfaces,
      new Map(data.nodes.map((n) => [n.id, n.name])),
    )
    const sections: BoardSection[] = ordered.map((n) => {
      const load = n.status === 'online' ? loads[n.id] : undefined
      const contents = load?.phase === 'ready' ? nodeContents(load.projects, load.containers, linkHost(n.id === localId, n.api_address, host)) : null
      const m = n.status === 'online' ? metrics.get(n.id) : undefined
      return {
        node: { id: n.id, name: n.name, status: n.status, lastSeen: n.last_seen, contents, failed: load?.phase === 'error' },
        load,
        kind: nodeRoleKind(n, data.leader_id),
        metrics: m ? { cpu: m.cpu_percent, memory: m.memory_percent, disk: m.disk_percent } : undefined,
        nodeParam: n.id,
        guests: groups.get(n.id),
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
  }, [overview, loads, guestLoads, interfaces, localId, enqueue, refresh])

  let state: ClusterBoardState
  if (status && !status.enabled) state = { phase: 'standalone' }
  else if (board) state = { phase: 'ready', board }
  else if (overview.error) state = { phase: 'error', error: overview.error }
  else state = { phase: 'loading' }
  return { state, refresh }
}

/**
 * The node the node menu is on: the server itself when there is no cluster or
 * the local node is selected, otherwise the selected node through ?node=.
 */
export function useNodeBoard(): { board: BoardModel; host: string; metrics: Metrics | null; refresh: () => void } {
  const { loads, guestLoads, enqueue } = useNodeLoads()
  const current = api.currentNode
  const [system, setSystem] = useState<{ host: string; metrics: Metrics | null }>({ host: '', metrics: null })
  // The selected node is reached at its advertised address; the node this
  // browser is on, by the name the browser already uses. Undefined until the
  // node list has said which one the selection is.
  const [linkTo, setLinkTo] = useState<string | null | undefined>(() => (current ? undefined : linkHost(true, undefined, browserHost())))
  // The cluster's nodes: asked for a selected node's address, and for which
  // of this node's guests are other nodes. Empty with no cluster.
  const [cluster, setCluster] = useState<{ nodes: ClusterNode[]; localId: string } | null>(null)
  const [round, setRound] = useState(0)
  const guestsHere = hasGuests(guestLoads)

  useEffect(() => {
    if (!current && !guestsHere) return
    let stale = false
    api.getClusterNodes()
      .then((r) => {
        if (stale) return
        setCluster({ nodes: r.nodes ?? [], localId: r.local_id ?? '' })
        if (!current) return
        const node = r.nodes.find((n) => n.id === current)
        setLinkTo(linkHost(current === r.local_id, node?.api_address, browserHost()))
      })
      .catch(() => { if (!stale && current) setLinkTo(null) })
    return () => { stale = true }
  }, [current, guestsHere])

  const online = useMemo(() => cluster?.nodes.filter((n) => n.status === 'online').map((n) => n.id) ?? [], [cluster])
  const interfaces = useInterfaces(guestsHere && online.length > 0 ? online : null, round)

  const refresh = useCallback(() => {
    setRound((r) => r + 1)
    api.getDashboardOverview()
      .then((o) => setSystem({ host: o.host?.hostname ?? '', metrics: o.metrics ?? null }))
      // The host's name and numbers only label the page; the board stands without them.
      .catch(() => {})
    enqueue([{ id: 'self' }])
  }, [enqueue])

  const board = useMemo<BoardModel>(() => {
    const load = loads.self
    const contents = load?.phase === 'ready' ? nodeContents(load.projects, load.containers, linkTo ?? null) : null
    // Within the cluster this node goes by its id, which is what the other
    // nodes' interfaces are keyed by; the local node's is the cluster's local id.
    const selfId = current ?? (cluster?.localId || 'self')
    const node: BoardNode = { id: selfId, name: system.host, status: 'online', contents, failed: load?.phase === 'error' }
    const items = attention([node])
    const guests = guestGroups([{ id: selfId, load: guestLoads.self }], interfaces, new Map(cluster?.nodes.map((n) => [n.id, n.name]) ?? [])).get(selfId)
    return {
      sections: [{ node, load: load ?? { phase: 'loading' }, guests, retry: () => enqueue([{ id: 'self', retry: true }]) }],
      items,
      totals: totals([node], items),
      header: { loading: load?.phase === 'loading', error: false, updatedAt: latest([load]), retry: refresh },
      localId: cluster?.localId ?? '',
    }
  }, [loads, guestLoads, interfaces, cluster, linkTo, system.host, current, enqueue, refresh])

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

export type RunningView = 'board' | 'canvas'

/** Board or canvas, kept across visits. The board unless the canvas was chosen: it is the form for a phone and a screen reader. */
export function useRunningView(): [RunningView, (view: RunningView) => void] {
  const [view, setView] = useState<RunningView>(() => {
    try {
      return localStorage.getItem(VIEW_KEY) === 'canvas' ? 'canvas' : 'board'
    } catch {
      return 'board'
    }
  })
  const choose = useCallback((next: RunningView) => {
    setView(next)
    try {
      localStorage.setItem(VIEW_KEY, next)
    } catch {
      // Storage refused: the choice holds for this visit.
    }
  }, [])
  return [view, choose]
}
