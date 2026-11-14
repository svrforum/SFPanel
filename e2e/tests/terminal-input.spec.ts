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

type Prefs = { snippets?: { kind: string; body: string }[]; keysets?: Record<string, unknown[][]> }

async function openTerminal(page: Page, active = AGENT.id, { uploads = true, uploadFails = '', prefs = {}, prefsFail = false }: {
  /** the panel takes attachments (GET /ai/tools session_uploads); false: one from before them */
  uploads?: boolean
  /** the upload route answers with this error code */
  uploadFails?: string
  /** what the panel has kept (GET /ai/prefs) */
  prefs?: Prefs
  /** saving them fails */
  prefsFail?: boolean
} = {}) {
  // The panel's side of the snippets and key rows, and every save it was sent.
  const kept = { snippets: prefs.snippets ?? [], keysets: prefs.keysets ?? {} }
  const saves: { what: string; node: string | null; body: any }[] = []
  // What reached the session, frame by frame: input as text, control as JSON.
  const input: string[] = []
  const control: string[] = []
  // The files the page sent to the upload route: session id and file name.
  const uploaded: string[] = []
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
    else if (url.pathname.endsWith('/ai/tools')) data = { tmux: { installed: true, supported: true, version: '3.4', min_version: '3.2' }, systemd_run: true, accounts: ['tester'], panel_account: 'tester', account: 'tester', tools: {}, attach_latest: true, ...(uploads ? { session_uploads: true } : {}) }
    else if (url.pathname.endsWith('/upload')) {
      const id = url.pathname.split('/').at(-2)!
      const name = /filename="([^"]*)"/.exec(route.request().postDataBuffer()?.toString('latin1') ?? '')?.[1] ?? ''
      uploaded.push(`${id}:${name}`)
      if (uploadFails) {
        await route.fulfill({ status: uploadFails === 'FILE_TOO_LARGE' ? 413 : 500, json: { success: false, error: { code: uploadFails, message: 'x' } } })
        return
      }
      // As the panel answers (internal/feature/ai SessionUpload): the path on the session's node.
      data = { path: `/home/tester/.sfpanel-ai/uploads/${id}/20261114-093005-${name}`, name: `20261114-093005-${name}`, size: 3 }
    }
    else if (url.pathname.endsWith('/ai/prefs')) data = kept
    else if (url.pathname.includes('/ai/prefs/')) {
      const what = url.pathname.split('/').at(-1) as 'snippets' | 'keysets'
      const body = route.request().postDataJSON()
      saves.push({ what, node: url.searchParams.get('node'), body })
      if (prefsFail) {
        await route.fulfill({ status: 500, json: { success: false, error: { code: 'INTERNAL_ERROR', message: 'x' } } })
        return
      }
      kept[what] = body[what]
      data = { [what]: body[what] }
    }
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
  const sheet = page.getByRole('dialog')
  const openTools = () => area.getByRole('button', { name: 'Composer tools' }).tap()
  return { terminal, area, key, nextKeys, composer, send, focused, input, control, uploaded, saves, sheet, openTools }
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

test('the terminal takes the keyboard when it is tapped, and no key brings one up', async ({ page }) => {
  const t = await openTerminal(page)
  // A field holding the focus is a keyboard waiting to open: in the Android
  // app the next tap anywhere on the page opened it, a key of the row included.
  await page.waitForTimeout(400)
  expect(await t.focused()).not.toBe('terminal')
  await t.key('Escape').tap()
  await expect.poll(() => t.input.at(-1)).toBe('\x1b')
  expect(await t.focused()).not.toBe('terminal')
  await t.terminal.tap()
  await expect.poll(t.focused).toBe('terminal')
})

