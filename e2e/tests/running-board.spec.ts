import { test, expect, type Page } from '@playwright/test'

// The running board: what runs on every node, with what needs looking at on
// top. Mocked API only: a cluster of an attached manager with stacks, an
// offline agent and a manager without Docker that is older than the guest
// list (and, for the failure case, an agent whose first container read fails;
// for the guests, a Proxmox host one of whose VMs is that manager), or one
// server with no cluster.
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
    // A job that ran once and ended with code 0: how this stack is meant to look.
    ctr('media-init-1', 'exited', 'Exited (0) 3 days ago', 'media'),
    ctr('photos-server-1', 'running', 'Up 2 hours', 'photos', [port(2283)]),
    ctr('photos-web-1', 'running', 'Up 2 hours', 'photos'),
    ctr('photos-redis-1', 'running', 'Up 2 hours', 'photos'),
    ctr('photos-ml-1', 'restarting', 'Restarting (1) 4 seconds ago', 'photos'),
    ctr('wiki-app-1', 'running', 'Up 5 hours', 'wiki', [port(3000), port(53, '0.0.0.0', 'udp')]),
    // Killed, and too long ago to be news of its own: the stack runs in part.
    ctr('wiki-db-1', 'exited', 'Exited (137) 2 days ago', 'wiki'),
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
const pve = { ...base, id: 'p-1', name: 'pve-host', kind: 'agent', role: '', status: 'online', api_address: '198.51.100.20:3628', last_seen: LAST_SEEN }

// The Proxmox host's guests: a VM that is delta-node (by its NIC address,
// which delta-node reports in lower case), an LXC container that is none of
// the nodes, and a VM that is stopped. Docker's example address range only.
const GiB = 1024 ** 3
const guest = (vmid: number, name: string, kind: string, status: string, macs: string[], extra = {}) => ({
  vmid, name, kind, status, cpus: 2, mem: status === 'running' ? GiB : 0, maxmem: 4 * GiB, uptime: 0, tags: [], macs, ...extra,
})
const GUESTS = [
  guest(101, 'delta-vm', 'qemu', 'running', ['02:42:AC:11:00:02'], { tags: ['prod'] }),
  guest(102, 'dns', 'lxc', 'running', ['02:42:AC:11:00:30'], { cpus: 0.5, mem: 256 * 1024 ** 2, maxmem: GiB }),
  guest(103, 'old-win', 'qemu', 'stopped', []),
]
const iface = (name: string, mac_address: string) => ({ name, type: 'ethernet', state: 'up', mac_address, mtu: 1500, speed: 0, addresses: [], is_default: false, driver: '' })
const INTERFACES: Record<string, unknown[]> = {
  'm-1': [iface('lo', ''), iface('eth0', '02:42:ac:11:00:10')],
  'm-2': [iface('lo', ''), iface('eth0', '02:42:ac:11:00:02')],
  'p-1': [iface('lo', ''), iface('vmbr0', '02:42:ac:11:00:20')],
}

const metrics = (id: string, cpu: number) => ({ node_id: id, cpu_percent: cpu, memory_percent: 61, disk_percent: 72, container_count: 0, uptime_seconds: 1, version: '0.88.0', timestamp: 0 })

interface Opts {
  cluster?: boolean
  /** Adds bravo-agent, whose container read fails until `state.bravoFails` is cleared. */
  bravo?: boolean
  /** Adds pve-host, a Proxmox host without Docker, with three guests. */
  pve?: boolean
  /** A node whose guest list Proxmox fails to give. */
  guestsFail?: string
  /** The node the node menu is on (node scope). */
  current?: string
  language?: 'en' | 'ko'
  /** Containers the attached node runs besides its own. */
  extra?: ReturnType<typeof ctr>[]
}

