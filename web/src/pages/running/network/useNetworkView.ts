import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, type ApiError } from '@/lib/api'
import { knownNodes, labelTargets, networkEntries, wakeNodes, type DeviceLabel, type MapNode, type NetworkEntry } from '@/lib/netmap'
import type { LanScanStatus, NetworkInterfaceInfo, PortMapRow } from '@/types/api'
import type { BoardSection } from '../useRunningBoard'

/** How many nodes are asked at once. */
const CONCURRENCY = 3
/** How often a node that is scanning is asked whether it is done. */
const POLL_MS = 2000

export type ScanLoad =
  | { phase: 'loading' }
  | { phase: 'ready'; status: LanScanStatus }
  /** A node older than the route: it has no scans, which is no failure. */
  | { phase: 'unsupported' }
  | { phase: 'error'; error: string }

export type InterfacesLoad =
  | { phase: 'loading' }
  | { phase: 'ready'; list: NetworkInterfaceInfo[] }
  | { phase: 'error'; error: string }

export type PortsLoad =
  | { phase: 'loading' }
  | { phase: 'ready'; rows: PortMapRow[] }
  | { phase: 'error'; error: string }

/** How a scan the page was watching ended, when not well. */
export interface ScanEnd {
  nodeId: string
  network: string
  state: 'failed' | 'timeout' | 'cancelled'
}

export interface NetworkServer {
  id: string
  name: string
  online: boolean
  interfaces?: InterfacesLoad
  ports?: PortsLoad
  /** Asks the node for its interfaces and ports again. */
  retry: () => void
}

export interface NetworkModel {
  entries: NetworkEntry[]
  servers: NetworkServer[]
  /** No node has answered about its scans yet. */
  loading: boolean
  /** Nodes that could not be asked about their scans. */
  failed: { id: string; name: string; error: string }[]
  /** Every node that answered is older than the route. */
  unsupported: boolean
  ended: ScanEnd | null
  dismissEnded: () => void
  /** Starts a scan; resolves to what went wrong, or null. */
  start: (nodeId: string, network: string, ports: boolean) => Promise<string | null>
  cancel: (nodeId: string) => Promise<void>
  /**
   * Writes what the operator said of a device to every node on its network
   * that keeps a registry — one node going away does not take the label with
   * it — all with the same time, so the nodes agree which label is the last.
   * Resolves to what went wrong when no node took it, or null.
   */
  saveDevice: (entry: NetworkEntry, mac: string, label: DeviceLabel) => Promise<string | null>
  /** The operator has seen that a device is new. */
  ackDevice: (entry: NetworkEntry, mac: string) => Promise<string | null>
  /** Takes a device out of the registries. */
  forgetDevice: (entry: NetworkEntry, mac: string) => Promise<string | null>
  /** Has a node that knows the device send it a Wake-on-LAN packet. */
  wakeDevice: (entry: NetworkEntry, mac: string) => Promise<string | null>
  /** Sets how often a node scans the network by itself (0 = never). */
  setSchedule: (entry: NetworkEntry, nodeId: string, intervalMinutes: number, portsDaily: boolean) => Promise<string | null>
}

/** What `saveDevice` and its like resolve to when there is no node to write to. */
export const NO_REGISTRY = 'no-registry'

const message = (err: unknown) => (err instanceof Error ? err.message : String(err))

interface Target {
  id: string
  /** The ?node= its reads are sent with; none for the node the page is on. */
  node?: string
}

/**
 * What the network view shows: each online node's scans, interfaces and
 * listening ports, asked once when the view opens and again when `round`
 * changes (the operator's refresh) — not with the board's own timer, since a
 * port map walks every socket of a node. A node that is scanning is asked
 * every two seconds until it is done.
 */
