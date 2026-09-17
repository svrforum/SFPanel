import { test, expect } from '@playwright/test'
import { resolve } from 'node:path'
import { readFileSync } from 'node:fs'

// These tests never contact a host shell: REST and WebSocket are both fixtures.
// Run against a frontend dev server or a built panel; no admin seed is needed.
test.use({ viewport: { width: 412, height: 860 }, hasTouch: true, locale: 'en-US' })

for (const path of ['/terminal']) {
  test(`${path}: mobile modifiers, history and keyboard resizing`, async ({ page }) => {
    const input: string[] = []
    await page.addInitScript(() => {
      sessionStorage.setItem('token', 'mobile-test-token')
      localStorage.setItem('i18nextLng', 'en')
    })
    await page.route('**/api/v1/**', async route => {
      const url = new URL(route.request().url())
      let data: unknown = {}
      if (url.pathname.endsWith('/auth/setup-status')) data = { setup_required: false }
      else if (url.pathname.endsWith('/auth/ws-ticket')) data = { ticket: 'mobile-test-ticket' }
      else if (url.pathname.endsWith('/cluster/status')) data = { enabled: false }
      else if (url.pathname.endsWith('/system/overview')) data = { version: 'test' }
      else if (url.pathname.endsWith('/ai/sessions')) data = [{ id: 'abcdef123456', title: 'Mobile coding', tool: 'codex', run_as: 'tester', cwd: '/tmp', state: 'working', persistence: 'service', attached: true, created_at: '2026-09-15T00:00:00Z' }]
      else if (url.pathname.endsWith('/ai/tools')) data = { tmux: { installed: true, supported: true, version: '3.4', min_version: '3.2' }, systemd_run: true, accounts: ['tester'], panel_account: 'tester', account: 'tester', tools: {} }
      else if (url.pathname.endsWith('/terminal/info')) data = { user: 'tester', hostname: 'fixture', shell: '/bin/bash' }
      await route.fulfill({ json: { success: true, data } })
    })
    await page.routeWebSocket(/\/ws\//, socket => {
      socket.onMessage(message => {
        const value = typeof message === 'string' ? message : message.toString('utf8')
        if (!value.startsWith('{')) input.push(value)
      })
    })
    await page.goto(path)
    const terminal = page.locator('[data-terminal-session="active"]')
    await expect(terminal).toBeVisible()
    await expect.poll(() => terminal.evaluate(el => {
      const session = el as HTMLElement & { __wsRef?: { current?: WebSocket } }
      return session.__wsRef?.current?.readyState
    })).toBe(1)
    const bar = page.locator('[data-mobile-terminal-bar]')
    const shift = bar.getByRole('button', { name: 'Toggle shift for the next key' })
    await shift.tap()
    await expect(shift).toHaveAttribute('aria-pressed', 'true')
    await bar.getByRole('button', { name: 'Tab', exact: true }).tap()
    await expect.poll(() => input.at(-1)).toBe('\x1b[Z')
    await expect(shift).toHaveAttribute('aria-pressed', 'false')
    await shift.tap()
    await bar.getByRole('button', { name: 'Enter', exact: true }).tap()
    await expect.poll(() => input.at(-1)).toBe('\x1b[13;2u')

    await bar.getByRole('button', { name: 'Toggle ctrl for the next key' }).tap()
    await page.keyboard.press('c')
    await expect.poll(() => input.at(-1)).toBe('\x03')
    await expect(bar.getByRole('button', { name: 'Toggle ctrl for the next key' })).toHaveAttribute('aria-pressed', 'false')

    await terminal.evaluate(el => {
      const session = el as HTMLElement & { __termRef?: { current?: { write: (s: string) => void } } }
      session.__termRef?.current?.write(Array.from({ length: 300 }, (_, i) => `output line ${i}\r\n`).join(''))
    })
    const swipe = async (delta: number) => {
      const box = (await terminal.boundingBox())!
      const client = await page.context().newCDPSession(page)
      const x = box.x + box.width / 2, y = box.y + box.height / 2
      await client.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] })
      for (let i = 1; i <= 6; i++) await client.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x, y: y + delta * i / 6 }] })
      await client.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
      await client.detach()
    }
    const scrollPosition = () => terminal.evaluate(el => {
      const session = el as HTMLElement & { __termRef?: { current?: { buffer: { active: { viewportY: number; baseY: number } } } } }
      const b = session.__termRef!.current!.buffer.active
      return { viewport: b.viewportY, bottom: b.baseY }
    })
    await expect.poll(async () => (await scrollPosition()).bottom).toBeGreaterThan(100)
    await bar.getByRole('button', { name: 'Scroll up', exact: true }).tap()
    await expect.poll(async () => { const p = await scrollPosition(); return p.viewport < p.bottom }).toBe(true)
    // Keyboard-sized viewport must preserve the operator's reading position.
    await page.setViewportSize({ width: 412, height: 560 })
    await expect.poll(async () => { const p = await scrollPosition(); return p.viewport < p.bottom }).toBe(true)
    await bar.getByRole('button', { name: 'Latest output', exact: true }).tap()
    await expect.poll(async () => { const p = await scrollPosition(); return p.viewport === p.bottom }).toBe(true)
    // A clipping frame must not become an extra scroll container when the
    // WebView brings its focused textarea into view above the keyboard.
    const frame = page.locator('[data-ai-workspace] > .overflow-clip')
    expect(await frame.evaluate(el => { el.scrollTop = 100; return el.scrollTop })).toBe(0)
    for (const button of await bar.getByRole('button').all()) {
      const box = await button.boundingBox()
      expect(box?.height).toBeGreaterThanOrEqual(48)
    }
    // Match WebView.evaluateJavascript; a DOM script tag is blocked by the
    // production CSP and does not model how the Android asset is installed.
    await page.evaluate(readFileSync(resolve(__dirname, '../../android/app/src/main/assets/panel.js'), 'utf8'))
    // Compact mode (the Android page script) hides the first child of the
    // workspace — the rail aside, already hidden on a phone — and the key bar;
    // the pane must grow. Opening the tools panel through the html attribute
    // must still reach the account control.
    const aside = page.locator('[data-ai-workspace] > :first-child')
    await expect(aside).toBeHidden()
    await expect(bar).toBeHidden()
    await expect.poll(async () => (await terminal.boundingBox())?.height ?? 0).toBeGreaterThan(450)
    await page.evaluate(() => document.documentElement.setAttribute('data-ai-tools-open', ''))
    await expect(page.getByRole('combobox', { name: 'Run as' })).toBeVisible()
    await page.evaluate(() => document.documentElement.removeAttribute('data-ai-tools-open'))
    await expect(page.getByRole('combobox', { name: 'Run as' })).toBeHidden()
    await expect(page.locator('[role=tab][aria-selected=true]')).toHaveCount(0)
    await page.getByRole('button', { name: 'Sessions' }).tap()
    await expect(page.getByRole('tab', { name: /Mobile coding/ })).toBeVisible()
    await expect(page.locator('[role=tab][aria-selected=true]')).toHaveCount(1)
    await page.keyboard.press('Escape')
    await expect(page.locator('[role=tab][aria-selected=true]')).toHaveCount(0)
    // A real touch drag, not the native history buttons, must move scrollback.
    await swipe(100)
    await expect.poll(async () => { const p = await scrollPosition(); return p.viewport < p.bottom }).toBe(true)
    await swipe(-100)
    // Full-screen CLI mode has no scrollback: forward wheel input through xterm.
    await terminal.evaluate(async el => {
      const t = (el as any).__termRef.current
      await new Promise<void>(resolve => t.write('\x1b[?1049h\x1b[?1000h\x1b[?1006h', resolve))
    })
    const beforeWheel = input.length
    await swipe(100)
    await expect.poll(() => input.slice(beforeWheel).some(value => /\x1b\[<64;/.test(value))).toBe(true)
    // Mouse-disabled alternate buffers use xterm's arrow-key fallback.
    await terminal.evaluate(async el => {
      await new Promise<void>(resolve => (el as any).__termRef.current.write('\x1b[?1000l\x1b[?1006l', resolve))
    })
    const beforeArrow = input.length
    await swipe(-100)
    await expect.poll(() => input.slice(beforeArrow).some(value => value.includes('\x1b[B'))).toBe(true)

  })
}

