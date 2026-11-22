import { test, expect, type Page } from '@playwright/test'

// The running page's network view: each network the nodes are on as a diagram
// of what nmap found there, and each node from the inside. Mocked API only,
// and in the shape and size of a real home network: a /24 with 28 hosts up —
// a gateway, a server on metal that made the scan, a Proxmox host with twelve
// guests of which four are nodes of this cluster, and fifteen other devices.
// Addresses are invented (10.20.30.0/24, Docker's example MAC range).
test.use({ viewport: { width: 1280, height: 900 }, locale: 'en-US' })

const NET = '10.20.30.0/24'
const ip = (n: number) => `10.20.30.${n}`
const mac = (n: number) => `02:42:ac:14:1e:${n.toString(16).padStart(2, '0')}`
const tcp = (...ports: number[]) => ports.map((port) => ({ port, proto: 'tcp' }))
const PVE = 'Proxmox Server Solutions GmbH'
const host = (n: number, extra: Record<string, unknown> = {}) => ({ ip: ip(n), mac: mac(n), vendor: '', hostname: '', self: false, timed_out: false, ports: [], ...extra })

const HOSTS = [
  host(1, { vendor: 'Example Electronics', hostname: 'router.lan', ports: tcp(53, 80) }),
  host(5, { self: true, hostname: 'alpha.lan', ports: tcp(22, 8443) }),
  host(10, { vendor: 'Example Electronics', hostname: 'pve.lan', ports: tcp(22, 3128, 8006) }),
  host(11, { vendor: PVE, ports: tcp(22, 80, 443) }),
  host(12, { vendor: PVE, ports: tcp(22) }),
  host(13, { vendor: PVE, ports: tcp(22, 3000, 8080) }),
  host(14, { vendor: PVE, ports: tcp(22, 5432) }),
  host(15, { vendor: PVE, hostname: 'nas.lan', ports: tcp(21, 80, 139, 443, 445, 5000, 5001, 5357) }),
  host(16, { vendor: PVE, ports: [{ port: 53, proto: 'tcp' }] }),
  host(17, { vendor: PVE, ports: tcp(22, 9000) }),
  host(18, { vendor: PVE, ports: tcp(80) }),
  host(19, { vendor: PVE, ports: tcp(22, 8123) }),
  host(20, { vendor: PVE, ports: tcp(22) }),
  host(21, { vendor: PVE, ports: tcp(22, 3306) }),
  host(22, { vendor: PVE }),
  host(30, { vendor: 'Raspberry Pi Trading', hostname: 'pi.lan', ports: tcp(22, 80, 443) }),
  host(31, { vendor: 'Espressif' }),
  host(32, { vendor: 'Espressif' }),
  host(33, { vendor: 'Espressif', hostname: 'plug-3.lan' }),
  host(34, { vendor: 'Example Electronics', hostname: 'printer.lan', ports: tcp(515, 631, 9100) }),
  host(35, { hostname: 'phone.lan', timed_out: true }),
  host(36, { vendor: 'Example Electronics', hostname: 'tv.lan', ports: tcp(8008, 8009, 8443) }),
  host(37),
  host(38),
  host(39, { ports: tcp(443) }),
  host(40, { ports: tcp(6668) }),
  host(41, { vendor: 'Example Electronics', hostname: 'camera.lan', ports: tcp(80, 554) }),
  host(42, { vendor: 'Example Electronics' }),
]
const result = (hosts: unknown[], finished = '2026-11-22T02:13:08Z', ports = true) => ({
  network: NET, interface: 'eth0', address: ip(5), gateway: ip(1), ports, started_at: '2026-11-22T02:12:04Z', finished_at: finished, hosts,
})

const base = { role: 'voter', joined_at: '2026-01-01T00:00:00Z', grpc_address: `${ip(5)}:3629`, status: 'online', last_seen: '2026-11-22T02:00:00Z' }
const NODES = [
  { ...base, id: 'm-1', name: 'alpha-node', kind: 'manager', api_address: `${ip(5)}:3628` },
  { ...base, id: 'p-1', name: 'pve-host', kind: 'agent', role: '', api_address: `${ip(10)}:3628` },
  { ...base, id: 'm-2', name: 'delta-node', kind: 'manager', api_address: `${ip(11)}:3628` },
  { ...base, id: 'a-1', name: 'bravo-agent', kind: 'agent', role: '', api_address: `${ip(12)}:3628` },
  { ...base, id: 'a-3', name: 'echo-agent', kind: 'agent', role: '', api_address: `${ip(13)}:3628` },
  { ...base, id: 'a-4', name: 'foxtrot-agent', kind: 'agent', role: '', api_address: `${ip(14)}:3628` },
]
/** The last number of each node's address. */
const AT: Record<string, number> = { 'm-1': 5, 'p-1': 10, 'm-2': 11, 'a-1': 12, 'a-3': 13, 'a-4': 14 }

