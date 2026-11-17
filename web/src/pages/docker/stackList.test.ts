import { describe, it, expect } from 'vitest'
import type { ClusterNodeStacks, ComposeProjectWithStatus } from '@/types/api'
import { sortNodeGroups, stackCounts, matchesStackFilter } from './stackList'

const stack = (name: string, real_status: string): ComposeProjectWithStatus => ({
  name, real_status, service_count: 2, running_count: real_status === 'running' ? 2 : real_status === 'partial' ? 1 : 0,
  compose_file: 'docker-compose.yml', path: `/opt/stacks/${name}`, has_env: false,
} as ComposeProjectWithStatus)

const group = (node_id: string, node_name: string, local: boolean): ClusterNodeStacks => ({
  node_id, node_name, local, status: 'online', stacks: [],
})

describe('sortNodeGroups', () => {
  // The server builds the list from a map; its order changed between loads.
  it('puts this node first, then the others by name, whatever order they came in', () => {
    const a = [group('3', 'node-10', false), group('1', 'node-2', false), group('2', 'manager', true)]
    const b = [group('1', 'node-2', false), group('2', 'manager', true), group('3', 'node-10', false)]
    expect(sortNodeGroups(a).map((g) => g.node_name)).toEqual(['manager', 'node-2', 'node-10'])
    expect(sortNodeGroups(b).map((g) => g.node_name)).toEqual(['manager', 'node-2', 'node-10'])
  })

  it('does not reorder the list it was given', () => {
    const a = [group('3', 'b', false), group('1', 'a', false)]
    sortNodeGroups(a)
    expect(a.map((g) => g.node_id)).toEqual(['3', '1'])
  })
})

describe('stackCounts', () => {
  it('counts each state', () => {
    expect(stackCounts([stack('a', 'running'), stack('b', 'partial'), stack('c', 'stopped'), stack('d', 'running')]))
      .toEqual({ all: 4, running: 2, partial: 1, stopped: 1 })
  })

  // In cluster mode the summary counted the single-node list, which is empty
  // there: 0 / 0 / 0 beside a list of twenty stacks.
  it('counts every node’s stacks when given the cluster list', () => {
    const nodes = [
      { ...group('1', 'a', true), stacks: [stack('web', 'running'), stack('db', 'stopped')] },
      { ...group('2', 'b', false), stacks: [stack('web', 'partial')] },
    ]
    expect(stackCounts(nodes.flatMap((n) => n.stacks))).toEqual({ all: 3, running: 1, partial: 1, stopped: 1 })
  })
})

describe('matchesStackFilter', () => {
  it('filters by the stack’s own state, not "needs attention"', () => {
    const list = [stack('a', 'running'), stack('b', 'partial'), stack('c', 'stopped')]
    expect(list.filter((p) => matchesStackFilter(p, '', 'all')).map((p) => p.name)).toEqual(['a', 'b', 'c'])
    expect(list.filter((p) => matchesStackFilter(p, '', 'running')).map((p) => p.name)).toEqual(['a'])
    expect(list.filter((p) => matchesStackFilter(p, '', 'partial')).map((p) => p.name)).toEqual(['b'])
    expect(list.filter((p) => matchesStackFilter(p, '', 'stopped')).map((p) => p.name)).toEqual(['c'])
  })

  it('matches the search case-insensitively on the name', () => {
    expect(matchesStackFilter(stack('Nextcloud', 'running'), 'cloud', 'all')).toBe(true)
    expect(matchesStackFilter(stack('Nextcloud', 'running'), 'cloud', 'stopped')).toBe(false)
    expect(matchesStackFilter(stack('gitea', 'running'), 'cloud', 'all')).toBe(false)
  })
})
