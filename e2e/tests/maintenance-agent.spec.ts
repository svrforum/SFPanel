import { test, expect, type Page } from '@playwright/test'

// Backup and restore under Settings → System, with a cluster node selected.
// The screen warned every node of Raft state an agent does not hold, never
// said that a manager's backup is refused on an agent (and carries the
// cluster's JWT key there), showed that refusal in English, restored a
// scheduled backup without the cluster question and without waiting, and
// after an upload restore of another node reloaded as soon as this manager
// answered. Mocked API: a cluster of m-1 (this manager), m-2 and agent-1.
test.use({ viewport: { width: 1400, height: 1000 }, locale: 'en-US' })

const base = { status: 'online', joined_at: '2026-01-01T00:00:00Z', last_seen: new Date().toISOString() }
const nodes = [
  { ...base, id: 'm-1', name: 'm-1', kind: 'manager', role: 'voter', grpc_address: '192.0.2.1:3629', api_address: '192.0.2.1:3628' },
  { ...base, id: 'm-2', name: 'm-2', kind: 'manager', role: 'voter', grpc_address: '192.0.2.2:3629', api_address: '192.0.2.2:3628' },
  { ...base, id: 'agent-1', name: 'agent-1', kind: 'agent', role: '', grpc_address: '198.51.100.1:3629', api_address: '198.51.100.1:3628' },
]

const archive = 'sfpanel-backup-2026-10-30-0300.tar.gz'
const restored = { status: 200, json: { success: true, data: { message: 'Backup restored. Service restarting...' } } }
type Reply = { status: number; json: unknown }

interface Mock {
  /** Restore POSTs, path and ?node= (null = this manager). */
  restores: { path: string; node: string | null }[]
  /** /system/info?node= values asked after the restore. */
  infoAfter: (string | null)[]
  /** Whether the selected node answers /system/info. */
  setDown: (down: boolean) => void
}

