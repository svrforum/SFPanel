import { test, expect, type Page } from '@playwright/test'

// Settings → System, standalone node. A failed read of the node's settings
// left the defaults in the fields looking current (saving wrote them over the
// real values). Mocked API.
test.use({ viewport: { width: 1280, height: 900 }, locale: 'en-US' })

type Fail = { settings?: boolean }

async function mock(page: Page, fail: Fail): Promise<{ saved: unknown[] }> {
  const saved: unknown[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace('/api/v1', '')
    const refuse = () => route.fulfill({ status: 500, json: { success: false, error: { code: 'INTERNAL_ERROR', message: 'database is locked' } } })
    if (path === '/settings' && req.method() === 'GET' && fail.settings) return refuse()
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: false }
    else if (path === '/settings' && req.method() === 'GET') data = { terminal_timeout: '45', max_upload_size: '2048' }
    else if (path === '/settings') { saved.push(req.postDataJSON()); data = { message: 'ok' } }
    else if (path === '/system/backup/schedule') data = { schedule: { enabled: false, interval_hours: 24, retention: 7 }, files: [] }
    await route.fulfill({ json: { success: true, data } })
  })
  return { saved }
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
