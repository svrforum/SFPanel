import { afterEach, describe, expect, it, vi } from 'vitest'
import i18next from 'i18next'
import en from '@/i18n/locales/en.json'
import ko from '@/i18n/locales/ko.json'
import type { TaskRunStreamEvent } from '@/types/api'

// The group-task API is answered by the manager this browser is attached to:
// every manager holds the tasks and the runs, and the leader takes the writes
// over from it. A ?node= sent an agent's 409 or had another manager answer.
// Built-in task IDs carry a slash (builtin/command), which the path has to
// carry escaped exactly once. The preview asks every node and can outlast
// the client's 30 s default, as can both Git routes.
//
// Same harness as clusterRoutes.test.ts: the client is a module singleton
// reading storage in its constructor, and vitest runs in node with no DOM.

function memoryStorage(): Storage {
  const map = new Map<string, string>()
  return {
    get length() {
      return map.size
    },
    clear: () => {
      map.clear()
    },
    getItem: (key: string) => map.get(key) ?? null,
    key: (index: number) => Array.from(map.keys())[index] ?? null,
    removeItem: (key: string) => {
      map.delete(key)
    },
    setItem: (key: string, value: string) => {
      map.set(key, value)
    },
  }
}

type Answer = (url: string, init?: RequestInit) => Promise<Response>

const ok: Answer = () =>
  Promise.resolve(new Response(JSON.stringify({ success: true, data: {} }), { status: 200, headers: { 'Content-Type': 'application/json' } }))

async function freshApi(answer: Answer = ok) {
  const local = memoryStorage()
  // A node is selected: the task routes must not follow it.
  local.setItem('sfpanel_current_node', 'node-b')
  const session = memoryStorage()
  session.setItem('token', 'test-token')
  vi.stubGlobal('sessionStorage', session)
  vi.stubGlobal('localStorage', local)
  vi.stubGlobal('window', {})
  vi.stubGlobal('document', { cookie: '' })

  const calls: { url: string; init?: RequestInit }[] = []
  vi.stubGlobal('fetch', (input: RequestInfo | URL, init?: RequestInit) => {
    calls.push({ url: String(input), init })
    return answer(String(input), init)
  })

  vi.resetModules()
  const { api } = await import('./api')
  return { api, calls }
}

// A body that arrives in the pieces given, as a network delivers it.
function streamOf(pieces: string[]): Response {
  const encoder = new TextEncoder()
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      for (const piece of pieces) controller.enqueue(encoder.encode(piece))
      controller.close()
    },
  })
  return new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } })
}

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
})

describe('task routes', () => {
  it("asks the browser's manager, whatever node is selected", async () => {
    const { api, calls } = await freshApi()
    await api.listTasks()
    await api.getTask('t1')
    await api.validateTask('name: x')
    await api.formatTask({ name: 'x', targets: {}, batch: 1, stop_on_failure: true, steps: [] })
    await api.createTask('name: x')
    await api.updateTask('t1', 'name: x')
    await api.deleteTask('t1')
    await api.planTaskRun({ task_id: 't1' })
    await api.startTaskRun({ task_id: 't1' })
    await api.listTaskRuns()
    await api.getTaskRun('r1')
    await api.stopTaskRun('r1', true)
    await api.rerunTaskRun('r1', { onlyFailed: true })
    await api.rerunTaskRun('r1', { nodes: ['a'] })
    await api.getTaskStepOutput('r1', 'node-a', 2, 35)
    await api.previewTaskImport({ url: 'https://github.com/acme/ops', branch: 'main', path: 'tasks' })
    await api.importTasks({ url: 'https://github.com/acme/ops', branch: 'main', path: 'tasks', commit: 'c'.repeat(40), files: ['tasks/a.yaml'] })
    expect(calls).toHaveLength(17)
    for (const { url } of calls) {
      expect(url).toContain('/api/v1/cluster/')
      expect(url).not.toContain('node=')
    }
  })

  it('escapes a built-in ID once in the path, and leaves it as it is in a body', async () => {
    const { api, calls } = await freshApi()
    await api.getTask('builtin/command')
    await api.updateTask('builtin/command', 'name: x')
    await api.deleteTask('builtin/command')
    await api.planTaskRun({ task_id: 'builtin/command' })
    expect(calls.slice(0, 3).map((c) => c.url)).toEqual([
      '/api/v1/cluster/tasks/builtin%2Fcommand',
      '/api/v1/cluster/tasks/builtin%2Fcommand',
      '/api/v1/cluster/tasks/builtin%2Fcommand',
    ])
    expect(JSON.parse(String(calls[3].init?.body))).toEqual({ task_id: 'builtin/command' })
  })

  it('sends each request where and how the server takes it', async () => {
    const { api, calls } = await freshApi()
    await api.stopTaskRun('20261101-091500-3fa2c1', true)
    await api.rerunTaskRun('20261101-091500-3fa2c1', { onlyFailed: true })
    await api.rerunTaskRun('20261101-091500-3fa2c1', { nodes: ['node-a2'] })
    await api.getTaskStepOutput('20261101-091500-3fa2c1', 'node/a', 2, 35)
    await api.formatTask({ name: 'x', targets: {}, batch: 1, stop_on_failure: true, steps: [] })
    const seen = calls.map((c) => [c.init?.method ?? 'GET', c.url, c.init?.body ?? null])
    expect(seen).toEqual([
      ['POST', '/api/v1/cluster/task-runs/20261101-091500-3fa2c1/stop', '{"force":true}'],
      ['POST', '/api/v1/cluster/task-runs/20261101-091500-3fa2c1/rerun?only=failed', null],
      ['POST', '/api/v1/cluster/task-runs/20261101-091500-3fa2c1/rerun', '{"nodes":["node-a2"]}'],
      ['GET', '/api/v1/cluster/task-runs/20261101-091500-3fa2c1/nodes/node%2Fa/steps/2/output?skip=35', null],
      ['POST', '/api/v1/cluster/tasks/format', '{"task":{"name":"x","targets":{},"batch":1,"stop_on_failure":true,"steps":[]}}'],
    ])
  })
})

