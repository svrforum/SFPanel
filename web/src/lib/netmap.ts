import type { LanKnownNode, LanScanResult, LanScanStatus, NetworkInterfaceInfo, PortMapRow } from '@/types/api'
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
  /** Its interfaces' MACs; empty until they were read. */
  macs: readonly string[]
  /** Its interfaces' IPv4 addresses; empty until they were read. */
  addresses: readonly string[]
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
  /** Some node's default route leaves through it. */
  gateway: boolean
  /** The nodes whose default route that is, by name. */
  gatewayFor: string[]
  /**
   * It answered on an address that is a node's own, with a MAC that is not
   * that node's: two devices are using one address.
   */
  conflict: { id: string; name: string } | null
  /** The node the scan was made from. */
  scanner: boolean
  /** The node of this panel it is. */
  node: { id: string; name: string } | null
  /** The Proxmox guest it is, and on which node. */
  guest: { hostId: string; hostName: string; vmid: number; kind: 'vm' | 'lxc'; title: string } | null
  ports: MapPort[]
  /** The port check gave up on it: its ports are not known. */
  timedOut: boolean
  /** What the operator wrote of it; null where nothing was written. */
  label: DeviceLabel | null
  /** When a scan first and last saw it (ms); 0 where no registry says. */
  firstSeen: number
  lastSeen: number
  /** Known from before, and not in the scan that is drawn. */
  absent: boolean
  /** It appeared after the network was first scanned, and nobody has looked at it yet. */
  fresh: boolean
  /** The other MACs that held its address over the same period: a conflict that is suspected, not seen. */
  suspect: string[]
  /** The guests that run on it, by address. */
  children: MapDevice[]
}

/** One network as it was last scanned. */
export interface Segment {
  cidr: string
  prefix: number
  scannedBy: { id: string; name: string }
  finishedAt: number
  /** Whether open ports are known: looked for by that scan, or kept from an earlier one. */
  portsChecked: boolean
  /** When they were last looked for (ms); 0 for never. */
  portsAt: number
  /**
   * The devices some node's default route leaves through, by address: one on
   * most networks, two where some servers go out through a router of their own.
   */
  gateways: MapDevice[]
  /** The scanning node's gateway address, also where the scan did not find it. */
  gatewayAddress: string
  /** The devices that answered on a node's address without being that node. */
  conflicts: MapDevice[]
  /** Addresses two MACs held over the same period, as the nodes' registries tell. */
  suspects: { ip: string; macs: string[] }[]
  /** What the operator said is network equipment (a router, a switch, an access point), by address. */
  gear: MapDevice[]
  /** This panel's nodes, with their guests under them. */
  servers: MapDevice[]
  /** Everything else, by address. */
  devices: MapDevice[]
  /** Hosts the scan found, whichever way they are drawn. */
  count: number
  /** Devices drawn that the scan did not find. */
  absent: number
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
  /** The node keeps a device registry and schedules (0.91 and later). */
  registry: boolean
}

/** A node's own scanning of a network at intervals. */
export interface ScanSchedule {
  nodeId: string
  nodeName: string
  intervalMinutes: number
  portsDaily: boolean
  /** ms; 0 for none. */
  nextRun: number
  /** Why it does not run: "" · no_tool · gone · conflict. */
  pausedReason: string
}