const GiB = 1024 ** 3
const guest = (vmid: number, name: string, kind: string, n: number | null, status = 'running') => ({
  vmid, name, kind, status, cpus: 2, mem: status === 'running' ? GiB : 0, maxmem: 4 * GiB, uptime: 0, tags: [], macs: n === null ? [] : [mac(n).toUpperCase()],
})
const GUESTS = [
  guest(101, 'delta-vm', 'qemu', 11), guest(102, 'bravo-vm', 'qemu', 12), guest(103, 'echo-vm', 'qemu', 13), guest(104, 'foxtrot-vm', 'qemu', 14),
  guest(105, 'nas', 'qemu', 15), guest(106, 'dns', 'lxc', 16), guest(107, 'docs', 'qemu', 17), guest(108, 'blog', 'qemu', 18),
  guest(109, 'home', 'qemu', 19), guest(110, 'build', 'qemu', 20), guest(111, 'db', 'qemu', 21), guest(112, 'lab', 'qemu', 22),
  guest(113, 'old-win', 'qemu', 60, 'stopped'),
]

const nic = (name: string, mac_address: string, addresses: [string, number][] = [], extra: Record<string, unknown> = {}) => ({
  name, type: 'ethernet', state: 'up', mac_address, mtu: 1500, speed: 1000, driver: '', is_default: false,
  addresses: addresses.map(([address, prefix]) => ({ address, prefix, family: address.includes(':') ? 'ipv6' : 'ipv4' })), ...extra,
})
const interfaces = (node: string) => [
  nic('lo', '', [['127.0.0.1', 8]]),
  nic(node === 'p-1' ? 'vmbr0' : 'eth0', mac(AT[node]), [[ip(AT[node]), 24], ['fe80::42:acff:fe14:1e05', 64]], { is_default: true }),
  ...(node === 'm-1' ? [nic('docker0', '02:42:ac:11:00:01', [['172.17.0.1', 16]]), nic('br-0123456789ab', '02:42:ac:12:00:01', [['172.18.0.1', 16]]), nic('veth1a2b3c', '02:42:ac:12:00:09'), nic('tailscale0', '', [['100.64.0.5', 32]])] : []),
]
const PORTMAP = [
  { port: 22, proto: 'tcp', state: 'listening', firewall: { action: 'ALLOW', scope: 'Anywhere', rule_id: 1, source: 'ufw' }, containers: [], process: { pid: 1, name: 'sshd' }, addresses: ['0.0.0.0', '::'] },
  { port: 53, proto: 'udp', state: 'listening', firewall: null, containers: [], process: { pid: 2, name: 'systemd-resolve' }, addresses: ['127.0.0.53', '127.0.0.54'] },
  { port: 631, proto: 'tcp', state: 'listening', firewall: null, containers: [], process: { pid: 3, name: 'cupsd' }, addresses: ['127.0.0.1', '::1'] },
  { port: 5432, proto: 'tcp', state: 'listening', firewall: { action: 'DENY', scope: 'Anywhere', rule_id: 4, source: 'ufw' }, containers: [], process: { pid: 4, name: 'postgres' }, addresses: [ip(5)] },
  { port: 8443, proto: 'tcp', state: 'listening', firewall: null, containers: [{ id: 'c1', name: 'proxy-1', stack: 'edge' }], process: { pid: 5, name: 'docker-proxy' }, addresses: ['0.0.0.0', '::'] },
  { port: 41641, proto: 'udp', state: 'listening', firewall: null, containers: [], process: { pid: 6, name: 'tailscaled' }, addresses: ['*'] },
]

interface Opts {
  cluster?: boolean
  /** No scan has been made yet. */
  unscanned?: boolean
  /** nmap is on no node. */
  noTool?: boolean
  /** How the scan the test starts ends. */
  ends?: 'done' | 'failed'
  /** Nodes older than the route. */
  old?: boolean
  /** Every answer takes between 150 and 1100 ms, as on a real panel. */
  slow?: boolean
  /** foxtrot-agent goes out through a router of its own: the guest at .17. */
  twoGateways?: boolean
  /** What answers at bravo-agent's address is another device; bravo-agent itself is not seen. */
  conflict?: boolean
  view?: string
}

