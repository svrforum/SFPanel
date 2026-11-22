import { test, expect, type Page } from '@playwright/test'

// The running page's rack view: racks the operator draws — which server,
// device or thing sits at which height — kept by every node and read as one.
// Mocked API only: a cluster of three nodes that answer, each with its own
// copy of the racks. Addresses are invented (10.20.30.0/24, Docker's example
// MAC range).
test.use({ viewport: { width: 1280, height: 900 }, locale: 'en-US' })

const NET = '10.20.30.0/24'
const ip = (n: number) => `10.20.30.${n}`
const mac = (n: number) => `02:42:ac:14:1e:${n.toString(16).padStart(2, '0')}`
const base = { role: 'voter', joined_at: '2026-01-01T00:00:00Z', grpc_address: `${ip(5)}:3629`, status: 'online', last_seen: '2026-11-22T02:00:00Z' }
const NODES = [
  { ...base, id: 'm-1', name: 'alpha-node', kind: 'manager', api_address: `${ip(5)}:3628` },
  { ...base, id: 'p-1', name: 'pve-host', kind: 'agent', role: '', api_address: `${ip(10)}:3628` },
  { ...base, id: 'm-2', name: 'delta-node', kind: 'manager', api_address: `${ip(11)}:3628` },
]
const AT: Record<string, number> = { 'm-1': 5, 'p-1': 10, 'm-2': 11 }

const nic = (name: string, n: number | null, extra: Record<string, unknown> = {}) => ({
  name, type: 'ethernet', state: 'up', mac_address: n === null ? '' : mac(n), mtu: 1500, speed: 1000, driver: '', is_default: false, kind: '',
  addresses: n === null ? [] : [{ address: ip(n), prefix: 24, family: 'ipv4' }], ...extra,
})
const INTERFACES: Record<string, unknown[]> = {
  'm-1': [nic('lo', null, { type: 'loopback' }), nic('enp2s0', 5, { is_default: true, speed: 2500 }), nic('enp3s0', null, { state: 'down', speed: -1 }), nic('docker0', null, { type: 'bridge', kind: 'bridge' })],
  'p-1': [nic('eno1', null), nic('eno2', null, { speed: 10000 }), nic('vmbr0', 10, { type: 'bridge', kind: 'bridge', is_default: true })],
  'm-2': [nic('ens18', 11, { is_default: true })],
}

const host = (n: number, extra: Record<string, unknown> = {}) => ({ ip: ip(n), mac: mac(n), vendor: '', hostname: '', self: false, timed_out: false, ports: [], ...extra })
const known = (n: number, extra: Record<string, unknown> = {}) => ({
  mac: mac(n), name: '', note: '', kind: '', labelled_at: '', acked: false, watch: false, network: NET, ip: ip(n), vendor: '', hostname: '',
  first_seen: '2026-10-22T02:13:08Z', last_seen: '2026-11-22T02:13:08Z', ...extra,
})
const LABEL = { labelled_at: '2026-11-21T00:00:00Z' }
const SCAN = {
  installed: true,
  networks: [{ cidr: NET, interface: 'enp2s0', address: ip(5), mac: mac(5), gateway: ip(1), scannable: true, reason: '' }],
  running: null, last: null,
  results: [{ network: NET, interface: 'enp2s0', address: ip(5), gateway: ip(1), ports: false, ports_at: '', started_at: '2026-11-22T02:13:00Z', finished_at: '2026-11-22T02:13:08Z',
    hosts: [host(1, { hostname: 'router.lan' }), host(5, { self: true }), host(10), host(11), host(41, { vendor: 'Example Electronics' }), host(42)] }],
  devices: [known(41, { name: 'Core switch', kind: 'switch', ...LABEL }), known(42), known(70, { name: 'Garage NAS', kind: 'nas', ...LABEL, last_seen: '2026-11-16T02:13:08Z' })],
  conflicts: [], schedules: [],
}

interface Stored { id: string; doc: Record<string, unknown>; updated_at: string }
const rack = (id: string, name: string, at: string, extra: Record<string, unknown> = {}): Stored => ({ id, updated_at: at, doc: { name, units: 8, deleted: false, items: [], ...extra } })

