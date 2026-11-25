import { test, expect, type Page } from '@playwright/test'

// The app shell around every page. Each route used to be titled "SFPanel",
// there was no way past the menu to the view, a click on the menu left focus
// on the menu (so a keyboard or screen reader started over from the top), and
// an address no route claimed drew a blank page with no menu. Mocked API,
// desktop viewport, standalone node.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

async function mock(page: Page) {
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname
    let data: unknown = {}
    if (path.endsWith('/auth/setup-status')) data = { setup_required: false }
    else if (path.endsWith('/cluster/status')) data = { enabled: false }
    else if (path.endsWith('/system/tuning')) data = { categories: [], total_params: 0, applied: 0, pending_rollback: false, rollback_remaining: 0, system_info: { cpu_cores: 2, total_ram: 2147483648, kernel: '6.8' } }
    else if (path.endsWith('/alerts/history')) data = { items: [], total: 0 }
    else if (path.endsWith('/alerts/channels') || path.endsWith('/alerts/rules')) data = []
    else if (path.endsWith('/audit/logs')) data = { logs: [], total: 0 }
    else if (path.endsWith('/appstore/categories') || path.endsWith('/appstore/apps')) data = []
    else if (path.endsWith('/docker/compose') || path.endsWith('/docker/containers')) data = []
    await route.fulfill({ json: { success: true, data } })
  })
}

const activeId = (page: Page) => page.evaluate(() => document.activeElement?.id || document.activeElement?.tagName)

test('each page names itself in the title, and an unknown address says so inside the shell', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster')
  await expect(page).toHaveTitle('Cluster · SFPanel')
  await page.getByRole('navigation').getByRole('link', { name: 'Settings', exact: true }).click()
  await expect(page).toHaveTitle('Settings · SFPanel')

  await page.goto('/no-such-page')
  await expect(page.getByRole('heading', { name: 'Page not found' })).toBeVisible()
  await expect(page).toHaveTitle('Page not found · SFPanel')
  // The menu is still there, and so is the way back.
  await expect(page.getByRole('navigation').getByRole('link', { name: 'Settings', exact: true })).toBeVisible()
  await page.getByRole('link', { name: 'Go to the dashboard' }).click()
  await expect(page).toHaveURL(/\/dashboard$/)
})

test('the first Tab reaches a link past the menu to the view', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster')
  await expect(page.getByRole('heading', { name: 'Cluster' }).first()).toBeVisible()
  await page.keyboard.press('Tab')
  const skip = page.getByRole('link', { name: 'Skip to content' })
  await expect(skip).toBeFocused()
  await expect(skip).toBeInViewport()
  await page.keyboard.press('Enter')
  expect(await activeId(page)).toBe('main-content')
})

test('a new section takes focus to its view, but a first load and a tab inside one do not', async ({ page }) => {
  await mock(page)
  await page.goto('/settings')
  await expect(page.getByRole('heading', { name: 'Settings', level: 1 })).toBeVisible()
  // A first load leaves focus where the browser put it.
  expect(await activeId(page)).toBe('BODY')

  // A tab within the section keeps focus on the tab just pressed.
  await page.getByRole('tab', { name: 'Updates & Backups' }).click()
  await expect(page).toHaveURL(/tab=system/)
  await expect(page.getByRole('tab', { name: 'Updates & Backups' })).toBeFocused()

  // Another section: focus moves off the menu to the view.
  await page.getByRole('navigation').getByRole('link', { name: 'Cluster', exact: true }).click()
  await expect(page).toHaveURL(/\/cluster/)
  await expect.poll(() => activeId(page)).toBe('main-content')
})

// The terminal page prefixes "(n) " for sessions waiting on the operator, and
// puts back the title it found when it goes. The shell's title has to land
// before the first and after the second.
test('the terminal keeps its waiting count, and the next page does not inherit its name', async ({ page }) => {
  const waiting = (id: string) => ({ id, title: id, tool: 'claude', run_as: 'tester', cwd: '/srv/app', state: 'waiting', persistence: 'service', attached: false, created_at: '2026-01-01T00:00:00Z' })
  await mock(page)
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname
    let data: unknown
    if (path.endsWith('/auth/ws-ticket')) data = { ticket: 'fixture-ticket' }
    else if (path.endsWith('/ai/sessions')) data = [waiting('aaaaaa111111'), waiting('bbbbbb222222')]
    else if (path.endsWith('/ai/tools')) data = { tmux: { installed: true, supported: true, version: '3.4', min_version: '3.2' }, systemd_run: true, accounts: ['tester'], panel_account: 'tester', account: 'tester', tools: {} }
    else if (path.endsWith('/terminal/info')) data = { shell_user: 'tester', is_root: false, hostname: 'fixture' }
    else if (path.endsWith('/terminal/sessions')) data = { sessions: [] }
    else return route.fallback()
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/terminal')
  // Two wait; the one on screen is not counted.
  await expect(page).toHaveTitle('(1) Terminal · SFPanel')
  await page.getByRole('navigation').getByRole('link', { name: 'Settings', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Settings', level: 1 })).toBeVisible()
  await expect(page).toHaveTitle('Settings · SFPanel')
})

// Services, processes, scheduled tasks and packages are one menu entry's tabs,
// the app store is Docker's last and the running board the dashboard's second,
// each at the address it always had.
test('system groups four pages as tabs at their old addresses', async ({ page }) => {
  await mock(page)
  await page.goto('/processes')
  await expect(page.getByRole('heading', { level: 1, name: 'System' })).toBeVisible()
  // The page's own name sits under the menu's.
  await expect(page.getByRole('heading', { level: 2, name: 'Processes' })).toBeVisible()
  await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1)
  const tabs = page.getByRole('navigation', { name: 'System' }).getByRole('link')
  expect(await tabs.evaluateAll((a) => a.map((l) => l.getAttribute('href')))).toEqual(['/services', '/processes', '/cron', '/packages'])
  await expect(tabs.filter({ hasText: 'Processes' })).toHaveAttribute('aria-current', 'page')
  await expect(page).toHaveTitle('Processes · SFPanel')
  const packages = tabs.filter({ hasText: 'Packages' })
  await packages.click()
  await expect(page).toHaveURL(/\/packages$/)
  await expect(packages).toBeFocused()
})

test('Docker carries the app store as its last tab; the dashboard its running tab', async ({ page }) => {
  await mock(page)
  await page.goto('/appstore')
  await expect(page.getByRole('heading', { level: 1, name: 'Docker' })).toBeVisible()
  await expect(page.getByRole('heading', { level: 2, name: 'App Store' })).toBeVisible()
  await expect(page.getByRole('heading', { level: 1 })).toHaveCount(1)
  await expect(page.getByRole('navigation', { name: 'Docker' }).getByRole('link', { name: 'App Store' })).toHaveAttribute('aria-current', 'page')
  await page.goto('/running')
  await expect(page.getByRole('main').getByRole('link', { name: 'Running here' })).toHaveAttribute('aria-current', 'page')
  await expect(page.getByRole('heading', { level: 1, name: 'Running here' })).toBeVisible()
})

// The dashboard reads the overview the shell already asked for; the tab
// layout between them has to hand the shell's context on. Counted on a
// production build: the dev server's strict mode runs the shell's read twice.
test('the dashboard still reuses the shell\'s overview through its tab layout', async ({ page }) => {
  const asked: string[] = []
  page.on('request', (r) => { if (r.url().includes('/api/v1/system/overview')) asked.push(r.url()) })
  await mock(page)
  await page.goto('/dashboard')
  await page.waitForTimeout(1500)
  expect(asked).toHaveLength(1)
})
