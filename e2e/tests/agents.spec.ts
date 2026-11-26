import { test, expect, type Page } from '@playwright/test'

// The agents page: the cluster's Zabbix and Wazuh connection values, set once,
// and every server's two agents as that server reads them: whether each
// really reaches its server, not whether its service runs. Deploying opens a
// group task run. Mocked API only: node-a (the leader, attached) whose agents
// are connected; agent-1, whose agents both run and still point at a server
// that is gone; agent-2, offline; node-b, a manager older than the status
// route. The node list answers late, as a busy cluster's does.
test.use({ viewport: { width: 1280, height: 900 }, locale: 'ko-KR' })

// ---------------------------------------------------------------- fixture --

const seen = '2026-11-26T00:00:00Z'
const base = { joined_at: seen, last_seen: seen }
const NODES = [
  { ...base, id: 'm-1', name: 'node-a', kind: 'manager', role: 'voter', status: 'online', api_address: '192.0.2.1:3628', grpc_address: '192.0.2.1:3629' },
  { ...base, id: 'a-1', name: 'agent-1', kind: 'agent', role: '', status: 'online', api_address: '198.51.100.1:3628', grpc_address: '198.51.100.1:3629' },
  { ...base, id: 'a-2', name: 'agent-2', kind: 'agent', role: '', status: 'offline', api_address: '198.51.100.2:3628', grpc_address: '198.51.100.2:3629' },
  { ...base, id: 'm-2', name: 'node-b', kind: 'manager', role: 'voter', status: 'online', api_address: '192.0.2.2:3628', grpc_address: '192.0.2.2:3629' },
]

const SETTINGS = {
  saved: true,
  zabbix: { server: '192.0.2.10', server_active: '192.0.2.10', host_metadata: 'linux-fleet-auto' },
  wazuh: { manager: '192.0.2.10', groups: '', version: '4.14.10' },
  updated_at: seen,
  updated_by: 'admin',
}
// Before anyone saves: every value empty but the default Wazuh version.
const UNSAVED = {
  saved: false,
  zabbix: { server: '', server_active: '', host_metadata: '' },
  wazuh: { manager: '', groups: '', version: '4.14.10' },
}

const os = { id: 'ubuntu', id_like: 'debian', version_id: '24.04', codename: 'noble', pretty: 'Ubuntu 24.04.3 LTS' }
/** GET /agents/status, by the node it is asked of. */
const STATUS: Record<string, unknown> = {
  'm-1': {
    os,
    zabbix: {
      state: 'installed', version: '1:7.0.32-2+ubuntu24.04', source: 'vendor', service: 'active',
      config: { server: '192.0.2.10', server_active: '192.0.2.10', hostname: 'node-a', host_metadata: 'linux-fleet-auto' },
      connection: { state: 'connected' },
    },
    wazuh: {
      state: 'installed', version: '4.14.10-1', held: true, service: 'active',
      config: { servers: ['192.0.2.10'], enrollment_manager: '192.0.2.10', groups: '' },
      connection: { state: 'connected', last_keepalive: '2026-11-26 10:00:00', last_ack: '2026-11-26 10:00:02' },
    },
    checked_at: seen,
  },
  // Both services active, both pointed at a server that no longer answers.
  'a-1': {
    os,
    zabbix: {
      state: 'installed', version: '1:7.0.22+dfsg-1', source: 'distro', service: 'active',
      config: { server: '192.0.2.19', server_active: '192.0.2.19', hostname: 'agent-1', host_metadata: '' },
      connection: { state: 'unreachable', detail: 'dial tcp 192.0.2.19:10051: i/o timeout' },
    },
    wazuh: {
      state: 'installed', version: '4.14.10-1', service: 'active',
      config: { servers: ['192.0.2.19'], enrollment_manager: '192.0.2.19', groups: '' },
      connection: { state: 'pending', last_keepalive: '2026-11-20 08:00:00' },
    },
    checked_at: seen,
  },
}

interface Opts {
  /** GET /agents/settings answers that nothing has been saved yet. */
  unsaved?: boolean
  /** The server the operator looked at last, which the shell keeps for server pages. */
  current?: string
}

interface Call { method: string; search: string; body: unknown }

/**
 * Mocks the panel's API. asked has the node= of every status read; settings
 * every request to /agents/settings, reads included.
 */
