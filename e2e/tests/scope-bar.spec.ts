import { test, expect, type Page } from '@playwright/test'

// In a cluster, one line above every page says what the page acts on: the
// server it names, marked when that is not the server this panel runs on; all
// servers, with how many answer; or where a setting is saved. A server the
// address names that the cluster does not have is said to be unknown, where
// before the page moved to another server without a word. Not on the
// terminal, whose rows it would take, and not on a single server. Mocked API:
// managers m-1 (attached, leader) and m-2, agents agent-1 (group web) and
// agent-2 (offline); the node list answers late, as a leader does.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const base = { role: 'voter', status: 'online', joined_at: '2026-01-01T00:00:00Z', last_seen: '2026-01-01T00:00:00Z' }
const nodes = [
  { ...base, id: 'm-1', name: 'm-1', kind: 'manager', api_address: '192.0.2.1:3628', grpc_address: '192.0.2.1:3629' },
  { ...base, id: 'm-2', name: 'm-2', kind: 'manager', api_address: '192.0.2.2:3628', grpc_address: '192.0.2.2:3629' },
  { ...base, id: 'agent-1', name: 'agent-1', kind: 'agent', role: '', labels: { group: 'web' }, api_address: '198.51.100.1:3628', grpc_address: '198.51.100.1:3629' },
  { ...base, id: 'agent-2', name: 'agent-2', kind: 'agent', role: '', status: 'offline', api_address: '198.51.100.2:3628', grpc_address: '198.51.100.2:3629' },
]
// Enough units that the page scrolls under the line.
const services = Array.from({ length: 40 }, (_, i) => ({
  name: `unit-${i}.service`, description: `unit-${i} fixture`, load_state: 'loaded', active_state: 'active', sub_state: 'running', enabled: 'enabled',
}))
const session = { id: 'abcdef123456', title: 'Scope test', tool: 'shell', run_as: 'tester', cwd: '/srv/app', state: 'shell', persistence: 'service', attached: true, created_at: '2026-01-01T00:00:00Z' }

interface MockOptions {
  /** Written to localStorage on the first load only: a reload finds what the page stored. */
  storage?: Record<string, string>
  /** The node list the cluster answers with, read on every ask. */
  list?: () => unknown[]
  /** A single server: cluster status says not enabled. */
  standalone?: boolean
}

async function mock(page: Page, opts: MockOptions = {}) {
  const requests: string[] = []
  await page.addInitScript((storage) => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    if (sessionStorage.getItem('fixture-seeded')) return
    sessionStorage.setItem('fixture-seeded', '1')
    for (const [k, v] of Object.entries(storage)) localStorage.setItem(k, v)
  }, opts.storage ?? {})
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace('/api/v1', '')
    const node = url.searchParams.get('node') ?? 'm-1'
    requests.push(path + url.search)
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/auth/ws-ticket') data = { ticket: 'fixture-ticket' }
    else if (path === '/cluster/status') data = opts.standalone
      ? { enabled: false }
      : { enabled: true, name: 'lab', node_count: 4, leader_id: 'm-1', local_id: 'm-1', is_leader: true }
    else if (path === '/cluster/nodes') {
      await new Promise((resolve) => setTimeout(resolve, 600))
      data = { nodes: opts.list?.() ?? nodes, local_id: 'm-1', is_leader: true }
    }
    else if (path === '/cluster/overview') data = { name: 'lab', node_count: 4, leader_id: 'm-1', nodes, metrics: [] }
    else if (path === '/cluster/events') data = { events: [] }
    else if (path === '/system/overview') data = {
      version: '0.94.0',
      host: { hostname: `${node}-host`, os: 'linux', platform: 'debian', platform_version: '13', kernel: '6.12', uptime: 3600, num_cpu: 4 },
      metrics: { cpu: 12, mem_percent: 25, disk_percent: 30 }, metrics_history: [],
    }
    else if (path === '/system/services') data = { services, total: services.length }
    else if (path === '/docker/compose' || path === '/docker/containers') data = []
    else if (path === '/alerts/history') data = { items: [], total: 0 }
    else if (path === '/alerts/channels' || path === '/alerts/rules') data = []
    else if (path === '/ai/sessions') data = [session]
    else if (path === '/ai/tools') data = { tmux: { installed: true, supported: true, version: '3.4', min_version: '3.2' }, systemd_run: true, accounts: ['tester'], panel_account: 'tester', account: 'tester', tools: {} }
    else if (path === '/terminal/info') data = { shell_user: 'tester', is_root: false, hostname: 'fixture' }
    else if (path === '/terminal/sessions') data = { sessions: [] }
    else if (/batch|history|events|metrics/.test(path)) data = []
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  return requests
}

