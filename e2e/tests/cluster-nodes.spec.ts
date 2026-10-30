import { test, expect, type Page } from '@playwright/test'

// The node list is one table for managers and agents. An agent's role is
// empty, so the old table showed a blank in the role column for every agent,
// and with forty of them nothing narrowed the list down. Mocked API: three
// managers (one an older record with no kind) and forty agents.
test.use({ viewport: { width: 1400, height: 1000 }, locale: 'en-US' })

const base = { status: 'online', grpc_address: '192.0.2.1:3629', joined_at: '2026-01-01T00:00:00Z', last_seen: new Date().toISOString() }
const managers = [
  { ...base, id: 'm-1', name: 'm-1', kind: 'manager', role: 'voter', api_address: '192.0.2.1:3628' },
  { ...base, id: 'm-2', name: 'm-2', kind: 'manager', role: 'nonvoter', api_address: '192.0.2.2:3628' },
  // Written by an older leader: no kind, and it is a manager all the same.
  { ...base, id: 'm-3', name: 'm-3', role: 'voter', api_address: '192.0.2.3:3628' },
]
const OFFLINE = new Set([5, 17, 29])
const groupOf = (i: number) => (i % 3 === 0 ? 'db' : i % 3 === 1 ? 'web' : '')
const agents = Array.from({ length: 40 }, (_, k) => {
  const i = k + 1
  const group = groupOf(i)
  return {
    ...base,
    id: `agent-${i}`,
    name: `agent-${i}`,
    kind: 'agent',
    role: '',
    status: OFFLINE.has(i) ? 'offline' : 'online',
    api_address: `198.51.100.${i}:3628`,
    labels: group ? { group, zone: 'a' } : { zone: 'b' },
    // A node that never answered: Go's zero time.
    last_seen: OFFLINE.has(i) ? '0001-01-01T00:00:00Z' : base.last_seen,
  }
})
// Listed out of order on purpose: the page sorts.
const nodes = [...agents.slice().reverse(), managers[2], managers[1], managers[0]]
const version = (id: string) => (id === 'm-2' || id === 'agent-4' ? '0.79.3' : '0.80.0')
const metrics = nodes.map((n) => ({ node_id: n.id, cpu_percent: 1, memory_percent: 1, disk_percent: 1, container_count: 0, uptime_seconds: 1, version: version(n.id), timestamp: 0 }))

interface Call { method: string; path: string; body: unknown }

async function mock(page: Page, opts: { failNodes?: () => boolean; statusHeld?: Promise<void> } = {}) {
  const calls: Call[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    // What the page told the shell: the sidebar reloads on this event.
    const w = window as unknown as { clusterChanged: number }
    w.clusterChanged = 0
    window.addEventListener('sfpanel:cluster-changed', () => { w.clusterChanged++ })
  })
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace('/api/v1', '')
    if (req.method() !== 'GET') calls.push({ method: req.method(), path, body: req.postData() ? req.postDataJSON() : null })
    if (path === '/cluster/nodes' && opts.failNodes?.()) {
      await route.fulfill({ status: 503, json: { success: false, error: { code: 'INTERNAL_ERROR', message: 'leader unreachable' } } })
      return
    }
    if (path === '/cluster/status') await opts.statusHeld
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: true, name: 'lab', node_count: nodes.length, leader_id: 'm-1', local_id: 'm-1', is_leader: true }
    else if (path === '/cluster/nodes') data = { nodes, local_id: 'm-1', is_leader: true }
    else if (path === '/cluster/overview') data = { name: 'lab', node_count: nodes.length, leader_id: 'm-1', nodes, metrics, latest_version: '0.80.0' }
    else if (path === '/cluster/events') data = { events: [] }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  return calls
}

const table = (page: Page) => page.getByRole('table')
const bodyRows = (page: Page) => table(page).locator('tbody tr')
const row = (page: Page, name: string) => bodyRows(page).filter({ has: page.getByText(name, { exact: true }) })
const names = (page: Page) => bodyRows(page).locator('td:first-child [data-node-name]').allInnerTexts()

