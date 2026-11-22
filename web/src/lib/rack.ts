import type { LanRack, LanRackDoc, LanRackItem, NetworkInterfaceInfo } from '@/types/api'

// The rack view's model: racks as the operator drew them, kept by several
// nodes and read as one, and the moves that can be made in one — placing,
// moving, resizing and taking out — each refused where two things would take
// the same unit. Pure, so the rules are pinned by rack.test.ts.

export const MAX_UNITS = 60
export const MAX_HEIGHT = 8
export const MAX_RACKS = 32
export const MAX_NAME = 64

export type RackItem = LanRackItem

export interface Rack {
  id: string
  name: string
  units: number
  items: RackItem[]
  /** When it was last written (ms). */
  updatedAt: number
}

const byName = (a: Rack, b: Rack) => a.name.localeCompare(b.name, undefined, { numeric: true }) || a.id.localeCompare(b.id)

/**
 * The nodes' racks as one. A rack is written to every node and one that was
 * down then has an older one: of the copies of a rack, the one written last
 * is the rack — and if that one is a deletion, there is no rack.
 */
export function mergeRacks(lists: Iterable<readonly LanRack[]>): Rack[] {
  const latest = new Map<string, { doc: LanRackDoc; at: number }>()
  for (const list of lists) {
    for (const r of list ?? []) {
      if (!r || typeof r.id !== 'string' || !r.doc) continue
      const at = Date.parse(r.updated_at) || 0
      const was = latest.get(r.id)
      if (!was || at > was.at) latest.set(r.id, { doc: r.doc, at })
    }
  }
  const racks: Rack[] = []
  for (const [id, { doc, at }] of latest) {
    if (doc.deleted) continue
    const units = Number.isInteger(doc.units) && doc.units >= 1 ? Math.min(doc.units, MAX_UNITS) : 1
    racks.push({ id, name: doc.name ?? '', units, items: (doc.items ?? []).filter((i) => i && typeof i.id === 'string').map((i) => ({ ...i })), updatedAt: at })
  }
  return racks.sort(byName)
}

/** The rack as it is written. */
export function rackDoc(rack: Rack, deleted = false): LanRackDoc {
  return deleted
    ? { name: '', units: rack.units, deleted: true, items: [] }
    : { name: rack.name.trim(), units: rack.units, deleted: false, items: rack.items.map((i) => ({ ...i })) }
}

const top = (i: { u: number; height: number }) => i.u + i.height - 1

/** Whether `height` units from `u` upward are inside the rack and free — of everything but `except`. */
export function fits(rack: Rack, u: number, height: number, except?: string): boolean {
  if (!Number.isInteger(u) || !Number.isInteger(height) || u < 1 || height < 1 || height > MAX_HEIGHT || u + height - 1 > rack.units) return false
  return !rack.items.some((i) => i.id !== except && i.u <= u + height - 1 && u <= top(i))
}

/** How tall a thing put at `u` could be: the free units from there upward, at most MAX_HEIGHT; 0 where `u` is taken. */
export function roomAt(rack: Rack, u: number, except?: string): number {
  let height = 0
  while (height < MAX_HEIGHT && fits(rack, u, height + 1, except)) height++
  return height
}

/** The rack with one thing more, or null where it does not fit or the id is taken. */
export function place(rack: Rack, item: RackItem): Rack | null {
  if (rack.items.some((i) => i.id === item.id) || !fits(rack, item.u, item.height)) return null
  return { ...rack, items: [...rack.items, { ...item }] }
}

/** The rack with the thing `by` units higher (lower when negative), or null where it cannot go. */
export function move(rack: Rack, id: string, by: number): Rack | null {
  const item = rack.items.find((i) => i.id === id)
  if (!item || !fits(rack, item.u + by, item.height, id)) return null
  return { ...rack, items: rack.items.map((i) => (i.id === id ? { ...i, u: i.u + by } : i)) }
}

/** The rack with the thing `height` units tall, growing upward, or null where there is no room. */
export function resize(rack: Rack, id: string, height: number): Rack | null {
  const item = rack.items.find((i) => i.id === id)
  if (!item || !fits(rack, item.u, height, id)) return null
  return { ...rack, items: rack.items.map((i) => (i.id === id ? { ...i, height } : i)) }
}

export function remove(rack: Rack, id: string): Rack {
  return { ...rack, items: rack.items.filter((i) => i.id !== id) }
}

