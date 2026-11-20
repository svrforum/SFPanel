import { test, expect, type Page } from '@playwright/test'

// The running board: what runs on every node, with what needs looking at on
// top. Mocked API only: a cluster of an attached manager with stacks, an
// offline agent and a manager without Docker (and, for the failure case, an
// agent whose first container read fails), or one server with no cluster.
test.use({ viewport: { width: 1280, height: 900 }, locale: 'en-US' })

const PROJECT = 'com.docker.compose.project'
const port = (PublicPort: number, IP = '0.0.0.0', Type = 'tcp') => ({ IP, PrivatePort: PublicPort, PublicPort, Type })
const ctr = (name: string, State: string, Status: string, project?: string, Ports: unknown[] = []) => ({
  Id: `${name}-id`, Names: [`/${name}`], Image: `example/${name}:latest`, State, Status, Ports, Created: 1,
  Labels: project ? { [PROJECT]: project } : {},
})
const managed = (name: string) => ({ name, compose_file: 'compose.yaml', has_env: false, path: `/opt/stacks/${name}`, service_count: 0, running_count: 0, real_status: 'stopped' })

// The attached manager: a stack running, one in part with a restarting
// container, one in part with nothing failing, one stopped on purpose, and
// standalone containers running, exited long ago and exited just now.
const ALPHA = {
  projects: ['media', 'photos', 'wiki', 'monitoring'].map(managed),
  containers: [
    ctr('media-player-1', 'running', 'Up 3 hours', 'media', [port(8096), port(8096, '::')]),
    ctr('media-indexer-1', 'running', 'Up 3 hours', 'media', [port(9117, '127.0.0.1')]),
    ctr('photos-server-1', 'running', 'Up 2 hours', 'photos', [port(2283)]),
    ctr('photos-web-1', 'running', 'Up 2 hours', 'photos'),
    ctr('photos-redis-1', 'running', 'Up 2 hours', 'photos'),
    ctr('photos-ml-1', 'restarting', 'Restarting (1) 4 seconds ago', 'photos'),
    ctr('wiki-app-1', 'running', 'Up 5 hours', 'wiki', [port(3000), port(53, '0.0.0.0', 'udp')]),
    ctr('wiki-db-1', 'exited', 'Exited (0) About an hour ago', 'wiki'),
    ctr('monitoring-prom-1', 'exited', 'Exited (0) 5 days ago', 'monitoring'),
    ctr('monitoring-grafana-1', 'exited', 'Exited (0) 5 days ago', 'monitoring'),
    ctr('speedtest', 'running', 'Up 2 days', undefined, [port(8765)]),
    ctr('old-test', 'exited', 'Exited (1) 3 days ago'),
    ctr('backup-agent', 'exited', 'Exited (1) 2 hours ago'),
  ],
}
const BRAVO = {
  projects: [managed('cloud')],
  containers: [ctr('cloud-app-1', 'running', 'Up 1 hour', 'cloud', [port(8080)]), ctr('agent-tool', 'running', 'Up 1 hour')],
}

const base = { role: 'voter', joined_at: '2026-01-01T00:00:00Z', grpc_address: '192.0.2.1:3629' }
const LAST_SEEN = new Date(Date.now() - 5 * 60_000).toISOString()
const alpha = { ...base, id: 'm-1', name: 'alpha-node', kind: 'manager', status: 'online', api_address: '192.0.2.1:3628', last_seen: LAST_SEEN }
const charlie = { ...base, id: 'a-2', name: 'charlie-agent', kind: 'agent', role: '', status: 'offline', api_address: '198.51.100.9:3628', last_seen: LAST_SEEN }
const delta = { ...base, id: 'm-2', name: 'delta-node', kind: 'manager', status: 'online', api_address: '192.0.2.2:3628', last_seen: LAST_SEEN }
const bravo = { ...base, id: 'a-1', name: 'bravo-agent', kind: 'agent', role: '', status: 'online', api_address: '198.51.100.7:3628', last_seen: LAST_SEEN }

const metrics = (id: string, cpu: number) => ({ node_id: id, cpu_percent: cpu, memory_percent: 61, disk_percent: 72, container_count: 0, uptime_seconds: 1, version: '0.88.0', timestamp: 0 })

interface Opts {
  cluster?: boolean
  /** Adds bravo-agent, whose container read fails until `state.bravoFails` is cleared. */
  bravo?: boolean
  language?: 'en' | 'ko'
}

