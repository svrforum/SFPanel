import { test, expect, type Page } from '@playwright/test'

// The cluster's sidebar was two columns: a tree of nodes and a datacenter, and
// beside it a menu that changed with what the tree had selected. Picking a node
// always opened its dashboard, and the same names (Running, Docker, Settings)
// were in the menu twice. It is now one list, the same in both modes, with the
// server the pages act on named at the top and picked from there. Mocked API:
// three managers and forty agents in groups db and web, or in none; the node
// list answers late, as a leader does.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const base = { role: 'voter', status: 'online', grpc_address: '192.0.2.1:3629', joined_at: '2026-01-01T00:00:00Z', last_seen: '2026-01-01T00:00:00Z' }
const managers = ['m-1', 'm-2', 'm-3'].map((id, i) => ({ ...base, id, name: id, kind: 'manager', api_address: `192.0.2.${i + 1}:3628` }))
const groupOf = (i: number) => (i % 3 === 0 ? 'db' : i % 3 === 1 ? 'web' : '')
const agents = Array.from({ length: 40 }, (_, k) => {
  const i = k + 1
  const group = groupOf(i)
  return {
    ...base, id: `agent-${i}`, name: `agent-${i}`, kind: 'agent', role: '',
    api_address: `198.51.100.${i}:3628`, labels: group ? { group } : {},
  }
})
// Listed out of order on purpose: the picker sorts.
const nodes = [...agents.slice().reverse(), ...managers.slice().reverse()]
const metrics = {
  cpu: 12, mem_total: 8 * 1024 ** 3, mem_used: 2 * 1024 ** 3, mem_percent: 25,
  swap_total: 0, swap_used: 0, swap_percent: 0, disk_total: 100 * 1024 ** 3,
  disk_used: 30 * 1024 ** 3, disk_percent: 30, net_bytes_sent: 0, net_bytes_recv: 0, timestamp: 0,
}

interface Opts {
  storage?: Record<string, string>
  list?: () => unknown[]
  nodesDown?: boolean
  nodesDelay?: number
  versions?: Record<string, string>
  unreachable?: string
  standalone?: boolean
}

