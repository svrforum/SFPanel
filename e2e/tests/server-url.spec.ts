import { test, expect, type Page } from '@playwright/test'

// In a cluster the address names the server a page acts on: ?node=<id>, the
// attached server by its own id too, so a bookmark, a new tab or a reload opens
// the same server. The address wins over the server stored from an earlier
// visit; a link without ?node= gets the stored one back by replace; a server
// the node list no longer has falls back to the attached one; a single server
// never carries ?node=. Mocked API: managers m-1 (attached, leader) and m-2,
// agents agent-1 (group web) and agent-2 (offline). `requests` records each
// API path with its query; `window.__urls` each history entry the page pushed
// or replaced, as the address it left.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const base = { role: 'voter', status: 'online', joined_at: '2026-01-01T00:00:00Z', last_seen: '2026-01-01T00:00:00Z' }
const nodes = [
  { ...base, id: 'm-1', name: 'm-1', kind: 'manager', api_address: '192.0.2.1:3628', grpc_address: '192.0.2.1:3629' },
  { ...base, id: 'm-2', name: 'm-2', kind: 'manager', api_address: '192.0.2.2:3628', grpc_address: '192.0.2.2:3629' },
  { ...base, id: 'agent-1', name: 'agent-1', kind: 'agent', role: '', labels: { group: 'web' }, api_address: '198.51.100.1:3628', grpc_address: '198.51.100.1:3629' },
  { ...base, id: 'agent-2', name: 'agent-2', kind: 'agent', role: '', status: 'offline', api_address: '198.51.100.2:3628', grpc_address: '198.51.100.2:3629' },
]
const owner = { uid: 1000, gid: 1000, user: 'tester', group: 'tester' }
const sources = [
  { id: 'syslog', name: 'System Log', path: '/var/log/syslog', size: 4096, exists: true, custom: false },
  { id: 'firewall', name: 'Firewall', path: '/var/log/ufw.log', size: 2048, exists: true, custom: false },
]

interface MockOptions {
  /** Written to localStorage on the first load only: a reload finds what the page stored. */
  storage?: Record<string, string>
  /** How long the node list takes to answer, in ms (600 unless given). */
  nodesDelay?: number
  /** The node list answers 503, as a follower does with the leader unreachable. */
  nodesDown?: boolean
  /** A single server: cluster status says not enabled. */
  standalone?: boolean
}

async function mock(page: Page, opts: MockOptions = {}) {
  const requests: string[] = []
  await page.addInitScript((storage) => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    const w = window as unknown as { __urls: string[] }
    w.__urls = []
    const push = history.pushState.bind(history)
    const replace = history.replaceState.bind(history)
    history.pushState = (...args: Parameters<History['pushState']>) => {
      push(...args)
      w.__urls.push(`push ${location.pathname}${location.search}`)
    }
    history.replaceState = (...args: Parameters<History['replaceState']>) => {
      replace(...args)
      w.__urls.push(`replace ${location.pathname}${location.search}`)
    }
    if (sessionStorage.getItem('fixture-seeded')) return
    sessionStorage.setItem('fixture-seeded', '1')
    for (const [k, v] of Object.entries(storage)) localStorage.setItem(k, v)
  }, opts.storage ?? {})
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace('/api/v1', '')
    requests.push(path + url.search)
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = opts.standalone
      ? { enabled: false }
      : { enabled: true, name: 'lab', node_count: 4, leader_id: 'm-1', local_id: 'm-1', is_leader: true }
    else if (path === '/cluster/nodes') {
      if (opts.nodesDown) {
        await route.fulfill({ status: 503, json: { success: false, error: { code: 'CLUSTER_NOT_LEADER', message: 'leader unreachable' } } })
        return
      }
      await new Promise((resolve) => setTimeout(resolve, opts.nodesDelay ?? 600))
      data = { nodes, local_id: 'm-1', is_leader: true }
    }
    else if (path === '/cluster/overview') data = { name: 'lab', node_count: 4, leader_id: 'm-1', nodes, metrics: [] }
    else if (path === '/cluster/events') data = { events: [] }
    else if (path === '/system/overview') data = { version: '0.94.0', host: { hostname: 'm-1-host' } }
    else if (path === '/system/services') data = { services: [], total: 0 }
    else if (path === '/files/trash') data = { entries: [], retentionDays: 7 }
    else if (path === '/files') data = url.searchParams.get('path') === '/srv'
      ? [{ name: 'app', path: '/srv/app', isDir: true, size: 4096, modTime: '2026-01-01T00:00:00Z', mode: 'drwxr-xr-x', kind: 'dir', owner }]
      : []
    else if (path === '/logs/sources') data = sources
    else if (path === '/logs/read') data = { lines: [], total_lines: 0 }
    // agent-1 runs one stack, demo; the other servers none.
    else if (path === '/docker/compose') data = url.searchParams.get('node') === 'agent-1'
      ? [{ name: 'demo', compose_file: 'compose.yaml', has_env: false, path: '/opt/stacks/demo', service_count: 1, running_count: 1, real_status: 'running' }]
      : []
    else if (path === '/docker/compose/demo') data = { name: 'demo', yaml: 'services:\n  web:\n    image: nginx\n' }
    else if (path === '/docker/compose/demo/env') data = { content: '' }
    else if (path === '/docker/compose/demo/services' || path === '/docker/containers') data = []
    else if (path === '/alerts/history') data = { items: [], total: 0 }
    else if (path === '/alerts/channels' || path === '/alerts/rules') data = []
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  return requests
}

