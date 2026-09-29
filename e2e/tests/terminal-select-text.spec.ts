import { test, expect, type Page } from '@playwright/test'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// xterm draws into a canvas, so a phone had no way to select or copy terminal
// output. A long press now selects the word under the finger in place and
// raises a bar — Copy, Copy line, View all — and the session menu opens the
// whole output as selectable text: a tmux session's from the server (tmux
// keeps the history, the browser holds one screen), a temporary shell's from
// the terminal itself. REST and WebSocket are fixtures.
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

// Write output the way tmux leaves the terminal: alternate screen, mouse
// tracking on — the state a phone session is in.
async function screen(page: Page, text: string) {
  await page.locator('[data-terminal-session="active"]').evaluate((el, text) => new Promise<void>(done => {
    const term = (el as HTMLElement & { __termRef: { current: { write: (s: string, cb: () => void) => void } } }).__termRef.current
    term.write('\x1b[?1049h\x1b[?1000h\x1b[?1006h\x1b[H\x1b[2J' + text, done)
  }), text)
}

// The middle of the cell at (row, col) of the visible screen, in page pixels.
async function cellPoint(page: Page, row: number, col: number) {
  return page.locator('[data-terminal-session="active"]').evaluate((el, [row, col]) => {
    const term = (el as HTMLElement & { __termRef: { current: { cols: number; rows: number } } }).__termRef.current
    const r = el.querySelector('.xterm-screen')!.getBoundingClientRect()
    const w = r.width / term.cols
    const h = r.height / term.rows
    return { x: r.left + (col + 0.5) * w, y: r.top + (row + 0.5) * h }
  }, [row, col])
}

const selection = (page: Page) => page.locator('[data-terminal-session="active"]').evaluate(el =>
  (el as HTMLElement & { __termRef: { current: { getSelection: () => string } } }).__termRef.current.getSelection())

test('a long press selects the word under the finger, and Copy takes just that word', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  await mock(page)
  await openTerminal(page)
  await screen(page, '$ docker logs web\r\nerror: open /opt/stacks/app/docker-compose.yml: no such file\r\nexit status 1\r\n')

  const at = await cellPoint(page, 1, 16)
  await touchPress(page, at.x, at.y)
  const bar = page.getByRole('toolbar', { name: 'Copy terminal text' })
  await expect(bar).toBeVisible()
  // Selected in place, with the colon after it left off.
  expect(await selection(page)).toBe('/opt/stacks/app/docker-compose.yml')
  // Lifting the finger that raised it does not dismiss it.
  await page.waitForTimeout(300)
  await expect(bar).toBeVisible()

  await bar.getByRole('button', { name: 'Copy', exact: true }).click()
  await expect(page.getByText('Copied: /opt/stacks/app/docker-compose.yml')).toBeVisible()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('/opt/stacks/app/docker-compose.yml')
  await expect(bar).toBeHidden()
  expect(await selection(page)).toBe('')
})

// Drag an element (a selection handle) with a finger to a point on screen.
async function dragTo(page: Page, from: { x: number; y: number }, to: { x: number; y: number }) {
  const client = await page.context().newCDPSession(page)
  await client.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [from] })
  for (let i = 1; i <= 8; i++) {
    await client.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: from.x + ((to.x - from.x) * i) / 8, y: from.y + ((to.y - from.y) * i) / 8 }] })
  }
  await client.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
}

async function handleCentre(page: Page, end: 'start' | 'end') {
  const box = (await page.locator(`[data-selection-handle="${end}"]`).boundingBox())!
  return { x: box.x + box.width / 2, y: box.y + box.height / 2 }
}

// A handle's knob hangs below the text, so the finger holding it sits a row
// below the cell it aims at.
async function belowCell(page: Page, row: number, col: number) {
  const p = await cellPoint(page, row, col)
  const q = await cellPoint(page, row + 1, col)
  return { x: p.x, y: q.y }
}

test('a handle narrows a prompt to its path, and Copy takes what is selected', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  await mock(page)
  await openTerminal(page)
  await screen(page, 'user@host:/opt/stacks/SFPanel$ ls\r\n')

  const at = await cellPoint(page, 0, 20)
  await touchPress(page, at.x, at.y)
  // The prompt's $ is not part of the path.
  expect(await selection(page)).toBe('user@host:/opt/stacks/SFPanel')
  // 'user@host:' is ten cells: the path starts at column 10.
  await dragTo(page, await handleCentre(page, 'start'), await belowCell(page, 0, 10))
  expect(await selection(page)).toBe('/opt/stacks/SFPanel')

  await page.getByRole('toolbar', { name: 'Copy terminal text' }).getByRole('button', { name: 'Copy', exact: true }).click()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('/opt/stacks/SFPanel')
})

test('the end handle widens the selection across words', async ({ page }) => {
  await mock(page)
  await openTerminal(page)
  await screen(page, 'hello brave new world\r\n')
  const at = await cellPoint(page, 0, 1)
  await touchPress(page, at.x, at.y)
  expect(await selection(page)).toBe('hello')
  await dragTo(page, await handleCentre(page, 'end'), await belowCell(page, 0, 14))
  expect(await selection(page)).toBe('hello brave new')
})

