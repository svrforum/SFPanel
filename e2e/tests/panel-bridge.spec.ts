import { test, expect, type Page } from '@playwright/test'

// The Android app drew its feature sheet from a list of its own and titled
// itself with the saved connection's name: a manager's name while the pages
// acted on an agent, and a menu that did not match the panel's. The page now
// publishes the server it acts on and its menu on window.__sfpanel, which the
// app reads, and says on window when the server changes. Mocked API: managers
// m-1 (attached, leader) and m-2, agents agent-1 and agent-2; the node list
// answers late, as a leader does.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

interface Bridge {
  v: number
  server: { name: string; role: 'manager' | 'agent' | null; attached: boolean } | null
  nav: { group: string; title: string; items: { label: string; path: string; tabs: { label: string; path: string }[] }[] }[]
}

declare global {
  interface Window {
    __sfpanel?: Bridge
    __seen?: (string | undefined)[]
    __titles?: { title: string; published: boolean; server: string | null }[]
  }
}

const base = { role: 'voter', status: 'online', joined_at: '2026-01-01T00:00:00Z', last_seen: '2026-01-01T00:00:00Z' }
const nodes = [
  { ...base, id: 'm-1', name: 'm-1', kind: 'manager', api_address: '192.0.2.1:3628', grpc_address: '192.0.2.1:3629' },
  { ...base, id: 'm-2', name: 'm-2', kind: 'manager', api_address: '192.0.2.2:3628', grpc_address: '192.0.2.2:3629' },
  { ...base, id: 'agent-1', name: 'agent-1', kind: 'agent', role: '', api_address: '198.51.100.1:3628', grpc_address: '198.51.100.1:3629' },
  { ...base, id: 'agent-2', name: 'agent-2', kind: 'agent', role: '', status: 'offline', api_address: '198.51.100.2:3628', grpc_address: '198.51.100.2:3629' },
]

interface MockOptions {
  /** A single server: cluster status says not enabled. */
  standalone?: boolean
  /** The interface language the page starts in. */
  language?: string
}

async function mock(page: Page, opts: MockOptions = {}) {
  await page.addInitScript((language) => {
    sessionStorage.setItem('token', 'fixture-token')
    if (sessionStorage.getItem('fixture-seeded')) return
    sessionStorage.setItem('fixture-seeded', '1')
    localStorage.setItem('sfpanel_language', language)
  }, opts.language ?? 'en')
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace('/api/v1', '')
    const node = url.searchParams.get('node') ?? 'm-1'
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/auth/ws-ticket') data = { ticket: 'fixture-ticket' }
    else if (path === '/cluster/status') data = opts.standalone
      ? { enabled: false }
      : { enabled: true, name: 'lab', node_count: 4, leader_id: 'm-1', local_id: 'm-1', is_leader: true }
    else if (path === '/cluster/nodes') {
      await new Promise((resolve) => setTimeout(resolve, 600))
      data = { nodes, local_id: 'm-1', is_leader: true }
    }
    else if (path === '/cluster/overview') data = { name: 'lab', node_count: 4, leader_id: 'm-1', nodes, metrics: [] }
    else if (path === '/cluster/events') data = { events: [] }
    else if (path === '/system/overview') data = {
      version: '0.94.0',
      host: { hostname: `${node}-host`, os: 'linux', platform: 'debian', platform_version: '13', kernel: '6.12', uptime: 3600, num_cpu: 4 },
      metrics: { cpu: 12, mem_percent: 25, disk_percent: 30 }, metrics_history: [],
    }
    else if (path === '/system/services') data = { services: [], total: 0 }
    else if (path === '/docker/compose' || path === '/docker/containers') data = []
    else if (path === '/alerts/history') data = { items: [], total: 0 }
    else if (path === '/alerts/channels' || path === '/alerts/rules') data = []
    else if (path === '/ai/sessions') data = []
    else if (path === '/terminal/info') data = { shell_user: 'tester', is_root: false, hostname: 'fixture' }
    else if (path === '/terminal/sessions') data = { sessions: [] }
    else if (/batch|history|events|metrics/.test(path)) data = []
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
}

const bridge = (page: Page) => page.evaluate(() => window.__sfpanel)
const systemLabel = async (page: Page) => (await bridge(page))?.nav[0]?.items.find((i) => i.path === '/services')?.label

test('a server page publishes its server and the menu', async ({ page }) => {
  await mock(page)
  await page.goto('/services?node=agent-1')
  await expect.poll(() => page.evaluate(() => window.__sfpanel?.server)).toEqual({ name: 'agent-1', role: 'agent', attached: false })
  const b = (await bridge(page))!
  expect(b.v).toBe(1)
  expect(b.nav.map((g) => g.title)).toEqual(['agent-1', 'All servers', ''])
  expect(b.nav.flatMap((g) => g.items.map((i) => i.path)))
    .toEqual(['/terminal', '/dashboard', '/docker', '/files', '/logs', '/services', '/network', '/firewall', '/disk', '/cluster/running', '/map', '/cluster/tasks', '/agents', '/cluster', '/settings'])
  expect(b.nav[0].items.find((i) => i.path === '/services')!.tabs).toEqual([
    { label: 'Services', path: '/services' },
    { label: 'Processes', path: '/processes' },
    { label: 'Scheduled Tasks', path: '/cron' },
    { label: 'Packages', path: '/packages' },
    { label: 'Performance Tuning', path: '/tuning' },
  ])
  await expect(page).toHaveTitle(/agent-1/)
})