describe('slow task calls', () => {
  // A fetch that answers only when it is aborted, as a preview asking 200
  // nodes keeps the browser waiting.
  const hangs: Answer = (_url, init) =>
    new Promise((_resolve, reject) => {
      init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
    })

  async function settledAfter(ms: number, start: () => Promise<unknown>) {
    let settled = false
    start().then(
      () => { settled = true },
      () => { settled = true },
    )
    await vi.advanceTimersByTimeAsync(ms)
    return settled
  }

  it('waits past 30 seconds for the preview and both Git routes', async () => {
    vi.useFakeTimers()
    const { api } = await freshApi(hangs)
    const git = { url: 'https://github.com/acme/ops', branch: 'main', path: 'tasks' }
    expect(await settledAfter(60_000, () => api.planTaskRun({ task_id: 't1' }))).toBe(false)
    expect(await settledAfter(60_000, () => api.previewTaskImport(git))).toBe(false)
    expect(await settledAfter(60_000, () => api.importTasks({ ...git, commit: 'c'.repeat(40), files: ['tasks/a.yaml'] }))).toBe(false)
  })

  it('still gives up on an ordinary call at 30 seconds', async () => {
    vi.useFakeTimers()
    const { api } = await freshApi(hangs)
    expect(await settledAfter(30_001, () => api.listTasks())).toBe(true)
  })
})

describe('taskRunStream', () => {
  it("opens the browser's manager's stream, whatever node is selected", async () => {
    const { api, calls } = await freshApi(() => Promise.resolve(streamOf([])))
    await api.taskRunStream('20261101-091500-3fa2c1', () => {})
    expect(calls).toHaveLength(1)
    expect(calls[0].url).toBe('/api/v1/cluster/task-runs/20261101-091500-3fa2c1/stream')
    expect((calls[0].init?.headers as Record<string, string>).Authorization).toBe('Bearer test-token')
  })

  it('rejects a refused stream with its code, not a bare status', async () => {
    const { api } = await freshApi(() =>
      Promise.resolve(new Response(JSON.stringify({ success: false, error: { code: 'TASK_RUN_NOT_FOUND', message: 'No such task run' } }), {
        status: 404,
        headers: { 'Content-Type': 'application/json' },
      })))
    const err = await api.taskRunStream('r1', () => {}).then(
      () => undefined,
      (e: unknown) => e as { status?: number; code?: string; message: string },
    )
    expect([err?.status, err?.code, err?.message]).toEqual([404, 'TASK_RUN_NOT_FOUND', 'No such task run'])
  })

  it('reads the data lines, split however they arrive, and drops the keepalives', async () => {
    const snapshot = { type: 'snapshot', run: { id: 'r1', status: 'running' }, nodes: { a: { status: 'pending' } } }
    const node = { type: 'node', node_id: 'a', record: { status: 'running' } }
    const text = `data: ${JSON.stringify(snapshot)}\n\n: keepalive\n\ndata: ${JSON.stringify(node)}\n\n: keepalive\n\ndata: {"type":"end"}\n\n`
    // Cut mid-line, mid-keepalive and mid-JSON.
    const pieces = [text.slice(0, 7), text.slice(7, 60), text.slice(60, 140), text.slice(140)]
    const { api } = await freshApi(() => Promise.resolve(streamOf(pieces)))
    const events: TaskRunStreamEvent[] = []
    await api.taskRunStream('r1', (e) => events.push(e))
    expect(events).toEqual([snapshot, node, { type: 'end' }])
  })

  it('passes the caller signal to the fetch, so leaving the page closes the stream', async () => {
    const { api, calls } = await freshApi(() => Promise.resolve(streamOf([])))
    const controller = new AbortController()
    await api.taskRunStream('r1', () => {}, controller.signal)
    expect(calls[0].init?.signal).toBe(controller.signal)
  })
})

