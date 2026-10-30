import { test, expect, type Page, type WebSocketRoute } from '@playwright/test'

// The cluster overview with managers and agents. It counted "nodes" as one
// number, said nothing of agents that went offline or of nodes behind the
// latest release, had no wave size, dropped the update's wave, target and the
// reason it stopped, offered to force any refused leave, and showed the
// disband's unreached agents (and a leave no leader took in) in a toast that
// the reload swept away.
// Mocked API: two managers, four agents (agent-03 offline).
test.use({ viewport: { width: 1400, height: 1000 }, locale: 'en-US' })

const base = { status: 'online', joined_at: '2026-01-01T00:00:00Z', last_seen: '2026-01-01T00:00:00Z' }
const managers = [
  { ...base, id: 'm-1', name: 'node-a', kind: 'manager', role: 'voter', api_address: '192.0.2.1:3628', grpc_address: '192.0.2.1:3629' },
  { ...base, id: 'm-2', name: 'node-b', kind: 'manager', role: 'voter', api_address: '192.0.2.2:3628', grpc_address: '192.0.2.2:3629' },
]
const agents = [1, 2, 3, 4].map((i) => ({
  ...base,
  id: `a-${i}`,
  name: `agent-0${i}`,
  kind: 'agent',
  role: '',
  status: i === 3 ? 'offline' : 'online',
  api_address: `198.51.100.${i}:3628`,
  grpc_address: `198.51.100.${i}:3629`,
}))
const nodes = [...agents, ...managers]
// Behind the release: node-b and agent-02 online, agent-03 offline (not counted).
const VERSIONS: Record<string, string> = { 'm-2': '0.79.4', 'a-2': '0.79.4', 'a-3': '0.79.0' }
const metrics = nodes.map((n) => ({
  node_id: n.id, cpu_percent: 1, memory_percent: 1, disk_percent: 1, container_count: 0, uptime_seconds: 1,
  version: VERSIONS[n.id] ?? '0.80.0', timestamp: 0,
}))

interface Reply { status: number; body: unknown }
const fail = (status: number, code: string, message: string): Reply => ({ status, body: { success: false, error: { code, message } } })
const ok = (data: unknown): Reply => ({ status: 200, body: { success: true, data } })

interface Opts {
  stale?: boolean
  latest?: string
  /** What POST /cluster/update answers: an SSE body, or a refusal. */
  update?: string | Reply
  /** What POST /cluster/leave answers, in order; the last one repeats. */
  leave?: Reply[]
  disband?: Reply
  /** /cluster/status answers {enabled:false}: the node restarted standalone. */
  standalone?: boolean
}

interface Call { method: string; path: string; body: unknown }

async function mock(page: Page, opts: Opts = {}) {
  const calls: Call[] = []
  const probes = { count: 0 }
  let leaves = 0
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
  })
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname.replace('/api/v1', '')
    if (req.method() !== 'GET') calls.push({ method: req.method(), path: path + url.search, body: req.postData() ? req.postDataJSON() : null })
    if (path === '/auth/setup-status') probes.count++
    if (req.method() === 'POST' && path === '/cluster/update') {
      const u = opts.update ?? ''
      if (typeof u === 'string') await route.fulfill({ status: 200, headers: { 'Content-Type': 'text/event-stream' }, body: u })
      else await route.fulfill({ status: u.status, json: u.body })
      return
    }
    if (req.method() === 'POST' && path === '/cluster/leave') {
      const list = opts.leave ?? [ok({ message: 'ok' })]
      const r = list[Math.min(leaves++, list.length - 1)]
      await route.fulfill({ status: r.status, json: r.body })
      return
    }
    if (req.method() === 'POST' && path === '/cluster/disband') {
      const r = opts.disband ?? ok({ message: 'Cluster disbanded.' })
      await route.fulfill({ status: r.status, json: r.body })
      return
    }
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/auth/ws-ticket') data = { ticket: 'fixture-ticket' }
    else if (path === '/cluster/status' && opts.standalone) data = { enabled: false }
    else if (path === '/cluster/status') data = { enabled: true, name: 'lab', node_count: nodes.length, leader_id: 'm-1', local_id: 'm-1', is_leader: true, stale: !!opts.stale }
    else if (path === '/cluster/nodes') data = { nodes, local_id: 'm-1', is_leader: true }
    else if (path === '/cluster/overview') data = { name: 'lab', node_count: nodes.length, leader_id: 'm-1', nodes, metrics, latest_version: opts.latest }
    else if (path === '/cluster/events') data = { events: [] }
    await route.fulfill({ json: { success: true, data } })
  })
  const sockets: WebSocketRoute[] = []
  await page.routeWebSocket(/\/ws\//, (socket) => { sockets.push(socket) })
  return { calls, probes, sockets }
}

