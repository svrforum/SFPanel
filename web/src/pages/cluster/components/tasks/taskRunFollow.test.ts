import { describe, expect, it } from 'vitest'
import type { RunNodeRecord, RunRecord, TaskRunDetail, TaskRunStreamEvent } from '@/types/api'
import type { ApiError } from '@/lib/api'
import type { RunState } from './taskRunProgress'
import { FAILURES_BEFORE_POLLING, followRun, type FollowIO } from './taskRunFollow'

const RUN_ID = '20261101-101500-abcd'

const head = (over: Partial<RunRecord> = {}): RunRecord => ({
  id: RUN_ID,
  task_id: 'builtin/command',
  task_name: 'Run a command',
  rev: '1',
  nodes: ['node-a', 'node-b'],
  waves: [['node-a', 'node-b']],
  batch: 2,
  stop_on_failure: true,
  status: 'running',
  started_by: 'admin',
  started_at: '2026-11-01T10:15:00Z',
  ...over,
})

const rec = (status: RunNodeRecord['status']): RunNodeRecord => ({ status })

const snapshot = (nodes: Record<string, RunNodeRecord>, run = head()): TaskRunStreamEvent => ({ type: 'snapshot', run, nodes })
const end: TaskRunStreamEvent = { type: 'end' }

// The refusal taskRunStream and getTaskRun throw for a run this manager does
// not have (yet), shaped as apiErrorFrom shapes it (api.ts cannot be imported
// here: it reaches for localStorage when loaded).
const apiError = (status: number, message: string, code?: string): ApiError =>
  Object.assign(new Error(message), { status, code })
const notFound = () => apiError(404, 'No such task run', 'TASK_RUN_NOT_FOUND')
// The panel going away under the request.
const unreachable = () => new TypeError('Failed to fetch')
// The same status, from something that is not this route (a reverse proxy).
const bare404 = () => apiError(404, 'HTTP 404')

// One scripted connection: the events it delivers, then how it closes. A
// connection with no events and an error is refused before the stream.
type Conn = { events?: TaskRunStreamEvent[]; error?: Error }
type Get = TaskRunDetail | Error

// A scripted network, in the manner of aptTransaction.test.ts: each open()
// replays the next connection, each get() answers the next detail, and every
// call lands in one log so the order of opens, gets and sleeps is visible.
function scripted(conns: Conn[], gets: Get[] = []) {
  const log: string[] = []
  const states: RunState[] = []
  const io: FollowIO = {
    open: async (id, onEvent) => {
      log.push(`open ${id}`)
      const c = conns.shift()
      if (!c) throw new Error('script ran out of connections')
      for (const e of c.events ?? []) onEvent(e)
      if (c.error) throw c.error
    },
    get: async (id) => {
      log.push(`get ${id}`)
      const g = gets.shift()
      if (!g) throw new Error('script ran out of gets')
      if (g instanceof Error) throw g
      return g
    },
    sleep: async (ms) => {
      log.push(`sleep ${ms}`)
      // A follower that never stops fails here, with its last steps, rather
      // than at the test's timeout.
      if (log.length > 100) throw new Error(`still following: ${log.slice(-6).join(', ')}`)
    },
  }
  return { io, log, states, onState: (s: RunState) => states.push(s) }
}