export function useNetworkView(sections: BoardSection[], round: number): NetworkModel {
  const [scans, setScans] = useState<Record<string, ScanLoad>>({})
  const [interfaces, setInterfaces] = useState<Record<string, InterfacesLoad>>({})
  const [ports, setPorts] = useState<Record<string, PortsLoad>>({})
  const [ended, setEnded] = useState<ScanEnd | null>(null)
  const mounted = useRef(true)
  // The scans this page saw running, to tell how each ended.
  const watching = useRef(new Map<string, string>())

  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false }
  }, [])

  const key = sections.filter((s) => s.node.status === 'online').map((s) => `${s.node.id}\t${s.nodeParam ?? ''}`).join('\n')
  const targets = useMemo<Target[]>(
    () => (key ? key.split('\n').map((line) => {
      const [id, node] = line.split('\t')
      return { id, node: node || undefined }
    }) : []),
    [key],
  )
  const targetOf = useRef(new Map<string, Target>())
  useEffect(() => { targetOf.current = new Map(targets.map((t) => [t.id, t])) }, [targets])

  const readScan = useCallback(async (t: Target) => {
    try {
      const status = await api.getLanScan(t.node)
      if (!mounted.current) return
      setScans((prev) => ({ ...prev, [t.id]: { phase: 'ready', status } }))
      const was = watching.current.get(t.id)
      if (status.running) watching.current.set(t.id, status.running.network)
      else if (was !== undefined) {
        watching.current.delete(t.id)
        const last = status.last
        if (last && last.network === was && last.state !== 'done') setEnded({ nodeId: t.id, network: was, state: last.state })
      }
    } catch (err) {
      if (!mounted.current) return
      const unsupported = (err as ApiError | undefined)?.status === 404
      setScans((prev) => {
        const before = prev[t.id]
        // A refresh that fails keeps what was last seen.
        if (!unsupported && before?.phase === 'ready') return prev
        return { ...prev, [t.id]: unsupported ? { phase: 'unsupported' } : { phase: 'error', error: message(err) } }
      })
    }
  }, [])

  const readDetails = useCallback(async (t: Target) => {
    const nics = api.getNetworkInterfaces(t.node)
      .then((list) => { if (mounted.current) setInterfaces((prev) => ({ ...prev, [t.id]: { phase: 'ready', list: Array.isArray(list) ? list : [] } })) })
      .catch((err: unknown) => {
        if (mounted.current) setInterfaces((prev) => (prev[t.id]?.phase === 'ready' ? prev : { ...prev, [t.id]: { phase: 'error', error: message(err) } }))
      })
    const map = api.getPortMap(t.node)
      .then((rows) => { if (mounted.current) setPorts((prev) => ({ ...prev, [t.id]: { phase: 'ready', rows: Array.isArray(rows) ? rows : [] } })) })
      .catch((err: unknown) => {
        if (mounted.current) setPorts((prev) => (prev[t.id]?.phase === 'ready' ? prev : { ...prev, [t.id]: { phase: 'error', error: message(err) } }))
      })
    await Promise.all([nics, map])
  }, [])

  useEffect(() => {
    if (targets.length === 0) return
    const todo = [...targets]
    let stopped = false
    setScans((prev) => {
      const next = { ...prev }
      for (const t of targets) if (!next[t.id]) next[t.id] = { phase: 'loading' }
      return next
    })
    const next = async (): Promise<void> => {
      const t = todo.shift()
      if (!t || stopped) return
      await Promise.all([readScan(t), readDetails(t)])
      return next()
    }
    for (let i = 0; i < CONCURRENCY; i++) void next()
    return () => { stopped = true }
  }, [targets, round, readScan, readDetails])

  const scanning = useMemo(
    () => Object.entries(scans).filter(([, s]) => s.phase === 'ready' && s.status.running).map(([id]) => id).join('\n'),
    [scans],
  )
  useEffect(() => {
    if (!scanning) return
    const ids = scanning.split('\n')
    const timer = setInterval(() => {
      for (const id of ids) {
        const t = targetOf.current.get(id)
        if (t) void readScan(t)
      }
    }, POLL_MS)
    return () => clearInterval(timer)
  }, [scanning, readScan])

  const start = useCallback(async (nodeId: string, network: string, withPorts: boolean) => {
    const t = targetOf.current.get(nodeId)
    if (!t) return null
    let problem: string | null = null
    try {
      await api.startLanScan(network, withPorts, t.node)
      watching.current.set(nodeId, network)
      setEnded(null)
    } catch (err) {
      // One is running already: the next read shows it, which is the answer.
      if ((err as ApiError | undefined)?.code !== 'LAN_SCAN_IN_PROGRESS') problem = message(err)
    }
    await readScan(t)
    return problem
  }, [readScan])

  const cancel = useCallback(async (nodeId: string) => {
    const t = targetOf.current.get(nodeId)
    if (!t) return
    try {
      await api.cancelLanScan(t.node)
    } catch {
      // The next read says whether it still runs.
    }
    await readScan(t)
  }, [readScan])

  const dismissEnded = useCallback(() => setEnded(null), [])

  // The same request to each node of a network; done when every node has
  // answered, well when any did. `tolerate` is a refusal that is no failure
  // (a node that never saw the device has nothing to acknowledge).
  const toEach = useCallback(async (entry: NetworkEntry, send: (node: string | undefined) => Promise<unknown>, tolerate?: string) => {
    const targets = labelTargets(entry).map((id) => targetOf.current.get(id)).filter((t): t is Target => !!t)
    if (targets.length === 0) return NO_REGISTRY
    const results = await Promise.allSettled(targets.map((t) => send(t.node)))
    await Promise.all(targets.map((t) => readScan(t)))
    const failures = results.filter((r): r is PromiseRejectedResult => r.status === 'rejected' && (r.reason as ApiError | undefined)?.code !== tolerate)
    return failures.length === targets.length ? message(failures[0].reason) : null
  }, [readScan])

  const saveDevice = useCallback((entry: NetworkEntry, mac: string, label: DeviceLabel) => {
    const labelled_at = new Date().toISOString()
    return toEach(entry, (node) => api.saveLanDevice(mac, { ...label, labelled_at }, node))
  }, [toEach])
  const ackDevice = useCallback((entry: NetworkEntry, mac: string) => toEach(entry, (node) => api.ackLanDevice(mac, node), 'LAN_DEVICE_NOT_FOUND'), [toEach])
  const forgetDevice = useCallback((entry: NetworkEntry, mac: string) => toEach(entry, (node) => api.forgetLanDevice(mac, node)), [toEach])

  // What each node last answered, for asking the node that knows a device.
  const statusesNow = useRef(new Map<string, LanScanStatus>())
  useEffect(() => {
    statusesNow.current = new Map(Object.entries(scans).flatMap(([id, s]) => (s.phase === 'ready' ? [[id, s.status] as [string, LanScanStatus]] : [])))
  }, [scans])
  const wakeDevice = useCallback(async (entry: NetworkEntry, mac: string) => {
    const from = wakeNodes(entry, mac, statusesNow.current)[0]
    const t = from ? targetOf.current.get(from.nodeId) : undefined
    if (!from || !t) return NO_REGISTRY
    try {
      await api.wakeLanDevice(mac, from.network, t.node)
      return null
    } catch (err) {
      return message(err)
    }
  }, [])

  const nodes = useMemo<MapNode[]>(() => sections.map((s) => {
    const nics = interfaces[s.node.id]
    return {
      id: s.node.id,
      name: s.node.name || s.node.id,
      online: s.node.status === 'online',
      macs: nics?.phase === 'ready' ? nics.list.map((i) => i.mac_address).filter(Boolean) : [],
      addresses: nics?.phase === 'ready' ? nics.list.flatMap((i) => (i.addresses ?? []).filter((a) => a.family === 'ipv4').map((a) => a.address)) : [],
      guests: s.guests?.guests ?? [],
    }
  }), [sections, interfaces])
  const nodesNow = useRef<MapNode[]>([])
  useEffect(() => { nodesNow.current = nodes }, [nodes])

  const setSchedule = useCallback(async (entry: NetworkEntry, nodeId: string, intervalMinutes: number, portsDaily: boolean) => {
    const t = targetOf.current.get(nodeId)
    const network = entry.scanners.find((x) => x.nodeId === nodeId)?.network
    if (!t || !network) return NO_REGISTRY
    let problem: string | null = null
    try {
      // The node is told the cluster's own addresses there, to stop by itself when one of them is in conflict.
      await api.setLanSchedule({ network, interval_minutes: intervalMinutes, ports_daily: portsDaily, known: knownNodes(nodesNow.current, entry.cidr) }, t.node)
    } catch (err) {
      problem = message(err)
    }
    await readScan(t)
    return problem
  }, [readScan])

  return useMemo<NetworkModel>(() => {
    const statuses = new Map<string, LanScanStatus>()
    for (const n of nodes) {
      const scan = scans[n.id]
      if (scan?.phase === 'ready') statuses.set(n.id, scan.status)
    }
    const asked = nodes.filter((n) => n.online).map((n) => ({ node: n, scan: scans[n.id] }))
    const answered = asked.filter((a) => a.scan && a.scan.phase !== 'loading')
    return {
      entries: networkEntries(nodes, statuses),
      servers: sections.map((s) => ({
        id: s.node.id,
        name: s.node.name || s.node.id,
        online: s.node.status === 'online',
        interfaces: interfaces[s.node.id],
        ports: ports[s.node.id],
        retry: () => {
          const t = targetOf.current.get(s.node.id)
          if (!t) return
          setInterfaces((prev) => (prev[t.id]?.phase === 'error' ? { ...prev, [t.id]: { phase: 'loading' } } : prev))
          setPorts((prev) => (prev[t.id]?.phase === 'error' ? { ...prev, [t.id]: { phase: 'loading' } } : prev))
          void readDetails(t)
        },
      })),
      loading: asked.length > 0 && answered.length === 0,
      failed: asked.flatMap((a) => (a.scan?.phase === 'error' ? [{ id: a.node.id, name: a.node.name, error: a.scan.error }] : [])),
      unsupported: answered.length > 0 && answered.every((a) => a.scan!.phase === 'unsupported'),
      ended,
      dismissEnded,
      start,
      cancel,
      saveDevice,
      ackDevice,
      forgetDevice,
      wakeDevice,
      setSchedule,
    }
  }, [sections, nodes, scans, interfaces, ports, ended, dismissEnded, start, cancel, readDetails, saveDevice, ackDevice, forgetDevice, wakeDevice, setSchedule])
}