const bar = (page: Page) => page.locator('[data-scope-bar]')

test('a server page names its server; one other than the attached is marked', async ({ page }) => {
  await mock(page)
  await page.goto('/services?node=agent-1')
  await expect(bar(page)).toContainText('Server agent-1 · Agent (group web)')
  await expect(bar(page).locator('[data-remote="true"]')).toBeVisible()
  await page.goto('/services?node=m-1')
  await expect(bar(page)).toContainText('Server m-1 · Manager')
  await expect(bar(page).locator('[data-remote="false"]')).toBeVisible()
  await expect(bar(page).locator('[data-remote="true"]')).toHaveCount(0)
})

test('the server in the line opens the picker, and a pick stays on the page', async ({ page }) => {
  const requests = await mock(page)
  await page.goto('/services?node=m-1')
  await bar(page).getByRole('button', { name: /^Server m-1/ }).click()
  await page.getByRole('dialog', { name: 'Choose a server' }).getByRole('button', { name: /^agent-1/ }).click()
  await expect(page).toHaveURL(/\/services\?node=agent-1$/)
  await expect(bar(page)).toContainText('Server agent-1 · Agent (group web)')
  await expect.poll(() => requests.some((r) => r.startsWith('/system/services') && r.includes('node=agent-1'))).toBe(true)
})

test('an all-servers page counts them', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/running')
  await expect(bar(page)).toHaveText(/All servers · 3 of 4 online/)
})

test('settings say where they are saved', async ({ page }) => {
  await mock(page, { storage: { sfpanel_current_node: 'agent-1' } })
  await page.goto('/settings')
  await expect(bar(page)).toContainText('Same on every server')
  await page.goto('/settings?scope=node')
  await expect(bar(page)).toContainText('Saved on agent-1 only')
})

test('the terminal has no bar', async ({ page }) => {
  await mock(page)
  await page.goto('/terminal')
  await expect(page.locator('[data-terminal-session="active"]')).toBeVisible()
  // The shell knows it is in a cluster (the menu's head is the server picker)
  // and the node list has come: nothing the bar would wait for is on its way.
  // The menu's head, in the sidebar: the terminal's header has its own picker.
  await expect(page.locator('aside').getByRole('button', { name: /^Choose a server: m-1/ })).toContainText('Manager')
  await expect(bar(page)).toHaveCount(0)
})

test('on the terminal an unknown server is said in a notice', async ({ page }) => {
  await mock(page, { storage: { sfpanel_current_node: 'gone' } })
  await page.goto('/terminal')
  await expect(page.locator('[data-terminal-session="active"]')).toBeVisible()
  await expect(page.getByText('Server not found · gone')).toBeVisible()
  await expect(bar(page)).toHaveCount(0)
})

test('an unknown server is said to be unknown', async ({ page }) => {
  await mock(page)
  await page.goto('/services?node=gone')
  await expect(bar(page).getByRole('status')).toHaveText(/Server not found · gone/)
  await expect(bar(page)).toContainText('Server m-1')
})

