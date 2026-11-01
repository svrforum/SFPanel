import { describe, expect, it } from 'vitest'
import type { RunNodeRecord, RunRecord, RunStepRecord } from '@/types/api'
import { applyRunEvent, cellState, emptyRunState, runMatrix, type RunState } from './taskRunProgress'

const head = (over: Partial<RunRecord> = {}): RunRecord => ({
  id: '20261101-101500-abcd',
  task_id: 'builtin/command',
  task_name: 'Run a command',
  rev: '1',
  nodes: ['node-a', 'node-b', 'node-c'],
  waves: [['node-a'], ['node-b', 'node-c']],
  batch: 1,
  stop_on_failure: true,
  status: 'running',
  started_by: 'admin',
  started_at: '2026-11-01T10:15:00Z',
  ...over,
})

const step = (status: RunStepRecord['status'], changed?: boolean): RunStepRecord =>
  changed === undefined ? { status } : { status, changed }

const node = (status: RunNodeRecord['status'], steps?: RunStepRecord[], reason?: string): RunNodeRecord => ({
  status,
  ...(steps ? { steps } : {}),
  ...(reason ? { reason } : {}),
})

const name = (id: string) => `name of ${id}`

describe('applyRunEvent', () => {
  it('takes the whole state from a snapshot, dropping what an earlier connection left', () => {
    const before: RunState = {
      run: head({ status: 'stopping' }),
      nodes: { 'node-a': node('failed'), 'node-gone': node('running') },
      ended: false,
    }
    const nodes = { 'node-a': node('running', [step('running')]), 'node-b': node('pending') }
    const after = applyRunEvent(before, { type: 'snapshot', run: head(), nodes })
    expect(after).toEqual({ run: head(), nodes, ended: false })
    expect(after.nodes).not.toHaveProperty('node-gone')
  })

  it('swaps in one node record, or the head, and leaves the rest as it was', () => {
    let s = applyRunEvent(emptyRunState, {
      type: 'snapshot',
      run: head(),
      nodes: { 'node-a': node('running', [step('running')]), 'node-b': node('pending') },
    })
    const b = s.nodes['node-b']
    s = applyRunEvent(s, { type: 'node', node_id: 'node-a', record: node('succeeded', [step('succeeded', true)]) })
    expect(s.nodes['node-a']).toEqual(node('succeeded', [step('succeeded', true)]))
    expect(s.nodes['node-b']).toBe(b)
    expect(s.run?.status).toBe('running')

    s = applyRunEvent(s, { type: 'run', run: head({ status: 'succeeded', ended_at: '2026-11-01T10:20:00Z' }) })
    expect(s.run?.status).toBe('succeeded')
    expect(s.nodes['node-a'].status).toBe('succeeded')
    expect(s.ended).toBe(false)
  })

  it('marks the state ended only at the end event', () => {
    const s = applyRunEvent({ run: head(), nodes: {}, ended: false }, { type: 'end' })
    expect(s).toEqual({ run: head(), nodes: {}, ended: true })
  })

  it('never changes the state it was given', () => {
    const before: RunState = { run: head(), nodes: { 'node-a': node('pending') }, ended: false }
    const copy = structuredClone(before)
    applyRunEvent(before, { type: 'node', node_id: 'node-a', record: node('running') })
    applyRunEvent(before, { type: 'run', run: head({ status: 'failed' }) })
    applyRunEvent(before, { type: 'end' })
    expect(before).toEqual(copy)
  })
})

describe('cellState', () => {
  it('names each of the eight cells', () => {
    expect(cellState(undefined)).toBe('pending')
    expect(cellState(step('pending'))).toBe('pending')
    expect(cellState(step('starting'))).toBe('running')
    expect(cellState(step('running'))).toBe('running')
    expect(cellState(step('succeeded', true))).toBe('changed')
    expect(cellState(step('failed'))).toBe('failed')
    expect(cellState(step('skipped'))).toBe('skipped')
    expect(cellState(step('unknown'))).toBe('unknown')
    expect(cellState(step('cancelled'))).toBe('cancelled')
  })

  it('reads a success that left changed out, or set it false, as unchanged', () => {
    expect(cellState(step('succeeded'))).toBe('unchanged')
    expect(cellState(step('succeeded', false))).toBe('unchanged')
  })
})

