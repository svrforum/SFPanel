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

test.describe('font size on a phone', () => {
  test.use({ viewport: { width: 412, height: 860 }, hasTouch: true })

  test('the menu steps it without closing, and says how wide the terminal is', async ({ page }) => {
    const t = await openTerminal(page)
    const before = await t.settledCols()
    await page.locator('[data-session-menu]').tap()
    const menu = page.getByRole('menu')
    const size = menu.locator('[data-font-size]')
    await expect(size).toHaveText('14')
    await expect(menu.getByRole('group', { name: 'Font size' })).toContainText(`${before} columns`)
    const smaller = menu.getByRole('menuitem', { name: 'Decrease font size' })
    await smaller.tap()
    await smaller.tap()
    await smaller.tap()
    // Three steps in one visit: the menu closed after each one before.
    await expect(size).toHaveText('11')
    await expect(menu).toBeVisible()
    const after = await t.settledCols()
    expect(after).toBeGreaterThan(before)
    await expect(menu.getByRole('group', { name: 'Font size' })).toContainText(`${after} columns`)
    // 8px, where an upright phone fits 80 columns; 10 was the floor.
    for (let i = 0; i < 6; i++) await smaller.tap()
    await expect(size).toHaveText('8')
    expect(await t.settledCols()).toBeGreaterThanOrEqual(80)
  })

  test('two fingers pinch it: a preview while they move, one resize when they lift', async ({ page }) => {
    const t = await openTerminal(page)
    await t.settledCols()
    const box = (await t.terminal.boundingBox())!
    const y = box.y + box.height / 2, mid = box.x + box.width / 2
    const client = await page.context().newCDPSession(page)
    const touch = (type: string, half?: number) => client.send('Input.dispatchTouchEvent', {
      type, touchPoints: half === undefined ? [] : [{ x: mid - half, y, id: 0 }, { x: mid + half, y, id: 1 }],
    })
    const sent = t.resizes.length
    // The page focuses a session it opens; a keyboard-less phone shows none of
    // that, so drop it to see what the gesture itself does.
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
    await touch('touchStart', 150)
    for (const half of [140, 125, 110, 100]) await touch('touchMove', half)
    // 14px × 200/300.
    await expect(page.locator('[data-font-preview]')).toContainText('9px')
    const size = () => t.terminal.evaluate(el => (el as any).__termRef.current.options.fontSize as number)
    // Nothing applied while the fingers are down: every size is a redraw.
    expect(await size()).toBe(14)
    expect(t.resizes.length).toBe(sent)
    await touch('touchEnd')
    await expect(page.locator('[data-font-preview]')).toHaveCount(0)
    await expect.poll(size).toBe(9)
    await t.settledCols()
    expect(t.resizes.length).toBe(sent + 1)
    expect(await t.terminal.evaluate(el => ((el as any).__termRef.current.element as HTMLElement).style.transform)).toBe('')
    // The setting, not this session's alone.
    expect(await page.evaluate(() => localStorage.getItem('sfpanel_terminal_fontsize'))).toBe('9')
    // The pinch is not a tap: the terminal did not take focus and raise the keyboard.
    expect(await page.evaluate(() => document.activeElement?.classList.contains('xterm-helper-textarea'))).toBe(false)
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