export interface ScanRun {
  nodeId: string
  nodeName: string
  network: string
  ports: boolean
  startedAt: number
  /** Started by the node's schedule, not by the operator. */
  scheduled: boolean
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
  /** The nodes that scan it by themselves. */
  schedules: ScanSchedule[]
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
 * guest's as Proxmox has it, the name the operator gave it, the name the
 * network's DNS gave, or failing all of those who made its network card. Null
 * for a device nothing names.
 */
export function deviceName(d: MapDevice): { text: string; source: 'node' | 'guest' | 'label' | 'hostname' | 'vendor' } | null {
  if (d.node) return { text: d.node.name, source: 'node' }
  if (d.guest) return { text: d.guest.title, source: 'guest' }
  if (d.label?.name) return { text: d.label.name, source: 'label' }
  if (d.hostname) return { text: d.hostname, source: 'hostname' }
  if (d.vendor) return { text: d.vendor, source: 'vendor' }
  return null
}

/** What the operator says of a device: its name, a note, what it is, and whether to be told when it is gone. */
export interface DeviceLabel {
  name: string
  note: string
  kind: string
  watch: boolean
}

/**
 * The time a write carries. A node keeps the newer of what it has and what it
 * is sent, by their times — and the time of what it has may come from a
 * device whose clock runs ahead of this one. Stamped with this clock alone, a
 * change would then be the older of the two and dropped without a word: so a
 * write is always later than what it replaces (`was`, ms; 0 for nothing).
 */
export function writeTime(was: number, now: number): number {
  return Math.max(now, (was > 0 ? was : 0) + 1)
}

/** A device as the nodes' registries know it together. */
export interface Known {
  mac: string
  /** What the operator wrote, as the node that was written to last has it; null where nothing was written. */
  label: DeviceLabel | null
  labelledAt: number
  acked: boolean
  /** As the node that saw it last has it. */
  network: string
  ip: string
  vendor: string
  hostname: string
  firstSeen: number
  lastSeen: number
}

const time = (s: string | undefined) => (s ? Date.parse(s) || 0 : 0)

/**
 * The nodes' registries as one, by MAC. A label is written to every node on
 * the network and one that was down then has an older one: the label written
 * last is the label. A device was first seen when any node first saw it, last
 * seen when any last did, and is where the node that saw it last says.
 */
export function knownDevices(statuses: ReadonlyMap<string, LanScanStatus>): Map<string, Known> {
  const known = new Map<string, Known>()
  for (const status of statuses.values()) {
    for (const d of status.devices ?? []) {
      const mac = macKey(d.mac)
      if (!mac) continue
      const labelledAt = time(d.labelled_at)
      const firstSeen = time(d.first_seen)
      const lastSeen = time(d.last_seen)
      const label = labelledAt > 0 ? { name: d.name ?? '', note: d.note ?? '', kind: d.kind ?? '', watch: !!d.watch } : null
      const was = known.get(mac)
      if (!was) {
        known.set(mac, { mac, label, labelledAt, acked: !!d.acked, network: d.network ?? '', ip: d.ip ?? '', vendor: d.vendor ?? '', hostname: d.hostname ?? '', firstSeen, lastSeen })
        continue
      }
      if (labelledAt > was.labelledAt) {
        was.label = label
        was.labelledAt = labelledAt
      }
      was.acked = was.acked || !!d.acked
      if (firstSeen > 0 && (was.firstSeen === 0 || firstSeen < was.firstSeen)) was.firstSeen = firstSeen
      if (lastSeen > was.lastSeen) {
        was.lastSeen = lastSeen
        was.network = d.network ?? ''
        was.ip = d.ip ?? ''
        was.vendor = d.vendor || was.vendor
        was.hostname = d.hostname || was.hostname
      }
    }
  }
  return known
}

/** How long a device nobody labelled stays on the map after it was last seen. */
const ABSENT_SHOWN_MS = 7 * 24 * 3600 * 1000
/** How long a device counts as new. */
const FRESH_MS = 7 * 24 * 3600 * 1000
/** Devices first seen within this of a network's first scan came with that scan: they are not news. */
const FIRST_SCAN_MS = 60 * 1000
/** What the operator can say a device is; the first three are network equipment. */
export const DEVICE_KINDS = ['router', 'switch', 'ap', 'server', 'nas', 'pc', 'phone', 'tv', 'camera', 'printer', 'iot', 'other'] as const
const GEAR_KINDS: ReadonlySet<string> = new Set(['router', 'switch', 'ap'])

interface Owners {
  /** The node an interface address belongs to, where exactly one node has it. */
  nodeOf: Map<string, MapNode>
  /** The guest a NIC address belongs to, where that can mean one guest. */
  guestOf: Map<string, { host: MapNode; guest: MapGuest }>
  /** The node an IPv4 address is an interface address of, where exactly one node has it. */
  addressOf: Map<string, MapNode>
}

function owners(nodes: readonly MapNode[]): Owners {
  const claimed = new Map<string, Set<MapNode>>()
  const guests = new Map<string, { host: MapNode; guest: MapGuest }[]>()
  const addressed = new Map<string, Set<MapNode>>()
  for (const node of nodes) {
    for (const address of node.addresses) {
      const set = addressed.get(address)
      if (set) set.add(node)
      else addressed.set(address, new Set([node]))
    }
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
  const addressOf = new Map<string, MapNode>()
  for (const [address, set] of addressed) if (set.size === 1) addressOf.set(address, [...set][0])
  return { nodeOf, guestOf, addressOf }
}

/**
 * `routes`: the addresses default routes of this network's nodes leave
 * through, with the names of the nodes that use each.
 */
function segmentOf(
  scannedBy: MapNode,
  result: LanScanResult,
  own: Owners,
  routes: ReadonlyMap<string, string[]>,
  known: ReadonlyMap<string, Known>,
  suspects: readonly { ip: string; macs: string[] }[],
  now: number,
): Segment {
  const prefix = prefixOf(result.network)
  const finishedAt = Date.parse(result.finished_at) || 0
  // When this network was first scanned, as far as any registry tells: what
  // was found then is not news.
  let firstScan = 0
  for (const k of known.values()) if (k.network === result.network && k.firstSeen > 0 && (firstScan === 0 || k.firstSeen < firstScan)) firstScan = k.firstSeen
  const record = (key: string | null) => {
    const k = key ? known.get(key) : undefined
    return {
      label: k?.label ?? null,
      firstSeen: k?.firstSeen ?? 0,
      lastSeen: k?.lastSeen ?? 0,
      fresh: !!k && !k.acked && !k.label && k.firstSeen > firstScan + FIRST_SCAN_MS && now - k.firstSeen <= FRESH_MS,
    }
  }
  const seen = new Set<string>()
  const found = result.hosts.map((h): MapDevice => {
    const key = macKey(h.mac)
    if (key) seen.add(key)
    const node = h.self ? scannedBy : key ? own.nodeOf.get(key) ?? null : null
    const claim = key ? own.guestOf.get(key) : undefined
    // A node is never a guest of itself.
    const guest = claim && claim.host.id !== node?.id ? claim : undefined
    // The address is a node's own, the node's interfaces were read, and what
    // answered is none of them: another device has taken the address. (The
    // node itself, answering on its own address, is found by its MAC above.)
    const owner = h.self || !key ? undefined : own.addressOf.get(h.ip)
    const ownerMacs = owner ? owner.macs.map((m) => macKey(m)).filter(Boolean) : []
    const conflict = owner && ownerMacs.length > 0 && !ownerMacs.includes(key) ? owner : undefined
    return {
      key: key ?? `ip:${h.ip}`,
      ip: h.ip,
      mac: key ?? '',
      vendor: h.vendor ?? '',
      hostname: h.hostname ?? '',
      gateway: routes.has(h.ip),
      gatewayFor: routes.get(h.ip) ?? [],
      conflict: conflict ? { id: conflict.id, name: conflict.name } : null,
      scanner: !!h.self,
      node: node ? { id: node.id, name: node.name } : null,
      guest: guest ? { hostId: guest.host.id, hostName: guest.host.name, vmid: guest.guest.vmid, kind: guest.guest.kind, title: guest.guest.title } : null,
      ports: [...(h.ports ?? [])].sort((a, b) => a.port - b.port || a.proto.localeCompare(b.proto)).map((p) => mapPort(h.ip, p.port, p.proto)),
      timedOut: !!h.timed_out,
      ...record(key),
      absent: false,
      suspect: (suspects.find((x) => x.ip === h.ip)?.macs ?? []).filter((m) => m !== key),
      children: [],
    }
  })
  // What a registry knows of this network and the scan did not find: kept
  // while it has a label, or for a week after it was last seen.
  const gone: MapDevice[] = []
  for (const k of known.values()) {
    if (k.network !== result.network || seen.has(k.mac) || k.lastSeen === 0) continue
    if (!k.label && now - k.lastSeen > ABSENT_SHOWN_MS) continue
    const node = own.nodeOf.get(k.mac) ?? null
    const claim = own.guestOf.get(k.mac)
    const guest = claim && claim.host.id !== node?.id ? claim : undefined
    gone.push({
      key: k.mac,
      ip: k.ip,
      mac: k.mac,
      vendor: k.vendor,
      hostname: k.hostname,
      gateway: routes.has(k.ip) && !found.some((d) => d.ip === k.ip),
      gatewayFor: routes.get(k.ip) ?? [],
      conflict: null,
      scanner: false,
      node: node ? { id: node.id, name: node.name } : null,
      guest: guest ? { hostId: guest.host.id, hostName: guest.host.name, vmid: guest.guest.vmid, kind: guest.guest.kind, title: guest.guest.title } : null,
      ports: [],
      timedOut: false,
      ...record(k.mac),
      fresh: false,
      absent: true,
      suspect: [],
      children: [],
    })
  }
  const all = [...found, ...gone].sort(byAddress)

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
  // Network equipment is what the operator said is: a scan cannot tell a
  // switch from a smart plug of the same maker. A node stays a server.
  const isGear = (d: MapDevice) => !d.node && !!d.label && GEAR_KINDS.has(d.label.kind)
  return {
    cidr: result.network,
    prefix,
    scannedBy: { id: scannedBy.id, name: scannedBy.name },
    finishedAt,
    // A scan for devices only keeps the ports an earlier one found.
    portsChecked: !!result.ports || !!result.ports_at,
    portsAt: result.ports ? finishedAt : time(result.ports_at),
    gateways: all.filter((d) => d.gateway),
    gatewayAddress: result.gateway ?? '',
    conflicts: all.filter((d) => d.conflict),
    suspects: suspects.filter((x) => !all.some((d) => d.conflict && d.ip === x.ip)),
    gear: top.filter(isGear),
    servers: top.filter((d) => d.node),
    devices: top.filter((d) => !d.node && !isGear(d)),
    count: found.length,
    absent: gone.length,
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
 * and its gateway is the scan's, or a device the scan saw. A network no scan
 * covers is listed once, with every node on it — whatever each node's gateway
 * is: six servers of one LAN, one of them going out through a router of its
 * own, were listed as two networks.
 */
export function networkEntries(nodes: readonly MapNode[], statuses: ReadonlyMap<string, LanScanStatus>, now = Date.now()): NetworkEntry[] {
  const byId = new Map(nodes.map((n) => [n.id, n]))
  const own = owners(nodes)
  const known = knownDevices(statuses)
  // Addresses two MACs held at once, as any node's history has it.
  const suspects = new Map<string, { network: string; ip: string; macs: Set<string> }>()
  for (const status of statuses.values()) {
    for (const c of status.conflicts ?? []) {
      const key = `${c.network}|${c.ip}`
      const was = suspects.get(key) ?? { network: c.network, ip: c.ip, macs: new Set<string>() }
      for (const m of c.macs ?? []) {
        const mac = macKey(m)
        if (mac) was.macs.add(mac)
      }
      suspects.set(key, was)
    }
  }
  const suspectsOf = (cidr: string) => [...suspects.values()].filter((x) => x.network === cidr && x.macs.size > 1).map((x) => ({ ip: x.ip, macs: [...x.macs].sort() }))

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

  // Each node's networks: with the scan that covers them, or on their own.
  const placed: { index: number | null; nodeId: string; node: MapNode; status: LanScanStatus; net: LanScanStatus['networks'][number] }[] = []
  for (const [nodeId, status] of statuses) {
    const node = byId.get(nodeId)
    if (!node) continue
    for (const net of status.networks ?? []) {
      const mac = macKey(net.mac)
      const index =
        firstIndex(groups, (g) => g[0].result.network === net.cidr && (g.some((s) => s.node.id === nodeId) || (!!mac && g.some((s) => s.macs.has(mac))))) ??
        firstIndex(groups, (g) => g[0].result.network === net.cidr && !!net.gateway && (g[0].result.gateway === net.gateway || (g[0].result.hosts ?? []).some((h) => h.ip === net.gateway)))
      placed.push({ index, nodeId, node, status, net })
    }
  }

  // The gateways of a scanned network: the scanning node's as it was then,
  // and that of every node on the network now.
  const routes = groups.map((group) => new Map<string, string[]>([[group[0].result.gateway, [group[0].node.name]]].filter(([address]) => !!address) as [string, string[]][]))
  for (const p of placed) {
    if (p.index === null || !p.net.gateway) continue
    const users = routes[p.index].get(p.net.gateway)
    if (!users) routes[p.index].set(p.net.gateway, [p.node.name])
    else if (!users.includes(p.node.name)) users.push(p.node.name)
  }

  const entries: NetworkEntry[] = groups.map((group, i) => {
    const { node, result } = group[0]
    return { key: `${node.id}|${result.network}`, cidr: result.network, segment: segmentOf(node, result, own, routes[i], known, suspectsOf(result.network), now), scanners: [], running: null, schedules: [] }
  })

  const loose = new Map<string, NetworkEntry>()
  for (const { index, nodeId, node, status, net } of placed) {
    let entry = index === null ? undefined : entries[index]
    if (!entry) {
      entry = loose.get(net.cidr)
      if (!entry) loose.set(net.cidr, (entry = { key: `loose|${net.cidr}`, cidr: net.cidr, segment: null, scanners: [], running: null, schedules: [] }))
    }
    entry.scanners.push({
      nodeId,
      nodeName: node.name,
      network: net.cidr,
      installed: !!status.installed,
      scannable: !!net.scannable,
      reason: net.reason ?? '',
      online: node.online,
      registry: Array.isArray(status.devices),
    })
    const run = status.running
    if (run && run.network === net.cidr && !entry.running) {
      entry.running = { nodeId, nodeName: node.name, network: run.network, ports: !!run.ports, startedAt: Date.parse(run.started_at) || 0, scheduled: !!run.scheduled }
    }
    const schedule = (status.schedules ?? []).find((x) => x.network === net.cidr)
    if (schedule && schedule.interval_minutes > 0) {
      entry.schedules.push({
        nodeId,
        nodeName: node.name,
        intervalMinutes: schedule.interval_minutes,
        portsDaily: !!schedule.ports_daily,
        nextRun: time(schedule.next_run),
        pausedReason: schedule.paused_reason ?? '',
      })
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
  const words = [d.hostname, d.vendor, d.node?.name, d.guest?.title, d.guest?.hostName, d.guest ? String(d.guest.vmid) : '', d.label?.name, d.label?.note]
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
 * port. The gateways stay: they are what the rest hangs from.
 */
export function filterSegment(segment: Segment, query: string): Segment {
  const q = query.trim().toLowerCase()
  if (!q) return segment
  return { ...segment, gear: filterDevices(segment.gear, q), servers: filterDevices(segment.servers, q), devices: filterDevices(segment.devices, q) }
}

/** How many devices a segment shows below its gateway, guests included. */
export function shownCount(segment: Segment): number {
  const count = (list: MapDevice[]): number => list.reduce((sum, d) => sum + 1 + count(d.children), 0)
  return count(segment.gear) + count(segment.servers) + count(segment.devices)
}

/** Whether an IPv4 address is of the network "10.20.30.0/24". */
export function inNetwork(ip: string, cidr: string): boolean {
  const [base, bits] = cidr.split('/')
  const a = addressValue(ip)
  const b = addressValue(base ?? '')
  const prefix = Number(bits)
  if (Number.isNaN(a) || Number.isNaN(b) || !Number.isInteger(prefix) || prefix < 0 || prefix > 32) return false
  const size = 2 ** (32 - prefix)
  return Math.floor(a / size) === Math.floor(b / size)
}

/**
 * What a node that scans a network by itself is told of the cluster's nodes
 * there: each node's addresses on that network with its interfaces' MACs, so
 * the node can tell that another device answers on one of them and stop.
 */
export function knownNodes(nodes: readonly MapNode[], cidr: string): LanKnownNode[] {
  const list: LanKnownNode[] = []
  for (const node of nodes) {
    const macs = [...new Set(node.macs.map((m) => macKey(m)).filter((m): m is string => !!m))].sort()
    if (macs.length === 0) continue
    for (const address of node.addresses) if (inNetwork(address, cidr)) list.push({ address, macs: macs.slice(0, 16) })
  }
  return list.sort((a, b) => addressValue(a.address) - addressValue(b.address)).slice(0, 64)
}

/**
 * The nodes that can wake a device of this network: those that answer, are on
 * a network they may use, and have the device in their own registry — a node
 * wakes what it knows, it is no packet generator. The one that scanned last first.
 */
export function wakeNodes(entry: NetworkEntry, mac: string, statuses: ReadonlyMap<string, LanScanStatus>): Scanner[] {
  const key = macKey(mac)
  if (!key) return []
  return entry.scanners.filter((s) => s.online && s.registry && s.scannable && (statuses.get(s.nodeId)?.devices ?? []).some((d) => macKey(d.mac) === key))
}

/** The nodes a label of a device on this network is written to: those that answer and keep a registry. */
export function labelTargets(entry: NetworkEntry): string[] {
  return [...new Set(entry.scanners.filter((s) => s.online && s.registry).map((s) => s.nodeId))]
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

/**
 * The server whose alerts judge this network's scans: one that scans it by
 * itself, else the one that scanned it last, else the one a scan would be
 * asked of.
 */
export function rulesNode(entry: NetworkEntry): { id: string; name: string } | null {
  const own = entry.schedules[0]
  if (own) return { id: own.nodeId, name: own.nodeName }
  const last = entry.segment?.scannedBy
  if (last) return { id: last.id, name: last.name }
  const next = defaultScanner(entry)
  return next ? { id: next.nodeId, name: next.nodeName } : null
}

/**
 * Where those rules are edited: that server's Settings › Alerts in a cluster
 * (null when no server scans the network), this server's alone.
 */
export function alertRulesLink(entry: NetworkEntry, cluster: boolean): { to: string; serverName: string | null } | null {
  if (!cluster) return { to: '/settings?tab=alerts', serverName: null }
  const node = rulesNode(entry)
  return node ? { to: `/settings?tab=alerts&node=${encodeURIComponent(node.id)}`, serverName: node.name } : null
}
