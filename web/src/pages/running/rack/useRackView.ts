import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api, type ApiError } from '@/lib/api'
import { networkEntries, writeTime, type MapDevice, type MapNode } from '@/lib/netmap'
import { mergeRacks, nodePorts, rackDoc, type Port, type Rack } from '@/lib/rack'
import type { LanRack, LanScanStatus, NetworkInterfaceInfo } from '@/types/api'
import type { BoardSection } from '../useRunningBoard'

/** How many nodes are asked at once. */
const CONCURRENCY = 3

type Load<T> = { phase: 'loading' } | { phase: 'ready'; value: T } | { phase: 'unsupported' } | { phase: 'error'; error: string }

export interface RackNode {
  id: string
  name: string
  online: boolean
  /** Its first address, where its interfaces were read. */
  address: string
  /** Its sockets; null until its interfaces were read. */
  ports: Port[] | null
}

export interface RackModel {
  racks: Rack[]
  nodes: RackNode[]
  /** The devices the networks' scans and registries know, by MAC. */
  devices: ReadonlyMap<string, MapDevice>
  loading: boolean
  /** No node keeps racks: every one that answered is older than them. */
  unsupported: boolean
  failed: { id: string; name: string; error: string }[]
  /**
   * Writes a rack — or its deletion — to every node that answers and keeps
   * racks, all with one time. Resolves to what went wrong when none took it.
   */
  save: (rack: Rack, deleted?: boolean) => Promise<string | null>
}

/** What `save` resolves to when no node keeps racks. */
export const NO_RACK_NODE = 'no-rack-node'

const message = (err: unknown) => (err instanceof Error ? err.message : String(err))

interface Target {
  id: string
  node?: string
}

/**
 * What the rack view shows: the racks each node keeps, read as one, with what
 * the nodes know of themselves (their ports) and of the network (the
 * registry) to fill the units with. Asked when the view opens and when
 * `round` changes.
 */
