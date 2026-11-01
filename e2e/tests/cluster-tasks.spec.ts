import { test, expect, type Page, type Route, type WebSocketRoute } from '@playwright/test'

// The cluster's Tasks tab: group tasks picked from a list (built-in, the
// operator's own, imported from Git), set up and previewed, run, followed
// node by node, and found again in the history. Mocked API only: three
// managers (m-1 leads and serves this browser) and four agents in the groups
// web and db.
test.use({ viewport: { width: 1400, height: 1000 }, locale: 'en-US' })

// ---------------------------------------------------------------- fixture --

const seen = '2026-01-01T00:00:00Z'
const base = { status: 'online', joined_at: seen, last_seen: seen }
const managers = [1, 2, 3].map((i) => ({
  ...base,
  id: `m-${i}`,
  name: `manager-${i}`,
  kind: 'manager',
  role: 'voter',
  api_address: `192.0.2.${i}:3628`,
  grpc_address: `192.0.2.${i}:3629`,
}))
const agents = [1, 2, 3, 4].map((i) => ({
  ...base,
  id: `a-${i}`,
  name: `agent-0${i}`,
  kind: 'agent',
  role: '',
  api_address: `198.51.100.${i}:3628`,
  grpc_address: `198.51.100.${i}:3629`,
  labels: { group: i <= 2 ? 'web' : 'db' },
}))
const nodes = [...managers, ...agents]

// The built-ins as the server lists them: Korean names from the binary.
const builtin = (file: string, name: string, description: string) => ({
  id: `builtin/${file}`, name, description, source: { kind: 'builtin' }, read_only: true,
})
const BUILTINS = [
  builtin('package-update', '패키지 업데이트', '패키지 목록을 새로 받고 설치된 패키지를 모두 업그레이드합니다.'),
  builtin('package-install', '패키지 설치', '패키지 목록을 새로 받고 입력한 패키지를 한 번에 설치합니다. 여러 개는 공백이나 쉼표로 나눕니다.'),
  builtin('service-restart', '서비스 재시작', '입력한 서비스를 재시작합니다.'),
  builtin('command', '명령 실행', '입력한 명령을 root 권한으로 실행합니다. 입력한 그대로 셸에 전달됩니다.'),
  builtin('reboot', '재부팅 (필요할 때만)', '업데이트 뒤 재부팅이 필요한 서버만 재부팅하고, 다시 연결될 때까지 기다립니다.'),
]
const USER_TASKS = [
  { id: '0a1b2c3d4e5f6071', name: 'Deploy web', description: 'Pulls and restarts the web stack', source: { kind: 'user' }, read_only: false, updated_at: seen, updated_by: 'admin' },
  { id: '1b2c3d4e5f607182', name: 'Rotate logs', source: { kind: 'user' }, read_only: false, updated_at: seen, updated_by: 'admin' },
]
const GIT_TASKS = [
  {
    id: '2c3d4e5f60718293', name: 'Nightly backup', description: 'Copies /srv to the backup host', read_only: true, updated_at: seen, updated_by: 'admin',
    source: { kind: 'git', repo: 'https://github.com/acme/ops', branch: 'main', path: 'tasks/backup.yaml', sha: '0123456789abcdef0123456789abcdef01234567' },
  },
]
const TASKS = [...BUILTINS, ...USER_TASKS, ...GIT_TASKS]

// The tasks as GET /cluster/tasks/:id has them: the document's model. Deploy
// web asks for a version (required) and extra flags (a default, raw), takes
// the group web, runs two agents at a time; its second step restarts nginx.
const DEPLOY_ID = '0a1b2c3d4e5f6071'
const DEPLOY = {
  ...USER_TASKS[0],
  yaml: 'name: Deploy web\n',
  task: {
    name: 'Deploy web',
    description: 'Pulls and restarts the web stack',
    inputs: [
      { name: 'version', label: 'Version', required: true },
      { name: 'flags', label: 'Extra flags', default: '--pull', raw: true },
    ],
    targets: { groups: ['web'] },
    batch: 2,
    stop_on_failure: true,
    steps: [
      { kind: 'stack', name: 'Update the web stack', stack: { name: 'web', compose: 'services: {}\n', up: true } },
      { kind: 'service', name: 'Restart nginx', service: { name: 'nginx.service', action: 'restart' } },
    ],
  },
}
// Built-ins define no targets: the picker starts empty for them.
const COMMAND = {
  ...BUILTINS[3],
  yaml: 'name: 명령 실행\n',
  task: {
    name: '명령 실행',
    inputs: [{ name: 'command', label: '실행할 명령', required: true, raw: true }],
    targets: {},
    batch: 1,
    stop_on_failure: true,
    steps: [{ kind: 'command', name: '명령 실행', command: { run: '{{ command }}', timeout: 600_000_000_000 } }],
  },
}
const REBOOT = {
  ...BUILTINS[4],
  yaml: 'name: 재부팅 (필요할 때만)\n',
  task: {
    name: '재부팅 (필요할 때만)',
    targets: {},
    batch: 1,
    stop_on_failure: true,
    steps: [{ kind: 'reboot', name: '필요하면 재부팅', reboot: { only_if_required: true, wait: 600_000_000_000 } }],
  },
}
const TASK_DETAILS = { [DEPLOY_ID]: DEPLOY, 'builtin/command': COMMAND, 'builtin/reboot': REBOOT }

