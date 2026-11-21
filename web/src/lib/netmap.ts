import type { LanScanResult, LanScanStatus, NetworkInterfaceInfo, PortMapRow } from '@/types/api'
import { macKey, portURL } from './running'

// The network view's model: what nmap found on a network, told apart into the
// gateway, this panel's own nodes, their Proxmox guests and everything else,
// and each node's own interfaces and listening ports. Pure, so the rules are
// pinned by netmap.test.ts rather than by whatever the page happened to draw.

/** A Proxmox guest as the map needs it. */
export interface MapGuest {
  vmid: number
  title: string
  kind: 'vm' | 'lxc'
  running: boolean
  macs: readonly string[]
}

/** A node of the panel as the map needs it. */
export interface MapNode {
  id: string
  name: string
  online: boolean
  /** Its interfaces' addresses; empty until they were read. */
  macs: readonly string[]
  /** Its guests, for a node that is a Proxmox host. */
  guests: readonly MapGuest[]
}

export interface MapPort {
  port: number
  proto: string
  /** What usually answers there ("SSH"); null for a port with no usual name. */
  name: string | null
  /** Where a browser tab may get an answer; null for a port that is no link. */
  href: string | null
}

export interface MapDevice {
  /** Its MAC, or its address where nmap saw no MAC. */
  key: string
  ip: string
  mac: string
  vendor: string
  hostname: string
  /** The default route of the node that scanned leaves through it. */
  gateway: boolean
  /** The node the scan was made from. */
  scanner: boolean
  /** The node of this panel it is. */
  node: { id: string; name: string } | null
  /** The Proxmox guest it is, and on which node. */
  guest: { hostId: string; hostName: string; vmid: number; kind: 'vm' | 'lxc'; title: string } | null
  ports: MapPort[]
  /** The port check gave up on it: its ports are not known. */
  timedOut: boolean
  /** The guests that run on it, by address. */
  children: MapDevice[]
}

/** One network as it was last scanned. */
export interface Segment {
  cidr: string
  prefix: number
  scannedBy: { id: string; name: string }
  finishedAt: number
  /** Whether that scan looked for open ports. */
  portsChecked: boolean
  gateway: MapDevice | null
  /** The gateway's address, also where the scan did not find it. */
  gatewayAddress: string
  /** This panel's nodes, with their guests under them. */
  servers: MapDevice[]
  /** Everything else, by address. */
  devices: MapDevice[]
  /** Hosts found, whichever way they are drawn. */
  count: number
}

/** A node that is on a network: whether and why it can scan it. */
export interface Scanner {
  nodeId: string
  nodeName: string
  /** The network as that node names it: what a scan is asked for with. */
  network: string
  installed: boolean
  scannable: boolean
  /** Why not scannable ("too_large"). */
  reason: string
  online: boolean
}

export interface ScanRun {
  nodeId: string
  nodeName: string
  network: string
  ports: boolean
  startedAt: number
}

/** A network some node is on: scanned (a segment) or not yet. */
export interface NetworkEntry {
  key: string
  cidr: string
  segment: Segment | null
  /** The nodes on it, the one that scanned last first. */
  scanners: Scanner[]
  /** A scan of it that is running now. */
  running: ScanRun | null
}

// Ports a browser tab usually gets an answer on, and those of them that speak
// TLS. A link is a guess here — nmap says a port is open, not what speaks on
// it — so the list is kept to the ports web interfaces are commonly put on.
const WEB_PORTS: ReadonlySet<number> = new Set([
  80, 81, 82, 83, 84, 85, 88, 443, 3000, 3001, 5000, 5001, 8000, 8001, 8006, 8080, 8081, 8082, 8088, 8090, 8096, 8123, 8181, 8443, 8444,
  8800, 8888, 9000, 9001, 9090, 9091, 9443, 32400,
])
const TLS_PORTS: ReadonlySet<number> = new Set([443, 5001, 8006, 8443, 8444, 9443])
const PORT_NAMES: Readonly<Record<number, string>> = {
  21: 'FTP', 22: 'SSH', 23: 'Telnet', 25: 'SMTP', 53: 'DNS', 111: 'RPC', 139: 'SMB', 445: 'SMB', 515: 'LPD', 548: 'AFP', 554: 'RTSP',
  631: 'IPP', 1883: 'MQTT', 2049: 'NFS', 3306: 'MySQL', 3389: 'RDP', 5432: 'PostgreSQL', 5900: 'VNC', 6379: 'Redis', 9100: 'JetDirect',
}