export function useRackView(sections: BoardSection[], round: number): RackModel {
  const [racks, setRacks] = useState<Record<string, Load<LanRack[]>>>({})
  const [interfaces, setInterfaces] = useState<Record<string, NetworkInterfaceInfo[]>>({})
  const [scans, setScans] = useState<Record<string, LanScanStatus>>({})
  // What was just written, drawn until the nodes have answered: null is a deletion.
  const [pending, setPending] = useState<Record<string, Rack | null>>({})
  const mounted = useRef(true)
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

  const readRacks = useCallback(async (t: Target) => {
    try {
      const answer = await api.getRacks(t.node)
      if (mounted.current) setRacks((prev) => ({ ...prev, [t.id]: { phase: 'ready', value: Array.isArray(answer?.racks) ? answer.racks : [] } }))
    } catch (err) {
      if (!mounted.current) return
      const unsupported = (err as ApiError | undefined)?.status === 404
      setRacks((prev) => {
        // A refresh that fails keeps what was last read.
        if (!unsupported && prev[t.id]?.phase === 'ready') return prev
        return { ...prev, [t.id]: unsupported ? { phase: 'unsupported' } : { phase: 'error', error: message(err) } }
      })
    }
  }, [])

  useEffect(() => {
    if (targets.length === 0) return
    const todo = [...targets]
    let stopped = false
    setRacks((prev) => {
      const next = { ...prev }
      for (const t of targets) if (!next[t.id]) next[t.id] = { phase: 'loading' }
      return next
    })
    const next = async (): Promise<void> => {
      const t = todo.shift()
      if (!t || stopped) return
      await Promise.all([
        readRacks(t),
        // What fills the units; a node that cannot say leaves its units with a name only.
        api.getNetworkInterfaces(t.node).then((list) => { if (mounted.current) setInterfaces((prev) => ({ ...prev, [t.id]: Array.isArray(list) ? list : [] })) }).catch(() => {}),
        api.getLanScan(t.node).then((status) => { if (mounted.current) setScans((prev) => ({ ...prev, [t.id]: status })) }).catch(() => {}),
      ])
      return next()
    }
    for (let i = 0; i < CONCURRENCY; i++) void next()
    return () => { stopped = true }
  }, [targets, round, readRacks])

  const racksNow = useRef(racks)
  useEffect(() => { racksNow.current = racks }, [racks])
  const save = useCallback(async (rack: Rack, deleted = false) => {
    const to = [...targetOf.current.values()].filter((t) => racksNow.current[t.id]?.phase === 'ready')
    if (to.length === 0) return NO_RACK_NODE
    // Later than the copy it changes, whichever clock wrote that one.
    const at = writeTime(rack.updatedAt, Date.now())
    const updated_at = new Date(at).toISOString()
    const shown = deleted ? null : { ...rack, updatedAt: at }
    setPending((prev) => ({ ...prev, [rack.id]: shown }))
    const results = await Promise.allSettled(to.map((t) => api.saveRack(rack.id, rackDoc(rack, deleted), updated_at, t.node)))
    await Promise.all(to.map((t) => readRacks(t)))
    if (mounted.current) {
      setPending((prev) => {
        // A change made meanwhile is drawn until its own write is done.
        if (prev[rack.id] !== shown) return prev
        const next = { ...prev }
        delete next[rack.id]
        return next
      })
    }
    const failures = results.filter((r): r is PromiseRejectedResult => r.status === 'rejected')
    return failures.length === to.length ? message(failures[0].reason) : null
  }, [readRacks])

  return useMemo<RackModel>(() => {
    const mapNodes: MapNode[] = sections.map((s) => {
      const nics = interfaces[s.node.id]
      return {
        id: s.node.id,
        name: s.node.name || s.node.id,
        online: s.node.status === 'online',
        macs: (nics ?? []).map((i) => i.mac_address).filter(Boolean),
        addresses: (nics ?? []).flatMap((i) => (i.addresses ?? []).filter((a) => a.family === 'ipv4').map((a) => a.address)),
        guests: s.guests?.guests ?? [],
      }
    })
    const devices = new Map<string, MapDevice>()
    const walk = (list: MapDevice[]) => {
      for (const d of list) {
        if (d.mac && !devices.has(d.mac)) devices.set(d.mac, d)
        walk(d.children)
      }
    }
    for (const entry of networkEntries(mapNodes, new Map(Object.entries(scans)))) {
      if (!entry.segment) continue
      walk(entry.segment.gateways)
      walk(entry.segment.gear)
      walk(entry.segment.servers)
      walk(entry.segment.devices)
    }

    const merged = mergeRacks(Object.values(racks).flatMap((r) => (r.phase === 'ready' ? [r.value] : [])))
    const drawn = merged.filter((r) => !(r.id in pending)).concat(Object.values(pending).filter((r): r is Rack => r !== null))
    drawn.sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }) || a.id.localeCompare(b.id))

    const asked = sections.filter((s) => s.node.status === 'online').map((s) => ({ node: s.node, load: racks[s.node.id] }))
    const answered = asked.filter((a) => a.load && a.load.phase !== 'loading')
    return {
      racks: drawn,
      nodes: sections.map((s) => {
        const nics = interfaces[s.node.id]
        const first = (nics ?? []).find((i) => i.is_default) ?? (nics ?? []).find((i) => (i.addresses ?? []).some((a) => a.family === 'ipv4' && !a.address.startsWith('127.')))
        return {
          id: s.node.id,
          name: s.node.name || s.node.id,
          online: s.node.status === 'online',
          address: first?.addresses?.find((a) => a.family === 'ipv4')?.address ?? '',
          ports: nics ? nodePorts(nics) : null,
        }
      }),
      devices,
      loading: asked.length > 0 && answered.length === 0,
      unsupported: answered.length > 0 && answered.every((a) => a.load!.phase === 'unsupported'),
      failed: asked.flatMap((a) => (a.load?.phase === 'error' ? [{ id: a.node.id, name: a.node.name || a.node.id, error: a.load.error }] : [])),
      save,
    }
  }, [sections, racks, interfaces, scans, pending, save])
}