const sse = (...events: unknown[]) => events.map((e) => `data: ${JSON.stringify(e)}\n\n`).join('')
const dialog = (page: Page) => page.getByRole('dialog')

test('counts managers and agents, and names offline agents', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/overview')
  await expect(page.getByTestId('stat-managers')).toContainText('2/2')
  await expect(page.getByTestId('stat-agents')).toContainText('3/4')
  const band = page.getByRole('link', { name: /1 agent offline: agent-03/ })
  await expect(band).toBeVisible()
  await expect(band).toHaveAttribute('href', '/cluster/nodes?kind=agent&status=offline')
})

test('does not warn of offline agents from stale data', async ({ page }) => {
  await mock(page, { stale: true })
  await page.goto('/cluster/overview')
  await expect(page.getByTestId('stat-agents')).toContainText('3/4')
  await expect(page.getByText(/agents? offline/)).toHaveCount(0)
})

test('counts the nodes behind the latest release', async ({ page }) => {
  const { sockets } = await mock(page, { latest: '0.80.0' })
  await page.goto('/cluster/overview')
  await expect(page.getByText('2 nodes to update to 0.80.0')).toBeVisible()

  // A push replaces the overview: the release it names has to come along,
  // or the reference falls back to the newest running version (0.80.0).
  const overviewSocket = () => sockets.find((s) => s.url().includes('/ws/cluster/overview'))
  await expect.poll(overviewSocket).toBeTruthy()
  const socket = overviewSocket()!
  socket.send(JSON.stringify({ enabled: true, name: 'lab', node_count: nodes.length, leader_id: 'm-1', local_id: 'm-1', is_leader: true, nodes, metrics, events: [], latest_version: '0.80.1' }))
  // Every online node is behind 0.80.1; offline agent-03 is not counted.
  await expect(page.getByText('5 nodes to update to 0.80.1')).toBeVisible()
})

test('sends the wave size with a rolling update', async ({ page }) => {
  const { calls } = await mock(page, { update: sse({ overall: 'started', mode: 'rolling', total_nodes: 0, waves: 0, target: 'v0.80.0' }, { overall: 'complete', updated: 0, failed: 0, leader_restarting: false }) })
  await page.goto('/cluster/overview')
  await page.getByLabel('Wave size').fill('2')
  await page.getByRole('button', { name: 'Rolling Update' }).click()
  await expect(dialog(page)).toContainText('Agents go first in waves')
  await dialog(page).getByRole('button', { name: 'Confirm' }).click()
  await expect.poll(() => calls.find((c) => c.path === '/cluster/update')?.body).toEqual({ mode: 'rolling', wave_size: 2 })

  await page.getByRole('button', { name: 'Simultaneous Update' }).click()
  await expect(dialog(page)).toContainText('Every agent goes in one wave')
  await dialog(page).getByRole('button', { name: 'Confirm' }).click()
  await expect.poll(() => calls.filter((c) => c.path === '/cluster/update').at(-1)?.body).toEqual({ mode: 'simultaneous' })
})

