import { test, expect, type Page } from '@playwright/test'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// xterm draws into a canvas, so a phone had no way to select or copy terminal
// output. The session menu and a long press on the terminal open the output as
// selectable text: a tmux session's from the server (tmux keeps the history,
// the browser holds one screen), a temporary shell's from the terminal itself.
// REST and WebSocket are fixtures.
test.use({ viewport: { width: 412, height: 860 }, hasTouch: true, locale: 'en-US' })

// Longer than a phone-width terminal, so it wraps across rows.
const LONG = 'cat /opt/stacks/' + 'very-long-directory-name/'.repeat(5) + 'docker-compose.yml'
// What the server's capture of the tmux session returns: history included.
const HISTORY = [...Array.from({ length: 200 }, (_, i) => `history ${i + 1}`), '$ ' + LONG, 'gzip: stdin: not in gzip format'].join('\n')

async function mock(page: Page, { tmux = true, insecure = false } = {}) {
  await page.addInitScript((insecure) => {
    sessionStorage.setItem('token', 'select-test-token')
    localStorage.setItem('i18nextLng', 'en')
    // A panel served over plain HTTP: the async clipboard API is off limits.
    if (insecure) Object.defineProperty(window, 'isSecureContext', { get: () => false })
  }, insecure)
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname
    let data: unknown = {}
    if (path.endsWith('/auth/setup-status')) data = { setup_required: false }
    else if (path.endsWith('/auth/ws-ticket')) data = { ticket: 'select-test-ticket' }
    else if (path.endsWith('/cluster/status')) data = { enabled: false }
    else if (path.endsWith('/ai/sessions/abcdef123456/text')) data = { text: HISTORY }
    else if (path.endsWith('/ai/sessions')) data = tmux ? [{ id: 'abcdef123456', title: 'Shell', tool: 'shell', run_as: 'tester', cwd: '/tmp', state: 'working', persistence: 'service', attached: true, created_at: '2026-09-15T00:00:00Z' }] : []
    else if (path.endsWith('/ai/tools')) data = { tmux: tmux ? { installed: true, supported: true, version: '3.4', min_version: '3.2' } : { installed: false, supported: false, version: '', min_version: '3.2' }, systemd_run: true, accounts: ['tester'], panel_account: 'tester', account: 'tester', tools: {} }
    else if (path.endsWith('/terminal/sessions')) data = { sessions: [] }
    else if (path.endsWith('/terminal/info')) data = { shell_user: 'tester', hostname: 'fixture', home: '/home/tester', shell: '/bin/bash', is_root: false }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
}

async function openTerminal(page: Page) {
  await page.goto('/terminal')
  const terminal = page.locator('[data-terminal-session="active"]')
  await expect(terminal).toBeVisible()
  await expect.poll(() => terminal.evaluate(el => Boolean((el as HTMLElement & { __termRef?: { current?: unknown } }).__termRef?.current))).toBe(true)
  return terminal
}

async function touchPress(page: Page, x: number, y: number, { holdMs = 700, dy = 0 } = {}) {
  const client = await page.context().newCDPSession(page)
  await client.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] })
  const steps = dy ? 10 : 0
  for (let i = 1; i <= steps; i++) {
    await page.waitForTimeout(holdMs / steps)
    await client.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x, y: y + (dy * i) / steps }] })
  }
  if (!steps) await page.waitForTimeout(holdMs)
  await client.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
}

async function centre(page: Page) {
  const box = (await page.locator('[data-terminal-session="active"]').boundingBox())!
  return { x: box.x + box.width / 2, y: box.y + box.height / 3 }
}

