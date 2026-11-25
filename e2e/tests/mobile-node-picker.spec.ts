import { test, expect, type Locator, type Page } from '@playwright/test'

// A phone switches nodes from the picker at the top of the More drawer. The
// picker asked for the node list once, when the page loaded, and kept nothing
// when that failed — a follower answers 503 while it cannot reach the leader —
// so the picker stayed hidden until the page was reloaded, long after the
// cluster had recovered. Mocked API, phone viewport.
test.use({ viewport: { width: 412, height: 860 }, hasTouch: true })

const nodes = ['node-a', 'node-b'].map(id => ({
  id, name: id, role: 'voter', status: 'online', api_address: '192.168.1.x:3628', grpc_address: '192.168.1.x:3629',
  joined_at: '2026-01-01T00:00:00Z', last_seen: '2026-01-01T00:00:00Z',
}))

async function mock(page: Page, leaderAnswers: () => boolean, cluster = true) {
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname
    if (path.endsWith('/cluster/nodes') && !leaderAnswers()) {
      await route.fulfill({ status: 503, json: { success: false, error: { code: 'INTERNAL_ERROR', message: 'leader unreachable: proxy: rpc error: code = Canceled desc = grpc: the client connection is closing' } } })
      return
    }
    let data: unknown = {}
    if (path.endsWith('/auth/setup-status')) data = { setup_required: false }
    else if (path.endsWith('/cluster/status')) data = cluster
      ? { enabled: true, name: 'lab', node_count: 2, leader_id: 'node-b', local_id: 'node-a', is_leader: false }
      : { enabled: false }
    else if (path.endsWith('/cluster/nodes')) data = { nodes, local_id: 'node-a', is_leader: false }
    else if (path.endsWith('/cluster/overview')) data = { name: 'lab', node_count: 2, leader_id: 'node-b', nodes, metrics: [] }
    else if (path.endsWith('/cluster/events')) data = { events: [] }
    await route.fulfill({ json: { success: true, data } })
  })
}

test('the node picker is there once the leader answers again, without a reload', async ({ page }) => {
  let leaderAnswers = false
  await mock(page, () => leaderAnswers)
  await page.goto('/cluster/overview')
  // Every ask for the node list made while the page loaded was refused.
  await page.waitForLoadState('networkidle')
  leaderAnswers = true

  await page.getByRole('button', { name: 'More' }).tap()
  const drawer = page.getByRole('dialog')
  const picker = drawer.getByRole('button', { name: 'node-a' })
  await expect(picker).toBeVisible()
  await picker.tap()
  // The node in use is pressed, not only tinted, and each says its state.
  await expect(drawer.getByRole('button', { name: /node-a.*Attached/ })).toHaveAttribute('aria-pressed', 'true')
  await expect(drawer.getByRole('button', { name: 'node-b' })).toHaveAttribute('aria-pressed', 'false')
  await expect(drawer.getByRole('button', { name: 'node-b' })).toHaveAccessibleName(/Online/)
  await drawer.getByRole('button', { name: 'node-b' }).tap()
  await expect.poll(() => page.evaluate(() => localStorage.getItem('sfpanel_current_node'))).toBe('node-b')
})

const hrefs = (links: Locator) => links.evaluateAll(els => els.map(el => el.getAttribute('href')))

test('More groups the menu under the server and all servers', async ({ page }) => {
  await mock(page, () => true)
  await page.goto('/cluster/overview')
  await page.getByRole('button', { name: 'More' }).tap()
  const drawer = page.getByRole('dialog')
  await expect(drawer.getByRole('heading', { name: 'node-a' })).toBeVisible()
  await expect(drawer.getByRole('heading', { name: 'All servers' })).toBeVisible()
  // A server page's link names the server shown; the pages about all servers do not.
  const server = ['/files', '/services', '/network', '/firewall', '/disk'].map((to) => `${to}?node=node-a`)
  const fleet = ['/cluster/running', '/map', '/cluster/tasks', '/cluster']
  expect(await hrefs(drawer.getByRole('region', { name: 'node-a' }).getByRole('link'))).toEqual(server)
  expect(await hrefs(drawer.getByRole('region', { name: 'All servers' }).getByRole('link'))).toEqual(fleet)
  // Then settings, under neither: its own page, not one server's half of it.
  expect(await hrefs(drawer.getByRole('link'))).toEqual([...server, ...fleet, '/settings'])
  await expect(drawer.getByRole('link').last()).toHaveAccessibleName('Settings')
})

test("a single server's More has no headings", async ({ page }) => {
  await mock(page, () => true, false)
  await page.goto('/cluster')
  await page.getByRole('button', { name: 'More' }).tap()
  const drawer = page.getByRole('dialog')
  await expect(drawer.getByRole('link', { name: 'Files' })).toBeVisible()
  // The drawer's own title is the only heading: no group is named.
  await expect(drawer.getByRole('heading')).toHaveText(['More'])
  await expect(drawer.getByRole('region')).toHaveCount(0)
  expect(await hrefs(drawer.getByRole('link'))).toEqual(['/files', '/services', '/network', '/firewall', '/disk', '/map', '/cluster', '/settings'])
})