interface Opts {
  cluster?: boolean
  /** Each node's racks when the page opens. */
  racks?: Record<string, Stored[]>
  /** Nodes older than the racks. */
  old?: string[]
  /** Nodes that refuse every write. */
  refuses?: string[]
  view?: string
}

async function mock(page: Page, opts: Opts = {}) {
  const cluster = opts.cluster ?? true
  const state = {
    racks: Object.fromEntries(NODES.map((n) => [n.id, new Map<string, Stored>((opts.racks?.[n.id] ?? []).map((r) => [r.id, r]))])) as Record<string, Map<string, Stored>>,
    puts: [] as { node: string; id: string; body: { doc: Record<string, unknown>; updated_at: string } }[],
    reads: [] as string[],
  }
  await page.addInitScript(({ view }) => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('sfpanel_language', 'en')
    if (!localStorage.getItem('sfpanel-running-view')) localStorage.setItem('sfpanel-running-view', view)
  }, { view: opts.view ?? 'rack' })
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request()
    const url = new URL(request.url())
    const path = url.pathname.replace('/api/v1', '')
    const node = url.searchParams.get('node') ?? 'm-1'
    const fail = (status: number, code: string, message: string, details?: Record<string, string>) =>
      route.fulfill({ status, json: { success: false, error: { code, message, details } } })
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/auth/ws-ticket') data = { ticket: 'fixture-ticket' }
    else if (path === '/cluster/status') data = cluster
      ? { enabled: true, name: 'lab', node_count: NODES.length, leader_id: 'm-1', local_id: 'm-1', is_leader: true }
      : { enabled: false }
    else if (path === '/cluster/nodes') data = { nodes: NODES, local_id: 'm-1', is_leader: true }
    else if (path === '/cluster/overview') data = { name: 'lab', node_count: NODES.length, leader_id: 'm-1', nodes: NODES, metrics: [] }
    else if (path === '/cluster/events') data = { events: [] }
    else if (path === '/system/overview') data = {
      host: { hostname: 'alpha-node', os: 'linux', platform: 'debian', platform_version: '13', kernel: '6.12', uptime: 1, num_cpu: 4 },
      metrics: { cpu: 12, mem_percent: 40, disk_percent: 55 }, version: '0.93.0', metrics_history: [],
    }
    else if (path === '/system/guests') data = []
    else if (path === '/network/interfaces') data = INTERFACES[node] ?? []
    else if (path === '/network/scan') data = node === 'm-1' ? SCAN : { ...SCAN, networks: [{ ...SCAN.networks[0], address: ip(AT[node]), mac: mac(AT[node]) }], results: [], devices: [] }
    else if (path === '/network/racks') {
      if (opts.old?.includes(node)) return fail(404, 'NOT_FOUND', 'Not Found')
      state.reads.push(node)
      data = { racks: [...state.racks[node].values()].sort((a, b) => a.id.localeCompare(b.id)) }
    }
    else if (path.startsWith('/network/racks/')) {
      const id = decodeURIComponent(path.split('/')[3])
      const body = request.postDataJSON() as { doc: Record<string, unknown>; updated_at: string }
      // Counted before it is answered: a write to a node that has no such route is a write too many.
      state.puts.push({ node, id, body })
      if (opts.old?.includes(node)) return fail(404, 'NOT_FOUND', 'Not Found')
      if (opts.refuses?.includes(node)) return fail(400, 'LAN_RACK_INVALID', 'The rack is not valid', { field: 'items' })
      const was = state.racks[node].get(id)
      // A node keeps the newer of what it has and what it is sent.
      if (!was || Date.parse(body.updated_at) >= Date.parse(was.updated_at)) state.racks[node].set(id, { id, doc: body.doc, updated_at: body.updated_at })
      data = state.racks[node].get(id)
    }
    else if (path === '/docker/compose' || path === '/docker/containers') return fail(503, 'DOCKER_UNAVAILABLE', 'Docker is not available on this node')
    else if (/batch|history|events|metrics/.test(path)) data = []
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  return state
}

