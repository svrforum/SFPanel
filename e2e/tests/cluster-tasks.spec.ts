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
  // The agent install: listed after the tasks, with no document behind it.
  builtin('agent-install', '에이전트 설치 (SSH)', 'SSH로 서버에 접속해 SFPanel 에이전트를 설치하고 클러스터에 등록합니다.'),
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
  /** GET /cluster/tasks. Default: the five built-ins and the agent install, two of the operator's own, one from Git. */
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
  /**
   * Any request, by "METHOD /path" (no query): answers in order, the last one
   * repeats, or a function asked at each request. Wins over everything above.
   */
  replies?: Record<string, Reply | Reply[] | (() => Reply)>
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
      const r = typeof reply === 'function' ? reply() : Array.isArray(reply) ? nth(key, reply) : reply
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
    'Package update', 'Package install', 'Service restart', 'Run a command', 'Reboot (only when required)', 'Agent install (SSH)',
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
    ['/cluster/tasks/builtin%2Fagent-install', 'task-pane-install'],
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
      '패키지 업데이트', '패키지 설치', '서비스 재시작', '명령 실행', '재부팅 (필요할 때만)', '에이전트 설치 (SSH)',
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

  test('the preview names the steps of the document it previewed, not the one the screen read', async ({ page }) => {
    // The document changed after the screen read it: a step stopping docker
    // now comes first, and the stack's step lost its name.
    await mock(page, {
      taskDetails: TASK_DETAILS,
      replies: {
        'POST /cluster/task-runs/plan': planReply({
          waves: [[pn('a-1', {
            verdicts: [
              { step: 0, needs_confirm: true, code: 'SERVICE_CONFIRM_REQUIRED' },
              { step: 1, refused: true, code: 'COMPOSE_FORBIDDEN', reason: 'privileged: true is not allowed' },
              { step: 2 },
            ],
          })]],
          steps: [{ name: 'Stop docker', kind: 'service' }, { kind: 'stack' }, { name: 'Restart nginx', kind: 'service' }],
        }),
      },
    })
    await page.goto(`/cluster/tasks/${DEPLOY_ID}`)
    await setup(page).getByLabel('Version').fill('1.2.3')
    await setup(page).getByRole('button', { name: 'Run' }).click()
    const dialog = previewDialog(page)
    await expect(dialog.getByRole('checkbox', { name: 'Confirm “Stop docker” on every node' })).toBeVisible()
    const wave = dialog.getByTestId('plan-wave').first()
    await expect(wave).toContainText('Stop docker: This unit is needed')
    await expect(wave).toContainText('Step 2: privileged: true is not allowed')
    await expect(dialog).not.toContainText('Update the web stack')
  })

  test('a start refused as changed reads the task again, then previews the new document', async ({ page }) => {
    // Edited through another manager: an input more, and a step in front.
    const CHANGED = {
      ...DEPLOY,
      updated_at: '2026-01-02T00:00:00Z',
      task: {
        ...DEPLOY.task,
        inputs: [...DEPLOY.task.inputs, { name: 'port', label: 'Port' }],
        steps: [{ kind: 'service', name: 'Stop docker', service: { name: 'docker.service', action: 'stop' } }, ...DEPLOY.task.steps],
      },
    }
    let changed = false
    let starts = 0
    const { requests, calls } = await mock(page, {
      replies: {
        [`GET /cluster/tasks/${DEPLOY_ID}`]: () => ok(changed ? CHANGED : DEPLOY),
        'POST /cluster/task-runs/plan': () => changed
          ? planReply({
            waves: [[pn('a-1', { verdicts: [{ step: 0, needs_confirm: true, code: 'SERVICE_CONFIRM_REQUIRED' }, { step: 1 }, { step: 2 }] })]],
            plan_id: 'p-2',
            steps: [{ name: 'Stop docker', kind: 'service' }, { name: 'Update the web stack', kind: 'stack' }, { name: 'Restart nginx', kind: 'service' }],
          })
          : planReply({ steps: RUN_STEPS }),
        'POST /cluster/task-runs': () => {
          starts += 1
          if (starts > 1) return started()
          changed = true
          return fail(409, 'TASK_PLAN_CHANGED', 'The task, its inputs or its targets changed since the preview')
        },
      },
    })
    await page.goto(`/cluster/tasks/${DEPLOY_ID}`)
    await setup(page).getByLabel('Version').fill('1.2.3')
    // A clean preview: Run starts it, and the start finds the document changed.
    await setup(page).getByRole('button', { name: 'Run' }).click()
    const dialog = previewDialog(page)
    await expect(dialog.getByTestId('plan-notice')).toBeVisible()
    await expect(dialog.getByRole('checkbox', { name: 'Confirm “Stop docker” on every node' })).toBeVisible()
    // The values the new document asks for, the one typed kept.
    await expect(dialog.getByTestId('plan-inputs')).toContainText('Port')
    await expect(dialog.getByTestId('plan-inputs')).toContainText('1.2.3')
    // The task was read again between the refused start and the new preview.
    const refused = requests.indexOf('POST /cluster/task-runs')
    const again = requests.lastIndexOf('POST /cluster/task-runs/plan')
    expect(refused).toBeGreaterThanOrEqual(0)
    expect(again).toBeGreaterThan(refused)
    expect(requests.slice(refused, again)).toContain(`GET /cluster/tasks/${DEPLOY_ID}`)
    const plans = posts(calls, '/cluster/task-runs/plan')
    expect(plans).toHaveLength(2)
    expect(plans[1].body).toEqual(plans[0].body)
  })

  test('a preview that answers after the operator moved to another task starts nothing and leaves the page be', async ({ page }) => {
    const { calls } = await mock(page, {
      taskDetails: TASK_DETAILS,
      replies: { 'POST /cluster/task-runs/plan': planReply(), 'POST /cluster/task-runs': started() },
    })
    // A large cluster: every node is asked, and the answer takes its time.
    await page.route('**/api/v1/cluster/task-runs/plan', async (route) => {
      await new Promise((resolve) => setTimeout(resolve, 1500))
      await route.fallback()
    })
    await page.goto(`/cluster/tasks/${DEPLOY_ID}`)
    await setup(page).getByLabel('Version').fill('1.2.3')
    const answered = page.waitForResponse('**/api/v1/cluster/task-runs/plan')
    await setup(page).getByRole('button', { name: 'Run' }).click()
    // A clean preview would start at once; the operator has moved on by then.
    await list(page).getByRole('link', { name: 'Rotate logs' }).click()
    await expect(page).toHaveURL(/\/cluster\/tasks\/1b2c3d4e5f607182$/)
    await answered
    await page.waitForTimeout(1000)
    expect(posts(calls, '/cluster/task-runs/plan')).toHaveLength(1)
    expect(posts(calls, '/cluster/task-runs')).toEqual([])
    await expect(page).toHaveURL(/\/cluster\/tasks\/1b2c3d4e5f607182$/)
    await expect(previewDialog(page)).toHaveCount(0)
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

  test('recent runs that did not load say why, not that the task has not run', async ({ page }) => {
    await mock(page, {
      taskDetails: TASK_DETAILS,
      replies: { 'GET /cluster/task-runs': fail(503, 'INTERNAL_ERROR', 'No cluster leader available — retry in a few seconds') },
    })
    await page.goto(`/cluster/tasks/${DEPLOY_ID}`)
    const recent = setup(page).getByRole('region', { name: 'Recent runs' })
    await expect(recent).toContainText('The cluster leader is changing or catching up. Try again in a few seconds.')
    await expect(recent).not.toContainText('This task has not run yet')
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

// ------------------------------------------------------- a run's progress --

// Deploy web on two agents and the leader: one batch of agents, then m-1.
const RUN_ID = '20261101-120000-abcdef'
const RUN_PATH = `/cluster/task-runs/${RUN_ID}`
const RUN_STEPS = [{ name: 'Update the web stack', kind: 'stack' }, { name: 'Restart nginx', kind: 'service' }]
const liveRun = (over: Record<string, unknown> = {}) => runRecord({
  id: RUN_ID, task_id: DEPLOY_ID, task_name: 'Deploy web', rev: 'rev-d',
  nodes: ['a-1', 'a-2', 'm-1'], waves: [['a-1', 'a-2'], ['m-1']], batch: 2,
  status: 'running', ended_at: undefined, ...over,
})
type Steps = Record<string, unknown>[]
const rec = (status: string, steps?: Steps, over: Record<string, unknown> = {}) => ({ status, ...(steps ? { steps } : {}), ...over })
const st = (status: string, over: Record<string, unknown> = {}) => ({ status, ...over })
const PENDING = { 'a-1': rec('pending'), 'a-2': rec('pending'), 'm-1': rec('pending') }
/** GET /cluster/task-runs/:id, which knows the step names. */
const runDetail = (run: unknown, nodeRecords: unknown, over: Record<string, unknown> = {}) => ({ run, nodes: nodeRecords, steps: RUN_STEPS, ...over })
const snapshot = (run: unknown, nodeRecords: unknown) => ({ type: 'snapshot', run, nodes: nodeRecords })
const nodeEvent = (id: string, record: unknown) => ({ type: 'node', node_id: id, record })

// The first batch under way: the stream's snapshot.
const FIRST_BATCH = {
  'a-1': rec('running', [st('running', { job_id: 'j-a1-0' }), st('pending')]),
  'a-2': rec('running', [st('succeeded', { job_id: 'j-a2-0', changed: true }), st('running', { job_id: 'j-a2-1' })]),
  'm-1': rec('pending'),
}
// What the node events after it make of the first batch, and the leader started.
const A1_DONE = rec('succeeded', [st('succeeded', { job_id: 'j-a1-0', changed: true }), st('succeeded', { job_id: 'j-a1-1' })])
const A2_DONE = rec('succeeded', [st('succeeded', { job_id: 'j-a2-0', changed: true }), st('succeeded', { job_id: 'j-a2-1', changed: true })])
const M1_RUNNING = rec('running', [st('running', { job_id: 'j-m1-0' }), st('pending')])
const M1_DONE = rec('succeeded', [st('succeeded', { job_id: 'j-m1-0', changed: true }), st('succeeded', { job_id: 'j-m1-1', changed: true })])
const SECOND_BATCH = { 'a-1': A1_DONE, 'a-2': A2_DONE, 'm-1': M1_RUNNING }
/** The stream while the second batch runs: a snapshot of the first, then node records; no end. */
const UNDER_WAY = sse(
  snapshot(liveRun(), FIRST_BATCH),
  nodeEvent('a-1', A1_DONE),
  nodeEvent('a-2', A2_DONE),
  nodeEvent('m-1', M1_RUNNING),
)
const FINISHED = sse(
  snapshot(liveRun(), SECOND_BATCH),
  nodeEvent('m-1', M1_DONE),
  { type: 'run', run: liveRun({ status: 'succeeded', ended_at: seen }) },
  { type: 'end' },
)

const runPane = (page: Page) => page.getByTestId('task-pane-run')
const matrix = (page: Page) => page.getByTestId('task-run-matrix')
/** A cell of the desktop table: node ID and step index. */
const cell = (page: Page, node: string, step: number) => matrix(page).locator(`[data-cell="${node}/${step}"]`)
const streamCount = (requests: string[]) => requests.filter((r) => r === `GET ${RUN_PATH}/stream`).length

test.describe('run progress', () => {
  test('the table follows the stream: a snapshot, node records, the head and its end', async ({ page }) => {
    const streams = { [RUN_ID]: [UNDER_WAY] }
    const { requests } = await mock(page, { runDetails: { [RUN_ID]: runDetail(liveRun(), PENDING) }, streams })
    await page.goto(`/cluster/tasks/runs/${RUN_ID}`)

    const pane = runPane(page)
    await expect(pane.getByRole('heading', { name: 'Deploy web' })).toBeVisible()
    await expect(pane.getByTestId('task-run-status')).toHaveText('Running')
    await expect(matrix(page).getByRole('columnheader')).toHaveText(['Node', 'Update the web stack', 'Restart nginx'])
    await expect(matrix(page).getByRole('rowheader')).toContainText(['agent-01', 'agent-02', 'manager-1'])
    // The node records after the snapshot are in the table: the snapshot alone
    // had agent-01 running and the first batch under way.
    await expect(cell(page, 'a-1', 0)).toHaveText('Succeeded')
    await expect(cell(page, 'a-1', 1)).toHaveText('No change')
    await expect(cell(page, 'm-1', 0)).toHaveText('Running')
    await expect(cell(page, 'm-1', 1)).toHaveText('Waiting')
    await expect(pane.getByTestId('task-run-wave')).toHaveText('Batch 2 of 2')

    // The run ends: the leader's record, the head, then end.
    streams[RUN_ID].push(FINISHED)
    await expect(pane.getByTestId('task-run-status')).toHaveText('Succeeded')
    await expect(cell(page, 'm-1', 1)).toHaveText('Succeeded')
    await expect(pane.getByTestId('task-run-wave')).toHaveCount(0)
    await expect(pane.getByTestId('task-run-counts')).toHaveText(/Succeeded\s*5.*No change\s*1/)
    // Nothing is opened again after the end.
    const opened = streamCount(requests)
    await page.waitForTimeout(2500)
    expect(streamCount(requests)).toBe(opened)
  })

  // The band and the history read the runs every ten seconds: a run this view
  // sees end is read again there at once, not left "under way" above it.
  test('the band lets go of a run as soon as its view sees it end', async ({ page }) => {
    const streams = { [RUN_ID]: [UNDER_WAY] }
    let ended = false
    await mock(page, {
      runDetails: { [RUN_ID]: runDetail(liveRun(), PENDING) },
      streams,
      replies: { 'GET /cluster/task-runs': () => ok([ended ? liveRun({ status: 'succeeded', ended_at: seen }) : liveRun()]) },
    })
    await page.goto(`/cluster/tasks/runs/${RUN_ID}`)
    const band = page.getByTestId('task-runs-active')
    await expect(band).toContainText('Deploy web')
    await expect(cell(page, 'm-1', 0)).toHaveText('Running')

    ended = true
    streams[RUN_ID].push(FINISHED)
    await expect(runPane(page).getByTestId('task-run-status')).toHaveText('Succeeded')
    await expect(band).toHaveCount(0, { timeout: 3000 })
  })

  test('a reload re-attaches to the same table, from a new snapshot', async ({ page }) => {
    // The GET is behind the stream: every node pending.
    const { requests } = await mock(page, {
      runDetails: { [RUN_ID]: runDetail(liveRun(), PENDING) },
      streams: { [RUN_ID]: [UNDER_WAY] },
    })
    await page.goto(`/cluster/tasks/runs/${RUN_ID}`)
    await expect(cell(page, 'a-1', 0)).toHaveText('Succeeded')

    const before = requests.length
    await page.reload()
    await expect.poll(() => requests.slice(before)).toContain(`GET ${RUN_PATH}/stream`)
    const after = requests.slice(before)
    // The run is read first (step names, a session refreshed), then followed.
    expect(after.indexOf(`GET ${RUN_PATH}`)).toBeGreaterThanOrEqual(0)
    expect(after.indexOf(`GET ${RUN_PATH}`)).toBeLessThan(after.indexOf(`GET ${RUN_PATH}/stream`))
    await expect(cell(page, 'a-1', 0)).toHaveText('Succeeded')
    await expect(cell(page, 'a-2', 1)).toHaveText('Succeeded')
    await expect(cell(page, 'm-1', 0)).toHaveText('Running')
    await expect(runPane(page).getByTestId('task-run-wave')).toHaveText('Batch 2 of 2')
  })

  test('the run as read shows before the stream answers', async ({ page }) => {
    // The stream will not open (a manager restarting); the GET has the run.
    await mock(page, {
      runDetails: { [RUN_ID]: runDetail(liveRun(), FIRST_BATCH) },
      replies: { [`GET ${RUN_PATH}/stream`]: fail(503, 'INTERNAL_ERROR', 'No cluster leader available — retry in a few seconds') },
    })
    await page.goto(`/cluster/tasks/runs/${RUN_ID}`)
    await expect(cell(page, 'a-2', 0)).toHaveText('Succeeded')
    await expect(cell(page, 'a-2', 1)).toHaveText('Running')
    await expect(runPane(page).getByTestId('task-run-wave')).toHaveText('Batch 1 of 2')
  })

  test('a stream that closes without an end is opened again and carries on', async ({ page }) => {
    const { requests } = await mock(page, {
      runDetails: { [RUN_ID]: runDetail(liveRun(), PENDING) },
      streams: { [RUN_ID]: [sse(snapshot(liveRun(), FIRST_BATCH)), FINISHED] },
    })
    await page.goto(`/cluster/tasks/runs/${RUN_ID}`)
    await expect(runPane(page).getByTestId('task-run-status')).toHaveText('Succeeded')
    await expect(cell(page, 'm-1', 0)).toHaveText('Succeeded')
    expect(streamCount(requests)).toBe(2)
    await page.waitForTimeout(2000)
    expect(streamCount(requests)).toBe(2)
  })

  test('a node chosen shows its output, taken on from next_skip until done', async ({ page }) => {
    const job = { id: 'j-a1-0', run_id: RUN_ID, kind: 'stack', changed: false, started_at: seen }
    const { requests } = await mock(page, {
      runDetails: { [RUN_ID]: runDetail(liveRun(), FIRST_BATCH) },
      streams: { [RUN_ID]: [sse(snapshot(liveRun(), FIRST_BATCH))] },
      outputs: {
        [`${RUN_ID}/a-1/0`]: [
          // next_skip is where the node's raw log stands, not the text's length.
          { ...job, status: 'running', output: 'pulling web\n', next_skip: 4096, done: false },
          { ...job, status: 'succeeded', output: 'web is up\n', next_skip: 8192, done: true },
        ],
      },
    })
    await page.goto(`/cluster/tasks/runs/${RUN_ID}`)
    await expect(cell(page, 'a-1', 0)).toHaveText('Running')
    await expect(page.getByTestId('task-node-output')).toHaveCount(0)
    // The node's name opens the step it is on.
    await matrix(page).getByRole('button', { name: 'agent-01', exact: true }).click()
    const output = page.getByTestId('task-node-output')
    await expect(output.getByRole('heading')).toHaveText('agent-01 · Update the web stack')
    await expect(output.getByTestId('task-node-output-text')).toHaveText('pulling web\nweb is up\n')
    const asked = () => requests.filter((r) => r.startsWith(`GET ${RUN_PATH}/nodes/a-1/steps/0/output`))
    expect(asked()).toEqual([`GET ${RUN_PATH}/nodes/a-1/steps/0/output?skip=0`, `GET ${RUN_PATH}/nodes/a-1/steps/0/output?skip=4096`])
    // done: nothing more is asked for.
    await page.waitForTimeout(2500)
    expect(asked()).toHaveLength(2)
  })

  test('Stop and Force stop ask first, and send force false and true', async ({ page }) => {
    const { calls } = await mock(page, {
      runDetails: { [RUN_ID]: runDetail(liveRun(), FIRST_BATCH) },
      streams: { [RUN_ID]: [sse(snapshot(liveRun(), FIRST_BATCH))] },
      replies: {
        [`POST ${RUN_PATH}/stop`]: [
          ok(liveRun({ status: 'stopping', reason: 'stopped by admin', stopped_by: 'admin' })),
          ok(liveRun({ status: 'stopping', reason: 'stopped by admin', stopped_by: 'admin', force: true })),
        ],
      },
    })
    await page.goto(`/cluster/tasks/runs/${RUN_ID}`)
    const pane = runPane(page)
    const dialog = page.getByRole('dialog')

    // Cancelled: nothing is sent.
    await pane.getByRole('button', { name: 'Stop', exact: true }).click()
    await expect(dialog).toContainText('No new step starts')
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    expect(posts(calls, `${RUN_PATH}/stop`)).toHaveLength(0)

    await pane.getByRole('button', { name: 'Stop', exact: true }).click()
    await dialog.getByRole('button', { name: 'Stop', exact: true }).click()
    await expect.poll(() => posts(calls, `${RUN_PATH}/stop`).length).toBe(1)
    expect(posts(calls, `${RUN_PATH}/stop`)[0].body).toEqual({ force: false })

    await pane.getByRole('button', { name: 'Force stop' }).click()
    await expect(dialog).toContainText('cancels the command and reboot jobs under way')
    await dialog.getByRole('button', { name: 'Force stop' }).click()
    await expect.poll(() => posts(calls, `${RUN_PATH}/stop`).length).toBe(2)
    expect(posts(calls, `${RUN_PATH}/stop`)[1].body).toEqual({ force: true })
  })

  test.describe('an ended run', () => {
    // a-2 failed; with stop on failure, the leader never ran.
    const FAILED_NODES = {
      'a-1': A1_DONE,
      'a-2': rec('failed', [st('succeeded', { job_id: 'j-a2-0', changed: true }), st('failed', { job_id: 'j-a2-1', summary: 'exit 1' })], { reason: 'Restart nginx failed' }),
      'm-1': rec('pending'),
    }
    const failedRun = liveRun({ status: 'failed', ended_at: seen, reason: 'agent-02 failed' })
    const NEW_RUN = '20261101-130000-123456'
    const fixture = (): Fixture => ({
      runDetails: { [RUN_ID]: runDetail(failedRun, FAILED_NODES) },
      streams: { [RUN_ID]: [sse(snapshot(failedRun, FAILED_NODES), { type: 'end' })] },
      replies: { [`POST ${RUN_PATH}/rerun`]: { status: 202, body: { success: true, data: { run_id: NEW_RUN } } } },
    })

    test('reruns the nodes that did not succeed with ?only=failed', async ({ page }) => {
      const { calls } = await mock(page, fixture())
      await page.goto(`/cluster/tasks/runs/${RUN_ID}`)
      const pane = runPane(page)
      await expect(pane.getByTestId('task-run-status')).toHaveText('Failed')
      await expect(pane.getByRole('button', { name: 'Stop', exact: true })).toHaveCount(0)
      await expect(pane.getByRole('button', { name: 'Force stop' })).toHaveCount(0)

      await pane.getByRole('button', { name: 'Rerun failed nodes only' }).click()
      const dialog = page.getByRole('dialog')
      await expect(dialog).toContainText('2 nodes')
      await dialog.getByRole('button', { name: 'Rerun', exact: true }).click()
      await expect(page).toHaveURL(new RegExp(`/cluster/tasks/runs/${NEW_RUN}$`))
      const reruns = calls.filter((c) => c.path.startsWith(`${RUN_PATH}/rerun`))
      expect(reruns).toEqual([{ method: 'POST', path: `${RUN_PATH}/rerun?only=failed`, body: null }])
    })

    test('reruns the nodes chosen', async ({ page }) => {
      const { calls } = await mock(page, fixture())
      await page.goto(`/cluster/tasks/runs/${RUN_ID}`)
      await runPane(page).getByRole('button', { name: 'Choose nodes to rerun' }).click()
      const dialog = page.getByRole('dialog')
      // The nodes that did not succeed are ticked to begin with.
      await expect(dialog.getByRole('checkbox', { name: 'agent-01' })).not.toBeChecked()
      await expect(dialog.getByRole('checkbox', { name: 'agent-02' })).toBeChecked()
      await expect(dialog.getByRole('checkbox', { name: 'manager-1' })).toBeChecked()
      await dialog.getByRole('checkbox', { name: 'manager-1' }).click()
      await dialog.getByRole('checkbox', { name: 'agent-01' }).click()
      await dialog.getByRole('button', { name: 'Rerun', exact: true }).click()
      await expect(page).toHaveURL(new RegExp(`/cluster/tasks/runs/${NEW_RUN}$`))
      const reruns = calls.filter((c) => c.path.startsWith(`${RUN_PATH}/rerun`))
      expect(reruns).toEqual([{ method: 'POST', path: `${RUN_PATH}/rerun`, body: { nodes: ['a-1', 'a-2'] } }])
    })
  })

  test('step headers: a built-in in the screen words, a run with no names as Step N', async ({ page }) => {
    const cmd = liveRun({ id: 'r-cmd', task_id: 'builtin/command', task_name: '명령 실행', nodes: ['a-1'], waves: [['a-1']] })
    const lost = liveRun({ id: 'r-lost' })
    const lostNodes = { 'a-1': rec('running', [st('succeeded'), st('running'), st('pending')]), 'a-2': rec('pending'), 'm-1': rec('pending') }
    await mock(page, {
      runDetails: {
        'r-cmd': { run: cmd, nodes: { 'a-1': rec('running', [st('running')]) }, steps: [{ name: '명령 실행', kind: 'command' }] },
        // The run's revision cannot be read: no steps, and the longest record counts them.
        'r-lost': { run: lost, nodes: lostNodes },
      },
      streams: {
        'r-cmd': [sse(snapshot(cmd, { 'a-1': rec('running', [st('running')]) }))],
        'r-lost': [sse(snapshot(lost, lostNodes))],
      },
    })
    await page.goto('/cluster/tasks/runs/r-cmd')
    await expect(runPane(page).getByRole('heading', { name: 'Run a command' })).toBeVisible()
    await expect(matrix(page).getByRole('columnheader')).toHaveText(['Node', 'Run the command'])
    await page.goto('/cluster/tasks/runs/r-lost')
    await expect(matrix(page).getByRole('columnheader')).toHaveText(['Node', 'Step 1', 'Step 2', 'Step 3'])
  })

  test('a run that is not there says so', async ({ page }) => {
    await mock(page)
    await page.goto('/cluster/tasks/runs/r-gone')
    await expect(runPane(page)).toContainText('No such run. It may have been cleared from the history.', { timeout: 10_000 })
  })

  test('a run this manager had not caught up with is read again for its step names', async ({ page }) => {
    // Read a moment too early (a run just started on the leader): the stream
    // finds it, and the run is read once more for the names of its steps.
    await mock(page, {
      replies: { [`GET ${RUN_PATH}`]: [fail(404, 'TASK_RUN_NOT_FOUND', 'No such task run'), ok(runDetail(liveRun(), FIRST_BATCH))] },
      streams: { [RUN_ID]: [UNDER_WAY] },
    })
    await page.goto(`/cluster/tasks/runs/${RUN_ID}`)
    await expect(matrix(page).getByRole('columnheader')).toHaveText(['Node', 'Update the web stack', 'Restart nginx'])
  })

  test.describe('Korean', () => {
    test.use({ locale: 'ko-KR' })

    test('names the batch, the cells and the buttons in Korean', async ({ page }) => {
      await page.addInitScript(() => localStorage.setItem('sfpanel_language', 'ko'))
      await mock(page, {
        runDetails: { [RUN_ID]: runDetail(liveRun(), PENDING) },
        streams: { [RUN_ID]: [UNDER_WAY] },
      })
      await page.goto(`/cluster/tasks/runs/${RUN_ID}`)
      const pane = runPane(page)
      await expect(pane.getByTestId('task-run-wave')).toHaveText('묶음 2/2')
      await expect(cell(page, 'a-1', 1)).toHaveText('변경 없음')
      await expect(cell(page, 'm-1', 1)).toHaveText('대기')
      await expect(pane.getByRole('button', { name: '중지', exact: true })).toBeVisible()
      await expect(pane.getByRole('button', { name: '강제 중지' })).toBeVisible()
    })
  })

  test.describe('phone', () => {
    test.use({ viewport: { width: 412, height: 860 }, hasTouch: true })

    test('nodes are cards, and a step tapped shows its output below', async ({ page }) => {
      const job = { id: 'j-a2-1', run_id: RUN_ID, kind: 'service', changed: false, started_at: seen }
      await mock(page, {
        runDetails: { [RUN_ID]: runDetail(liveRun(), FIRST_BATCH) },
        streams: { [RUN_ID]: [sse(snapshot(liveRun(), FIRST_BATCH))] },
        outputs: { [`${RUN_ID}/a-2/1`]: [{ ...job, status: 'running', output: 'restarting nginx\n', next_skip: 17, done: false }] },
      })
      await page.goto(`/cluster/tasks/runs/${RUN_ID}`)
      const cards = page.getByTestId('task-run-node-card')
      await expect(cards).toHaveCount(3)
      // The table is there, and hidden: an element not there yet would pass as hidden too.
      await expect(matrix(page)).toHaveCount(1)
      await expect(matrix(page)).toBeHidden()
      const card = cards.filter({ hasText: 'agent-02' })
      await expect(card).toContainText('Restart nginx')
      await card.getByRole('button', { name: /Restart nginx/ }).click()
      const output = page.getByTestId('task-node-output')
      await expect(output).toContainText('restarting nginx')
      const [o, c] = await Promise.all([output.boundingBox(), cards.last().boundingBox()])
      expect(o && c && o.y > c.y).toBe(true)
      expect(o && o.x >= 0 && o.x + o.width <= 412).toBe(true)
    })
  })
})

// ------------------------------------------------------------ the history --

// The clock the history's "when" is read against.
const NOW = new Date('2026-11-01T12:00:00Z')
const ago = (seconds: number) => new Date(NOW.getTime() - seconds * 1000).toISOString()
/** A run started `since` seconds before NOW that took `took` seconds; null: under way. */
const histRun = (id: string, since: number, took: number | null, over: Record<string, unknown> = {}) => runRecord({
  id, started_at: ago(since), ended_at: took === null ? undefined : ago(since - took), ...over,
})
// Newest first, as the server lists them.
const HISTORY = [
  histRun('r-5', 120, null, { task_id: DEPLOY_ID, task_name: 'Deploy web', nodes: ['a-1', 'a-2', 'm-1'], status: 'running' }),
  histRun('r-4', 3600, 95, { status: 'failed', started_by: 'ops' }),
  histRun('r-3', 86_400, 3725, { task_id: DEPLOY_ID, task_name: 'Deploy web', nodes: ['a-1'] }),
  histRun('r-2', 2 * 86_400, 42, { task_id: 'builtin/command', task_name: '명령 실행', nodes: ['a-1', 'a-2', 'a-3', 'a-4'], status: 'stopped' }),
]

const historyPane = (page: Page) => page.getByTestId('task-pane-history')
/** The desktop table's rows, the header row left out. */
const historyRows = (page: Page) => page.getByTestId('task-history-table').locator('tbody tr')
const cellsOf = (page: Page, i: number) => historyRows(page).nth(i).getByRole('cell')
const statusFilter = (page: Page) => historyPane(page).getByLabel('Filter by status')
const taskFilter = (page: Page) => historyPane(page).getByLabel('Filter by task')

test.describe('history', () => {
  test.beforeEach(async ({ page }) => {
    await page.clock.setFixedTime(NOW)
  })

  test('lists the runs newest first: status, task, nodes, who, when and how long', async ({ page }) => {
    await mock(page, { runs: HISTORY })
    await page.goto('/cluster/tasks/runs')
    await expect(historyPane(page).getByRole('heading', { name: 'Run history' })).toBeVisible()
    await expect(page.getByTestId('task-history-table').getByRole('columnheader'))
      .toHaveText(['Status', 'Task', 'Nodes', 'Started by', 'Started', 'Took'])
    await expect(historyRows(page)).toHaveCount(4)
    await expect(cellsOf(page, 0)).toHaveText(['Running', 'Deploy web', '3 nodes', 'admin', '2 minutes ago', 'Under way'])
    // A built-in in the screen's words, whatever name the run kept.
    await expect(cellsOf(page, 1)).toHaveText(['Failed', 'Package update', '2 nodes', 'ops', '1 hour ago', '1m 35s'])
    await expect(cellsOf(page, 2)).toHaveText(['Succeeded', 'Deploy web', '1 node', 'admin', '1 day ago', '1h 2m'])
    await expect(cellsOf(page, 3)).toHaveText(['Stopped', 'Run a command', '4 nodes', 'admin', '2 days ago', '42s'])
    await expect(historyPane(page)).not.toContainText('패키지')
  })

  test('narrows the list by status and by task', async ({ page }) => {
    await mock(page, { runs: HISTORY })
    await page.goto('/cluster/tasks/runs')
    await expect(historyRows(page)).toHaveCount(4)
    await expect(statusFilter(page).locator('option'))
      .toHaveText(['All statuses', 'Under way', 'Succeeded', 'Failed', 'Stopped'])
    // Each task the history holds, once, in the screen's words.
    await expect(taskFilter(page).locator('option'))
      .toHaveText(['All tasks', 'Deploy web', 'Package update', 'Run a command'])

    await statusFilter(page).selectOption({ label: 'Failed' })
    await expect(historyRows(page)).toHaveCount(1)
    await expect(cellsOf(page, 0).nth(1)).toHaveText('Package update')
    await statusFilter(page).selectOption({ label: 'Under way' })
    await expect(historyRows(page)).toHaveCount(1)
    await expect(cellsOf(page, 0).first()).toHaveText('Running')

    await statusFilter(page).selectOption({ label: 'All statuses' })
    await taskFilter(page).selectOption({ label: 'Deploy web' })
    await expect(historyRows(page).locator('td:nth-child(5)')).toHaveText(['2 minutes ago', '1 day ago'])
    await statusFilter(page).selectOption({ label: 'Succeeded' })
    await expect(historyRows(page)).toHaveCount(1)
    await expect(cellsOf(page, 0).nth(4)).toHaveText('1 day ago')
    await statusFilter(page).selectOption({ label: 'Stopped' })
    await expect(historyPane(page)).toContainText('No runs match these filters.')
    await expect(historyRows(page)).toHaveCount(0)
  })

  test('a row leads to that run, which ends at once when it has ended', async ({ page }) => {
    const ended = HISTORY[1]
    const endedNodes = { 'a-1': rec('failed', [st('failed', { job_id: 'j-1' })]), 'm-1': rec('pending') }
    const { requests } = await mock(page, {
      runs: HISTORY,
      runDetails: { 'r-4': { run: ended, nodes: endedNodes, steps: [{ name: 'Upgrade the packages', kind: 'package' }] } },
      streams: { 'r-4': [sse(snapshot(ended, endedNodes), { type: 'end' })] },
    })
    await page.goto('/cluster/tasks/runs')
    const link = historyRows(page).nth(1).getByRole('link')
    await expect(link).toHaveAttribute('href', '/cluster/tasks/runs/r-4')
    // Anywhere on the row will do, not just the name.
    await cellsOf(page, 1).nth(3).click()
    await expect(page).toHaveURL(/\/cluster\/tasks\/runs\/r-4$/)
    await expect(runPane(page).getByTestId('task-run-status')).toHaveText('Failed')
    await expect(cell(page, 'a-1', 0)).toHaveText('Failed')
    await expect(runPane(page).getByRole('button', { name: 'Rerun failed nodes only' })).toBeVisible()
    // The snapshot and its end are all there is: nothing is opened again.
    const streams = () => requests.filter((r) => r === 'GET /cluster/task-runs/r-4/stream').length
    const opened = streams()
    expect(opened).toBeGreaterThan(0)
    await page.waitForTimeout(2500)
    expect(streams()).toBe(opened)
  })

  test('shows 50 runs to a page, and a filter starts again from the first', async ({ page }) => {
    // 120 runs a minute apart, newest first; every third one failed.
    const many = Array.from({ length: 120 }, (_, i) => histRun(`r-${String(120 - i).padStart(3, '0')}`, 60 * (i + 1), 10, {
      status: i % 3 === 2 ? 'failed' : 'succeeded',
    }))
    await mock(page, { runs: many })
    await page.goto('/cluster/tasks/runs')
    const link = (i: number) => historyRows(page).nth(i).getByRole('link')
    await expect(historyRows(page)).toHaveCount(50)
    await expect(link(0)).toHaveAttribute('href', '/cluster/tasks/runs/r-120')
    await expect(historyPane(page)).toContainText('Page 1 / 3')
    const next = historyPane(page).getByRole('button', { name: 'Next' })
    await next.click()
    await expect(historyPane(page)).toContainText('Page 2 / 3')
    await expect(link(0)).toHaveAttribute('href', '/cluster/tasks/runs/r-070')
    await next.click()
    await expect(historyRows(page)).toHaveCount(20)
    await expect(link(19)).toHaveAttribute('href', '/cluster/tasks/runs/r-001')
    await expect(next).toBeDisabled()

    // From page 3, a filter starts on the first page of what it keeps: 80
    // succeeded runs, two pages.
    await statusFilter(page).selectOption({ label: 'Succeeded' })
    await expect(historyPane(page)).toContainText('Page 1 / 2')
    await expect(link(0)).toHaveAttribute('href', '/cluster/tasks/runs/r-120')
    // 40 failed runs fit one page: no pager.
    await statusFilter(page).selectOption({ label: 'Failed' })
    await expect(historyRows(page)).toHaveCount(40)
    await expect(historyPane(page)).not.toContainText('Page ')
    await expect(link(0)).toHaveAttribute('href', '/cluster/tasks/runs/r-118')
  })

  test('says when there is nothing yet, and when the list does not load', async ({ page }) => {
    const replies: Record<string, Reply> = { 'GET /cluster/task-runs': fail(503, 'INTERNAL_ERROR', 'leader changed') }
    const { requests } = await mock(page, { replies })
    await page.goto('/cluster/tasks/runs')
    await expect(historyPane(page)).toContainText('Could not load the run history')
    // A 503 in the task screen's words, not the server's.
    await expect(historyPane(page)).toContainText('The cluster leader is changing or catching up.')
    await expect(historyPane(page)).not.toContainText('leader changed')
    const before = requests.filter((r) => r === 'GET /cluster/task-runs').length
    replies['GET /cluster/task-runs'] = ok([])
    await historyPane(page).getByRole('button', { name: 'Retry' }).click()
    await expect(historyPane(page)).toContainText('No runs yet.')
    expect(requests.filter((r) => r === 'GET /cluster/task-runs').length).toBeGreaterThan(before)
  })

  test('task names and who started a run render as text', async ({ page }) => {
    await mock(page, { runs: [histRun('r-x', 60, 5, { task_id: '3d4e5f6071829304', task_name: '<b>{{x}}</b>', started_by: '<i>ops</i>' })] })
    await page.goto('/cluster/tasks/runs')
    await expect(cellsOf(page, 0).nth(1)).toHaveText('<b>{{x}}</b>')
    await expect(cellsOf(page, 0).nth(3)).toHaveText('<i>ops</i>')
    await expect(historyPane(page).locator('b, i')).toHaveCount(0)
  })

  test.describe('Korean', () => {
    test.use({ locale: 'ko-KR' })

    test('counts the nodes in 대 and words the rest in Korean', async ({ page }) => {
      await page.addInitScript(() => localStorage.setItem('sfpanel_language', 'ko'))
      await mock(page, { runs: HISTORY })
      await page.goto('/cluster/tasks/runs')
      await expect(page.getByTestId('task-history-table').getByRole('columnheader'))
        .toHaveText(['상태', '작업', '대상', '시작한 사람', '시작', '걸린 시간'])
      await expect(cellsOf(page, 0)).toHaveText(['진행 중', 'Deploy web', '3대', 'admin', '2분 전', '진행 중'])
      await expect(cellsOf(page, 1)).toHaveText(['실패', '패키지 업데이트', '2대', 'ops', '1시간 전', '1분 35초'])
      await expect(cellsOf(page, 2).last()).toHaveText('1시간 2분')
      await expect(statusFilter(page)).toHaveCount(0)
      await expect(historyPane(page).getByLabel('상태로 거르기').locator('option'))
        .toHaveText(['모든 상태', '진행 중', '성공', '실패', '중지됨'])
    })
  })

  test.describe('phone', () => {
    test.use({ viewport: { width: 412, height: 860 }, hasTouch: true })

    test('runs are cards, and a card tapped opens its run', async ({ page }) => {
      const ended = HISTORY[1]
      await mock(page, {
        runs: HISTORY,
        runDetails: { 'r-4': { run: ended, nodes: {} } },
        streams: { 'r-4': [sse(snapshot(ended, {}), { type: 'end' })] },
      })
      await page.goto('/cluster/tasks/runs')
      const cards = page.getByTestId('task-history-card')
      await expect(cards).toHaveCount(4)
      // The table is there, and hidden.
      await expect(page.getByTestId('task-history-table')).toHaveCount(1)
      await expect(page.getByTestId('task-history-table')).toBeHidden()
      const card = cards.nth(1)
      await expect(card).toContainText('Package update')
      await expect(card).toContainText('2 nodes')
      await expect(card).toContainText('1m 35s')
      const box = await card.boundingBox()
      expect(box && box.x >= 0 && box.x + box.width <= 412).toBe(true)
      await card.tap()
      await expect(page).toHaveURL(/\/cluster\/tasks\/runs\/r-4$/)
      await expect(runPane(page).getByTestId('task-run-status')).toHaveText('Failed')
    })
  })
})

// ----------------------------------------------------------------- editor --

// The editor holds one document in two views: step cards (the model validate
// returns) and YAML (Monaco). The web has no YAML parser: cards become YAML
// through POST /cluster/tasks/format, YAML becomes cards through validate.
test.describe('editor', () => {
  const GIT_ID = '2c3d4e5f60718293'
  const editorPane = (page: Page) => page.getByTestId('task-pane-editor')
  const viewButton = (page: Page, name: 'Form' | 'YAML') => editorPane(page).getByRole('button', { name, exact: true })
  const problemList = (page: Page) => editorPane(page).getByRole('region', { name: 'Problems in the document' })
  /** The editor's line n (from 1), as Monaco draws its decorations. */
  const editorLine = (page: Page, n: number) => editorPane(page).locator('.view-overlays > div').nth(n - 1)
  const valid = (task: unknown): Reply => ok({ valid: true, problems: [], task })

  test('a new task built from cards is formatted, validated, then created from that YAML', async ({ page }) => {
    const id = '3d4e5f6071829304'
    const yaml = 'name: Check disk\nsteps:\n  - command:\n      run: df -h\n  - reboot: {}\n'
    const model = {
      name: 'Check disk', targets: {}, batch: 1, stop_on_failure: true,
      steps: [
        { kind: 'command', command: { run: 'df -h', timeout: 600_000_000_000 } },
        { kind: 'reboot', reboot: { only_if_required: true, wait: 600_000_000_000 } },
      ],
    }
    const tasks: unknown[] = [...TASKS]
    const { calls } = await mock(page, {
      tasks,
      replies: {
        'POST /cluster/tasks/format': ok({ yaml }),
        'POST /cluster/tasks/validate': valid(model),
        'POST /cluster/tasks': ok({ id, name: 'Check disk', source: { kind: 'user' }, read_only: false, updated_at: seen, updated_by: 'admin', yaml, task: model }),
      },
    })
    await page.goto('/cluster/tasks')
    await list(page).getByRole('link', { name: 'New task' }).click()
    await expect(page).toHaveURL(/\/cluster\/tasks\/new$/)
    const editor = editorPane(page)
    await expect(viewButton(page, 'Form')).toHaveAttribute('aria-pressed', 'true')
    await editor.getByLabel('Task name').fill('Check disk')
    await editor.getByRole('button', { name: 'Add step' }).click()
    await page.getByRole('menuitem', { name: 'Command' }).click()
    await editor.getByTestId('task-step-0').getByLabel('Command', { exact: true }).fill('df -h')
    await editor.getByRole('button', { name: 'Add step' }).click()
    await page.getByRole('menuitem', { name: 'Reboot' }).click()
    await expect(editor.getByTestId('task-step-1').getByLabel('Only when the node needs it')).toBeChecked()

    // The list read after the save has the new task.
    tasks.push({ id, name: 'Check disk', source: { kind: 'user' }, read_only: false, updated_at: seen, updated_by: 'admin' })
    await editor.getByRole('button', { name: 'Save', exact: true }).click()
    await expect(page).toHaveURL(new RegExp(`/cluster/tasks/${id}$`))
    // Defaults the server's format leaves out still go: stop on failure, and
    // a reboot only when required, are true.
    expect(calls).toEqual([
      { method: 'POST', path: '/cluster/tasks/format', body: { task: model } },
      { method: 'POST', path: '/cluster/tasks/validate', body: { yaml } },
      { method: 'POST', path: '/cluster/tasks', body: { yaml } },
    ])
    await expect(section(page, 'My tasks').getByRole('link', { name: 'Check disk' })).toHaveAttribute('aria-current', 'page')
    await expect(page.getByTestId('task-pane-setup')).toContainText('Check disk')
  })

  test('a YAML with problems shows them listed and on their line, and is not saved', async ({ page }) => {
    // No newline at the end: Monaco would indent the line after it.
    const bad = 'name: Bad\nsteps:\n  - shell: { run: ls }'
    const { calls } = await mock(page, {
      replies: {
        'POST /cluster/tasks/format': ok({ yaml: 'name: ""\nsteps: []\n' }),
        'POST /cluster/tasks/validate': ok({ valid: false, task: null, problems: [{ line: 3, message: 'unknown step kind "shell"' }] }),
      },
    })
    await page.goto('/cluster/tasks/new')
    const editor = editorPane(page)
    await viewButton(page, 'YAML').click()
    await expect(editor.locator('.view-lines')).toContainText('steps: []')
    await editor.locator('.monaco-editor').click()
    await page.keyboard.press('Control+a')
    await page.keyboard.insertText(bad)
    await editor.getByRole('button', { name: 'Save', exact: true }).click()

    await expect(problemList(page).getByRole('button')).toHaveText(['Line 3: unknown step kind "shell"'])
    await expect(editorLine(page, 3).locator('.squiggly-error')).toHaveCount(1)
    await expect(editor.locator('.view-overlays .squiggly-error')).toHaveCount(1)
    expect(calls).toEqual([
      { method: 'POST', path: '/cluster/tasks/format', body: { task: { name: '', targets: {}, batch: 1, stop_on_failure: true, steps: [] } } },
      { method: 'POST', path: '/cluster/tasks/validate', body: { yaml: bad } },
    ])
    // A problem chosen puts the cursor on its line, from wherever it was.
    await editor.locator('.monaco-editor').click()
    await page.keyboard.press('Control+Home')
    await expect(editorLine(page, 1).locator('.current-line')).toHaveCount(1)
    await problemList(page).getByRole('button').click()
    await expect(editorLine(page, 3).locator('.current-line')).toHaveCount(1)
  })

  test('problems found on saving the cards are listed there and lead to their line in YAML', async ({ page }) => {
    const yaml = 'name: ""\nsteps: []\n'
    const { calls } = await mock(page, {
      replies: {
        'POST /cluster/tasks/format': ok({ yaml }),
        'POST /cluster/tasks/validate': ok({
          valid: false, task: null,
          problems: [{ line: 1, message: 'name is required' }, { line: 2, message: 'a task needs at least one step' }],
        }),
      },
    })
    await page.goto('/cluster/tasks/new')
    await editorPane(page).getByRole('button', { name: 'Save', exact: true }).click()
    await expect(problemList(page).getByRole('button')).toHaveText(['Line 1: name is required', 'Line 2: a task needs at least one step'])
    await expect(viewButton(page, 'Form')).toHaveAttribute('aria-pressed', 'true')
    expect(calls.map((c) => `${c.method} ${c.path}`)).toEqual(['POST /cluster/tasks/format', 'POST /cluster/tasks/validate'])

    await problemList(page).getByRole('button', { name: /^Line 2/ }).click()
    await expect(viewButton(page, 'YAML')).toHaveAttribute('aria-pressed', 'true')
    await expect(editorPane(page).locator('.view-lines')).toContainText('steps: []')
    await expect(editorLine(page, 2).locator('.squiggly-error')).toHaveCount(1)
    await expect(editorLine(page, 2).locator('.current-line')).toHaveCount(1)
  })

  test('a YAML with comments asks before it becomes cards', async ({ page }) => {
    const yaml = '# rolls the web stack\nname: Deploy web\n'
    const { calls } = await mock(page, {
      taskDetails: { [DEPLOY_ID]: { ...DEPLOY, yaml } },
      replies: { 'POST /cluster/tasks/validate': valid(DEPLOY.task) },
    })
    await page.goto(`/cluster/tasks/${DEPLOY_ID}/edit`)
    const editor = editorPane(page)
    // A document with comments opens as it is written.
    await expect(viewButton(page, 'YAML')).toHaveAttribute('aria-pressed', 'true')
    await expect(editor.locator('.view-lines')).toContainText('# rolls the web stack')

    await viewButton(page, 'Form').click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toContainText('Comments and formatting will be lost')
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(dialog).toHaveCount(0)
    await expect(viewButton(page, 'YAML')).toHaveAttribute('aria-pressed', 'true')
    expect(calls).toEqual([])

    await viewButton(page, 'Form').click()
    await page.getByRole('dialog').getByRole('button', { name: 'Switch to the form' }).click()
    await expect(editor.getByLabel('Task name')).toHaveValue('Deploy web')
    expect(calls).toEqual([{ method: 'POST', path: '/cluster/tasks/validate', body: { yaml } }])
  })

  test("an operator's own task is changed with PUT, and its pane is shown again", async ({ page }) => {
    const yaml = 'name: Deploy web\ndescription: Rolls the web stack\n'
    const { calls } = await mock(page, {
      taskDetails: TASK_DETAILS,
      replies: {
        'POST /cluster/tasks/format': ok({ yaml }),
        'POST /cluster/tasks/validate': valid({ ...DEPLOY.task, description: 'Rolls the web stack' }),
        [`PUT /cluster/tasks/${DEPLOY_ID}`]: ok({ ...DEPLOY, yaml }),
      },
    })
    await page.goto(`/cluster/tasks/${DEPLOY_ID}`)
    const pane = page.getByTestId('task-pane-setup')
    await pane.getByRole('link', { name: 'Edit', exact: true }).click()
    await expect(page).toHaveURL(new RegExp(`/cluster/tasks/${DEPLOY_ID}/edit$`))
    const editor = editorPane(page)
    await expect(viewButton(page, 'Form')).toHaveAttribute('aria-pressed', 'true')
    await expect(editor.getByLabel('Task name')).toHaveValue('Deploy web')
    await editor.getByLabel('Description').fill('Rolls the web stack')
    await editor.getByRole('button', { name: 'Save', exact: true }).click()
    // A stored task is changed in place, not created again.
    await expect.poll(() => calls.map((c) => `${c.method} ${c.path}`)).toEqual([
      'POST /cluster/tasks/format', 'POST /cluster/tasks/validate', `PUT /cluster/tasks/${DEPLOY_ID}`,
    ])
    await expect(page).toHaveURL(new RegExp(`/cluster/tasks/${DEPLOY_ID}$`))
    // Everything the cards did not touch goes back as it came.
    expect(calls).toEqual([
      { method: 'POST', path: '/cluster/tasks/format', body: { task: { ...DEPLOY.task, description: 'Rolls the web stack' } } },
      { method: 'POST', path: '/cluster/tasks/validate', body: { yaml } },
      { method: 'PUT', path: `/cluster/tasks/${DEPLOY_ID}`, body: { yaml } },
    ])
  })

  test('Delete asks first, then sends DELETE and goes back to the list', async ({ page }) => {
    const tasks: { id: string }[] = [...TASKS]
    const { calls } = await mock(page, { tasks })
    await page.goto(`/cluster/tasks/${GIT_ID}`)
    const pane = page.getByTestId('task-pane-setup')
    await pane.getByRole('button', { name: 'Delete', exact: true }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toContainText('Delete Nightly backup?')
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(dialog).toHaveCount(0)
    expect(calls).toEqual([])

    await pane.getByRole('button', { name: 'Delete', exact: true }).click()
    tasks.splice(tasks.findIndex((x) => x.id === GIT_ID), 1)
    await page.getByRole('dialog').getByRole('button', { name: 'Delete', exact: true }).click()
    await expect(page).toHaveURL(/\/cluster\/tasks$/)
    expect(calls).toEqual([{ method: 'DELETE', path: `/cluster/tasks/${GIT_ID}`, body: null }])
    // The list is read again, without it.
    await expect(section(page, 'Git')).toHaveCount(0)
  })

  // The browser's manager is a follower: a write goes on to the leader, and
  // this manager has it a second or two later. The page waits for the list
  // to show the write before it moves on.
  test('a task saved through a follower is waited for: the page lands on it, and the list has it', async ({ page }) => {
    const id = '3d4e5f6071829304'
    const yaml = 'name: Check disk\nsteps:\n  - command:\n      run: df -h\n'
    const model = { name: 'Check disk', targets: {}, batch: 1, stop_on_failure: true, steps: [{ kind: 'command', command: { run: 'df -h', timeout: 600_000_000_000 } }] }
    const created = { id, name: 'Check disk', source: { kind: 'user' }, read_only: false, updated_at: '2026-01-02T00:00:00Z', updated_by: 'admin' }
    let saved = false
    let readsAfter = 0
    await mock(page, {
      replies: {
        'GET /cluster/tasks': () => {
          if (!saved) return ok(TASKS)
          readsAfter += 1
          return ok(readsAfter >= 2 ? [...TASKS, created] : TASKS)
        },
        [`GET /cluster/tasks/${id}`]: ok({ ...created, yaml, task: model }),
        'POST /cluster/tasks/format': ok({ yaml }),
        'POST /cluster/tasks/validate': valid(model),
        'POST /cluster/tasks': () => {
          saved = true
          return ok({ ...created, yaml, task: model })
        },
      },
    })
    await page.goto('/cluster/tasks/new')
    const editor = editorPane(page)
    await editor.getByLabel('Task name').fill('Check disk')
    await editor.getByRole('button', { name: 'Add step' }).click()
    await page.getByRole('menuitem', { name: 'Command' }).click()
    await editor.getByTestId('task-step-0').getByLabel('Command', { exact: true }).fill('df -h')
    await editor.getByRole('button', { name: 'Save', exact: true }).click()
    await expect(page).toHaveURL(new RegExp(`/cluster/tasks/${id}$`))
    const pane = page.getByTestId('task-pane-setup')
    await expect(pane.getByRole('heading', { name: 'Check disk' })).toBeVisible()
    await expect(pane).not.toContainText('No such task')
    await expect(section(page, 'My tasks').getByRole('link', { name: 'Check disk' })).toHaveAttribute('aria-current', 'page')
    expect(readsAfter).toBe(2)
  })

  test('a task deleted through a follower is waited for: it leaves the list without a reload', async ({ page }) => {
    const ID = '1b2c3d4e5f607182'
    let deleted = false
    let readsAfter = 0
    await mock(page, {
      replies: {
        'GET /cluster/tasks': () => {
          if (!deleted) return ok(TASKS)
          readsAfter += 1
          return ok(readsAfter >= 2 ? TASKS.filter((x) => x.id !== ID) : TASKS)
        },
        [`DELETE /cluster/tasks/${ID}`]: () => {
          deleted = true
          return ok({})
        },
      },
    })
    await page.goto(`/cluster/tasks/${ID}`)
    await page.getByTestId('task-pane-setup').getByRole('button', { name: 'Delete', exact: true }).click()
    await page.getByRole('dialog').getByRole('button', { name: 'Delete', exact: true }).click()
    await expect(page).toHaveURL(/\/cluster\/tasks$/)
    await expect(section(page, 'My tasks').getByRole('link')).toHaveText(['Deploy web'])
    expect(readsAfter).toBe(2)
  })

  test('a delete waited for while the operator opens another task leaves the page on that task', async ({ page }) => {
    const ID = '1b2c3d4e5f607182'
    let deleted = false
    let readsAfter = 0
    await mock(page, {
      taskDetails: TASK_DETAILS,
      replies: {
        'GET /cluster/tasks': () => {
          if (!deleted) return ok(TASKS)
          readsAfter += 1
          // The follower has the delete from the fourth read, 1.5 s on.
          return ok(readsAfter >= 4 ? TASKS.filter((x) => x.id !== ID) : TASKS)
        },
        [`DELETE /cluster/tasks/${ID}`]: () => {
          deleted = true
          return ok({})
        },
      },
    })
    await page.goto(`/cluster/tasks/${ID}`)
    await page.getByTestId('task-pane-setup').getByRole('button', { name: 'Delete', exact: true }).click()
    await page.getByRole('dialog').getByRole('button', { name: 'Delete', exact: true }).click()
    await expect.poll(() => readsAfter).toBeGreaterThanOrEqual(1)
    await list(page).getByRole('link', { name: 'Deploy web' }).click()
    await expect(page).toHaveURL(new RegExp(`/cluster/tasks/${DEPLOY_ID}$`))
    await expect(section(page, 'My tasks').getByRole('link')).toHaveText(['Deploy web'])
    expect(readsAfter).toBe(4)
    await page.waitForTimeout(500)
    await expect(page).toHaveURL(new RegExp(`/cluster/tasks/${DEPLOY_ID}$`))
  })

  test('read-only tasks offer no edit: a built-in only a clone, a Git task a delete as well', async ({ page }) => {
    await mock(page, { taskDetails: TASK_DETAILS })
    const actions = async (path: string, name: string) => {
      await page.goto(path)
      const pane = page.getByTestId('task-pane-setup')
      await expect(pane.getByRole('heading', { level: 2 })).toHaveText(name)
      return {
        edit: await pane.getByRole('link', { name: 'Edit', exact: true }).count(),
        clone: await pane.getByRole('link', { name: 'Clone and edit', exact: true }).count(),
        delete: await pane.getByRole('button', { name: 'Delete', exact: true }).count(),
      }
    }
    expect(await actions('/cluster/tasks/builtin%2Freboot', 'Reboot (only when required)')).toEqual({ edit: 0, clone: 1, delete: 0 })
    expect(await actions(`/cluster/tasks/${GIT_ID}`, 'Nightly backup')).toEqual({ edit: 0, clone: 1, delete: 1 })
    expect(await actions('/cluster/tasks/1b2c3d4e5f607182', 'Rotate logs')).toEqual({ edit: 1, clone: 0, delete: 1 })

    // An edit address for a read-only task shows it read-only, with the clone.
    await page.goto(`/cluster/tasks/${GIT_ID}/edit`)
    const editor = editorPane(page)
    await expect(editor).toContainText('imported from Git')
    await expect(editor.getByRole('button', { name: 'Save', exact: true })).toHaveCount(0)
    await expect(editor.getByLabel('Task name')).toHaveCount(0)
    await expect(editor.getByRole('button', { name: 'Delete', exact: true })).toHaveCount(1)

    await page.goto('/cluster/tasks/builtin%2Fcommand/edit')
    await expect(editor).toContainText('A built-in task cannot be changed')
    await expect(editor.getByRole('button', { name: 'Save', exact: true })).toHaveCount(0)
    await expect(editor.getByRole('button', { name: 'Delete', exact: true })).toHaveCount(0)
    // The clone takes the screen's words for a built-in.
    await editor.getByRole('link', { name: 'Clone and edit', exact: true }).click()
    await expect(page).toHaveURL(/\/cluster\/tasks\/new\?from=builtin%2Fcommand$/)
    await expect(editor.getByLabel('Task name')).toHaveValue('Run a command copy')
    await expect(editor.getByLabel('Description')).toHaveValue('Runs the command you type as root. It goes to the shell exactly as typed.')
  })

  test('a draft survives a trip to another menu, and leaving the page asks first', async ({ page }) => {
    await mock(page)
    await page.goto('/cluster/tasks/new')
    const editor = editorPane(page)
    await editor.getByLabel('Task name').fill('Half done')
    await tab(page, 'Overview').click()
    await expect(page).toHaveURL(/\/cluster\/overview$/)
    await tab(page, 'Tasks').click()
    await list(page).getByRole('link', { name: 'New task' }).click()
    await expect(editor.getByLabel('Task name')).toHaveValue('Half done')
    await expect(editor).toContainText('Your unsaved changes were kept')

    let asked = ''
    page.on('dialog', (d) => {
      asked = d.type()
      void d.dismiss()
    })
    await page.close({ runBeforeUnload: true })
    await expect.poll(() => asked).toBe('beforeunload')
  })

  test.describe('Korean', () => {
    test.use({ locale: 'ko-KR' })

    test('[복제해서 고치기] on a built-in opens a new task named "<이름> 복사본", saved with POST', async ({ page }) => {
      await page.addInitScript(() => localStorage.setItem('sfpanel_language', 'ko'))
      const yaml = 'name: 명령 실행 복사본\n'
      const copy = { id: '4e5f607182930415', name: '명령 실행 복사본', source: { kind: 'user' }, read_only: false, updated_at: seen, updated_by: 'admin' }
      const tasks: unknown[] = [...TASKS]
      const { calls } = await mock(page, {
        tasks,
        taskDetails: TASK_DETAILS,
        replies: {
          'POST /cluster/tasks/format': ok({ yaml }),
          'POST /cluster/tasks/validate': valid({ ...COMMAND.task, name: '명령 실행 복사본' }),
          'POST /cluster/tasks': ok(copy),
        },
      })
      await page.goto('/cluster/tasks/builtin%2Fcommand')
      const pane = page.getByTestId('task-pane-setup')
      await expect(pane.getByRole('heading', { level: 2 })).toHaveText('명령 실행')
      await expect(pane.getByRole('link', { name: '고치기', exact: true })).toHaveCount(0)
      await expect(pane.getByRole('button', { name: '삭제', exact: true })).toHaveCount(0)
      await pane.getByRole('link', { name: '복제해서 고치기' }).click()
      await expect(page).toHaveURL(/\/cluster\/tasks\/new\?from=builtin%2Fcommand$/)
      const editor = editorPane(page)
      await expect(editor.getByLabel('작업 이름')).toHaveValue('명령 실행 복사본')
      // The address keeps the clone across a reload.
      await page.reload()
      await expect(editor.getByLabel('작업 이름')).toHaveValue('명령 실행 복사본')

      tasks.push(copy)
      await editor.getByRole('button', { name: '저장', exact: true }).click()
      await expect(page).toHaveURL(/\/cluster\/tasks\/4e5f607182930415$/)
      expect(calls.map((c) => `${c.method} ${c.path}`)).toEqual(['POST /cluster/tasks/format', 'POST /cluster/tasks/validate', 'POST /cluster/tasks'])
      expect(calls[0].body).toEqual({
        task: {
          ...COMMAND.task,
          name: '명령 실행 복사본',
          description: '입력한 명령을 root 권한으로 실행합니다. 입력한 그대로 셸에 전달됩니다.',
        },
      })
      expect(calls[2].body).toEqual({ yaml })
    })
  })

  test.describe('phone', () => {
    test.use({ viewport: { width: 412, height: 860 }, hasTouch: true })

    test('the editor, its inputs and a step fit the screen', async ({ page }) => {
      await mock(page)
      await page.goto('/cluster/tasks/new')
      const editor = editorPane(page)
      await editor.getByRole('button', { name: 'Add input' }).click()
      await editor.getByRole('button', { name: 'Add step' }).click()
      await page.getByRole('menuitem', { name: 'File' }).click()
      await expect(editor.getByTestId('task-step-0')).toBeVisible()
      const main = page.getByRole('main')
      expect(await main.evaluate((m) => m.scrollWidth <= m.clientWidth)).toBe(true)
      for (const part of [editor, editor.getByTestId('task-step-0'), editor.getByTestId('task-input-0')]) {
        const box = await part.boundingBox()
        expect(box && box.x >= 0 && box.x + box.width <= 412).toBe(true)
      }
    })
  })
})

test.describe('Git import', () => {
  const REPO = 'https://github.com/acme/ops'
  const COMMIT = 'fedcba9876543210fedcba9876543210fedcba98'
  const TOKEN = 'ghp_FixtureToken0123456789'
  const file = (name: string, state: string, extra: Record<string, unknown> = {}) =>
    ({ path: `tasks/${name}`, name, sha: '1'.repeat(40), size: 512, state, ...extra })
  const PREVIEW = {
    repo: REPO, branch: 'main', path: 'tasks', commit: COMMIT,
    files: [
      file('deploy.yaml', 'new'),
      file('backup.yaml', 'changed', { task_id: '2c3d4e5f60718293', task_name: 'Nightly backup' }),
      file('prune.yaml', 'unchanged', { task_id: '5f60718293041526', task_name: 'Prune images' }),
      file('huge.yaml', 'too_large'),
      file('broken.yml', 'new'),
    ],
    removed: [{ task_id: '60718293a4b5c6d7', task_name: 'Old cleanup', path: 'tasks/old.yaml' }],
  }
  const IMPORTED = {
    commit: COMMIT,
    imported: [{ id: '7182930415263748', name: 'Deploy everything', read_only: true, updated_at: seen, updated_by: 'admin', source: { kind: 'git', repo: REPO, branch: 'main', path: 'tasks/deploy.yaml', sha: COMMIT } }],
    failed: [{ code: 'TASK_INVALID', message: 'line 3: steps: at least one step', details: { path: 'tasks/broken.yml', line: '3' } }],
  }
  const importDialog = (page: Page) => page.getByRole('dialog')
  const fileBox = (page: Page, path: string) => importDialog(page).getByRole('checkbox', { name: path })
  const listReads = (requests: string[]) => requests.filter((r) => r === 'GET /cluster/tasks').length

  test('previews a folder, then imports only the files picked, at the commit the preview read', async ({ page }) => {
    const tasks: unknown[] = [...TASKS]
    const { calls, requests } = await mock(page, {
      tasks,
      replies: { 'POST /cluster/tasks/import/preview': ok(PREVIEW), 'POST /cluster/tasks/import': ok(IMPORTED) },
    })
    await page.goto('/cluster/tasks')
    await list(page).getByRole('button', { name: 'Import from Git' }).click()
    const dialog = importDialog(page)
    await expect(dialog.getByRole('heading', { name: 'Import tasks from Git' })).toBeVisible()
    await expect(dialog.getByLabel('Branch')).toHaveValue('main')
    await dialog.getByLabel('Repository URL').fill(REPO)
    await dialog.getByLabel('Folder').fill('tasks')
    await dialog.getByRole('button', { name: 'Preview' }).click()

    // New and changed files start picked; unchanged and too large cannot be.
    await expect(fileBox(page, 'tasks/deploy.yaml')).toBeChecked()
    await expect(fileBox(page, 'tasks/backup.yaml')).toBeChecked()
    await expect(fileBox(page, 'tasks/broken.yml')).toBeChecked()
    await expect(fileBox(page, 'tasks/prune.yaml')).not.toBeChecked()
    await expect(fileBox(page, 'tasks/prune.yaml')).toBeDisabled()
    await expect(fileBox(page, 'tasks/huge.yaml')).not.toBeChecked()
    await expect(fileBox(page, 'tasks/huge.yaml')).toBeDisabled()
    for (const state of ['New', 'Changed', 'Unchanged', 'Too large']) await expect(dialog.getByText(state, { exact: true }).first()).toBeVisible()
    await expect(dialog).toContainText('fedcba9')

    await fileBox(page, 'tasks/backup.yaml').click()
    const reads = listReads(requests)
    // The list read after the import has what it imported.
    tasks.push(...IMPORTED.imported)
    await dialog.getByRole('button', { name: /^Import/ }).click()

    await expect(dialog.getByTestId('import-result')).toContainText('Imported 1 task')
    await expect(dialog.getByTestId('import-result')).toContainText('Deploy everything')
    const failed = dialog.getByTestId('import-failed')
    await expect(failed).toContainText('The task document has problems.')
    await expect(failed).toContainText('tasks/broken.yml')
    await expect(failed).toContainText('line 3')
    expect(calls).toEqual([
      { method: 'POST', path: '/cluster/tasks/import/preview', body: { url: REPO, branch: 'main', path: 'tasks' } },
      { method: 'POST', path: '/cluster/tasks/import', body: { url: REPO, branch: 'main', path: 'tasks', commit: COMMIT, files: ['tasks/deploy.yaml', 'tasks/broken.yml'] } },
    ])
    // The list is read again before the result shows; once, as it has the
    // task imported. The result stays.
    await expect.poll(() => listReads(requests)).toBe(reads + 1)
    await expect(dialog.getByTestId('import-result')).toBeVisible()
  })

  test('the token is a password, sent in the bodies only: in no address, and not kept', async ({ page }) => {
    const { calls, requests } = await mock(page, {
      tasks: [...TASKS, ...IMPORTED.imported],
      replies: { 'POST /cluster/tasks/import/preview': ok(PREVIEW), 'POST /cluster/tasks/import': ok(IMPORTED) },
    })
    await page.goto('/cluster/tasks')
    await list(page).getByRole('button', { name: 'Import from Git' }).click()
    const dialog = importDialog(page)
    await dialog.getByLabel('Repository URL').fill(REPO)
    await expect(dialog.getByLabel('Token', { exact: true })).toHaveAttribute('type', 'password')
    await expect(dialog).toContainText('never stored')
    await dialog.getByLabel('Token', { exact: true }).fill(TOKEN)
    await dialog.getByRole('button', { name: 'Preview' }).click()
    await dialog.getByRole('button', { name: /^Import/ }).click()
    await expect(dialog.getByTestId('import-result')).toBeVisible()

    expect(calls.map((c) => (c.body as { token?: string }).token)).toEqual([TOKEN, TOKEN])
    expect(requests.length).toBeGreaterThan(2)
    expect(requests.filter((r) => r.includes(TOKEN) || r.includes(encodeURIComponent(TOKEN)))).toEqual([])
    const kept = await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }) + location.href)
    expect(kept).not.toContain(TOKEN)

    // Closed and opened again, the dialog has forgotten it.
    await dialog.getByRole('button', { name: 'Close', exact: true }).first().click()
    await list(page).getByRole('button', { name: 'Import from Git' }).click()
    await expect(importDialog(page).getByLabel('Token', { exact: true })).toHaveValue('')
  })

  test.describe('rate limit', () => {
    test.use({ timezoneId: 'UTC' })

    test('reads as the local time the allowance resets, not as a token problem', async ({ page }) => {
      await mock(page, {
        replies: {
          'POST /cluster/tasks/import/preview': fail(429, 'GIT_RATE_LIMITED', 'GitHub rate limit exceeded', { reset_at: '2026-11-01T10:07:00Z', limit: '60' }),
        },
      })
      await page.goto('/cluster/tasks')
      await list(page).getByRole('button', { name: 'Import from Git' }).click()
      const dialog = importDialog(page)
      await dialog.getByLabel('Repository URL').fill(REPO)
      await dialog.getByRole('button', { name: 'Preview' }).click()
      await expect(dialog.getByRole('alert')).toHaveText("GitHub's request allowance is used up. Try again after 10:07, or add a token.")
      // The form stays, to add a token.
      await expect(dialog.getByLabel('Token', { exact: true })).toBeVisible()
    })
  })

  test('a GitHub address is checked before anything is sent', async ({ page }) => {
    const { calls } = await mock(page)
    await page.goto('/cluster/tasks')
    await list(page).getByRole('button', { name: 'Import from Git' }).click()
    const dialog = importDialog(page)
    await dialog.getByLabel('Repository URL').fill('https://gitlab.com/acme/ops')
    await dialog.getByRole('button', { name: 'Preview' }).click()
    await expect(dialog).toContainText('Enter a GitHub address: https://github.com/<owner>/<repo>')
    expect(calls).toEqual([])
  })

  test('[Pull again] opens the dialog on the folder the tasks came from; only new and changed files are ticked', async ({ page }) => {
    const { calls } = await mock(page, {
      replies: { 'POST /cluster/tasks/import/preview': ok(PREVIEW), 'DELETE /cluster/tasks/60718293a4b5c6d7': ok({}) },
    })
    // From a task's pane: deleting a task gone from the repository keeps the page and the dialog.
    await page.goto('/cluster/tasks/2c3d4e5f60718293')
    await section(page, 'acme/ops').getByRole('button', { name: 'Pull again' }).click()
    const dialog = importDialog(page)
    await expect(dialog.getByLabel('Repository URL')).toHaveValue(REPO)
    await expect(dialog.getByLabel('Branch')).toHaveValue('main')
    await expect(dialog.getByLabel('Folder')).toHaveValue('tasks')
    await expect(dialog.getByLabel('Token', { exact: true })).toHaveValue('')
    await dialog.getByRole('button', { name: 'Preview' }).click()
    expect(calls).toEqual([{ method: 'POST', path: '/cluster/tasks/import/preview', body: { url: REPO, branch: 'main', path: 'tasks' } }])
    const ticked = await dialog.getByRole('checkbox').evaluateAll((boxes) =>
      boxes.filter((b) => b.getAttribute('aria-checked') === 'true').map((b) => b.getAttribute('aria-label')))
    expect(ticked).toEqual(['tasks/deploy.yaml', 'tasks/backup.yaml', 'tasks/broken.yml'])

    // A task whose file is gone is offered for deletion, asked first.
    const gone = dialog.getByTestId('import-removed')
    await expect(gone).toContainText('Gone from the repository')
    await expect(gone).toContainText('Old cleanup')
    await expect(gone).toContainText('tasks/old.yaml')
    await gone.getByRole('button', { name: 'Delete', exact: true }).click()
    const confirm = page.getByRole('dialog').filter({ hasText: 'Delete Old cleanup?' })
    await confirm.getByRole('button', { name: 'Delete', exact: true }).click()
    await expect(gone).toHaveCount(0)
    expect(calls.slice(1)).toEqual([{ method: 'DELETE', path: '/cluster/tasks/60718293a4b5c6d7', body: null }])
    await expect(page).toHaveURL(/\/cluster\/tasks\/2c3d4e5f60718293$/)
    await expect(fileBox(page, 'tasks/deploy.yaml')).toBeChecked()
  })

  test('[Pull again] on the open task shows the new version in its pane without a reload', async ({ page }) => {
    const GIT_ID = '2c3d4e5f60718293'
    const model = {
      name: 'Nightly backup', targets: { groups: ['db'] }, batch: 1, stop_on_failure: true,
      steps: [{ kind: 'command', name: 'Copy', command: { run: 'rsync -a /srv/ backup:/srv/', timeout: 600_000_000_000 } }],
    }
    const pulledTask = { ...GIT_TASKS[0], updated_at: '2026-01-02T00:00:00Z', source: { ...GIT_TASKS[0].source, sha: COMMIT } }
    let pulled = false
    await mock(page, {
      replies: {
        'GET /cluster/tasks': () => ok(pulled ? [...BUILTINS, ...USER_TASKS, pulledTask] : TASKS),
        [`GET /cluster/tasks/${GIT_ID}`]: () => ok(pulled
          ? { ...pulledTask, yaml: 'name: Nightly backup\n', task: { ...model, inputs: [{ name: 'port', label: 'Port' }] } }
          : { ...GIT_TASKS[0], yaml: 'name: Nightly backup\n', task: model }),
        'POST /cluster/tasks/import/preview': ok({ ...PREVIEW, files: [file('backup.yaml', 'changed', { task_id: GIT_ID, task_name: 'Nightly backup' })], removed: [] }),
        'POST /cluster/tasks/import': () => {
          pulled = true
          return ok({ commit: COMMIT, imported: [pulledTask], failed: [] })
        },
      },
    })
    await page.goto(`/cluster/tasks/${GIT_ID}`)
    const pane = page.getByTestId('task-pane-setup')
    await expect(pane.getByTestId('task-target-count')).toHaveText('2 nodes targeted')
    await expect(pane.getByLabel('Port')).toHaveCount(0)
    await section(page, 'acme/ops').getByRole('button', { name: 'Pull again' }).click()
    const dialog = importDialog(page)
    await dialog.getByRole('button', { name: 'Preview' }).click()
    await dialog.getByRole('button', { name: /^Import/ }).click()
    await expect(dialog.getByTestId('import-result')).toContainText('Imported 1 task')
    await dialog.getByRole('button', { name: 'Close', exact: true }).first().click()
    await expect(pane.getByTestId('task-source')).toContainText('fedcba9')
    await expect(pane.getByLabel('Port')).toBeVisible()
  })

  test("a Git task's pane names its repository, branch, path and commit", async ({ page }) => {
    await mock(page)
    await page.goto('/cluster/tasks/2c3d4e5f60718293')
    const source = page.getByTestId('task-pane-setup').getByTestId('task-source')
    await expect(source).toContainText(REPO)
    await expect(source).toContainText('main')
    await expect(source).toContainText('tasks/backup.yaml')
    await expect(source).toContainText('0123456')
    await expect(source).not.toContainText('01234567')
    await page.goto('/cluster/tasks/0a1b2c3d4e5f6071')
    await expect(page.getByTestId('task-pane-setup').getByRole('heading', { level: 2 })).toHaveText('Deploy web')
    await expect(page.getByTestId('task-source')).toHaveCount(0)
  })

  test.describe('Korean', () => {
    test.use({ locale: 'ko-KR', timezoneId: 'UTC' })

    test('the buttons, the states and the rate limit read in Korean', async ({ page }) => {
      await page.addInitScript(() => localStorage.setItem('sfpanel_language', 'ko'))
      await mock(page, {
        replies: {
          'POST /cluster/tasks/import/preview': [
            fail(429, 'GIT_RATE_LIMITED', 'GitHub rate limit exceeded', { reset_at: '2026-11-01T10:07:00Z' }),
            ok(PREVIEW),
          ],
        },
      })
      await page.goto('/cluster/tasks')
      const koList = page.getByRole('navigation', { name: '작업 목록' })
      await expect(koList.getByRole('button', { name: 'Git에서 가져오기' })).toBeVisible()
      await koList.getByRole('region', { name: 'acme/ops', exact: true }).getByRole('button', { name: '다시 받기' }).click()
      const dialog = importDialog(page)
      await expect(dialog.getByRole('heading', { name: 'Git에서 작업 가져오기' })).toBeVisible()
      await expect(dialog).toContainText('저장하지 않습니다')
      await dialog.getByRole('button', { name: '미리보기' }).click()
      await expect(dialog.getByRole('alert')).toHaveText('GitHub 요청 한도를 다 썼습니다. 10:07 이후에 다시 하거나 토큰을 넣으세요.')
      await dialog.getByRole('button', { name: '미리보기' }).click()
      for (const state of ['새로', '바뀜', '그대로', '너무 큼']) await expect(dialog.getByText(state, { exact: true }).first()).toBeVisible()
      await expect(dialog.getByTestId('import-removed')).toContainText('저장소에서 사라짐')
      await expect(dialog.getByRole('button', { name: /가져오기/ })).toBeEnabled()
    })
  })

  test.describe('phone', () => {
    test.use({ viewport: { width: 412, height: 860 }, hasTouch: true })

    test('the import is reached from the list and its preview fits the screen', async ({ page }) => {
      await mock(page, { replies: { 'POST /cluster/tasks/import/preview': ok(PREVIEW) } })
      await page.goto('/cluster/tasks')
      await list(page).getByRole('button', { name: 'Import from Git' }).click()
      const dialog = importDialog(page)
      await dialog.getByLabel('Repository URL').fill(REPO)
      await dialog.getByRole('button', { name: 'Preview' }).click()
      await expect(fileBox(page, 'tasks/deploy.yaml')).toBeChecked()
      const box = await dialog.boundingBox()
      expect(box && box.x >= 0 && box.x + box.width <= 412).toBe(true)
      expect(await dialog.evaluate((d) => d.scrollWidth <= d.clientWidth)).toBe(true)
    })
  })
})

// ----------------------------------------------------------- agent install --

// The agent install's own form, opened from the built-in list: servers, one
// account, how to install, then [Preview] or [Start install]. Its secrets
// live in the form alone and leave the browser in the start's body.
test.describe('agent install', () => {
  const INSTALL_PATH = '/cluster/tasks/builtin%2Fagent-install'
  const PASSWORD = 'Fixture-Pw-0123'
  const SUDO = 'Fixture-Sudo-4567'
  const KEY = '-----BEGIN OPENSSH PRIVATE KEY-----\nFixtureKeyMaterial89ab\n-----END OPENSSH PRIVATE KEY-----\n'
  const PASSPHRASE = 'Fixture-Phrase-cdef'
  const pane = (page: Page) => page.getByTestId('task-pane-install')
  const row = (page: Page, i: number) => page.getByTestId(`install-server-${i}`)
  const host = (page: Page, n: number) => pane(page).getByRole('textbox', { name: `Server ${n}: IP or host`, exact: true })
  const started = (over: Record<string, unknown> = {}): Reply => ({ status: 202, body: { success: true, data: { run_id: 'i-1', ...over } } })

  test('opens from the end of the built-in list, across a reload, with nothing to clone or edit', async ({ page }) => {
    const { requests } = await mock(page)
    await page.goto('/cluster/tasks')
    await expect(items(page, 'Built-in').last()).toHaveText('Agent install (SSH)')
    await section(page, 'Built-in').getByRole('link', { name: 'Agent install (SSH)' }).click()
    await expect(page).toHaveURL(new RegExp(`${INSTALL_PATH}$`))
    const chosen = () => section(page, 'Built-in').getByRole('link', { name: 'Agent install (SSH)' })
    await expect(chosen()).toHaveAttribute('aria-current', 'page')
    await expect(pane(page).getByRole('heading', { name: 'Agent install (SSH)' })).toBeVisible()
    await expect(pane(page)).toContainText('installs the SFPanel agent')
    await expect(page.getByTestId('task-pane-setup')).toHaveCount(0)
    for (const name of ['Clone', 'Edit', 'Delete']) await expect(pane(page).getByRole('button', { name })).toHaveCount(0)
    await expect(pane(page).getByRole('link', { name: /Clone|Edit/ })).toHaveCount(0)

    await page.reload()
    await expect(chosen()).toHaveAttribute('aria-current', 'page')
    await expect(pane(page).getByRole('button', { name: 'Start install' })).toBeVisible()
    // It is not a task: no task route is asked about it.
    expect(requests.filter((r) => r.startsWith('GET /cluster/tasks/builtin'))).toEqual([])
  })

  test('pasted lines fill the table, and a line left out is named by its number', async ({ page }) => {
    await mock(page)
    await page.goto(INSTALL_PATH)
    await pane(page).getByRole('button', { name: 'Paste several lines' }).click()
    await pane(page).getByRole('textbox', { name: 'Servers, one a line' }).fill(
      '192.0.2.21 web-04 web\n# a comment\n192.0.2.22:2222\nnot/a/host\n192.0.2.21\n',
    )
    await pane(page).getByRole('button', { name: 'Add to the list' }).click()
    // The blank row the form starts with is taken by the first server.
    await expect(pane(page).locator('[data-testid^="install-server-"]')).toHaveCount(2)
    await expect(host(page, 1)).toHaveValue('192.0.2.21')
    await expect(pane(page).getByRole('textbox', { name: 'Server 1: name' })).toHaveValue('web-04')
    await expect(pane(page).getByRole('textbox', { name: 'Server 1: group' })).toHaveValue('web')
    await expect(host(page, 2)).toHaveValue('192.0.2.22')
    await expect(pane(page).getByRole('textbox', { name: 'Server 2: SSH port' })).toHaveValue('2222')
    const skipped = pane(page).getByTestId('install-paste-problems')
    await expect(skipped).toContainText('Line 4: Enter an IP address or a host name.')
    await expect(skipped).toContainText('Line 5: This server is already on line 1.')

    // A row is added by hand and taken away.
    await pane(page).getByRole('button', { name: 'Add server' }).click()
    await expect(row(page, 2)).toBeVisible()
    await pane(page).getByRole('button', { name: 'Remove server 3' }).click()
    await expect(pane(page).locator('[data-testid^="install-server-"]')).toHaveCount(2)
  })

  test('the login method decides the fields: Tailscale SSH asks for no secret', async ({ page }) => {
    await mock(page)
    await page.goto(INSTALL_PATH)
    const account = pane(page).getByTestId('install-account')
    await expect(account.getByRole('radio', { name: 'Password' })).toBeChecked()
    await expect(account.getByLabel('Login password', { exact: true })).toHaveAttribute('type', 'password')
    await expect(account.getByRole('checkbox', { name: 'The sudo password is the login password' })).toBeChecked()
    await expect(account.getByLabel('sudo password', { exact: true })).toHaveCount(0)
    await account.getByRole('checkbox', { name: 'The sudo password is the login password' }).click()
    await expect(account.getByLabel('sudo password', { exact: true })).toHaveAttribute('type', 'password')

    await account.getByRole('radio', { name: 'Private key' }).check()
    await expect(account.getByLabel('Login password', { exact: true })).toHaveCount(0)
    const key = account.getByRole('textbox', { name: 'Private key' })
    await expect(key).toHaveAttribute('spellcheck', 'false')
    await expect(key).toHaveAttribute('autocomplete', 'off')
    await expect(account.getByLabel('Key passphrase', { exact: true })).toHaveAttribute('type', 'password')
    await expect(account.getByRole('button', { name: 'Read from a file' })).toBeVisible()
    // A key login cannot hand its password to sudo: sudo's own field.
    await expect(account.getByRole('checkbox', { name: 'The sudo password is the login password' })).toHaveCount(0)
    await expect(account.getByLabel('sudo password', { exact: true })).toBeVisible()

    await account.getByRole('radio', { name: 'Tailscale SSH' }).check()
    await expect(account.getByLabel('Login password', { exact: true })).toHaveCount(0)
    await expect(account.getByRole('textbox', { name: 'Private key' })).toHaveCount(0)
    await expect(account.getByLabel('Key passphrase', { exact: true })).toHaveCount(0)
    await expect(account).toContainText("tailnet's SSH rules")

    // root needs no sudo.
    await account.getByRole('textbox', { name: 'User' }).fill('root')
    await expect(account.getByLabel('sudo password', { exact: true })).toHaveCount(0)
    await expect(account).toContainText('root needs no sudo')
  })

  test('[Start install] sends the form and opens the run; [Preview] asks the run to stop for a review', async ({ page }) => {
    const { calls } = await mock(page, { replies: { 'POST /cluster/agent-installs': started() } })
    await page.goto(INSTALL_PATH)
    await host(page, 1).fill('192.0.2.21')
    await pane(page).getByRole('textbox', { name: 'Server 1: name' }).fill('web-04')
    await pane(page).getByRole('textbox', { name: 'Server 1: group' }).fill('web')
    await pane(page).getByRole('button', { name: 'Add server' }).click()
    await host(page, 2).fill('host-b.example.ts.net')
    await pane(page).getByRole('textbox', { name: 'Server 2: SSH port' }).fill('2222')
    const account = pane(page).getByTestId('install-account')
    await account.getByRole('textbox', { name: 'User' }).fill('ubuntu')
    await account.getByLabel('Login password', { exact: true }).fill(PASSWORD)
    await pane(page).getByRole('radio', { name: 'Offline' }).check()
    await pane(page).getByLabel('Servers at once').selectOption('2')
    await expect(pane(page)).toContainText('never stored')
    await pane(page).getByRole('button', { name: 'Start install' }).click()

    await expect(page).toHaveURL(/\/cluster\/tasks\/runs\/i-1$/)
    expect(calls).toEqual([{
      method: 'POST',
      path: '/cluster/agent-installs',
      body: {
        servers: [{ host: '192.0.2.21', port: 22, name: 'web-04', group: 'web' }, { host: 'host-b.example.ts.net', port: 2222 }],
        account: { user: 'ubuntu', method: 'password', password: PASSWORD, sudo_same: true },
        review: false,
        mode: 'offline',
        concurrency: 2,
      },
    }])

    await page.goto(INSTALL_PATH)
    await host(page, 1).fill('192.0.2.30')
    await pane(page).getByTestId('install-account').getByRole('textbox', { name: 'User' }).fill('root')
    await pane(page).getByTestId('install-account').getByRole('radio', { name: 'Tailscale SSH' }).check()
    await pane(page).getByRole('button', { name: 'Preview' }).click()
    await expect(page).toHaveURL(/\/cluster\/tasks\/runs\/i-1$/)
    expect(calls[1]).toEqual({
      method: 'POST',
      path: '/cluster/agent-installs',
      body: { servers: [{ host: '192.0.2.30', port: 22 }], account: { user: 'root', method: 'tailscale' }, review: true, mode: 'auto', concurrency: 3 },
    })
  })

  test('a form with problems is not sent, and says where', async ({ page }) => {
    const { calls } = await mock(page)
    await page.goto(INSTALL_PATH)
    await host(page, 1).fill('not/a/host')
    await pane(page).getByRole('button', { name: 'Start install' }).click()
    await expect(row(page, 0)).toContainText('Enter an IP address or a host name.')
    await expect(pane(page).getByTestId('install-account')).toContainText('Enter the SSH user.')
    await expect(pane(page).getByTestId('install-account')).toContainText('Enter the login password.')
    await expect(pane(page).getByRole('alert')).toContainText('Correct the fields marked above.')
    expect(calls).toEqual([])
    // Each problem goes as it is put right.
    await host(page, 1).fill('192.0.2.21')
    await expect(row(page, 0)).not.toContainText('Enter an IP address')
    await pane(page).getByTestId('install-account').getByRole('textbox', { name: 'User' }).fill('ubuntu')
    await pane(page).getByTestId('install-account').getByLabel('Login password', { exact: true }).fill(PASSWORD)
    await expect(pane(page).getByRole('alert')).toHaveCount(0)
  })

  test('the secrets are passwords, sent in the body alone: in no address, not kept, gone when the form opens again', async ({ page }) => {
    const { calls, requests } = await mock(page, { replies: { 'POST /cluster/agent-installs': started() } })
    await page.goto(INSTALL_PATH)
    await host(page, 1).fill('192.0.2.21')
    const account = pane(page).getByTestId('install-account')
    await account.getByRole('textbox', { name: 'User' }).fill('ubuntu')
    const password = account.getByLabel('Login password', { exact: true })
    await expect(password).toHaveAttribute('type', 'password')
    await expect(password).toHaveAttribute('autocomplete', 'off')
    await password.fill(PASSWORD)
    await account.getByRole('button', { name: 'Show the login password' }).click()
    await expect(password).toHaveAttribute('type', 'text')
    await account.getByRole('button', { name: 'Hide the login password' }).click()
    await expect(password).toHaveAttribute('type', 'password')
    await account.getByRole('checkbox', { name: 'The sudo password is the login password' }).click()
    await expect(account.getByLabel('sudo password', { exact: true })).toHaveAttribute('type', 'password')
    await account.getByLabel('sudo password', { exact: true }).fill(SUDO)

    // Left for another task and opened again, inside the page, the form has
    // forgotten both: nothing outlives the pane.
    const reopen = async () => {
      await section(page, 'Built-in').getByRole('link', { name: 'Run a command' }).click()
      await expect(page.getByTestId('task-pane-setup')).toBeVisible()
      await section(page, 'Built-in').getByRole('link', { name: 'Agent install (SSH)' }).click()
      await expect(pane(page)).toBeVisible()
    }
    await reopen()
    await expect(account.getByLabel('Login password', { exact: true })).toHaveValue('')
    await expect(account.getByRole('checkbox', { name: 'The sudo password is the login password' })).toBeChecked()
    await account.getByRole('checkbox', { name: 'The sudo password is the login password' }).click()
    await expect(account.getByLabel('sudo password', { exact: true })).toHaveValue('')

    await host(page, 1).fill('192.0.2.21')
    await account.getByRole('textbox', { name: 'User' }).fill('ubuntu')
    await account.getByLabel('Login password', { exact: true }).fill(PASSWORD)
    await account.getByLabel('sudo password', { exact: true }).fill(SUDO)
    await pane(page).getByRole('button', { name: 'Preview' }).click()
    await expect(page).toHaveURL(/\/cluster\/tasks\/runs\/i-1$/)

    // Opened again from the run, the form is empty.
    await section(page, 'Built-in').getByRole('link', { name: 'Agent install (SSH)' }).click()
    await expect(account.getByLabel('Login password', { exact: true })).toHaveValue('')

    // A key, read from a file, with its passphrase.
    await host(page, 1).fill('192.0.2.22')
    await account.getByRole('textbox', { name: 'User' }).fill('ubuntu')
    await account.getByRole('radio', { name: 'Private key' }).check()
    await account.locator('input[type=file]').setInputFiles({ name: 'id_ed25519', mimeType: 'application/octet-stream', buffer: Buffer.from(KEY) })
    await expect(account.getByRole('textbox', { name: 'Private key' })).toHaveValue(KEY)
    await expect(account.getByLabel('Key passphrase', { exact: true })).toHaveAttribute('type', 'password')
    await account.getByLabel('Key passphrase', { exact: true }).fill(PASSPHRASE)
    await pane(page).getByRole('button', { name: 'Start install' }).click()
    await expect(page).toHaveURL(/\/cluster\/tasks\/runs\/i-1$/)

    expect(calls.map((c) => (c.body as { account: unknown }).account)).toEqual([
      { user: 'ubuntu', method: 'password', password: PASSWORD, sudo_password: SUDO },
      { user: 'ubuntu', method: 'key', key: KEY, passphrase: PASSPHRASE },
    ])
    expect(requests.length).toBeGreaterThan(4)
    for (const secret of [PASSWORD, SUDO, PASSPHRASE, 'FixtureKeyMaterial89ab']) {
      expect(requests.filter((r) => r.includes(secret) || r.includes(encodeURIComponent(secret)))).toEqual([])
      const kept = await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }) + location.href)
      expect(kept).not.toContain(secret)
    }
    await section(page, 'Built-in').getByRole('link', { name: 'Agent install (SSH)' }).click()
    await account.getByRole('radio', { name: 'Private key' }).check()
    await expect(account.getByRole('textbox', { name: 'Private key' })).toHaveValue('')
    await expect(account.getByLabel('Key passphrase', { exact: true })).toHaveValue('')
  })

  test('a refusal that names a row or a field is shown beside it, and the secrets must be typed again', async ({ page }) => {
    const { calls } = await mock(page, {
      replies: {
        'POST /cluster/agent-installs': [
          fail(400, 'AGENT_INSTALL_INVALID', 'A server\'s SSH port is 1 to 65535', { field: 'port', row: 's2' }),
          fail(400, 'AGENT_INSTALL_INVALID', 'The user is missing', { field: 'user' }),
        ],
      },
    })
    await page.goto(INSTALL_PATH)
    // Three rows, the middle one left blank: the leader's s2 is the third row.
    await host(page, 1).fill('192.0.2.21')
    await pane(page).getByRole('button', { name: 'Add server' }).click()
    await pane(page).getByRole('button', { name: 'Add server' }).click()
    await host(page, 3).fill('192.0.2.23')
    const account = pane(page).getByTestId('install-account')
    await account.getByRole('textbox', { name: 'User' }).fill('ubuntu')
    await account.getByLabel('Login password', { exact: true }).fill(PASSWORD)
    await pane(page).getByRole('button', { name: 'Start install' }).click()

    await expect(row(page, 2)).toContainText('The SSH port is a number from 1 to 65535.')
    await expect(row(page, 0)).not.toContainText('SSH port is a number')
    await expect(row(page, 1)).not.toContainText('SSH port is a number')
    await expect(pane(page).getByRole('alert')).toContainText("correct a server's SSH port")
    await expect(pane(page).getByRole('alert')).toContainText('enter them again')
    await expect(page).toHaveURL(new RegExp(`${INSTALL_PATH}$`))
    // Cleared when the request was sent.
    await expect(account.getByLabel('Login password', { exact: true })).toHaveValue('')

    await account.getByLabel('Login password', { exact: true }).fill(PASSWORD)
    await pane(page).getByRole('button', { name: 'Start install' }).click()
    await expect(account).toContainText('Enter the SSH user.')
    await expect(row(page, 2)).not.toContainText('SSH port is a number')
    expect(calls).toHaveLength(2)
  })

  test.describe('Korean', () => {
    test.use({ locale: 'ko-KR' })

    test('the form reads in Korean', async ({ page }) => {
      await page.addInitScript(() => localStorage.setItem('sfpanel_language', 'ko'))
      await mock(page)
      await page.goto(INSTALL_PATH)
      const p = pane(page)
      await expect(p.getByRole('heading', { name: '에이전트 설치 (SSH)' })).toBeVisible()
      for (const text of ['설치할 서버', '접속 계정 (모든 서버에 같이 씀)', '비밀번호·키는 이 설치에만 쓰고 저장하지 않습니다']) await expect(p).toContainText(text)
      await expect(p.getByRole('textbox', { name: '1번째 서버: IP 또는 호스트' })).toBeVisible()
      await expect(p.getByRole('radio', { name: '비밀번호' })).toBeChecked()
      await expect(p.getByRole('radio', { name: '개인 키' })).toBeVisible()
      await expect(p.getByRole('checkbox', { name: 'sudo 비밀번호는 접속 비밀번호와 같음' })).toBeChecked()
      await expect(p.getByRole('button', { name: '미리 보기' })).toBeVisible()
      await expect(p.getByRole('button', { name: '설치 시작' })).toBeVisible()
      await p.getByRole('button', { name: '여러 줄 붙여넣기' }).click()
      await p.getByRole('textbox', { name: '서버 목록 (한 줄에 하나)' }).fill('192.0.2.21\nnot/a/host')
      await p.getByRole('button', { name: '목록에 넣기' }).click()
      await expect(p.getByTestId('install-paste-problems')).toContainText('2번째 줄: IP 주소나 호스트 이름을 넣으세요.')
    })
  })

  test.describe('phone', () => {
    test.use({ viewport: { width: 412, height: 860 }, hasTouch: true })

    test('the form fits the screen, a server a card', async ({ page }) => {
      await mock(page)
      await page.goto(INSTALL_PATH)
      await expect(page.getByRole('button', { name: /Agent install \(SSH\)/ })).toBeVisible()
      await pane(page).getByRole('button', { name: 'Add server' }).click()
      for (const el of [host(page, 1), host(page, 2), pane(page).getByRole('button', { name: 'Remove server 2' }), pane(page).getByRole('button', { name: 'Start install' })]) {
        await el.scrollIntoViewIfNeeded()
        const box = await el.boundingBox()
        expect(box && box.x >= 0 && box.x + box.width <= 412).toBe(true)
      }
      expect(await pane(page).evaluate((d) => d.scrollWidth <= d.clientWidth)).toBe(true)
    })
  })

  // The install's progress: the run view of a run of kind agent-install. Its
  // rows are servers (s1, s2 …) through five phases; a row waiting on the
  // operator asks above the table, and what can be done about a server that
  // failed is said below it.
  test.describe('progress', () => {
    const IRUN = 'i-1'
    const ROWS = `/cluster/agent-installs/${IRUN}`
    const FINGERPRINT = 'ED25519 SHA256:4mF1xQb7Lw0eTqGZc2yP9sVhK3nRjUa8oXdE5iBk9Pz'
    const FINGERPRINT2 = 'ECDSA SHA256:Zq0+Xc9yWv8uTs7rQp6oNm5lKj4iHg3fEd2cBa1zYxw'
    const CLEANUP = 'curl -fsSL https://raw.githubusercontent.com/svrforum/SFPanel/v0.81.0/scripts/install.sh | sudo bash -s -- uninstall --purge'
    const install = (over: Record<string, unknown> = {}) => ({
      id: IRUN, kind: 'agent-install', task_id: 'builtin/agent-install', task_name: '에이전트 설치 (SSH)', rev: '',
      inputs: { mode: 'auto', review: 'false', version: '0.81.0' },
      nodes: ['s1', 's2'], waves: [['s1', 's2']], batch: 2, stop_on_failure: false,
      status: 'running', started_by: 'admin', started_at: seen, ...over,
    })
    const ended = (over: Record<string, unknown> = {}) => install({ status: 'failed', ended_at: seen, ...over })
    /** A server's row: its five phases as given (the rest pending), each with the job ID its log is read by. */
    const srv = (key: string, status: string, phases: string[], over: Record<string, unknown> = {}) =>
      ({ status, steps: Array.from({ length: 5 }, (_, i) => st(phases[i] ?? 'pending', { job_id: `install/${key}/${i}` })), ...over })
    const ALL_DONE = ['succeeded', 'succeeded', 'succeeded', 'succeeded', 'succeeded']
    const CHECK_FAILED = ['succeeded', 'succeeded', 'succeeded', 'succeeded', 'failed']
    /** The run as read and as streamed; an install has no document, so no step names. */
    const follow = (run: Record<string, unknown>, rows: Record<string, unknown>, id = IRUN): Fixture => ({
      runDetails: { [id]: { run, nodes: rows } },
      streams: { [id]: [sse(snapshot(run, rows), ...(run.status === 'running' ? [] : [{ type: 'end' }]))] },
    })
    const wait = (page: Page, key: string) => runPane(page).getByTestId(`install-wait-${key}`)
    const server = (page: Page, key: string) => runPane(page).getByTestId(`install-row-${key}`)
    // A registered server whose connection check failed: the agent never reached the managers.
    const OFFLINE = srv('s2', 'failed', CHECK_FAILED, {
      host: '192.0.2.22', ip: '192.0.2.22', name: 'web-06', node_id: 'a-10',
      reason: 'AGENT_INSTALL_OFFLINE', reason_details: { cause: 'agent_to_managers', port: '3629' },
    })
    const SUCCEEDED = srv('s1', 'succeeded', ALL_DONE, { host: '192.0.2.21', ip: '192.0.2.21', name: 'web-04', node_id: 'a-1' })

    test('a host key waits for the operator: the whole fingerprint, and the answer carries it as shown', async ({ page }) => {
      const rows = {
        s1: srv('s1', 'running', ['running'], { host: '192.0.2.21', name: 'web-04', awaiting: 'host_key', host_key: FINGERPRINT }),
        s2: srv('s2', 'running', ['running'], { host: '192.0.2.22', awaiting: 'host_key', host_key: FINGERPRINT2 }),
      }
      const job = { id: 'install/s1/0', run_id: IRUN, kind: '', changed: false, started_at: seen }
      const { calls } = await mock(page, {
        ...follow(install(), rows),
        outputs: { [`${IRUN}/s1/0`]: [{ ...job, status: 'running', output: 'host key ED25519: waiting for the operator\n', next_skip: 43, done: false }] },
        replies: {
          [`POST ${ROWS}/servers/s1/host-key`]: ok(null),
          [`POST ${ROWS}/servers/s2/host-key`]: fail(409, 'AGENT_INSTALL_NOT_WAITING', 'This server is not waiting for that answer'),
        },
      })
      await page.goto(`/cluster/tasks/runs/${IRUN}`)
      const p = runPane(page)
      await expect(p.getByRole('heading', { name: 'Agent install (SSH)' })).toBeVisible()
      await expect(p.getByTestId('install-run-size')).toHaveText('2 servers · 2 at a time')
      await expect(p.getByRole('button', { name: 'Stop', exact: true })).toBeVisible()
      await expect(p.getByRole('button', { name: 'Force stop' })).toHaveCount(0)
      await expect(matrix(page).getByRole('columnheader')).toHaveText(['Server', 'Connect', 'Pre-flight check', 'Install', 'Register', 'Connection check'])
      await expect(matrix(page).getByRole('rowheader')).toContainText(['web-04', '192.0.2.22'])

      // The whole fingerprint and its key type, never shortened.
      const s1 = wait(page, 's1')
      await expect(s1).toContainText('Connecting to web-04 for the first time')
      await expect(s1.getByTestId('install-host-key')).toHaveText(FINGERPRINT)
      await s1.getByRole('button', { name: 'Yes, continue' }).click()
      await expect.poll(() => posts(calls, `${ROWS}/servers/s1/host-key`).length).toBe(1)
      expect(posts(calls, `${ROWS}/servers/s1/host-key`)[0].body).toEqual({ fingerprint: FINGERPRINT, accept: true })

      // A refusal reads as a sentence by the row, and nothing is sent again by itself.
      const s2 = wait(page, 's2')
      await expect(s2.getByTestId('install-host-key')).toHaveText(FINGERPRINT2)
      await s2.getByRole('button', { name: 'No, stop' }).click()
      await expect(s2.getByRole('alert')).toHaveText('This server is no longer waiting for that answer. The screen shows where it is now.')
      expect(posts(calls, `${ROWS}/servers/s2/host-key`).map((c) => c.body)).toEqual([{ fingerprint: FINGERPRINT2, accept: false }])
      await page.waitForTimeout(1500)
      expect(posts(calls, `${ROWS}/servers/s2/host-key`)).toHaveLength(1)

      // A cell opens that phase's log.
      await cell(page, 's1', 0).click()
      const output = page.getByTestId('task-node-output')
      await expect(output.getByRole('heading')).toHaveText('web-04 · Connect')
      await expect(output.getByTestId('task-node-output-text')).toHaveText('host key ED25519: waiting for the operator\n')
    })

    test('a Tailscale check links an https address and shows its host; anything else is only text', async ({ page }) => {
      const rows = {
        s1: srv('s1', 'running', ['running'], { host: 'web-05', awaiting: 'check', check_url: 'https://login.tailscale.com/a/1a2b3c4d' }),
        s2: srv('s2', 'running', ['running'], { host: '100.64.0.7', awaiting: 'check', check_url: 'javascript:alert(document.cookie)' }),
      }
      await mock(page, follow(install(), rows))
      await page.goto(`/cluster/tasks/runs/${IRUN}`)
      const link = wait(page, 's1').getByRole('link')
      await expect(link).toHaveAttribute('href', 'https://login.tailscale.com/a/1a2b3c4d')
      await expect(link).toHaveAttribute('target', '_blank')
      await expect(link).toHaveAttribute('rel', 'noopener noreferrer')
      await expect(wait(page, 's1')).toContainText('login.tailscale.com')
      await expect(wait(page, 's2')).toContainText('javascript:alert(document.cookie)')
      await expect(wait(page, 's2').getByRole('link')).toHaveCount(0)
      await expect(runPane(page).locator('a[href^="javascript:"]')).toHaveCount(0)
    })

    test('a standalone panel becomes an agent only when the operator says so; Stop asks first', async ({ page }) => {
      const rows = {
        s1: srv('s1', 'running', ['succeeded', 'running'], { host: '192.0.2.21', name: 'web-04', awaiting: 'standalone' }),
        s2: srv('s2', 'running', ['succeeded', 'running'], { host: '192.0.2.22', awaiting: 'standalone' }),
      }
      const { calls } = await mock(page, {
        ...follow(install(), rows),
        replies: {
          [`POST ${ROWS}/servers/s1/standalone`]: ok(null),
          [`POST ${ROWS}/servers/s2/standalone`]: ok(null),
          [`POST /cluster/task-runs/${IRUN}/stop`]: ok(install({ status: 'stopping', stopped_by: 'admin' })),
        },
      })
      await page.goto(`/cluster/tasks/runs/${IRUN}`)
      await expect(wait(page, 's1')).toContainText('web-04 already runs a standalone SFPanel')
      await expect(wait(page, 's1')).toContainText('local sign-in')
      await wait(page, 's1').getByRole('button', { name: 'Convert it' }).click()
      await wait(page, 's2').getByRole('button', { name: 'Skip it' }).click()
      await expect.poll(() => calls.filter((c) => c.path.endsWith('/standalone')).length).toBe(2)
      expect(posts(calls, `${ROWS}/servers/s1/standalone`).map((c) => c.body)).toEqual([{ accept: true }])
      expect(posts(calls, `${ROWS}/servers/s2/standalone`).map((c) => c.body)).toEqual([{ accept: false }])

      const p = runPane(page)
      const dialog = page.getByRole('dialog')
      await p.getByRole('button', { name: 'Stop', exact: true }).click()
      await expect(dialog).toContainText('credentials are cleared at once')
      await dialog.getByRole('button', { name: 'Cancel' }).click()
      expect(posts(calls, `/cluster/task-runs/${IRUN}/stop`)).toHaveLength(0)
      await p.getByRole('button', { name: 'Stop', exact: true }).click()
      await dialog.getByRole('button', { name: 'Stop', exact: true }).click()
      await expect.poll(() => posts(calls, `/cluster/task-runs/${IRUN}/stop`).map((c) => c.body)).toEqual([{ force: false }])
    })

    test('a review waits for one [Continue install]; a server that failed its check says why, with nothing to undo', async ({ page }) => {
      const run = install({ nodes: ['s1', 's2', 's3'], waves: [['s1', 's2', 's3']], inputs: { mode: 'auto', review: 'true', version: '0.81.0' } })
      const rows = {
        s1: srv('s1', 'running', ['succeeded', 'succeeded'], { host: '192.0.2.21', name: 'web-04', awaiting: 'review' }),
        s2: srv('s2', 'running', ['succeeded', 'succeeded'], { host: '192.0.2.22', awaiting: 'review' }),
        s3: srv('s3', 'failed', ['succeeded', 'failed', 'skipped', 'skipped', 'skipped'], {
          host: '192.0.2.23', reason: 'AGENT_INSTALL_ARCH', reason_details: { arch: 'armv7l' },
        }),
      }
      const { calls } = await mock(page, { ...follow(run, rows), replies: { [`POST ${ROWS}/proceed`]: ok(null) } })
      await page.goto(`/cluster/tasks/runs/${IRUN}`)
      const review = runPane(page).getByTestId('install-review')
      await expect(review).toContainText('Ready to install: web-04, 192.0.2.22.')
      await expect(review.getByRole('button')).toHaveCount(1)
      await expect(wait(page, 's1')).toHaveCount(0)
      await expect(server(page, 's3')).toContainText('No SFPanel release is built for this CPU (armv7l)')
      await expect(server(page, 's3').getByRole('button')).toHaveCount(0)
      await review.getByRole('button', { name: 'Continue install' }).click()
      await expect.poll(() => posts(calls, `${ROWS}/proceed`).map((c) => c.body)).toEqual([null])
    })

    test('a registered server that failed its check: [Recheck], and [Revert] after asking', async ({ page }) => {
      await page.clock.setFixedTime(NOW)
      const run = ended({ credentials_until: new Date(NOW.getTime() + 600_000).toISOString() })
      const { calls } = await mock(page, {
        ...follow(run, { s1: SUCCEEDED, s2: OFFLINE }),
        replies: {
          [`POST ${ROWS}/servers/s2/recheck`]: fail(404, 'AGENT_INSTALL_NOT_FOUND', 'No such install'),
          [`POST ${ROWS}/servers/s2/revert`]: ok(null),
        },
      })
      await page.goto(`/cluster/tasks/runs/${IRUN}`)
      const s2 = server(page, 's2')
      await expect(s2).toContainText("its heartbeat never reached the leader: the agent cannot reach the managers' port 3629")
      await expect(server(page, 's1').getByRole('button', { name: 'Recheck' })).toHaveCount(0)
      await expect(server(page, 's1').getByRole('button', { name: 'Revert' })).toHaveCount(0)

      await s2.getByRole('button', { name: 'Recheck' }).click()
      await expect(s2.getByRole('alert')).toHaveText(/^The cluster leader no longer holds this install/)
      expect(posts(calls, `${ROWS}/servers/s2/recheck`).map((c) => c.body)).toEqual([null])

      const dialog = page.getByRole('dialog')
      await s2.getByRole('button', { name: 'Revert' }).click()
      await expect(dialog).toContainText('web-06 is removed from the cluster')
      await expect(dialog).toContainText('had no SFPanel before')
      await dialog.getByRole('button', { name: 'Cancel' }).click()
      expect(posts(calls, `${ROWS}/servers/s2/revert`)).toHaveLength(0)
      await s2.getByRole('button', { name: 'Revert' }).click()
      await dialog.getByRole('button', { name: 'Revert' }).click()
      await expect.poll(() => posts(calls, `${ROWS}/servers/s2/revert`).map((c) => c.body)).toEqual([null])
      await page.waitForTimeout(1000)
      expect(posts(calls, `${ROWS}/servers/s2/recheck`)).toHaveLength(1)
    })

    test('the credentials run down to their end, then a server left to clean shows the command for it', async ({ page }) => {
      await page.clock.setFixedTime(NOW)
      const run = ended({ credentials_until: new Date(NOW.getTime() + 65_000).toISOString() })
      await mock(page, follow(run, { s1: SUCCEEDED, s2: OFFLINE }))
      await page.goto(`/cluster/tasks/runs/${IRUN}`)
      const left = runPane(page).getByTestId('install-credentials')
      await expect(left).toContainText('cleared in 1:05')
      await expect(runPane(page).getByText(CLEANUP)).toHaveCount(0)

      await page.clock.setFixedTime(new Date(NOW.getTime() + 60_000))
      await expect(left).toContainText('cleared in 0:05')
      await page.clock.setFixedTime(new Date(NOW.getTime() + 66_000))
      await expect(left).toContainText('The credentials are cleared')
      await expect(server(page, 's2').getByText(CLEANUP)).toBeVisible()
      await expect(server(page, 's2').getByRole('button', { name: 'Copy' })).toBeVisible()
      await expect(server(page, 's1').getByText(CLEANUP)).toHaveCount(0)
    })

    test('a server that succeeded opens its node; the run reason is worded; an install offers no rerun', async ({ page }) => {
      const run = ended({ reason: 'AGENT_INSTALL_LEADER_CHANGED' })
      const rows = {
        s1: SUCCEEDED,
        s2: srv('s2', 'failed', ['succeeded', 'succeeded', 'unknown'], { host: '192.0.2.22', ip: '192.0.2.22', reason: 'AGENT_INSTALL_LEADER_CHANGED' }),
      }
      await mock(page, follow(run, rows))
      await page.goto(`/cluster/tasks/runs/${IRUN}`)
      const p = runPane(page)
      await expect(p.getByTestId('task-run-status')).toHaveText('Failed')
      await expect(p.getByTestId('install-run-reason')).toContainText('The cluster leader changed during the install')
      await expect(p).not.toContainText('AGENT_INSTALL_LEADER_CHANGED')
      for (const name of ['Rerun failed nodes only', 'Choose nodes to rerun', 'Rerun', 'Stop', 'Force stop']) {
        await expect(p.getByRole('button', { name, exact: true })).toHaveCount(0)
      }
      // The leader that held the credentials is gone: the server is cleaned by hand.
      await expect(server(page, 's2').getByText(CLEANUP)).toBeVisible()
      await expect(server(page, 's2').getByRole('button', { name: 'Revert' })).toHaveCount(0)

      await server(page, 's1').getByRole('button', { name: 'Open node' }).click()
      await expect(page).toHaveURL(/\/dashboard$/)
      expect(await page.evaluate(() => localStorage.getItem('sfpanel_current_node'))).toBe('a-1')
    })

    test.describe('Korean', () => {
      test.use({ locale: 'ko-KR' })

      test('the progress reads in Korean', async ({ page }) => {
        await page.addInitScript(() => localStorage.setItem('sfpanel_language', 'ko'))
        await page.clock.setFixedTime(NOW)
        const waiting = { s1: srv('s1', 'running', ['running'], { host: '192.0.2.21', name: 'web-04', awaiting: 'host_key', host_key: FINGERPRINT }), s2: srv('s2', 'pending', []) }
        const failed = ended({ id: 'i-2', credentials_until: new Date(NOW.getTime() + 65_000).toISOString() })
        await mock(page, { runDetails: { ...follow(install(), waiting).runDetails, ...follow(failed, { s1: SUCCEEDED, s2: OFFLINE }, 'i-2').runDetails },
          streams: { ...follow(install(), waiting).streams, ...follow(failed, { s1: SUCCEEDED, s2: OFFLINE }, 'i-2').streams } })
        await page.goto(`/cluster/tasks/runs/${IRUN}`)
        const p = runPane(page)
        await expect(p.getByRole('heading', { name: '에이전트 설치 (SSH)' })).toBeVisible()
        await expect(p.getByTestId('install-run-size')).toHaveText('2대 · 한 번에 2대')
        await expect(matrix(page).getByRole('columnheader')).toHaveText(['서버', '접속', '사전 점검', '설치', '등록', '연결 확인'])
        await expect(wait(page, 's1')).toContainText('web-04에 처음 접속합니다')
        await expect(wait(page, 's1').getByRole('button', { name: '맞음, 계속' })).toBeVisible()
        await expect(wait(page, 's1').getByRole('button', { name: '아니요, 중단' })).toBeVisible()
        await expect(p.getByRole('button', { name: '중지', exact: true })).toBeVisible()

        await page.goto('/cluster/tasks/runs/i-2')
        await expect(p.getByTestId('install-credentials')).toContainText('1:05 뒤 지웁니다')
        await expect(server(page, 's2').getByRole('button', { name: '다시 확인' })).toBeVisible()
        await expect(server(page, 's2').getByRole('button', { name: '되돌리기' })).toBeVisible()
        await expect(server(page, 's1').getByRole('button', { name: '노드 열기' })).toBeVisible()
        await expect(server(page, 's2')).toContainText('3629')
      })
    })

    test.describe('phone', () => {
      test.use({ viewport: { width: 412, height: 860 }, hasTouch: true })

      test('the questions, the servers and their buttons fit the screen', async ({ page }) => {
        const rows = {
          s1: srv('s1', 'running', ['running'], { host: '192.0.2.21', name: 'web-04', awaiting: 'host_key', host_key: FINGERPRINT }),
          s2: OFFLINE,
        }
        await mock(page, follow(install(), rows))
        await page.goto(`/cluster/tasks/runs/${IRUN}`)
        await expect(page.getByTestId('task-run-node-card')).toHaveCount(2)
        // A row's reason is worded once, below the cards: never its code.
        await expect(server(page, 's2')).toContainText("the agent cannot reach the managers' port 3629")
        await expect(runPane(page)).not.toContainText('AGENT_INSTALL_')
        for (const el of [
          wait(page, 's1').getByTestId('install-host-key'),
          wait(page, 's1').getByRole('button', { name: 'Yes, continue' }),
          wait(page, 's1').getByRole('button', { name: 'No, stop' }),
          server(page, 's2').getByRole('button', { name: 'Revert' }),
        ]) {
          await el.scrollIntoViewIfNeeded()
          const box = await el.boundingBox()
          expect(box && box.x >= 0 && box.x + box.width <= 412).toBe(true)
        }
        expect(await runPane(page).evaluate((d) => d.scrollWidth <= d.clientWidth)).toBe(true)
      })
    })
  })
})