/** The items a row's menu offers, read with the menu open and closed again. */
async function menuOf(page: Page, name: string) {
  await row(page, name).getByRole('button', { name: 'Node actions' }).click()
  const menu = page.getByRole('menu')
  await expect(menu).toBeVisible()
  const items = (await menu.getByRole('menuitem').allInnerTexts()).map((s) => s.trim())
  await page.keyboard.press('Escape')
  await expect(menu).toHaveCount(0)
  return items
}

async function choose(page: Page, name: string, item: string) {
  await row(page, name).getByRole('button', { name: 'Node actions' }).click()
  await page.getByRole('menu').getByRole('menuitem', { name: item }).click()
}

test('shows managers and agents in one table, never with an empty kind', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/nodes')
  await expect(bodyRows(page)).toHaveCount(43)

  await expect(row(page, 'm-1')).toContainText('Leader')
  await expect(row(page, 'm-2')).toContainText('Manager (not voting)')
  await expect(row(page, 'm-3')).toContainText('Manager')
  await expect(row(page, 'agent-7')).toContainText('Agent')

  const kinds = await bodyRows(page).locator('td:nth-child(2)').allInnerTexts()
  expect(kinds).toHaveLength(43)
  expect(kinds.filter((k) => k.trim() === '')).toEqual([])
})

test('sorts managers first and agents by number', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/nodes')
  await expect(bodyRows(page)).toHaveCount(43)
  const order = await names(page)
  expect(order.slice(0, 3)).toEqual(['m-1', 'm-2', 'm-3'])
  expect(order.indexOf('agent-2')).toBeLessThan(order.indexOf('agent-10'))
  expect(order.at(-1)).toBe('agent-40')
})

test('filters by kind, status and group, and searches', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/nodes')
  await expect(bodyRows(page)).toHaveCount(43)

  const kind = page.getByRole('group', { name: 'Kind' })
  const status = page.getByRole('group', { name: 'Status' })

  await kind.getByRole('button', { name: 'Agents' }).click()
  await expect(bodyRows(page)).toHaveCount(40)
  await expect(page.getByText('40 of 43')).toBeVisible()
  await kind.getByRole('button', { name: 'Managers' }).click()
  await expect(bodyRows(page)).toHaveCount(3)
  await kind.getByRole('button', { name: 'All' }).click()

  await status.getByRole('button', { name: 'Offline' }).click()
  await expect(bodyRows(page)).toHaveCount(3)
  expect(await names(page)).toEqual(['agent-5', 'agent-17', 'agent-29'])
  // A node that never answered has no "last seen", not a date in year 1.
  await expect(row(page, 'agent-5').locator('td:nth-child(7)')).toHaveText('—')
  await status.getByRole('button', { name: 'All' }).click()

  await page.getByRole('combobox', { name: 'Group' }).click()
  await page.getByRole('option', { name: 'db', exact: true }).click()
  await expect(bodyRows(page)).toHaveCount(13)
  const groups = await bodyRows(page).locator('td:nth-child(5)').allInnerTexts()
  expect(new Set(groups.map((g) => g.trim()))).toEqual(new Set(['db']))
  // The group label has its own column and is not repeated among the labels.
  await expect(row(page, 'agent-3').locator('td:nth-child(6)')).not.toContainText('group=')

  await page.getByRole('combobox', { name: 'Group' }).click()
  await page.getByRole('option', { name: 'No group' }).click()
  await expect(bodyRows(page)).toHaveCount(3 + 13)

  await page.getByRole('combobox', { name: 'Group' }).click()
  await page.getByRole('option', { name: 'All' }).click()
  await page.getByRole('textbox', { name: 'Search name, address or label' }).fill('agent-37')
  await expect(bodyRows(page)).toHaveCount(1)
  await expect(page.getByText('1 of 43')).toBeVisible()

  await page.getByRole('textbox', { name: 'Search name, address or label' }).fill('nothing-like-this')
  await expect(page.getByText('No node matches these filters.')).toBeVisible()
})