describe('followRun', () => {
  it('stops at the end event, having handed over each state', async () => {
    const { io, log, states, onState } = scripted([
      {
        events: [
          snapshot({ 'node-a': rec('running'), 'node-b': rec('pending') }),
          { type: 'node', node_id: 'node-b', record: rec('running') },
          { type: 'run', run: head({ status: 'succeeded' }) },
          end,
        ],
      },
    ])
    expect(await followRun(io, RUN_ID, onState)).toBe('ended')
    expect(log).toEqual([`open ${RUN_ID}`])
    expect(states).toHaveLength(4)
    expect(states.at(-1)).toEqual({
      run: head({ status: 'succeeded' }),
      nodes: { 'node-a': rec('running'), 'node-b': rec('running') },
      ended: true,
    })
  })

  it('opens a stream cut without end again, and takes the state from its new snapshot', async () => {
    const { io, log, states, onState } = scripted([
      { events: [snapshot({ 'node-a': rec('running'), 'node-b': rec('pending') }), { type: 'node', node_id: 'node-a', record: rec('failed') }] },
      { events: [snapshot({ 'node-a': rec('succeeded'), 'node-b': rec('succeeded') }, head({ status: 'succeeded' })), end] },
    ])
    expect(await followRun(io, RUN_ID, onState)).toBe('ended')
    expect(log).toEqual([`open ${RUN_ID}`, 'sleep 1000', `open ${RUN_ID}`])
    expect(states.at(-1)).toEqual({
      run: head({ status: 'succeeded' }),
      nodes: { 'node-a': rec('succeeded'), 'node-b': rec('succeeded') },
      ended: true,
    })
  })

  it('waits 1, 2 and then 5 seconds between attempts that keep failing', async () => {
    const { io, log, onState } = scripted([
      { events: [snapshot({ 'node-a': rec('running') })] },
      { error: unreachable() },
      { error: unreachable() },
      { error: unreachable() },
      { events: [snapshot({ 'node-a': rec('succeeded') }, head({ status: 'succeeded' })), end] },
    ])
    expect(await followRun(io, RUN_ID, onState)).toBe('ended')
    expect(log.filter((l) => l.startsWith('sleep'))).toEqual(['sleep 1000', 'sleep 2000', 'sleep 5000', 'sleep 5000'])
  })

  it('starts the waits over once a connection delivers again', async () => {
    const { io, log, onState } = scripted([
      { error: unreachable() },
      { error: unreachable() },
      { events: [snapshot({ 'node-a': rec('running') })] },
      { events: [snapshot({ 'node-a': rec('succeeded') }), end] },
    ])
    expect(await followRun(io, RUN_ID, onState)).toBe('ended')
    expect(log.filter((l) => l.startsWith('sleep'))).toEqual(['sleep 1000', 'sleep 2000', 'sleep 1000'])
  })

  it('looks three more times at a run not found, 0.5, 1 and 2 seconds apart, and then attaches', async () => {
    const { io, log, states, onState } = scripted([
      { error: notFound() },
      { error: notFound() },
      { error: notFound() },
      { events: [snapshot({ 'node-a': rec('running') }), end] },
    ])
    expect(await followRun(io, RUN_ID, onState)).toBe('ended')
    expect(log).toEqual([
      `open ${RUN_ID}`,
      'sleep 500',
      `open ${RUN_ID}`,
      'sleep 1000',
      `open ${RUN_ID}`,
      'sleep 2000',
      `open ${RUN_ID}`,
    ])
    expect(states.at(-1)?.nodes).toEqual({ 'node-a': rec('running') })
  })

  it('answers not_found for a run that is still not found after three more looks', async () => {
    const { io, log, states, onState } = scripted([
      { error: notFound() },
      { error: notFound() },
      { error: notFound() },
      { error: notFound() },
      { events: [snapshot({}), end] },
    ])
    expect(await followRun(io, RUN_ID, onState)).toBe('not_found')
    expect(log.filter((l) => l.startsWith('open'))).toHaveLength(4)
    expect(states).toEqual([])
  })

  it('does not take a 404 from something other than the route for a missing run', async () => {
    const { io, log, onState } = scripted([
      { error: bare404() },
      { error: bare404() },
      { error: bare404() },
      { error: bare404() },
      { events: [snapshot({}), end] },
    ])
    expect(await followRun(io, RUN_ID, onState)).toBe('ended')
    expect(log.filter((l) => l.startsWith('sleep'))).toEqual(['sleep 1000', 'sleep 2000', 'sleep 5000', 'sleep 5000'])
  })

  it(`polls the run every 5 seconds after ${FAILURES_BEFORE_POLLING} failures in a row, until it ends`, async () => {
    const failing = Array.from({ length: FAILURES_BEFORE_POLLING + 1 }, (): Conn => ({ error: unreachable() }))
    const { io, log, states, onState } = scripted(failing, [
      { run: head(), nodes: { 'node-a': rec('running'), 'node-b': rec('pending') } },
      unreachable(),
      { run: head({ status: 'failed' }), nodes: { 'node-a': rec('failed'), 'node-b': rec('pending') } },
    ])
    expect(await followRun(io, RUN_ID, onState)).toBe('ended')
    expect(log).toEqual([
      `open ${RUN_ID}`,
      'sleep 1000',
      `open ${RUN_ID}`,
      'sleep 2000',
      `open ${RUN_ID}`,
      'sleep 5000',
      `open ${RUN_ID}`,
      'sleep 5000',
      `open ${RUN_ID}`,
      // Five in a row: from here a GET keeps the table current, every 5 s,
      // and the stream is tried again only after a GET that answered.
      'sleep 5000',
      `get ${RUN_ID}`,
      `open ${RUN_ID}`,
      'sleep 5000',
      `get ${RUN_ID}`,
      'sleep 5000',
      `get ${RUN_ID}`,
    ])
    expect(states.map((s) => s.nodes['node-a'].status)).toEqual(['running', 'failed'])
    expect(states.at(-1)?.ended).toBe(true)
  })

  it('goes back to the stream when it opens again while polling', async () => {
    const failing = Array.from({ length: FAILURES_BEFORE_POLLING }, (): Conn => ({ error: unreachable() }))
    const { io, log, onState } = scripted(
      [...failing, { events: [snapshot({ 'node-a': rec('running') })] }, { events: [snapshot({ 'node-a': rec('succeeded') }), end] }],
      [{ run: head(), nodes: { 'node-a': rec('running') } }],
    )
    expect(await followRun(io, RUN_ID, onState)).toBe('ended')
    // The connection that delivered starts the waits over: 1 s, and no GET.
    expect(log.slice(-5)).toEqual(['sleep 5000', `get ${RUN_ID}`, `open ${RUN_ID}`, 'sleep 1000', `open ${RUN_ID}`])
    expect(log.filter((l) => l.startsWith('get'))).toHaveLength(1)
  })

  it('answers not_found when a poll keeps finding no run', async () => {
    const failing = Array.from({ length: FAILURES_BEFORE_POLLING }, (): Conn => ({ error: unreachable() }))
    const { io, onState } = scripted(failing, [notFound(), notFound(), notFound(), notFound()])
    expect(await followRun(io, RUN_ID, onState)).toBe('not_found')
  })

  it('answers aborted when the page goes away, and hands over nothing after', async () => {
    const ctl = new AbortController()
    const { io, states, onState } = scripted([
      { events: [snapshot({ 'node-a': rec('running') })] },
      { events: [snapshot({ 'node-a': rec('succeeded') }), end] },
    ])
    const sleep = io.sleep
    io.sleep = async (ms) => {
      ctl.abort()
      await sleep(ms)
    }
    expect(await followRun(io, RUN_ID, onState, ctl.signal)).toBe('aborted')
    expect(states).toHaveLength(1)
  })

  it('answers aborted, not a failure, when the abort cuts the open request', async () => {
    const ctl = new AbortController()
    const io: FollowIO = {
      open: async (_id, onEvent, signal) => {
        onEvent(snapshot({ 'node-a': rec('running') }))
        ctl.abort()
        // A late event the cut request still delivered.
        onEvent({ type: 'node', node_id: 'node-a', record: rec('failed') })
        expect(signal).toBe(ctl.signal)
        throw new DOMException('The operation was aborted.', 'AbortError')
      },
      get: async () => {
        throw new Error('no get after an abort')
      },
      sleep: async () => {
        throw new Error('no sleep after an abort')
      },
    }
    const states: RunState[] = []
    expect(await followRun(io, RUN_ID, (s) => states.push(s), ctl.signal)).toBe('aborted')
    expect(states.map((s) => s.nodes['node-a'].status)).toEqual(['running'])
  })

  it('opens nothing for a page that has already gone', async () => {
    const ctl = new AbortController()
    ctl.abort()
    const { io, log, onState } = scripted([])
    expect(await followRun(io, RUN_ID, onState, ctl.signal)).toBe('aborted')
    expect(log).toEqual([])
  })
})