/** A run's head as GET /cluster/task-runs lists it. */
function runRecord(over: Record<string, unknown> = {}) {
  return {
    id: 'r-1', task_id: 'builtin/package-update', task_name: '패키지 업데이트', rev: 'rev-1',
    nodes: ['m-1', 'a-1'], waves: [['a-1'], ['m-1']], batch: 1, stop_on_failure: true,
    status: 'succeeded', started_by: 'admin', started_at: seen, ended_at: seen,
    ...over,
  }
}

interface Reply { status: number; body: unknown }
const ok = (data: unknown): Reply => ({ status: 200, body: { success: true, data } })
const fail = (status: number, code: string, message: string, details?: Record<string, string>): Reply =>
  ({ status, body: { success: false, error: { code, message, details } } })
/** An SSE body: every event a data: line. */
const sse = (...events: unknown[]) => events.map((e) => `data: ${JSON.stringify(e)}\n\n`).join('')

interface Fixture {
  /** GET /cluster/tasks. Default: the five built-ins, two of the operator's own, one from Git. */
  tasks?: unknown[]
  /** GET /cluster/tasks/:id, by task ID. Default: the listed task, as it stands. */
  taskDetails?: Record<string, unknown>
  /** GET /cluster/task-runs. Default: one ended run. */
  runs?: unknown[]
  /** GET /cluster/task-runs/:id, by run ID. */
  runDetails?: Record<string, unknown>
  /** GET /cluster/task-runs/:id/stream, by run ID: SSE bodies in order, the last one repeats. */
  streams?: Record<string, string[]>
  /** GET …/nodes/:node/steps/:n/output, by "<run>/<node>/<step>": answers in order, the last one repeats. */
  outputs?: Record<string, unknown[]>
  /** Any request, by "METHOD /path" (no query): answers in order, the last one repeats. Wins over everything above. */
  replies?: Record<string, Reply | Reply[]>
  /** /cluster/status answers {enabled: false}. */
  standalone?: boolean
}

interface Call { method: string; path: string; body: unknown }

/**
 * Mocks the panel's API for the Tasks tab. calls has every write (method,
 * path with its query, JSON body); requests every request, reads included.
 */
async function mock(page: Page, fixture: Fixture = {}) {
  const calls: Call[] = []
  const requests: string[] = []
  const counts = new Map<string, number>()
  const nth = <T>(key: string, list: T[]): T => {
    const n = counts.get(key) ?? 0
    counts.set(key, n + 1)
    return list[Math.min(n, list.length - 1)]
  }
  const tasks = fixture.tasks ?? TASKS
  const runs = fixture.runs ?? [runRecord()]

  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
  })
  await page.route('**/api/v1/**', async (route: Route) => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname.replace('/api/v1', '')
    const method = req.method()
    requests.push(`${method} ${path}${url.search}`)
    if (method !== 'GET') calls.push({ method, path: path + url.search, body: req.postData() ? req.postDataJSON() : null })

    const key = `${method} ${path}`
    const reply = fixture.replies?.[key]
    if (reply) {
      const r = Array.isArray(reply) ? nth(key, reply) : reply
      await route.fulfill({ status: r.status, json: r.body })
      return
    }
    const stream = path.match(/^\/cluster\/task-runs\/([^/]+)\/stream$/)
    if (stream) {
      const id = decodeURIComponent(stream[1])
      const bodies = fixture.streams?.[id]
      if (!bodies) {
        await route.fulfill({ status: 404, json: fail(404, 'TASK_RUN_NOT_FOUND', 'No such task run').body })
        return
      }
      await route.fulfill({ status: 200, headers: { 'Content-Type': 'text/event-stream' }, body: nth(key, bodies) })
      return
    }
    const output = path.match(/^\/cluster\/task-runs\/([^/]+)\/nodes\/([^/]+)\/steps\/(\d+)\/output$/)
    if (output) {
      const id = output.slice(1, 4).map(decodeURIComponent).join('/')
      const list = fixture.outputs?.[id]
      if (!list) {
        await route.fulfill({ status: 404, json: fail(404, 'TASK_RUN_NOT_FOUND', 'No such task run').body })
        return
      }
      await route.fulfill({ json: { success: true, data: nth(`output ${id}`, list) } })
      return
    }

    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/auth/ws-ticket') data = { ticket: 'fixture-ticket' }
    else if (path === '/cluster/status' && fixture.standalone) data = { enabled: false }
    else if (path === '/cluster/status') data = { enabled: true, name: 'lab', node_count: nodes.length, leader_id: 'm-1', local_id: 'm-1', is_leader: true }
    else if (path === '/cluster/nodes') data = { nodes, local_id: 'm-1', is_leader: true }
    else if (path === '/cluster/overview') data = { name: 'lab', node_count: nodes.length, leader_id: 'm-1', nodes, metrics: [] }
    else if (path === '/cluster/events') data = { events: [] }
    else if (path === '/cluster/tasks') data = tasks
    else if (path === '/cluster/task-runs') data = runs
    else if (path.startsWith('/cluster/tasks/') && method === 'GET') {
      const id = decodeURIComponent(path.slice('/cluster/tasks/'.length))
      const detail = fixture.taskDetails?.[id] ?? tasks.find((t) => (t as { id: string }).id === id)
      if (!detail) {
        await route.fulfill({ status: 404, json: fail(404, 'TASK_NOT_FOUND', 'No such task').body })
        return
      }
      data = detail
    } else if (path.startsWith('/cluster/task-runs/') && method === 'GET') {
      const id = decodeURIComponent(path.slice('/cluster/task-runs/'.length))
      const detail = fixture.runDetails?.[id]
      if (!detail) {
        await route.fulfill({ status: 404, json: fail(404, 'TASK_RUN_NOT_FOUND', 'No such task run').body })
        return
      }
      data = detail
    }
    await route.fulfill({ json: { success: true, data } })
  })
  const sockets: WebSocketRoute[] = []
  await page.routeWebSocket(/\/ws\//, (socket) => { sockets.push(socket) })
  return { calls, requests, sockets }
}