const stored = (page: Page) => page.evaluate(() => localStorage.getItem('sfpanel_current_node'))
const historyUrls = (page: Page) => page.evaluate(() => (window as unknown as { __urls: string[] }).__urls)

test('the address wins over the stored server, from the first request', async ({ page }) => {
  const requests = await mock(page, { storage: { sfpanel_current_node: 'm-2', 'sfpanel-cluster-mode': 'true', 'sfpanel-cluster-local-id': 'm-1' } })
  await page.goto('/services?node=agent-1')
  await expect.poll(() => requests.filter((r) => r.startsWith('/system/services')).length).toBeGreaterThan(0)
  expect(requests.filter((r) => r.startsWith('/system/services')).every((r) => r.includes('node=agent-1'))).toBe(true)
  // Not one request, the first ones before any page included, asked the stored server.
  expect(requests.filter((r) => r.includes('node=m-2'))).toEqual([])
  expect(await stored(page)).toBe('agent-1')
})

test('a link without ?node= gets the selected server back, by replace', async ({ page }) => {
  await mock(page, { storage: { sfpanel_current_node: 'agent-1' } })
  await page.goto('/services?node=agent-1')
  const before = await page.evaluate(() => history.length)
  // The way the app's own navigate() moves: a new entry, then the router reads it.
  await page.evaluate(() => { history.pushState({}, '', '/docker/stacks'); dispatchEvent(new PopStateEvent('popstate')) })
  await expect(page).toHaveURL(/\/docker\/stacks\?node=agent-1$/)
  expect(await page.evaluate(() => history.length)).toBe(before + 1)
})

test('the attached server is named by its own id, and the store keeps null', async ({ page }) => {
  const requests = await mock(page)
  await page.goto('/docker/stacks')
  await expect(page).toHaveURL(/\/docker\/stacks\?node=m-1$/)
  expect(await stored(page)).toBeNull()
  await expect.poll(() => requests.filter((r) => r.startsWith('/docker/compose')).length).toBeGreaterThan(0)
  expect(requests.filter((r) => r.startsWith('/docker/compose')).some((r) => r.includes('node='))).toBe(false)
})

test('a single server never carries ?node=', async ({ page }) => {
  const requests = await mock(page, { standalone: true })
  await page.goto('/services')
  await expect.poll(() => requests.includes('/cluster/status')).toBe(true)
  await page.waitForTimeout(1000)
  expect(page.url()).not.toContain('node=')
})

test('back and forward follow the server in the address', async ({ page }) => {
  const requests = await mock(page)
  await page.goto('/services?node=agent-1')
  await page.evaluate(() => { history.pushState({}, '', '/services?node=m-2'); dispatchEvent(new PopStateEvent('popstate')) })
  await expect.poll(() => requests.some((r) => r.startsWith('/system/services') && r.includes('node=m-2'))).toBe(true)
  const mark = requests.length
  await page.goBack()
  await expect(page).toHaveURL(/node=agent-1$/)
  await expect.poll(() => requests.slice(mark).some((r) => r.startsWith('/system/services') && r.includes('node=agent-1'))).toBe(true)
  expect(await stored(page)).toBe('agent-1')
})

test('an unknown server falls back to the attached one once the list says so', async ({ page }) => {
  const requests = await mock(page, { nodesDelay: 800 })
  await page.goto('/services?node=gone')
  // Before the list answers, the page asks the server the address names.
  await expect.poll(() => requests.some((r) => r.startsWith('/system/services') && r.includes('node=gone'))).toBe(true)
  await expect(page).toHaveURL(/\/services\?node=m-1$/)
  expect(await stored(page)).toBeNull()
})