async function mock(page: Page, opts: Opts = {}) {
  const asked: string[] = []
  const settings: Call[] = []
  let saved: unknown = opts.unsaved ? UNSAVED : SETTINGS
  await page.addInitScript((current) => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('sfpanel_language', 'ko')
    if (current) localStorage.setItem('sfpanel_current_node', current)
  }, opts.current ?? '')
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname.replace('/api/v1', '')
    const fail = (status: number, code: string, message: string, details?: Record<string, string>) =>
      route.fulfill({ status, json: { success: false, error: { code, message, details } } })
    const late = () => new Promise((resolve) => setTimeout(resolve, 800))
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/auth/ws-ticket') data = { ticket: 'fixture-ticket' }
    else if (path === '/cluster/status') data = { enabled: true, name: 'lab', node_count: NODES.length, leader_id: 'm-1', local_id: 'm-1', is_leader: true }
    else if (path === '/cluster/nodes') {
      await late()
      data = { nodes: NODES, local_id: 'm-1', is_leader: true }
    } else if (path === '/cluster/overview') {
      await late()
      data = { name: 'lab', node_count: NODES.length, leader_id: 'm-1', nodes: NODES, metrics: [] }
    } else if (path === '/cluster/events') data = { events: [] }
    else if (path === '/cluster/tasks' || path === '/cluster/task-runs') data = []
    else if (path === '/agents/status') {
      const node = url.searchParams.get('node') ?? ''
      asked.push(node)
      // node-b's panel predates the route: its router answers as for any unknown path.
      if (node === 'm-2') return fail(404, 'NOT_FOUND', 'No such endpoint')
      if (!STATUS[node]) return fail(502, 'CLUSTER_NODE_UNREACHABLE', 'The node could not be reached')
      data = STATUS[node]
    } else if (path === '/agents/settings') {
      const method = req.method()
      const body = req.postData() ? req.postDataJSON() : null
      settings.push({ method, search: url.search, body })
      if (method === 'PUT') {
        const active = (body as { zabbix?: { server_active?: string } }).zabbix?.server_active ?? ''
        if (active.endsWith(':70000')) {
          return fail(400, 'SERVER_AGENT_SETTINGS_INVALID', 'zabbix.server_active: port "70000" must be 1-65535', { field: 'zabbix.server_active' })
        }
        saved = { ...(body as object), saved: true, updated_at: seen, updated_by: 'admin' }
      }
      data = saved
    }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  return { asked, settings }
}

// ---------------------------------------------------------------- helpers --

const row = (page: Page, id: string) => page.getByTestId(`agent-row-${id}`)
const field = (page: Page, name: string) => page.getByLabel(name, { exact: true })
const FIELDS = {
  zabbixServer: 'Zabbix 서버 (passive, 쉼표로 여러 개)',
  zabbixActive: 'Zabbix active 주소 (호스트[:포트])',
  zabbixMetadata: '자동 등록 메타데이터',
  wazuhManager: 'Wazuh 매니저 주소',
  wazuhGroups: 'Wazuh 그룹 (비우면 default)',
  wazuhVersion: 'Wazuh 에이전트 버전',
}

// ------------------------------------------------------------------ tests --

test('an agent that runs but does not reach its server reads as not connected', async ({ page }) => {
  const { asked } = await mock(page)
  await page.goto('/agents')
  await expect(page.getByRole('heading', { level: 1, name: '에이전트' })).toBeVisible()

  // Both of agent-1's services are active; neither agent is connected.
  const a1 = row(page, 'a-1')
  await expect(a1).toContainText('실행 중')
  await expect(a1).toContainText('서버에 닿지 않음')
  await expect(a1).toContainText('dial tcp 192.0.2.19:10051: i/o timeout')
  await expect(a1).toContainText('다름: Server=192.0.2.19 · ServerActive=192.0.2.19')
  await expect(a1).toContainText('대기(pending)')
  await expect(a1).not.toContainText('연결됨')

  const m1 = row(page, 'm-1')
  await expect(m1.getByText('연결됨', { exact: true })).toHaveCount(2)
  await expect(m1).toContainText('고정됨(hold)')
  await expect(m1).toContainText('마지막 keepalive 2026-11-26 10:00:00')

  await expect(row(page, 'a-2')).toContainText('오프라인')
  await expect(row(page, 'm-2')).toContainText('업데이트하세요')

  // Each status is read on its own server; an offline one is not asked.
  expect([...new Set(asked)].sort()).toEqual(['a-1', 'm-1', 'm-2'])
})