async function mock(page: Page, opts: Opts = {}) {
  const cluster = opts.cluster ?? true
  const state = {
    posts: [] as { node: string; body: unknown }[],
    deletes: [] as string[],
    scanReads: 0,
    overviews: 0,
    portmap: [] as string[],
    running: null as null | { network: string; ports: boolean; started_at: string },
    last: null as null | { network: string; ports: boolean; state: string; at: string },
    results: opts.unscanned ? [] as unknown[] : [result(opts.conflict ? HOSTS.map((h) => (h.ip === ip(12) ? host(12, { mac: mac(77), vendor: 'Example Electronics' }) : h)) : HOSTS)],
    /** Reads of the scanning node's status left before the scan ends. */
    left: 0,
  }
  await page.addInitScript(({ view }) => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('sfpanel_language', 'en')
    if (!localStorage.getItem('sfpanel-running-view')) localStorage.setItem('sfpanel-running-view', view)
  }, { view: opts.view ?? 'network' })
  let turn = 0
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace('/api/v1', '')
    const node = url.searchParams.get('node') ?? 'm-1'
    const fail = (status: number, code: string, message: string) => route.fulfill({ status, json: { success: false, error: { code, message } } })
    if (opts.slow) await new Promise((r) => setTimeout(r, 150 + ((turn++ * 397) % 950)))
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/auth/ws-ticket') data = { ticket: 'fixture-ticket' }
    else if (path === '/cluster/status') data = cluster
      ? { enabled: true, name: 'lab', node_count: NODES.length, leader_id: 'm-1', local_id: 'm-1', is_leader: true }
      : { enabled: false }
    else if (path === '/cluster/nodes') data = { nodes: NODES, local_id: 'm-1', is_leader: true }
    else if (path === '/cluster/overview') {
      state.overviews++
      data = { name: 'lab', node_count: NODES.length, leader_id: 'm-1', nodes: NODES, metrics: [] }
    }
    else if (path === '/cluster/events') data = { events: [] }
    else if (path === '/system/overview') data = {
      host: { hostname: 'alpha-node', os: 'linux', platform: 'debian', platform_version: '13', kernel: '6.12', uptime: 1, num_cpu: 4 },
      metrics: { cpu: 12, mem_percent: 40, disk_percent: 55 }, version: '0.90.0', metrics_history: [],
    }
    else if (path === '/system/portmap') {
      state.portmap.push(node)
      data = node === 'm-1' ? PORTMAP : PORTMAP.slice(0, 2)
    }
    else if (path === '/system/guests') data = node === 'p-1' ? GUESTS : []
    else if (path === '/network/interfaces') data = interfaces(node)
    else if (path === '/network/scan') {
      if (opts.old) return fail(404, 'NOT_FOUND', 'Not Found')
      const method = request.method()
      if (method === 'POST') {
        state.posts.push({ node, body: request.postDataJSON() })
        if (state.running) return fail(409, 'LAN_SCAN_IN_PROGRESS', 'a scan is already running')
        const body = request.postDataJSON() as { network: string; ports: boolean }
        state.running = { network: body.network, ports: body.ports, started_at: '2026-11-22T03:00:00Z' }
        state.left = 2
        data = { started: true }
      } else if (method === 'DELETE') {
        state.deletes.push(node)
        const was = state.running
        state.running = null
        if (was) state.last = { network: was.network, ports: was.ports, state: 'cancelled', at: '2026-11-22T03:00:05Z' }
        data = { cancelled: !!was }
      } else {
        const scanner = node === 'm-1'
        if (scanner) {
          state.scanReads++
          if (state.running && state.left-- <= 0) {
            const was = state.running
            state.running = null
            state.last = { network: was.network, ports: was.ports, state: opts.ends ?? 'done', at: '2026-11-22T03:01:00Z' }
            // The new scan finds one device more, and looked for ports or not as asked.
            if ((opts.ends ?? 'done') === 'done') state.results = [result([...HOSTS, host(50, { hostname: 'new.lan' })], '2026-11-22T03:01:00Z', was.ports)]
          }
        }
        const address = AT[node]
        data = {
          installed: !opts.noTool && (node === 'm-1' || node === 'm-2'),
          networks: [{ cidr: NET, interface: 'eth0', address: ip(address), mac: mac(address), gateway: ip(opts.twoGateways && node === 'a-4' ? 17 : 1), scannable: true, reason: '' }],
          running: scanner ? state.running : null,
          last: scanner ? state.last : null,
          results: scanner ? state.results : [],
        }
      }
    }
    else if (path === '/docker/compose' || path === '/docker/containers') return fail(503, 'DOCKER_UNAVAILABLE', 'Docker is not available on this node')
    else if (/batch|history|events|metrics/.test(path)) data = []
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  return state
}

const device = (page: Page, n: number) => page.locator(`[data-testid="net-device"][data-ip="${ip(n)}"]`)

// ---- tests ----

