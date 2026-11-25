import { test, expect, type Page } from '@playwright/test'

// A standalone panel's cluster page: the create and join form, and nothing
// else. It used to show the six tabs of a cluster it is not in, five of them
// saying only "Cluster not enabled". Only a definite answer makes a panel
// standalone: a cluster node whose status read is late or fails keeps the
// page it asked for, and its tabs. Mocked API.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

interface Opts {
  /** What GET /cluster/status answers: the mode, or a server error. */
  status: { enabled: boolean } | 500
  /** Milliseconds before /cluster/status answers. */
  delay?: number
  /** The shell's saved belief from an earlier visit (sfpanel-cluster-mode). */
  cached?: boolean
}

const base = { status: 'online', joined_at: '2026-01-01T00:00:00Z', last_seen: '2026-01-01T00:00:00Z' }
const nodes = [
  { ...base, id: 'm-1', name: 'node-a', kind: 'manager', role: 'voter', api_address: '192.0.2.1:3628', grpc_address: '192.0.2.1:3629' },
  { ...base, id: 'm-2', name: 'node-b', kind: 'manager', role: 'voter', api_address: '192.0.2.2:3628', grpc_address: '192.0.2.2:3629' },
]

async function mock(page: Page, opts: Opts) {
  await page.addInitScript((cached) => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('sfpanel_language', 'en')
    if (cached !== undefined) localStorage.setItem('sfpanel-cluster-mode', String(cached))
  }, opts.cached)
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '')
    if (path === '/cluster/status') {
      if (opts.delay) await new Promise((r) => setTimeout(r, opts.delay))
      if (opts.status === 500) {
        await route.fulfill({ status: 500, json: { success: false, error: { code: 'INTERNAL_ERROR', message: 'status unavailable' } } })
        return
      }
      const data = opts.status.enabled
        ? { enabled: true, name: 'lab', node_count: nodes.length, leader_id: 'm-1', local_id: 'm-1', is_leader: true }
        : { enabled: false }
      await route.fulfill({ json: { success: true, data } })
      return
    }
    // A standalone server refuses the cluster's own reads, as its handlers do;
    // the page behind the redirect reads them until the status answers.
    if (opts.status !== 500 && !opts.status.enabled) {
      const refuse = (code: string, message: string) => route.fulfill({ status: 400, json: { success: false, error: { code, message } } })
      if (path === '/docker/compose/cluster-stacks') return refuse('INTERNAL_ERROR', 'cluster is not enabled')
      if (path.startsWith('/cluster/') && path !== '/cluster/interfaces') return refuse('INVALID_REQUEST', 'Cluster not configured')
    }
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/auth/ws-ticket') data = { ticket: 'fixture-ticket' }
    else if (path === '/cluster/nodes') data = { nodes, local_id: 'm-1', is_leader: true }
    else if (path === '/cluster/overview') data = { name: 'lab', node_count: nodes.length, leader_id: 'm-1', nodes, metrics: [] }
    else if (path === '/cluster/events') data = { events: [] }
    else if (path === '/cluster/interfaces') data = { interfaces: [{ name: 'eth0', address: '192.0.2.10' }] }
    else if (path === '/system/overview') data = {
      host: { hostname: 'host-a', os: 'linux', platform: 'debian', platform_version: '13', kernel: '6.12', uptime: 1, num_cpu: 4 },
      metrics: { cpu: 12, mem_percent: 40, disk_percent: 55 }, version: '0.93.1', metrics_history: [],
    }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
}

/** A tab in the row above the cluster pages. */
const tab = (page: Page, name: string) => page.getByRole('main').getByRole('link', { name, exact: true })
const statusAnswer = (page: Page) => page.waitForResponse((r) => new URL(r.url()).pathname === '/api/v1/cluster/status')

test('a standalone panel opens create and join, with no tab row', async ({ page }) => {
  await mock(page, { status: { enabled: false } })
  await page.goto('/cluster/nodes')
  await expect(page).toHaveURL(/\/cluster\/overview$/)
  await expect(page.getByRole('button', { name: 'Initialize Cluster' })).toBeVisible()
  await expect(tab(page, 'Group Tasks')).toHaveCount(0)
  await expect(tab(page, 'Key Rotation')).toHaveCount(0)
})

test('every other cluster address lands there too', async ({ page }) => {
  await mock(page, { status: { enabled: false } })
  for (const path of ['/cluster/security', '/cluster/tasks', '/cluster/tasks/runs', '/cluster/stacks', '/cluster/tokens']) {
    await page.goto(path)
    await expect(page, path).toHaveURL(/\/cluster\/overview$/)
  }
  // The fleet board has a counterpart for one server: what runs on it.
  await page.goto('/cluster/running')
  await expect(page).toHaveURL(/\/running$/)
})

test('the redirect replaces the address', async ({ page }) => {
  await mock(page, { status: { enabled: false } })
  await page.goto('/dashboard')
  await page.goto('/cluster/tasks')
  await expect(page).toHaveURL(/\/cluster\/overview$/)
  // Back leaves the cluster page instead of bouncing off the old address onto it.
  await page.goBack()
  await expect(page).toHaveURL(/\/dashboard$/)
})

test('a cluster node whose status answers late keeps its tab', async ({ page }) => {
  await mock(page, { status: { enabled: true }, delay: 1500, cached: true })
  const answered = statusAnswer(page)
  await page.goto('/cluster/nodes')
  await answered
  await expect(page).toHaveURL(/\/cluster\/nodes$/)
  await expect(tab(page, 'Nodes')).toHaveAttribute('aria-current', 'page')
})

test('a cluster node whose status read fails keeps its tab', async ({ page }) => {
  await mock(page, { status: 500, cached: true })
  const answered = statusAnswer(page)
  await page.goto('/cluster/nodes')
  expect((await answered).status()).toBe(500)
  await expect(page).toHaveURL(/\/cluster\/nodes$/)
  await expect(tab(page, 'Nodes')).toHaveAttribute('aria-current', 'page')
})
