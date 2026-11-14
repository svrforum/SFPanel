import { test, expect, type Page } from '@playwright/test'
import { resolve } from 'node:path'
import { readFileSync } from 'node:fs'

// The composer and the key row of a touch device. REST and WebSocket are
// fixtures: what the page sends to the session is read off the socket.
test.use({ viewport: { width: 412, height: 860 }, hasTouch: true, locale: 'en-US' })

const PANEL_JS = resolve(__dirname, '../../android/app/src/main/assets/panel.js')
const S = (id: string, tool: string, title: string) => ({ id, tool, title, run_as: 'tester', cwd: '/srv/app', state: tool === 'shell' ? 'shell' : 'working', persistence: 'service', attached: true, created_at: '2026-01-01T00:00:00Z' })
const AGENT = S('aaaaaaaaaaa1', 'claude', 'Agent')
const SHELL = S('aaaaaaaaaaa2', 'shell', 'Shell')

async function openTerminal(page: Page, active = AGENT.id) {
  // What reached the session, frame by frame: input as text, control as JSON.
  const input: string[] = []
  const control: string[] = []
  await page.addInitScript((active) => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    if (!localStorage.getItem('sfpanel_terminal_active:local')) localStorage.setItem('sfpanel_terminal_active:local', 'tmux:' + active)
  }, active)
  await page.route('**/api/v1/**', async route => {
    const url = new URL(route.request().url())
    let data: unknown = {}
    if (url.pathname.endsWith('/auth/setup-status')) data = { setup_required: false }
    else if (url.pathname.endsWith('/auth/ws-ticket')) data = { ticket: 'fixture-ticket' }
    else if (url.pathname.endsWith('/cluster/status')) data = { enabled: false }
    else if (url.pathname.endsWith('/system/overview')) data = { version: 'test' }
    else if (url.pathname.endsWith('/ai/sessions')) data = [AGENT, SHELL]
    else if (url.pathname.endsWith('/ai/tools')) data = { tmux: { installed: true, supported: true, version: '3.4', min_version: '3.2' }, systemd_run: true, accounts: ['tester'], panel_account: 'tester', account: 'tester', tools: {}, attach_latest: true }
    else if (url.pathname.endsWith('/terminal/info')) data = { shell_user: 'tester', is_root: false, hostname: 'fixture' }
    else if (url.pathname.endsWith('/terminal/sessions')) data = { sessions: [] }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, socket => {
    socket.onMessage(message => {
      if (typeof message === 'string') {
        if (!message.includes('"resize"')) control.push(message)
      } else input.push(message.toString('utf8'))
    })
  })
  await page.goto('/terminal')
  const terminal = page.locator('[data-terminal-session="active"]')
  await expect(terminal).toBeVisible()
  await expect.poll(() => terminal.evaluate(el => (el as any).__wsRef?.current?.readyState)).toBe(1)
  const area = page.locator('[data-terminal-input]')
  const key = (name: string) => area.locator('[data-terminal-keys]').getByRole('button', { name, exact: true })
  const nextKeys = area.getByRole('button', { name: /^Next keys/ })
  const composer = area.getByRole('textbox', { name: 'Composer' })
  const send = area.getByRole('button', { name: 'Send', exact: true })
  const focused = () => page.evaluate(() => {
    const el = document.activeElement
    return el?.closest('[data-terminal-composer]') ? 'composer' : el?.classList.contains('xterm-helper-textarea') ? 'terminal' : el?.tagName ?? 'none'
  })
  return { terminal, area, key, nextKeys, composer, send, focused, input, control }
}

test('what the composer wrote goes out as one paste and one Enter', async ({ page }) => {
  const t = await openTerminal(page)
  // The program asked for bracketed paste, as an AI CLI does: the lines of a
  // prompt must not each be an Enter.
  await t.terminal.evaluate(el => new Promise<void>(done => (el as any).__termRef.current.write('\x1b[?2004h', done)))
  await t.composer.fill('첫 줄입니다\nsecond line')
  await t.send.tap()
  await expect.poll(() => t.input.length).toBe(2)
  expect(t.input[0]).toBe('\x1b[200~첫 줄입니다\rsecond line\x1b[201~')
  expect(t.input[1]).toBe('\r')
  // Out of copy mode first: there the text would be that mode's keys.
  expect(t.control).toEqual(['{"type":"latest"}'])
  await expect(t.composer).toHaveValue('')
  // The keyboard stays with the composer for the next message.
  expect(await t.focused()).toBe('composer')
  await page.waitForTimeout(300)
  expect(t.input.length).toBe(2)
})