test('a scanned network as a diagram: the gateway on top, the nodes with their guests inside their host, the rest by address', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/running')
  const network = page.locator('[data-testid="net-network"]')
  await expect(network.getByRole('heading', { name: NET, level: 3 })).toBeVisible()
  await expect(network).toContainText('Devices: 28 · scanned from alpha-node')
  await expect(network).toContainText('ports checked')
  await expect(page.getByTestId('net-device')).toHaveCount(28)

  // The gateway, under where the network leaves.
  await expect(page.getByTestId('net-diagram').getByText('Internet', { exact: true })).toBeVisible()
  await expect(device(page, 1)).toHaveAttribute('data-kind', 'gateway')
  await expect(device(page, 1)).toContainText('router.lan')
  await expect(device(page, 1)).toContainText('Gateway · Example Electronics')

  // Each kind counted as what it is: six nodes, eight guests that are not nodes, thirteen others.
  await expect(network.getByText('Servers 6 · VMs and LXC containers 8', { exact: true })).toBeVisible()
  await expect(network.getByText('Other devices 13', { exact: true })).toBeVisible()

  // The Proxmox host holds its twelve guests: four of them nodes, named as nodes.
  const host = page.getByTestId('net-host')
  await expect(host).toHaveCount(1)
  await expect(host.getByTestId('net-device')).toHaveCount(13)
  await expect(device(page, 10)).toContainText('pve-host')
  await expect(host.locator(`[data-ip="${ip(11)}"]`)).toHaveAttribute('data-kind', 'server')
  await expect(host.locator(`[data-ip="${ip(11)}"]`)).toContainText('delta-node')
  // Inside its host a guest says its number, not once more whose it is; and
  // not who made its network card, which is the hypervisor every time.
  await expect(host.locator(`[data-ip="${ip(11)}"] summary`)).toContainText('Server · VM 101')
  await expect(host.locator(`[data-ip="${ip(11)}"] summary`)).not.toContainText('pve-host')
  await expect(host.locator(`[data-ip="${ip(11)}"] summary`)).not.toContainText('Proxmox')
  await expect(host.locator(`[data-ip="${ip(15)}"]`)).toHaveAttribute('data-kind', 'vm')
  await expect(host.locator(`[data-ip="${ip(15)}"]`)).toContainText('nas')
  await expect(host.locator(`[data-ip="${ip(15)}"]`)).toContainText('VM 105 · nas.lan')
  await expect(host.locator(`[data-ip="${ip(16)}"]`)).toHaveAttribute('data-kind', 'lxc')
  await expect(host.locator(`[data-ip="${ip(16)}"]`)).toContainText('LXC 106')
  // A guest that is stopped answered nothing and is not drawn.
  await expect(network).not.toContainText('old-win')

  // The node that scanned stands on its own, before the host: by address.
  await expect(device(page, 5)).toHaveAttribute('data-kind', 'server')
  await expect(device(page, 5)).toContainText('alpha-node')
  await expect(device(page, 5)).toContainText('Scanned from here')
  await expect(host.locator(`[data-ip="${ip(5)}"]`)).toHaveCount(0)
  const order = await page.getByTestId('net-device').evaluateAll((els) => els.map((el) => el.getAttribute('data-ip')))
  expect(order.slice(0, 3)).toEqual([ip(1), ip(5), ip(10)])
  const others = await page.locator('[data-testid="net-device"][data-kind="device"]').evaluateAll((els) => els.map((el) => Number(el.getAttribute('data-ip')!.split('.')[3])))
  expect(others).toEqual([30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42])

  // Names: what DNS said, else who made it, else that there is none — with the MAC to tell two alike apart.
  await expect(device(page, 30)).toContainText('pi.lan')
  await expect(device(page, 30)).toContainText('Raspberry Pi Trading')
  await expect(device(page, 31)).toContainText('Espressif')
  await expect(device(page, 31)).toContainText(mac(31))
  await expect(device(page, 37)).toContainText('No name')
  await expect(device(page, 35)).toContainText('The port check timed out.')
})

test('the lines of the diagram: one trunk that ends at the last row, rows whose cards stand level', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/running')
  await expect(device(page, 42)).toBeVisible()

  // The trunk is drawn piece by piece, by each block for its own height; the
  // last block's piece stops at its rail. A block that is not directly under
  // the trunk's element would end the trunk half way down.
  const trunk = await page.locator('[data-testid="net-diagram"] > .nm-rows').evaluate((rows) => {
    const blocks = [...rows.children].filter((el) => el.classList.contains('nm-block'))
    return blocks.map((el) => ({ own: Math.round(el.getBoundingClientRect().height), piece: Math.round(parseFloat(getComputedStyle(el, '::before').height)) }))
  })
  expect(trunk.length).toBeGreaterThan(5)
  for (const block of trunk.slice(0, -1)) expect(block.piece).toBe(block.own)
  expect(trunk.at(-1)!.piece).toBeLessThan(20)
  expect(trunk.at(-1)!.own).toBeGreaterThan(60)

  // The cards of a row stand at one height and one beside the other; a row
  // holds what fits, and the next starts under it.
  const boxes = await page.locator('[data-testid="net-device"][data-kind="device"]').evaluateAll((els) => els.map((el) => {
    const r = el.getBoundingClientRect()
    return { top: Math.round(r.top), left: Math.round(r.left), bottom: Math.round(r.bottom), width: Math.round(r.width) }
  }))
  const firstRow = boxes.filter((b) => b.top === boxes[0].top)
  expect(firstRow.length).toBeGreaterThan(1)
  expect(firstRow.length).toBeLessThan(boxes.length)
  expect(new Set(firstRow.map((b) => b.bottom)).size).toBe(1)
  expect(new Set(boxes.map((b) => b.width)).size).toBe(1)
  expect(boxes[firstRow.length].top).toBeGreaterThan(firstRow[0].bottom)
  expect(boxes[firstRow.length].left).toBe(firstRow[0].left)

  // The gateway is above everything that hangs from it.
  const gateway = await device(page, 1).boundingBox()
  const first = await device(page, 5).boundingBox()
  expect(gateway!.y + gateway!.height).toBeLessThan(first!.y)
})

