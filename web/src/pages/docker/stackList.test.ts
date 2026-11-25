import { describe, it, expect } from 'vitest'
import type { ComposeProjectWithStatus } from '@/types/api'
import { stackCounts, matchesStackFilter } from './stackList'

const stack = (name: string, real_status: string): ComposeProjectWithStatus => ({
  name, real_status, service_count: 2, running_count: real_status === 'running' ? 2 : real_status === 'partial' ? 1 : 0,
  compose_file: 'docker-compose.yml', path: `/opt/stacks/${name}`, has_env: false,
} as ComposeProjectWithStatus)

describe('stackCounts', () => {
  it('counts each state', () => {
    expect(stackCounts([stack('a', 'running'), stack('b', 'partial'), stack('c', 'stopped'), stack('d', 'running')]))
      .toEqual({ all: 4, running: 2, partial: 1, stopped: 1 })
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
