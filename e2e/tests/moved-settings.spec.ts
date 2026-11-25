import { test, expect, type Page } from '@playwright/test'

// Two per-server settings left the settings page for the screens they govern:
// the temporary shell's timeout sits in the terminal's Tools & accounts sheet,
// the upload limit behind the file manager's Upload ▾. Each is still saved on
// the server the address names, and is read only when its sheet or dialog
// opens. The settings page says where they went. Mocked API: GET /settings
// answers 45 minutes and 2048 MB, or 500 while `settingsDown`; `saved`
// records each PUT with the node it went to. In a cluster, node-a (attached)
// and agent-1, with the node list answering late as a leader does.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const owner = { uid: 1000, gid: 1000, user: 'tester', group: 'tester' }
const entry = (dir: string, name: string, extra: Record<string, unknown> = {}) => ({
  name, path: `${dir}/${name}`, isDir: false, size: 120, modTime: '2026-01-01T00:00:00Z', mode: '-rw-r--r--', kind: 'text', owner, ...extra,
})
const LISTINGS: Record<string, unknown[]> = {
  '/srv': [entry('/srv', 'data', { isDir: true, size: 4096, mode: 'drwxr-xr-x' }), entry('/srv', 'notes.txt')],
}
const base = { status: 'online', joined_at: '2026-01-01T00:00:00Z', last_seen: '2026-01-01T00:00:00Z' }
const nodes = [
  { ...base, id: 'node-a', name: 'node-a', kind: 'manager', role: 'voter', api_address: '192.0.2.1:3628', grpc_address: '192.0.2.1:3629' },
  { ...base, id: 'agent-1', name: 'agent-1', kind: 'agent', role: '', api_address: '198.51.100.1:3628', grpc_address: '198.51.100.1:3629' },
]
const installed = { installed: true, version: '1.0.0', path: '/root/.local/bin/tool', latest: '1.0.0', update_available: false, logged_in: true }

interface MockOptions {
  /** GET /settings answers 500 until the test sets `settingsDown` back to false. */
  settingsDown?: boolean
  /** A cluster of node-a (attached) and agent-1; otherwise a single server. */
  cluster?: boolean
  /** The server stored from an earlier visit. */
  current?: string
}
interface Mock {
  settingsDown: boolean
  /** The node each GET /settings asked, null for the attached server unnamed. */
  reads: (string | null)[]
  saved: { node: string | null; body: unknown }[]
}

async function mock(page: Page, opts: MockOptions = {}): Promise<Mock> {
  const m: Mock = { settingsDown: opts.settingsDown ?? false, reads: [], saved: [] }
  await page.addInitScript((current) => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    if (current) localStorage.setItem('sfpanel_current_node', current)
  }, opts.current ?? '')
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname.replace('/api/v1', '')
    if (path === '/settings' && req.method() === 'GET') {
      m.reads.push(url.searchParams.get('node'))
      if (m.settingsDown) {
        await route.fulfill({ status: 500, json: { success: false, error: { code: 'DB_ERROR', message: 'Failed to read settings' } } })
        return
      }
    }
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = opts.cluster
      ? { enabled: true, name: 'lab', node_count: 2, leader_id: 'node-a', local_id: 'node-a', is_leader: true }
      : { enabled: false }
    else if (path === '/cluster/nodes') {
      await new Promise((resolve) => setTimeout(resolve, 600))
      data = { nodes, local_id: 'node-a', is_leader: true }
    }
    else if (path === '/cluster/overview') data = { name: 'lab', node_count: 2, leader_id: 'node-a', nodes, metrics: [] }
    else if (path === '/cluster/events') data = { events: [] }
    else if (path === '/settings' && req.method() === 'GET') data = { terminal_timeout: '45', max_upload_size: '2048' }
    else if (path === '/settings') {
      m.saved.push({ node: url.searchParams.get('node'), body: req.postDataJSON() })
      data = { message: 'Settings updated' }
    }
    // The terminal's own reads, so it draws instead of the error screen.
    else if (path === '/ai/sessions' || path === '/terminal/sessions') data = []
    else if (path === '/ai/tools') data = {
      tmux: { installed: true, supported: true, version: '3.4', min_version: '3.2' }, systemd_run: true,
      accounts: ['tester'], panel_account: 'tester', account: 'tester',
      tools: { claude: installed, codex: installed, gemini: installed },
    }
    else if (path === '/ai/dirs') data = { recent: [], stacks: [], home: '/root' }
    else if (path === '/ai/profiles') data = { profiles: [] }
    else if (path === '/files/trash') data = { entries: [], retentionDays: 7 }
    else if (path === '/files') data = LISTINGS[url.searchParams.get('path') ?? '/'] ?? []
    else if (path === '/system/backup/schedule') data = { schedule: { enabled: false, interval_hours: 24, retention: 7 }, files: [] }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  return m
}