/** An open port of a host at `ip`, as a chip. */
export function mapPort(ip: string, port: number, proto: string): MapPort {
  const tcp = proto === 'tcp'
  return {
    port,
    proto,
    name: PORT_NAMES[port] ?? null,
    href: tcp && WEB_PORTS.has(port) ? portURL(ip, port, TLS_PORTS.has(port)) : null,
  }
}

/** An IPv4 address as a number, for putting addresses in order; NaN for anything else. */
export function addressValue(ip: string): number {
  const parts = ip.split('.')
  if (parts.length !== 4) return NaN
  let value = 0
  for (const part of parts) {
    if (!/^\d{1,3}$/.test(part) || Number(part) > 255) return NaN
    value = value * 256 + Number(part)
  }
  return value
}

const byAddress = (a: { ip: string }, b: { ip: string }) => {
  const x = addressValue(a.ip)
  const y = addressValue(b.ip)
  if (Number.isNaN(x) || Number.isNaN(y)) return Number.isNaN(x) === Number.isNaN(y) ? a.ip.localeCompare(b.ip) : Number.isNaN(x) ? 1 : -1
  return x - y
}

/** The prefix length of "192.0.2.0/24"; 32 where there is none to read. */
export function prefixOf(cidr: string): number {
  const n = Number(cidr.split('/')[1])
  return Number.isInteger(n) && n >= 0 && n <= 32 ? n : 32
}

/**
 * An address in two parts: what every host of the network shares, and what
 * tells this one apart — the last number on a /24 or narrower, the last two
 * on anything wider. The second part is what the eye looks for.
 */
export function addressParts(ip: string, prefix: number): { network: string; host: string } {
  const parts = ip.split('.')
  if (parts.length !== 4 || prefix < 16) return { network: '', host: ip }
  const keep = prefix >= 24 ? 3 : 2
  return { network: `${parts.slice(0, keep).join('.')}.`, host: parts.slice(keep).join('.') }
}

/**
 * What to call a device, and where the name is from: the node's own name, the
 * guest's as Proxmox has it, the name the network's DNS gave, or failing all
 * of those who made its network card. Null for a device nothing names.
 */
export function deviceName(d: MapDevice): { text: string; source: 'node' | 'guest' | 'hostname' | 'vendor' } | null {
  if (d.node) return { text: d.node.name, source: 'node' }
  if (d.guest) return { text: d.guest.title, source: 'guest' }
  if (d.hostname) return { text: d.hostname, source: 'hostname' }
  if (d.vendor) return { text: d.vendor, source: 'vendor' }
  return null
}

interface Owners {
  /** The node an interface address belongs to, where exactly one node has it. */
  nodeOf: Map<string, MapNode>
  /** The guest a NIC address belongs to, where that can mean one guest. */
  guestOf: Map<string, { host: MapNode; guest: MapGuest }>
}

function owners(nodes: readonly MapNode[]): Owners {
  const claimed = new Map<string, Set<MapNode>>()
  const guests = new Map<string, { host: MapNode; guest: MapGuest }[]>()
  for (const node of nodes) {
    for (const mac of node.macs) {
      const key = macKey(mac)
      if (!key) continue
      const set = claimed.get(key)
      if (set) set.add(node)
      else claimed.set(key, new Set([node]))
    }
    for (const guest of node.guests) {
      for (const mac of guest.macs) {
        const key = macKey(mac)
        if (!key) continue
        const list = guests.get(key)
        if (list) list.push({ host: node, guest })
        else guests.set(key, [{ host: node, guest }])
      }
    }
  }
  const nodeOf = new Map<string, MapNode>()
  // An address two nodes both have (a cloned MAC) names neither.
  for (const [key, set] of claimed) if (set.size === 1) nodeOf.set(key, [...set][0])
  const guestOf = new Map<string, { host: MapNode; guest: MapGuest }>()
  // A backup restored with its addresses kept is the stopped copy of the
  // running original: of several guests with one address, the one that runs.
  for (const [key, list] of guests) {
    const running = list.filter((g) => g.guest.running)
    const one = list.length === 1 ? list[0] : running.length === 1 ? running[0] : null
    if (one) guestOf.set(key, one)
  }
  return { nodeOf, guestOf }
}

