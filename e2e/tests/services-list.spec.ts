import { test, expect, type Page } from '@playwright/test'

// The Services list. Units were sorted by name alone, so the two that failed
// sat somewhere among a couple of hundred; the badges said "running" and
// "enabled" under filters that said "Running"; and a list read that failed
// raised an "Action failed" toast at every 15-second poll. Mocked API.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const unit = (name: string, active_state: string, sub_state: string, enabled: string) => ({
  name, description: `${name} fixture`, load_state: 'loaded', active_state, sub_state, enabled,
})
const services = [
  unit('apache2.service', 'inactive', 'dead', 'disabled'),
  unit('cron.service', 'active', 'running', 'enabled'),
  unit('zfs-mount.service', 'failed', 'failed', 'enabled'),
  unit('backup.service', 'active', 'exited', 'static'),
]

async function open(page: Page, listing: () => { status: number; json: unknown }) {
  await page.clock.install()
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '')
    if (path === '/system/services') {
      const { status, json } = listing()
      await route.fulfill({ status, json })
      return
    }
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: false }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/services')
}

const listed = { status: 200, json: { success: true, data: { services, total: services.length } } }

test('puts a failed unit first and words each state as the filters do', async ({ page }) => {
  await open(page, () => listed)
  const rows = page.getByRole('row').filter({ has: page.getByRole('cell') })
  await expect(rows.first()).toContainText('zfs-mount.service')
  await expect(rows.nth(1)).toContainText('apache2.service')

  const failed = page.getByRole('row', { name: /zfs-mount/ })
  await expect(failed.getByText('Failed', { exact: true })).toHaveAttribute('title', 'failed')
  await expect(failed.getByText('Enabled', { exact: true })).toHaveAttribute('title', 'enabled')
  await expect(page.getByRole('row', { name: /apache2/ }).getByText('Stopped', { exact: true })).toHaveAttribute('title', 'dead')
  await expect(page.getByRole('row', { name: /backup/ }).getByText('Exited', { exact: true })).toBeVisible()

  // Each filter says how many units choosing it would show.
  await expect(page.getByRole('button', { name: 'Failed 1' })).toHaveAttribute('aria-pressed', 'false')
  await expect(page.getByRole('button', { name: 'All 4' })).toHaveAttribute('aria-pressed', 'true')
  await page.getByRole('button', { name: 'Stopped 1' }).click()
  await expect(rows).toHaveCount(1)
  await expect(rows.first()).toContainText('apache2.service')
})

test('a search that finds nothing offers to clear itself', async ({ page }) => {
  await open(page, () => listed)
  await page.getByRole('textbox', { name: 'Search services' }).fill('nothing-like-this')
  await expect(page.getByRole('table').getByText('No service matches this search or filter.')).toBeVisible()
  await page.getByRole('button', { name: 'Clear search and filter' }).click()
  await expect(page.getByRole('row', { name: /cron\.service/ })).toBeVisible()
})

test('a list read that fails keeps the list under a note, without a toast', async ({ page }) => {
  let down = false
  await open(page, () => down
    ? { status: 500, json: { success: false, error: { code: 'INTERNAL_ERROR', message: 'systemctl timed out' } } }
    : listed)
  await expect(page.getByRole('row', { name: /cron\.service/ })).toBeVisible()

  down = true
  await page.clock.runFor(16_000)
  const note = page.getByRole('status').filter({ hasText: 'Could not refresh the service list. Showing the last one.' })
  await expect(note).toBeVisible()
  await expect(page.getByRole('row', { name: /cron\.service/ })).toBeVisible()
  await expect(page.getByText('Action failed')).toHaveCount(0)

  down = false
  await page.clock.runFor(16_000)
  await expect(note).toHaveCount(0)
})