async function mock(page: Page, opts: Opts = {}) {
  const requests: string[] = []
  await page.addInitScript((storage) => {
    sessionStorage.setItem('token', 'fixture-token')
    // Only on the first load: a reload must find what the page itself stored.
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
    else if (path === '/cluster/status') {
      data = opts.standalone
        ? { enabled: false }
        : { enabled: true, name: 'lab', node_count: nodes.length, leader_id: 'm-1', local_id: 'm-1', is_leader: true }
    }
    else if (path === '/cluster/nodes') {
      // A mock answers at once; a leader answers after the page has drawn.
      await new Promise((resolve) => setTimeout(resolve, opts.nodesDelay ?? 600))
      if (opts.nodesDown) {
        // What a follower answers with the leader unreachable.
        await route.fulfill({ status: 503, json: { success: false, error: { code: 'CLUSTER_NOT_LEADER', message: 'leader unreachable' } } })
        return
      }
      data = { nodes: opts.list?.() ?? nodes, local_id: 'm-1', is_leader: true }
    }
    else if (path === '/cluster/overview') data = { name: 'lab', node_count: nodes.length, leader_id: 'm-1', nodes, metrics: [] }
    else if (path === '/cluster/events') data = { events: [] }
    else if (path === '/system/overview') data = {
      version: opts.versions ? opts.versions[node] : '0.80.0',
      host: { hostname: `${node}-host`, os: 'linux', platform: 'debian', platform_version: '13', kernel: '6.12', uptime: 3600, num_cpu: 4 },
      metrics, metrics_history: [],
    }
    else if (opts.unreachable && node === opts.unreachable) {
      // What a manager answers for an agent its probe could not reach (proxy.go).
      const name = opts.unreachable
      await route.fulfill({ status: 502, json: { success: false, error: { code: 'CLUSTER_NODE_UNREACHABLE', message: `${name} is not reachable from this manager; open the panel on a manager on its network`, details: { name } } } })
      return
    }
    // The dashboard's lists, so the page draws instead of its error screen.
    else if (/^\/(system\/processes|system\/services|filesystems|network\/interfaces|docker\/containers|docker\/compose)$/.test(path) || /history/.test(path)) data = []
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  return requests
}

const picker = (page: Page) => page.getByRole('button', { name: /Choose a server/ })

test('the menu is one list: 14 entries in a cluster', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/overview')
  const menu = page.getByRole('navigation', { name: 'Main menu' })
  await expect(menu.getByRole('link')).toHaveCount(14)
  expect(await menu.getByRole('link').evaluateAll((a) => a.map((l) => new URL((l as HTMLAnchorElement).href).pathname)))
    .toEqual(['/terminal', '/dashboard', '/docker', '/files', '/logs', '/services', '/network', '/firewall', '/disk', '/cluster/running', '/map', '/cluster/tasks', '/cluster', '/settings'])
  await expect(menu.getByText('All servers')).toBeVisible()
})

test('a single server has 12, and its name without a picker', async ({ page }) => {
  await mock(page, { standalone: true })
  await page.goto('/dashboard')
  await expect(page.getByRole('navigation', { name: 'Main menu' }).getByRole('link')).toHaveCount(12)
  await expect(picker(page)).toHaveCount(0)
  await expect(page.locator('aside').getByText('m-1-host')).toBeVisible()
})

test('fits 1366×768: settings and sign-out in view', async ({ page }) => {
  await page.setViewportSize({ width: 1366, height: 768 })
  await mock(page)
  await page.goto('/dashboard')
  await expect(page.getByRole('navigation', { name: 'Main menu' }).getByRole('link', { name: 'Settings' })).toBeInViewport()
  await expect(page.getByRole('button', { name: 'Logout' })).toBeInViewport()
})

test('the picker lists managers, then groups, searchable from the start', async ({ page }) => {
  await mock(page)
  await page.goto('/dashboard')
  await page.getByRole('button', { name: /Choose a server: m-1/ }).click()
  const pop = page.getByRole('dialog')
  await expect(pop.getByRole('searchbox', { name: 'Search servers' })).toBeVisible()
  await expect(pop.getByRole('region', { name: 'Managers' }).getByRole('button')).toHaveCount(3)
  await expect(pop.getByRole('region', { name: 'db' }).getByRole('button').first()).toHaveAccessibleName(/^agent-3/)
  // In this order: the managers, the groups by name, then the agents in none.
  const regions = pop.getByRole('region')
  await expect(regions).toHaveCount(4)
  for (const [i, name] of ['Managers', 'db', 'web', 'No group'].entries()) await expect(regions.nth(i)).toHaveAccessibleName(name)
  await pop.getByRole('searchbox', { name: 'Search servers' }).fill('agent-37')
  await expect(pop.getByRole('region')).toHaveCount(1)
  await expect(pop.getByRole('region', { name: 'web' })).toBeVisible()
})

// The rail drew each node as a coloured dot named only by the node: a node
// that went offline changed colour and nothing else.
test('names each server with its state, the leader and the attached one', async ({ page }) => {
  await mock(page, { list: () => nodes.map((n) => (n.id === 'agent-5' ? { ...n, status: 'offline' } : n)) })
  await page.goto('/dashboard')
  await expect(picker(page)).toContainText('1 offline')
  await picker(page).click()
  const pop = page.getByRole('dialog')
  await expect(pop.getByRole('button', { name: /^agent-5.*Offline/ })).toBeVisible()
  const self = pop.getByRole('button', { name: /^m-1/ })
  await expect(self).toContainText('Leader')
  await expect(self).toContainText('Attached')
  await expect(self).toHaveAttribute('aria-pressed', 'true')
})

// The head shows the server's role, whether it is the one this panel runs
// on and how many servers are off, but the button's name said only which
// server it was. A screen reader hears the rest as its description, collapsed
// too, where the line is not drawn.
test("the button's description carries the role, the attached server and the servers off", async ({ page }) => {
  await mock(page, { list: () => nodes.map((n) => (n.id === 'agent-5' ? { ...n, status: 'offline' } : n)) })
  await page.goto('/dashboard')
  await expect(picker(page)).toContainText('1 offline')
  await expect(picker(page)).toHaveAccessibleDescription('Manager · Attached · 1 offline')
  await picker(page).click()
  await page.getByRole('dialog').getByRole('button', { name: /^agent-1\b/ }).click()
  await expect(picker(page)).toHaveAccessibleDescription('Agent (group web) · 1 offline')
  await page.getByRole('button', { name: 'Collapse sidebar' }).click()
  await expect(picker(page)).toHaveText(/^\s*ag\s*$/)
  await expect(picker(page)).toHaveAccessibleDescription('Agent (group web) · 1 offline')
})

// Picking a node in the tree always opened its dashboard, whatever was open.
test('switching stays on the page and replaces the address', async ({ page }) => {
  const requests = await mock(page)
  await page.goto('/services?node=m-1')
  const before = await page.evaluate(() => history.length)
  await picker(page).click()
  await page.getByRole('dialog').getByRole('button', { name: /^agent-1\b/ }).click()
  await expect(page).toHaveURL(/\/services\?node=agent-1$/)
  expect(await page.evaluate(() => history.length)).toBe(before)
  await expect.poll(() => requests.some((r) => r.startsWith('/system/services') && r.includes('node=agent-1'))).toBe(true)
  await expect(page.getByRole('dialog')).toHaveCount(0)
})

test('on an all-servers page, picking opens that server\'s dashboard', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/overview')
  await picker(page).click()
  await page.getByRole('dialog').getByRole('button', { name: /^agent-1\b/ }).click()
  await expect(page).toHaveURL(/\/dashboard\?node=agent-1$/)
})