function segmentOf(scannedBy: MapNode, result: LanScanResult, own: Owners): Segment {
  const prefix = prefixOf(result.network)
  const all = result.hosts.map((h): MapDevice => {
    const key = macKey(h.mac)
    const node = h.self ? scannedBy : key ? own.nodeOf.get(key) ?? null : null
    const claim = key ? own.guestOf.get(key) : undefined
    // A node is never a guest of itself.
    const guest = claim && claim.host.id !== node?.id ? claim : undefined
    return {
      key: key ?? `ip:${h.ip}`,
      ip: h.ip,
      mac: key ?? '',
      vendor: h.vendor ?? '',
      hostname: h.hostname ?? '',
      gateway: !!result.gateway && h.ip === result.gateway,
      scanner: !!h.self,
      node: node ? { id: node.id, name: node.name } : null,
      guest: guest ? { hostId: guest.host.id, hostName: guest.host.name, vmid: guest.guest.vmid, kind: guest.guest.kind, title: guest.guest.title } : null,
      ports: [...(h.ports ?? [])].sort((a, b) => a.port - b.port || a.proto.localeCompare(b.proto)).map((p) => mapPort(h.ip, p.port, p.proto)),
      timedOut: !!h.timed_out,
      children: [],
    }
  }).sort(byAddress)

  const byNode = new Map<string, MapDevice>()
  for (const d of all) if (d.node && !byNode.has(d.node.id)) byNode.set(d.node.id, d)

  // A guest hangs under its host where the host is on this network too. The
  // gateway stays where the network's traffic goes through it, whatever it
  // runs on; and no chain of hosts may close into a loop.
  const parent = new Map<MapDevice, MapDevice>()
  for (const d of all) {
    if (d.gateway || !d.guest) continue
    const host = byNode.get(d.guest.hostId)
    if (!host || host === d || host.gateway) continue
    let up: MapDevice | undefined = host
    while (up && up !== d) up = parent.get(up)
    if (up === d) continue
    parent.set(d, host)
  }
  for (const [child, host] of parent) host.children.push(child)
  for (const d of all) d.children.sort(byAddress)

  const top = all.filter((d) => !d.gateway && !parent.has(d))
  return {
    cidr: result.network,
    prefix,
    scannedBy: { id: scannedBy.id, name: scannedBy.name },
    finishedAt: Date.parse(result.finished_at) || 0,
    portsChecked: !!result.ports,
    gateway: all.find((d) => d.gateway) ?? null,
    gatewayAddress: result.gateway ?? '',
    servers: top.filter((d) => d.node),
    devices: top.filter((d) => !d.node),
    count: all.length,
  }
}

interface Scan {
  node: MapNode
  result: LanScanResult
  macs: Set<string>
}

/**
 * Every network the nodes are on, scanned ones first.
 *
 * Several nodes may have scanned one network. Two results are of the same
 * network when they name the same CIDR and saw at least one MAC in common —
 * two sites that both use 192.168.1.0/24 share none — and of those the latest
 * is the one drawn, whole: the map is one scan, not a memory of several.
 *
 * A node is on a scanned network when the scan saw its interface, or when it
 * made the scan; failing both (it was down then), when it names the same CIDR
 * and gateway. A network no scan covers is listed with the nodes on it.
 */
