import { test, expect, type Page } from '@playwright/test'

// Converting a manager to an agent and promoting an agent to a manager, from
// the node list. The server answers 202 when the change has started; the page
// follows the node list until the node is what it was asked to become — through
// a leader change (503) and the moment a converting node is not listed at all.
// Mocked API, clock under the test's control.
test.use({ viewport: { width: 1400, height: 1000 }, locale: 'en-US' })

const base = { status: 'online', joined_at: '2026-01-01T00:00:00Z', last_seen: '2026-01-01T00:00:00Z' }
const leader = { ...base, id: 'm-1', name: 'node-a', kind: 'manager', role: 'voter', api_address: '192.0.2.1:3628', grpc_address: '192.0.2.1:3629' }
const nodeB = { ...base, id: 'm-2', name: 'node-b', kind: 'manager', role: 'voter', api_address: '192.0.2.2:3628', grpc_address: '192.0.2.2:3629' }
const nodeC = { ...base, id: 'm-3', name: 'node-c', kind: 'manager', role: 'voter', api_address: '192.0.2.3:3628', grpc_address: '192.0.2.3:3629' }
const agent = { ...base, id: 'a-1', name: 'agent-01', kind: 'agent', role: '', api_address: '198.51.100.1:3628', grpc_address: '198.51.100.1:3629' }

interface Reply { status: number; body: unknown }
const accepted = (message: string, nodeId: string): Reply => ({ status: 202, body: { success: true, data: { message, node_id: nodeId } } })
const refused = (status: number, code: string, message: string): Reply => ({ status, body: { success: false, error: { code, message } } })

interface Opts {
  /** The node list, or 503 while the managers elect a leader. */
  nodes?: () => unknown[] | 503
  localId?: string
  /** What POST …/convert and …/promote answer, in order; the last one repeats. */
  replies?: Reply[]
}

interface Call { method: string; path: string }

async function mock(page: Page, opts: Opts = {}) {
  const calls: Call[] = []
  const localId = opts.localId ?? 'm-1'
  let posts = 0
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    const w = window as unknown as { clusterChanged: number }
    w.clusterChanged = 0
    window.addEventListener('sfpanel:cluster-changed', () => { w.clusterChanged++ })
  })
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace('/api/v1', '')
    if (req.method() !== 'GET') calls.push({ method: req.method(), path })
    const list = opts.nodes?.() ?? [leader, nodeB, nodeC, agent]
    if (path === '/cluster/nodes' && list === 503) {
      await route.fulfill({ status: 503, json: { success: false, error: { code: 'INTERNAL_ERROR', message: 'no leader' } } })
      return
    }
    if (req.method() === 'POST' && /^\/cluster\/nodes\/[^/]+\/(convert|promote)$/.test(path)) {
      const replies = opts.replies ?? []
      const reply = replies[Math.min(posts++, replies.length - 1)]
      await route.fulfill({ status: reply.status, json: reply.body })
      return
    }
    const nodes = list === 503 ? [] : list
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: true, name: 'lab', node_count: nodes.length, leader_id: 'm-1', local_id: localId, is_leader: localId === 'm-1' }
    else if (path === '/cluster/nodes') data = { nodes, local_id: localId, is_leader: localId === 'm-1' }
    else if (path === '/cluster/overview') data = { name: 'lab', node_count: nodes.length, leader_id: 'm-1', nodes, metrics: [] }
    else if (path === '/cluster/events') data = { events: [] }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  return calls
}

const rows = (page: Page) => page.getByRole('table').locator('tbody tr')
const row = (page: Page, name: string) => rows(page).filter({ has: page.getByText(name, { exact: true }) })
const progress = (page: Page) => page.getByTestId('role-change')

async function choose(page: Page, name: string, item: string) {
  await row(page, name).getByRole('button', { name: 'Node actions' }).click()
  await page.getByRole('menu').getByRole('menuitem', { name: item }).click()
}

async function openConvert(page: Page, name: string) {
  await page.goto('/cluster/nodes')
  await choose(page, name, 'Convert to agent')
  const dialog = page.getByRole('dialog', { name: `Convert ${name} to an agent` })
  await expect(dialog).toBeVisible()
  return dialog
}

