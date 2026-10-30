import { test, expect, type Page } from '@playwright/test'

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

async function mock(page: Page, leaderAnswers: () => boolean) {
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
    else if (path.endsWith('/cluster/status')) data = { enabled: true, name: 'lab', node_count: 2, leader_id: 'node-b', local_id: 'node-a', is_leader: false }
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
  await drawer.getByRole('button', { name: 'node-b' }).tap()
  await expect.poll(() => page.evaluate(() => localStorage.getItem('sfpanel_current_node'))).toBe('node-b')
})
