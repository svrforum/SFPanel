import { test, expect, type Page } from '@playwright/test'

// Settings → System, standalone node. A failed read of the node's settings
// left the defaults in the fields looking current (saving wrote them over the
// real values); a failed read of the tuning status made the panel vanish
// after a toast; and a tuning category's header was a role="button" div with
// the Apply button inside it. Mocked API.
test.use({ viewport: { width: 1280, height: 900 }, locale: 'en-US' })

const category = {
  name: 'network', benefit: 'benefit_network', caution: 'caution_network', applied: 0, total: 1,
  params: [{ key: 'net.ipv4.tcp_rmem', current: '4096 131072 6291456', recommended: '4096 87380 16777216', description: 'TCP receive buffer', applied: false }],
}
const tuning = { categories: [category], total_params: 1, applied: 0, pending_rollback: false, rollback_remaining: 0, system_info: { cpu_cores: 2, total_ram: 2147483648, kernel: '6.8' } }

type Fail = { settings?: boolean; tuning?: boolean }

async function mock(page: Page, fail: Fail): Promise<{ saved: unknown[]; applied: unknown[] }> {
  const saved: unknown[] = []
  const applied: unknown[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace('/api/v1', '')
    const refuse = () => route.fulfill({ status: 500, json: { success: false, error: { code: 'INTERNAL_ERROR', message: 'database is locked' } } })
    if (path === '/settings' && req.method() === 'GET' && fail.settings) return refuse()
    if (path === '/system/tuning' && fail.tuning) return refuse()
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: false }
    else if (path === '/settings' && req.method() === 'GET') data = { terminal_timeout: '45', max_upload_size: '2048' }
    else if (path === '/settings') { saved.push(req.postDataJSON()); data = { message: 'ok' } }
    else if (path === '/system/backup/schedule') data = { schedule: { enabled: false, interval_hours: 24, retention: 7 }, files: [] }
    else if (path === '/system/tuning') data = tuning
    else if (path === '/system/tuning/apply') { applied.push(req.postDataJSON()); data = { message: 'ok', output: '', timeout: 60 } }
    await route.fulfill({ json: { success: true, data } })
  })
  return { saved, applied }
}

test('settings that could not be read are not offered as current values', async ({ page }) => {
  const fail: Fail = { settings: true }
  await mock(page, fail)
  await page.goto('/settings?tab=system')
  await expect(page.getByText('Could not load the current settings')).toBeVisible()
  const timeout = page.getByLabel('Session Timeout')
  await expect(timeout).toBeDisabled()
  await expect(page.getByLabel('Max Upload Size')).toBeDisabled()

  fail.settings = false
  await page.getByRole('button', { name: 'Retry' }).first().click()
  await expect(timeout).toBeEnabled()
  await expect(timeout).toHaveValue('45')
  await expect(page.getByLabel('Max Upload Size')).toHaveValue('2048')
})

test('a timeout below zero is refused beside its field', async ({ page }) => {
  const m = await mock(page, {})
  await page.goto('/settings?tab=system')
  const timeout = page.getByLabel('Session Timeout')
  await expect(timeout).toHaveValue('45')
  await timeout.fill('-5')
  await timeout.press('Enter')
  await expect(timeout).toHaveAttribute('aria-invalid', 'true')
  await expect(timeout).toHaveAccessibleDescription(/Please enter a valid number \(0 or greater\)/)
  expect(m.saved).toEqual([])
})

test('a tuning status that could not be read says so, with a retry', async ({ page }) => {
  const fail: Fail = { tuning: true }
  await mock(page, fail)
  await page.goto('/settings?tab=system')
  await expect(page.getByText('Failed to load tuning status').last()).toBeVisible()
  fail.tuning = false
  await page.getByRole('button', { name: 'Retry' }).click()
  await expect(page.getByRole('button', { name: 'Network', exact: true })).toBeVisible()
})

test('a tuning category opens from a real button, and Apply stands beside it', async ({ page }) => {
  const m = await mock(page, {})
  await page.goto('/settings?tab=system')
  const toggle = page.getByRole('button', { name: 'Network', exact: true })
  await expect(toggle).toHaveAttribute('aria-expanded', 'false')
  // Apply is its own button, not a child of the toggle.
  const apply = page.getByRole('button', { name: 'Apply', exact: true })
  expect(await apply.evaluate(el => el.parentElement?.closest('button') === null)).toBe(true)
  await toggle.press('Enter')
  await expect(toggle).toHaveAttribute('aria-expanded', 'true')
  await expect(page.getByText('net.ipv4.tcp_rmem', { exact: true })).toBeVisible()
  // Apply does not fold the category it applies.
  await apply.click()
  await expect.poll(() => m.applied.length).toBe(1)
  await expect(toggle).toHaveAttribute('aria-expanded', 'true')
  // The button that stops the rollback: the primary button, not white on the
  // bright success fill (2.3:1).
  const keep = page.getByRole('button', { name: 'Keep Changes' })
  await expect(keep).toBeVisible()
  await expect(keep).not.toHaveClass(/bg-success/)
})