describe('task and Git refusals', () => {
  // Each code with the details its return sites send (internal/feature/tasks,
  // taskjob, cluster/task_gate.go, githubapi). i18next prints a placeholder it
  // was given no value for as it stands, so a sentence naming a key one site
  // leaves out shows "{{key}}" to the operator.
  const sent: [string, Record<string, string> | undefined][] = [
    ['TASK_INVALID', undefined],
    ['TASK_TOO_LARGE', { max_kib: '64' }],
    ['TASK_TOO_LARGE', { input: 'service', max_kib: '16' }],
    ['TASK_TOO_LARGE', { input: '*', max_kib: '64' }],
    ['TASK_TOO_LARGE', { path: 'tasks/big.yaml', max_kib: '64' }],
    ['TASK_NAME_TAKEN', undefined],
    ['TASK_NOT_FOUND', undefined],
    ['TASK_READ_ONLY', undefined],
    ['TASK_RUN_NOT_FOUND', undefined],
    ['TASK_MANAGERS_NOT_READY', { managers: 'peer-2 (offline), peer-3 (0.80.0)' }],
    ['TASK_INPUT_MISSING', { input: 'service' }],
    ['TASK_NO_TARGETS', undefined],
    ['TASK_TOO_MANY_TARGETS', { max: '200' }],
    ['TASK_TARGET_BUSY', { node_id: 'node-a2', run_id: '20261101-091500-3fa2c1' }],
    ['TASK_PLAN_CHANGED', undefined],
    ['TASK_REBOOT_BUSY', { run_id: '20261101-091500-3fa2c1' }],
    ['TASK_CLUSTER_CHANGING', { what: 'conversion' }],
    ['TASK_RUN_ACTIVE', { run_id: '20261101-091500-3fa2c1' }],
    ['TASK_JOB_NOT_FOUND', undefined],
    ['TASK_JOB_CLUSTER_ONLY', undefined],
    ['TASK_NODE_BUSY', undefined],
    ['TASK_JOB_RUNNING', undefined],
    ['TASK_REBOOT_PVE_HOST', undefined],
    ['TASK_REBOOT_UNSUPPORTED', undefined],
    ['GIT_AUTH_FAILED', undefined],
    ['GIT_REPO_NOT_FOUND', undefined],
    ['GIT_PATH_NOT_FOUND', undefined],
    ['GIT_CLONE_FAILED', undefined],
    ['GIT_RATE_LIMITED', { reset_at: '2026-11-01T10:00:00Z', limit: '60', retry_after: '120' }],
    ['GIT_RATE_LIMITED', { retry_after: '120' }],
    ['GIT_RATE_LIMITED', undefined],
  ]

  it.each(['en', 'ko'])('words every one in %s, with nothing left unfilled', async (lng) => {
    await freshApi()
    const { apiErrorFrom, setApiTranslator } = await import('./api')
    const i18n = i18next.createInstance()
    await i18n.init({ lng, resources: { en: { translation: en }, ko: { translation: ko } }, interpolation: { escapeValue: false } })
    setApiTranslator((key, fallback, params) => i18n.t(key, { ...params, defaultValue: fallback }))
    for (const [code, details] of sent) {
      const message = apiErrorFrom(409, { success: false, error: { code, message: 'SERVER SENTENCE', details } }).message
      expect(message, code).not.toBe('SERVER SENTENCE')
      expect(message, code).not.toMatch(/\{\{/)
    }
  })

  it('names the limit, the input and the run the server sent', async () => {
    await freshApi()
    const { apiErrorFrom, setApiTranslator } = await import('./api')
    const i18n = i18next.createInstance()
    await i18n.init({ lng: 'en', resources: { en: { translation: en } }, interpolation: { escapeValue: false } })
    setApiTranslator((key, fallback, params) => i18n.t(key, { ...params, defaultValue: fallback }))
    const word = (code: string, details?: Record<string, string>) =>
      apiErrorFrom(400, { success: false, error: { code, message: 'x', details } }).message
    expect(word('TASK_TOO_LARGE', { input: 'service', max_kib: '16' })).toContain('16 KiB')
    expect(word('TASK_INPUT_MISSING', { input: 'service' })).toContain('service')
    expect(word('TASK_RUN_ACTIVE', { run_id: '20261101-091500-3fa2c1' })).toContain('20261101-091500-3fa2c1')
    expect(word('GIT_RATE_LIMITED', { reset_at: '2026-11-01T10:00:00Z' })).toContain('2026-11-01T10:00:00Z')
    // The worded sentence would otherwise hide GitHub's only clue.
    expect(word('GIT_CLONE_FAILED', { cause: 'GitHub answered HTTP 502' })).toContain('GitHub answered HTTP 502')
    // The files page's message gives the file's size and the limit; a worded
    // sentence would drop both.
    expect(word('FILE_TOO_LARGE')).toBe('x')
  })
})