test('shows the wave, the target and why the run stopped', async ({ page }) => {
  const reason = 'Wave 1 of 2 failed (agent-02): Update failed: timed out'
  const step = (name: string, s: string, message: string) => ({ node_id: name, node_name: name, step: s, message })
  await mock(page, {
    update: sse(
      { overall: 'started', mode: 'rolling', total_nodes: 3, waves: 2, target: 'v0.80.0' },
      step('agent-03', 'skipped', 'Node is offline'),
      { wave: 1, waves: 2, nodes: ['agent-01', 'agent-02'] },
      step('agent-01', 'updating', 'Starting update...'),
      step('agent-02', 'updating', 'Starting update...'),
      step('agent-01', 'online', 'Back on v0.80.0'),
      step('agent-02', 'error', 'Update failed: timed out'),
      { overall: 'error', message: reason, updated: 1, failed: 1 },
    ),
  })
  await page.goto('/cluster/overview')
  await page.getByRole('button', { name: 'Rolling Update' }).click()
  await dialog(page).getByRole('button', { name: 'Confirm' }).click()
  const card = page.getByTestId('update-progress')
  await expect(card).toContainText('Updating to v0.80.0')
  await expect(card).toContainText('Wave 1 of 2: agent-01, agent-02')
  // The skipped agent-03 is listed but not counted.
  await expect(card.getByTestId('update-count')).toHaveText(/^1 \/ 3/)
  const stopped = card.getByRole('alert')
  await expect(stopped).toContainText('The update stopped')
  await expect(stopped).toContainText(reason)
})

test('says an update is already running on 409', async ({ page }) => {
  const replies: Reply[] = [fail(409, 'UPDATE_IN_PROGRESS', 'update already running'), fail(409, 'INVALID_REQUEST', 'the cluster is changing its members')]
  const opts: Opts = { update: replies[0] }
  await mock(page, opts)
  await page.goto('/cluster/overview')
  await page.getByRole('button', { name: 'Rolling Update' }).click()
  await dialog(page).getByRole('button', { name: 'Confirm' }).click()
  await expect(page.getByText('A cluster update is already running.')).toBeVisible()

  // Another conflict is not an update in progress: its own words.
  opts.update = replies[1]
  await page.getByRole('button', { name: 'Rolling Update' }).click()
  await dialog(page).getByRole('button', { name: 'Confirm' }).click()
  await expect(page.getByText('the cluster is changing its members')).toBeVisible()
  await expect(page.getByText('A cluster update is already running.')).toHaveCount(1)
})

test('offers to force a leave only for a quorum refusal', async ({ page }) => {
  const why = 'Leave refused: 1 of 2 voters would remain; pass ?force=true to override'
  const opts: Opts = { leave: [fail(409, 'CLUSTER_QUORUM', why), ok({ message: 'Leaving cluster' })] }
  const { calls } = await mock(page, opts)
  await page.goto('/cluster/overview')
  await page.getByRole('button', { name: 'Leave cluster' }).click()
  await dialog(page).getByRole('button', { name: 'Confirm' }).click()
  // The second question, with the server's reason under it.
  await expect(dialog(page)).toContainText('Force leave anyway?')
  await expect(dialog(page)).toContainText(why)
  await dialog(page).getByRole('button', { name: 'Confirm' }).click()
  await expect.poll(() => calls.filter((c) => c.path.startsWith('/cluster/leave')).map((c) => c.path)).toEqual(['/cluster/leave', '/cluster/leave?force=true'])
})

test('does not offer to force a leave refused for another reason', async ({ page }) => {
  const { calls } = await mock(page, { leave: [fail(409, 'INVALID_REQUEST', 'the cluster is changing its members')] })
  await page.goto('/cluster/overview')
  await page.getByRole('button', { name: 'Leave cluster' }).click()
  await dialog(page).getByRole('button', { name: 'Confirm' }).click()
  await expect(page.getByText('the cluster is changing its members')).toBeVisible()
  await expect(dialog(page)).toHaveCount(0)
  expect(calls.filter((c) => c.path.startsWith('/cluster/leave')).map((c) => c.path)).toEqual(['/cluster/leave'])
})