async function mock(page: Page, opts: Opts = {}) {
  const cluster = opts.cluster ?? true
  const nodes = [alpha, charlie, delta, ...(opts.bravo ? [bravo] : []), ...(opts.pve ? [pve] : [])]
  const state = { bravoFails: true, portmap: [] as string[], asked: [] as string[], interfaces: [] as string[] }
  await page.addInitScript(({ language, current }) => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('sfpanel_language', language)
    if (current) localStorage.setItem('sfpanel_current_node', current)
  }, { language: opts.language ?? 'en', current: opts.current ?? '' })
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
      metrics: [metrics('m-1', 23), metrics('m-2', 6), ...(opts.bravo ? [metrics('a-1', 41)] : []), ...(opts.pve ? [metrics('p-1', 17)] : [])],
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
    else if (path === '/system/guests') {
      if (node === opts.guestsFail) return fail(500, 'PVE_GUESTS_ERROR', 'pvesh failed: ipcc_send_rec[1] failed: Connection refused')
      // delta-node is older than the route.
      if (node === 'm-2') return fail(404, 'NOT_FOUND', 'Not Found')
      data = node === 'p-1' ? GUESTS : []
    }
    else if (path === '/network/interfaces') {
      state.interfaces.push(node)
      data = INTERFACES[node] ?? []
    }
    else if (path === '/docker/compose' || path === '/docker/containers') {
      state.asked.push(`${path}?${node}`)
      if (node === 'm-2' || node === 'p-1') return fail(503, 'DOCKER_UNAVAILABLE', 'Docker is not available on this node')
      if (node === 'a-1' && path === '/docker/containers' && state.bravoFails) return fail(500, 'INTERNAL_ERROR', 'docker: daemon timed out')
      const fixture = node === 'a-1' ? BRAVO : ALPHA
      data = path === '/docker/compose' ? fixture.projects : node === 'a-1' ? fixture.containers : [...fixture.containers, ...(opts.extra ?? [])]
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
  await expect(lines.nth(3)).toContainText('backup-agent · alpha-node — Exited 2 hours ago (exit code 1). View logs')
  await expect(lines.nth(0).getByRole('link', { name: 'View nodes (charlie-agent)' })).toHaveAttribute('href', '/cluster/nodes?status=offline')
  await expect(lines.nth(1).getByRole('link', { name: 'Open stack (photos)' })).toHaveAttribute('href', '/cluster/stacks/m-1/photos')
  // The containers screen shows the selected node, and its address does not
  // say which: what leads there from another node's line is a button that
  // selects the node on the way, not a link a new tab could open on the
  // wrong server.
  await expect(lines.nth(3).getByRole('button', { name: 'View logs (backup-agent)' })).toBeVisible()
  await expect(lines.nth(3).getByRole('link')).toHaveCount(0)
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

  // A stack whose only idle container ended with code 0 says its count and
  // how that container ended, and is no warning: not in the list above, and
  // not drawn as one. The stack with a killed container is both.
  const tileOf = (name: string) => alphaRegion.getByRole('listitem').filter({ has: page.getByRole('link', { name, exact: true }) })
  await expect(tileOf('media')).toContainText('Partly running 2/3')
  await expect(tileOf('media')).toContainText('init Exited 3 days ago')
  await expect(tileOf('media')).not.toHaveClass(/border-destructive/)
  await expect(tileOf('wiki')).toContainText('Partly running 1/2')
  await expect(tileOf('wiki')).toHaveClass(/border-destructive/)
  await expect(attention).not.toContainText('media')

  // A port chip is a tint of its own, not one laid over the tile: over a
  // warning tile's red tint the blue letters fell to 4.41:1.
  const chipBackground = await tileOf('photos').getByRole('link', { name: '2283 (opens in a new tab)' }).evaluate((el) => getComputedStyle(el).backgroundColor)
  expect(chipBackground, 'the chip paints an opaque colour').not.toMatch(/\/|rgba/)

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
  await bravoRegion.getByRole('button', { name: 'agent-tool', exact: true }).click()
  await expect(page).toHaveURL(/\/docker\/containers\?container=agent-tool-id$/)
  expect(await page.evaluate(() => localStorage.getItem('sfpanel_current_node'))).toBe('a-1')
})

