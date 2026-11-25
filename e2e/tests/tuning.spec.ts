import { test, expect, type Page } from '@playwright/test'

// Performance tuning, standalone node: the fifth tab of System at /tuning. It
// used to sit untitled at the bottom of a settings tab. A failed read of the
// tuning status made the panel vanish after a toast; a category's header was a
// role="button" div with the Apply button inside it; and swappiness, set here
// and in Disks › Swap, gave no hint of the other place. Mocked API.
test.use({ viewport: { width: 1280, height: 900 }, locale: 'en-US' })

const category = {
  name: 'network', benefit: 'benefit_network', caution: 'caution_network', applied: 0, total: 1,
  params: [{ key: 'net.ipv4.tcp_rmem', current: '4096 131072 6291456', recommended: '4096 87380 16777216', description: 'TCP receive buffer', applied: false }],
}
const memory = {
  name: 'memory', benefit: 'benefit_memory', caution: 'caution_memory', applied: 0, total: 1,
  params: [{ key: 'vm.swappiness', current: '60', recommended: '10', description: 'Swap usage tendency', applied: false }],
}
const tuning = { categories: [category], total_params: 1, applied: 0, pending_rollback: false, rollback_remaining: 0, system_info: { cpu_cores: 2, total_ram: 2147483648, kernel: '6.8' } }

/** down: the tuning status cannot be read; swap: a memory category with swappiness, and swap on a zvol. */
type Opts = { down?: boolean; swap?: boolean }

async function mock(page: Page, opts: Opts): Promise<{ applied: unknown[] }> {
  const applied: unknown[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace('/api/v1', '')
    if (path === '/system/tuning' && opts.down) return route.fulfill({ status: 500, json: { success: false, error: { code: 'INTERNAL_ERROR', message: 'database is locked' } } })
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: false }
    else if (path === '/system/tuning') data = opts.swap
      ? { ...tuning, categories: [category, memory], total_params: 2, advisories: [{ id: 'swap_on_zvol', detail: '/dev/zd0' }] }
      : tuning
    else if (path === '/system/tuning/apply') { applied.push(req.postDataJSON()); data = { message: 'ok', output: '', timeout: 60 } }
    else if (path === '/swap') data = { total: 0, used: 0, free: 0, swappiness: 60, entries: [] }
    await route.fulfill({ json: { success: true, data } })
  })
  return { applied }
}

test('tuning is the fifth system tab, at its own address', async ({ page }) => {
  await mock(page, {})
  await page.goto('/tuning')
  await expect(page.getByRole('heading', { level: 2, name: 'Performance Tuning' })).toBeVisible()
  const tabs = page.getByRole('navigation', { name: 'System' }).getByRole('link')
  expect(await tabs.evaluateAll((a) => a.map((l) => l.getAttribute('href')))).toEqual(['/services', '/processes', '/cron', '/packages', '/tuning'])
  await expect(tabs.filter({ hasText: 'Performance Tuning' })).toHaveAttribute('aria-current', 'page')
  await expect(page).toHaveTitle('Performance Tuning · SFPanel')
})

test('a tuning status that could not be read says so, with a retry', async ({ page }) => {
  const opts: Opts = { down: true }
  await mock(page, opts)
  await page.goto('/tuning')
  await expect(page.getByText('Failed to load tuning status').last()).toBeVisible()
  opts.down = false
  await page.getByRole('button', { name: 'Retry' }).click()
  await expect(page.getByRole('button', { name: 'Network', exact: true })).toBeVisible()
})

test('a tuning category opens from a real button, and Apply stands beside it', async ({ page }) => {
  const m = await mock(page, {})
  await page.goto('/tuning')
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

test('swap is linked both ways', async ({ page }) => {
  await mock(page, { swap: true })
  await page.goto('/tuning')
  await expect(page.getByRole('alert').filter({ hasText: 'Swap is on a ZFS zvol' }).getByRole('link', { name: 'Open Disks › Swap' })).toHaveAttribute('href', '/disk/swap')
  await page.getByRole('button', { name: 'Memory', exact: true }).click()
  // The swappiness row has its own link, after the advisory's.
  await expect(page.getByRole('link', { name: 'Open Disks › Swap' })).toHaveCount(2)
  await page.getByRole('link', { name: 'Open Disks › Swap' }).last().click()
  await expect(page).toHaveURL(/\/disk\/swap$/)
})
