import { describe, expect, it } from 'vitest'
import type { TFunction } from 'i18next'
import type { RunRecord } from '@/types/api'
import { HISTORY_PAGE_SIZE, durationText, filterRuns, historyPage, historyTasks, runDuration } from './taskHistory'

const run = (over: Partial<RunRecord> = {}): RunRecord => ({
  id: 'r-1', task_id: 't-1', task_name: 'Deploy', rev: 'rev', nodes: ['a'], waves: [['a']],
  batch: 1, stop_on_failure: true, status: 'succeeded', started_by: 'admin',
  started_at: '2026-01-01T00:00:00Z', ended_at: '2026-01-01T00:01:35Z', ...over,
})

// A t that shows which key was asked for and with what.
const t = ((key: string, opts?: Record<string, unknown>) => (opts ? `${key}:${JSON.stringify(opts)}` : key)) as unknown as TFunction

describe('filterRuns', () => {
  const runs = [
    run({ id: 'r-6', status: 'running', task_id: 't-1' }),
    run({ id: 'r-5', status: 'stopping', task_id: 't-2' }),
    run({ id: 'r-4', status: 'handing_over', task_id: 't-1' }),
    run({ id: 'r-3', status: 'failed', task_id: 't-2' }),
    run({ id: 'r-2', status: 'succeeded', task_id: 't-1' }),
    run({ id: 'r-1', status: 'stopped', task_id: 't-1' }),
  ]
  const ids = (list: RunRecord[]) => list.map((r) => r.id)

  it('keeps every run, in the order given, with no filter', () => {
    expect(ids(filterRuns(runs, { status: '', task: '' }))).toEqual(['r-6', 'r-5', 'r-4', 'r-3', 'r-2', 'r-1'])
  })

  it('counts a run stopping or handing over as under way', () => {
    expect(ids(filterRuns(runs, { status: 'active', task: '' }))).toEqual(['r-6', 'r-5', 'r-4'])
  })

  it('matches an ended status exactly', () => {
    expect(ids(filterRuns(runs, { status: 'failed', task: '' }))).toEqual(['r-3'])
    expect(ids(filterRuns(runs, { status: 'stopped', task: '' }))).toEqual(['r-1'])
  })

  it('filters by task, and by both', () => {
    expect(ids(filterRuns(runs, { status: '', task: 't-2' }))).toEqual(['r-5', 'r-3'])
    expect(ids(filterRuns(runs, { status: 'active', task: 't-1' }))).toEqual(['r-6', 'r-4'])
  })
})

describe('historyTasks', () => {
  it('gives each task once, with the name of its newest run', () => {
    const runs = [
      run({ id: 'r-3', task_id: 't-2', task_name: 'Backup (renamed)' }),
      run({ id: 'r-2', task_id: 't-1', task_name: 'Deploy' }),
      run({ id: 'r-1', task_id: 't-2', task_name: 'Backup' }),
    ]
    expect(historyTasks(runs)).toEqual([
      { id: 't-2', name: 'Backup (renamed)' },
      { id: 't-1', name: 'Deploy' },
    ])
  })
})

describe('historyPage', () => {
  const list = Array.from({ length: 120 }, (_, i) => i)

  it('cuts 50 to a page', () => {
    expect(HISTORY_PAGE_SIZE).toBe(50)
    const first = historyPage(list, 1)
    expect(first.items).toHaveLength(50)
    expect(first.items[0]).toBe(0)
    expect(first.pages).toBe(3)
    const last = historyPage(list, 3)
    expect(last.items).toEqual(list.slice(100))
    expect(last.page).toBe(3)
  })

  it('brings a page past the end back to the last one, and an empty list to page 1', () => {
    expect(historyPage(list.slice(0, 60), 3)).toMatchObject({ page: 2, pages: 2 })
    expect(historyPage(list.slice(0, 60), 3).items).toEqual(list.slice(50, 60))
    expect(historyPage([], 4)).toEqual({ items: [], page: 1, pages: 1 })
    expect(historyPage(list, 0).page).toBe(1)
  })
})

describe('runDuration', () => {
  it('is ended_at less started_at', () => {
    expect(runDuration(run())).toBe(95_000)
  })

  it('is null for a run under way, and for times that do not read', () => {
    expect(runDuration(run({ ended_at: undefined }))).toBeNull()
    expect(runDuration(run({ started_at: 'not a time' }))).toBeNull()
    expect(runDuration(run({ ended_at: '' }))).toBeNull()
  })

  it('never goes below zero when the clocks disagree', () => {
    expect(runDuration(run({ ended_at: '2025-12-31T23:59:59Z' }))).toBe(0)
  })
})

describe('durationText', () => {
  it('reads seconds under a minute', () => {
    expect(durationText(42_400, t)).toBe('cluster.tasks.history.durationSeconds:{"s":42}')
    expect(durationText(0, t)).toBe('cluster.tasks.history.durationSeconds:{"s":0}')
  })

  it('reads minutes and seconds under an hour', () => {
    expect(durationText(95_000, t)).toBe('cluster.tasks.history.durationMinutes:{"m":1,"s":35}')
    expect(durationText(3_599_000, t)).toBe('cluster.tasks.history.durationMinutes:{"m":59,"s":59}')
  })

  it('reads hours and minutes from an hour', () => {
    expect(durationText(3_725_000, t)).toBe('cluster.tasks.history.durationHours:{"h":1,"m":2}')
    expect(durationText(26 * 3_600_000, t)).toBe('cluster.tasks.history.durationHours:{"h":26,"m":0}')
  })
})
