import { describe, it, expect } from 'vitest'
import { containerHealth, exitCode, needsAttention, compareForSummary } from './containerState'

describe('containerHealth', () => {
  it('separates a crash from a deliberate stop', () => {
    // The distinction the dashboard could not make: both were "stopped".
    expect(containerHealth('exited', 'Exited (0) 3 weeks ago')).toBe('stopped')
    expect(containerHealth('exited', 'Exited (2) 6 days ago')).toBe('crashed')
    expect(containerHealth('exited', 'Exited (137) 1 hour ago')).toBe('crashed')
  })

  it('reads the healthcheck suffix on a running container', () => {
    expect(containerHealth('running', 'Up 2 hours')).toBe('running')
    expect(containerHealth('running', 'Up 2 hours (unhealthy)')).toBe('unhealthy')
    // Still booting is not yet a failure.
    expect(containerHealth('running', 'Up 5 seconds (health: starting)')).toBe('running')
  })

  it('calls a restart loop what it is', () => {
    expect(containerHealth('restarting', 'Restarting (1) 3 seconds ago')).toBe('restarting')
  })

  it('does not invent a crash when the status is missing', () => {
    // A missing Status must not read as a non-zero exit.
    expect(containerHealth('exited')).toBe('stopped')
    expect(containerHealth('exited', '')).toBe('stopped')
    expect(containerHealth('created', 'Created')).toBe('stopped')
  })
})

describe('exitCode', () => {
  it('reads the code and nothing else', () => {
    expect(exitCode('Exited (137) 1 hour ago')).toBe(137)
    expect(exitCode('Up 2 hours')).toBeNull()
    // "Restarting (1)" is a restart count, not an exit code.
    expect(exitCode('Restarting (1) 3 seconds ago')).toBeNull()
  })
})

describe('compareForSummary', () => {
  it('puts trouble first, then the busiest', () => {
    const rows = [
      { name: 'idle', health: 'running' as const, cpu: 0.1 },
      { name: 'busy', health: 'running' as const, cpu: 40 },
      { name: 'crashed', health: 'crashed' as const, cpu: null },
      { name: 'nosample', health: 'running' as const, cpu: null },
    ]
    const order = [...rows].sort(compareForSummary).map((r) => r.name)
    expect(order).toEqual(['crashed', 'busy', 'idle', 'nosample'])
  })

  it('treats a missing sample as unknown, not as zero', () => {
    expect(needsAttention('running')).toBe(false)
    expect(needsAttention('unhealthy')).toBe(true)
    expect(compareForSummary({ health: 'running', cpu: null }, { health: 'running', cpu: 0 })).toBeGreaterThan(0)
  })
})