// ---------------------------------------------------------------- helpers --

/** The tab row above every cluster page. */
const tab = (page: Page, name: string) => page.getByRole('main').getByRole('link', { name, exact: true })
/** The task list: on a desktop, the left column. */
const list = (page: Page) => page.getByRole('navigation', { name: 'Task list' })
const section = (page: Page, name: string) => list(page).getByRole('region', { name, exact: true })
const items = (page: Page, name: string) => section(page, name).getByRole('link')

// ------------------------------------------------------------------ tests --

test('the Tasks tab comes fifth and opens /cluster/tasks', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/overview')
  const tasks = tab(page, 'Tasks')
  await expect(tasks).toBeVisible()
  const row = await tasks.locator('..').getByRole('link').allInnerTexts()
  expect(row.map((s) => s.trim())).toEqual(['Overview', 'Nodes', 'Docker', 'Security', 'Tasks'])
  await tasks.click()
  await expect(page).toHaveURL(/\/cluster\/tasks$/)
  await expect(tasks).toHaveAttribute('aria-current', 'page')
  await expect(list(page)).toBeVisible()
})

test('lists built-in, own and Git tasks apart, in English, and the search narrows them', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/tasks')
  await expect(items(page, 'Built-in')).toHaveText([
    'Package update', 'Package install', 'Service restart', 'Run a command', 'Reboot (only when required)',
  ])
  await expect(items(page, 'My tasks')).toHaveText(['Deploy web', 'Rotate logs'])
  // Git tasks sit under their repository.
  const git = section(page, 'Git')
  await expect(git.getByRole('region', { name: 'acme/ops' }).getByRole('link')).toHaveText(['Nightly backup'])
  // The server's Korean names never reach an English screen.
  await expect(list(page)).not.toContainText('패키지')
  // No run under way: no band.
  await expect(page.getByTestId('task-runs-active')).toHaveCount(0)

  const search = list(page).getByRole('searchbox', { name: 'Search tasks' })
  // Matched against the name on screen, not the server's.
  await search.fill('package')
  await expect(items(page, 'Built-in')).toHaveText(['Package update', 'Package install'])
  await expect(section(page, 'My tasks')).toHaveCount(0)
  await expect(section(page, 'Git')).toHaveCount(0)
  // And against the description.
  await search.fill('backup host')
  await expect(section(page, 'Built-in')).toHaveCount(0)
  await expect(section(page, 'Git').getByRole('link')).toHaveText(['Nightly backup'])
  await search.fill('no such thing')
  await expect(list(page)).toContainText('No tasks match')
})

test('a run under way shows a band that leads back to it', async ({ page }) => {
  await mock(page, {
    runs: [
      runRecord({ id: 'r-9', task_id: 'builtin/command', task_name: '명령 실행', status: 'running', ended_at: undefined }),
      runRecord({ id: 'r-8' }),
    ],
  })
  await page.goto('/cluster/tasks')
  const band = page.getByTestId('task-runs-active')
  await expect(band).toContainText('1 run under way')
  const link = band.getByRole('link', { name: 'Run a command' })
  await expect(link).toHaveAttribute('href', '/cluster/tasks/runs/r-9')
  await link.click()
  await expect(page).toHaveURL(/\/cluster\/tasks\/runs\/r-9$/)
  await expect(page.getByTestId('task-pane-run')).toBeVisible()
})