test('converts a manager after the name is typed, and follows it to an agent', async ({ page }) => {
  await page.clock.install()
  let phase: 'manager' | 'electing' | 'gone' | 'agent' = 'manager'
  const calls = await mock(page, {
    nodes: () => {
      if (phase === 'electing') return 503
      if (phase === 'gone') return [leader, nodeC, agent]
      if (phase === 'agent') return [leader, nodeC, agent, { ...nodeB, kind: 'agent', role: '' }]
      return [leader, nodeB, nodeC, agent]
    },
    replies: [accepted('node-b is leaving the cluster and joining it again as an agent', 'm-2')],
  })
  const dialog = await openConvert(page, 'node-b')

  // What goes and what stays, before anything is typed.
  await expect(dialog).toContainText('Erased on that server')
  await expect(dialog).toContainText('Raft data and snapshots · the cluster CA key and join tokens')
  await expect(dialog).toContainText('Kept')
  await expect(dialog).toContainText('Stacks, containers and files')
  await expect(dialog).toContainText("That server's panel stops serving this interface and its sign-in.")
  // Not the manager this browser is on.
  await expect(dialog).not.toContainText('This is the manager your browser is on')
  await expect(dialog).toContainText('Type the node name to confirm')

  const go = dialog.getByRole('button', { name: 'Convert to agent' })
  await expect(go).toBeDisabled()
  await dialog.getByRole('textbox').fill('node-')
  await expect(go).toBeDisabled()
  await dialog.getByRole('textbox').fill('node-b')
  await expect(go).toBeEnabled()
  await go.click()

  await expect.poll(() => calls.filter((c) => c.method === 'POST').map((c) => c.path)).toEqual(['/cluster/nodes/m-2/convert'])
  await expect(dialog).toHaveCount(0)
  await expect(progress(page)).toContainText('Converting node-b…')

  // The managers elect a leader: the list answers 503 and the page waits on.
  phase = 'electing'
  await page.clock.runFor(3100)
  await expect(progress(page)).toContainText('Converting node-b…')

  // It has left Raft and has not registered as an agent yet: its row stays.
  phase = 'gone'
  await page.clock.runFor(3100)
  await expect(row(page, 'node-b')).toContainText('Converting node-b…')
  await expect(rows(page)).toHaveCount(4)

  phase = 'agent'
  await page.clock.runFor(3100)
  await expect(progress(page)).toContainText('node-b is an agent now. It held the cluster\'s JWT key and internal proxy secret: rotate them.')
  await expect(progress(page).getByRole('link', { name: 'Rotate secrets' })).toHaveAttribute('href', '/cluster/security')
  await expect(row(page, 'node-b')).toContainText('Agent')
  await expect(page.getByText('Converting node-b…')).toHaveCount(0)
  // The sidebar's tree is told.
  await expect.poll(() => page.evaluate(() => (window as unknown as { clusterChanged: number }).clusterChanged)).toBeGreaterThan(0)
})

test('warns that converting this manager takes this page away', async ({ page }) => {
  await mock(page, { localId: 'm-2' })
  const dialog = await openConvert(page, 'node-b')
  await expect(dialog).toContainText('This is the manager your browser is on: this page goes away when the conversion is done. Follow it from another manager, 192.0.2.1:3628.')
  await dialog.getByRole('button', { name: 'Cancel' }).click()

  await choose(page, 'node-c', 'Convert to agent')
  const other = page.getByRole('dialog', { name: 'Convert node-c to an agent' })
  await expect(other).toContainText('Erased on that server')
  await expect(other).not.toContainText('This is the manager your browser is on')
})

test('shows why a conversion was refused', async ({ page }) => {
  const calls = await mock(page, {
    replies: [
      refused(409, 'CLUSTER_CONVERT_REFUSED', 'a rotation is pending'),
      refused(502, 'INTERNAL_ERROR', 'node m-2 did not answer: EOF'),
    ],
  })
  const dialog = await openConvert(page, 'node-b')
  await dialog.getByRole('textbox').fill('node-b')
  await dialog.getByRole('button', { name: 'Convert to agent' }).click()
  const alert = dialog.getByRole('alert')
  await expect(alert).toContainText('The conversion was refused')
  await expect(alert).toContainText('a rotation is pending')
  // Nothing started: the dialog stays and there is no progress.
  await expect(progress(page)).toHaveCount(0)

  // The node did not answer: maybe it is leaving. Not a refusal, and not the server's words.
  await dialog.getByRole('button', { name: 'Convert to agent' }).click()
  await expect(alert).toContainText('The node did not answer. Check the node list in a minute; if it is still a manager, convert it again.')
  await expect(alert).not.toContainText('The conversion was refused')
  await expect(alert).not.toContainText('EOF')
  expect(calls.filter((c) => c.method === 'POST')).toHaveLength(2)
})

