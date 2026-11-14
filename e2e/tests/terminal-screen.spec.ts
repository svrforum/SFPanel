import { test, expect, type Page } from '@playwright/test'

// How much of the window the terminal gets, on each kind of device. REST and
// WebSocket are fixtures: nothing here reaches a host shell.

const session = { id: 'abcdef123456', title: 'Screen test', tool: 'shell', run_as: 'tester', cwd: '/srv/app', state: 'shell', persistence: 'service', attached: true, created_at: '2026-01-01T00:00:00Z' }

async function openTerminal(page: Page, path = '/terminal') {
  const resizes: { cols: number; rows: number }[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const url = new URL(route.request().url())
    let data: unknown = {}
    if (url.pathname.endsWith('/auth/setup-status')) data = { setup_required: false }
    else if (url.pathname.endsWith('/auth/ws-ticket')) data = { ticket: 'fixture-ticket' }
    else if (url.pathname.endsWith('/cluster/status')) data = { enabled: false }
    else if (url.pathname.endsWith('/system/overview')) data = { version: 'test' }
    else if (url.pathname.endsWith('/ai/sessions')) data = [session]
    else if (url.pathname.endsWith('/ai/tools')) data = { tmux: { installed: true, supported: true, version: '3.4', min_version: '3.2' }, systemd_run: true, accounts: ['tester'], panel_account: 'tester', account: 'tester', tools: {} }
    else if (url.pathname.endsWith('/terminal/info')) data = { shell_user: 'tester', is_root: false, hostname: 'fixture' }
    else if (url.pathname.endsWith('/terminal/sessions')) data = { sessions: [] }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, socket => {
    socket.onMessage(message => {
      const value = typeof message === 'string' ? message : message.toString('utf8')
      if (value.startsWith('{')) {
        const m = JSON.parse(value)
        if (m.type === 'resize') resizes.push({ cols: m.cols, rows: m.rows })
      }
    })
  })
  await page.goto(path)
  const terminal = page.locator('[data-terminal-session="active"]')
  await expect(terminal).toBeVisible()
  const cols = () => terminal.evaluate(el => (el as any).__termRef.current.cols as number)
  // Settled: the fit is debounced, and the first one runs before the layout has.
  const settledCols = async () => {
    let seen = -1
    await expect.poll(async () => { const now = await cols(); const still = now === seen; seen = now; return still }, { intervals: [300] }).toBe(true)
    return seen
  }
  return { terminal, cols, settledCols, resizes }
}

const appSidebar = (page: Page) => page.locator('aside').filter({ has: page.getByRole('link', { name: 'Docker', exact: true }) })
const rail = (page: Page) => page.locator('[data-ai-workspace] > aside')
const keyBar = (page: Page) => page.locator('[data-mobile-terminal-bar]')

test.describe('a phone held sideways', () => {
  test.use({ viewport: { width: 915, height: 412 }, hasTouch: true })
  test('gets the phone layout: no sidebars, the key bar, the whole width', async ({ page }) => {
    const t = await openTerminal(page)
    await expect(keyBar(page)).toBeVisible()
    await expect(appSidebar(page)).toBeHidden()
    await expect(rail(page)).toBeHidden()
    await expect(page.getByRole('button', { name: 'Sessions' })).toBeVisible()
    // 42 before: the app's sidebar and the session rail took 512 of the 915px.
    expect(await t.settledCols()).toBeGreaterThan(95)
  })
  test('has its keys in one row, which leaves lines to type on with the keyboard up', async ({ page }) => {
    const t = await openTerminal(page)
    const groups = keyBar(page).getByRole('group')
    const [keys, history] = [await groups.nth(0).boundingBox(), await groups.nth(1).boundingBox()]
    expect(history!.y).toBeLessThan(keys!.y + keys!.height / 2)
    expect((await keyBar(page).boundingBox())!.height).toBeLessThan(52)
    // The far end of the row is reachable.
    await keyBar(page).getByRole('button', { name: 'Ctrl+C' }).scrollIntoViewIfNeeded()
    await expect(keyBar(page).getByRole('button', { name: 'Ctrl+C' })).toBeInViewport()
    // What a keyboard leaves of a 412px-high window. Two rows of keys left two lines.
    await page.setViewportSize({ width: 915, height: 190 })
    await expect.poll(() => t.terminal.evaluate(el => (el as any).__termRef.current.rows as number)).toBeGreaterThanOrEqual(5)
  })
})

