import type { RunNodeRecord, RunNodeStatus, RunRecord, RunStepRecord, RunStatus, TaskRunDetail, TaskRunStreamEvent } from '@/types/api'

// A task run as the progress page shows it, built from the run's stream
// (internal/feature/tasks/stream.go): a snapshot first, on every connection,
// then a node's record or the run's head whenever one changes, then end.

/** What the page knows of a run. run is null until the first snapshot. */
export type RunState = { run: RunRecord | null; nodes: Record<string, RunNodeRecord>; ended: boolean }

export const emptyRunState: RunState = { run: null, nodes: {}, ended: false }

/**
 * applyRunEvent folds one stream event into the state, without changing the
 * state it was given. A snapshot replaces everything: a connection opened
 * again starts from one, and what the cut connection left is stale.
 */
export function applyRunEvent(state: RunState, ev: TaskRunStreamEvent): RunState {
  switch (ev.type) {
    case 'snapshot':
      return { run: ev.run, nodes: { ...ev.nodes }, ended: false }
    case 'node':
      return { ...state, nodes: { ...state.nodes, [ev.node_id]: ev.record } }
    case 'run':
      return { ...state, run: ev.run }
    case 'end':
      return { ...state, ended: true }
  }
}

// The run statuses under which the run still moves (tasks_store.go).
const ACTIVE_RUN: ReadonlySet<RunStatus> = new Set<RunStatus>(['running', 'stopping', 'handing_over'])

/** runStateFrom reads GET /cluster/task-runs/:id as a state: ended once the run is. */
export function runStateFrom(detail: TaskRunDetail): RunState {
  return { run: detail.run, nodes: { ...detail.nodes }, ended: !ACTIVE_RUN.has(detail.run.status) }
}

/**
 * A cell of the node × step table: the design's six (waiting, in progress,
 * succeeded, no change, failed, skipped) and the server's two more, a step
 * whose outcome the run lost track of and a step a stop cancelled.
 */
export type CellState = 'pending' | 'running' | 'changed' | 'unchanged' | 'failed' | 'skipped' | 'unknown' | 'cancelled'

const CELL_STATES: readonly CellState[] = ['pending', 'running', 'changed', 'unchanged', 'failed', 'skipped', 'unknown', 'cancelled']

/** cellState names a step's cell. A node not started has no steps: pending. */
export function cellState(step?: RunStepRecord): CellState {
  switch (step?.status) {
    case undefined:
    case 'pending':
      return 'pending'
    case 'starting':
    case 'running':
      return 'running'
    case 'succeeded':
      // changed is left out when false.
      return step.changed ? 'changed' : 'unchanged'
    case 'failed':
    case 'skipped':
    case 'cancelled':
      return step.status
    default:
      return 'unknown'
  }
}

/** A node's row: its cells, one per step of the task. */
export interface RunRow {
  id: string
  name: string
  status: RunNodeStatus
  reason?: string
  cells: CellState[]
}

export interface RunMatrix {
  rows: RunRow[]
  /** The wave the run is in, from 1; null when the run has no waves. */
  wave: { index: number; total: number } | null
  /** How many cells are in each state, over the whole table. */
  counts: Record<CellState, number>
}

// A node with one of these has not finished its part of the run.
const UNFINISHED_NODE: ReadonlySet<RunNodeStatus> = new Set<RunNodeStatus>(['pending', 'running'])

/**
 * runMatrix lays the state out as the table: a row per node in run order,
 * with stepCount cells each. The run records no current wave, so it is the
 * first wave that still has a node not finished, or the last once every node
 * has.
 */
export function runMatrix(state: RunState, stepCount: number, nodeName: (id: string) => string): RunMatrix {
  const counts = Object.fromEntries(CELL_STATES.map((c) => [c, 0])) as Record<CellState, number>
  const run = state.run
  if (!run) return { rows: [], wave: null, counts }

  const statusOf = (id: string): RunNodeStatus => state.nodes[id]?.status ?? 'pending'

  const rows = run.nodes.map((id): RunRow => {
    const record = state.nodes[id]
    const cells = Array.from({ length: stepCount }, (_, i) => cellState(record?.steps?.[i]))
    for (const c of cells) counts[c]++
    const row: RunRow = { id, name: nodeName(id), status: statusOf(id), cells }
    if (record?.reason) row.reason = record.reason
    return row
  })

  const total = run.waves.length
  let wave: RunMatrix['wave'] = null
  if (total > 0) {
    const at = run.waves.findIndex((ids) => ids.some((id) => UNFINISHED_NODE.has(statusOf(id))))
    wave = { index: at < 0 ? total : at + 1, total }
  }
  return { rows, wave, counts }
}