test('holding on after the press and dragging extends the selection', async ({ page }) => {
  await mock(page)
  await openTerminal(page)
  await screen(page, 'alpha beta gamma delta\r\n')
  const start = await cellPoint(page, 0, 7)
  const end = await cellPoint(page, 0, 15)
  const client = await page.context().newCDPSession(page)
  await client.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [start] })
  await page.waitForTimeout(700)
  for (let i = 1; i <= 6; i++) {
    await client.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: start.x + ((end.x - start.x) * i) / 6, y: start.y }] })
  }
  await client.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
  expect(await selection(page)).toBe('beta gamma')
})

test('a wrapped path comes whole, Copy line takes the line, View all opens the text view', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  await mock(page)
  await openTerminal(page)
  const cols = await page.locator('[data-terminal-session="active"]').evaluate(el =>
    (el as HTMLElement & { __termRef: { current: { cols: number } } }).__termRef.current.cols)
  const path = '/opt/stacks/' + 'x'.repeat(cols) + '/compose.yml'
  await screen(page, 'cat ' + path + '\r\n')

  // Press on the second row, inside the part of the path that wrapped.
  let at = await cellPoint(page, 1, 3)
  await touchPress(page, at.x, at.y)
  const bar = page.getByRole('toolbar', { name: 'Copy terminal text' })
  await expect(bar).toBeVisible()
  expect(await selection(page)).toBe(path)
  await bar.getByRole('button', { name: 'Copy line' }).click()
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('cat ' + path)

  at = await cellPoint(page, 0, 1)
  await touchPress(page, at.x, at.y)
  await bar.getByRole('button', { name: 'View all' }).click()
  await expect(page.getByRole('dialog', { name: 'Select text' })).toBeVisible()
})

test('a tap does nothing, a tap elsewhere dismisses the bar, a press on an empty row opens the text view', async ({ page }) => {
  await mock(page)
  const terminal = await openTerminal(page)
  await screen(page, 'hello world\r\n')
  const box = (await terminal.boundingBox())!
  await terminal.tap({ position: { x: box.width / 2, y: box.height / 3 } })
  await page.waitForTimeout(700)
  await expect(page.getByRole('toolbar')).toHaveCount(0)
  await expect(page.getByRole('dialog')).toHaveCount(0)

  const at = await cellPoint(page, 0, 2)
  await touchPress(page, at.x, at.y)
  await expect(page.getByRole('toolbar', { name: 'Copy terminal text' })).toBeVisible()
  expect(await selection(page)).toBe('hello')
  await terminal.tap({ position: { x: 10, y: box.height - 20 } })
  await expect(page.getByRole('toolbar')).toHaveCount(0)
  expect(await selection(page)).toBe('')

  const empty = await cellPoint(page, 5, 2)
  await touchPress(page, empty.x, empty.y)
  await expect(page.getByRole('dialog', { name: 'Select text' })).toBeVisible()
})

// The Android app injects a script that owns terminal drags at the document
// and stops them there. The long press used to never see the finger move, so a
// scroll longer than half a second opened the dialog.
test('in the Android app a long scroll is a scroll, and a long press still selects', async ({ page }) => {
  await mock(page)
  await openTerminal(page)
  await screen(page, Array.from({ length: 60 }, (_, i) => `row ${i}`).join('\r\n'))
  await page.evaluate(readFileSync(resolve(__dirname, '../../android/app/src/main/assets/panel.js'), 'utf8'))
  const { x, y } = await centre(page)
  await touchPress(page, x, y, { holdMs: 900, dy: 200 })
  await page.waitForTimeout(300)
  await expect(page.getByRole('toolbar')).toHaveCount(0)
  await expect(page.getByRole('dialog')).toHaveCount(0)

  const at = await cellPoint(page, 3, 1)
  await touchPress(page, at.x, at.y)
  await expect(page.getByRole('toolbar', { name: 'Copy terminal text' })).toBeVisible()
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
  await page.getByRole('button', { name: 'Close' }).first().click()
  await expect(page.getByRole('dialog')).toHaveCount(0)

  // With scrollback above the screen, a long press still lands on the word
  // under the finger.
  await terminal.evaluate(el => new Promise<void>(done => {
    const term = (el as HTMLElement & { __termRef: { current: { write: (s: string, cb: () => void) => void; rows: number } } }).__termRef.current
    term.write(Array.from({ length: 120 }, (_, i) => `scroll ${i}`).join('\r\n') + '\r\nneedle here\r\n', done)
  }))
  const rows = await terminal.evaluate(el => (el as HTMLElement & { __termRef: { current: { rows: number } } }).__termRef.current.rows)
  const at = await cellPoint(page, rows - 2, 2)
  await touchPress(page, at.x, at.y)
  expect(await selection(page)).toBe('needle')
})
