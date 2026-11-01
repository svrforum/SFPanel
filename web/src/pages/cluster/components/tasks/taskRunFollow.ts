import type { ApiError } from '@/lib/api'
import type { TaskRunDetail, TaskRunStreamEvent } from '@/types/api'
import { applyRunEvent, emptyRunState, runStateFrom, type RunState } from './taskRunProgress'

// Following a run from the progress page. The stream is only a view: a panel
// shutting down cuts it without an end, a manager that has not caught up yet
// does not know a run that just started, and either way the run goes on. The
// page follows it again, from the run ID in its URL.

/** What following a run needs from the network. */
export interface FollowIO {
  /** api.taskRunStream: resolves when the stream closes, with or without an end. A refusal throws. */
  open: (id: string, onEvent: (e: TaskRunStreamEvent) => void, signal?: AbortSignal) => Promise<void>
  /** api.getTaskRun. */
  get: (id: string) => Promise<TaskRunDetail>
  sleep: (ms: number) => Promise<void>
}

/** The waits between attempts that keep failing: the third and later wait the last. */
export const REOPEN_DELAYS_MS = [1000, 2000, 5000]
/** The waits before looking again at a run not found: three more looks, then it is not there. */
export const NOT_FOUND_DELAYS_MS = [500, 1000, 2000]
/** After this many failed attempts in a row, a GET keeps the page current. */
export const FAILURES_BEFORE_POLLING = 5
export const POLL_MS = 5000

export type FollowEnd = 'ended' | 'not_found' | 'aborted'

// The refusal for a run this manager does not have. A bare 404 from something
// in front of the panel is not one: it is a failure like any other.
const isNotFound = (err: unknown) => (err as ApiError | undefined)?.code === 'TASK_RUN_NOT_FOUND'

/**
 * followRun hands the run's state to onState as it changes, until the run's
 * stream ends ('ended'), the run cannot be found ('not_found') or signal is
 * aborted ('aborted', with nothing handed over after the abort).
 *
 * - An attempt fails when the stream closes without an end or cannot be
 *   opened. The next one waits 1, 2, then 5 seconds; a connection that
 *   delivered an event starts the waits over. Opening again starts from a
 *   snapshot, which replaces the state.
 * - TASK_RUN_NOT_FOUND may be a manager that has not caught up with a run that
 *   just started: it is looked at three more times, 0.5, 1 and 2 seconds
 *   apart, before the answer is 'not_found'.
 * - After five failures in a row, each attempt waits 5 seconds and GETs the
 *   run first, so the table moves even while the stream will not open; the
 *   stream is tried again after every GET that answered.
 */
export async function followRun(
  io: FollowIO,
  id: string,
  onState: (s: RunState) => void,
  signal?: AbortSignal,
): Promise<FollowEnd> {
  const aborted = () => signal?.aborted === true
  let state = emptyRunState
  let failures = 0
  let notFound = 0

  // A run not found: wait to look again, or answer that it is not there.
  const lookAgain = async (): Promise<boolean> => {
    if (notFound >= NOT_FOUND_DELAYS_MS.length) return false
    await io.sleep(NOT_FOUND_DELAYS_MS[notFound++])
    return true
  }

  for (;;) {
    if (aborted()) return 'aborted'

    if (failures >= FAILURES_BEFORE_POLLING) {
      let detail: TaskRunDetail
      try {
        detail = await io.get(id)
      } catch (err) {
        if (aborted()) return 'aborted'
        if (isNotFound(err)) {
          if (!(await lookAgain())) return 'not_found'
        } else {
          await io.sleep(POLL_MS)
        }
        continue
      }
      if (aborted()) return 'aborted'
      notFound = 0
      state = runStateFrom(detail)
      onState(state)
      if (state.ended) return 'ended'
    }

    let delivered = false
    let ended = false
    try {
      await io.open(
        id,
        (ev) => {
          if (aborted() || ended) return
          delivered = true
          if (ev.type === 'end') ended = true
          state = applyRunEvent(state, ev)
          onState(state)
        },
        signal,
      )
    } catch (err) {
      // A run not found is looked at again. An abort is answered below, and
      // anything else is a failed attempt.
      if (!aborted() && !delivered && isNotFound(err)) {
        if (!(await lookAgain())) return 'not_found'
        continue
      }
    }
    if (aborted()) return 'aborted'
    if (ended) return 'ended'

    if (delivered) {
      notFound = 0
      failures = 1
    } else {
      failures++
    }
    await io.sleep(failures >= FAILURES_BEFORE_POLLING ? POLL_MS : REOPEN_DELAYS_MS[Math.min(failures, REOPEN_DELAYS_MS.length) - 1])
  }
}
