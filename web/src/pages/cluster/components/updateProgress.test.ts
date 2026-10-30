import { describe, expect, it } from 'vitest'
import type { ClusterUpdateEvent } from '@/types/api'
import { reduceUpdateEvents } from './updateProgress'

// The events update_run.go sends, in the order it sends them. The old
// progress card counted skipped and already-current nodes as done (3 / 3 with
// the leader still updating), dropped the wave event and never said why a run
// stopped.
const started = (total: number, waves: number, target: string): ClusterUpdateEvent =>
  ({ overall: 'started', mode: 'rolling', total_nodes: total, waves, target })
const step = (name: string, s: string, message = ''): ClusterUpdateEvent =>
  ({ node_id: `id-${name}`, node_name: name, step: s, message })
const skipped = (name: string) => step(name, 'skipped', 'Node is offline')
const alreadyOn = (name: string) => step(name, 'online', 'Already on v0.80.0')
const wave = (i: number, n: number, nodes: string[]): ClusterUpdateEvent => ({ wave: i, waves: n, nodes })
const updating = (name: string) => step(name, 'updating', 'Starting update...')
const online = (name: string) => step(name, 'online', 'Back on v0.80.0')

const firstWave = [
  started(3, 1, 'v0.80.0'),
  skipped('agent-03'),
  alreadyOn('agent-04'),
  wave(1, 1, ['agent-01', 'agent-02']),
  updating('agent-01'),
  updating('agent-02'),
]

describe('reduceUpdateEvents', () => {
  it('counts only the nodes it asked to update', () => {
    const s = reduceUpdateEvents(firstWave)
    expect([s.done, s.total, s.waveNodes]).toEqual([0, 3, ['agent-01', 'agent-02']])
    expect([s.target, s.wave, s.waves]).toEqual(['v0.80.0', 1, 1])
    // Skipped and already-current nodes are listed, in the order they came.
    expect(s.nodes.map((n) => [n.name, n.step])).toEqual([
      ['agent-03', 'skipped'],
      ['agent-04', 'online'],
      ['agent-01', 'updating'],
      ['agent-02', 'updating'],
    ])
    expect(reduceUpdateEvents([...firstWave, online('agent-01')]).done).toBe(1)
  })

  it('keeps why the run stopped', () => {
    const reason = 'Wave 1 of 1 failed (agent-02): Update failed: timed out'
    const s = reduceUpdateEvents([
      ...firstWave,
      online('agent-01'),
      step('agent-02', 'error', 'Update failed: timed out'),
      { overall: 'error', message: reason, updated: 1, failed: 1 },
    ])
    expect([s.overall, s.stopReason, s.done, s.failed]).toEqual(['error', reason, 1, 1])
  })

  it('shows a failed release lookup', () => {
    const s = reduceUpdateEvents([{ overall: 'error', message: 'Could not look up the latest release: rate limited' }])
    expect([s.overall, s.stopReason, s.total, s.nodes]).toEqual(['error', 'Could not look up the latest release: rate limited', 0, []])
  })

  it('notes a leader restarting', () => {
    const s = reduceUpdateEvents([
      ...firstWave,
      online('agent-01'),
      online('agent-02'),
      step('node-a', 'updating', 'Updating this node...'),
      { overall: 'complete', updated: 3, failed: 0, leader_restarting: true },
    ])
    expect([s.overall, s.leaderRestarting, s.done, s.failed]).toEqual(['complete', true, 3, 0])
    expect(reduceUpdateEvents(firstWave).leaderRestarting).toBe(false)
  })

  it('reads an old leader that reports complete per node', () => {
    const s = reduceUpdateEvents([
      { overall: 'started', mode: 'rolling', total_nodes: 2 },
      step('node-b', 'updating'),
      step('node-b', 'complete', 'Update triggered'),
      step('node-c', 'updating'),
      step('node-c', 'error', 'Update failed: refused'),
    ])
    expect([s.done, s.failed, s.total, s.target, s.wave]).toEqual([1, 1, 2, undefined, undefined])
  })
})
