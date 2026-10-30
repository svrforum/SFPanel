import { test, expect, type Page } from '@playwright/test'

// The panel update card under Settings → System. It took the error out of the
// view the moment it arrived, read every refusal before the stream as
// "Connection lost", spun forever on a stream that ended without a word, and
// after an update of another node reloaded as soon as this one answered.
// Mocked API: a standalone panel, and a cluster with agent-1 selected.
test.use({ viewport: { width: 1400, height: 1000 }, locale: 'en-US' })

const base = { status: 'online', grpc_address: '192.0.2.1:3629', joined_at: '2026-01-01T00:00:00Z', last_seen: new Date().toISOString() }
const nodes = [
  { ...base, id: 'm-1', name: 'm-1', kind: 'manager', role: 'voter', api_address: '192.0.2.1:3628' },
  { ...base, id: 'agent-1', name: 'agent-1', kind: 'agent', role: '', api_address: '198.51.100.1:3628' },
]

type Reply = { status?: number; json?: unknown; sse?: string }
const sse = (...events: string[]) => events.map((e) => `${e}\n\n`).join('')
const data = (o: unknown) => `data: ${JSON.stringify(o)}`

interface Mock {
  /** The /system/info?node= values seen, in order. */
  infoNodes: (string | null)[]
  /** The version /system/info answers with from now on. */
  setVersion: (v: string) => void
}