const racks = (page: Page) => page.getByTestId('rack')
const free = (page: Page, unit: number) => page.locator(`[data-testid="rack-free"][data-unit="${unit}"]`)
const things = (page: Page) => page.getByTestId('rack-item')
const LATER = '2026-03-02T11:00:00Z'
const EARLIER = '2026-03-02T10:00:00Z'
const FILLED = rack('main', 'Main rack', EARLIER, { items: [
  { id: 'a', u: 1, height: 2, kind: 'node', node: 'p-1' },
  { id: 'b', u: 4, height: 1, kind: 'device', mac: mac(41) },
  { id: 'c', u: 8, height: 1, kind: 'text', text: 'Patch panel' },
] })
const everywhere = (list: Stored[]) => ({ 'm-1': list, 'p-1': list, 'm-2': list })

// ---- tests ----

test('a rack is made with a name and a height, and written to every node with one time', async ({ page }) => {
  const state = await mock(page)
  await page.goto('/cluster/running')
  await expect(page.getByText('No rack yet. Make one, and put the servers and devices at the height they really sit at.')).toBeVisible()
  await page.getByRole('button', { name: 'Add rack' }).click()
  const form = page.getByTestId('rack-new')
  await expect(form.getByRole('button', { name: 'Create' })).toBeDisabled()
  await form.getByLabel('Name').fill('  Main rack ')
  await form.getByLabel('Height (U)').fill('8')
  await form.getByRole('button', { name: 'Create' }).click()

  await expect(racks(page)).toHaveCount(1)
  await expect(racks(page).getByRole('heading', { name: 'Main rack', level: 3 })).toBeVisible()
  await expect(racks(page)).toContainText('8U · 0 in it')
  // Eight free units, numbered from the top down.
  await expect(page.getByTestId('rack-free')).toHaveCount(8)
  expect(await page.getByTestId('rack-free').evaluateAll((els) => els.map((el) => el.getAttribute('data-unit')))).toEqual(['8', '7', '6', '5', '4', '3', '2', '1'])

  await expect.poll(() => state.puts.length).toBe(3)
  expect(state.puts.map((p) => p.node).sort()).toEqual(['m-1', 'm-2', 'p-1'])
  expect(new Set(state.puts.map((p) => p.id)).size).toBe(1)
  expect(state.puts[0].id).toMatch(/^[A-Za-z0-9_-]{8,64}$/)
  expect(new Set(state.puts.map((p) => JSON.stringify(p.body))).size).toBe(1)
  expect(state.puts[0].body.doc).toEqual({ name: 'Main rack', units: 8, deleted: false, items: [] })
  expect(Date.parse(state.puts[0].body.updated_at)).toBeGreaterThan(0)
  await expect(page.getByText('No rack yet.')).toHaveCount(0)
})

test('a server put on a unit shows its ports, lit where the link is up', async ({ page }) => {
  const state = await mock(page, { racks: everywhere([rack('main', 'Main rack', EARLIER)]) })
  await page.goto('/cluster/running')
  await free(page, 1).click()
  const form = page.getByTestId('rack-add-form')
  await expect(form).toContainText('Put something at U1')
  // A server is what is offered first; the three nodes are there to choose, in the board's order.
  await expect(form.getByRole('radio', { name: 'Server' })).toBeChecked()
  await expect(form.getByRole('combobox', { name: 'Server' }).getByRole('option')).toHaveText(['alpha-node', 'delta-node', 'pve-host'])
  await form.getByRole('combobox', { name: 'Server' }).selectOption('p-1')
  await form.getByRole('combobox', { name: 'Height' }).selectOption('2')
  await form.getByRole('button', { name: 'Put' }).click()

  const unit = things(page).first()
  await expect(unit).toHaveAttribute('data-kind', 'node')
  await expect(unit).toHaveAttribute('data-unit', '1')
  await expect(unit).toHaveAttribute('data-height', '2')
  await expect(unit).toContainText('pve-host')
  await expect(unit).toContainText(ip(10))
  // Its sockets, not the bridge made of them.
  await expect(unit.getByRole('img', { name: '2 of 2 ports linked' })).toBeVisible()
  await expect(page.getByTestId('rack-free')).toHaveCount(6)
  await expect(racks(page)).toContainText('8U · 1 in it')

  await expect.poll(() => state.puts.length).toBe(3)
  const items = state.puts[0].body.doc.items as Record<string, unknown>[]
  expect(items).toHaveLength(1)
  expect(items[0]).toMatchObject({ u: 1, height: 2, kind: 'node', node: 'p-1' })
  expect(items[0]).not.toHaveProperty('mac')

  // Chosen, it says each port and its link.
  await unit.click()
  const panel = page.getByTestId('rack-item-panel')
  await expect(panel).toContainText('pve-host')
  await expect(panel).toContainText('U1–U2')
  await expect(panel.getByRole('list', { name: 'Address and ports' })).toContainText('eno1 1G · linked')
  await expect(panel.getByRole('list', { name: 'Address and ports' })).toContainText('eno2 10G · linked')
  await expect(panel).not.toContainText('vmbr0')
})