test('the leader cannot be converted', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/nodes')
  await row(page, 'node-a').getByRole('button', { name: 'Node actions' }).click()
  const menu = page.getByRole('menu')
  const item = menu.getByRole('menuitem', { name: 'Convert to agent' })
  await expect(item).toBeVisible()
  await expect(item).toBeDisabled()
  await expect(menu).toContainText('The leader cannot be converted. Transfer leadership first.')
  await page.keyboard.press('Escape')

  // Another manager's item is not held back, and carries no such hint.
  await row(page, 'node-b').getByRole('button', { name: 'Node actions' }).click()
  await expect(menu.getByRole('menuitem', { name: 'Convert to agent' })).toBeEnabled()
  await expect(menu).not.toContainText('The leader cannot be converted')
  await expect(menu.getByRole('menuitem', { name: 'Promote to manager' })).toHaveCount(0)
})

test('promotes an agent and explains a closed Raft port', async ({ page }) => {
  await page.clock.install()
  let phase: 'agent' | 'nonvoter' | 'voter' = 'agent'
  const calls = await mock(page, {
    nodes: () => {
      if (phase === 'agent') return [leader, nodeB, nodeC, agent]
      return [leader, nodeB, nodeC, { ...agent, kind: 'manager', role: phase }]
    },
    replies: [accepted('agent-01 is joining the cluster as a manager under its own ID, and restarts as one', 'a-1')],
  })
  await page.goto('/cluster/nodes')
  await choose(page, 'agent-01', 'Promote to manager')
  const dialog = page.getByRole('dialog', { name: 'Promote agent-01 to a manager' })
  await expect(dialog).toContainText("A manager receives the cluster's JWT key, CA key and admin account")
  await expect(dialog).toContainText('its cluster port + 1, 3630 by default')
  await dialog.getByRole('button', { name: 'Promote to manager' }).click()
  await expect.poll(() => calls.filter((c) => c.method === 'POST').map((c) => c.path)).toEqual(['/cluster/nodes/a-1/promote'])
  await expect(dialog).toHaveCount(0)
  await expect(progress(page)).toContainText('Promoting agent-01…')

  // A manager, but the leader cannot reach its Raft port: it never votes.
  phase = 'nonvoter'
  await page.clock.runFor(3100)
  await expect(row(page, 'agent-01')).toContainText('Manager (not voting)')
  await expect(progress(page)).toContainText('Promoting agent-01…')
  await expect(progress(page)).not.toContainText('not voting yet')
  await page.clock.fastForward(2 * 60_000)
  await expect(progress(page)).toContainText('agent-01 is a manager but not voting yet. Check that the leader can reach its Raft port.')
  // Waiting on the port is not a timeout.
  await expect(progress(page)).not.toContainText('Not done yet')

  phase = 'voter'
  await page.clock.runFor(3100)
  await expect(progress(page)).toContainText('agent-01 is a voting manager now.')
  await expect(progress(page)).not.toContainText('Raft port')
})

test('says so when a conversion is not done in time', async ({ page }) => {
  await page.clock.install()
  await mock(page, { replies: [accepted('node-b is leaving the cluster and joining it again as an agent', 'm-2')] })
  const dialog = await openConvert(page, 'node-b')
  await dialog.getByRole('textbox').fill('node-b')
  await dialog.getByRole('button', { name: 'Convert to agent' }).click()
  await expect(progress(page)).toContainText('Converting node-b…')
  await page.clock.runFor(3100)
  await expect(progress(page)).not.toContainText('Not done yet')

  await page.clock.fastForward(3 * 60_000)
  await expect(progress(page)).toContainText('Not done yet. Check the node list; if it is still a manager, convert it again.')
})