test("the tools sheet holds the temporary shell's timeout, refused below zero beside its field", async ({ page }) => {
  const m = await mock(page); await page.goto('/terminal')
  await page.getByRole('button', { name: 'Tools & accounts' }).first().click()
  const field = page.getByRole('dialog', { name: 'Tools & accounts' }).getByLabel('Temporary shell timeout')
  await expect(field).toHaveValue('45')
  await field.fill('-5'); await field.press('Enter')
  await expect(field).toHaveAttribute('aria-invalid', 'true')
  await expect(field).toHaveAccessibleDescription(/Enter a whole number, 0 or more/)
  expect(m.saved).toEqual([])
  await field.fill('10'); await field.press('Enter')
  await expect.poll(() => m.saved.map((s) => s.body)).toEqual([{ settings: { terminal_timeout: '10' } }])
})

test('a value that could not be read is not offered as current', async ({ page }) => {
  const m = await mock(page, { settingsDown: true }); await page.goto('/terminal')
  await page.getByRole('button', { name: 'Tools & accounts' }).first().click()
  const sheet = page.getByRole('dialog', { name: 'Tools & accounts' })
  await expect(sheet.getByText('Could not load the current settings')).toBeVisible()
  await expect(sheet.getByLabel('Temporary shell timeout')).toBeDisabled()
  m.settingsDown = false; await sheet.getByRole('button', { name: 'Retry' }).click()
  await expect(sheet.getByLabel('Temporary shell timeout')).toHaveValue('45')
})

test("the file manager's upload menu holds the upload limit", async ({ page }) => {
  const m = await mock(page); await page.goto('/files?path=%2Fsrv')
  await page.getByRole('button', { name: 'Upload options' }).click()
  await page.getByRole('menuitem', { name: 'Upload limit…' }).click()
  const dialog = page.getByRole('dialog', { name: 'Upload limit' })
  const field = dialog.getByLabel('Largest file'); await expect(field).toHaveValue('2048')
  await field.fill('0'); await field.press('Enter')
  await expect(field).toHaveAccessibleDescription(/Enter a whole number, 1 or more/); expect(m.saved).toEqual([])
  await field.fill('4096'); await field.press('Enter')
  await expect.poll(() => m.saved.map((s) => s.body)).toEqual([{ settings: { max_upload_size: '4096' } }])
  // Closing it gives the keyboard back to the button that opened the menu.
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
  await expect(page.getByRole('button', { name: 'Upload options' })).toBeFocused()
})

test("on another server the limit is that server's", async ({ page }) => {
  const m = await mock(page, { cluster: true, current: 'agent-1' }); await page.goto('/files?path=%2Fsrv&node=agent-1')
  await page.getByRole('button', { name: 'Upload options' }).click(); await page.getByRole('menuitem', { name: 'Upload limit…' }).click()
  const field = page.getByRole('dialog', { name: 'Upload limit' }).getByLabel('Largest file')
  await expect(field).toHaveValue('2048'); await field.fill('512'); await field.press('Enter')
  await expect.poll(() => m.saved).toEqual([{ node: 'agent-1', body: { settings: { max_upload_size: '512' } } }])
  // The value shown was agent-1's too: every read of it asked agent-1.
  expect(new Set(m.reads)).toEqual(new Set(['agent-1']))
})

test('the old place says where they went, for one release', async ({ page }) => {
  await mock(page); await page.goto('/settings?tab=system')
  const moved = page.getByRole('region', { name: 'Settings that moved' })
  await expect(moved.getByRole('link', { name: /Performance tuning moved/ })).toHaveAttribute('href', '/tuning')
  await expect(moved.getByRole('link', { name: /terminal timeout moved/ })).toHaveAttribute('href', '/terminal')
  await expect(moved.getByRole('link', { name: /upload limit moved/ })).toHaveAttribute('href', '/files')
  await expect(page.getByLabel('Session Timeout')).toHaveCount(0); await expect(page.getByLabel('Max Upload Size')).toHaveCount(0)
})
