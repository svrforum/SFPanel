import { describe, expect, it } from 'vitest'
import type { TFunction } from 'i18next'
import type { RunNodeRecord, RunRecord, RunStepRecord, TaskJobView } from '@/types/api'
import type { RunState } from './taskRunProgress'
import {
  OUTPUT_KEEP,
  appendOutput,
  emptyOutput,
  focusStep,
  runActions,
  runStepCount,
  stepHeader,
  unsuccessfulNodes,
} from './taskRunViewModel'

const run = (over: Partial<RunRecord> = {}): RunRecord => ({
  id: 'r-1', task_id: 't-1', task_name: 'Deploy', rev: 'rev', nodes: ['a', 'b', 'c'], waves: [['a', 'b', 'c']],
  batch: 3, stop_on_failure: true, status: 'running', started_by: 'admin', started_at: '2026-01-01T00:00:00Z', ...over,
})
const st = (status: RunStepRecord['status'], over: Partial<RunStepRecord> = {}): RunStepRecord => ({ status, ...over })
const rec = (status: RunNodeRecord['status'], steps?: RunStepRecord[]): RunNodeRecord => (steps ? { status, steps } : { status })
const state = (nodes: Record<string, RunNodeRecord>, head: RunRecord | null = run()): RunState => ({ run: head, nodes, ended: false })

const view = (output: string, nextSkip: number, done = false): TaskJobView => ({
  id: 'j', run_id: 'r-1', kind: 'command', status: done ? 'succeeded' : 'running', changed: false,
  output, next_skip: nextSkip, done, started_at: '2026-01-01T00:00:00Z',
})

// A t that shows which key was asked for and with what.
const t = ((key: string, opts?: Record<string, unknown>) => (opts ? `${key}:${JSON.stringify(opts)}` : key)) as unknown as TFunction

describe('runStepCount', () => {
  it('takes the run document steps when the server read them', () => {
    expect(runStepCount([{ name: 'x', kind: 'command' }, { kind: 'reboot' }], state({ a: rec('running', [st('running')]) }))).toBe(2)
  })

  it('counts from the longest node record when the steps could not be read', () => {
    const s = state({ a: rec('running', [st('succeeded'), st('running')]), b: rec('pending'), c: rec('failed', [st('succeeded'), st('failed'), st('pending')]) })
    expect(runStepCount(undefined, s)).toBe(3)
  })

  it('is zero before anything is known', () => {
    expect(runStepCount(undefined, state({}, null))).toBe(0)
  })
})

describe('appendOutput', () => {
  it('appends each piece and keeps the next_skip given, not the text length', () => {
    const first = appendOutput(emptyOutput, view('pulling\n', 4096))
    expect(first).toEqual({ text: 'pulling\n', skip: 4096, done: false, trimmed: false })
    const second = appendOutput(first, view('up\n', 8192, true))
    expect(second).toEqual({ text: 'pulling\nup\n', skip: 8192, done: true, trimmed: false })
  })

  it('takes an answer with no output as nothing new', () => {
    const v = view('', 10)
    delete v.output
    expect(appendOutput({ text: 'a\n', skip: 10, done: false, trimmed: false }, v)).toEqual({ text: 'a\n', skip: 10, done: false, trimmed: false })
  })

  it('output cap: keeps only the last OUTPUT_KEEP characters, from a line start, and says so', () => {
    expect(OUTPUT_KEEP).toBe(1 << 20)
    const line = 'x'.repeat(99) + '\n' // 100 characters
    const big = line.repeat(12_000) // 1.2 M characters
    const out = appendOutput(emptyOutput, view(big, big.length))
    expect(out.trimmed).toBe(true)
    expect(out.text.length).toBeLessThanOrEqual(OUTPUT_KEEP)
    expect(out.text.length).toBeGreaterThan(OUTPUT_KEEP - line.length)
    expect(out.text.startsWith('x'.repeat(99) + '\n')).toBe(true)
    expect(out.text.endsWith(line)).toBe(true)
    // Trimmed once is trimmed for good, even when the next piece is small.
    const next = appendOutput(out, view('tail\n', big.length + 5))
    expect(next.trimmed).toBe(true)
    expect(next.text.endsWith('tail\n')).toBe(true)
    expect(next.text.length).toBeLessThanOrEqual(OUTPUT_KEEP)
  })

  it('cuts mid-line when the kept part has no line break', () => {
    const out = appendOutput(emptyOutput, view('y'.repeat(OUTPUT_KEEP + 10), 1))
    expect(out.text.length).toBe(OUTPUT_KEEP)
    expect(out.trimmed).toBe(true)
  })
})

