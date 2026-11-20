import type { ClusterNode, ComposeProjectWithStatus, Container, ContainerPort, PortMapRow } from '@/types/api'
import { containerHealth, containerNeedsAttention, exitCode, type ContainerHealth } from './containerState'

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

/**
 * The published ports as chips: one per port and protocol, however many
 * addresses (0.0.0.0 and ::) Docker bound it to. Unpublished ports are not
 * reachable from outside the node and are left out.
 */
export function portChips(ports: ContainerPort[], host: string | null): PortChip[] {
  const byKey = new Map<string, { port: number; proto: string; loopbackOnly: boolean }>()
  for (const p of ports) {
    if (!p.PublicPort || p.PublicPort <= 0) continue
    const proto = (p.Type || 'tcp').toLowerCase()
    const key = `${p.PublicPort}/${proto}`
    const seen = byKey.get(key)
    const loopback = isLoopback(p.IP)
    if (seen) seen.loopbackOnly = seen.loopbackOnly && loopback
    else byKey.set(key, { port: p.PublicPort, proto, loopbackOnly: loopback })
  }
  return [...byKey.values()]
    .sort((a, b) => a.port - b.port || a.proto.localeCompare(b.proto))
    .map(({ port, proto, loopbackOnly }) => ({
      port,
      proto,
      internal: loopbackOnly,
      href: !loopbackOnly && proto === 'tcp' && !NON_HTTP_PORTS.has(port) && host ? portURL(host, port) : null,
    }))
}

function portURL(host: string, port: number): string {
  const scheme = HTTPS_PORTS.has(port) ? 'https' : 'http'
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
    state: stackState(running, members.length),
    containers: members,
    services: [...services].sort(([a], [b]) => byName(a, b)).map(([service, container]) => ({ name: service, container })),
    ports: portChips(containers.flatMap((c) => c.Ports ?? []), host),
  }
}

/**
 * What runs on one node. Containers are grouped by their compose project
 * label; every stack the panel manages gets a tile even with no containers,
 * and so does every project seen only in labels (one started by hand with
 * `docker compose` somewhere else). Compose lowercases a project's name, so
 * a managed directory is matched to its label without case.
 */
export function nodeContents(projects: ComposeProjectWithStatus[], containers: Container[], host: string | null): NodeContents {
  const byProject = new Map<string, Container[]>()
  const standalone: Container[] = []
  for (const c of containers) {
    const project = c.Labels?.[PROJECT_LABEL]
    if (!project) {
      standalone.push(c)
      continue
    }
    const list = byProject.get(project)
    if (list) list.push(c)
    else byProject.set(project, [c])
  }
  const stacks: RunningStack[] = []
  for (const p of projects) {
    const members: Container[] = []
    for (const [label, list] of byProject) {
      if (label.toLowerCase() !== p.name.toLowerCase()) continue
      members.push(...list)
      byProject.delete(label)
    }
    stacks.push(stackOf(p.name, true, members, host))
  }
  for (const [label, list] of byProject) stacks.push(stackOf(label, false, list, host))
  return {
    stacks: stacks.sort((a, b) => byName(a.name, b.name)),
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
 * order the nodes are given, then by name.
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
      const partial = stack.state === 'partial'
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