test('ports: a link where a browser may get an answer, words otherwise, and the whole list on opening the card', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/running')
  const nas = device(page, 15)
  await expect(nas.getByRole('link', { name: /^80/ })).toHaveAttribute('href', `http://${ip(15)}/`)
  await expect(nas.getByRole('link', { name: /^443/ })).toHaveAttribute('href', `https://${ip(15)}/`)
  await expect(nas.getByRole('link', { name: /^80/ })).toHaveAttribute('target', '_blank')
  await expect(nas.getByRole('listitem').filter({ hasText: '139' })).toContainText('139SMB')
  await expect(nas.getByRole('link', { name: /139|445|21/ })).toHaveCount(0)
  // Eight are open: five are shown and the rest counted.
  await expect(nas.getByRole('list', { name: 'Open ports' }).getByRole('listitem')).toHaveCount(6)
  await expect(nas.getByText('+3', { exact: true })).toBeVisible()

  // Closed until asked for.
  await expect(nas.getByText(mac(15), { exact: true })).toBeHidden()
  await nas.locator('summary').click()
  await expect(nas.getByText(mac(15), { exact: true })).toBeVisible()
  await expect(nas.getByText('21, 80, 139, 443, 445, 5000, 5001, 5357', { exact: true })).toBeVisible()
  await expect(nas.getByText('Proxmox Server Solutions GmbH', { exact: true })).toBeVisible()

  // A device with nothing open says so where the whole list is, not on its face.
  const plug = device(page, 31)
  await expect(plug.getByRole('list', { name: 'Open ports' })).toHaveCount(0)
  await plug.locator('summary').click()
  await expect(plug.locator('dd').last()).toHaveText('None')
})

test('searching keeps what matches and the gateway, and says when nothing does', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/running')
  await expect(page.getByTestId('net-device')).toHaveCount(28)
  const search = page.getByRole('searchbox', { name: 'Find a device by address, name, maker, MAC or port' })
  await search.fill('printer')
  await expect(page.getByTestId('net-device')).toHaveCount(2)
  await expect(device(page, 34)).toBeVisible()
  await expect(device(page, 1)).toBeVisible()

  // A guest that matches keeps its host around it.
  await search.fill('nas')
  await expect(page.getByTestId('net-host').getByTestId('net-device')).toHaveCount(2)
  await expect(device(page, 15)).toBeVisible()

  await search.fill('9100')
  await expect(device(page, 34)).toBeVisible()
  await expect(page.locator('[data-testid="net-device"][data-kind="device"]')).toHaveCount(1)

  await search.fill('no-such-thing')
  await expect(page.getByText('Nothing matches “no-such-thing”.')).toBeVisible()
  await expect(page.getByTestId('net-diagram')).toHaveCount(0)
  await page.getByRole('button', { name: 'Clear search' }).click()
  await expect(page.getByTestId('net-device')).toHaveCount(28)
})

test('scanning again: asked of the node that scanned last, watched until it is done, and the new result drawn', async ({ page }) => {
  const state = await mock(page)
  await page.goto('/cluster/running')
  await expect(page.getByTestId('net-device')).toHaveCount(28)
  await expect(page.getByText('alpha-node checks the 1000 most used ports of every device it finds. About a minute.')).toBeVisible()
  await page.getByRole('button', { name: 'Scan again' }).click()
  await expect(page.getByRole('status').filter({ hasText: 'alpha-node is looking for devices and open ports…' })).toBeVisible()
  expect(state.posts).toEqual([{ node: 'm-1', body: { network: NET, ports: true } }])
  // The earlier result stays while the new one is made.
  await expect(page.getByTestId('net-device')).toHaveCount(28)

  await expect(device(page, 50)).toBeVisible({ timeout: 15_000 })
  await expect(device(page, 50)).toContainText('new.lan')
  await expect(page.locator('[data-testid="net-network"]')).toContainText('Devices: 29')
  await expect(page.getByRole('button', { name: 'Scan again' })).toBeVisible()
  await expect(page.getByRole('alert')).toHaveCount(0)
})

test('a scan for devices only says so, and is remembered as the choice', async ({ page }) => {
  const state = await mock(page)
  await page.goto('/cluster/running')
  await page.getByRole('checkbox', { name: 'Check open ports too' }).uncheck()
  await expect(page.getByText('alpha-node asks every address of this network to answer. About ten seconds.')).toBeVisible()
  await page.getByRole('button', { name: 'Scan again' }).click()
  await expect(page.getByRole('status').filter({ hasText: 'alpha-node is looking for devices…' })).toBeVisible()
  expect(state.posts[0].body).toEqual({ network: NET, ports: false })
  await expect(page.locator('[data-testid="net-network"]')).toContainText('devices only', { timeout: 15_000 })
  await page.reload()
  await expect(page.getByRole('checkbox', { name: 'Check open ports too' })).not.toBeChecked()
})