test('a built-in task opened from its link stays chosen across a reload', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/tasks')
  await section(page, 'Built-in').getByRole('link', { name: 'Run a command' }).click()
  // The ID's slash is escaped once.
  await expect(page).toHaveURL(/\/cluster\/tasks\/builtin%2Fcommand$/)
  const chosen = () => section(page, 'Built-in').getByRole('link', { name: 'Run a command' })
  await expect(chosen()).toHaveAttribute('aria-current', 'page')
  await expect(page.getByTestId('task-pane-setup')).toContainText('Run a command')

  await page.reload()
  await expect(chosen()).toHaveAttribute('aria-current', 'page')
  await expect(page.getByTestId('task-pane-setup')).toContainText('Run a command')
  await expect(page.getByTestId('task-pane-setup')).toContainText('Runs the command you type as root')
})

test('every tasks route has its pane', async ({ page }) => {
  await mock(page)
  for (const [path, pane] of [
    ['/cluster/tasks', 'task-pane-none'],
    ['/cluster/tasks/new', 'task-pane-editor'],
    ['/cluster/tasks/runs', 'task-pane-history'],
    ['/cluster/tasks/runs/r-1', 'task-pane-run'],
    ['/cluster/tasks/0a1b2c3d4e5f6071', 'task-pane-setup'],
    ['/cluster/tasks/0a1b2c3d4e5f6071/edit', 'task-pane-editor'],
  ]) {
    await page.goto(path)
    await expect(page.getByTestId(pane), path).toBeVisible()
    await expect(tab(page, 'Tasks'), path).toHaveAttribute('aria-current', 'page')
  }
  // The history link at the foot of the list is the chosen entry there.
  await expect(list(page).getByRole('link', { name: 'Run history' })).toHaveAttribute('href', '/cluster/tasks/runs')
  // A task that is not in the list says so.
  await page.goto('/cluster/tasks/ffffffffffffffff')
  await expect(page.getByTestId('task-pane-setup')).toContainText('No such task')
})

test('a node outside a cluster says so', async ({ page }) => {
  await mock(page, { standalone: true })
  await page.goto('/cluster/tasks')
  await expect(page.getByText('Cluster not enabled')).toBeVisible()
  await expect(list(page)).toHaveCount(0)
})

test.describe('Korean', () => {
  test.use({ locale: 'ko-KR' })

  test('names the tab, the sections, the built-ins and the band in Korean', async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('sfpanel_language', 'ko'))
    await mock(page, { runs: [runRecord({ id: 'r-9', status: 'running', ended_at: undefined })] })
    await page.goto('/cluster/tasks')
    await expect(tab(page, '작업')).toHaveAttribute('aria-current', 'page')
    const listKo = page.getByRole('navigation', { name: '작업 목록' })
    await expect(listKo.getByRole('region', { name: '내장', exact: true }).getByRole('link')).toHaveText([
      '패키지 업데이트', '패키지 설치', '서비스 재시작', '명령 실행', '재부팅 (필요할 때만)',
    ])
    await expect(listKo.getByRole('region', { name: '내 작업', exact: true })).toBeVisible()
    await expect(page.getByTestId('task-runs-active')).toContainText('실행 중 1건')
  })
})

// The tab row scrolls on a phone with its scrollbar hidden; the list becomes a
// picker at the top, as the More drawer's node picker is.
test.describe('phone', () => {
  test.use({ viewport: { width: 412, height: 860 }, hasTouch: true })

  test('the Tasks tab is scrolled into the row', async ({ page }) => {
    await mock(page)
    await page.goto('/cluster/tasks')
    const tasks = tab(page, 'Tasks')
    await expect(tasks).toHaveAttribute('aria-current', 'page')
    const row = tasks.locator('..')
    await expect.poll(async () => {
      const [t, r] = await Promise.all([tasks.boundingBox(), row.boundingBox()])
      if (!t || !r) return 'unmeasured'
      return t.x >= r.x - 0.5 && t.x + t.width <= r.x + r.width + 0.5 ? 'inside' : `tab ${t.x}..${t.x + t.width}, row ${r.x}..${r.x + r.width}`
    }).toBe('inside')
  })

  test('the list is a picker at the top', async ({ page }) => {
    await mock(page)
    await page.goto('/cluster/tasks/0a1b2c3d4e5f6071')
    const picker = page.getByRole('button', { name: /Deploy web/ })
    await expect(picker).toHaveAttribute('aria-expanded', 'false')
    // The desktop column is not there; the picker is folded.
    await expect(page.getByRole('navigation', { name: 'Task list' })).toHaveCount(0)

    await picker.click()
    await expect(picker).toHaveAttribute('aria-expanded', 'true')
    const pickerList = page.getByRole('navigation', { name: 'Task list' })
    await expect(pickerList).toBeVisible()
    await pickerList.getByRole('searchbox', { name: 'Search tasks' }).fill('command')
    await pickerList.getByRole('link', { name: 'Run a command' }).click()

    await expect(page).toHaveURL(/\/cluster\/tasks\/builtin%2Fcommand$/)
    const chosen = page.getByRole('button', { name: /Run a command/ })
    await expect(chosen).toHaveAttribute('aria-expanded', 'false')
    await expect(page.getByTestId('task-pane-setup')).toContainText('Run a command')
    const box = await chosen.boundingBox()
    expect(box && box.x >= 0 && box.x + box.width <= 412).toBe(true)
  })
})

