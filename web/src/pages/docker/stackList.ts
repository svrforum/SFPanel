import type { ComposeProjectWithStatus } from '@/types/api'

// What the stack list counts and which stacks its filter keeps.

/** A stack's own state; the server's real_status is one of the last three. */
export type StackFilter = 'all' | 'running' | 'partial' | 'stopped'

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
