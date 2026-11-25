import { test, expect, type Page } from '@playwright/test'

// In cluster mode the sidebar said nothing of servers behind: the standalone
// sidebar's update badge has no place in the tree, and the count lived on the
// overview alone. The tree now carries it — an arrow above sign-out in the
// rail, a badge beside the version when expanded — counted as the overview's
// banner counts it, from the same /cluster/overview. Mocked API: four
// managers, node-d offline; the node list answers late, as a leader does.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const node = (i: number, status: string) => ({
  id: `n-${i}`, name: `node-${'abcd'[i - 1]}`, kind: 'manager', role: 'voter', status,
  api_address: `192.0.2.${i}:3628`, grpc_address: `192.0.2.${i}:3629`,
  joined_at: '2026-01-01T00:00:00Z', last_seen: '2026-01-01T00:00:00Z',
})
const NODES = [node(1, 'online'), node(2, 'online'), node(3, 'online'), node(4, 'offline')]
const NODES_DELAY_MS = 1500
const LATEST = '0.93.1'

// n-2 and n-3 are online and behind; n-4 is further behind, but offline.
const BEHIND = ['0.93.1', '0.93.0', '0.92.0', '0.90.0']
const CURRENT = ['0.93.1', '0.93.1', '0.93.1', '0.90.0']

const EXPANDED = { 'sfpanel-cluster-tree-collapsed': 'false' }

interface Opts { versions?: string[]; overviewDown?: boolean; storage?: Record<string, string> }

// Opts are read on each request, so a test can change the answer midway.
async function mock(page: Page, opts: Opts = {}) {
  const overview = { asked: 0, refused: 0 }
  await page.addInitScript((storage) => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('sfpanel_language', 'en')
    for (const [k, v] of Object.entries(storage)) localStorage.setItem(k, v)
  }, opts.storage ?? {})
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace('/api/v1', '')
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/auth/ws-ticket') data = { ticket: 'fixture-ticket' }
    else if (path === '/cluster/status') data = { enabled: true, name: 'lab', node_count: NODES.length, leader_id: 'n-1', local_id: 'n-1', is_leader: true }
    else if (path === '/cluster/nodes') {
      // A mock answers at once; a leader answers after the page has drawn.
      await new Promise((resolve) => setTimeout(resolve, NODES_DELAY_MS))
      data = { nodes: NODES, local_id: 'n-1', is_leader: true }
    }
    else if (path === '/cluster/overview') {
      overview.asked++
      if (opts.overviewDown) {
        overview.refused++
        // What a follower answers with the leader unreachable.
        await route.fulfill({ status: 503, json: { success: false, error: { code: 'CLUSTER_NOT_LEADER', message: 'leader unreachable' } } })
        return
      }
      const versions = opts.versions ?? BEHIND
      data = {
        name: 'lab', node_count: NODES.length, leader_id: 'n-1', nodes: NODES, latest_version: LATEST,
        metrics: NODES.map((n, i) => ({
          node_id: n.id, cpu_percent: 1, memory_percent: 1, disk_percent: 1, container_count: 0, uptime_seconds: 1,
          version: versions[i], timestamp: 0,
        })),
      }
    }
    else if (path === '/cluster/events') data = { events: [] }
    else if (path === '/system/overview') data = { version: '0.93.1' }
    await route.fulfill({ json: { success: true, data } })
  })
  return overview
}

const badge = (page: Page) => page.getByRole('link', { name: /to update/ })
// The rail's node buttons, named "node-a · Online"; they come with the late node list.
const railNodes = (page: Page) => page.getByRole('button', { name: /^node-[a-d] · / })

test('the rail links the count of servers behind to the overview', async ({ page }) => {
  // No remembered mode: the shell learns it is a cluster after it has drawn.
  await mock(page)
  await page.goto('/cluster/nodes')

  const link = page.getByRole('link', { name: '2 to update' })
  await expect(link).toBeVisible()
  await expect(link).toHaveAttribute('href', '/cluster/overview')
  await link.click()
  await expect(page).toHaveURL(/\/cluster\/overview$/)
  // The same number: offline node-d is behind, and neither counts it.
  await expect(page.getByText(`2 nodes to update to ${LATEST}`)).toBeVisible()
})

test('the expanded tree shows the count beside the version', async ({ page }) => {
  await mock(page, { storage: { ...EXPANDED, 'sfpanel-cluster-mode': 'true' } })
  await page.goto('/cluster/nodes')

  const link = page.getByRole('link', { name: '2 to update' })
  await expect(link).toBeVisible()
  await expect(link).toHaveAttribute('href', '/cluster/overview')
  await expect(link.locator('xpath=..')).toContainText(`v${LATEST}`)
})

test('no badge when every online server is current', async ({ page }) => {
  await mock(page, { versions: CURRENT })
  await page.goto('/cluster/nodes')

  // The overview answered long before the node list: it has been counted.
  await expect(railNodes(page)).toHaveCount(4)
  await expect(badge(page)).toHaveCount(0)

  await page.getByRole('button', { name: 'Expand sidebar' }).click()
  await expect(page.locator('[data-tree-node]')).toHaveCount(4)
  await expect(badge(page)).toHaveCount(0)
})

test('an overview the leader cannot answer shows no badge', async ({ page }) => {
  const errors: string[] = []
  page.on('pageerror', (e) => errors.push(e.message))
  const overview = await mock(page, { overviewDown: true, storage: EXPANDED })
  await page.goto('/cluster/nodes')

  // The sidebar is drawn as ever, after the refused overview.
  await expect(page.locator('[data-tree-node]')).toHaveCount(4)
  expect(overview.asked).toBeGreaterThan(0)
  await expect(badge(page)).toHaveCount(0)
  expect(errors).toEqual([])
})

test('a read that fails keeps the last count', async ({ page }) => {
  await page.clock.install()
  const opts: Opts = {}
  const overview = await mock(page, opts)
  await page.goto('/cluster/nodes')
  const link = page.getByRole('link', { name: '2 to update' })
  await expect(link).toBeVisible()

  // The next minute's read is refused.
  opts.overviewDown = true
  await page.clock.fastForward('01:01')
  await expect.poll(() => overview.refused).toBeGreaterThan(0)
  // Nothing on screen marks the refusal: give it the moment it would take to land.
  await page.waitForTimeout(1000)
  await expect(link).toBeVisible()
})