test('the node that scans can be chosen where more than one can', async ({ page }) => {
  const state = await mock(page)
  await page.goto('/cluster/running')
  const from = page.getByRole('combobox', { name: 'Scan from' })
  // Only the nodes that have nmap, the one that scanned last first.
  await expect(from.getByRole('option')).toHaveText(['alpha-node', 'delta-node'])
  await from.selectOption('delta-node')
  await expect(page.getByText('delta-node checks the 1000 most used ports of every device it finds. About a minute.')).toBeVisible()
  await page.getByRole('button', { name: 'Scan again' }).click()
  await expect.poll(() => state.posts.length).toBe(1)
  expect(state.posts[0].node).toBe('m-2')
})

test('a running scan can be stopped, and the page says it was', async ({ page }) => {
  const state = await mock(page)
  await page.goto('/cluster/running')
  await page.getByRole('button', { name: 'Scan again' }).click()
  await page.getByRole('button', { name: 'Stop' }).click()
  await expect(page.getByRole('alert')).toHaveText(/The scan of 10\.20\.30\.0\/24 from alpha-node was stopped\./)
  expect(state.deletes).toEqual(['m-1'])
  await expect(page.getByRole('button', { name: 'Scan again' })).toBeVisible()
  await expect(page.getByTestId('net-device')).toHaveCount(28)
  await page.getByRole('button', { name: 'Close' }).click()
  await expect(page.getByRole('alert')).toHaveCount(0)
})

test('a scan that fails leaves the earlier result and says so', async ({ page }) => {
  await mock(page, { ends: 'failed' })
  await page.goto('/cluster/running')
  await page.getByRole('button', { name: 'Scan again' }).click()
  await expect(page.getByRole('alert')).toHaveText(/The scan of 10\.20\.30\.0\/24 from alpha-node failed\. The earlier result is still shown\./, { timeout: 15_000 })
  await expect(page.getByTestId('net-device')).toHaveCount(28)
  await expect(page.locator('[data-testid="net-network"]')).toContainText('Devices: 28')
})

test('a network not scanned yet is listed with the nodes on it and a way to scan it', async ({ page }) => {
  const state = await mock(page, { unscanned: true })
  await page.goto('/cluster/running')
  const network = page.locator('[data-testid="net-network"]')
  await expect(network).toHaveCount(1)
  await expect(network).toContainText('Not scanned yet. Servers on this network: alpha-node, delta-node, bravo-agent, echo-agent, foxtrot-agent, pve-host')
  await expect(network).toContainText('A scan finds the devices that answer on this network and draws them by address.')
  await expect(page.getByTestId('net-diagram')).toHaveCount(0)
  await network.getByRole('button', { name: 'Scan', exact: true }).click()
  await expect.poll(() => state.posts.length).toBe(1)
  expect(state.posts[0]).toEqual({ node: 'm-1', body: { network: NET, ports: true } })
  await expect(page.getByTestId('net-device')).toHaveCount(29, { timeout: 15_000 })
})

test('without nmap on any node the page says which node lacks it and leads to its packages', async ({ page }) => {
  await mock(page, { unscanned: true, noTool: true })
  await page.goto('/cluster/running')
  const network = page.locator('[data-testid="net-network"]')
  await expect(network).toContainText('nmap is not installed on alpha-node.')
  await expect(network.getByRole('button', { name: /^Scan/ })).toHaveCount(0)
  // The packages screen shows the selected node: a button that selects it on the way, not a link.
  await network.getByRole('button', { name: 'Install nmap in Packages' }).click()
  await expect(page).toHaveURL(/\/packages$/)
})

test('nodes older than the scan say so, and still show their interfaces and ports', async ({ page }) => {
  await mock(page, { old: true })
  await page.goto('/cluster/running')
  await expect(page.getByText("This server's panel version has no network scan. Update it to use this.")).toBeVisible()
  await expect(page.locator('[data-testid="net-network"]')).toHaveCount(0)
  await expect(page.getByTestId('net-server')).toHaveCount(6)
})