describe('focusStep', () => {
  it('opens the step under way', () => {
    expect(focusStep(rec('running', [st('succeeded'), st('starting'), st('pending')]))).toBe(1)
    expect(focusStep(rec('running', [st('succeeded'), st('succeeded'), st('running')]))).toBe(2)
    // Ahead of an earlier step whose outcome was lost: the live output first.
    expect(focusStep(rec('running', [st('unknown'), st('running'), st('pending')]))).toBe(1)
  })

  it('opens the first step that failed or was lost when none is under way', () => {
    expect(focusStep(rec('failed', [st('succeeded'), st('failed'), st('cancelled')]))).toBe(1)
    expect(focusStep(rec('unknown', [st('succeeded'), st('unknown')]))).toBe(1)
  })

  it('opens the last step that ran when every step is done', () => {
    expect(focusStep(rec('succeeded', [st('succeeded'), st('skipped'), st('pending')]))).toBe(1)
  })

  it('opens the first step of a node that has not started', () => {
    expect(focusStep(rec('pending'))).toBe(0)
    expect(focusStep(undefined)).toBe(0)
  })
})

describe('unsuccessfulNodes', () => {
  it('names, in run order, every node a rerun of the failed would take', () => {
    const s = state({ c: rec('failed', [st('failed')]), a: rec('succeeded', [st('succeeded')]), b: rec('stopped', [st('cancelled')]) }, run({ nodes: ['a', 'b', 'c', 'd'] }))
    expect(unsuccessfulNodes(s)).toEqual(['b', 'c', 'd'])
  })

  it('is empty before the run is known', () => {
    expect(unsuccessfulNodes(state({}, null))).toEqual([])
  })
})

describe('runActions', () => {
  it('offers both stops while the run is under way', () => {
    expect(runActions(run({ status: 'running' }))).toEqual({ stop: true, forceStop: true, rerun: false })
    expect(runActions(run({ status: 'handing_over' }))).toEqual({ stop: true, forceStop: true, rerun: false })
  })

  it('offers only the force stop to a run stopping without force', () => {
    expect(runActions(run({ status: 'stopping' }))).toEqual({ stop: false, forceStop: true, rerun: false })
    expect(runActions(run({ status: 'stopping', force: true }))).toEqual({ stop: false, forceStop: false, rerun: false })
  })

  it('offers the reruns once the run has ended', () => {
    for (const status of ['succeeded', 'failed', 'stopped'] as const) {
      expect(runActions(run({ status }))).toEqual({ stop: false, forceStop: false, rerun: true })
    }
  })
})

describe('stepHeader', () => {
  it('words a built-in step in the screen language', () => {
    expect(stepHeader(t, 'builtin/command', [{ name: '명령 실행', kind: 'command' }], 0)).toBe('cluster.tasks.builtins.command.steps.0')
  })

  it('keeps a stored task step name as written', () => {
    expect(stepHeader(t, 't-1', [{ name: 'Restart nginx', kind: 'service' }], 0)).toBe('Restart nginx')
  })

  it('numbers a step with no name, or a run whose steps could not be read', () => {
    expect(stepHeader(t, 't-1', [{ kind: 'service' }], 0)).toBe('cluster.tasks.plan.step:{"n":1}')
    expect(stepHeader(t, 't-1', undefined, 2)).toBe('cluster.tasks.plan.step:{"n":3}')
  })
})