test('starts from the filter in the address', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/nodes?kind=agent&status=offline')
  await expect(bodyRows(page)).toHaveCount(3)
  await expect(page.getByRole('group', { name: 'Kind' }).getByRole('button', { name: 'Agents' })).toHaveAttribute('aria-pressed', 'true')
})

test('marks a node outdated against the latest release', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/nodes')
  const outdated = row(page, 'm-2').getByText('Outdated')
  await expect(outdated).toBeVisible()
  await expect(outdated).toHaveAttribute('title', 'Latest: 0.80.0')
  await expect(row(page, 'agent-4').getByText('Outdated')).toBeVisible()
  await expect(row(page, 'm-1')).toContainText('0.80.0')
  await expect(row(page, 'm-1').getByText('Outdated')).toHaveCount(0)
  await expect(bodyRows(page).getByText('Outdated')).toHaveCount(2)
})

test('keeps the last list when a refresh fails', async ({ page }) => {
  let fail = false
  await mock(page, { failNodes: () => fail })
  await page.goto('/cluster/nodes')
  await expect(bodyRows(page)).toHaveCount(43)

  fail = true
  await page.getByRole('button', { name: 'Refresh' }).click()
  await expect(page.getByText('Could not refresh the node list. Showing the last one.')).toBeVisible()
  await expect(bodyRows(page)).toHaveCount(43)

  fail = false
  await page.getByRole('button', { name: 'Refresh' }).click()
  await expect(page.getByText('Could not refresh the node list. Showing the last one.')).toHaveCount(0)
})

test('a first load that fails says so and offers a retry, not "not enabled"', async ({ page }) => {
  let fail = true
  await mock(page, { failNodes: () => fail })
  await page.goto('/cluster/nodes')
  await expect(page.getByText('Could not load the node list.')).toBeVisible()
  await expect(page.getByText('Cluster not enabled')).toHaveCount(0)

  fail = false
  await page.getByRole('button', { name: 'Retry' }).click()
  await expect(bodyRows(page)).toHaveCount(43)
})

test('waits for the first answer before saying anything about cluster mode', async ({ page }) => {
  let release = () => {}
  const statusHeld = new Promise<void>((resolve) => { release = resolve })
  await mock(page, { statusHeld })
  await page.goto('/cluster/nodes')
  // The list's own placeholder rows, where the page used to say "not enabled".
  await expect(page.locator('main [data-slot="skeleton"]').first()).toBeVisible()
  await expect(page.getByText('Cluster not enabled')).toHaveCount(0)
  release()
  await expect(bodyRows(page)).toHaveCount(43)
})

test('a phone sees cards, not a table', async ({ page }) => {
  await page.setViewportSize({ width: 412, height: 860 })
  await mock(page)
  await page.goto('/cluster/nodes')
  const card = page.getByTestId('node-card').filter({ has: page.getByText('agent-7', { exact: true }) })
  await expect(card).toBeVisible()
  await expect(card).toContainText('Agent')
  await expect(card).toContainText('Online')
  await expect(page.getByTestId('node-card').filter({ has: page.getByText('m-1', { exact: true }) })).toContainText('Leader')
  await expect(page.getByRole('table')).toBeHidden()
})

test('offers only what a node of that kind can do', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/nodes')
  await expect(bodyRows(page)).toHaveCount(43)
  // An agent cannot lead (the server answered 404) and its address is the one
  // its certificate names: neither is offered.
  expect(await menuOf(page, 'agent-7')).toEqual(['Open node', 'Edit labels', 'Delete'])
  // Only a voting manager takes the leadership: not a non-voter.
  expect(await menuOf(page, 'm-2')).toEqual(['Open node', 'Edit labels', 'Edit address', 'Delete'])
  // An older record with no kind is a voting manager.
  expect(await menuOf(page, 'm-3')).toEqual(['Open node', 'Edit labels', 'Edit address', 'Transfer leadership', 'Delete'])
  // The leader already leads, cannot be removed, and is this node.
  expect(await menuOf(page, 'm-1')).toEqual(['Open node', 'Edit labels', 'Edit address'])
})

