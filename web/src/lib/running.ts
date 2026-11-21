import type { ClusterNode, ComposeProjectWithStatus, Container, ContainerPort, PortMapRow, PveGuest } from '@/types/api'
import { containerHealth, containerNeedsAttention, exitCode, type ContainerHealth } from './containerState'
import { formatBytes } from './utils'

// The running board's model: what runs on a node, grouped the way an operator
// thinks of it (stacks, then the containers that belong to none), and what of
// it needs looking at. Pure, so the rules are pinned by running.test.ts rather
// than by whatever a page happened to draw.

export const PROJECT_LABEL = 'com.docker.compose.project'

/**
 * Ports that answer something other than HTTP. A link to one opens a browser
 * tab on a protocol it cannot speak, so these are shown as a plain chip.
 */
export const NON_HTTP_PORTS: ReadonlySet<number> = new Set([
  21, 22, 23, 25, 53, 110, 143, 465, 587, 993, 995, 1433, 1521, 1883, 3306, 5432, 5672, 6379, 8883, 9092, 11211, 27017,
])

/** Ports whose service speaks TLS by convention. */
const HTTPS_PORTS: ReadonlySet<number> = new Set([443, 8443])

export type StackState = 'running' | 'partial' | 'stopped'

/** An age as Docker words it in a container's status ("2 hours ago"). */
export interface Age {
  value: number
  unit: 'second' | 'minute' | 'hour' | 'day' | 'week' | 'month' | 'year'
}

export interface PortChip {
  port: number
  proto: string
  /** Bound to loopback only: reachable from the node itself, not from here. */
  internal: boolean
  /** Where the chip opens the service; null for a chip that is not a link. */
  href: string | null
}

export interface RunningContainer {
  id: string
  /** The container's name, without Docker's leading slash. */
  name: string
  /** The service in its stack; the name itself for a standalone container. */
  service: string
  image: string
  state: string
  status: string
  health: ContainerHealth
  running: boolean
  exitCode: number | null
  exitedAgo: Age | null
  ports: PortChip[]
}

export interface ServiceEntry {
  name: string
  /** The worst of the service's containers, which is the one its word describes. */
  container: RunningContainer
}

export interface RunningStack {
  name: string
  /** A stack the panel manages (a directory under its stacks path). */
  managed: boolean
  running: number
  total: number
  /** Containers that neither run nor ended with code 0. */
  unsettled: number
  state: StackState
  containers: RunningContainer[]
  services: ServiceEntry[]
  ports: PortChip[]
}

export interface NodeContents {
  stacks: RunningStack[]
  standalone: RunningContainer[]
}

const byName = (a: string, b: string) => a.localeCompare(b, undefined, { numeric: true })

export function stackState(running: number, total: number): StackState {
  if (running === 0) return 'stopped'
  return running < total ? 'partial' : 'running'
}

/**
 * The service a compose container runs: "photos-server-1" in project "photos"
 * is "server" (v1 compose joined with "_"). A container_name that does not
 * start with the project is the operator's own name and is kept whole, suffix
 * and all, since nothing says the digits are a replica number.
 */