test('/ai lands on /terminal', async ({ page }) => {
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'mobile-test-token')
    localStorage.setItem('i18nextLng', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const url = new URL(route.request().url())
    let data: unknown = {}
    if (url.pathname.endsWith('/auth/setup-status')) data = { setup_required: false }
    else if (url.pathname.endsWith('/cluster/status')) data = { enabled: false }
    else if (url.pathname.endsWith('/system/overview')) data = { version: 'test' }
    else if (url.pathname.endsWith('/ai/sessions')) data = []
    else if (url.pathname.endsWith('/ai/tools')) data = { tmux: { installed: true, supported: true, version: '3.4', min_version: '3.2' }, systemd_run: true, accounts: ['tester'], panel_account: 'tester', account: 'tester', tools: {} }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.goto('/ai')
  await expect(page).toHaveURL(/\/terminal$/)
  await expect(page.getByText('No open sessions')).toBeVisible()
})

test('without tmux the page falls back to a temporary PTY shell and says so', async ({ page }) => {
  const sockets: string[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'mobile-test-token')
    localStorage.setItem('i18nextLng', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const url = new URL(route.request().url())
    let data: unknown = {}
    if (url.pathname.endsWith('/auth/setup-status')) data = { setup_required: false }
    else if (url.pathname.endsWith('/auth/ws-ticket')) data = { ticket: 'mobile-test-ticket' }
    else if (url.pathname.endsWith('/cluster/status')) data = { enabled: false }
    else if (url.pathname.endsWith('/system/overview')) data = { version: 'test' }
    else if (url.pathname.endsWith('/ai/sessions')) data = []
    else if (url.pathname.endsWith('/ai/tools')) data = { tmux: { installed: false, supported: false, version: '', min_version: '3.2' }, systemd_run: true, accounts: ['tester'], panel_account: 'tester', account: 'tester', tools: {} }
    else if (url.pathname.endsWith('/terminal/sessions')) data = { sessions: [] }
    else if (url.pathname.endsWith('/terminal/info')) data = { shell_user: 'tester', hostname: 'fixture', home: '/home/tester', shell: '/bin/bash', is_root: false }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, socket => { sockets.push(socket.url()) })
  await page.goto('/terminal')
  await expect(page.getByText(/Without tmux, sessions live in this browser only/)).toBeVisible()
  await page.getByRole('button', { name: 'Sessions' }).tap()
  await expect(page.getByRole('tab')).toHaveCount(0)
  await page.getByRole('button', { name: 'New temporary session' }).tap()
  const terminal = page.locator('[data-terminal-session="active"]')
  await expect(terminal).toBeVisible()
  await expect.poll(() => sockets.some((u) => u.includes('/ws/terminal?'))).toBe(true)
  await expect(page.getByRole('button', { name: 'Sessions' })).toBeVisible()
  await page.getByRole('button', { name: 'Sessions' }).tap()
  await expect(page.getByRole('tab', { name: /Terminal 1/ })).toBeVisible()
  await expect(page.getByText('Temporary sessions')).toBeVisible()
})