export function networkEntries(nodes: readonly MapNode[], statuses: ReadonlyMap<string, LanScanStatus>): NetworkEntry[] {
  const byId = new Map(nodes.map((n) => [n.id, n]))
  const own = owners(nodes)

  const scans: Scan[] = []
  for (const [nodeId, status] of statuses) {
    const node = byId.get(nodeId)
    if (!node) continue
    for (const result of status.results ?? []) {
      const macs = new Set<string>()
      for (const h of result.hosts ?? []) {
        const key = macKey(h.mac)
        if (key) macs.add(key)
      }
      scans.push({ node, result, macs })
    }
  }
  // Latest first, so the first scan of a group is the one drawn.
  scans.sort((a, b) => (Date.parse(b.result.finished_at) || 0) - (Date.parse(a.result.finished_at) || 0))
  const groups: Scan[][] = []
  for (const scan of scans) {
    const group = groups.find((g) => g.some((other) => other.result.network === scan.result.network && [...scan.macs].some((m) => other.macs.has(m))))
    if (group) group.push(scan)
    else groups.push([scan])
  }

  const entries: NetworkEntry[] = groups.map((group) => {
    const { node, result } = group[0]
    return { key: `${node.id}|${result.network}`, cidr: result.network, segment: segmentOf(node, result, own), scanners: [], running: null }
  })

  // Each node's networks, put with the scan that covers them or listed alone.
  const loose = new Map<string, NetworkEntry>()
  for (const [nodeId, status] of statuses) {
    const node = byId.get(nodeId)
    if (!node) continue
    for (const net of status.networks ?? []) {
      const scanner: Scanner = {
        nodeId,
        nodeName: node.name,
        network: net.cidr,
        installed: !!status.installed,
        scannable: !!net.scannable,
        reason: net.reason ?? '',
        online: node.online,
      }
      const mac = macKey(net.mac)
      const index =
        firstIndex(groups, (g) => g[0].result.network === net.cidr && (g.some((s) => s.node.id === nodeId) || (!!mac && g.some((s) => s.macs.has(mac))))) ??
        firstIndex(groups, (g) => g[0].result.network === net.cidr && !!net.gateway && g[0].result.gateway === net.gateway)
      let entry = index === null ? undefined : entries[index]
      if (!entry) {
        const key = `${net.cidr}|${net.gateway ?? ''}`
        entry = loose.get(key)
        if (!entry) loose.set(key, (entry = { key: `loose|${key}`, cidr: net.cidr, segment: null, scanners: [], running: null }))
      }
      entry.scanners.push(scanner)
      const run = status.running
      if (run && run.network === net.cidr && !entry.running) {
        entry.running = { nodeId, nodeName: node.name, network: run.network, ports: !!run.ports, startedAt: Date.parse(run.started_at) || 0 }
      }
    }
  }
  for (const entry of entries) {
    const last = entry.segment?.scannedBy.id
    entry.scanners.sort((a, b) => Number(b.nodeId === last) - Number(a.nodeId === last))
  }
  return [...entries, ...[...loose.values()].sort((a, b) => byAddress({ ip: a.cidr.split('/')[0] }, { ip: b.cidr.split('/')[0] }))]
}

function firstIndex<T>(list: readonly T[], test: (item: T) => boolean): number | null {
  const i = list.findIndex(test)
  return i < 0 ? null : i
}

/** Whether a node can be asked to scan now. */
export const canScan = (s: Scanner) => s.online && s.installed && s.scannable

/**
 * The node a scan of the network is asked of unless the operator picks
 * another: the one that scanned it last if it still can, else the first that
 * can. Null when none can.
 */
export function defaultScanner(entry: NetworkEntry): Scanner | null {
  return entry.scanners.find(canScan) ?? null
}

function matchesDevice(d: MapDevice, q: string): boolean {
  if (d.ip.includes(q) || d.mac.includes(q)) return true
  const words = [d.hostname, d.vendor, d.node?.name, d.guest?.title, d.guest?.hostName, d.guest ? String(d.guest.vmid) : '']
  if (words.some((w) => !!w && w.toLowerCase().includes(q))) return true
  return d.ports.some((p) => String(p.port) === q || (!!p.name && p.name.toLowerCase() === q))
}

function filterDevices(list: MapDevice[], q: string): MapDevice[] {
  const kept: MapDevice[] = []
  for (const d of list) {
    // A host that matches keeps all its guests; one that does not is kept for
    // the guests that do.
    if (matchesDevice(d, q)) kept.push(d)
    else {
      const children = filterDevices(d.children, q)
      if (children.length > 0) kept.push({ ...d, children })
    }
  }
  return kept
}

/**
 * The segment with only what a search finds, by address, name, maker, MAC or
 * port. The gateway stays: it is what the rest hangs from.
 */
export function filterSegment(segment: Segment, query: string): Segment {
  const q = query.trim().toLowerCase()
  if (!q) return segment
  return { ...segment, servers: filterDevices(segment.servers, q), devices: filterDevices(segment.devices, q) }
}

/** How many devices a segment shows below its gateway, guests included. */
export function shownCount(segment: Segment): number {
  const count = (list: MapDevice[]): number => list.reduce((sum, d) => sum + 1 + count(d.children), 0)
  return count(segment.servers) + count(segment.devices)
}

/** The cards of a row of `columns`: the list cut into rows. */
export function rowsOf<T>(list: readonly T[], columns: number): T[][] {
  const size = Math.max(1, Math.floor(columns))
  const rows: T[][] = []
  for (let i = 0; i < list.length; i += size) rows.push(list.slice(i, i + size))
  return rows
}

/** How many cards of at least `min` wide fit a row of `width` with `gap` between them. */
export function columnsFor(width: number, min: number, gap: number): number {
  if (!(width > 0)) return 1
  return Math.max(1, Math.floor((width + gap) / (min + gap)))
}