test('the notice goes once the address names another server', async ({ page }) => {
  await mock(page)
  await page.goto('/services?node=gone')
  await expect(bar(page).getByRole('status')).toHaveText(/Server not found · gone/)
  // A link or Back to an address that names a server the cluster has.
  await page.evaluate(() => { history.pushState({}, '', '/services?node=agent-1'); dispatchEvent(new PopStateEvent('popstate')) })
  await expect(bar(page)).toContainText('Server agent-1')
  await expect(bar(page).getByText('Server not found')).toHaveCount(0)
})

test('a server that is offline is not unknown', async ({ page }) => {
  await mock(page)
  await page.goto('/services?node=agent-2')
  await expect(bar(page)).toContainText('Server agent-2')
  await page.waitForTimeout(1000)
  await expect(bar(page).getByText('Server not found')).toHaveCount(0)
  await expect(page).toHaveURL(/node=agent-2$/)
})

// Node ids are UUIDs; the operator knows the server by its name.
test('a server that leaves while shown is named by its name', async ({ page }) => {
  const leaving = { ...base, id: '3f2b9c1e-6a4d-4b8e-9f0a-1c2d3e4f5a6b', name: 'agent-07', kind: 'agent', role: '', api_address: '198.51.100.7:3628', grpc_address: '198.51.100.7:3629' }
  let list: unknown[] = [...nodes, leaving]
  await mock(page, { list: () => list })
  await page.goto(`/dashboard?node=${leaving.id}`)
  await expect(bar(page)).toContainText('Server agent-07 · Agent')
  list = nodes
  await page.evaluate(() => window.dispatchEvent(new Event('sfpanel:cluster-changed')))
  await expect(bar(page).getByRole('status')).toHaveText(/Server not found · agent-07/)
  await expect(page).toHaveURL(/node=m-1$/)
  await expect(page.getByText(/3f2b9c1e/)).toHaveCount(0)
})

test('a single server has no bar', async ({ page }) => {
  const requests = await mock(page, { standalone: true })
  await page.goto('/services')
  await expect(page.getByRole('heading', { level: 1, name: 'System' })).toBeVisible()
  await expect.poll(() => requests.includes('/cluster/status')).toBe(true)
  await page.waitForTimeout(800)
  await expect(bar(page)).toHaveCount(0)
})

test.describe('phone', () => {
  test.use({ viewport: { width: 412, height: 860 }, hasTouch: true })

  test('drawn in the Android app too, and not sticky', async ({ page }) => {
    await page.addInitScript(() => document.addEventListener('DOMContentLoaded', () => { document.documentElement.dataset.sfpanelAndroid = 'true' }))
    await mock(page)
    await page.goto('/services?node=agent-1')
    await expect(bar(page)).toBeVisible()
    await expect(bar(page)).toContainText('Server agent-1')
    expect(await page.evaluate(() => document.documentElement.dataset.sfpanelAndroid)).toBe('true')
    expect(await bar(page).evaluate((el) => getComputedStyle(el).position)).not.toBe('sticky')
  })
})

test('a desktop keeps the line at the top as the page scrolls, and it covers nothing at rest', async ({ page }) => {
  await mock(page)
  await page.goto('/services?node=m-1')
  await expect(bar(page)).toContainText('Server m-1')
  await expect(page.getByRole('row', { name: /unit-39\.service/ })).toBeAttached()
  const main = page.locator('#main-content')
  const mainTop = (await main.boundingBox())!.y
  const barTop = async () => (await bar(page).boundingBox())!.y
  // At rest: the first thing in the view, and the heading starts below it.
  expect(await barTop()).toBe(mainTop)
  const line = (await bar(page).boundingBox())!
  const heading = (await page.getByRole('heading', { level: 1, name: 'System' }).boundingBox())!
  expect(heading.y).toBeGreaterThanOrEqual(line.y + line.height)
  await main.evaluate((el) => { el.scrollTop = 600 })
  expect(await main.evaluate((el) => el.scrollTop)).toBeGreaterThan(0)
  await expect.poll(barTop).toBe(mainTop)
})