test('a list that cannot be had judges nothing', async ({ page }) => {
  const requests = await mock(page, { nodesDown: true })
  await page.goto('/services?node=gone')
  await expect.poll(() => requests.filter((r) => r === '/cluster/nodes').length).toBeGreaterThan(0)
  await page.waitForTimeout(1500)
  await expect(page).toHaveURL(/node=gone$/)
  expect(await stored(page)).toBe('gone')
})

test("a page's own parameters survive the server in the address", async ({ page }) => {
  await mock(page, { storage: { sfpanel_current_node: 'agent-1' } })
  await page.goto('/settings?scope=node&tab=alerts')
  await expect(page).toHaveURL(/\/settings\?scope=node&tab=alerts&node=agent-1$/)
  // The account half acts on no one server: nothing is added.
  await page.goto('/settings?tab=account')
  await page.waitForTimeout(800)
  await expect(page).toHaveURL(/\/settings\?tab=account$/)
})

test('the file manager steps into a folder carrying the server', async ({ page }) => {
  await mock(page, { storage: { sfpanel_current_node: 'm-2' } })
  await page.goto('/files?path=%2Fsrv&node=m-2')
  await page.locator('table [data-row-open]').filter({ hasText: /^app$/ }).click()
  await expect(page).toHaveURL(/path=%2Fsrv%2Fapp/)
  const urls = await historyUrls(page)
  // One entry for the step, already naming the server: none put it back afterwards.
  const push = urls.findLastIndex((u) => u.startsWith('push') && u.includes('path=%2Fsrv%2Fapp'))
  expect(push).toBeGreaterThanOrEqual(0)
  expect(urls[push]).toContain('node=m-2')
  expect(urls.slice(push + 1).filter((u) => u.startsWith('replace'))).toEqual([])
})

test('clearing ?new=1 keeps the server', async ({ page }) => {
  await mock(page, { storage: { sfpanel_current_node: 'm-2' } })
  await page.goto('/docker/stacks?new=1&node=m-2')
  await expect(page).toHaveURL(/\/docker\/stacks\?node=m-2$/)
  const urls = await historyUrls(page)
  expect(urls.filter((u) => u.startsWith('replace')).every((u) => u.includes('node=m-2'))).toBe(true)
})

test('an old address that redirects keeps the server it named', async ({ page }) => {
  // The old firewall-logs address redirects to /logs?source=firewall and drops
  // ?node=: the boot adoption and the re-attach put it back.
  const requests = await mock(page)
  await page.goto('/firewall/logs?node=agent-1')
  await expect(page).toHaveURL(/\/logs\?source=firewall&node=agent-1$/)
  await expect.poll(() => requests.some((r) => r.startsWith('/logs/read') && r.includes('node=agent-1'))).toBe(true)
})

test('an old cluster stack address opens the stack screen on its server through the address', async ({ page }) => {
  const requests = await mock(page)
  await page.goto('/cluster/stacks/agent-1/demo')
  await expect(page).toHaveURL(/\/docker\/stacks\/demo\?node=agent-1$/)
  await expect.poll(() => requests.some((r) => r.startsWith('/docker/compose/demo') && r.includes('node=agent-1'))).toBe(true)
  // The shell was told: the old address pointed the client at the server
  // without a word, and the picker went on naming the attached one.
  await expect(page.getByRole('button', { name: /Choose a server: agent-1/ })).toBeVisible()
  await page.getByRole('navigation', { name: 'Main menu' }).getByRole('link', { name: 'Cluster' }).click()
  await expect(page).toHaveURL(/\/cluster\/overview$/)
  // Leaving does not put the server back: it is the one chosen now.
  expect(await stored(page)).toBe('agent-1')
})

test('the old cluster Docker opens the running board', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/stacks')
  await expect(page).toHaveURL(/\/cluster\/running$/)
})

test.describe('phone', () => {
  test.use({ viewport: { width: 412, height: 860 }, hasTouch: true })

  test('a switch stays on the page and replaces the address', async ({ page }) => {
    const requests = await mock(page)
    await page.goto('/services?node=m-1')
    const before = await page.evaluate(() => history.length)
    await page.getByRole('button', { name: 'More' }).tap()
    const drawer = page.getByRole('dialog')
    await drawer.getByRole('button', { name: /^m-1/ }).tap()
    await drawer.getByRole('button', { name: /^agent-1/ }).tap()
    await expect(page).toHaveURL(/\/services\?node=agent-1$/)
    expect(await page.evaluate(() => history.length)).toBe(before)
    await expect.poll(() => requests.some((r) => r.startsWith('/system/services') && r.includes('node=agent-1'))).toBe(true)
  })
})
