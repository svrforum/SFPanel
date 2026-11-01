import type { TFunction } from 'i18next'
import type { RunRecord } from '@/types/api'
import { activeRuns } from './taskList'

/** Runs on one page of the history. */
export const HISTORY_PAGE_SIZE = 50

/** '' is every status; active is a run not yet ended (running, stopping, handing over). */
export type HistoryStatus = '' | 'active' | 'succeeded' | 'failed' | 'stopped'

export interface HistoryFilter {
  status: HistoryStatus
  /** A task ID; '' is every task. */
  task: string
}

/** The runs the filter keeps, in the order given (the server's: newest first). */
export function filterRuns(runs: RunRecord[], filter: HistoryFilter): RunRecord[] {
  const active = filter.status === 'active' ? new Set(activeRuns(runs)) : null
  return runs.filter((r) => {
    if (filter.task && r.task_id !== filter.task) return false
    if (active) return active.has(r)
    return !filter.status || r.status === filter.status
  })
}

/**
 * Every task the history holds, once, in the order of its newest run, named
 * as that run has it: a task renamed since keeps its latest name.
 */
export function historyTasks(runs: RunRecord[]): { id: string; name: string }[] {
  const seen = new Map<string, string>()
  for (const r of runs) if (!seen.has(r.task_id)) seen.set(r.task_id, r.task_name)
  return [...seen].map(([id, name]) => ({ id, name }))
}

/**
 * One page of the list. A page past the end (the list shrank under a filter
 * or a refresh) is the last one; pages counts from 1 even when list is empty.
 */
export function historyPage<T>(list: T[], page: number): { items: T[]; page: number; pages: number } {
  const pages = Math.max(1, Math.ceil(list.length / HISTORY_PAGE_SIZE))
  const p = Math.min(Math.max(1, page), pages)
  return { items: list.slice((p - 1) * HISTORY_PAGE_SIZE, p * HISTORY_PAGE_SIZE), page: p, pages }
}

/** How long the run took, in ms: null while it is under way or when its times do not read. */
export function runDuration(run: RunRecord): number | null {
  if (!run.ended_at) return null
  const start = new Date(run.started_at).getTime()
  const end = new Date(run.ended_at).getTime()
  if (Number.isNaN(start) || Number.isNaN(end)) return null
  return Math.max(0, end - start)
}

/** 42s, 1m 35s, 1h 2m: two units at most, the larger first. */
export function durationText(ms: number, t: TFunction): string {
  const total = Math.floor(ms / 1000)
  if (total < 60) return t('cluster.tasks.history.durationSeconds', { s: total })
  if (total < 3600) return t('cluster.tasks.history.durationMinutes', { m: Math.floor(total / 60), s: total % 60 })
  return t('cluster.tasks.history.durationHours', { h: Math.floor(total / 3600), m: Math.floor((total % 3600) / 60) })
}