export function serviceName(containerName: string, project: string): string {
  const name = containerName.replace(/^\//, '')
  const lower = name.toLowerCase()
  const prefix = project.toLowerCase()
  if (!prefix || !(lower.startsWith(`${prefix}-`) || lower.startsWith(`${prefix}_`))) return name
  const rest = name.slice(prefix.length + 1).replace(/[-_]\d+$/, '')
  return rest || name
}

const AGE_UNITS = new Set<Age['unit']>(['second', 'minute', 'hour', 'day', 'week', 'month', 'year'])

/**
 * How long ago a container exited, from Docker's own words for it
 * ("Exited (1) About an hour ago"). Wording it does not use reads as unknown.
 */
export function exitedAgo(status: string): Age | null {
  const m = /^exited \(\d+\) (.+) ago$/.exec((status || '').trim().toLowerCase())
  if (!m) return null
  const phrase = m[1]
  if (phrase === 'less than a second') return { value: 0, unit: 'second' }
  if (phrase === 'about a minute') return { value: 1, unit: 'minute' }
  if (phrase === 'about an hour') return { value: 1, unit: 'hour' }
  const n = /^(\d+) ([a-z]+?)s?$/.exec(phrase)
  if (!n || !AGE_UNITS.has(n[2] as Age['unit'])) return null
  return { value: Number(n[1]), unit: n[2] as Age['unit'] }
}

/**
 * Within the last day. Docker counts in hours up to two days ("47 hours"), so
 * the hour count is read rather than the unit alone; it rounds to the hour,
 * and "24 hours" is still taken as the last day.
 */
export function withinDay(age: Age | null): boolean {
  if (!age) return false
  if (age.unit === 'second' || age.unit === 'minute') return true
  return age.unit === 'hour' && age.value <= 24
}

/** A host as it goes in a URL: an IPv6 literal in brackets. */
function urlHost(host: string): string {
  if (host.startsWith('[') || !host.includes(':')) return host
  return `[${host}]`
}

/** The host part of an api_address ("192.0.2.2:3628", "[2001:db8::2]:3628"). */
function addressHost(address: string): string | null {
  const bare = address.trim().replace(/^[a-z][a-z0-9+.-]*:\/\//i, '').replace(/\/.*$/, '')
  if (!bare) return null
  if (bare.startsWith('[')) {
    const end = bare.indexOf(']')
    return end > 1 ? bare.slice(0, end + 1) : null
  }
  const host = /:\d+$/.test(bare) ? bare.slice(0, bare.lastIndexOf(':')) : bare
  return host ? urlHost(host) : null
}

/**
 * The host a node's published ports are opened on. The node this browser is
 * attached to is reached by the name the browser already uses for it — the
 * cluster's advertised address may be one this browser cannot reach. Any
 * other node is reached at its advertised API address.
 */
export function linkHost(attached: boolean, apiAddress: string | undefined, browserHost: string): string | null {
  if (attached) return browserHost ? urlHost(browserHost) : null
  return apiAddress ? addressHost(apiAddress) : null
}

const isLoopback = (ip: string | undefined) => !!ip && (ip === '::1' || ip.startsWith('127.'))
/** Bound to every address of the node: Docker says 0.0.0.0, ::, or nothing. */
const isEveryAddress = (ip: string | undefined) => !ip || ip === '0.0.0.0' || ip === '::'

/**
 * The published ports as chips: one per port and protocol, however many
 * addresses (0.0.0.0 and ::) Docker bound it to. Unpublished ports are not
 * reachable from outside the node and are left out.
 *
 * A chip is a link when a browser tab could do something with it, and what
 * answers is what the container listens on, so both ports are looked at:
 * Postgres published as 5433 is no link, and 443 published as 4443 opens
 * over https. A port bound to every address opens on the node's host; one
 * bound to particular addresses opens on the first of those that is not
 * loopback, since that is the only place it is; one bound to loopback alone
 * is reachable from the node itself and is no link.
 */
export function portChips(ports: ContainerPort[], host: string | null): PortChip[] {
  const byKey = new Map<string, { port: number; proto: string; inside: number[]; everywhere: boolean; addresses: string[] }>()
  for (const p of ports) {
    if (!p.PublicPort || p.PublicPort <= 0) continue
    const proto = (p.Type || 'tcp').toLowerCase()
    const key = `${p.PublicPort}/${proto}`
    let seen = byKey.get(key)
    if (!seen) byKey.set(key, (seen = { port: p.PublicPort, proto, inside: [], everywhere: false, addresses: [] }))
    if (p.PrivatePort) seen.inside.push(p.PrivatePort)
    if (isEveryAddress(p.IP)) seen.everywhere = true
    else if (!isLoopback(p.IP)) seen.addresses.push(p.IP!)
  }
  return [...byKey.values()]
    .sort((a, b) => a.port - b.port || a.proto.localeCompare(b.proto))
    .map(({ port, proto, inside, everywhere, addresses }) => {
      const internal = !everywhere && addresses.length === 0
      const speaksHttp = proto === 'tcp' && ![port, ...inside].some((n) => NON_HTTP_PORTS.has(n))
      const at = everywhere ? host : addresses.length > 0 ? urlHost(addresses[0]) : null
      const tls = [port, ...inside].some((n) => HTTPS_PORTS.has(n))
      return { port, proto, internal, href: speaksHttp && at ? portURL(at, port, tls) : null }
    })
}

export function portURL(host: string, port: number, tls: boolean): string {
  const scheme = tls ? 'https' : 'http'
  const isDefault = (scheme === 'http' && port === 80) || (scheme === 'https' && port === 443)
  return `${scheme}://${host}${isDefault ? '' : `:${port}`}/`
}

function runningContainer(c: Container, project: string | null, host: string | null): RunningContainer {
  const name = (c.Names?.[0] ?? c.Id.slice(0, 12)).replace(/^\//, '')
  return {
    id: c.Id,
    name,
    service: project ? serviceName(name, project) : name,
    image: c.Image,
    state: c.State,
    status: c.Status,
    health: containerHealth(c.State, c.Status),
    running: c.State === 'running',
    exitCode: exitCode(c.Status),
    exitedAgo: exitedAgo(c.Status),
    ports: portChips(c.Ports ?? [], host),
  }
}

// Which of a service's containers its one word describes: the worst.
const SEVERITY: Record<ContainerHealth, number> = { restarting: 0, crashed: 1, unhealthy: 2, stopped: 3, running: 4 }
const severity = (c: RunningContainer) => (c.state === 'dead' ? -1 : SEVERITY[c.health])

/** Exited with code 0: a job that did what it was for, or a service someone stopped. */
const endedCleanly = (c: RunningContainer) => c.state.toLowerCase() === 'exited' && c.exitCode === 0

/**
 * Runs in part for a reason worth a look: some container of it neither runs
 * nor ended with code 0. A stack whose only idle containers ended cleanly
 * still counts and reads as running in part (the stacks page says the same),
 * but it is how a stack with a one-off job always looks, so it is no warning.
 */
export function stackInPart(stack: RunningStack): boolean {
  return stack.state === 'partial' && stack.unsettled > 0
}

function stackOf(name: string, managed: boolean, containers: Container[], host: string | null): RunningStack {
  const members = containers.map((c) => runningContainer(c, c.Labels?.[PROJECT_LABEL] ?? name, host)).sort((a, b) => byName(a.name, b.name))
  const services = new Map<string, RunningContainer>()
  for (const c of members) {
    const seen = services.get(c.service)
    if (!seen || severity(c) < severity(seen)) services.set(c.service, c)
  }
  const running = members.filter((c) => c.running).length
  return {
    name,
    managed,
    running,
    total: members.length,
    unsettled: members.filter((c) => !c.running && !endedCleanly(c)).length,
    state: stackState(running, members.length),
    containers: members,
    services: [...services].sort(([a], [b]) => byName(a, b)).map(([service, container]) => ({ name: service, container })),
    ports: portChips(containers.flatMap((c) => c.Ports ?? []), host),
  }
}

/** The directory compose ran in, which is the stack's own however compose named the project. */
export const PROJECT_DIR_LABEL = 'com.docker.compose.project.working_dir'

const inDirectory = (dir: string | undefined, root: string) =>
  !!dir && !!root && (dir === root || dir.startsWith(root.endsWith('/') ? root : `${root}/`))

/**
 * What runs on one node. Every stack the panel manages gets a tile, with the
 * containers compose started from its directory (or one below it): the
 * panel runs compose there without naming the project, so the project label
 * is the file's own `name:`, or COMPOSE_PROJECT_NAME, or the directory name
 * with its dots taken out, and matching on it showed a running stack as
 * stopped beside a nameless twin. This is how the stacks page counts them.
 * A managed stack no container names the directory of takes the containers
 * labelled with its name, without case (compose lowercases it). Every other
 * project seen in labels (one started by hand somewhere else) gets a tile of
 * its own.
 */
export function nodeContents(projects: ComposeProjectWithStatus[], containers: Container[], host: string | null): NodeContents {
  const members = new Map<ComposeProjectWithStatus, Container[]>(projects.map((p) => [p, []]))
  const byLabel = new Map<string, Container[]>()
  const standalone: Container[] = []
  for (const c of containers) {
    const label = c.Labels?.[PROJECT_LABEL]
    if (!label) {
      standalone.push(c)
      continue
    }
    const dir = c.Labels?.[PROJECT_DIR_LABEL]
    const owner = projects.find((p) => inDirectory(dir, p.path))
    if (owner) {
      members.get(owner)!.push(c)
      continue
    }
    const list = byLabel.get(label)
    if (list) list.push(c)
    else byLabel.set(label, [c])
  }
  const stacks: RunningStack[] = []
  for (const p of projects) {
    const mine = members.get(p)!
    // By name only for a stack nothing was found in the directory of: once
    // it has its own, a project of the same name is one run from elsewhere.
    if (mine.length === 0) {
      for (const [label, list] of byLabel) {
        if (label.toLowerCase() !== p.name.toLowerCase()) continue
        mine.push(...list)
        byLabel.delete(label)
      }
    }
    stacks.push(stackOf(p.name, true, mine, host))
  }
  for (const [label, list] of byLabel) stacks.push(stackOf(label, false, list, host))
  return {
    // A managed stack before a stranger of the same name.
    stacks: stacks.sort((a, b) => byName(a.name, b.name) || Number(b.managed) - Number(a.managed)),
    standalone: standalone.map((c) => runningContainer(c, null, host)).sort((a, b) => byName(a.name, b.name)),
  }
}

/** Tiles whose stack, service or container name holds the query. */
export function filterContents(contents: NodeContents, query: string): NodeContents {
  const q = query.trim().toLowerCase()
  if (!q) return contents
  const has = (s: string) => s.toLowerCase().includes(q)
  return {
    stacks: contents.stacks.filter((s) => has(s.name) || s.containers.some((c) => has(c.name) || has(c.service))),
    standalone: contents.standalone.filter((c) => has(c.name)),
  }
}

/** A node as the board knows it. */
export interface BoardNode {
  id: string
  name: string
  status: ClusterNode['status']
  lastSeen?: string
  /** What runs there; null when it was not asked, has no Docker, or has not answered yet. */
  contents: NodeContents | null
  /** It was asked and the answer failed, with nothing earlier to show instead. */
  failed?: boolean
}

export type Problem =
  | { kind: 'restarting' | 'unhealthy' | 'dead'; container: RunningContainer }
  | { kind: 'exited'; container: RunningContainer; code: number; age: Age }

/**
 * What is wrong with one container, if anything. The shared rule
 * (containerNeedsAttention) counts every non-zero exit however old; here only
 * one from the last day is news — a job that failed last week and was left
 * there is not what this screen is for.
 */
export function containerProblem(c: RunningContainer): Problem | null {
  if (!containerNeedsAttention(c.state, c.status)) return null
  if (c.state.toLowerCase() === 'dead') return { kind: 'dead', container: c }
  if (c.health === 'restarting' || c.health === 'unhealthy') return { kind: c.health, container: c }
  const age = c.exitedAgo
  if (c.health === 'crashed' && c.exitCode !== null && age && withinDay(age)) {
    return { kind: 'exited', container: c, code: c.exitCode, age }
  }
  return null
}

const problemRank = (p: Problem): 2 | 4 => (p.kind === 'exited' ? 4 : 2)

export interface AttentionItem {
  /** 1 a node, 2 a failing container, 3 a stack running in part, 4 a recent non-zero exit. */
  rank: 1 | 2 | 3 | 4
  nodeId: string
  nodeName: string
  subject:
    | { kind: 'node'; status: ClusterNode['status']; lastSeen?: string }
    | { kind: 'stack'; stack: RunningStack }
    | { kind: 'container'; container: RunningContainer }
  /** Set when the subject is a stack that runs in part. */
  partial?: { running: number; total: number }
  problems: Problem[]
  /** Where its link goes: the node list, the stack, or one container's logs. */
  opens:
    | { kind: 'nodes' }
    | { kind: 'stack'; stack: RunningStack }
    | { kind: 'logs'; container: RunningContainer }
}

/**
 * What needs looking at, in this order: a node that is suspect or offline; a
 * container restarting, unhealthy or dead; a stack that runs in part; a
 * container that exited non-zero within the last day. Within a rank, by the
 * order the nodes are given, then by name. A stack runs in part, for this
 * list, when stackInPart says so.
 *
 * A stack is one line however many of its containers are in trouble: it
 * takes the rank of its worst finding and carries them all. It opens on the
 * stack, unless one container is the whole story — then on that container's
 * logs. A stack with nothing running and nothing failed recently was stopped
 * on purpose as far as anyone can tell, and is not a line at all.
 */
export function attention(nodes: BoardNode[]): AttentionItem[] {
  const items: { item: AttentionItem; order: number; name: string }[] = []
  nodes.forEach((node, order) => {
    const base = { nodeId: node.id, nodeName: node.name }
    if (node.status === 'suspect' || node.status === 'offline') {
      items.push({
        order, name: node.name,
        item: { ...base, rank: 1, subject: { kind: 'node', status: node.status, lastSeen: node.lastSeen }, problems: [], opens: { kind: 'nodes' } },
      })
    }
    if (!node.contents) return
    for (const stack of node.contents.stacks) {
      const problems = stack.containers.map(containerProblem).filter((p): p is Problem => p !== null)
      // Restarting before exited: the order the ranks give them.
      problems.sort((a, b) => problemRank(a) - problemRank(b))
      const partial = stackInPart(stack)
      if (!partial && problems.length === 0) continue
      const rank = Math.min(partial ? 3 : 4, ...problems.map(problemRank)) as 2 | 3 | 4
      const opens: AttentionItem['opens'] = !partial && problems.length === 1
        ? { kind: 'logs', container: problems[0].container }
        : { kind: 'stack', stack }
      items.push({
        order, name: stack.name,
        item: { ...base, rank, subject: { kind: 'stack', stack }, ...(partial ? { partial: { running: stack.running, total: stack.total } } : {}), problems, opens },
      })
    }
    for (const container of node.contents.standalone) {
      const problem = containerProblem(container)
      if (!problem) continue
      items.push({
        order, name: container.name,
        item: { ...base, rank: problemRank(problem), subject: { kind: 'container', container }, problems: [problem], opens: { kind: 'logs', container } },
      })
    }
  })
  return items
    .sort((a, b) => a.item.rank - b.item.rank || a.order - b.order || byName(a.name, b.name))
    .map(({ item }) => item)
}

export interface Totals {
  nodes: number
  online: number
  stacks: number
  /** Running containers, in stacks and standalone. */
  running: number
  attention: number
  /** Nodes asked and not loaded: left out of the numbers above. */
  failed: number
}

export function totals(nodes: BoardNode[], items: AttentionItem[]): Totals {
  let stacks = 0
  let running = 0
  for (const n of nodes) {
    if (!n.contents) continue
    stacks += n.contents.stacks.length
    running += n.contents.stacks.reduce((sum, s) => sum + s.running, 0)
    running += n.contents.standalone.filter((c) => c.running).length
  }
  return {
    nodes: nodes.length,
    online: nodes.filter((n) => n.status === 'online').length,
    stacks,
    running,
    attention: items.length,
    failed: nodes.filter((n) => n.failed).length,
  }
}

export interface HostListener {
  port: number
  proto: string
  /** The listening process, when the node could tell. */
  process: string | null
}

/**
 * What listens on the host itself: the port map without the ports Docker
 * publishes, which the tiles already show.
 */
export function hostListeners(rows: PortMapRow[]): HostListener[] {
  return rows
    .filter((r) => (r.containers ?? []).length === 0)
    .map((r) => ({ port: r.port, proto: r.proto, process: r.process?.name ?? null }))
    .sort((a, b) => a.port - b.port || a.proto.localeCompare(b.proto))
}

/** The states of a Proxmox guest the board has words for. */
export type GuestState = 'running' | 'stopped' | 'paused' | 'suspended' | 'unknown'

const GUEST_STATES = new Set<GuestState>(['running', 'stopped', 'paused', 'suspended', 'unknown'])

/** Proxmox's word for a guest's state, or null for one the board has no word for (shown as sent). */
export function guestState(status: string): GuestState | null {
  const word = (status || '').trim().toLowerCase() as GuestState
  return GUEST_STATES.has(word) ? word : null
}

/** A virtual machine or LXC container of a Proxmox VE node, as the board shows it. */
export interface RunningGuest {
  vmid: number
  name: string
  kind: 'vm' | 'lxc'
  /** The name, or "VM 130" for a guest Proxmox has no name for yet. */
  title: string
  state: GuestState | null
  /** Proxmox's own word, for a state the board has none for. */
  status: string
  running: boolean
  cpus: number
  mem: number
  maxmem: number
  tags: string[]
  macs: string[]
}

/** A node's guests, by vmid. */
export function runningGuests(list: PveGuest[]): RunningGuest[] {
  return list
    .map((g) => {
      const kind = g.kind === 'lxc' ? 'lxc' : 'vm'
      const name = (g.name || '').trim()
      const state = guestState(g.status)
      return {
        vmid: g.vmid,
        name,
        kind,
        title: name || `${kind === 'lxc' ? 'LXC' : 'VM'} ${g.vmid}`,
        state,
        status: g.status || '',
        running: state === 'running',
        cpus: g.cpus || 0,
        mem: g.mem || 0,
        maxmem: g.maxmem || 0,
        tags: g.tags ?? [],
        macs: g.macs ?? [],
      } satisfies RunningGuest
    })
    .sort((a, b) => a.vmid - b.vmid)
}

/**
 * A guest's CPUs and memory as they are shown: a fraction of a CPU (a
 * container's cpulimit) to two places, the memory in use of what is allotted
 * while it runs and only what is allotted otherwise. Null where Proxmox gave
 * no figure.
 */
export function guestResources(g: RunningGuest): { cpus: string | null; memory: string | null } {
  const cpus = g.cpus > 0 ? String(Math.round(g.cpus * 100) / 100) : null
  let memory: string | null = null
  if (g.maxmem > 0) memory = g.running && g.mem > 0 ? `${formatBytes(g.mem)} / ${formatBytes(g.maxmem)}` : formatBytes(g.maxmem)
  return { cpus, memory }
}

/** Guests whose name or vmid holds the query. */
export function filterGuests(guests: RunningGuest[], query: string): RunningGuest[] {
  const q = query.trim().toLowerCase()
  if (!q) return guests
  return guests.filter((g) => g.title.toLowerCase().includes(q) || String(g.vmid).includes(q))
}

/** One node's guests, for telling which guest is which node. */
export interface GuestHost {
  nodeId: string
  guests: RunningGuest[]
}

/** A guest across the cluster: its host and its vmid. */
export const guestKey = (hostId: string, vmid: number) => `${hostId}/${vmid}`

/** An address as compared: lower case, colons; null for one that names no NIC (empty, all zero). */
export function macKey(mac: string): string | null {
  const key = (mac || '').trim().toLowerCase().replace(/-/g, ':')
  return /[1-9a-f]/.test(key) ? key : null
}

/**
 * Which guests are this cluster's own nodes: a guest is node X when one of its
 * NIC addresses is an address of one of X's interfaces. Keyed by guestKey, the
 * node's id as value. Only a match that can mean one thing is kept:
 * - an address two nodes both have (a cloned MAC, a shared virtual one) names
 *   neither;
 * - a guest never names the host it runs on;
 * - a guest whose addresses point at more than one node names none;
 * - a node several guests point at is named by the one of them that runs, and
 *   by none when not exactly one does (a backup restored with its addresses
 *   kept is the stopped copy of the running original);
 * - a match that would make a host the guest of its own guest is dropped,
 *   hosts and guests taken in the order given, so the hosts and guests drawn
 *   from it form no loop.
 * `interfaces` holds the addresses of the nodes whose interfaces were read; a
 * node it does not hold is no guest.
 */
export function guestServers(hosts: GuestHost[], interfaces: ReadonlyMap<string, readonly string[]>): Map<string, string> {
  const owners = new Map<string, Set<string>>()
  for (const [nodeId, macs] of interfaces) {
    for (const mac of macs) {
      const key = macKey(mac)
      if (!key) continue
      const set = owners.get(key)
      if (set) set.add(nodeId)
      else owners.set(key, new Set([nodeId]))
    }
  }

  const claims: { key: string; hostId: string; nodeId: string; running: boolean }[] = []
  for (const host of hosts) {
    for (const g of host.guests) {
      const named = new Set<string>()
      for (const mac of g.macs) {
        const key = macKey(mac)
        const nodes = key ? owners.get(key) : undefined
        if (nodes?.size === 1) named.add([...nodes][0])
      }
      named.delete(host.nodeId)
      if (named.size === 1) claims.push({ key: guestKey(host.nodeId, g.vmid), hostId: host.nodeId, nodeId: [...named][0], running: g.running })
    }
  }

  const byNode = new Map<string, typeof claims>()
  for (const c of claims) {
    const list = byNode.get(c.nodeId)
    if (list) list.push(c)
    else byNode.set(c.nodeId, [c])
  }
  const result = new Map<string, string>()
  // Each node's host, as the kept claims make it: walked to refuse a loop.
  const hostOf = new Map<string, string>()
  for (const c of claims) {
    const rivals = byNode.get(c.nodeId)!
    const running = rivals.filter((r) => r.running)
    const winner = rivals.length === 1 ? rivals[0] : running.length === 1 ? running[0] : null
    if (winner !== c) continue
    let up: string | undefined = c.hostId
    while (up !== undefined && up !== c.nodeId) up = hostOf.get(up)
    if (up === c.nodeId) continue
    hostOf.set(c.nodeId, c.hostId)
    result.set(c.key, c.nodeId)
  }
  return result
}