test('the session menu opens a tmux session history as text, latest first, wrapped lines whole', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  await mock(page)
  await openTerminal(page)
  await page.locator('[data-session-menu]').click()
  await page.getByRole('menuitem', { name: 'Select text' }).click()

  const dialog = page.getByRole('dialog', { name: 'Select text' })
  const text = dialog.locator('pre')
  // The history comes from tmux, not just the one screen the browser holds.
  await expect(text).toContainText('history 1\n')
  await expect(text).toContainText('gzip: stdin: not in gzip format')
  expect((await text.textContent())?.split('\n')).toContain('$ ' + LONG)
  await expect.poll(() => text.evaluate(el => el.scrollHeight - el.clientHeight - el.scrollTop)).toBeLessThan(2)

  await dialog.getByRole('button', { name: 'Copy all' }).click()
  await expect(page.getByText('Copied the terminal output')).toBeVisible()
  await expect(dialog).toBeHidden()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(HISTORY)
})

// Over plain HTTP the copy falls back to a hidden textarea, which the dialog's
// focus trap used to starve of focus: "copied", and the old clipboard stayed.
test('Copy all really copies on a plain-HTTP panel', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  await mock(page, { insecure: true })
  await openTerminal(page)
  await page.evaluate(() => navigator.clipboard.writeText('STALE'))
  await page.locator('[data-session-menu]').click()
  await page.getByRole('menuitem', { name: 'Select text' }).click()
  const dialog = page.getByRole('dialog', { name: 'Select text' })
  await expect(dialog.locator('pre')).toContainText('gzip')
  await dialog.getByRole('button', { name: 'Copy all' }).click()
  await expect(page.getByText('Copied the terminal output')).toBeVisible()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(HISTORY)
})

test('a long press on the terminal opens it; a tap does not', async ({ page }) => {
  await mock(page)
  const terminal = await openTerminal(page)
  const box = (await terminal.boundingBox())!
  await terminal.tap({ position: { x: box.width / 2, y: box.height / 3 } })
  await page.waitForTimeout(700)
  await expect(page.getByRole('dialog')).toHaveCount(0)

  const { x, y } = await centre(page)
  await touchPress(page, x, y)
  const dialog = page.getByRole('dialog', { name: 'Select text' })
  await expect(dialog).toBeVisible()
  await expect(dialog.locator('pre')).toContainText('gzip')
  // Lifting the finger that opened it neither closes it nor lands in the terminal.
  await page.waitForTimeout(300)
  await expect(dialog).toBeVisible()
  await dialog.getByRole('button', { name: 'Close' }).first().click()
  await expect(dialog).toBeHidden()
})

// The Android app injects a script that owns terminal drags at the document
// and stops them there. The long press used to never see the finger move, so a
// scroll longer than half a second opened the dialog.
test('in the Android app a long scroll is a scroll, and a long press still opens it', async ({ page }) => {
  await mock(page)
  await openTerminal(page)
  await page.evaluate(readFileSync(resolve(__dirname, '../../android/app/src/main/assets/panel.js'), 'utf8'))
  const { x, y } = await centre(page)
  await touchPress(page, x, y, { holdMs: 900, dy: 200 })
  await page.waitForTimeout(300)
  await expect(page.getByRole('dialog')).toHaveCount(0)

  await touchPress(page, x, y)
  await expect(page.getByRole('dialog', { name: 'Select text' })).toBeVisible()
})

test('a temporary shell (no tmux) gives its own buffer, wrapped lines whole', async ({ page }) => {
  await mock(page, { tmux: false })
  await page.goto('/terminal')
  await page.getByRole('button', { name: 'New temporary session' }).tap()
  const terminal = page.locator('[data-terminal-session="active"]')
  await expect.poll(() => terminal.evaluate(el => Boolean((el as HTMLElement & { __termRef?: { current?: unknown } }).__termRef?.current))).toBe(true)
  await terminal.evaluate((el, long) => new Promise<void>(done => {
    const term = (el as HTMLElement & { __termRef: { current: { write: (s: string, cb: () => void) => void } } }).__termRef.current
    term.write('first\r\n$ ' + long + '\r\nlast\r\n', done)
  }), LONG)
  await page.locator('[data-session-menu]').click()
  await page.getByRole('menuitem', { name: 'Select text' }).click()
  const text = page.getByRole('dialog', { name: 'Select text' }).locator('pre')
  await expect(text).toContainText('last')
  expect((await text.textContent())?.split('\n')).toContain('$ ' + LONG)
})
