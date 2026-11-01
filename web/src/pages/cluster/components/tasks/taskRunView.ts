import type { TFunction } from 'i18next'
import type { StatusPillTone } from '@/components/StatusPill'
import type { RunNodeRecord, RunNodeStatus, RunRecord, RunStepName, RunStepStatus, TaskJobView } from '@/types/api'
import { builtinText } from './taskBuiltins'
import type { CellState, RunState } from './taskRunProgress'

// What the progress page works out for itself: how many columns the table
// has, what a step is called, which step a node opens on, a step's output as
// it arrives, and which buttons a run offers.

/**
 * The table's columns: the run document's steps, or, when the server could
 * not read that revision, the longest step list a node has recorded.
 */
export function runStepCount(steps: RunStepName[] | undefined, state: RunState): number {
  if (steps) return steps.length
  return Object.values(state.nodes).reduce((n, record) => Math.max(n, record.steps?.length ?? 0), 0)
}

/** A step's header: a built-in's in the screen's words, a stored task's as written, otherwise "Step N". */
export function stepHeader(t: TFunction, taskId: string, steps: RunStepName[] | undefined, i: number): string {
  return builtinText(t, taskId, `steps.${i}`, steps?.[i]?.name ?? '') || t('cluster.tasks.plan.step', { n: i + 1 })
}

/** How much of a step's output the page keeps, in characters: the last 1 MiB of plain text. */
export const OUTPUT_KEEP = 1 << 20

/** A step's output so far. skip is the node's next_skip, sent back as given. */
export interface OutputState {
  text: string
  skip: number
  done: boolean
  /** Earlier output was dropped to keep the page within OUTPUT_KEEP. */
  trimmed: boolean
}

export const emptyOutput: OutputState = { text: '', skip: 0, done: false, trimmed: false }

/**
 * appendOutput adds one answer of the output route. next_skip is a place in
 * the node's raw log, not a count of the text, so it is kept as sent. Past
 * OUTPUT_KEEP the oldest text goes, from the start of a line where one falls
 * within the part kept.
 */
export function appendOutput(prev: OutputState, view: TaskJobView, keep = OUTPUT_KEEP): OutputState {
  let text = prev.text + (view.output ?? '')
  let trimmed = prev.trimmed
  if (text.length > keep) {
    const cut = text.length - keep
    // Where the cut falls inside a line, the rest of that line goes too.
    const next = text[cut - 1] === '\n' ? cut - 1 : text.indexOf('\n', cut)
    text = next >= 0 && next < text.length - 1 ? text.slice(next + 1) : text.slice(cut)
    trimmed = true
  }
  return { text, skip: view.next_skip, done: view.done, trimmed }
}

const UNDER_WAY: ReadonlySet<RunStepStatus> = new Set<RunStepStatus>(['starting', 'running'])
const WENT_WRONG: ReadonlySet<RunStepStatus> = new Set<RunStepStatus>(['failed', 'unknown'])

/**
 * The step a node opens on when its name is chosen: the one under way, else
 * the first that failed or was lost, else the last that did anything.
 */
export function focusStep(record?: RunNodeRecord): number {
  const steps = record?.steps ?? []
  const under = steps.findIndex((s) => UNDER_WAY.has(s.status))
  if (under >= 0) return under
  const wrong = steps.findIndex((s) => WENT_WRONG.has(s.status))
  if (wrong >= 0) return wrong
  for (let i = steps.length - 1; i >= 0; i--) {
    if (steps[i].status !== 'pending') return i
  }
  return 0
}

/** The nodes ?only=failed reruns, in run order: every one that did not succeed (rerun.go). */
export function unsuccessfulNodes(state: RunState): string[] {
  return (state.run?.nodes ?? []).filter((id) => state.nodes[id]?.status !== 'succeeded')
}

/**
 * The buttons a run offers. A run under way can be stopped, or stopped by
 * force; a stop already asked for can still be forced; an ended run can run
 * again.
 */
export function runActions(run: RunRecord): { stop: boolean; forceStop: boolean; rerun: boolean } {
  switch (run.status) {
    case 'running':
    case 'handing_over':
      return { stop: true, forceStop: true, rerun: false }
    case 'stopping':
      return { stop: false, forceStop: !run.force, rerun: false }
    default:
      return { stop: false, forceStop: false, rerun: true }
  }
}

// Literal keys, so locales.test sees every one.
const CELL_TEXT: Record<CellState, (t: TFunction) => string> = {
  pending: (t) => t('cluster.tasks.cell.pending'),
  running: (t) => t('cluster.tasks.cell.running'),
  changed: (t) => t('cluster.tasks.cell.changed'),
  unchanged: (t) => t('cluster.tasks.cell.unchanged'),
  failed: (t) => t('cluster.tasks.cell.failed'),
  skipped: (t) => t('cluster.tasks.cell.skipped'),
  unknown: (t) => t('cluster.tasks.cell.unknown'),
  cancelled: (t) => t('cluster.tasks.cell.cancelled'),
}

const CELL_TONE: Record<CellState, StatusPillTone> = {
  pending: 'secondary',
  running: 'primary',
  changed: 'success',
  unchanged: 'muted',
  failed: 'destructive',
  skipped: 'muted',
  unknown: 'warning',
  cancelled: 'warning',
}

/** The cells in the order the counts are shown. */
export const CELL_ORDER: readonly CellState[] = ['changed', 'unchanged', 'running', 'pending', 'failed', 'unknown', 'cancelled', 'skipped']

export function cellText(cell: CellState, t: TFunction): string {
  return CELL_TEXT[cell](t)
}

export function cellTone(cell: CellState): StatusPillTone {
  return CELL_TONE[cell]
}

const NODE_TEXT: Record<RunNodeStatus, (t: TFunction) => string> = {
  pending: (t) => t('cluster.tasks.nodeStatus.pending'),
  running: (t) => t('cluster.tasks.nodeStatus.running'),
  succeeded: (t) => t('cluster.tasks.nodeStatus.succeeded'),
  failed: (t) => t('cluster.tasks.nodeStatus.failed'),
  skipped: (t) => t('cluster.tasks.nodeStatus.skipped'),
  unknown: (t) => t('cluster.tasks.nodeStatus.unknown'),
  stopped: (t) => t('cluster.tasks.nodeStatus.stopped'),
}

const NODE_TONE: Record<RunNodeStatus, StatusPillTone> = {
  pending: 'secondary',
  running: 'primary',
  succeeded: 'success',
  failed: 'destructive',
  skipped: 'muted',
  unknown: 'warning',
  stopped: 'muted',
}

/** A node's part of the run in the screen's words; a status this screen does not know, as sent. */
export function nodeStatusText(status: RunNodeStatus, t: TFunction): string {
  return Object.hasOwn(NODE_TEXT, status) ? NODE_TEXT[status](t) : status
}

export function nodeStatusTone(status: RunNodeStatus): StatusPillTone {
  return Object.hasOwn(NODE_TONE, status) ? NODE_TONE[status] : 'muted'
}