test('with Enter switched off the text is only put in, and a bare send is the Enter', async ({ page }) => {
  const t = await openTerminal(page)
  await t.area.getByRole('button', { name: 'Composer tools' }).tap()
  await page.getByRole('dialog').getByRole('checkbox').click()
  await page.keyboard.press('Escape')
  await t.composer.fill('check this first')
  await t.send.tap()
  await expect.poll(() => t.input.length).toBe(1)
  await page.waitForTimeout(300)
  expect(t.input).toEqual(['check this first'])
  // Nothing written: the Enter an approval prompt, or the text just put in, waits for.
  await t.send.tap()
  await expect.poll(() => t.input.at(-1)).toBe('\r')
  expect(t.input.length).toBe(2)
  // Remembered: the next page load sends as it was left.
  await page.reload()
  await expect(page.locator('[data-terminal-session="active"]')).toBeVisible()
  await page.locator('[data-terminal-input]').getByRole('button', { name: 'Composer tools' }).tap()
  await expect(page.getByRole('dialog').getByRole('checkbox')).not.toBeChecked()
})

test('a key leaves the keyboard where it was', async ({ page }) => {
  const t = await openTerminal(page)
  await t.composer.tap()
  await t.composer.fill('half written')
  await t.key('Move cursor up').tap()
  await expect.poll(() => t.input.at(-1)).toBe('\x1b[A')
  await t.key('Escape').tap()
  await expect.poll(() => t.input.at(-1)).toBe('\x1b')
  expect(await t.focused()).toBe('composer')
  await expect(t.composer).toHaveValue('half written')
  // And with the terminal: its hidden field keeps the focus, so the on-screen
  // keyboard does not close at every key.
  await t.terminal.tap()
  await expect.poll(t.focused).toBe('terminal')
  await t.key('Shift+Tab').tap()
  await expect.poll(() => t.input.at(-1)).toBe('\x1b[Z')
  expect(await t.focused()).toBe('terminal')
})

test('a modifier is for the next key, or locked for every key until tapped again', async ({ page }) => {
  const t = await openTerminal(page, SHELL.id)
  await t.terminal.tap()
  const ctrl = t.key('Toggle ctrl for the next key')
  await ctrl.tap()
  await expect(ctrl).toHaveAttribute('aria-pressed', 'true')
  await expect(ctrl).not.toHaveAttribute('data-locked')
  await page.keyboard.press('c')
  await expect.poll(() => t.input.at(-1)).toBe('\x03')
  await expect(ctrl).toHaveAttribute('aria-pressed', 'false')
  await page.keyboard.press('c')
  await expect.poll(() => t.input.at(-1)).toBe('c')
  // Twice: locked. C-x C-s without going back to Ctrl in between.
  await ctrl.tap()
  await ctrl.tap()
  await expect(ctrl).toHaveAttribute('data-locked')
  await page.keyboard.press('x')
  await expect.poll(() => t.input.at(-1)).toBe('\x18')
  await page.keyboard.press('s')
  await expect.poll(() => t.input.at(-1)).toBe('\x13')
  await expect(ctrl).toHaveAttribute('aria-pressed', 'true')
  // A key of the row is held by it too.
  await t.key('Move cursor left').tap()
  await expect.poll(() => t.input.at(-1)).toBe('\x1b[1;5D')
  await ctrl.tap()
  await expect(ctrl).toHaveAttribute('aria-pressed', 'false')
  await page.keyboard.press('s')
  await expect.poll(() => t.input.at(-1)).toBe('s')
})

test('an arrow held down repeats, and a tap is one', async ({ page }) => {
  const t = await openTerminal(page, SHELL.id)
  const up = t.key('Move cursor up')
  await up.tap()
  await page.waitForTimeout(200)
  expect(t.input.filter(i => i === '\x1b[A').length).toBe(1)
  const box = (await up.boundingBox())!
  const client = await page.context().newCDPSession(page)
  const at = { x: box.x + box.width / 2, y: box.y + box.height / 2 }
  await client.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [at] })
  await page.waitForTimeout(900)
  await client.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
  await client.detach()
  const held = t.input.filter(i => i === '\x1b[A').length - 1
  // 380ms, then one every 90: some five in 900ms. Not one, and not a runaway.
  expect(held).toBeGreaterThanOrEqual(3)
  expect(held).toBeLessThanOrEqual(8)
  await page.waitForTimeout(400)
  expect(t.input.filter(i => i === '\x1b[A').length - 1).toBe(held)
})

