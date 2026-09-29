import { test, expect, type Page } from '@playwright/test'

// In cluster mode Settings has two halves — cluster-wide (account: password,
// 2FA) and this node (updates, backups, alerts, audit). A phone reaches
// Settings only through the More drawer, which opens the node half, so the
// password form was unreachable from a phone. The page now switches between
// the halves itself. Mocked API, phone viewport.
test.use({ viewport: { width: 412, height: 860 }, hasTouch: true })

const node = { id: 'node-a', name: 'node-a', role: 'voter', status: 'online', api_address: '192.168.1.x:3628', grpc_address: '192.168.1.x:3629', joined_at: '2026-01-01T00:00:00Z', last_seen: '2026-01-01T00:00:00Z' }

async function mock(page: Page, cluster: boolean) {
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname
    let data: unknown = {}
    if (path.endsWith('/auth/setup-status')) data = { setup_required: false }
    else if (path.endsWith('/cluster/status')) data = cluster
      ? { enabled: true, name: 'lab', node_count: 2, leader_id: 'node-a', local_id: 'node-a', is_leader: true }
      : { enabled: false }
    else if (path.endsWith('/cluster/nodes')) data = { nodes: [node] }
    else if (path.endsWith('/cluster/overview')) data = { name: 'lab', node_count: 1, leader_id: 'node-a', nodes: [node], metrics: [] }
    else if (path.endsWith('/cluster/events')) data = { events: [] }
    // The node half's System tab renders the tuning panel, which needs its shape.
    else if (path.endsWith('/system/tuning')) data = { categories: [], total_params: 0, applied: 0, pending_rollback: false, rollback_remaining: 0, system_info: { cpu_cores: 2, total_ram: 2147483648, kernel: '6.8' } }
    // Enough for the terminal page to render, which is where the phone starts.
    else if (path.endsWith('/ai/sessions') || path.endsWith('/terminal/sessions')) data = []
    else if (path.endsWith('/ai/tools')) data = { tmux: { installed: true, supported: true, version: '3.4', min_version: '3.2' }, systemd_run: true, accounts: ['tester'], panel_account: 'tester', account: 'tester', tools: {} }
    await route.fulfill({ json: { success: true, data } })
  })
}

test('a phone in cluster mode reaches the password form from the More menu', async ({ page }) => {
  await mock(page, true)
  await page.goto('/terminal')
  await page.getByRole('button', { name: 'More' }).tap()
  await page.getByRole('button', { name: 'Settings' }).or(page.getByRole('link', { name: 'Settings' })).first().tap()
  await expect(page).toHaveURL(/\/settings\?scope=node/)

  const scope = page.getByRole('navigation', { name: 'Settings scope' })
  await expect(scope.getByRole('link', { name: /This node/ })).toHaveAttribute('aria-current', 'page')
  await scope.getByRole('link', { name: /Cluster-wide/ }).tap()
  await expect(page).toHaveURL(/\/settings$/)
  await expect(page.getByRole('heading', { name: 'Change Password' })).toBeVisible()

  await scope.getByRole('link', { name: /This node/ }).tap()
  await expect(page).toHaveURL(/scope=node/)
  await expect(page.getByRole('tab', { name: 'System' })).toBeVisible()
})

test('a single-node panel has no scope switch and keeps the account tab', async ({ page }) => {
  await mock(page, false)
  await page.goto('/settings')
  await expect(page.getByRole('tab', { name: 'Account' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Change Password' })).toBeVisible()
  await expect(page.getByRole('navigation', { name: 'Settings scope' })).toHaveCount(0)
})