test('saving the connection values sends them and keeps them', async ({ page }) => {
  // The operator looked at agent-1 last: a request that is not the attached
  // manager's own would carry node=a-1, to an agent that holds no settings.
  const { settings } = await mock(page, { current: 'a-1' })
  await page.goto('/agents')
  await expect(field(page, FIELDS.zabbixServer)).toHaveValue('192.0.2.10')

  await field(page, FIELDS.zabbixServer).fill('192.0.2.10,198.51.100.7')
  await field(page, FIELDS.zabbixActive).fill('192.0.2.10')
  await field(page, FIELDS.zabbixMetadata).fill('linux-fleet-auto')
  await field(page, FIELDS.wazuhManager).fill('192.0.2.10')
  await field(page, FIELDS.wazuhGroups).fill('linux')
  await field(page, FIELDS.wazuhVersion).fill('4.14.10')
  await page.getByRole('button', { name: '저장', exact: true }).click()
  await expect(page.getByText('저장했습니다')).toBeVisible()

  const puts = settings.filter((c) => c.method === 'PUT')
  expect(puts).toHaveLength(1)
  // The values alone: what was saved when and by whom is the server's to say.
  expect(puts[0].body).toEqual({
    zabbix: { server: '192.0.2.10,198.51.100.7', server_active: '192.0.2.10', host_metadata: 'linux-fleet-auto' },
    wazuh: { manager: '192.0.2.10', groups: 'linux', version: '4.14.10' },
  })
  // Read and written on the manager this browser is attached to.
  expect(settings.some((c) => c.method === 'GET')).toBe(true)
  for (const c of settings) expect(c.search, `${c.method} /agents/settings`).toBe('')
  await expect(field(page, FIELDS.zabbixServer)).toHaveValue('192.0.2.10,198.51.100.7')
  await expect(field(page, FIELDS.wazuhGroups)).toHaveValue('linux')

  // A value the manager refuses: its reason sits under that value.
  await field(page, FIELDS.zabbixActive).fill('192.0.2.10:70000')
  await page.getByRole('button', { name: '저장', exact: true }).click()
  const active = field(page, FIELDS.zabbixActive)
  await expect(active).toHaveAttribute('aria-invalid', 'true')
  await expect(active).toHaveAccessibleDescription(/^연결값이 올바르지 않습니다: Zabbix active 주소 \(호스트\[:포트\]\)\s*port "70000" must be 1-65535$/)
  await expect(field(page, FIELDS.zabbixServer)).toHaveAttribute('aria-invalid', 'false')
  await expect(page.getByText('저장했습니다')).toHaveCount(0)
})

test('deploy opens the group task run with the chosen servers', async ({ page }) => {
  await mock(page)
  await page.goto('/agents')
  await expect(row(page, 'a-1')).toContainText('서버에 닿지 않음')
  // An offline server cannot be picked.
  await expect(row(page, 'a-2').getByRole('checkbox')).toBeDisabled()
  // Picked out of order: the run lists them as the table does.
  await row(page, 'a-1').getByRole('checkbox').click()
  await row(page, 'm-1').getByRole('checkbox').click()
  await page.getByRole('link', { name: 'Zabbix 배포', exact: true }).click()

  await expect(page).toHaveURL(/\/cluster\/tasks\/builtin%2Fzabbix-agent\?/)
  const url = new URL(page.url())
  expect(url.pathname).toBe('/cluster/tasks/builtin%2Fzabbix-agent')
  expect(url.searchParams.get('nodes')).toBe('m-1,a-1')
})

test('re-apply takes every server that has the agent', async ({ page }) => {
  await mock(page)
  await page.goto('/agents')
  // node-b could not say whether it has one, and agent-2 is offline.
  await page.getByRole('link', { name: 'Wazuh 연결값 다시 적용 (2대)', exact: true }).click()

  await expect(page).toHaveURL(/\/cluster\/tasks\/builtin%2Fwazuh-agent\?/)
  const url = new URL(page.url())
  expect(url.pathname).toBe('/cluster/tasks/builtin%2Fwazuh-agent')
  expect(url.searchParams.get('nodes')).toBe('m-1,a-1')
})

test('without saved values deploying is off and says why', async ({ page }) => {
  await mock(page, { unsaved: true })
  await page.goto('/agents')
  // A server is picked, so the values are the only thing missing.
  await row(page, 'm-1').getByRole('checkbox').click()
  const deploy = page.getByRole('button', { name: 'Zabbix 배포', exact: true })
  await expect(deploy).toBeDisabled()
  await expect(deploy).toHaveAccessibleDescription('먼저 연결값을 저장하세요')
  await expect(page.getByText('먼저 연결값을 저장하세요')).toBeVisible()
})

test('on a phone each server is a card', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await mock(page)
  await page.goto('/agents')
  const card = page.getByTestId('agent-card-a-1')
  await expect(card).toBeVisible()
  await expect(card).toContainText('서버에 닿지 않음')
  await expect(row(page, 'a-1')).toBeHidden()
})