async function mock(page: Page, opts: { cluster: boolean; update: Reply }): Promise<Mock> {
  const infoNodes: (string | null)[] = []
  let version = '0.79.4'
  await page.addInitScript((cluster) => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    if (cluster) localStorage.setItem('sfpanel_current_node', 'agent-1')
  }, opts.cluster)
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace('/api/v1', '')
    if (path === '/system/update') {
      const r = opts.update
      if (r.sse !== undefined) await route.fulfill({ status: 200, headers: { 'content-type': 'text/event-stream' }, body: r.sse })
      else await route.fulfill({ status: r.status ?? 200, json: r.json })
      return
    }
    let body: unknown = {}
    if (path === '/auth/setup-status') body = { setup_required: false }
    else if (path === '/cluster/status') body = opts.cluster
      ? { enabled: true, name: 'lab', node_count: 2, leader_id: 'm-1', local_id: 'm-1', is_leader: true }
      : { enabled: false }
    else if (path === '/cluster/nodes') body = { nodes, local_id: 'm-1', is_leader: true }
    else if (path === '/cluster/overview') body = { name: 'lab', node_count: 2, leader_id: 'm-1', nodes, metrics: [] }
    else if (path === '/cluster/events') body = { events: [] }
    else if (path === '/system/info') {
      infoNodes.push(url.searchParams.get('node'))
      body = { host: { hostname: 'host-a', kernel: '6.8', uptime: 60 }, metrics: {}, version }
    } else if (path === '/system/update-check') body = { current_version: '0.79.4', latest_version: '0.80.0', update_available: true, release_notes: '' }
    else if (path === '/system/backup/schedule') body = { schedule: { enabled: false, interval_hours: 24, retention: 7 }, files: [] }
    // The same tab renders the tuning panel, which needs its shape.
    else if (path === '/system/tuning') body = { categories: [], total_params: 0, applied: 0, pending_rollback: false, rollback_remaining: 0, system_info: { cpu_cores: 2, total_ram: 2147483648, kernel: '6.8' } }
    await route.fulfill({ json: { success: true, data: body } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  return { infoNodes, setVersion: (v) => { version = v } }
}

const card = (page: Page) => page.getByTestId('panel-update')

async function startUpdate(page: Page, cluster: boolean) {
  await page.goto(cluster ? '/settings?scope=node&tab=system' : '/settings?tab=system')
  await card(page).getByRole('button', { name: 'Check for Updates' }).click()
  await card(page).getByRole('button', { name: 'Update Now' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Confirm' }).click()
}

test('shows why an update failed', async ({ page }) => {
  await mock(page, { cluster: false, update: { sse: sse(data({ step: 'downloading', message: 'Downloading v0.80.0' }), data({ step: 'error', message: 'Checksum verification failed' })) } })
  await startUpdate(page, false)
  await expect(card(page)).toContainText('Checksum verification failed')
  await expect(card(page).getByRole('button', { name: 'Update Now' })).toBeVisible()
  await expect(card(page).locator('.animate-spin')).toHaveCount(0)
})

test('words a refusal before the stream', async ({ page }) => {
  await mock(page, { cluster: true, update: { status: 409, json: { success: false, error: { code: 'UPDATE_IN_PROGRESS', message: 'Another update is already running' } } } })
  await startUpdate(page, true)
  await expect(card(page)).toContainText('An update is already running.')
  await expect(card(page)).not.toContainText('Connection lost')
})

test('gives the reason with a quorum refusal', async ({ page }) => {
  const reason = 'Refusing update: 1 of 3 voters would remain online (quorum=2).'
  await mock(page, { cluster: true, update: { status: 409, json: { success: false, error: { code: 'CLUSTER_QUORUM', message: reason } } } })
  await startUpdate(page, true)
  await expect(card(page)).toContainText('This would leave the cluster without a quorum.')
  await expect(card(page)).toContainText(reason)
  await expect(card(page)).not.toContainText('already running')
})

test('reads an up-to-date answer', async ({ page }) => {
  await mock(page, { cluster: false, update: { json: { success: true, data: { status: 'up_to_date' } } } })
  await startUpdate(page, false)
  await expect(page.getByText("You're running the latest version")).toBeVisible()
  await expect(card(page).locator('.animate-spin')).toHaveCount(0)
  await expect(card(page).getByRole('button', { name: 'Check for Updates' })).toBeVisible()
  await expect(card(page)).not.toContainText('Connection lost')
})

test('stops spinning when the relay cuts the stream', async ({ page }) => {
  await mock(page, { cluster: true, update: { sse: sse(data({ step: 'downloading', message: 'Downloading v0.80.0' }), `event: error\n${data({ message: 'upstream stream interrupted' })}`) } })
  await startUpdate(page, true)
  await expect(card(page)).toContainText('upstream stream interrupted')
  await expect(card(page).locator('.animate-spin')).toHaveCount(0)
})

test('says so when the stream ends without an answer', async ({ page }) => {
  await mock(page, { cluster: true, update: { sse: sse(data({ step: 'downloading', message: 'Downloading v0.80.0' })) } })
  await startUpdate(page, true)
  await expect(card(page)).toContainText("The update stream ended before the node said how it went. Check the node's version in a minute.")
  await expect(card(page).locator('.animate-spin')).toHaveCount(0)
})

test('waits for the selected node, not this one', async ({ page }) => {
  await page.clock.install()
  const m = await mock(page, { cluster: true, update: { sse: sse(data({ step: 'restarting', message: 'Restarting' }), data({ step: 'complete', message: 'Updated to v0.80.0. Restarting...' })) } })
  await startUpdate(page, true)
  await expect(card(page)).toContainText('Update complete!')
  await page.evaluate(() => { (window as unknown as { before: boolean }).before = true })
  const asked = m.infoNodes.length

  // This manager answers its setup probe all along; the old card reloaded on
  // that five seconds in. agent-1 still answers with the version it had.
  await page.clock.runFor(3100)
  await page.clock.runFor(2100)
  await page.clock.runFor(2100)
  await expect.poll(() => m.infoNodes.length).toBeGreaterThan(asked)
  expect(m.infoNodes.slice(asked).every((n) => n === 'agent-1')).toBe(true)
  expect(await page.evaluate(() => (window as unknown as { before?: boolean }).before)).toBe(true)

  m.setVersion('0.80.0')
  await page.clock.runFor(2100)
  await expect.poll(() => page.evaluate(() => (window as unknown as { before?: boolean }).before)).toBeUndefined()
})

test('names the node it updates', async ({ page }) => {
  await mock(page, { cluster: true, update: { sse: '' } })
  await page.goto('/settings?scope=node&tab=system')
  await expect(card(page).getByRole('heading', { name: 'Update agent-1' })).toBeVisible()
  await card(page).getByRole('button', { name: 'Check for Updates' }).click()
  await card(page).getByRole('button', { name: 'Update Now' }).click()
  await expect(page.getByRole('dialog')).toContainText('Update agent-1')
})

test('a standalone panel keeps its plain title', async ({ page }) => {
  await mock(page, { cluster: false, update: { sse: '' } })
  await page.goto('/settings?tab=system')
  await expect(card(page).getByRole('heading', { name: 'Panel Update' })).toBeVisible()
})