// The page's own line said "Loading…" for as long as the page stayed open
// when the first read failed, above a body saying it could not be loaded.
test('a first load that fails is said to have failed, in the header too', async ({ page }) => {
  const state = await mock(page, { bravo: true, current: 'a-1' })
  await page.goto('/running')
  const main = page.getByRole('main')
  await expect(main.getByRole('alert')).toContainText('docker: daemon timed out')
  const header = main.locator('div').filter({ has: page.getByRole('heading', { name: 'Running', level: 1 }) }).last()
  await expect(header.getByRole('status')).toContainText('Unable to load data')
  await expect(header).not.toContainText('Loading…')
  // Its retry asks again, and then it says when it was read.
  state.bravoFails = false
  await header.getByRole('button', { name: 'Retry' }).click()
  await expect(main.getByRole('link', { name: 'cloud', exact: true })).toBeVisible()
  await expect(header).toContainText('Updated')
})

test('a node list that answers after the page was left asks no node', async ({ page }) => {
  const state = await mock(page)
  let release!: () => void
  const held = new Promise<void>((resolve) => { release = resolve })
  // Registered after the mock's, so tried first: the node list is held back.
  await page.route('**/api/v1/cluster/overview', async (route) => {
    await held
    await route.fallback()
  })
  await page.goto('/cluster/running')
  const title = page.getByRole('main').getByRole('heading', { name: 'Running', exact: true })
  await expect(title).toBeVisible()
  await page.getByRole('link', { name: 'Security', exact: true }).first().click()
  // Left, and not only by the address: the page itself is gone.
  await expect(page).toHaveURL(/\/cluster\/security/)
  await expect(title).toHaveCount(0)
  release()
  await page.waitForTimeout(800)
  expect(state.asked).toEqual([])
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

// A project started by hand has no stack screen: its tile opens the node's
// containers, searched by the project. Its containers are often named
// without it (container_name: grafana), and a search over names and images
// alone opened an empty list.
test('a project the panel does not manage opens its containers, whatever they are named', async ({ page }) => {
  await mock(page, { cluster: false, extra: [ctr('grafana', 'running', 'Up 1 hour', 'observ'), ctr('prom', 'running', 'Up 1 hour', 'observ')] })
  await page.goto('/running')
  const tile = page.getByRole('main').getByRole('link', { name: 'observ', exact: true })
  await expect(tile).toHaveAttribute('href', '/docker/containers?search=observ')
  await tile.click()
  await expect(page).toHaveURL(/\/docker\/containers\?search=observ$/)
  // The containers screen, with the project in its search field.
  await expect(page.getByRole('textbox', { name: 'Search containers' })).toHaveValue('observ')
  const main = page.getByRole('main')
  await expect(main.getByTitle('grafana', { exact: true })).toBeVisible()
  await expect(main.getByTitle('prom', { exact: true })).toBeVisible()
  await expect(main.getByTitle('media-player-1', { exact: true })).toHaveCount(0)
})

test('a Proxmox host lists its guests, and a VM that is one of the nodes leads to it', async ({ page }) => {
  const state = await mock(page, { pve: true })
  await page.goto('/cluster/running')
  const pveRegion = region(page, 'pve-host')
  await expect(pveRegion.getByRole('heading', { name: 'VMs and LXC containers 3', level: 4 })).toBeVisible()
  // The guests come above what Docker would have to say, which here is that it is not there.
  await expect(pveRegion).toContainText('Docker is not installed here, or not running.')

  const tiles = pveRegion.getByRole('list').filter({ hasText: 'delta-vm' }).locator(':scope > li')
  await expect(tiles).toHaveCount(3)
  await expect(tiles.nth(0)).toContainText('delta-vm')
  await expect(tiles.nth(0)).toContainText('Running')
  await expect(tiles.nth(0)).toContainText('VM 101 · CPU 2 · Memory 1.0 GB / 4.0 GB')
  await expect(tiles.nth(0).getByRole('list', { name: 'Tags' })).toHaveText('prod')
  await expect(tiles.nth(0)).toContainText('Cluster node delta-node')
  await expect(tiles.nth(1)).toContainText('LXC 102 · CPU 0.5 · Memory 256.0 MB / 1.0 GB')
  await expect(tiles.nth(1)).not.toContainText('Cluster node')
  await expect(tiles.nth(2)).toContainText('old-win')
  await expect(tiles.nth(2)).toContainText('Stopped')
  await expect(tiles.nth(2)).toContainText('VM 103 · CPU 2 · Memory 4.0 GB')

  // A stopped guest is not news, and guests add nothing to the totals.
  await expect(page.getByTestId('total-attention')).toContainText('4')
  await expect(region(page, 'Needs attention 4')).not.toContainText('old-win')
  await expect(page.getByTestId('total-stacks')).toContainText('4')
  // Interfaces are asked of the online nodes only: not of charlie-agent.
  expect([...new Set(state.interfaces)].sort()).toEqual(['m-1', 'm-2', 'p-1'])

  // The VM's node link brings delta-node's section into view, its header in focus.
  const toNode = tiles.nth(0).getByRole('link', { name: 'delta-node' })
  await expect(toNode).toHaveAttribute('href', '#running-node-m-2')
  await toNode.click()
  const deltaToggle = region(page, 'delta-node').getByRole('button', { name: 'delta-node' })
  await expect(deltaToggle).toBeFocused()
  await expect(deltaToggle).toBeInViewport()

  // The search finds a guest by its name and by its vmid.
  const search = page.getByRole('searchbox', { name: 'Find by stack, container or service name' })
  await search.fill('dns')
  await expect(pveRegion.getByRole('heading', { name: 'VMs and LXC containers 1' })).toBeVisible()
  await expect(region(page, 'alpha-node')).toHaveCount(0)
  await search.fill('103')
  await expect(pveRegion.getByText('old-win')).toBeVisible()
  await expect(pveRegion.getByText('delta-vm')).toHaveCount(0)
})

test('guests that could not be listed say so beside the stacks, and an older node shows no guests', async ({ page }) => {
  const state = await mock(page, { guestsFail: 'm-1' })
  await page.goto('/cluster/running')
  const alphaRegion = region(page, 'alpha-node')
  await expect(alphaRegion.getByRole('heading', { name: 'VMs and LXC containers', exact: true, level: 4 })).toBeVisible()
  const line = alphaRegion.getByRole('alert')
  await expect(line).toContainText('Could not load the list.')
  await expect(line).toContainText('pvesh failed: ipcc_send_rec[1] failed: Connection refused')
  await expect(alphaRegion.getByRole('link', { name: 'photos', exact: true })).toBeVisible()
  await expect(page.getByTestId('total-stacks')).toContainText('4')

  // delta-node answers 404: no group, and no error.
  const deltaRegion = region(page, 'delta-node')
  await expect(deltaRegion).toContainText('Docker is not installed here, or not running.')
  await expect(deltaRegion.getByRole('heading', { name: /VMs and LXC/ })).toHaveCount(0)
  await expect(deltaRegion.getByRole('alert')).toHaveCount(0)
  // No node has guests, so no node is asked for its interfaces.
  expect(state.interfaces).toEqual([])
})

test("node scope: a Proxmox host's VM that is another node leads to that node's board", async ({ page }) => {
  await mock(page, { pve: true, current: 'p-1' })
  await page.goto('/running')
  await expect(page.getByRole('heading', { name: 'VMs and LXC containers 3', level: 2 })).toBeVisible()
  // In the page, not the node tree beside it.
  const toNode = page.getByRole('main').getByRole('button', { name: 'delta-node' })
  await toNode.click()
  await expect.poll(() => page.evaluate(() => localStorage.getItem('sfpanel_current_node'))).toBe('m-2')
  await expect(page.getByText('Docker is not installed here, or not running.')).toBeVisible()
  await expect(page.getByRole('heading', { name: /VMs and LXC/ })).toHaveCount(0)
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

// The canvas: the same data as a map. Its chunk (with the library) is asked
// for only once the canvas is chosen: by its module path on the dev server,
// by its chunk name in a build.
const canvasChunk = /RunningCanvas|@xyflow|xyflow_react/
const canvas = (page: Page) => page.getByRole('application', { name: 'Running canvas' })
const box = (page: Page, name: string) => canvas(page).getByRole('group', { name, exact: true })

async function chooseCanvas(page: Page) {
  await page.getByRole('group', { name: 'View' }).getByRole('button', { name: 'Canvas' }).click()
  await expect(canvas(page)).toBeVisible({ timeout: 20_000 })
}

test.describe('canvas', () => {
  test('is fetched only when chosen, and the choice is kept', async ({ page }) => {
    const asked: string[] = []
    page.on('request', (r) => { if (canvasChunk.test(r.url())) asked.push(r.url()) })
    await mock(page)
    await page.goto('/cluster/running')
    const views = page.getByRole('group', { name: 'View' })
    await expect(views.getByRole('button', { name: 'Board' })).toHaveAttribute('aria-pressed', 'true')
    await expect(region(page, 'alpha-node').getByRole('link', { name: 'photos', exact: true })).toBeVisible()
    expect(asked).toEqual([])

    await chooseCanvas(page)
    expect(asked.length).toBeGreaterThan(0)
    await expect(views.getByRole('button', { name: 'Canvas' })).toHaveAttribute('aria-pressed', 'true')
    await expect(views.getByRole('button', { name: 'Board' })).toHaveAttribute('aria-pressed', 'false')
    await expect(page.getByTestId('total-stacks')).toHaveCount(0)

    await page.reload()
    await expect(canvas(page)).toBeVisible({ timeout: 20_000 })
    await views.getByRole('button', { name: 'Board' }).click()
    await expect(canvas(page)).toHaveCount(0)
    await expect(page.getByTestId('total-stacks')).toContainText('4')
  })

  test('draws a box per server and an edge from a VM to the server it is', async ({ page }) => {
    await mock(page, { pve: true })
    await page.goto('/cluster/running')
    await chooseCanvas(page)
    for (const name of ['alpha-node', 'charlie-agent', 'delta-node', 'pve-host']) await expect(box(page, name)).toBeVisible()
    const pveBox = box(page, 'pve-host')
    await expect(pveBox.getByRole('heading', { name: 'VMs and LXC containers 3' })).toBeVisible()
    await expect(pveBox.getByText('delta-vm')).toBeVisible()
    await expect(pveBox).toContainText('Running')
    await expect(pveBox).toContainText('Stopped')
    await expect(box(page, 'alpha-node').getByText('Partly running 3/4')).toBeVisible()
    await expect(box(page, 'delta-node')).toContainText('Docker is not installed here, or not running.')

    // The one guest that is a server is the one edge, named for what it joins.
    const edge = canvas(page).getByRole('img', { name: 'VM delta-vm on pve-host → delta-node' })
    await expect(edge).toHaveCount(1)
    await expect(canvas(page).locator('.react-flow__edge')).toHaveCount(1)
    await expect(canvas(page).locator('.react-flow__edge.animated')).toHaveCount(0)

    // A search draws only what it finds; a box with nothing left is its header.
    await page.getByRole('searchbox', { name: 'Find by stack, container or service name' }).fill('dns')
    await expect(pveBox.getByText('dns')).toBeVisible()
    await expect(pveBox.getByText('delta-vm')).toHaveCount(0)
    await expect(canvas(page).locator('.react-flow__edge')).toHaveCount(0)
    await expect(box(page, 'alpha-node')).toBeVisible()
    await expect(box(page, 'alpha-node').getByRole('link')).toHaveCount(0)
    await expect(box(page, 'alpha-node').getByRole('heading')).toHaveCount(1)
  })

  // The canvas has no list of what needs attention above it: a tile says so
  // itself, in words and a mark, not by its red edge alone. Both of these
  // read "Exited"; one failed two hours ago, the other three days ago.
  test('a tile that needs attention says so, not only by its colour', async ({ page }) => {
    await mock(page)
    await page.goto('/cluster/running')
    await chooseCanvas(page)
    const tile = (name: string) => box(page, 'alpha-node').getByRole('listitem').filter({ hasText: name })
    await expect(tile('backup-agent')).toContainText('Exited')
    await expect(tile('backup-agent')).toContainText('Needs attention')
    await expect(tile('photos')).toContainText('Needs attention')
    await expect(tile('old-test')).toContainText('Exited')
    await expect(tile('old-test')).not.toContainText('Needs attention')
    await expect(tile('media')).not.toContainText('Needs attention')
  })

  // A node that answered once and then stops answering keeps its box as it
  // was last seen. It has to say so: a container that died since would
  // otherwise go on reading "Running".
  test('a box whose node could not be refreshed says how old it is', async ({ page }) => {
    const state = await mock(page, { bravo: true })
    state.bravoFails = false
    await page.goto('/cluster/running')
    await chooseCanvas(page)
    const bravoBox = box(page, 'bravo-agent')
    await expect(bravoBox.getByRole('link', { name: 'cloud', exact: true })).toBeVisible()
    await expect(bravoBox).not.toContainText('Could not refresh')
    state.bravoFails = true
    await page.getByRole('button', { name: 'Refresh' }).click()
    await expect(bravoBox).toContainText(/Could not refresh; showing what was seen at /)
    await expect(bravoBox.getByRole('link', { name: 'cloud', exact: true })).toBeVisible()
  })

  test('a tile leads where it does on the board', async ({ page }) => {
    await mock(page)
    await page.goto('/cluster/running')
    await chooseCanvas(page)
    const photos = box(page, 'alpha-node').getByRole('link', { name: 'photos', exact: true })
    await expect(photos).toHaveAttribute('href', '/cluster/stacks/m-1/photos')
    await photos.click()
    await expect(page).toHaveURL(/\/cluster\/stacks\/m-1\/photos$/)
  })

  test("node scope: the server's one box, its tiles leading to this server's screens", async ({ page }) => {
    await mock(page, { cluster: false })
    await page.goto('/running')
    await chooseCanvas(page)
    const solo = box(page, 'solo-server')
    await expect(solo.getByRole('heading', { name: 'solo-server', level: 2 })).toBeVisible()
    await expect(solo).toContainText('CPU 12%')
    await expect(solo.getByRole('link', { name: 'photos', exact: true })).toHaveAttribute('href', '/docker/stacks/photos')
    await expect(canvas(page).locator('.react-flow__node')).toHaveCount(1)
  })

  test('a moved box stays where it was put, and auto arrange puts it back', async ({ page }) => {
    await mock(page, { pve: true })
    await page.goto('/cluster/running')
    await chooseCanvas(page)
    const node = canvas(page).locator('.react-flow__node[data-id="m-2"]')
    const where = () => node.evaluate((el) => (el as HTMLElement).style.transform)
    await expect(node).toBeVisible()
    const before = await where()

    // Dragged by its header: the name is no link, so pressing it starts a drag.
    const handle = box(page, 'delta-node').getByRole('heading', { name: 'delta-node' })
    const at = (await handle.boundingBox())!
    await page.mouse.move(at.x + 10, at.y + at.height / 2)
    await page.mouse.down()
    await page.mouse.move(at.x + 90, at.y + 120, { steps: 12 })
    await page.mouse.up()
    await expect.poll(where).not.toBe(before)
    const moved = await where()
    const stored = await page.evaluate(() => JSON.parse(localStorage.getItem('sfpanel-running-canvas:cluster') ?? '{}'))
    expect(Object.keys(stored)).toEqual(['m-2'])

    await page.reload()
    await expect(node).toBeVisible({ timeout: 20_000 })
    await expect.poll(where).toBe(moved)

    await canvas(page).getByRole('button', { name: 'Auto arrange' }).click()
    await expect.poll(where).toBe(before)
    expect(await page.evaluate(() => localStorage.getItem('sfpanel-running-canvas:cluster'))).toBeNull()
  })

  test('pressing a tile neither drags its box nor pans the canvas', async ({ page }) => {
    await mock(page)
    await page.goto('/cluster/running')
    await chooseCanvas(page)
    const node = canvas(page).locator('.react-flow__node[data-id="m-1"]')
    const viewport = canvas(page).locator('.react-flow__viewport')
    await expect(node).toBeVisible()
    const [boxBefore, viewBefore] = [await node.getAttribute('style'), await viewport.getAttribute('style')]
    const tile = (await box(page, 'alpha-node').getByRole('link', { name: 'media', exact: true }).boundingBox())!
    await page.mouse.move(tile.x + 4, tile.y + 4)
    await page.mouse.down()
    await page.mouse.move(tile.x + 80, tile.y + 60, { steps: 8 })
    await page.mouse.up()
    expect(await node.getAttribute('style')).toBe(boxBefore)
    expect(await viewport.getAttribute('style')).toBe(viewBefore)
  })

  test('a tile reached with Tab is brought into view', async ({ page }) => {
    await mock(page)
    await page.goto('/cluster/running')
    await chooseCanvas(page)
    const last = box(page, 'alpha-node').getByRole('button', { name: 'speedtest', exact: true })
    await expect(last).toBeVisible()
    // The background dragged up and to the side: the box goes out of view.
    const frame = (await canvas(page).boundingBox())!
    await page.mouse.move(frame.x + frame.width - 40, frame.y + frame.height - 40)
    await page.mouse.down()
    await page.mouse.move(frame.x + 40, frame.y + 40, { steps: 12 })
    await page.mouse.up()
    await expect(last).not.toBeInViewport()
    await last.focus()
    await expect(last).toBeInViewport()
  })

  test("follows the app's theme while open, and names its controls", async ({ page }) => {
    await mock(page)
    await page.goto('/cluster/running')
    await chooseCanvas(page)
    const flow = page.locator('.react-flow')
    await expect(flow).toHaveClass(/\blight\b/)
    await page.evaluate(() => {
      document.documentElement.classList.add('dark')
      window.dispatchEvent(new CustomEvent('sfpanel:themechange'))
    })
    await expect(flow).toHaveClass(/\bdark\b/)
    for (const name of ['Zoom in', 'Zoom out', 'Fit to screen', 'Auto arrange']) {
      await expect(canvas(page).getByRole('button', { name })).toBeVisible()
    }
    await expect(canvas(page).getByRole('button', { name: 'Toggle Interactivity' })).toHaveCount(0)
  })

  test('phone 360px: the canvas fills the screen below the header, and the page does not scroll sideways', async ({ page }) => {
    await page.setViewportSize({ width: 360, height: 740 })
    await mock(page, { pve: true })
    await page.goto('/cluster/running')
    await chooseCanvas(page)
    await expect(box(page, 'pve-host')).toBeVisible()
    const size = (await canvas(page).boundingBox())!
    expect(size.width).toBeLessThanOrEqual(360)
    expect(size.height).toBeGreaterThanOrEqual(740 * 0.7 - 1)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    expect(await page.evaluate(() => {
      const main = document.querySelector('main')!
      return main.scrollWidth <= main.clientWidth
    })).toBe(true)
  })
})
