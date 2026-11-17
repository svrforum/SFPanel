import { test, expect, type Page } from '@playwright/test'

// Settings → Audit Log. A filter that matched nothing said "No audit logs
// recorded", offered no way back, and hid Clear Logs; the filters had no
// names; a failed read was a toast over an empty list. Mocked API.
test.use({ viewport: { width: 1280, height: 900 }, locale: 'en-US' })

const longPath = '/api/v1/docker/compose/a-project-with-a-rather-long-name/healthcheck/a-service-with-a-long-name-too'
const entry = { id: 1, username: 'admin', method: 'POST', path: longPath, status: 200, ip: '192.0.2.10', node_id: '', created_at: '2026-11-01T03:00:00Z' }

async function mock(page: Page, { fail = () => false } = {}) {
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace('/api/v1', '')
    if (path === '/audit/logs' && fail()) {
      await route.fulfill({ status: 500, json: { success: false, error: { code: 'INTERNAL_ERROR', message: 'database is locked' } } })
      return
    }
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: false }
    else if (path === '/audit/logs') data = url.searchParams.get('user') ? { logs: [], total: 0 } : { logs: [entry], total: 1 }
    await route.fulfill({ json: { success: true, data } })
  })
}

test('a filter that matches nothing says so and can be cleared', async ({ page }) => {
  await mock(page)
  await page.goto('/settings?tab=audit')
  const user = page.getByRole('textbox', { name: 'User' })
  await expect(page.getByRole('combobox', { name: 'Method' })).toBeVisible()
  await expect(page.getByRole('combobox', { name: 'Status' })).toBeVisible()
  await user.fill('nobody')
  await user.press('Enter')
  await expect(page.getByText('No entries match these filters')).toBeVisible()
  await expect(page.getByText('No audit logs recorded')).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Clear Logs' })).toBeVisible()
  await page.getByRole('button', { name: 'Clear filters' }).click()
  await expect(user).toHaveValue('')
  await expect(page.getByRole('cell', { name: 'admin' })).toBeVisible()
})

test('the whole path is shown, not cut at 300px', async ({ page }) => {
  await mock(page)
  await page.goto('/settings?tab=audit')
  const cell = page.getByRole('cell', { name: longPath.replace('/api/v1', '') })
  await expect(cell).toBeVisible()
  expect(await cell.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true)
})

test('a log that could not be read says so, with a retry', async ({ page }) => {
  let failing = true
  await mock(page, { fail: () => failing })
  await page.goto('/settings?tab=audit')
  await expect(page.getByText('Failed to load audit logs')).toBeVisible()
  await expect(page.getByText('No audit logs recorded')).toHaveCount(0)
  failing = false
  await page.getByRole('button', { name: 'Retry' }).click()
  await expect(page.getByRole('cell', { name: 'admin' })).toBeVisible()
})