describe('runMatrix', () => {
  it('has no rows and no wave before the first snapshot', () => {
    const m = runMatrix(emptyRunState, 3, name)
    expect(m.rows).toEqual([])
    expect(m.wave).toBeNull()
    expect(Object.values(m.counts).every((n) => n === 0)).toBe(true)
  })

  it('lays the rows out in run order, with the name, status, reason and one cell per step', () => {
    const s: RunState = {
      run: head({ nodes: ['node-c', 'node-a', 'node-b'] }),
      nodes: {
        'node-a': node('succeeded', [step('succeeded', true), step('succeeded')]),
        'node-b': node('failed', [step('failed'), step('cancelled')], 'step 1 failed'),
        'node-c': node('running', [step('succeeded'), step('starting')]),
      },
      ended: false,
    }
    const m = runMatrix(s, 2, name)
    expect(m.rows).toEqual([
      { id: 'node-c', name: 'name of node-c', status: 'running', cells: ['unchanged', 'running'] },
      { id: 'node-a', name: 'name of node-a', status: 'succeeded', cells: ['changed', 'unchanged'] },
      { id: 'node-b', name: 'name of node-b', status: 'failed', reason: 'step 1 failed', cells: ['failed', 'cancelled'] },
    ])
    expect(m.counts).toEqual({
      pending: 0,
      running: 1,
      changed: 1,
      unchanged: 2,
      failed: 1,
      skipped: 0,
      unknown: 0,
      cancelled: 1,
    })
  })

  it('fills every cell of a waiting node that has no steps yet, or no record at all, with pending', () => {
    const s: RunState = { run: head(), nodes: { 'node-a': node('pending') }, ended: false }
    const m = runMatrix(s, 4, name)
    expect(m.rows.map((r) => [r.id, r.status, r.cells])).toEqual([
      ['node-a', 'pending', ['pending', 'pending', 'pending', 'pending']],
      ['node-b', 'pending', ['pending', 'pending', 'pending', 'pending']],
      ['node-c', 'pending', ['pending', 'pending', 'pending', 'pending']],
    ])
    expect(m.counts.pending).toBe(12)
  })

  it('places the run in the first wave that still has a node not finished', () => {
    const waves = [['node-a', 'node-b'], ['node-c'], ['node-d']]
    const run = head({ nodes: ['node-a', 'node-b', 'node-c', 'node-d'], waves })
    const at = (nodes: Record<string, RunNodeRecord>) => runMatrix({ run, nodes, ended: false }, 1, name).wave

    expect(at({})).toEqual({ index: 1, total: 3 })
    // One node of the first wave is done; the other still runs.
    expect(at({ 'node-a': node('succeeded'), 'node-b': node('running') })).toEqual({ index: 1, total: 3 })
    // Every way a node can finish moves the run on.
    expect(at({ 'node-a': node('failed'), 'node-b': node('skipped') })).toEqual({ index: 2, total: 3 })
    expect(at({ 'node-a': node('unknown'), 'node-b': node('stopped'), 'node-c': node('succeeded') })).toEqual({ index: 3, total: 3 })
    // A later wave that already started does not count before an earlier one finishes.
    expect(at({ 'node-a': node('succeeded'), 'node-b': node('running'), 'node-c': node('running') })).toEqual({ index: 1, total: 3 })
    // All done: the last wave.
    const done = { 'node-a': node('succeeded'), 'node-b': node('succeeded'), 'node-c': node('succeeded'), 'node-d': node('succeeded') }
    expect(at(done)).toEqual({ index: 3, total: 3 })
  })

  it('has no wave for a run whose waves are empty', () => {
    expect(runMatrix({ run: head({ nodes: [], waves: [] }), nodes: {}, ended: false }, 1, name).wave).toBeNull()
  })

  it('lays out 200 nodes by 50 steps', () => {
    const ids = Array.from({ length: 200 }, (_, i) => `node-${String(i).padStart(3, '0')}`)
    const nodes: Record<string, RunNodeRecord> = {}
    ids.forEach((id, i) => {
      // Half the nodes have finished every step; the rest have not started.
      if (i % 2 === 0) nodes[id] = node('succeeded', Array.from({ length: 50 }, (_, j) => step('succeeded', j % 2 === 0)))
    })
    const waves = Array.from({ length: 20 }, (_, w) => ids.slice(w * 10, w * 10 + 10))
    const m = runMatrix({ run: head({ nodes: ids, waves }), nodes, ended: false }, 50, name)
    expect(m.rows).toHaveLength(200)
    expect(m.rows.map((r) => r.id)).toEqual(ids)
    expect(m.rows.every((r) => r.cells.length === 50)).toBe(true)
    expect(m.counts).toMatchObject({ changed: 2500, unchanged: 2500, pending: 5000 })
    expect(Object.values(m.counts).reduce((a, b) => a + b, 0)).toBe(10000)
    expect(m.wave).toEqual({ index: 1, total: 20 })
  })
})