test('the keys are those of what the session runs, a page at a time', async ({ page }) => {
  const t = await openTerminal(page)
  const labels = () => t.area.locator('[data-terminal-keys] button').allTextContents()
  // An AI CLI: stop, permission mode, its menus, answer, interrupt, a command.
  expect(await labels()).toEqual(['Esc', '⇧Tab', '↑', '↓', '⏎', '^C', '/', '', '1/3'])
  await t.nextKeys.tap()
  expect((await labels()).slice(0, 7)).toEqual(['1', '2', '3', '←', '→', 'Tab', '@'])
  await t.key('2').tap()
  await expect.poll(() => t.input.at(-1)).toBe('2')
  // A sideways drag across the row turns the page as well, either way.
  const row = (await t.area.locator('[data-terminal-keys]').boundingBox())!
  const client = await page.context().newCDPSession(page)
  const drag = async (from: number, to: number) => {
    const y = row.y + row.height / 2
    await client.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: from, y }] })
    await client.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: (from + to) / 2, y }] })
    await client.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: to, y }] })
    await client.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
  }
  const sent = t.input.length
  await drag(row.x + 300, row.x + 100)
  await expect.poll(async () => (await labels()).at(-1)).toBe('3/3')
  await drag(row.x + 100, row.x + 300)
  await expect.poll(async () => (await labels()).at(-1)).toBe('2/3')
  await client.detach()
  // The drag pressed nothing on its way.
  expect(t.input.length).toBe(sent)
  // No key is wider than its cell: the row never scrolls sideways.
  expect(await t.area.locator('[data-terminal-keys]').evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true)
  // A shell has its own.
  await page.getByRole('button', { name: 'Sessions' }).tap()
  await page.getByRole('tab', { name: /Shell/ }).tap()
  await expect.poll(labels).toEqual(['Esc', 'Tab', 'Ctrl', '↑', '↓', '←', '→', '', '1/3'])
})

test('a draft stays with its session, and what was sent can be had again', async ({ page }) => {
  const t = await openTerminal(page)
  await t.composer.fill('for the agent')
  await page.getByRole('button', { name: 'Sessions' }).tap()
  await page.getByRole('tab', { name: /Shell/ }).tap()
  await expect(t.composer).toHaveValue('')
  await expect(t.composer).toHaveAttribute('placeholder', 'Write a command, then send')
  await t.composer.fill('ls -la')
  await page.locator('[data-terminal-input]').getByRole('button', { name: 'Send', exact: true }).tap()
  await page.getByRole('button', { name: 'Sessions' }).tap()
  await page.getByRole('tab', { name: /Agent/ }).tap()
  await expect(t.composer).toHaveValue('for the agent')
  await t.area.getByRole('button', { name: 'Composer tools' }).tap()
  await page.getByRole('dialog').locator('[data-recent]', { hasText: 'ls -la' }).tap()
  // Added to what was being written, not sent, and the keyboard is the composer's.
  await expect(t.composer).toHaveValue('for the agentls -la')
  await expect.poll(t.focused).toBe('composer')
})

test('an Android app up to 0.1.6 hides the input area and one that says it uses it does not', async ({ page }) => {
  const t = await openTerminal(page)
  await expect(t.area).toBeVisible()
  // As the app installs its script (WebView.evaluateJavascript).
  await page.evaluate(readFileSync(PANEL_JS, 'utf8'))
  await expect(t.area).toBeHidden()
})

test('the app\'s "paste and edit" reaches the composer', async ({ page }) => {
  const t = await openTerminal(page)
  await t.composer.fill('before ')
  await t.area.evaluate(el => (el as any).__compose('from the clipboard'))
  await expect(t.composer).toHaveValue('before from the clipboard')
  await expect.poll(t.focused).toBe('composer')
})

test.describe('sideways', () => {
  test.use({ viewport: { width: 915, height: 412 }, hasTouch: true })
  test('the composer and the keys take turns, which leaves lines to read with the keyboard up', async ({ page }) => {
    const t = await openTerminal(page)
    await expect(t.area.locator('[data-terminal-keys]')).toBeVisible()
    await expect(t.composer).toBeHidden()
    expect((await t.area.boundingBox())!.height).toBeLessThan(56)
    await t.area.getByRole('button', { name: 'Write', exact: true }).tap()
    await expect(t.composer).toBeFocused()
    await expect(t.area.locator('[data-terminal-keys]')).toBeHidden()
    expect((await t.area.boundingBox())!.height).toBeLessThan(56)
    // What a keyboard leaves of a 412px-high window.
    await page.setViewportSize({ width: 915, height: 190 })
    await expect.poll(() => t.terminal.evaluate(el => (el as any).__termRef.current.rows as number)).toBeGreaterThanOrEqual(5)
    await t.area.getByRole('button', { name: 'Show the keys' }).tap()
    await expect(t.area.locator('[data-terminal-keys]')).toBeVisible()
    await expect(t.composer).toBeHidden()
  })
})