async function mock(page: Page, selected: string, restore: Reply = restored): Promise<Mock> {
  const restores: Mock['restores'] = []
  const infoAfter: Mock['infoAfter'] = []
  let down = false
  await page.addInitScript((node) => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_current_node', node)
  }, selected)
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace('/api/v1', '')
    if (path === '/system/restore' || path === '/system/restore/file') {
      restores.push({ path, node: url.searchParams.get('node') })
      await route.fulfill({ status: restore.status, json: restore.json })
      return
    }
    if (path === '/system/info' && restores.length > 0) {
      infoAfter.push(url.searchParams.get('node'))
      if (down) {
        await route.fulfill({ status: 502, json: { success: false, error: { code: 'CLUSTER_PROXY_FAILED', message: 'node unreachable' } } })
        return
      }
    }
    let body: unknown = {}
    if (path === '/auth/setup-status') body = { setup_required: false }
    else if (path === '/cluster/status') body = { enabled: true, name: 'lab', node_count: 3, leader_id: 'm-1', local_id: 'm-1', is_leader: true }
    else if (path === '/cluster/nodes') body = { nodes, local_id: 'm-1', is_leader: true }
    else if (path === '/cluster/overview') body = { name: 'lab', node_count: 3, leader_id: 'm-1', nodes, metrics: [] }
    else if (path === '/cluster/events') body = { events: [] }
    else if (path === '/system/info') body = { host: { hostname: 'host-a', kernel: '6.8', uptime: 60 }, metrics: {}, version: '0.79.4' }
    else if (path === '/system/backup/schedule') body = {
      schedule: { enabled: true, interval_hours: 24, retention: 7 },
      files: [{ name: archive, size: 2048, mod_time: '2026-10-30T03:00:00Z' }],
    }
    // The same tab renders the tuning panel, which needs its shape.
    else if (path === '/system/tuning') body = { categories: [], total_params: 0, applied: 0, pending_rollback: false, rollback_remaining: 0, system_info: { cpu_cores: 2, total_ram: 2147483648, kernel: '6.8' } }
    await route.fulfill({ json: { success: true, data: body } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  return { restores, infoAfter, setDown: (d) => { down = d } }
}

const agentNote = "This node is an agent: only its own backups restore here. A manager's backup is refused, and it holds the cluster's JWT key — do not upload one."
const raftWarn = /desyncs replicated state/

async function openSystem(page: Page) {
  await page.goto('/settings?scope=node&tab=system')
  await expect(page.getByRole('heading', { name: 'Scheduled backups' })).toBeVisible()
}

async function upload(page: Page) {
  await page.setInputFiles('#restore-file-input', { name: 'backup.tar.gz', mimeType: 'application/gzip', buffer: Buffer.from('not really an archive') })
}

const confirmDialog = (page: Page) => page.getByRole('dialog')

test('an agent says which backups restore on it, and asks nothing about Raft', async ({ page }) => {
  const m = await mock(page, 'agent-1')
  await openSystem(page)
  await expect(page.getByText(agentNote)).toBeVisible()

  await upload(page)
  await confirmDialog(page).getByRole('button', { name: 'Confirm' }).click()
  // One question, then the upload: no Raft warning in between.
  await expect.poll(() => m.restores.length).toBe(1)
  expect(m.restores[0]).toEqual({ path: '/system/restore', node: 'agent-1' })
  await expect(page.getByText(raftWarn)).toHaveCount(0)
})

test('a manager shows no agent note', async ({ page }) => {
  await mock(page, 'm-2')
  await openSystem(page)
  await expect(page.getByText(agentNote)).toHaveCount(0)
})

test("a manager's backup refused on an agent is worded", async ({ page }) => {
  // The role refusal answers 400, the status every other bad archive gets.
  await mock(page, 'agent-1', { status: 400, json: { success: false, error: {
    code: 'RESTORE_MANAGER_BACKUP_ON_AGENT',
    message: "Refusing to restore: this backup is a cluster manager's. It holds the cluster's admin account and JWT key, which an agent must not hold — restore it on a manager",
  } } })
  await openSystem(page)
  // The node list says agent-1 is an agent; before it, the screen asks as for a manager.
  await expect(page.getByText(agentNote)).toBeVisible()
  await upload(page)
  await confirmDialog(page).getByRole('button', { name: 'Confirm' }).click()
  await expect(page.getByText("A manager's backup holds the cluster's secrets and does not go on an agent.")).toBeVisible()
  await expect(page.getByText(/Refusing to restore/)).toHaveCount(0)
  // Nothing restarts after a refusal: the button comes back. It used to stay
  // on "Restoring..." until a reload.
  await expect(page.getByRole('button', { name: 'Select Backup File' })).toBeEnabled()
})

test('a scheduled backup restore asks the cluster question and waits for the node', async ({ page }) => {
  await page.clock.install()
  const m = await mock(page, 'm-2')
  await openSystem(page)
  await page.getByRole('button', { name: 'Restore', exact: true }).click()
  await confirmDialog(page).getByRole('button', { name: 'Confirm' }).click()
  // The same Raft question the upload path asks, before anything is sent.
  await expect(confirmDialog(page).getByText(raftWarn)).toBeVisible()
  expect(m.restores).toHaveLength(0)
  m.setDown(true)
  await confirmDialog(page).getByRole('button', { name: 'Confirm' }).click()
  await expect.poll(() => m.restores.length).toBe(1)
  expect(m.restores[0]).toEqual({ path: '/system/restore/file', node: 'm-2' })
  await page.evaluate(() => { (window as unknown as { before: boolean }).before = true })

  // m-2 is restarting; this manager answers its own probe all along.
  await page.clock.runFor(3100)
  await page.clock.runFor(2100)
  await page.clock.runFor(2100)
  await expect.poll(() => m.infoAfter.length).toBeGreaterThan(1)
  expect(m.infoAfter.every((n) => n === 'm-2')).toBe(true)
  expect(await page.evaluate(() => (window as unknown as { before?: boolean }).before)).toBe(true)

  m.setDown(false)
  await page.clock.runFor(2100)
  await expect.poll(() => page.evaluate(() => (window as unknown as { before?: boolean }).before)).toBeUndefined()
})

test('an upload restore waits for the selected node, not this one', async ({ page }) => {
  await page.clock.install()
  const m = await mock(page, 'm-2')
  await openSystem(page)
  await upload(page)
  await confirmDialog(page).getByRole('button', { name: 'Confirm' }).click()
  m.setDown(true)
  await confirmDialog(page).getByText(raftWarn).waitFor()
  await confirmDialog(page).getByRole('button', { name: 'Confirm' }).click()
  await expect.poll(() => m.restores.length).toBe(1)
  await page.evaluate(() => { (window as unknown as { before: boolean }).before = true })

  await page.clock.runFor(3100)
  await page.clock.runFor(2100)
  await page.clock.runFor(2100)
  await expect.poll(() => m.infoAfter.length).toBeGreaterThan(1)
  expect(await page.evaluate(() => (window as unknown as { before?: boolean }).before)).toBe(true)

  m.setDown(false)
  await page.clock.runFor(2100)
  await expect.poll(() => page.evaluate(() => (window as unknown as { before?: boolean }).before)).toBeUndefined()
})

test("a manager's backup download says it holds the cluster's JWT key", async ({ page }) => {
  await mock(page, 'm-2')
  await openSystem(page)
  await page.getByRole('button', { name: 'Download Backup' }).click()
  await expect(confirmDialog(page).getByText("A manager's backup holds the cluster's JWT key (config.yaml) and the admin account. Keep it somewhere safe.")).toBeVisible()
})