test('a port whose link is down is not lit, and is counted so in words', async ({ page }) => {
  await mock(page, { racks: everywhere([rack('main', 'Main rack', EARLIER, { items: [{ id: 'a', u: 1, height: 1, kind: 'node', node: 'm-1' }] })]) })
  await page.goto('/cluster/running')
  const unit = things(page).first()
  await expect(unit.getByRole('img', { name: '1 of 2 ports linked' })).toBeVisible()
  await unit.click()
  await expect(page.getByTestId('rack-item-panel')).toContainText('enp2s0 2.5G · linked')
  await expect(page.getByTestId('rack-item-panel')).toContainText(/enp3s0\s+· down/)
})

test('a device of the registry and plain words can be put, and what is in a rack already is not offered again', async ({ page }) => {
  const state = await mock(page, { racks: everywhere([FILLED]) })
  await page.goto('/cluster/running')
  await expect(things(page)).toHaveCount(3)
  // The switch by the name it was given; the words as written.
  await expect(page.locator('[data-testid="rack-item"][data-kind="device"]')).toContainText('Core switch')
  await expect(page.locator('[data-testid="rack-item"][data-kind="text"]')).toHaveText(/Patch panel/)

  await free(page, 6).click()
  const form = page.getByTestId('rack-add-form')
  // pve-host is in the rack: two servers are left.
  await expect(form.getByRole('combobox', { name: 'Server' }).getByRole('option')).toHaveText(['alpha-node', 'delta-node'])
  await form.getByRole('radio', { name: 'Device' }).check()
  const options = form.getByRole('combobox', { name: 'Device' }).getByRole('option')
  // Not the switch, which is in the rack; not the servers, which are servers.
  await expect(options).toHaveText([`router.lan · ${ip(1)}`, `${mac(42)} · ${ip(42)}`, `Garage NAS · ${ip(70)}`])
  await form.getByRole('combobox', { name: 'Device' }).selectOption(mac(70))
  await form.getByRole('button', { name: 'Put' }).click()
  const nas = page.locator('[data-testid="rack-item"][data-unit="6"]')
  await expect(nas).toContainText('Garage NAS')
  // The last scan did not find it.
  await expect(nas).toContainText('Not seen')
  await expect.poll(() => state.puts.length).toBe(3)
  expect((state.puts[0].body.doc.items as Record<string, unknown>[]).at(-1)).toMatchObject({ u: 6, height: 1, kind: 'device', mac: mac(70) })

  await free(page, 7).click()
  await page.getByTestId('rack-add-form').getByRole('radio', { name: 'Words' }).check()
  await expect(page.getByTestId('rack-add-form').getByRole('button', { name: 'Put' })).toBeDisabled()
  await page.getByTestId('rack-add-form').getByLabel('What it says').fill('  UPS ')
  await page.getByTestId('rack-add-form').getByRole('button', { name: 'Put' }).click()
  await expect(page.locator('[data-testid="rack-item"][data-unit="7"]')).toHaveText(/UPS/)
  await expect.poll(() => state.puts.length).toBe(6)
  expect((state.puts[5].body.doc.items as Record<string, unknown>[]).at(-1)).toMatchObject({ u: 7, kind: 'text', text: 'UPS' })
})