async function mock(page: Page, opts: Opts = {}) {
  const cluster = opts.cluster ?? true
  const nodes = [alpha, charlie, delta, ...(opts.bravo ? [bravo] : [])]
  const state = { bravoFails: true, portmap: [] as string[], asked: [] as string[] }
  await page.addInitScript((language) => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('sfpanel_language', language)
  }, opts.language ?? 'en')
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace('/api/v1', '')
    const node = url.searchParams.get('node') ?? 'm-1'
    const fail = (status: number, code: string, message: string) =>
      route.fulfill({ status, json: { success: false, error: { code, message } } })
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/auth/ws-ticket') data = { ticket: 'fixture-ticket' }
    else if (path === '/cluster/status') data = cluster
      ? { enabled: true, name: 'lab', node_count: nodes.length, leader_id: 'm-1', local_id: 'm-1', is_leader: true }
      : { enabled: false }
    else if (path === '/cluster/nodes') data = { nodes, local_id: 'm-1', is_leader: true }
    else if (path === '/cluster/overview') data = {
      name: 'lab', node_count: nodes.length, leader_id: 'm-1', nodes,
      metrics: [metrics('m-1', 23), metrics('m-2', 6), ...(opts.bravo ? [metrics('a-1', 41)] : [])],
    }
    else if (path === '/cluster/events') data = { events: [] }
    else if (path === '/system/overview') data = {
      host: { hostname: 'solo-server', os: 'linux', platform: 'debian', platform_version: '13', kernel: '6.12', uptime: 1, num_cpu: 4 },
      metrics: { cpu: 12, mem_percent: 40, disk_percent: 55 }, version: '0.88.0', metrics_history: [],
    }
    else if (path === '/system/portmap') {
      state.portmap.push(node)
      data = [
        { port: 22, proto: 'tcp', state: 'listening', firewall: null, containers: [], process: { pid: 1, name: 'sshd' } },
        { port: 8096, proto: 'tcp', state: 'listening', firewall: null, containers: [{ id: 'media-player-1-id', name: 'media-player-1', stack: 'media' }], process: null },
        { port: 41641, proto: 'udp', state: 'bound', firewall: null, containers: [], process: { pid: 2, name: 'tailscaled' } },
      ]
    }
    else if (path === '/docker/compose' || path === '/docker/containers') {
      state.asked.push(`${path}?${node}`)
      if (node === 'm-2') return fail(503, 'DOCKER_UNAVAILABLE', 'Docker is not available on this node')
      if (node === 'a-1' && path === '/docker/containers' && state.bravoFails) return fail(500, 'INTERNAL_ERROR', 'docker: daemon timed out')
      const fixture = node === 'a-1' ? BRAVO : ALPHA
      data = path === '/docker/compose' ? fixture.projects : fixture.containers
    }
    else if (path.endsWith('/inspect')) data = { state: 'running', hostname: 'h', image: 'i', ports: [], mounts: [], networks: [], env: [] }
    else if (/batch|history|events|metrics/.test(path)) data = []
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  return state
}

const region = (page: Page, name: string) => page.getByRole('region', { name, exact: true })

test('datacenter scope: totals, what needs attention in order, and links that lead somewhere', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/running')
  await expect(page.getByRole('heading', { name: 'Running', level: 2 })).toBeVisible()

  await expect(page.getByTestId('total-nodes')).toContainText('2 / 3')
  await expect(page.getByTestId('total-stacks')).toContainText('4')
  await expect(page.getByTestId('total-running')).toContainText('7')
  await expect(page.getByTestId('total-attention')).toContainText('4')

  const attention = region(page, 'Needs attention 4')
  const lines = attention.getByRole('listitem')
  await expect(lines).toHaveCount(4)
  await expect(lines.nth(0)).toContainText(/^charlie-agent — Offline, last seen 5 minutes ago\. View nodes/)
  await expect(lines.nth(1)).toContainText('photos · alpha-node — 3 of 4 services running. ml keeps restarting. Open stack')
  await expect(lines.nth(2)).toContainText('wiki · alpha-node — 1 of 2 services running. Open stack')
  await expect(lines.nth(3)).toContainText('backup-agent · alpha-node — Exited 2 hours ago with code 1. View logs')
  await expect(lines.nth(0).getByRole('link', { name: 'View nodes (charlie-agent)' })).toHaveAttribute('href', '/cluster/nodes?status=offline')
  await expect(lines.nth(1).getByRole('link', { name: 'Open stack (photos)' })).toHaveAttribute('href', '/cluster/stacks/m-1/photos')
  await expect(lines.nth(3).getByRole('link', { name: 'View logs (backup-agent)' })).toHaveAttribute('href', '/docker/containers?container=backup-agent-id&tab=logs')
  // Stopped on purpose and failed long ago are tiles, not lines.
  await expect(attention).not.toContainText('monitoring')
  await expect(attention).not.toContainText('old-test')

  const alphaRegion = region(page, 'alpha-node')
  await expect(alphaRegion).toContainText('Leader')
  await expect(alphaRegion).toContainText('CPU23%')
  await expect(alphaRegion.getByRole('heading', { name: 'Stacks 4', level: 4 })).toBeVisible()
  await expect(alphaRegion.getByRole('heading', { name: 'Standalone containers 3', level: 4 })).toBeVisible()
  await expect(alphaRegion.getByText('Partly running 3/4')).toBeVisible()
  await expect(alphaRegion.getByText('ml Restarting')).toBeVisible()
  await expect(alphaRegion.getByText('Stopped 0/2')).toBeVisible()

  // The attached node's ports open on the browser's own host, in a new tab.
  const player = alphaRegion.getByRole('link', { name: '8096 (opens in a new tab)' })
  await expect(player).toHaveAttribute('href', 'http://localhost:8096/')
  await expect(player).toHaveAttribute('target', '_blank')
  await expect(player).toHaveAttribute('rel', /noopener/)
  // Loopback-bound and UDP ports are words, not links.
  await expect(alphaRegion.getByText('9117 internal')).toBeVisible()
  await expect(alphaRegion.getByRole('link', { name: /9117/ })).toHaveCount(0)
  await expect(alphaRegion.getByText('53/udp')).toBeVisible()
  await expect(alphaRegion.getByRole('link', { name: /^53/ })).toHaveCount(0)

  // A stack tile opens the stack on its node.
  await expect(alphaRegion.getByRole('link', { name: 'media', exact: true })).toHaveAttribute('href', '/cluster/stacks/m-1/media')

  // The offline node is not asked; the node without Docker says so calmly.
  await expect(region(page, 'charlie-agent')).toContainText('A node that is not answering is not asked.')
  await expect(region(page, 'delta-node')).toContainText('Docker is not installed here, or not running.')
  await expect(region(page, 'delta-node').getByRole('alert')).toHaveCount(0)
})