test('opens a node and lands on its dashboard', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/nodes')
  await choose(page, 'agent-7', 'Open node')
  await expect(page).toHaveURL(/\/dashboard$/)
  expect(await page.evaluate(() => localStorage.getItem('sfpanel_current_node'))).toBe('agent-7')
})

test('edits the group as the group label', async ({ page }) => {
  const calls = await mock(page)
  await page.goto('/cluster/nodes')
  await choose(page, 'agent-8', 'Edit labels')
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByText('Nodes with the same group are listed together.')).toBeVisible()
  await dialog.getByRole('textbox', { name: 'Group' }).fill('  web  ')
  await dialog.getByRole('button', { name: 'Save' }).click()
  await expect.poll(() => calls.find((c) => c.path === '/cluster/nodes/agent-8/labels')?.body)
    .toEqual({ labels: { zone: 'b', group: 'web' } })
  await expect.poll(() => page.evaluate(() => (window as unknown as { clusterChanged: number }).clusterChanged)).toBe(1)
})

test('a cleared group removes the label, and the list leaves it to the group field', async ({ page }) => {
  const calls = await mock(page)
  await page.goto('/cluster/nodes')
  await choose(page, 'agent-7', 'Edit labels')
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('textbox', { name: 'Group' })).toHaveValue('web')
  await expect(dialog.getByText('zone = a')).toBeVisible()
  await expect(dialog.getByText('group = web')).toHaveCount(0)
  await dialog.getByRole('textbox', { name: 'Group' }).fill('   ')
  await dialog.getByRole('button', { name: 'Save' }).click()
  await expect.poll(() => calls.find((c) => c.path === '/cluster/nodes/agent-7/labels')?.body)
    .toEqual({ labels: { zone: 'a' } })
})

test('tells how to reset an agent it removes', async ({ page }) => {
  const calls = await mock(page)
  await page.goto('/cluster/nodes')
  await choose(page, 'agent-7', 'Delete')
  const dialog = page.getByRole('dialog')
  await expect(dialog).toContainText('sudo sfpanel agent reset --yes')
  await dialog.getByRole('button', { name: 'Confirm' }).click()
  await expect.poll(() => calls.find((c) => c.method === 'DELETE')?.path).toBe('/cluster/nodes/agent-7')
  await expect.poll(() => page.evaluate(() => (window as unknown as { clusterChanged: number }).clusterChanged)).toBe(1)

  // A manager's removal reads as it always did.
  await choose(page, 'm-2', 'Delete')
  await expect(page.getByText('Remove node m-2 from the cluster?')).toBeVisible()
  await expect(page.getByText('sudo sfpanel agent reset --yes')).toHaveCount(0)
})

test('a phone opens the same menu from a card', async ({ page }) => {
  await page.setViewportSize({ width: 412, height: 860 })
  await mock(page)
  await page.goto('/cluster/nodes')
  const card = page.getByTestId('node-card').filter({ has: page.getByText('agent-7', { exact: true }) })
  await card.getByRole('button', { name: 'Node actions' }).click()
  const items = (await page.getByRole('menu').getByRole('menuitem').allInnerTexts()).map((s) => s.trim())
  expect(items).toEqual(['Open node', 'Edit labels', 'Delete'])
})

test("the overview offers an address edit for managers, not for an agent", async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/overview')
  const overviewRow = (name: string) => page.locator('main div.px-5.py-4').filter({ has: page.getByText(name, { exact: true }) })
  await expect(overviewRow('agent-7')).toBeVisible()
  await expect(overviewRow('m-2').getByRole('button', { name: 'Edit address' })).toHaveCount(1)
  await expect(overviewRow('m-3').getByRole('button', { name: 'Edit address' })).toHaveCount(1)
  await expect(overviewRow('agent-7').getByRole('button', { name: 'Edit address' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Edit address' })).toHaveCount(3)
})