test('each server from the inside: its networks, and its ports by who can reach them', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/running')
  await expect(page.getByRole('heading', { name: 'Networks and ports by server', level: 3 })).toBeVisible()
  const servers = page.getByTestId('net-server')
  await expect(servers).toHaveCount(6)
  const alpha = page.locator('[data-testid="net-server"][data-node="m-1"]')
  await expect(alpha.locator('summary').first()).toContainText('alpha-node')
  await expect(alpha.locator('summary').first()).toContainText(`${ip(5)}/24`)
  await expect(alpha.locator('summary').first()).toContainText('Open ports 4 · server only 2')
  // Six servers: closed until one is opened.
  await expect(alpha.getByTestId('net-interface').first()).toBeHidden()
  await alpha.locator('summary').first().click()

  const nics = alpha.getByTestId('net-interface')
  await expect(nics).toHaveCount(2)
  await expect(nics.nth(0)).toContainText('eth0')
  await expect(nics.nth(0)).toContainText('Default route')
  await expect(nics.nth(0)).toContainText(`${ip(5)}/24`)
  await expect(nics.nth(0)).toContainText(mac(5))
  // A link-local address says nothing; Docker's own bridges are counted, not listed.
  await expect(nics.nth(0)).not.toContainText('fe80')
  await expect(nics.nth(1)).toContainText('tailscale0')
  await expect(alpha).toContainText('Inner networks (Docker and the like) not shown: 2')
  await expect(alpha).not.toContainText('172.17.0.1')

  const open = alpha.getByRole('list', { name: 'Ports reachable from elsewhere' }).getByTestId('net-port')
  await expect(open).toHaveCount(4)
  await expect(open.nth(0)).toHaveText(/^22\/tcp\s*Every address\s*sshd\s*Allow · Anywhere$/)
  await expect(open.nth(1)).toHaveText(new RegExp(`^5432/tcp\\s*${ip(5).replace(/\./g, '\\.')}\\s*postgres\\s*Deny · Anywhere$`))
  await expect(open.nth(2)).toHaveText(/^8443\/tcp\s*Every address\s*proxy-1 \(edge stack\)\s*No firewall rule$/)
  await expect(open.nth(3)).toHaveText(/^41641\/udp\s*Every address\s*tailscaled\s*No firewall rule$/)

  // What only the server itself reaches is kept apart, behind a line of its own.
  const local = alpha.getByText('Ports open to the server alone: 2', { exact: true })
  await expect(alpha.locator('[data-port="53/udp"]')).toBeHidden()
  await local.click()
  await expect(alpha.locator('[data-port="53/udp"]')).toHaveText(/^53\/udp\s*Server only\s*systemd-resolve/)
  await expect(alpha.locator('[data-port="631/tcp"]')).toHaveText(/^631\/tcp\s*Server only\s*cupsd/)

})

test('the port maps are read when the view opens and when the operator asks, not with the board\'s timer', async ({ page }) => {
  // A port map walks every socket of a node. The board refreshes itself every
  // fifteen seconds; this view's own reads do not ride on that.
  await page.clock.install()
  const state = await mock(page)
  await page.goto('/cluster/running')
  await expect(page.getByTestId('net-device')).toHaveCount(28)
  await expect.poll(() => new Set(state.portmap).size).toBe(6)
  const asked = state.portmap.length
  const overviews = state.overviews
  await page.clock.runFor(31_000)
  await expect.poll(() => state.overviews).toBeGreaterThan(overviews)
  expect(state.portmap.length).toBe(asked)
  expect(state.scanReads).toBeLessThanOrEqual(2)

  await page.getByRole('button', { name: 'Refresh' }).click()
  await expect.poll(() => state.portmap.length).toBeGreaterThanOrEqual(asked + 6)
})

test('one server with no cluster: its own network, its own ports open on the page', async ({ page }) => {
  await mock(page, { cluster: false })
  await page.goto('/running')
  await expect(page.getByRole('heading', { name: 'Running', level: 1 })).toBeVisible()
  await expect(page.locator('[data-testid="net-network"]').getByRole('heading', { name: NET, level: 2 })).toBeVisible()
  await expect(page.getByTestId('net-device')).toHaveCount(28)
  // Alone it knows no other node: the scan's own address is the one server.
  await expect(page.locator('[data-testid="net-device"][data-kind="server"]')).toHaveCount(1)
  await expect(device(page, 5)).toContainText('Scanned from here')
  // No choice of node to offer.
  await expect(page.getByRole('combobox', { name: 'Scan from' })).toHaveCount(0)
  // One server: its ports are open without asking.
  await expect(page.getByTestId('net-server')).toHaveCount(1)
  await expect(page.getByTestId('net-port').first()).toBeVisible()
})

test('the view is one of three, and the choice is kept', async ({ page }) => {
  await mock(page, { view: 'board' })
  await page.goto('/cluster/running')
  const views = page.getByRole('group', { name: 'View' })
  await expect(views.getByRole('button')).toHaveText(['Board', 'Canvas', 'Network'])
  await expect(views.getByRole('button', { name: 'Board' })).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByTestId('net-view')).toHaveCount(0)
  await views.getByRole('button', { name: 'Network' }).click()
  await expect(views.getByRole('button', { name: 'Network' })).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByTestId('net-device')).toHaveCount(28)
  await page.reload()
  await expect(page.getByTestId('net-device')).toHaveCount(28)
})