test('keeps the disband warning on screen until it is read', async ({ page }) => {
  await page.clock.install()
  const { probes } = await mock(page, {
    disband: ok({
      message: "Cluster disbanded. All nodes are restarting in standalone mode... These agents were not reached and need 'sudo sfpanel agent reset --yes' on each: agent-03",
      warnings: [{ code: 'CLUSTER_AGENTS_NOT_REACHED', message: 'These agents were not reached: agent-03', details: { agents: 'agent-03' } }],
    }),
  })
  await page.goto('/cluster/overview')
  await page.getByRole('button', { name: 'Disband Cluster' }).click()
  await expect(dialog(page)).toContainText('agent')
  await dialog(page).getByRole('textbox').fill('lab')
  await dialog(page).getByRole('button', { name: 'Disband Cluster' }).click()

  const warning = dialog(page)
  await expect(warning).toContainText('The cluster is disbanded, with one thing left to do')
  await expect(warning).toContainText('agent-03')
  // Nothing waits for the restart (and reloads) while the operator reads.
  const before = probes.count
  await page.clock.runFor(10_000)
  expect(probes.count).toBe(before)
  await expect(warning).toContainText('agent-03')

  await warning.getByRole('button', { name: 'Confirm' }).click()
  await expect(dialog(page)).toHaveCount(0)
  await page.clock.runFor(5_100)
  await expect.poll(() => probes.count).toBeGreaterThan(before)
})

test('keeps a leave no leader acknowledged on screen until it is read', async ({ page }) => {
  await page.clock.install()
  const nodeID = '3f2b9c1e-6a4d-4b8e-9f0a-1c2d3e4f5a6b'
  const { probes } = await mock(page, {
    leave: [ok({
      message: 'Leaving cluster',
      warnings: [{ code: 'CLUSTER_LEAVE_UNACKNOWLEDGED', message: 'no leader acknowledged the leave', details: { node_id: nodeID } }],
    })],
  })
  await page.goto('/cluster/overview')
  await page.getByRole('button', { name: 'Leave cluster' }).click()
  await dialog(page).getByRole('button', { name: 'Confirm' }).click()

  const warning = dialog(page)
  await expect(warning).toContainText('You left the cluster, with one thing left to do')
  await expect(warning).toContainText(`sudo sfpanel cluster remove ${nodeID}`)
  // A toast would be gone in four seconds and the reload with it.
  const before = probes.count
  await page.clock.runFor(10_000)
  expect(probes.count).toBe(before)
  await expect(warning).toContainText(nodeID)

  await warning.getByRole('button', { name: 'Confirm' }).click()
  await expect(dialog(page)).toHaveCount(0)
  await page.clock.runFor(5_100)
  await expect.poll(() => probes.count).toBeGreaterThan(before)
})

test('keeps the disband warning when the page sees the node standalone', async ({ page }) => {
  const opts: Opts = {
    disband: ok({
      message: 'Cluster disbanded.',
      warnings: [{ code: 'CLUSTER_AGENTS_NOT_REACHED', message: 'These agents were not reached: agent-03', details: { agents: 'agent-03' } }],
    }),
  }
  await mock(page, opts)
  await page.goto('/cluster/overview')
  await page.getByRole('button', { name: 'Disband Cluster' }).click()
  await dialog(page).getByRole('textbox').fill('lab')
  await dialog(page).getByRole('button', { name: 'Disband Cluster' }).click()
  await expect(dialog(page)).toContainText('agent-03')

  // The operator looks at another tab; on return the page re-reads the
  // status, and the node now answers as a standalone panel.
  opts.standalone = true
  await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')))
  // The page behind the dialog has turned into the standalone form.
  await expect(page.getByText('Cluster not enabled')).toBeAttached()
  await expect(dialog(page)).toContainText('The cluster is disbanded, with one thing left to do')
  await expect(dialog(page)).toContainText('agent-03')
})