test('a node whose containers fail shows why and a retry, and the others stay drawn', async ({ page }) => {
  const state = await mock(page, { bravo: true })
  await page.goto('/cluster/running')
  const bravoRegion = region(page, 'bravo-agent')
  const error = bravoRegion.getByRole('alert')
  await expect(error).toContainText('Could not load what runs here')
  await expect(error).toContainText('docker: daemon timed out')
  // The others are drawn, and the failed node is left out of the totals, which say so.
  await expect(region(page, 'alpha-node').getByRole('link', { name: 'photos', exact: true })).toBeVisible()
  await expect(page.getByTestId('total-stacks')).toContainText('4')
  await expect(page.getByText('1 node could not be loaded and is left out of the totals.')).toBeVisible()

  state.bravoFails = false
  await error.getByRole('button', { name: 'Retry' }).click()
  await expect(bravoRegion.getByRole('link', { name: 'cloud', exact: true })).toBeVisible()
  await expect(bravoRegion.getByRole('link', { name: '8080 (opens in a new tab)' })).toHaveAttribute('href', 'http://198.51.100.7:8080/')
  await expect(page.getByTestId('total-stacks')).toContainText('5')
  await expect(page.getByText(/could not be loaded/)).toHaveCount(0)
  await expect(region(page, 'alpha-node').getByRole('link', { name: 'photos', exact: true })).toBeVisible()

  // A container opens on its own node's containers screen: the node is selected on the way.
  await bravoRegion.getByRole('link', { name: 'agent-tool', exact: true }).click()
  await expect(page).toHaveURL(/\/docker\/containers\?container=agent-tool-id$/)
  expect(await page.evaluate(() => localStorage.getItem('sfpanel_current_node'))).toBe('a-1')
})

test('node scope on a single server: no node header, the same tiles', async ({ page }) => {
  await mock(page, { cluster: false })
  await page.goto('/running')
  await expect(page.getByRole('heading', { name: 'Running', level: 1 })).toBeVisible()
  await expect(page.getByText('What runs on solo-server right now')).toBeVisible()
  await expect(page.getByTestId('total-nodes')).toHaveCount(0)
  await expect(page.getByTestId('total-stacks')).toContainText('4')
  await expect(page.getByTestId('total-running')).toContainText('7')
  await expect(page.getByTestId('total-attention')).toContainText('3')
  // No node header: nothing to fold, no section named for the server.
  await expect(page.getByRole('button', { name: 'solo-server' })).toHaveCount(0)
  await expect(page.getByRole('region', { name: 'solo-server' })).toHaveCount(0)

  await expect(page.getByRole('heading', { name: 'Stacks 4', level: 2 })).toBeVisible()
  const lines = region(page, 'Needs attention 3').getByRole('listitem')
  await expect(lines.nth(0)).toContainText(/^photos — 3 of 4 services running/)
  await expect(lines.nth(0).getByRole('link', { name: 'Open stack (photos)' })).toHaveAttribute('href', '/docker/stacks/photos')
  await expect(page.getByRole('link', { name: 'media', exact: true })).toHaveAttribute('href', '/docker/stacks/media')
  await expect(page.getByRole('link', { name: '8096 (opens in a new tab)' })).toHaveAttribute('href', 'http://localhost:8096/')
  await expect(page.getByRole('link', { name: 'speedtest', exact: true })).toHaveAttribute('href', '/docker/containers?container=speedtest-id')

  // "View logs" opens the container on its logs.
  await region(page, 'Needs attention 3').getByRole('link', { name: 'View logs (backup-agent)' }).click()
  await expect(page).toHaveURL(/\/docker\/containers\?container=backup-agent-id&tab=logs$/)
  await expect(page.getByRole('dialog').getByRole('tab', { name: 'Logs' })).toHaveAttribute('aria-selected', 'true')
})

