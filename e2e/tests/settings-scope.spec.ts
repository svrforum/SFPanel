import { test, expect, type Page } from '@playwright/test'

// Settings is one page of six tabs in both modes. Where a tab's values live is
// said once, by the scope line above the page in a cluster: the account on
// every server, the language and theme in this browser, the rest on the
// server the address names — which those tabs carry in a cluster. An old
// ?scope=node address opens the tab it meant, on the same server, in place.
// Mocked API, phone viewport; the node list answers late, as a leader does.
test.use({ viewport: { width: 412, height: 860 }, hasTouch: true })

const node = { id: 'node-a', name: 'node-a', role: 'voter', status: 'online', api_address: '192.168.1.x:3628', grpc_address: '192.168.1.x:3629', joined_at: '2026-01-01T00:00:00Z', last_seen: '2026-01-01T00:00:00Z' }

async function mock(page: Page, cluster: boolean) {
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname
    let data: unknown = {}
    if (path.endsWith('/auth/setup-status')) data = { setup_required: false }
    else if (path.endsWith('/cluster/status')) data = cluster
      ? { enabled: true, name: 'lab', node_count: 2, leader_id: 'node-a', local_id: 'node-a', is_leader: true }
      : { enabled: false }
    else if (path.endsWith('/cluster/nodes')) {
      await new Promise((resolve) => setTimeout(resolve, 600))
      data = { nodes: [node] }
    }
    else if (path.endsWith('/cluster/overview')) data = { name: 'lab', node_count: 1, leader_id: 'node-a', nodes: [node], metrics: [] }
    else if (path.endsWith('/cluster/events')) data = { events: [] }
    else if (path.includes('/alerts/')) data = []
    else if (path.endsWith('/system/tls')) data = { enabled: false }
    else if (path.endsWith('/audit/logs')) data = { logs: [], total: 0 }
    await route.fulfill({ json: { success: true, data } })
  })
}

const bar = (page: Page) => page.locator('[data-scope-bar]')
// The shell puts a server into the address only after the page shows: an
// address without one is judged once that would have happened.
const settledUrl = async (page: Page) => { await page.waitForTimeout(800); return page.url() }
const TABS = ['Account', 'Language & Theme', 'Updates & Backups', 'HTTPS Certificate', 'Alerts', 'Audit Log']

test('a phone in cluster mode reaches every settings tab, each saying where it saves', async ({ page }) => {
  await mock(page, true)
  // Any page with the phone's bottom bar will do, but not /terminal: it hides
  // the bar to give the terminal the screen.
  await page.goto('/cluster/overview')
  await page.getByRole('button', { name: 'More' }).tap()
  await page.getByRole('button', { name: 'Settings' }).or(page.getByRole('link', { name: 'Settings' })).first().tap()
  await expect(page).toHaveURL(/\/settings$/)
  await expect(page.getByRole('tab')).toHaveText(TABS)
  await expect(page.getByRole('heading', { name: 'Change Password' })).toBeVisible()
  await expect(bar(page)).toContainText('Same on every server')
  await expect(page.getByRole('navigation', { name: 'Settings scope' })).toHaveCount(0)

  await page.getByRole('tab', { name: 'Language & Theme' }).tap()
  await expect(bar(page)).toContainText('Saved in this browser only')
  expect(await settledUrl(page)).toMatch(/\/settings\?tab=browser$/)

  // A server's tab names the server, the attached one by its own id.
  await page.getByRole('tab', { name: 'Alerts' }).tap()
  await expect(page).toHaveURL(/\/settings\?tab=alerts&node=node-a$/)
  await expect(bar(page)).toContainText('Saved on node-a only')

  // Back to a tab no server holds: the address names none.
  await page.getByRole('tab', { name: 'Language & Theme' }).tap()
  await expect(bar(page)).toContainText('Saved in this browser only')
  expect(await settledUrl(page)).toMatch(/\/settings\?tab=browser$/)
})

test('an old node-scope address opens the same tab on the same server, by replace', async ({ page }) => {
  await mock(page, true)
  await page.goto('/dashboard')
  const before = await page.evaluate(() => history.length)
  // The order is not asserted: this page's replace and the shell's putting the
  // server back can run in the same commit, and either way the address has to
  // end up with the same values.
  const params = () => Object.fromEntries(new URL(page.url()).searchParams)
  await page.goto('/settings?scope=node')
  await expect.poll(params).toEqual({ tab: 'system', node: 'node-a' })
  await expect(page.getByRole('tab', { name: 'Updates & Backups' })).toHaveAttribute('aria-selected', 'true')
  expect(await page.evaluate(() => history.length)).toBe(before + 1)
  await page.goto('/settings?scope=node&tab=audit&node=node-a')
  await expect.poll(params).toEqual({ tab: 'audit', node: 'node-a' })
})

test('a single server shows the same six tabs, no scope line and no server in the address', async ({ page }) => {
  await mock(page, false)
  await page.goto('/settings?tab=alerts')
  await expect(page.getByRole('tab')).toHaveText(TABS)
  await expect(bar(page)).toHaveCount(0)
  expect(await settledUrl(page)).not.toContain('node=')
})

test('the six tabs fit a phone: the last is reached and the page does not scroll sideways', async ({ page }) => {
  await page.setViewportSize({ width: 360, height: 800 })
  await mock(page, false)
  await page.goto('/settings')
  await page.getByRole('tab', { name: 'Audit Log' }).tap()
  await expect(page.getByRole('tab', { name: 'Audit Log' })).toHaveAttribute('aria-selected', 'true')
  expect(await page.evaluate(() => { const m = document.querySelector('main')!; return m.scrollWidth <= m.clientWidth })).toBe(true)
})

// The tab list hides its scrollbar and opened scrolled to its start, so a link
// to a tab past the edge (the watch link to alerts, the logs' audit link)
// opened on a phone with the open tab out of sight. Not tapped here: a tap
// scrolls the tab into view by itself. An old address is replaced before the
// list is drawn, and has its tab in sight too.
test('a link to a tab past the edge of a phone opens with that tab in sight', async ({ page }) => {
  await page.setViewportSize({ width: 360, height: 800 })
  await mock(page, false)
  for (const [query, name] of [['tab=audit', 'Audit Log'], ['tab=alerts', 'Alerts'], ['scope=node&tab=audit', 'Audit Log']]) {
    await page.goto(`/settings?${query}`)
    const open = page.getByRole('tab', { name })
    await expect(open, query).toHaveAttribute('aria-selected', 'true')
    await expect(open, query).toBeInViewport({ ratio: 1 })
  }
})