test.describe('a tablet', () => {
  test.use({ viewport: { width: 820, height: 1180 }, hasTouch: true })
  test('gets the phone layout too', async ({ page }) => {
    const t = await openTerminal(page)
    await expect(keyBar(page)).toBeVisible()
    await expect(appSidebar(page)).toBeHidden()
    await expect(rail(page)).toBeHidden()
    // 30 before.
    expect(await t.settledCols()).toBeGreaterThan(85)
  })
  test('keeps the app sidebar on every other page', async ({ page }) => {
    await openTerminal(page)
    await page.getByRole('button', { name: 'Sessions' }).tap()
    await page.getByRole('dialog').getByRole('link', { name: 'Dashboard' }).tap()
    await expect(page).toHaveURL(/\/dashboard$/)
    await expect(appSidebar(page)).toBeVisible()
  })
})

test.describe('a phone', () => {
  test.use({ viewport: { width: 412, height: 860 }, hasTouch: true })
  test('leaves for the dashboard from the session drawer, not from a key', async ({ page }) => {
    await openTerminal(page)
    // It sat among the keys, one stray tap from leaving the terminal.
    await expect(keyBar(page).getByRole('button', { name: 'Dashboard' })).toHaveCount(0)
    await page.getByRole('button', { name: 'Sessions' }).tap()
    await page.getByRole('dialog').getByRole('link', { name: 'Dashboard' }).tap()
    await expect(page).toHaveURL(/\/dashboard$/)
  })
})

test.describe('a desktop', () => {
  test.use({ viewport: { width: 1440, height: 900 } })
  test('keeps both sidebars and has no key bar', async ({ page }) => {
    await openTerminal(page)
    await expect(appSidebar(page)).toBeVisible()
    await expect(rail(page)).toBeVisible()
    await expect(rail(page).getByRole('tablist')).toBeVisible()
    await expect(keyBar(page)).toHaveCount(0)
    await expect(rail(page).getByRole('link', { name: 'Dashboard' })).toHaveCount(0)
  })
  test('focus hides the app sidebar on the terminal page only, and is remembered', async ({ page }) => {
    const t = await openTerminal(page)
    const before = await t.settledCols()
    const focus = page.getByRole('button', { name: 'Focus on the terminal' })
    await expect(focus).toHaveAttribute('aria-pressed', 'false')
    await focus.click()
    await expect(appSidebar(page)).toBeHidden()
    await expect(rail(page)).toBeVisible()
    // The sidebar's 240px, at about 8px a column.
    await expect.poll(t.cols).toBeGreaterThan(before + 20)
    await page.reload()
    await expect(page.locator('[data-terminal-session="active"]')).toBeVisible()
    await expect(appSidebar(page)).toBeHidden()
    const back = page.getByRole('button', { name: 'Show the sidebar again' })
    await expect(back).toHaveAttribute('aria-pressed', 'true')
    // Elsewhere the sidebar is the only navigation there is.
    await page.goto('/dashboard')
    await expect(appSidebar(page)).toBeVisible()
    await page.goto('/terminal')
    await expect(page.locator('[data-terminal-session="active"]')).toBeVisible()
    await page.getByRole('button', { name: 'Show the sidebar again' }).click()
    await expect(appSidebar(page)).toBeVisible()
  })
  test('a narrow window is the phone layout, as before', async ({ page }) => {
    await page.setViewportSize({ width: 700, height: 800 })
    await openTerminal(page)
    await expect(keyBar(page)).toBeVisible()
    await expect(rail(page)).toBeHidden()
  })
})