test('a thing is no taller than the free units above it', async ({ page }) => {
  await mock(page, { racks: everywhere([FILLED]) })
  await page.goto('/cluster/running')
  // U3 is free, U4 is the switch: one unit of room.
  await free(page, 3).click()
  await expect(page.getByTestId('rack-add-form').getByRole('combobox', { name: 'Height' }).getByRole('option')).toHaveText(['1U'])
  // U5 to U7 are free under the patch panel at U8.
  await free(page, 5).click()
  await expect(page.getByTestId('rack-add-form').getByRole('combobox', { name: 'Height' }).getByRole('option')).toHaveText(['1U', '2U', '3U'])
})

test('a thing is moved a unit at a time, made taller or shorter, and taken out — never onto another', async ({ page }) => {
  const state = await mock(page, { racks: everywhere([FILLED]) })
  await page.goto('/cluster/running')
  const sw = page.locator('[data-testid="rack-item"][data-kind="device"]')
  await sw.click()
  const panel = page.getByTestId('rack-item-panel')
  await expect(panel).toContainText('U4–U4')

  await panel.getByRole('button', { name: 'Down' }).click()
  await expect(sw).toHaveAttribute('data-unit', '3')
  // Under it now is the server, at U1 and U2.
  await expect(panel.getByRole('button', { name: 'Down' })).toBeDisabled()
  await expect.poll(() => state.puts.length).toBe(3)
  expect((state.puts[0].body.doc.items as { id: string; u: number }[]).find((i) => i.id === 'b')?.u).toBe(3)

  await panel.getByRole('button', { name: 'Up' }).click()
  await expect(sw).toHaveAttribute('data-unit', '4')
  // Three units free above it, under the patch panel: it can be four tall.
  await expect(panel.getByRole('combobox', { name: 'Height' }).getByRole('option')).toHaveText(['1U', '2U', '3U', '4U'])
  await panel.getByRole('combobox', { name: 'Height' }).selectOption('4')
  await expect(sw).toHaveAttribute('data-height', '4')
  await expect(panel.getByRole('button', { name: 'Up' })).toBeDisabled()
  await expect(page.getByTestId('rack-free')).toHaveCount(1)

  await panel.getByRole('button', { name: 'Take out' }).click()
  await expect(page.getByTestId('rack-item-panel')).toHaveCount(0)
  await expect(things(page)).toHaveCount(2)
  await expect(page.getByTestId('rack-free')).toHaveCount(5)
  await expect.poll(() => (state.puts.at(-1)!.body.doc.items as unknown[]).length).toBe(2)
})