/** Who can reach a listening port. */
export type Exposure =
  /** Every address of the node: whoever reaches the node reaches it. */
  | 'all'
  /** Particular addresses of the node, not loopback alone. */
  | 'address'
  /** Loopback only: the node itself. */
  | 'local'
  /** The node did not say (older than the field). */
  | 'unknown'

const isLoopback = (a: string) => a === '::1' || a.startsWith('127.')
const isAny = (a: string) => a === '*' || a === '0.0.0.0' || a === '::'

export function exposure(addresses: readonly string[] | undefined): { kind: Exposure; addresses: string[] } {
  const list = (addresses ?? []).filter(Boolean)
  if (list.length === 0) return { kind: 'unknown', addresses: [] }
  if (list.some(isAny)) return { kind: 'all', addresses: [] }
  const outside = list.filter((a) => !isLoopback(a))
  return outside.length > 0 ? { kind: 'address', addresses: outside } : { kind: 'local', addresses: [] }
}

export interface ServerPort {
  port: number
  proto: string
  exposure: Exposure
  /** The addresses, for a port open on particular ones. */
  addresses: string[]
  /** The containers it is published for; empty for a port of the host itself. */
  containers: { name: string; stack: string }[]
  /** The listening process, when the node could tell. */
  process: string | null
  firewall: { action: string; scope: string } | null
}

/**
 * A node's listening ports in two lists: those something other than the node
 * can reach, and those open to the node alone. A port whose reach the node did
 * not say counts as reachable — the safer way to be wrong.
 */
export function serverPorts(rows: readonly PortMapRow[]): { open: ServerPort[]; local: ServerPort[] } {
  const ports = [...rows]
    .sort((a, b) => a.port - b.port || a.proto.localeCompare(b.proto))
    .map((r): ServerPort => {
      const reach = exposure(r.addresses)
      return {
        port: r.port,
        proto: r.proto,
        exposure: reach.kind,
        addresses: reach.addresses,
        // Docker binds a published port once for each family (0.0.0.0 and ::),
        // and the port map names the container for each: once is enough.
        containers: [...new Map((r.containers ?? []).map((c) => [c.id || c.name, { name: c.name, stack: c.stack ?? '' }])).values()],
        process: r.process?.name || null,
        firewall: r.firewall ? { action: r.firewall.action, scope: r.firewall.scope } : null,
      }
    })
  return { open: ports.filter((p) => p.exposure !== 'local'), local: ports.filter((p) => p.exposure === 'local') }
}

export interface ServerInterface {
  name: string
  /** "192.0.2.10/24", IPv4 first. */
  addresses: string[]
  mac: string
  up: boolean
  /** The default route leaves by it. */
  isDefault: boolean
}

// The interfaces Docker and its like make for themselves: one bridge a compose
// project and one veth a container, none of them a network the node is on.
const INNER = /^(lo$|docker|br-|veth|virbr|lxcbr|lxdbr|cni|flannel|cali|tap|fwbr|fwpr|fwln)/

/**
 * A node's interfaces that carry an address, the default route's first, and
 * how many inner ones (Docker bridges and the like) were left out.
 */
export function serverInterfaces(list: readonly NetworkInterfaceInfo[]): { shown: ServerInterface[]; inner: number } {
  let inner = 0
  const shown: ServerInterface[] = []
  for (const i of list) {
    if (INNER.test(i.name)) {
      if (i.name !== 'lo' && !i.name.startsWith('veth') && !/^(tap|fwbr|fwpr|fwln)/.test(i.name)) inner++
      continue
    }
    const addresses = [...(i.addresses ?? [])]
      // A link-local IPv6 address says nothing an operator looks for.
      .filter((a) => !(a.family === 'ipv6' && a.address.toLowerCase().startsWith('fe80:')))
      .sort((a, b) => Number(a.family === 'ipv6') - Number(b.family === 'ipv6'))
      .map((a) => `${a.address}/${a.prefix}`)
    if (addresses.length === 0) continue
    shown.push({ name: i.name, addresses, mac: macKey(i.mac_address) ?? '', up: i.state === 'up', isDefault: !!i.is_default })
  }
  shown.sort((a, b) => Number(b.isDefault) - Number(a.isDefault) || a.name.localeCompare(b.name, undefined, { numeric: true }))
  return { shown, inner }
}