/** A slot of the rack as it is read from the top down: a thing, or a free unit. */
export type Slot =
  | { kind: 'item'; item: RackItem; top: number }
  | { kind: 'free'; unit: number }

/**
 * The rack from its top unit to its bottom: each thing once, where it
 * starts, and each free unit. A thing that reaches out of the rack (a rack
 * made shorter by a newer write) is drawn as far as the rack goes.
 */
export function slots(rack: Rack): Slot[] {
  const at = new Map<number, RackItem>()
  for (const item of rack.items) for (let u = item.u; u <= top(item); u++) if (u >= 1 && u <= rack.units && !at.has(u)) at.set(u, item)
  const list: Slot[] = []
  const drawn = new Set<string>()
  for (let u = rack.units; u >= 1; u--) {
    const item = at.get(u)
    if (!item) list.push({ kind: 'free', unit: u })
    else if (!drawn.has(item.id)) {
      drawn.add(item.id)
      list.push({ kind: 'item', item, top: u })
    }
  }
  return list
}

/** The grid rows a thing takes, counted from the top of a rack of `units`: where it starts and how many. */
export function gridRows(units: number, item: { u: number; height: number }): { start: number; span: number } {
  const highest = Math.min(top(item), units)
  const lowest = Math.max(item.u, 1)
  return { start: units - highest + 1, span: Math.max(1, highest - lowest + 1) }
}

/** What the nodes and MACs already in some rack are, so that a thing is offered once. */
export function placed(racks: readonly Rack[]): { nodes: Set<string>; macs: Set<string> } {
  const nodes = new Set<string>()
  const macs = new Set<string>()
  for (const rack of racks) {
    for (const item of rack.items) {
      if (item.kind === 'node' && item.node) nodes.add(item.node)
      if (item.kind === 'device' && item.mac) macs.add(item.mac.toLowerCase())
    }
  }
  return { nodes, macs }
}

/** A name a new rack or thing goes by in the data; nothing an operator reads. */
export function newId(): string {
  const random = globalThis.crypto?.randomUUID?.() ?? `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 10)}`
  return random.replace(/[^A-Za-z0-9_-]/g, '').slice(0, 24)
}

/** What stops a rack from being made or renamed so, or null. */
export function rackProblem(name: string, units: number, racks: number): 'name' | 'units' | 'racks' | null {
  const trimmed = name.trim()
  if (trimmed === '' || [...trimmed].length > MAX_NAME) return 'name'
  if (!Number.isInteger(units) || units < 1 || units > MAX_UNITS) return 'units'
  return racks >= MAX_RACKS ? 'racks' : null
}

/** The lowest unit a rack made `units` tall must keep: no thing may be left reaching above it. */
export function minUnits(rack: Rack): number {
  return rack.items.reduce((highest, i) => Math.max(highest, top(i)), 1)
}

export interface Port {
  name: string
  /** The link is up. */
  up: boolean
  /** "1G", "2.5G", "100M"; "" where the node does not say. */
  speed: string
}

/** A link speed in Mbps as it is said of a port. */
export function speedLabel(mbps: number): string {
  if (!(mbps > 0)) return ''
  if (mbps >= 1000) return `${Number((mbps / 1000).toFixed(1))}G`
  return `${mbps}M`
}

// What is no socket on the machine: loopback, bridges, bonds and VLANs made of
// the real ones, containers' and guests' ends, tunnels.
const NOT_A_PORT = /^(lo$|docker|br-|veth|virbr|lxcbr|lxdbr|vmbr|bond|tap|fwbr|fwpr|fwln|tun|wg|tailscale|zt|cni|flannel|cali|podman|dummy)/

/**
 * A node's network ports as its rack unit shows them: the physical
 * interfaces, by name. The kernel says which is one (no link kind, an
 * Ethernet type); a node older than that field is told by name.
 */
export function nodePorts(list: readonly NetworkInterfaceInfo[]): Port[] {
  return list
    .filter((i) => i.type === 'ethernet' && !i.kind && !NOT_A_PORT.test(i.name) && !i.name.includes('.'))
    .map((i) => ({ name: i.name, up: i.state === 'up', speed: speedLabel(i.speed) }))
    .sort((a, b) => a.name.localeCompare(b.name, undefined, { numeric: true }))
}