test('the search narrows the tiles, and a search with no match offers to clear it', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/running')
  const alphaRegion = region(page, 'alpha-node')
  await expect(alphaRegion.getByRole('link', { name: 'photos', exact: true })).toBeVisible()

  const search = page.getByRole('searchbox', { name: 'Find by stack, container or service name' })
  await search.fill('indexer')
  await expect(alphaRegion.getByRole('heading', { name: 'Stacks 1' })).toBeVisible()
  await expect(alphaRegion.getByRole('link', { name: 'media', exact: true })).toBeVisible()
  await expect(alphaRegion.getByRole('link', { name: 'photos', exact: true })).toHaveCount(0)
  await expect(alphaRegion.getByRole('heading', { name: /Standalone containers/ })).toHaveCount(0)

  await search.fill('no-such-thing')
  await expect(page.getByText('Nothing matches “no-such-thing”.')).toBeVisible()
  await expect(region(page, 'alpha-node')).toHaveCount(0)
  await page.getByRole('button', { name: 'Clear search' }).click()
  await expect(search).toHaveValue('')
  await expect(region(page, 'alpha-node').getByRole('link', { name: 'photos', exact: true })).toBeVisible()
})

test('a node section folds, and stays folded on the next visit', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/running')
  const toggle = region(page, 'alpha-node').getByRole('button', { name: 'alpha-node' })
  await expect(toggle).toHaveAttribute('aria-expanded', 'true')
  await toggle.click()
  await expect(toggle).toHaveAttribute('aria-expanded', 'false')
  await expect(region(page, 'alpha-node').getByRole('link', { name: 'photos', exact: true })).toHaveCount(0)
  await page.reload()
  // A reload fetches the app and the lazy page again, which a dev server on a
  // busy host can take longer than one assertion's wait for.
  await expect(page.getByRole('heading', { name: 'Running', level: 2 })).toBeVisible({ timeout: 20_000 })
  await expect(region(page, 'alpha-node').getByRole('button', { name: 'alpha-node' })).toHaveAttribute('aria-expanded', 'false')
})

test('the host listeners are asked for only when opened', async ({ page }) => {
  const state = await mock(page)
  await page.goto('/cluster/running')
  const alphaRegion = region(page, 'alpha-node')
  await expect(alphaRegion.getByRole('link', { name: 'photos', exact: true })).toBeVisible()
  expect(state.portmap).toEqual([])
  await alphaRegion.getByText('Services listening on the host').click()
  await expect(alphaRegion.getByText('Services listening on the host 2')).toBeVisible()
  await expect(alphaRegion.getByText('sshd :22')).toBeVisible()
  await expect(alphaRegion.getByText('tailscaled :41641/udp')).toBeVisible()
  expect(state.portmap).toEqual(['m-1'])
})

test('both routes are titled by the screen', async ({ page }) => {
  await mock(page, { language: 'ko' })
  await page.goto('/cluster/running')
  await expect(page.getByRole('heading', { name: '실행 현황', level: 2 })).toBeVisible()
  await expect(page).toHaveTitle('실행 현황 · SFPanel')
  await page.goto('/running')
  await expect(page.getByRole('heading', { name: '실행 현황', level: 1 })).toBeVisible()
  await expect(page).toHaveTitle('실행 현황 · SFPanel')
})

for (const width of [360, 320]) {
  test(`phone ${width}px: one column and no sideways scroll`, async ({ page }) => {
    await page.setViewportSize({ width, height: 740 })
    await mock(page)
    await page.goto('/cluster/running')
    const tiles = region(page, 'alpha-node').getByRole('list').filter({ has: page.getByRole('link', { name: 'photos', exact: true }) }).locator(':scope > li')
    await expect(tiles).toHaveCount(4)
    const xs = await tiles.evaluateAll((els) => els.map((el) => Math.round(el.getBoundingClientRect().left)))
    expect(new Set(xs).size).toBe(1)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    expect(await page.evaluate(() => {
      const main = document.querySelector('main')!
      return main.scrollWidth <= main.clientWidth
    })).toBe(true)
  })
}
