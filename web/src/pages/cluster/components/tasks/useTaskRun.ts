import { useCallback, useEffect, useState } from 'react'
import { api } from '@/lib/api'
import type { RunRecord, RunStepName, TaskRunDetail } from '@/types/api'
import { applyRunEvent, emptyRunState, runStateFrom, type RunState } from './taskRunProgress'
import { followRun, type FollowEnd, type FollowIO } from './taskRunFollow'

export interface TaskRunFollow {
  state: RunState
  /** The run document's steps; undefined until read, or when its revision cannot be read. */
  steps: RunStepName[] | undefined
  /** How following ended; null while it goes on. */
  outcome: FollowEnd | null
  /** Takes a head the server answered (a stop) ahead of the stream. */
  setHead: (run: RunRecord) => void
}

// A wait that ends early when the page goes away.
function pause(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    if (signal.aborted) return resolve()
    const timer = setTimeout(done, ms)
    function done() {
      clearTimeout(timer)
      signal.removeEventListener('abort', done)
      resolve()
    }
    signal.addEventListener('abort', done)
  })
}

/**
 * useTaskRun follows one run from its ID: the run is read first, for its step
 * names and so an expired session is renewed through request() before the
 * stream opens, and seeds the page; then the stream's snapshots and records
 * take over, opened again whenever it is cut (followRun). The page that holds
 * it is keyed by the run ID. Leaving it aborts the follow, which StrictMode's
 * second effect relies on.
 */
export function useTaskRun(runId: string): TaskRunFollow {
  const [state, setState] = useState<RunState>(emptyRunState)
  const [steps, setSteps] = useState<RunStepName[] | undefined>(undefined)
  const [outcome, setOutcome] = useState<FollowEnd | null>(null)

  useEffect(() => {
    const ctl = new AbortController()
    const signal = ctl.signal
    const io: FollowIO = {
      open: (id, onEvent, s) => api.taskRunStream(id, onEvent, s),
      get: (id) => api.getTaskRun(id),
      sleep: (ms) => pause(ms, signal),
    }
    let heard = false
    let read = false
    const take = (detail: TaskRunDetail) => {
      read = true
      setSteps(detail.steps)
    }

    void (async () => {
      try {
        const detail = await api.getTaskRun(runId)
        if (signal.aborted) return
        take(detail)
        setState(runStateFrom(detail))
      } catch {
        // A run that has just started may not have reached this manager yet:
        // the follower looks again, and the steps are read once it is found.
        if (signal.aborted) return
      }
      const end = await followRun(
        io,
        runId,
        (s) => {
          setState(s)
          if (heard || read) return
          heard = true
          api.getTaskRun(runId).then((d) => !signal.aborted && take(d), () => {})
        },
        signal,
      )
      if (!signal.aborted) setOutcome(end)
    })()

    return () => ctl.abort()
  }, [runId])

  const setHead = useCallback((run: RunRecord) => setState((s) => applyRunEvent(s, { type: 'run', run })), [])

  return { state, steps, outcome, setHead }
}