// ------------------------------------------------- run setup and preview --

const pn = (id: string, over: Record<string, unknown> = {}) => {
  const n = nodes.find((x) => x.id === id)
  return { id, name: n?.name ?? id, kind: n?.kind ?? '', verdicts: [{ step: 0 }, { step: 1 }], ...over }
}
/** A preview's answer: waves of PlanNodes, the left-out ones, a plan_id. */
const planReply = (over: Record<string, unknown> = {}): Reply => ok({
  waves: [[pn('a-1'), pn('a-2')]], plan_id: 'p-1', ...over,
})
const started = (runId = '20261101-120000-abcdef'): Reply => ({ status: 202, body: { success: true, data: { run_id: runId } } })

const setup = (page: Page) => page.getByTestId('task-pane-setup')
const previewDialog = (page: Page) => page.getByRole('dialog')
const posts = (calls: Call[], path: string) => calls.filter((c) => c.method === 'POST' && c.path === path)

test.describe('run setup', () => {
  test('the preview shows the batches, the nodes left out and every verdict', async ({ page }) => {
    await mock(page, {
      taskDetails: TASK_DETAILS,
      replies: {
        'POST /cluster/task-runs/plan': planReply({
          waves: [
            [
              pn('a-1', { verdicts: [{ step: 0 }, { step: 1, refused: true, code: 'SERVICE_PROTECTED', reason: 'Refusing to restart nginx.service: the host depends on it' }] }),
              pn('a-2', { verdicts: undefined, check_error: 'context deadline exceeded' }),
            ],
            [pn('m-2', { verdicts: [{ step: 0, refused: true, code: 'COMPOSE_FORBIDDEN', reason: 'privileged: true is not allowed' }, { step: 1 }] })],
            [pn('m-1', { leader: true })],
          ],
          left: [
            pn('a-3', { verdicts: undefined, reason: 'busy', run_id: 'r-7' }),
            pn('m-3', { verdicts: undefined, reason: 'offline' }),
            { id: 'ghost', name: 'ghost', kind: '', reason: 'unknown' },
          ],
        }),
      },
    })
    await page.goto(`/cluster/tasks/${DEPLOY_ID}`)
    await expect(setup(page).getByTestId('task-target-count')).toHaveText('2 nodes targeted')
    await setup(page).getByLabel('Version').fill('1.2.3')
    await setup(page).getByRole('button', { name: 'Preview' }).click()

    const dialog = previewDialog(page)
    await expect(dialog.getByRole('heading', { name: 'Preview: Deploy web' })).toBeVisible()
    // The values as the run fills them: a default applied, a raw one marked.
    const inputs = dialog.getByTestId('plan-inputs')
    await expect(inputs).toContainText('Version')
    await expect(inputs).toContainText('1.2.3')
    await expect(inputs).toContainText('--pull')
    await expect(inputs).toContainText('(default)')
    await expect(inputs).toContainText('Goes in as typed')

    const waves = dialog.getByTestId('plan-wave')
    await expect(waves).toHaveCount(3)
    await expect(waves.nth(0)).toContainText('Batch 1')
    await expect(waves.nth(0)).toContainText('agent-01')
    await expect(waves.nth(0)).toContainText('agent-02')
    // A verdict code the panel words reads in the screen's words, with the step it is about.
    await expect(waves.nth(0)).toContainText('Restart nginx')
    await expect(waves.nth(0)).toContainText("The panel won't do this to this unit")
    await expect(waves.nth(0)).toContainText('Could not check this node: context deadline exceeded')
    // A code it has no words for shows the node's reason.
    await expect(waves.nth(1)).toContainText('manager-2')
    await expect(waves.nth(1)).toContainText('privileged: true is not allowed')
    await expect(waves.nth(2)).toContainText('manager-1')
    await expect(waves.nth(2)).toContainText('Leader')
    await expect(waves.nth(2)).toContainText('Every step can run')

    const left = dialog.getByTestId('plan-left')
    await expect(left.getByRole('listitem')).toHaveCount(3)
    await expect(left).toContainText('agent-03')
    await expect(left).toContainText('Held by another run')
    await expect(left.getByRole('link', { name: 'r-7' })).toHaveAttribute('href', '/cluster/tasks/runs/r-7')
    await expect(left).toContainText('manager-3')
    await expect(left).toContainText('Offline')
    await expect(left).toContainText('No such node')

    // Nothing to confirm: Run is there to press.
    await expect(dialog.getByRole('button', { name: 'Run' })).toBeEnabled()
  })

  test('a confirmed step goes to the start with the preview plan_id and the same inputs and targets', async ({ page }) => {
    const { calls } = await mock(page, {
      taskDetails: TASK_DETAILS,
      replies: {
        'POST /cluster/task-runs/plan': planReply({
          waves: [
            [pn('a-1'), pn('a-2')],
            [pn('m-2', { verdicts: [{ step: 0 }, { step: 1, needs_confirm: true, code: 'SERVICE_CONFIRM_REQUIRED', reason: 'Type nginx.service to confirm' }] })],
          ],
        }),
        'POST /cluster/task-runs': started(),
      },
    })
    await page.goto(`/cluster/tasks/${DEPLOY_ID}`)
    const pane = setup(page)
    await pane.getByLabel('Version').fill('1.2.3')
    // The task's group is chosen; managers join it.
    await expect(pane.getByRole('group', { name: 'Groups' }).getByRole('button', { name: 'web' })).toHaveAttribute('aria-pressed', 'true')
    await pane.getByRole('group', { name: 'Kinds' }).getByRole('button', { name: 'Managers' }).click()
    await expect(pane.getByTestId('task-target-count')).toHaveText('5 nodes targeted')
    await pane.getByRole('button', { name: 'Run' }).click()

    // A step to confirm: Run shows the preview first.
    const dialog = previewDialog(page)
    const confirm = dialog.getByRole('checkbox', { name: 'Confirm “Restart nginx” on every node' })
    await expect(confirm).toBeVisible()
    await expect(dialog).toContainText('This unit is needed to manage the host or run its guests')
    const run = dialog.getByRole('button', { name: 'Run' })
    await expect(run).toBeDisabled()
    expect(posts(calls, '/cluster/task-runs')).toHaveLength(0)
    await confirm.click()
    await run.click()

    await expect(page).toHaveURL(/\/cluster\/tasks\/runs\/20261101-120000-abcdef$/)
    const [plan] = posts(calls, '/cluster/task-runs/plan')
    expect(plan.body).toEqual({
      task_id: DEPLOY_ID,
      inputs: { version: '1.2.3' },
      targets: { groups: ['web'], kinds: ['manager'], nodes: [] },
      batch: 2,
      stop_on_failure: true,
    })
    const [start] = posts(calls, '/cluster/task-runs')
    expect(start.body).toEqual({ ...(plan.body as object), confirmed: [1], plan_id: 'p-1' })
  })

  test('a task with nothing to confirm starts at one press of Run', async ({ page }) => {
    const { calls } = await mock(page, {
      taskDetails: TASK_DETAILS,
      replies: { 'POST /cluster/task-runs/plan': planReply(), 'POST /cluster/task-runs': started('20261101-120001-000001') },
    })
    await page.goto('/cluster/tasks/builtin%2Fcommand')
    const pane = setup(page)
    // A built-in takes no node until some are chosen.
    await expect(pane.getByTestId('task-target-count')).toHaveText('0 nodes targeted')
    await expect(pane.getByRole('button', { name: 'Run' })).toBeDisabled()
    await pane.getByLabel('Command to run').fill('uptime')
    await pane.getByRole('checkbox', { name: 'agent-01' }).click()
    await pane.getByRole('checkbox', { name: 'agent-02' }).click()
    await expect(pane.getByTestId('task-target-count')).toHaveText('2 nodes targeted')
    await pane.getByRole('button', { name: 'Run' }).click()

    await expect(page).toHaveURL(/\/cluster\/tasks\/runs\/20261101-120001-000001$/)
    await expect(page.getByTestId('task-pane-run')).toBeVisible()
    await expect(previewDialog(page)).toHaveCount(0)
    const [start] = posts(calls, '/cluster/task-runs')
    expect(start.body).toEqual({
      task_id: 'builtin/command',
      inputs: { command: 'uptime' },
      targets: { groups: [], kinds: [], nodes: ['a-1', 'a-2'] },
      batch: 1,
      stop_on_failure: true,
      plan_id: 'p-1',
    })
  })

  test('a Proxmox host shows its reboot as skipped, and the run still starts', async ({ page }) => {
    const { calls } = await mock(page, {
      taskDetails: TASK_DETAILS,
      replies: {
        'POST /cluster/task-runs/plan': planReply({
          waves: [
            [pn('m-2', { verdicts: [{ step: 0, refused: true, code: 'TASK_REBOOT_PVE_HOST', reason: 'This is a Proxmox VE host' }] })],
            [pn('m-1', { leader: true, verdicts: [{ step: 0 }] })],
          ],
        }),
        'POST /cluster/task-runs': started(),
      },
    })
    await page.goto('/cluster/tasks/builtin%2Freboot')
    await setup(page).getByRole('group', { name: 'Kinds' }).getByRole('button', { name: 'Managers' }).click()
    await setup(page).getByRole('button', { name: 'Run' }).click()
    const wave = previewDialog(page).getByTestId('plan-wave').first()
    await expect(wave).toContainText('manager-2')
    await expect(wave).toContainText('Skipped (Proxmox host)')
    await previewDialog(page).getByRole('button', { name: 'Run' }).click()
    await expect(page).toHaveURL(/\/cluster\/tasks\/runs\//)
    expect(posts(calls, '/cluster/task-runs')[0].body).not.toHaveProperty('confirmed')
  })

  test('a plan changed since the preview is previewed again, and says why', async ({ page }) => {
    const confirmStep = { verdicts: [{ step: 0 }, { step: 1, needs_confirm: true, code: 'SERVICE_CONFIRM_REQUIRED' }] }
    const { calls } = await mock(page, {
      taskDetails: TASK_DETAILS,
      replies: {
        'POST /cluster/task-runs/plan': [
          planReply({ waves: [[pn('a-1', confirmStep)]], plan_id: 'p-1' }),
          planReply({ waves: [[pn('a-1', confirmStep), pn('a-2', confirmStep)]], plan_id: 'p-2' }),
        ],
        'POST /cluster/task-runs': [
          fail(409, 'TASK_PLAN_CHANGED', 'The task, its inputs or its targets changed since the preview'),
          started(),
        ],
      },
    })
    await page.goto(`/cluster/tasks/${DEPLOY_ID}`)
    await setup(page).getByLabel('Version').fill('1.2.3')
    await setup(page).getByRole('button', { name: 'Run' }).click()
    const dialog = previewDialog(page)
    const confirm = dialog.getByRole('checkbox', { name: 'Confirm “Restart nginx” on every node' })
    await confirm.click()
    await dialog.getByRole('button', { name: 'Run' }).click()

    await expect(dialog.getByTestId('plan-notice')).toHaveText(
      'The task, its inputs or its targets changed since the preview, so it was previewed again. Check it and run again.',
    )
    // The fresh preview: a second node, and the confirmation asked again.
    await expect(dialog.getByTestId('plan-wave').first()).toContainText('agent-02')
    await expect(confirm).not.toBeChecked()
    expect(posts(calls, '/cluster/task-runs/plan')).toHaveLength(2)
    await confirm.click()
    await dialog.getByRole('button', { name: 'Run' }).click()
    await expect(page).toHaveURL(/\/cluster\/tasks\/runs\//)
    const starts = posts(calls, '/cluster/task-runs')
    expect(starts.map((c) => (c.body as { plan_id: string }).plan_id)).toEqual(['p-1', 'p-2'])
  })

  test('a manager not ready keeps Run from starting and names it', async ({ page }) => {
    const { calls } = await mock(page, {
      taskDetails: TASK_DETAILS,
      replies: { 'POST /cluster/task-runs/plan': planReply({ managers_not_ready: ['manager-3 (offline)', 'manager-2 (0.79.0)'] }) },
    })
    await page.goto(`/cluster/tasks/${DEPLOY_ID}`)
    await setup(page).getByLabel('Version').fill('1.2.3')
    await setup(page).getByRole('button', { name: 'Run' }).click()
    const dialog = previewDialog(page)
    const waiting = dialog.getByTestId('plan-managers')
    await expect(waiting).toContainText('The run cannot start until every manager is online and runs a version that knows group tasks')
    await expect(waiting.getByRole('listitem')).toHaveText(['manager-3: offline', 'manager-2: runs 0.79.0, which does not know group tasks — update it'])
    await expect(dialog.getByRole('button', { name: 'Run' })).toBeDisabled()
    expect(posts(calls, '/cluster/task-runs')).toHaveLength(0)
  })

  test('a 413 with no envelope and a 503 read as the task screen words them', async ({ page }) => {
    await mock(page, {
      taskDetails: TASK_DETAILS,
      replies: {
        'POST /cluster/task-runs/plan': [{ status: 413, body: { message: 'Request Entity Too Large' } }, planReply()],
        'POST /cluster/task-runs': fail(503, 'INTERNAL_ERROR', 'No cluster leader available — retry in a few seconds'),
      },
    })
    await page.goto(`/cluster/tasks/${DEPLOY_ID}`)
    await setup(page).getByLabel('Version').fill('1.2.3')
    const error = setup(page).getByTestId('task-run-error')
    await setup(page).getByRole('button', { name: 'Run' }).click()
    await expect(error).toHaveText('The request is too large: the panel takes at most 1 MiB.')
    await setup(page).getByRole('button', { name: 'Run' }).click()
    await expect(error).toHaveText('The cluster leader is changing or catching up. Try again in a few seconds.')
    await expect(page).toHaveURL(new RegExp(`/cluster/tasks/${DEPLOY_ID}$`))
  })

  test('a node held by another run is named, not given by its ID', async ({ page }) => {
    await mock(page, {
      taskDetails: TASK_DETAILS,
      replies: {
        'POST /cluster/task-runs/plan': planReply(),
        'POST /cluster/task-runs': fail(409, 'TASK_TARGET_BUSY', 'Node a-2 is taken by run r-7', { node_id: 'a-2', run_id: 'r-7' }),
      },
    })
    await page.goto(`/cluster/tasks/${DEPLOY_ID}`)
    await setup(page).getByLabel('Version').fill('1.2.3')
    await setup(page).getByRole('button', { name: 'Run' }).click()
    await expect(setup(page).getByTestId('task-run-error')).toHaveText(
      'agent-02 is held by another run under way. Try again once that run has finished.',
    )
  })

  test('a document that no longer parses keeps the line the server named', async ({ page }) => {
    await mock(page, {
      taskDetails: TASK_DETAILS,
      replies: { 'POST /cluster/task-runs/plan': fail(400, 'TASK_INVALID', 'line 7: unknown step kind "pkgs"') },
    })
    await page.goto(`/cluster/tasks/${DEPLOY_ID}`)
    await setup(page).getByLabel('Version').fill('1.2.3')
    await setup(page).getByRole('button', { name: 'Preview' }).click()
    const error = setup(page).getByTestId('task-run-error')
    await expect(error).toContainText('The task document has problems.')
    await expect(error).toContainText('line 7: unknown step kind "pkgs"')
    await expect(previewDialog(page)).toHaveCount(0)
  })

  test('a required value left empty is asked for before the server is', async ({ page }) => {
    const { calls } = await mock(page, { taskDetails: TASK_DETAILS })
    await page.goto(`/cluster/tasks/${DEPLOY_ID}`)
    await setup(page).getByRole('button', { name: 'Preview' }).click()
    await expect(setup(page).getByTestId('task-input-version')).toContainText('This value is required.')
    expect(posts(calls, '/cluster/task-runs/plan')).toHaveLength(0)
    // The warning about what happens to the values sits above them.
    await expect(setup(page)).toContainText('Input values are kept in the run history and copied to every manager. Do not enter passwords.')
  })

  test('values render as text', async ({ page }) => {
    await mock(page, { taskDetails: TASK_DETAILS, replies: { 'POST /cluster/task-runs/plan': planReply() } })
    await page.goto(`/cluster/tasks/${DEPLOY_ID}`)
    await setup(page).getByLabel('Version').fill('<b>{{x}}</b>')
    await setup(page).getByRole('button', { name: 'Preview' }).click()
    const inputs = previewDialog(page).getByTestId('plan-inputs')
    await expect(inputs).toContainText('<b>{{x}}</b>')
    await expect(inputs.locator('b')).toHaveCount(0)
  })

  test('the five latest runs of this task are listed under it', async ({ page }) => {
    const mine = [1, 2, 3, 4, 5, 6].map((i) => runRecord({ id: `r-d${i}`, task_id: DEPLOY_ID, task_name: 'Deploy web', started_at: `2026-01-0${i}T00:00:00Z` }))
    await mock(page, {
      taskDetails: TASK_DETAILS,
      // Newest first, as the server lists them; another task's run among them.
      runs: [mine[5], runRecord({ id: 'r-other' }), mine[4], mine[3], { ...mine[2], status: 'failed' }, mine[1], mine[0]],
    })
    await page.goto(`/cluster/tasks/${DEPLOY_ID}`)
    const recent = setup(page).getByRole('region', { name: 'Recent runs' })
    const links = recent.getByRole('link')
    await expect(links).toHaveCount(5)
    await expect(links.first()).toHaveAttribute('href', '/cluster/tasks/runs/r-d6')
    await expect(links.nth(3)).toContainText('Failed')
    await expect(recent).not.toContainText('r-other')
  })

  test.describe('Korean', () => {
    test.use({ locale: 'ko-KR' })

    test('counts the targets in 대 and names the buttons', async ({ page }) => {
      await page.addInitScript(() => localStorage.setItem('sfpanel_language', 'ko'))
      await mock(page, { taskDetails: TASK_DETAILS })
      await page.goto(`/cluster/tasks/${DEPLOY_ID}`)
      await expect(setup(page).getByTestId('task-target-count')).toHaveText('대상 2대')
      await expect(setup(page).getByRole('button', { name: '미리보기' })).toBeVisible()
      await expect(setup(page).getByRole('button', { name: '실행', exact: true })).toBeVisible()
      await expect(setup(page)).toContainText('묶음 크기')
      await expect(setup(page)).toContainText('실패하면 멈춤')
    })
  })

  test.describe('phone', () => {
    test.use({ viewport: { width: 412, height: 860 }, hasTouch: true })

    test('the setup and its preview fit the screen', async ({ page }) => {
      await mock(page, { taskDetails: TASK_DETAILS, replies: { 'POST /cluster/task-runs/plan': planReply() } })
      await page.goto(`/cluster/tasks/${DEPLOY_ID}`)
      await setup(page).getByLabel('Version').fill('1.2.3')
      await setup(page).getByRole('button', { name: 'Preview' }).click()
      const run = previewDialog(page).getByRole('button', { name: 'Run' })
      await expect(run).toBeVisible()
      const box = await previewDialog(page).boundingBox()
      expect(box && box.x >= 0 && box.x + box.width <= 412).toBe(true)
    })
  })
})
