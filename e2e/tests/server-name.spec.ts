import { test, expect, type Page } from '@playwright/test'

// In cluster mode every server page acts on one node — the one picked in the
// tree, else the one this panel runs on — and nothing on screen said which:
// the tab read "Dashboard · SFPanel" on all of them, and the dashboard's
// heading was the host's name, which is not the name the tree lists. Mocked
// API: a two-node cluster whose node list answers late, as a leader does.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const node = (id: string, name: string, ip: string) => ({
  id, name, kind: 'manager', role: 'voter', status: 'online',
  api_address: `${ip}:3628`, grpc_address: `${ip}:3629`,
  joined_at: '2026-01-01T00:00:00Z', last_seen: '2026-01-01T00:00:00Z',
})
const NODES = [node('n-1', 'node-a', '192.0.2.1'), node('n-2', 'node-b', '192.0.2.2')]
const NODES_DELAY_MS = 1500

const metrics = {
  cpu: 12, mem_total: 8 * 1024 ** 3, mem_used: 2 * 1024 ** 3, mem_percent: 25,
  swap_total: 0, swap_used: 0, swap_percent: 0, disk_total: 100 * 1024 ** 3,
  disk_used: 30 * 1024 ** 3, disk_percent: 30, net_bytes_sent: 0, net_bytes_recv: 0, timestamp: Date.now(),
}
const waiting = (id: string) => ({ id, title: id, tool: 'claude', run_as: 'tester', cwd: '/srv/app', state: 'waiting', persistence: 'service', attached: false, created_at: '2026-01-01T00:00:00Z' })

async function mock(page: Page, opts: { current?: string; nodesFail?: boolean } = {}) {
  const state = { sessions: [waiting('aaaaaa111111'), waiting('bbbbbb222222')] }
  await page.addInitScript((current) => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('sfpanel_language', 'en')
    localStorage.setItem('sfpanel-cluster-mode', 'true')
    if (current) localStorage.setItem('sfpanel_current_node', current)
  }, opts.current ?? '')
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace('/api/v1', '')
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/auth/ws-ticket') data = { ticket: 'fixture-ticket' }
    else if (path === '/cluster/status') data = { enabled: true, name: 'lab', node_count: 2, leader_id: 'n-1', local_id: 'n-1', is_leader: true }
    else if (path === '/cluster/nodes') {
      // A mock answers at once; a leader answers after the page has drawn.
      await new Promise((resolve) => setTimeout(resolve, NODES_DELAY_MS))
      if (opts.nodesFail) {
        await route.fulfill({ status: 503, json: { success: false, error: { code: 'CLUSTER_NOT_LEADER', message: 'leader unreachable' } } })
        return
      }
      data = { nodes: NODES, local_id: 'n-1', is_leader: true }
    }
    else if (path === '/cluster/overview') data = { name: 'lab', node_count: 2, leader_id: 'n-1', nodes: [], metrics: [] }
    else if (path === '/cluster/events') data = { events: [] }
    else if (path === '/system/overview') data = {
      host: { hostname: 'host-b', os: 'linux', platform: 'debian', platform_version: '13', kernel: '6.12', uptime: 3600, num_cpu: 4 },
      metrics, version: 'test', metrics_history: [],
    }
    else if (path === '/system/processes') data = []
    else if (path === '/system/backup/schedule') data = { schedule: null, files: [] }
    else if (path === '/filesystems') data = []
    else if (path === '/network/interfaces') data = []
    else if (path === '/docker/containers' || path === '/docker/compose') data = []
    else if (path === '/logs/sources') data = []
    else if (path === '/logs/read') data = { lines: [], total_lines: 0 }
    else if (path === '/ai/sessions') data = state.sessions
    else if (path === '/ai/tools') data = { tmux: { installed: true, supported: true, version: '3.4', min_version: '3.2' }, systemd_run: true, accounts: ['tester'], panel_account: 'tester', account: 'tester', tools: {} }
    else if (path === '/terminal/info') data = { shell_user: 'tester', is_root: false, hostname: 'host-b' }
    else if (path === '/terminal/sessions') data = { sessions: [] }
    else if (path === '/system/tuning') data = { categories: [], total_params: 0, applied: 0, pending_rollback: false, rollback_remaining: 0, system_info: { cpu_cores: 2, total_ram: 2147483648, kernel: '6.8' } }
    else if (path === '/alerts/history') data = { items: [], total: 0 }
    else if (path === '/alerts/channels' || path === '/alerts/rules') data = []
    else if (path === '/audit/logs') data = { logs: [], total: 0 }
    else if (/batch|history|events|guests/.test(path)) data = []
    await route.fulfill({ json: { success: true, data } })
  })
  return state
}

// The collapsed tree lists a node once the late list has come.
const listed = (page: Page) => expect(page.getByRole('button', { name: 'node-b · Online', exact: true })).toBeVisible()

test('the dashboard and the tab name the server the pages act on', async ({ page }) => {
  await mock(page, { current: 'n-2' })
  await page.goto('/dashboard')
  await expect(page.getByRole('heading', { level: 1, name: 'node-b' })).toBeVisible()
  // The host's own name stays in view under it. The closed server details
  // hold it too, so this has to be the visible line.
  await expect(page.getByText('Hostname · host-b', { exact: true })).toBeVisible()
  await expect(page).toHaveTitle('Dashboard · node-b · SFPanel')
  expect(await page.title()).not.toContain('host-b')
})

test('the attached server is named when no other is chosen', async ({ page }) => {
  await mock(page)
  await page.goto('/logs')
  await expect(page).toHaveTitle('Logs · node-a · SFPanel')
})

test('cluster and settings screens carry no server', async ({ page }) => {
  await mock(page)
  for (const [path, title] of [['/cluster/overview', 'Cluster · SFPanel'], ['/cluster/running', 'Running · SFPanel'], ['/settings', 'Settings · SFPanel']]) {
    await page.goto(path)
    await listed(page)
    await expect(page).toHaveTitle(title)
  }
})

test('a node list that fails leaves the hostname', async ({ page }) => {
  await mock(page, { nodesFail: true })
  await page.goto('/dashboard')
  await expect(page.getByTitle('Node list unavailable')).toBeVisible()
  await expect(page.getByRole('heading', { level: 1, name: 'host-b' })).toBeVisible()
  await expect(page.getByText('Hostname · host-b', { exact: true })).toHaveCount(0)
  await expect(page).toHaveTitle('Dashboard · SFPanel')
  await expect(page.getByText('Something went wrong')).toHaveCount(0)
})

// The terminal prefixes "(n) " for sessions waiting on the operator. The
// server's name comes after that, when the node list does, and must not take
// the count away; a count that changes later must not take the name away; on
// the way out the count goes and the name does not stay.
test('the terminal keeps its waiting count when the name arrives late', async ({ page }) => {
  const state = await mock(page, { current: 'n-2' })
  await page.goto('/terminal')
  // Two wait; the one on screen is not counted.
  await expect(page).toHaveTitle('(1) Terminal · node-b · SFPanel')
  // Another starts waiting, seen at the next 5-second poll.
  state.sessions = [...state.sessions, waiting('cccccc333333')]
  await expect(page).toHaveTitle('(2) Terminal · node-b · SFPanel', { timeout: 10_000 })
  // In the app, not a reload: the order of the terminal's cleanup and the
  // shell's title is what is under test.
  await page.getByRole('navigation').getByRole('link', { name: 'Settings', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Settings', level: 1 })).toBeVisible()
  await expect(page).toHaveTitle('Settings · SFPanel')
})
