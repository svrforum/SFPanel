import type { ClusterNodeStacks, ComposeProjectWithStatus } from '@/types/api'

// What the stack list shows and in what order, for one node and for the
// cluster's every node alike.

/** A stack's own state; the server's real_status is one of the last three. */
export type StackFilter = 'all' | 'running' | 'partial' | 'stopped'

const byName = (a: string, b: string) => a.localeCompare(b, undefined, { numeric: true })

/**
 * This node first, then the others by name. The server builds the list from
 * a map, so in the order it arrives the groups changed places between loads.
 */
export function sortNodeGroups(groups: ClusterNodeStacks[]): ClusterNodeStacks[] {
  return [...groups].sort((a, b) =>
    Number(b.local) - Number(a.local) || byName(a.node_name, b.node_name) || byName(a.node_id, b.node_id))
}

export function stackCounts(projects: ComposeProjectWithStatus[]): Record<StackFilter, number> {
  const counts: Record<StackFilter, number> = { all: projects.length, running: 0, partial: 0, stopped: 0 }
  for (const p of projects) {
    if (p.real_status === 'running' || p.real_status === 'partial' || p.real_status === 'stopped') counts[p.real_status]++
  }
  return counts
}

/**
 * The filter names a state. It used to be "needs attention", meaning anything
 * not running, so a stack stopped on purpose was listed as a problem.
 */
export function matchesStackFilter(p: ComposeProjectWithStatus, search: string, filter: StackFilter): boolean {
  return p.name.toLowerCase().includes(search.toLowerCase()) && (filter === 'all' || p.real_status === filter)
}