test('the keyboard put away lets go of the field it served', async ({ page }) => {
  const t = await openTerminal(page)
  for (const field of ['terminal', 'composer'] as const) {
    await (field === 'terminal' ? t.terminal : t.composer).tap()
    await expect.poll(t.focused).toBe(field)
    // The keyboard comes up: the field keeps the focus.
    await page.setViewportSize({ width: 412, height: 560 })
    await page.waitForTimeout(400)
    expect(await t.focused()).toBe(field)
    // And is put away: left focused, the next key pressed would bring it back.
    await page.setViewportSize({ width: 412, height: 860 })
    await expect.poll(t.focused).not.toBe(field)
  }
  // Turning the phone is not the keyboard going: taller, but another width.
  await page.setViewportSize({ width: 915, height: 412 })
  await t.terminal.tap()
  await expect.poll(t.focused).toBe('terminal')
  await page.setViewportSize({ width: 412, height: 860 })
  await page.waitForTimeout(400)
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

test('an Android app up to 0.1.6 hides the input area: it draws its own keys', async ({ page }) => {
  const t = await openTerminal(page)
  await expect(t.area).toBeVisible()
  // As the app installs its script (WebView.evaluateJavascript); 0.1.6 and
  // earlier set no flag about the input area.
  await page.evaluate(readFileSync(PANEL_JS, 'utf8'))
  await expect(t.area).toBeHidden()
})

test.describe('in the Android app from 0.1.7, on a small phone', () => {
  test.use({ viewport: { width: 360, height: 740 }, hasTouch: true })
  test('the page\'s input area stays, and its eight cells fit the screen', async ({ page }) => {
    const t = await openTerminal(page)
    // The app says the panel brings its own keys, then installs its script.
    await page.evaluate('window.__sfpanelAndroidWebInput=true;' + readFileSync(PANEL_JS, 'utf8'))
    await page.waitForTimeout(500)
    await expect(t.area).toBeVisible()
    const row = t.area.locator('[data-terminal-keys]')
    // The app's stylesheet makes every button at least 48px wide; eight of
    // those and their gaps are wider than this phone.
    expect(await row.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    for (const button of await row.getByRole('button').all()) {
      const box = (await button.boundingBox())!
      expect(box.x + box.width).toBeLessThanOrEqual(360)
      expect(box.height).toBeGreaterThanOrEqual(48)
    }
    // And it works as it does in a browser.
    await t.composer.fill('from the app')
    await t.send.tap()
    await expect.poll(() => t.input[0]).toBe('from the app')
    await expect.poll(() => t.input[1]).toBe('\r')
  })
})

test('the app\'s "paste and edit" reaches the composer', async ({ page }) => {
  const t = await openTerminal(page)
  await t.composer.fill('before ')
  await t.area.evaluate(el => (el as any).__compose('from the clipboard'))
  await expect(t.composer).toHaveValue('before from the clipboard')
  await expect.poll(t.focused).toBe('composer')
})

test('a file attached from the sheet is uploaded to the session and its path goes into the draft', async ({ page }) => {
  const t = await openTerminal(page)
  // No space after the last word: the path must not run into it.
  await t.composer.fill('look at')
  await t.area.getByRole('button', { name: 'Composer tools' }).tap()
  await expect(page.getByRole('dialog').getByRole('button', { name: /Attach a photo or a file/ })).toBeVisible()
  await page.locator('[data-terminal-attach]').setInputFiles([
    { name: 'shot.png', mimeType: 'image/png', buffer: Buffer.from('png') },
    { name: 'error.log', mimeType: 'text/plain', buffer: Buffer.from('log') },
  ])
  // To the session on screen, each file; the paths as the panel gave them, ready to send.
  await expect(t.composer).toHaveValue('look at /home/tester/.sfpanel-ai/uploads/aaaaaaaaaaa1/20261114-093005-shot.png /home/tester/.sfpanel-ai/uploads/aaaaaaaaaaa1/20261114-093005-error.log ')
  expect(t.uploaded).toEqual(['aaaaaaaaaaa1:shot.png', 'aaaaaaaaaaa1:error.log'])
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect.poll(t.focused).toBe('composer')
  // Nothing went to the session on its own.
  expect(t.input).toEqual([])
})

test('an image pasted into the composer is attached, and pasted text stays text', async ({ page }) => {
  const t = await openTerminal(page)
  await t.composer.tap()
  const paste = (kind: 'image' | 'text') => t.composer.evaluate((el, kind) => {
    const data = new DataTransfer()
    if (kind === 'image') data.items.add(new File(['png'], 'clipboard.png', { type: 'image/png' }))
    else data.setData('text/plain', 'plain words')
    return el.dispatchEvent(new ClipboardEvent('paste', { clipboardData: data, bubbles: true, cancelable: true }))
  }, kind)
  // dispatchEvent answers false when the page took the paste for itself.
  expect(await paste('image')).toBe(false)
  await expect(t.composer).toHaveValue('/home/tester/.sfpanel-ai/uploads/aaaaaaaaaaa1/20261114-093005-clipboard.png ')
  expect(t.uploaded).toEqual(['aaaaaaaaaaa1:clipboard.png'])
  expect(await paste('text')).toBe(true)
  expect(t.uploaded.length).toBe(1)
  // Cells copied from a spreadsheet come with a picture of themselves: the
  // text is what was copied, and the paste is left to be one.
  const both = await t.composer.evaluate((el) => {
    const data = new DataTransfer()
    data.items.add(new File(['png'], 'cells.png', { type: 'image/png' }))
    data.setData('text/plain', 'a\tb')
    return el.dispatchEvent(new ClipboardEvent('paste', { clipboardData: data, bubbles: true, cancelable: true }))
  })
  expect(both).toBe(true)
  expect(t.uploaded.length).toBe(1)
})

test('a file the panel refuses says why and leaves the draft alone', async ({ page }) => {
  const t = await openTerminal(page, AGENT.id, { uploadFails: 'FILE_TOO_LARGE' })
  await t.composer.fill('draft')
  await t.area.getByRole('button', { name: 'Composer tools' }).tap()
  await page.locator('[data-terminal-attach]').setInputFiles({ name: 'video.mp4', mimeType: 'video/mp4', buffer: Buffer.from('x') })
  await expect(page.getByText('video.mp4: larger than 25 MB')).toBeVisible()
  // The sheet stays, to pick another; the draft behind it is as it was.
  await expect(page.getByRole('dialog').getByRole('button', { name: /Attach a photo or a file/ })).toBeEnabled()
  await page.keyboard.press('Escape')
  await expect(t.composer).toHaveValue('draft')
})

test('a panel from before attachments offers none', async ({ page }) => {
  const t = await openTerminal(page, AGENT.id, { uploads: false })
  await t.area.getByRole('button', { name: 'Composer tools' }).tap()
  await expect(page.getByRole('dialog').getByText('Recently sent')).toBeVisible()
  await expect(page.locator('[data-terminal-attach]')).toHaveCount(0)
  await page.keyboard.press('Escape')
  // Nor does a pasted image go anywhere: there is no route to send it to.
  await t.composer.tap()
  await t.composer.evaluate((el) => {
    const data = new DataTransfer()
    data.items.add(new File(['png'], 'clipboard.png', { type: 'image/png' }))
    el.dispatchEvent(new ClipboardEvent('paste', { clipboardData: data, bubbles: true, cancelable: true }))
  })
  await page.waitForTimeout(300)
  expect(t.uploaded).toEqual([])
})

test('what is written can be kept as a snippet, put back with a tap, and removed with a way back', async ({ page }) => {
  const t = await openTerminal(page)
  const words = 'run the tests\nand fix only what fails'
  await t.composer.fill(`  ${words}\n`)
  await t.openTools()
  await expect(t.sheet.getByText('Keep what you send often')).toBeVisible()
  await t.sheet.getByRole('button', { name: 'Save what is written' }).tap()
  await expect(page.getByText('Saved as a snippet')).toBeVisible()
  // Kept by the panel, for this kind of session, without the blank edges.
  expect(t.saves).toEqual([{ what: 'snippets', node: null, body: { snippets: [{ kind: 'agent', body: words }] } }])
  const row = t.sheet.locator('[data-snippet]')
  await expect(row).toHaveText(words)
  // The same words are not kept twice.
  await t.sheet.getByRole('button', { name: 'Save what is written' }).tap()
  await expect(page.getByText('Already a snippet')).toBeVisible()
  expect(t.saves).toHaveLength(1)

  await page.keyboard.press('Escape')
  await t.composer.fill('')
  await t.openTools()
  await row.tap()
  // Into the draft, not to the session.
  await expect(t.composer).toHaveValue(words)
  await expect(t.sheet).toHaveCount(0)
  expect(t.input).toEqual([])

  await t.openTools()
  await t.sheet.getByRole('button', { name: 'Remove the snippet' }).tap()
  await expect(row).toHaveCount(0)
  expect(t.saves.at(-1)?.body).toEqual({ snippets: [] })
  // The way back is in the sheet: nothing outside an open sheet can be pressed.
  await expect(t.sheet.getByRole('status')).toContainText('Snippet removed')
  await t.sheet.getByRole('button', { name: 'Undo' }).tap()
  await expect(row).toHaveText(words)
  expect(t.saves.at(-1)?.body).toEqual({ snippets: [{ kind: 'agent', body: words }] })
  await expect(t.sheet.getByRole('status')).toHaveCount(0)
})

test('snippets are those of the kind of session, and what was sent can be kept as one', async ({ page }) => {
  const prefs = { snippets: [{ kind: 'agent', body: 'review the diff' }, { kind: 'shell', body: 'docker compose logs -f' }] }
  const t = await openTerminal(page, SHELL.id, { prefs })
  await t.composer.fill('df -h')
  await t.send.tap()
  await t.openTools()
  await expect(t.sheet.locator('[data-snippet]')).toHaveText(['docker compose logs -f'])
  const keep = t.sheet.getByRole('button', { name: 'Save as a snippet' })
  await keep.tap()
  // Newest first, and the other kind's stay in the list that is saved.
  await expect(t.sheet.locator('[data-snippet]')).toHaveText(['df -h', 'docker compose logs -f'])
  expect(t.saves.at(-1)?.body).toEqual({ snippets: [{ kind: 'shell', body: 'df -h' }, ...prefs.snippets] })
  await expect(keep).toBeDisabled()
})

test('a snippet the panel does not keep says so and is not shown as kept', async ({ page }) => {
  const t = await openTerminal(page, AGENT.id, { prefsFail: true })
  await t.composer.fill('keep me')
  await t.openTools()
  await t.sheet.getByRole('button', { name: 'Save what is written' }).tap()
  await expect(page.getByText('Could not be saved')).toBeVisible()
  await expect(t.sheet.locator('[data-snippet]')).toHaveCount(0)
  await page.keyboard.press('Escape')
  await expect(t.composer).toHaveValue('keep me')
})

test('the keys the operator arranged replace the panel\'s, and a key of their own types its text', async ({ page }) => {
  const t = await openTerminal(page, AGENT.id, { prefs: { keysets: { agent: [[{ id: 'esc' }, { label: '/compact', text: '/compact', enter: true }, { id: 'ctrl-b' }, { id: 'hyper' }, { label: 'y', text: 'y' }]] } } })
  const row = t.area.locator('[data-terminal-keys]')
  // What a later panel may have saved and this page cannot draw is left out;
  // one page of four keys, and the cell that turns it is still the last.
  await expect(row.getByRole('button')).toHaveText(['Esc', '/compact', '^B', 'y', '1/1'])
  const gap = await row.evaluate(el => el.getBoundingClientRect().right - el.querySelector('button:last-of-type')!.getBoundingClientRect().right)
  expect(gap).toBeLessThan(12)
  await t.key('Ctrl+B').tap()
  expect(t.input).toEqual(['\x02'])
  // Its text as a paste, on the live screen, and the Enter it asks for.
  await t.key('/compact').tap()
  await expect.poll(() => t.input).toEqual(['\x02', '/compact', '\r'])
  expect(t.control).toEqual(['{"type":"latest"}'])
  // One character is a key like any other: the modifiers apply.
  await t.key('y').tap()
  await expect.poll(() => t.input.at(-1)).toBe('y')
  // The shell's keys were not arranged: the panel's own.
  await page.unrouteAll({ behavior: 'ignoreErrors' })
})

test('the key editor moves, removes and adds keys, and saves the arrangement', async ({ page }) => {
  const t = await openTerminal(page)
  await t.openTools()
  await t.sheet.getByRole('button', { name: /Edit the keys/ }).tap()
  const editor = page.locator('[data-keys-editor]')
  await expect(editor.getByRole('button', { name: 'AI tools' })).toHaveAttribute('aria-pressed', 'true')
  const page1 = editor.locator('[data-keys-page="1"]')
  const chips = page1.locator('.grid-cols-7').first().getByRole('button')
  await expect(chips).toHaveText(['Esc', '⇧Tab', '↑', '↓', '⏎', '^C', '/'])
  // A full page takes no more.
  await expect(page1.getByRole('button', { name: 'Add a key to page 1' })).toHaveCount(0)

  // ^C to the front.
  await page1.getByRole('button', { name: 'Interrupt (Ctrl+C)' }).tap()
  for (let i = 0; i < 5; i++) await page1.getByRole('button', { name: 'Move earlier' }).tap()
  await expect(chips).toHaveText(['^C', 'Esc', '⇧Tab', '↑', '↓', '⏎', '/'])
  // First of the first page: nowhere earlier to go.
  await expect(page1.getByRole('button', { name: 'Move earlier' })).toBeDisabled()
  // Two keys out.
  await page1.getByRole('button', { name: '/', exact: true }).tap()
  await page1.getByRole('button', { name: 'Remove the key' }).tap()
  await page1.getByRole('button', { name: 'Enter', exact: true }).tap()
  await page1.getByRole('button', { name: 'Remove the key' }).tap()
  // Two in: one from the catalog, one of their own.
  await page1.getByRole('button', { name: 'Add a key to page 1' }).tap()
  const picker = page1.locator('[data-keys-picker]')
  await picker.getByRole('button', { name: 'Ctrl+B', exact: true }).tap()
  const add = picker.getByRole('button', { name: 'Add', exact: true })
  await expect(add).toBeDisabled()
  await picker.getByRole('textbox', { name: 'What it types' }).fill('/compact')
  await picker.getByRole('checkbox').check()
  await add.tap()
  await expect(chips).toHaveText(['^C', 'Esc', '⇧Tab', '↑', '↓', '^B', '/compact'])
  // Full again: the picker has gone with the cell that opened it.
  await expect(picker).toHaveCount(0)

  expect(t.saves).toEqual([])
  await editor.getByRole('button', { name: 'Save', exact: true }).tap()
  await expect(page.getByText('Keys saved')).toBeVisible()
  await expect(editor).toHaveCount(0)
  const id = (...ids: string[]) => ids.map(i => ({ id: i }))
  const char = (c: string) => ({ label: c, text: c })
  // The shell's keys were not touched: nothing is saved for them.
  expect(t.saves).toEqual([{ what: 'keysets', node: null, body: { keysets: { agent: [
    [...id('ctrl-c', 'esc', 'shiftTab', 'up', 'down', 'ctrl-b'), { label: '/compact', text: '/compact', enter: true }],
    [char('1'), char('2'), char('3'), ...id('left', 'right', 'tab'), char('@')],
    id('ctrl', 'alt', 'shift', 'pageUp', 'pageDown', 'home', 'end'),
  ] } } }])
  // And the row under the terminal is the new one.
  await expect(t.area.locator('[data-terminal-keys]').getByRole('button')).toHaveText(['^C', 'Esc', '⇧Tab', '↑', '↓', '^B', '/compact', '1/3'])
})

test('a key at its page\'s edge moves to the next page, and the panel\'s keys come back as nothing saved', async ({ page }) => {
  const t = await openTerminal(page, SHELL.id, { prefs: { keysets: { shell: [[{ id: 'esc' }, { id: 'tab' }], [{ id: 'up' }]], agent: [[{ id: 'enter' }]] } } })
  await t.openTools()
  await t.sheet.getByRole('button', { name: /Edit the keys/ }).tap()
  const editor = page.locator('[data-keys-editor]')
  // On the keys of the session on screen.
  await expect(editor.getByRole('button', { name: 'Shell', exact: true })).toHaveAttribute('aria-pressed', 'true')
  const chipsOf = (n: number) => editor.locator(`[data-keys-page="${n}"] .grid-cols-7`).first().getByRole('button', { pressed: undefined }).filter({ hasNotText: /^$/ })
  await editor.locator('[data-keys-page="1"]').getByRole('button', { name: 'Tab', exact: true }).tap()
  await editor.getByRole('button', { name: 'Move later' }).tap()
  await expect(chipsOf(1)).toHaveText(['Esc'])
  await expect(chipsOf(2)).toHaveText(['Tab', '↑'])
  // It is still the key in hand, on its new page.
  await expect(editor.locator('[data-keys-page="2"]').getByRole('button', { name: 'Tab', exact: true })).toHaveAttribute('aria-pressed', 'true')
  // A third page, and a key on it.
  await editor.getByRole('button', { name: 'Add a page' }).tap()
  await editor.locator('[data-keys-page="3"] [data-keys-picker]').getByRole('button', { name: 'Ctrl+L', exact: true }).tap()
  // The AI tools' keys go back to the panel's own.
  await editor.getByRole('button', { name: 'AI tools' }).tap()
  await expect(chipsOf(1)).toHaveText(['⏎'])
  await editor.getByRole('button', { name: 'Back to the panel\'s keys' }).tap()
  await expect(chipsOf(1)).toHaveText(['Esc', '⇧Tab', '↑', '↓', '⏎', '^C', '/'])
  await editor.getByRole('button', { name: 'Save', exact: true }).tap()
  await expect(editor).toHaveCount(0)
  expect(t.saves).toEqual([{ what: 'keysets', node: null, body: { keysets: { shell: [[{ id: 'esc' }], [{ id: 'tab' }, { id: 'up' }], [{ id: 'ctrl-l' }]] } } }])
})

test('an arrangement the panel does not keep stays open to try again', async ({ page }) => {
  const t = await openTerminal(page, AGENT.id, { prefsFail: true })
  await t.openTools()
  await t.sheet.getByRole('button', { name: /Edit the keys/ }).tap()
  const editor = page.locator('[data-keys-editor]')
  await editor.locator('[data-keys-page="1"]').getByRole('button', { name: 'Escape', exact: true }).tap()
  await editor.getByRole('button', { name: 'Remove the key' }).tap()
  await editor.getByRole('button', { name: 'Save', exact: true }).tap()
  await expect(page.getByText('Could not be saved')).toBeVisible()
  await expect(editor).toBeVisible()
  await page.keyboard.press('Escape')
  // Nothing changed under the terminal.
  await expect(t.area.locator('[data-terminal-keys]').getByRole('button').first()).toHaveText('Esc')
})

test.describe('with a mouse and a keyboard', () => {
  test.use({ viewport: { width: 1280, height: 800 }, hasTouch: false })

  // What the terminal's own field is given, as a browser gives it.
  const paste = (page: Page, kind: 'image' | 'text' | 'both') => page.locator('[data-terminal-session="active"] .xterm-helper-textarea').evaluate((el, kind) => {
    const data = new DataTransfer()
    if (kind !== 'text') data.items.add(new File(['png'], 'image.png', { type: 'image/png' }))
    if (kind !== 'image') data.setData('text/plain', 'plain words')
    el.dispatchEvent(new ClipboardEvent('paste', { clipboardData: data, bubbles: true, cancelable: true }))
  }, kind)
  const drag = (page: Page, type: 'dragenter' | 'drop' | 'dragleave', names: string[]) => page.locator('[data-terminal-session="active"]').evaluate((el, { type, names }) => {
    const data = new DataTransfer()
    for (const name of names) data.items.add(new File(['x'], name, { type: 'text/plain' }))
    el.dispatchEvent(new DragEvent(type, { dataTransfer: data, bubbles: true, cancelable: true }))
  }, { type, names })

  test('an image pasted into the terminal is uploaded and its path typed, and text is pasted as text', async ({ page }) => {
    const t = await openTerminal(page)
    await expect(t.area).toHaveCount(0)
    await paste(page, 'image')
    // The path, on the live screen, and no Enter: it is the start of a prompt.
    await expect.poll(() => t.input).toEqual(['/home/tester/.sfpanel-ai/uploads/aaaaaaaaaaa1/20261114-093005-image.png '])
    expect(t.uploaded).toEqual(['aaaaaaaaaaa1:image.png'])
    expect(t.control).toEqual(['{"type":"latest"}'])
    // Text is the terminal's own business, with or without a picture beside it.
    await paste(page, 'text')
    await paste(page, 'both')
    await expect.poll(() => t.input).toEqual(['/home/tester/.sfpanel-ai/uploads/aaaaaaaaaaa1/20261114-093005-image.png ', 'plain words', 'plain words'])
    expect(t.uploaded).toHaveLength(1)
  })

  test('files dropped on the terminal are uploaded and their paths typed', async ({ page }) => {
    const t = await openTerminal(page)
    const zone = page.locator('[data-terminal-drop]')
    await drag(page, 'dragenter', ['error.log'])
    await expect(zone).toBeVisible()
    await expect(page.getByText('Drop to upload to this session\'s server')).toBeVisible()
    await drag(page, 'drop', ['error.log', 'trace.txt'])
    await expect(zone).toHaveCount(0)
    await expect.poll(() => t.input).toEqual(['/home/tester/.sfpanel-ai/uploads/aaaaaaaaaaa1/20261114-093005-error.log /home/tester/.sfpanel-ai/uploads/aaaaaaaaaaa1/20261114-093005-trace.txt '])
    expect(t.uploaded).toEqual(['aaaaaaaaaaa1:error.log', 'aaaaaaaaaaa1:trace.txt'])
  })

  test('a file the panel refuses is named, and nothing is typed', async ({ page }) => {
    const t = await openTerminal(page, AGENT.id, { uploadFails: 'FILE_TOO_LARGE' })
    await paste(page, 'image')
    await expect(page.getByText('image.png: larger than 25 MB')).toBeVisible()
    expect(t.input).toEqual([])
  })

  test('a panel from before attachments is left as it was', async ({ page }) => {
    const t = await openTerminal(page, AGENT.id, { uploads: false })
    await paste(page, 'image')
    await drag(page, 'dragenter', ['error.log'])
    await page.waitForTimeout(300)
    await expect(page.locator('[data-terminal-drop]')).toHaveCount(0)
    expect(t.uploaded).toEqual([])
  })
})

// A flick as a finger makes one: the events' own clock a 16 ms frame apart,
// lifted mid-motion.
async function flick(page: Page, from: { x: number; y: number }, dx: number) {
  const client = await page.context().newCDPSession(page)
  const t0 = Date.now() / 1000
  await client.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [from], timestamp: t0 })
  for (let i = 1; i <= 6; i++) {
    await client.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: from.x + dx * i / 6, y: from.y }], timestamp: t0 + 0.016 * i })
    await page.waitForTimeout(16)
  }
  await client.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [], timestamp: t0 + 0.016 * 7 })
  await client.detach()
}

// The browser keeps a flick it was not told about as a fling in progress for a
// second or two, and the tap that stops a fling is not a click.
for (const where of ['the keys', 'the terminal'] as const) {
  test(`the key pressed right after a sideways flick across ${where} is pressed`, async ({ page }) => {
    const t = await openTerminal(page)
    const box = (await (where === 'the keys' ? t.area.locator('[data-terminal-keys]') : t.terminal).boundingBox())!
    await flick(page, { x: box.x + 300, y: box.y + box.height / 2 }, -200)
    // Across the keys it turned the page: the first key is now another.
    const first = t.area.locator('[data-terminal-keys] button').first()
    const label = await first.textContent()
    await page.waitForTimeout(100)
    await first.tap()
    await expect.poll(() => t.input.at(-1)).toBe(where === 'the keys' ? '1' : '\x1b')
    expect(label).toBe(where === 'the keys' ? '1' : 'Esc')
  })
}

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