// The menu's header named a node it had not looked up yet "Unknown node", and
// kept saying so while the leader could not be reached.
test('the button has a name while the list cannot be had', async ({ page }) => {
  await mock(page, { nodesDown: true })
  await page.goto('/dashboard')
  await expect(picker(page)).toContainText('m-1-host')
  await expect(page.getByText('Unknown node')).toHaveCount(0)
})

// Before the first list the picker is empty because nothing has come yet, not
// because no server matches or because the list failed.
test('a late list: the picker shows loading, then the list', async ({ page }) => {
  await mock(page, { nodesDelay: 1500 })
  await page.goto('/dashboard')
  const open = picker(page)
  for (let i = 0; i < 3; i++) {
    await open.click()
    const pop = page.getByRole('dialog')
    await expect(pop.getByRole('searchbox', { name: 'Search servers' })).toBeVisible()
    // As drawn now, not eventually: the list that arrives would hide a wrong answer.
    expect(await pop.getByText('No matching servers').count()).toBe(0)
    expect(await pop.getByText('Server list unavailable').count()).toBe(0)
    if (i === 0) await expect(pop.getByText('Loading…')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(pop).toHaveCount(0)
  }
  await open.click()
  await expect(page.getByRole('dialog').getByRole('region', { name: 'Managers' }).getByRole('button')).toHaveCount(3)
})

// The footer showed the version of the node selected when the page loaded,
// whichever node was open after that.
test('the version under the menu is the open server\'s', async ({ page }) => {
  await mock(page, { versions: { 'm-1': '0.80.0', 'agent-5': '0.79.3' } })
  await page.goto('/dashboard')
  const version = page.locator('aside').getByRole('link', { name: /^v\d/ })
  await expect(version).toHaveText('v0.80.0')

  await picker(page).click()
  await page.getByRole('dialog').getByRole('button', { name: /^agent-5\b/ }).click()
  await expect(page).toHaveURL(/\/dashboard\?node=agent-5$/)
  await expect(version).toHaveText('v0.79.3')

  await picker(page).click()
  await page.getByRole('dialog').getByRole('button', { name: /^m-1\b/ }).click()
  await expect(page).toHaveURL(/\/dashboard\?node=m-1$/)
  await expect(version).toHaveText('v0.80.0')
})

test('shows a change the page reports at once', async ({ page }) => {
  let list: unknown[] = nodes
  await mock(page, { list: () => list })
  await page.goto('/cluster/overview')
  await picker(page).click()
  const ungrouped = page.getByRole('dialog').getByRole('region', { name: 'No group' })
  await expect(ungrouped.getByRole('button')).toHaveCount(13)

  list = [...nodes, { ...base, id: 'agent-41', name: 'agent-41', kind: 'agent', role: '', api_address: '198.51.100.41:3628' }]
  await page.evaluate(() => window.dispatchEvent(new Event('sfpanel:cluster-changed')))
  // Well inside the list's 15-second poll.
  await expect(ungrouped.getByRole('button', { name: /^agent-41\b/ })).toBeVisible({ timeout: 3000 })
})

test('collapsed, the head keeps the state and two letters', async ({ page }) => {
  await mock(page, { storage: { 'sfpanel-sidebar-collapsed': 'true' } })
  await page.goto('/dashboard')
  await expect(page.getByRole('button', { name: /Choose a server: m-1/ })).toHaveText(/^\s*m-\s*$/)
})

// A node this manager cannot reach used to time out after thirty seconds and
// say nothing of why. The manager now refuses at once, and the page says which
// node and where to open it instead.
test('a node this manager cannot reach says so by name', async ({ page }) => {
  await mock(page, {
    storage: { sfpanel_current_node: 'agent-5' },
    unreachable: 'agent-5',
  })
  await page.goto('/docker/stacks')
  await expect(page.getByText('This manager cannot reach agent-5. Open the panel on a manager on its network (LAN or Tailscale).').first()).toBeVisible()
  await expect(page.getByText('is not reachable from this manager')).toHaveCount(0)
})