test('on a phone the diagram is one column that fits the screen', async ({ page }) => {
  await page.setViewportSize({ width: 360, height: 800 })
  await mock(page)
  await page.goto('/cluster/running')
  await expect(device(page, 42)).toBeVisible()
  const main = page.getByRole('main')
  expect(await main.evaluate((el) => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(0)
  const lefts = await page.locator('[data-testid="net-device"][data-kind="device"]').evaluateAll((els) => els.map((el) => Math.round(el.getBoundingClientRect().left)))
  expect(new Set(lefts).size).toBe(1)
  // Nothing is cut: every card ends inside the screen.
  const rights = await page.getByTestId('net-device').evaluateAll((els) => els.map((el) => Math.round(el.getBoundingClientRect().right)))
  expect(Math.max(...rights)).toBeLessThanOrEqual(360)
})

test('answers that arrive late and out of order end in the same diagram', async ({ page }) => {
  await mock(page, { slow: true })
  await page.goto('/cluster/running')
  await expect(page.getByTestId('net-device')).toHaveCount(28, { timeout: 30_000 })
  // The nodes are known for nodes only once their interfaces and the host's
  // guests have been read, whichever comes last.
  await expect(page.locator('[data-testid="net-device"][data-kind="server"]')).toHaveCount(6, { timeout: 30_000 })
  await expect(page.getByTestId('net-host').getByTestId('net-device')).toHaveCount(13, { timeout: 30_000 })
  await expect(page.getByText('Servers 6 · VMs and LXC containers 8', { exact: true })).toBeVisible()
})

test('a LAN whose servers go out two ways is one network, with both gateways on top', async ({ page }) => {
  // Six servers of one LAN, one of them going out through a firewall that is a
  // guest of the Proxmox host. Listed by network and gateway they were two
  // networks until the first scan.
  await mock(page, { twoGateways: true, unscanned: true })
  await page.goto('/cluster/running')
  await expect(page.locator('[data-testid="net-network"]')).toHaveCount(1)
  await expect(page.locator('[data-testid="net-network"]')).toContainText('alpha-node, delta-node, bravo-agent, echo-agent, foxtrot-agent, pve-host')
})

test('every device a server goes out through is a gateway, and says whose', async ({ page }) => {
  await mock(page, { twoGateways: true })
  await page.goto('/cluster/running')
  await expect(page.getByTestId('net-device')).toHaveCount(28)
  const top = page.locator('.nm-gateway').getByTestId('net-device')
  await expect(top).toHaveCount(2)
  await expect(top.nth(0)).toHaveAttribute('data-ip', ip(1))
  await expect(top.nth(1)).toHaveAttribute('data-ip', ip(17))
  await expect(device(page, 17)).toHaveAttribute('data-kind', 'gateway')
  // A guest of the host, drawn where traffic goes through it — and saying whose guest it is.
  await expect(device(page, 17).locator('summary')).toContainText('Gateway · VM 107 on pve-host')
  await expect(page.getByTestId('net-host').getByTestId('net-device')).toHaveCount(12)
  await device(page, 17).locator('summary').click()
  await expect(device(page, 17).getByText('Default route of', { exact: true })).toBeVisible()
  await expect(device(page, 17).locator('dd').last()).toHaveText('foxtrot-agent')
  await device(page, 1).locator('summary').click()
  await expect(device(page, 1).locator('dd').last()).toHaveText('alpha-node, delta-node, bravo-agent, echo-agent, pve-host')
  // Side by side on a wide screen, above everything that hangs from them.
  const a = await device(page, 1).boundingBox()
  const b = await device(page, 17).boundingBox()
  const first = await device(page, 5).boundingBox()
  expect(Math.abs(a!.y - b!.y)).toBeLessThan(2)
  expect(b!.x).toBeGreaterThan(a!.x + a!.width)
  expect(Math.max(a!.y + a!.height, b!.y + b!.height)).toBeLessThan(first!.y)
})

test('an address two devices use is said on top of the network and on the device', async ({ page }) => {
  // What answered at a server's own address has a MAC that is not the
  // server's: another device took the address. On the LAN this was first run
  // on, a smart plug held a server's address, and the page drew it as one
  // more device with nothing said.
  await mock(page, { conflict: true })
  await page.goto('/cluster/running')
  await expect(page.getByTestId('net-device')).toHaveCount(28)
  const alert = page.getByTestId('net-conflict')
  await expect(alert).toHaveCount(1)
  await expect(alert).toHaveRole('alert')
  await expect(alert).toContainText(`Address conflict at ${ip(12)}`)
  await expect(alert).toContainText(`It is server bravo-agent's address, and in the scan another device (Example Electronics, ${mac(77)}) answered on it.`)
  await expect(alert).toContainText("Reserve the address for the server in the router's DHCP")

  const impostor = device(page, 12)
  await expect(impostor).toHaveAttribute('data-conflict', 'true')
  // Not drawn as the server, and not inside the host the server is a guest of.
  await expect(impostor).toHaveAttribute('data-kind', 'device')
  await expect(impostor).toContainText("Address conflict: this address is server bravo-agent's.")
  await expect(page.getByTestId('net-host').locator(`[data-ip="${ip(12)}"]`)).toHaveCount(0)
  await expect(page.getByText('Servers 5 · VMs and LXC containers 8', { exact: true })).toBeVisible()
  await expect(page.getByText('Other devices 14', { exact: true })).toBeVisible()
  // The others carry no such mark.
  await expect(page.locator('[data-testid="net-device"][data-conflict="true"]')).toHaveCount(1)

  // A search for something else does not take the warning away.
  await page.getByRole('searchbox').fill('printer')
  await expect(page.getByTestId('net-device')).toHaveCount(2)
  await expect(alert).toBeVisible()
})

test('no address conflict is said of a network where every server answers for itself', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/running')
  await expect(page.locator('[data-testid="net-device"][data-kind="server"]')).toHaveCount(6)
  await expect(page.getByTestId('net-conflict')).toHaveCount(0)
  await expect(page.locator('[data-testid="net-device"][data-conflict="true"]')).toHaveCount(0)
})
