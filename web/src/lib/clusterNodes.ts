import type { ClusterNode } from '@/types/api'
import { relativeSince } from '@/lib/aiSessions'

// The one place the cluster screens decide what a node is: its kind, role,
// group and whether its version is behind. Keeping it here means the table,
// the tree and the overview cannot disagree about which node is an agent.

/** The label key that puts a node in a group. One group per node. */
export const GROUP_LABEL = 'group'

/**
 * An agent is a node whose record says so. An older leader sends no kind at
 * all, and every node it knows is a manager (Go's Node.IsManager reads it the
 * same way). An agent's role is empty, but so is a role an older record left
 * blank, so the role is never the test.
 */
export function isAgent(n: Pick<ClusterNode, 'kind'>): boolean {
  return n.kind === 'agent'
}

export function isManager(n: Pick<ClusterNode, 'kind'>): boolean {
  return !isAgent(n)
}

export function nodeGroup(n: Pick<ClusterNode, 'labels'>): string {
  return n.labels?.[GROUP_LABEL]?.trim() ?? ''
}

const byName = (a: string, b: string) => a.localeCompare(b, undefined, { numeric: true })

/** The named groups in use, by name. Nodes without one are left out. */
export function nodeGroups(nodes: ClusterNode[]): string[] {
  const groups = new Set<string>()
  for (const n of nodes) {
    const g = nodeGroup(n)
    if (g) groups.add(g)
  }
  return [...groups].sort(byName)
}

interface ParsedVersion {
  parts: number[]
  tail: string
}

function parseVersion(v: string): ParsedVersion | null {
  const s = v.trim().replace(/^v/, '')
  const dash = s.indexOf('-')
  const core = dash < 0 ? s : s.slice(0, dash)
  const tail = dash < 0 ? '' : s.slice(dash + 1)
  const fields = core.split('.')
  if (fields.some((f) => !/^\d+$/.test(f))) return null
  return { parts: fields.map(Number), tail }
}

/**
 * Orders two panel versions by number: a leading "v" is ignored, missing
 * fields count as 0, and a pre-release tail ("-rc1") sorts below the release
 * with the same numbers. A version that is not numbers (a "dev" build) cannot
 * be placed, so it compares equal to everything rather than looking outdated.
 */
export function compareVersions(a: string, b: string): number {
  const pa = parseVersion(a)
  const pb = parseVersion(b)
  if (!pa || !pb) return 0
  const len = Math.max(pa.parts.length, pb.parts.length)
  for (let i = 0; i < len; i++) {
    const d = (pa.parts[i] ?? 0) - (pb.parts[i] ?? 0)
    if (d !== 0) return d
  }
  if (pa.tail === pb.tail) return 0
  if (!pa.tail) return 1
  if (!pb.tail) return -1
  return byName(pa.tail, pb.tail)
}

/**
 * The version a node is measured against: the leader's cached latest release
 * when it sent one, otherwise the newest version running in the cluster (an
 * older leader sends no latest_version).
 */
export function referenceVersion(latest: string | undefined, versions: (string | undefined)[]): string | undefined {
  if (latest) return latest
  let best: string | undefined
  for (const v of versions) {
    if (!v || !parseVersion(v)) continue
    if (best === undefined || compareVersions(v, best) > 0) best = v
  }
  return best
}

export function isOutdated(version: string | undefined, reference: string | undefined): boolean {
  if (!version || !reference) return false
  return compareVersions(version, reference) < 0
}

export type NodeRoleKind = 'leader' | 'manager' | 'nonvoter' | 'agent'

export function nodeRoleKind(n: ClusterNode, leaderId: string): NodeRoleKind {
  if (isAgent(n)) return 'agent'
  if (n.id === leaderId) return 'leader'
  if (n.role === 'nonvoter') return 'nonvoter'
  return 'manager'
}

/** Managers before agents, the leader first among the managers, then by name. */
export function sortNodes(nodes: ClusterNode[], leaderId: string): ClusterNode[] {
  const rank = (n: ClusterNode) => (isAgent(n) ? 2 : n.id === leaderId ? 0 : 1)
  return [...nodes].sort((a, b) => rank(a) - rank(b) || byName(a.name, b.name))
}

export interface NodeFilter {
  kind: 'all' | 'manager' | 'agent'
  status: 'all' | 'online' | 'offline'
  /** null = every group, '' = only nodes without a group. */
  group: string | null
  query: string
}

export const ALL_NODES: NodeFilter = { kind: 'all', status: 'all', group: null, query: '' }

export function filterNodes(nodes: ClusterNode[], f: NodeFilter): ClusterNode[] {
  const q = f.query.trim().toLowerCase()
  return nodes.filter((n) => {
    if (f.kind === 'manager' && !isManager(n)) return false
    if (f.kind === 'agent' && !isAgent(n)) return false
    // Suspect and joining are not answering yet either, so "offline" takes them.
    if (f.status === 'online' && n.status !== 'online') return false
    if (f.status === 'offline' && n.status === 'online') return false
    if (f.group !== null && nodeGroup(n) !== f.group) return false
    if (!q) return true
    const fields = [n.name, n.api_address, n.grpc_address, ...Object.entries(n.labels ?? {}).map(([k, v]) => `${k}=${v}`)]
    return fields.some((field) => field.toLowerCase().includes(q))
  })
}

/**
 * The agent that joined with the token the add-agent dialog handed out: a node
 * that was not in the list when the token was made, and is an agent. Given a
 * name, only a node of that name — another agent may join meanwhile with a
 * token somebody else made. A new manager never is: it did not use this token.
 */
export function detectJoined(before: ReadonlySet<string>, nodes: ClusterNode[], name?: string): ClusterNode | undefined {
  return nodes.find((n) => !before.has(n.id) && isAgent(n) && (!name || n.name === name))
}

/**
 * The filter a link asked for: the overview's "offline agents" opens the list
 * with ?kind=agent&status=offline. A value this page does not know falls back
 * to "all" rather than hiding every node. An empty group means "no group",
 * as it does in NodeFilter.
 */
export function filterFromQuery(params: URLSearchParams): NodeFilter {
  const kind = params.get('kind')
  const status = params.get('status')
  return {
    kind: kind === 'manager' || kind === 'agent' ? kind : 'all',
    status: status === 'online' || status === 'offline' ? status : 'all',
    group: params.has('group') ? (params.get('group') ?? '').trim() : null,
    query: params.get('q') ?? '',
  }
}

/**
 * "5 minutes ago" for a node's last heartbeat, or '' when it never sent one.
 * A node that has not answered carries Go's zero time, which parses as a real
 * date in year 1 and would read as "2025 years ago".
 */
export function lastSeenAgo(value: string, lang: string, now = Date.now()): string {
  const ms = new Date(value).getTime()
  if (Number.isNaN(ms) || ms <= 0) return ''
  return relativeSince(value, lang, now)
}