test('a rack is renamed and made taller, not shorter than what is in it, and deleted after asking', async ({ page }) => {
  const state = await mock(page, { racks: everywhere([FILLED]) })
  await page.goto('/cluster/running')
  await racks(page).getByRole('button', { name: 'Edit' }).click()
  const settings = page.getByTestId('rack-settings')
  await settings.getByLabel('Height (U)').fill('6')
  await expect(settings).toContainText('What is in it reaches U8: it cannot be shorter.')
  await expect(settings.getByRole('button', { name: 'Save' })).toBeDisabled()
  await settings.getByLabel('Height (U)').fill('12')
  await settings.getByLabel('Name').fill('Closet rack')
  await settings.getByRole('button', { name: 'Save' }).click()
  await expect(racks(page).getByRole('heading', { name: 'Closet rack' })).toBeVisible()
  await expect(racks(page)).toContainText('12U · 3 in it')
  await expect.poll(() => state.puts.length).toBe(3)
  expect(state.puts[0].body.doc).toMatchObject({ name: 'Closet rack', units: 12, deleted: false })

  await racks(page).getByRole('button', { name: 'Edit' }).click()
  await page.getByTestId('rack-settings').getByRole('button', { name: 'Delete rack' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toContainText('Delete the rack “Closet rack”?')
  await dialog.getByRole('button', { name: 'Delete rack' }).click()
  await expect(racks(page)).toHaveCount(0)
  await expect.poll(() => state.puts.length).toBe(6)
  // Kept as a mark on every node, so that none brings it back.
  expect(state.puts[5].body.doc).toEqual({ name: '', units: 12, deleted: true, items: [] })
  await page.reload()
  await expect(page.getByText('No rack yet.')).toBeVisible()
  await expect(racks(page)).toHaveCount(0)
})

test('of the copies the nodes keep the one written last is the rack, a deletion included', async ({ page }) => {
  await mock(page, { racks: {
    'm-1': [rack('a', 'Old name', EARLIER), rack('b', 'Brought back?', EARLIER), rack('c', 'Only here', EARLIER)],
    'p-1': [rack('a', 'New name', LATER, { units: 4 }), rack('b', '', LATER, { deleted: true })],
    'm-2': [],
  } })
  await page.goto('/cluster/running')
  await expect(racks(page)).toHaveCount(2)
  await expect(racks(page).getByRole('heading')).toHaveText(['New name', 'Only here'])
  await expect(racks(page).first()).toContainText('4U · 0 in it')
})

test('a change is kept when the copy it changes was written by a clock ahead of this one', async ({ page }) => {
  // Another device, its clock a year ahead, wrote the rack last. A node keeps
  // the newer of two by their times: a change stamped with this clock alone
  // would be older than what it changes, and dropped without a word.
  const AHEAD = '2099-01-01T00:00:00.000Z'
  const state = await mock(page, { racks: everywhere([rack('main', 'Main rack', AHEAD)]) })
  await page.goto('/cluster/running')
  await free(page, 1).click()
  await page.getByTestId('rack-add-form').getByRole('button', { name: 'Put' }).click()
  await expect.poll(() => state.puts.length).toBe(3)
  expect(Date.parse(state.puts[0].body.updated_at)).toBeGreaterThan(Date.parse(AHEAD))
  // Read back from the nodes, it is still there — and so is the next change.
  await expect(things(page)).toHaveCount(1)
  await page.reload()
  await expect(things(page)).toHaveCount(1)
  await things(page).click()
  await page.getByTestId('rack-item-panel').getByRole('button', { name: 'Up' }).click()
  await expect.poll(() => state.puts.length).toBe(6)
  expect(Date.parse(state.puts[5].body.updated_at)).toBeGreaterThan(Date.parse(state.puts[0].body.updated_at))
  await page.reload()
  await expect(things(page)).toHaveAttribute('data-unit', '2')
})

test('nodes older than the racks are not written to, and with none that keeps racks the page says so', async ({ page }) => {
  const state = await mock(page, { old: ['p-1'] })
  await page.goto('/cluster/running')
  await page.getByRole('button', { name: 'Add rack' }).click()
  await page.getByTestId('rack-new').getByLabel('Name').fill('Main rack')
  await page.getByTestId('rack-new').getByRole('button', { name: 'Create' }).click()
  await expect(racks(page)).toHaveCount(1)
  await expect.poll(() => state.puts.length).toBe(2)
  expect(state.puts.map((p) => p.node).sort()).toEqual(['m-1', 'm-2'])
})

test('with no node that keeps racks the page says so and offers nothing to make', async ({ page }) => {
  await mock(page, { old: ['m-1', 'p-1', 'm-2'] })
  await page.goto('/cluster/running')
  await expect(page.getByText("This server's panel version cannot keep racks. Update it to use this.")).toBeVisible()
  await expect(page.getByRole('button', { name: 'Add rack' })).toHaveCount(0)
})

test('a write every node refuses is said, and one some node takes is not', async ({ page }) => {
  await mock(page, { racks: everywhere([rack('main', 'Main rack', EARLIER)]), refuses: ['m-1', 'p-1', 'm-2'] })
  await page.goto('/cluster/running')
  await free(page, 1).click()
  await page.getByTestId('rack-add-form').getByRole('button', { name: 'Put' }).click()
  await expect(racks(page).getByRole('alert')).toContainText('Could not save. The rack could not be saved. (The rack is not valid)')
  // What was not kept is not drawn.
  await expect(things(page)).toHaveCount(0)
})

test('a write one node refuses and the others take stands', async ({ page }) => {
  const state = await mock(page, { racks: everywhere([rack('main', 'Main rack', EARLIER)]), refuses: ['m-2'] })
  await page.goto('/cluster/running')
  await free(page, 1).click()
  const read = state.reads.length
  await page.getByTestId('rack-add-form').getByRole('button', { name: 'Put' }).click()
  // Once every node has answered the write and been read again, the write is done.
  await expect.poll(() => state.puts.length).toBe(3)
  await expect.poll(() => state.reads.length).toBeGreaterThanOrEqual(read + 3)
  await page.waitForTimeout(400)
  await expect(things(page)).toHaveCount(1)
  await expect(racks(page).getByRole('alert')).toHaveCount(0)
})

test('the view is one of four, and the choice is kept', async ({ page }) => {
  await mock(page, { view: 'board', racks: everywhere([FILLED]) })
  await page.goto('/cluster/running')
  const views = page.getByRole('group', { name: 'View' })
  await expect(views.getByRole('button')).toHaveText(['Board', 'Canvas', 'Network', 'Rack'])
  await expect(page.getByTestId('rack-view')).toHaveCount(0)
  await views.getByRole('button', { name: 'Rack' }).click()
  await expect(views.getByRole('button', { name: 'Rack' })).toHaveAttribute('aria-pressed', 'true')
  await expect(things(page)).toHaveCount(3)
  await page.reload()
  await expect(things(page)).toHaveCount(3)
})

test('the rack is a list read from the top down, each thing a button that says where it is', async ({ page }) => {
  await mock(page, { racks: everywhere([FILLED]) })
  await page.goto('/cluster/running')
  const list = page.getByRole('list', { name: 'Rack Main rack, from the top down' })
  await expect(list).toBeVisible()
  // The patch panel at the top, then free units, the switch, a free unit, the server.
  const names = await list.getByRole('button').evaluateAll((els) => els.map((el) => (el.getAttribute('aria-label') ?? el.textContent ?? '').replace(/\s+/g, ' ').trim()))
  expect(names).toEqual([
    'Patch panelU8–U8', 'Put something at U7', 'Put something at U6', 'Put something at U5', 'Core switchU4–U4', 'Put something at U3',
    expect.stringMatching(/^pve-host.*U1–U2$/),
  ])
  // Each thing takes the rows of its units: the server two, the others one.
  const heights = await things(page).evaluateAll((els) => els.map((el) => Math.round(el.getBoundingClientRect().height)))
  expect(heights[2]).toBeGreaterThan(heights[0] * 1.8)
  // Units are as tall as a finger needs.
  expect(heights[0]).toBeGreaterThanOrEqual(40)
})

test('on a phone a rack is as wide as the screen, and nothing is cut', async ({ page }) => {
  await page.setViewportSize({ width: 360, height: 800 })
  await mock(page, { racks: everywhere([FILLED]) })
  await page.goto('/cluster/running')
  await expect(things(page)).toHaveCount(3)
  const main = page.getByRole('main')
  expect(await main.evaluate((el) => el.scrollWidth - el.clientWidth)).toBeLessThanOrEqual(0)
  const box = await racks(page).boundingBox()
  expect(box!.x + box!.width).toBeLessThanOrEqual(360)
  await things(page).last().click()
  await expect(page.getByTestId('rack-item-panel').getByRole('button', { name: 'Take out' })).toBeVisible()
})

test('one server with no cluster keeps its own racks', async ({ page }) => {
  const state = await mock(page, { cluster: false })
  await page.goto('/running')
  await page.getByRole('button', { name: 'Add rack' }).click()
  await page.getByTestId('rack-new').getByLabel('Name').fill('Shelf')
  await page.getByTestId('rack-new').getByRole('button', { name: 'Create' }).click()
  await expect(racks(page).getByRole('heading', { name: 'Shelf', level: 2 })).toBeVisible()
  await expect.poll(() => state.puts.length).toBe(1)
  await free(page, 1).click()
  await expect(page.getByTestId('rack-add-form').getByRole('combobox', { name: 'Server' }).getByRole('option')).toHaveCount(1)
})