test('switching announces the new server once', async ({ page }) => {
  await page.addInitScript(() => {
    window.__seen = []
    addEventListener('sfpanel:server', (e) => window.__seen!.push((e as CustomEvent).detail?.name))
  })
  await mock(page)
  await page.goto('/services?node=agent-1')
  await expect.poll(() => page.evaluate(() => window.__seen!.at(-1))).toBe('agent-1')
  const n = await page.evaluate(() => window.__seen!.length)
  await page.locator('aside').getByRole('button', { name: /^Choose a server/ }).click()
  await page.getByRole('dialog', { name: 'Choose a server' }).getByRole('button', { name: /^m-1\b/ }).click()
  await expect(page).toHaveURL(/\/services\?node=m-1$/)
  await expect.poll(() => page.evaluate((n) => window.__seen!.slice(n), n)).toEqual(['m-1'])
  expect(await page.evaluate(() => window.__sfpanel!.server!.attached)).toBe(true)
  // The node list asks again on its own; the same server is not announced again.
  await page.evaluate(() => window.dispatchEvent(new Event('sfpanel:cluster-changed')))
  await page.waitForTimeout(1000)
  expect(await page.evaluate((n) => window.__seen!.slice(n), n)).toEqual(['m-1'])
})

// The app reads the bridge when the page renames its tab. A title set before
// the bridge it goes with sent the app to its own list on a first load, or
// kept the last server's name until the next move.
async function recordTitles(page: Page) {
  await page.addInitScript(() => {
    const own = Object.getOwnPropertyDescriptor(Document.prototype, 'title')!
    window.__titles = []
    Object.defineProperty(Document.prototype, 'title', {
      configurable: true,
      get() { return own.get!.call(this) },
      set(value: string) {
        window.__titles!.push({ title: value, published: window.__sfpanel !== undefined, server: window.__sfpanel?.server?.name ?? null })
        own.set!.call(this, value)
      },
    })
  })
}

test('the bridge goes with every title the page sets', async ({ page }) => {
  await recordTitles(page)
  await mock(page)
  await page.goto('/services?node=agent-1')
  await expect(page).toHaveTitle('Services · agent-1 · SFPanel')
  const titles = await page.evaluate(() => window.__titles!)
  expect(titles[0]).toMatchObject({ title: 'Services · SFPanel', published: true })
  // Every title that names the server finds the bridge naming it.
  const named = titles.filter((s) => s.title.includes('agent-1'))
  expect(named.length).toBeGreaterThan(0)
  expect(named.map((s) => s.server)).toEqual(named.map(() => 'agent-1'))
})

test('a single server\'s first title finds its menu', async ({ page }) => {
  await recordTitles(page)
  await mock(page, { standalone: true })
  await page.goto('/services')
  await expect(page).toHaveTitle('Services · SFPanel')
  const first = await page.evaluate(() => window.__titles![0])
  expect(first).toMatchObject({ title: 'Services · SFPanel', published: true })
})

test('a single server publishes no server', async ({ page }) => {
  await mock(page, { standalone: true })
  await page.goto('/services')
  await expect(page.getByRole('heading', { level: 1, name: 'System' })).toBeVisible()
  const b = (await bridge(page))!
  expect(b.server).toBeNull()
  expect(b.nav.map((g) => g.title)).toEqual(['', '', ''])
  expect(b.nav.flatMap((g) => g.items)).toHaveLength(12)
})

test('labels follow the interface language', async ({ page }) => {
  await mock(page, { language: 'ko' })
  await page.goto('/services?node=m-1')
  await expect.poll(() => page.evaluate(() => window.__sfpanel?.nav[1]?.title)).toBe('모든 서버')
  expect(await systemLabel(page)).toBe('시스템')
})

test('a language changed in settings relabels the menu', async ({ page }) => {
  await mock(page)
  await page.goto('/settings?tab=browser')
  // Everything else the menu is built from has come: the switch alone has to relabel it.
  await expect.poll(() => page.evaluate(() => window.__sfpanel?.server?.name)).toBe('m-1')
  expect(await systemLabel(page)).toBe('System')
  await page.getByRole('button', { name: '한국어', exact: true }).click()
  await expect.poll(() => systemLabel(page)).toBe('시스템')
  expect((await bridge(page))!.nav[1].title).toBe('모든 서버')
})

test('signing out takes the menu down', async ({ page }) => {
  await mock(page)
  await page.goto('/services?node=agent-1')
  await expect.poll(() => page.evaluate(() => window.__sfpanel?.server?.name)).toBe('agent-1')
  await page.locator('aside').getByRole('button', { name: 'Logout' }).click()
  await expect(page.getByRole('button', { name: 'Sign In' })).toBeVisible()
  await expect.poll(() => page.evaluate(() => window.__sfpanel)).toBeUndefined()
})

// The app's own list before this one: a panel it is pointed at keeps opening every one of them.
const OLD_APP_PAGES = ['/dashboard', '/terminal', '/files', '/docker', '/appstore', '/services', '/processes', '/cron', '/logs', '/network', '/firewall', '/disk', '/packages', '/cluster', '/settings']

for (const standalone of [false, true]) {
  test(`every page the old app lists still opens${standalone ? ' on a single server' : ''}`, async ({ page }) => {
    await mock(page, { standalone })
    for (const p of OLD_APP_PAGES) {
      await page.goto(p)
      // The tab names the page once the shell has drawn it; an address no page claims says "Page not found".
      await expect(page).toHaveTitle(/ · SFPanel$/)
      await expect(page).not.toHaveTitle(/^Page not found/)
      await expect(page.getByRole('heading', { name: 'Page not found' })).toHaveCount(0)
    }
  })
}
