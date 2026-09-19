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
    // A newline, not the CSI-u form of shift+enter: tmux collapses that one to
    // a carriage return, which sends the message the key exists to break.
    await expect.poll(() => input.at(-1)).toBe('\n')

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
    // Compact mode (the Android page script) hides the key bar and the pane
    // must grow into the freed height. The rail is the workspace's first
    // child and stays hidden on a phone — panel.js hides whatever that first
    // child is, so a first child that is not the rail would hide the pane
    // instead. Assert the identity, not just "something is hidden".
    const aside = page.locator('[data-ai-workspace] > aside:first-child')
    await expect(aside).toHaveCount(1)
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
  // The empty pane must promise what this mode can keep: a temporary shell,
  // not the tmux "survives the browser and a panel restart" sentence.
  await expect(page.getByText(/Start a temporary shell/)).toBeVisible()
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

test('a browser upgraded from the old terminal page lands on a live session, not a resurrected temporary shell', async ({ page }) => {
  const sockets: string[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'mobile-test-token')
    localStorage.setItem('i18nextLng', 'en')
    // What the PTY-only page wrote: a tab it created for every visitor, and
    // its bare id as the active tab.
    localStorage.setItem('sfpanel_terminal_tabs:local', JSON.stringify([{ id: 'term-1', title: 'Terminal 1' }]))
    localStorage.setItem('sfpanel_terminal_active:local', 'term-1')
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
    else if (url.pathname.endsWith('/terminal/sessions')) data = { sessions: [] }
    else if (url.pathname.endsWith('/terminal/info')) data = { shell_user: 'tester', hostname: 'fixture', home: '/home/tester', shell: '/bin/bash', is_root: false }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, socket => { sockets.push(new URL(socket.url()).pathname) })
  await page.goto('/terminal')
  await expect(page.locator('[data-terminal-session="active"]')).toBeVisible()
  // The live tmux session is what opens, and the dead tab is gone from the rail.
  await expect(page.locator('header').getByRole('button', { name: 'Mobile coding' })).toBeVisible()
  await expect.poll(() => sockets.some((p) => p === '/ws/ai/attach')).toBe(true)
  expect(sockets).not.toContain('/ws/terminal')
  await page.getByRole('button', { name: 'Sessions', exact: true }).tap()
  await expect(page.getByRole('tab', { name: /Mobile coding/ })).toBeVisible()
  await expect(page.getByText('Temporary sessions')).toBeHidden()
  // The old page's tab list is deleted on load, not merely ignored: nothing
  // reads a temporary tab back from storage any more.
  expect(await page.evaluate(() => localStorage.getItem('sfpanel_terminal_tabs:local'))).toBeNull()
})

test("without tmux the operator's server-side shells come back as tabs on reload", async ({ page }) => {
  // Ruling 3: in fallback mode the operator's whole workflow is PTY sessions,
  // so what survives a reload is what the SERVER still has — never a tab read
  // back from localStorage, which this browser does not even write.
  //
  // The fixture id is term-1 because that is the id the server really reports:
  // a PTY session is created by the tab that connects to it, so its id is one
  // this page's own generator handed out. The generator is module state and
  // restarts at zero on every load, so an adopted tab has to push it forward —
  // otherwise the next temporary shell is handed an id that is already a tab
  // and the operator gets a second row onto the same shell, not a new one.
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
    else if (url.pathname.endsWith('/terminal/sessions')) data = { sessions: [{ session_id: 'term-1', last_use: new Date(0).toISOString(), attached: false, reader_count: 0 }] }
    else if (url.pathname.endsWith('/terminal/info')) data = { shell_user: 'tester', hostname: 'fixture', home: '/home/tester', shell: '/bin/bash', is_root: false }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, socket => { sockets.push(socket.url()) })
  await page.goto('/terminal')
  await page.getByRole('button', { name: 'Sessions', exact: true }).tap()
  await expect(page.getByText('Temporary sessions')).toBeVisible()
  await expect(page.getByRole('tab', { name: /term-1/ })).toBeVisible()
  // One more temporary shell must be a new shell: its own row, its own id and
  // its own socket. Asserting the ids and not just the row count is the point
  // — a generator that collided would still render two rows, both pointing at
  // term-1, both selected, and closing either would remove both.
  await page.getByRole('button', { name: 'New temporary session' }).tap()
  await page.getByRole('button', { name: 'Sessions', exact: true }).tap()
  await expect.poll(() => page.locator('[data-rail-key]').evaluateAll(els => els.map(el => el.getAttribute('data-rail-key'))))
    .toEqual(['pty:term-1', 'pty:term-2'])
  await expect(page.locator('[role=tab][aria-selected=true]')).toHaveCount(1)
  await expect.poll(() => sockets.map(u => new URL(u).searchParams.get('session_id')).filter(Boolean).sort())
    .toEqual(['term-1', 'term-2'])
})

test('a temporary session just closed is offered under Reattach: closing a tab does not end the server PTY', async ({ page }) => {
  // The server registers a PTY session when its socket connects, so the list
  // only reports term-1 after the tab has been opened — which is why the page
  // has to re-ask when the tab count changes, not only when the temporary
  // group first appears (in fallback mode it never goes away).
  let registered = false
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
    else if (url.pathname.endsWith('/terminal/sessions')) data = { sessions: registered ? [{ session_id: 'term-1', last_use: '2026-09-18T00:00:00Z' }, { session_id: 'term-3', last_use: '2026-09-18T00:00:00Z' }] : [] }
    else if (url.pathname.endsWith('/terminal/info')) data = { shell_user: 'tester', hostname: 'fixture', home: '/home/tester', shell: '/bin/bash', is_root: false }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, socket => { if (socket.url().includes('/ws/terminal?')) registered = true })
  await page.goto('/terminal')
  await expect(page.getByText(/Start a temporary shell/)).toBeVisible()
  await page.getByRole('button', { name: 'Sessions' }).tap()
  await page.getByRole('button', { name: 'New temporary session' }).tap()
  await expect(page.locator('[data-terminal-session="active"]')).toBeVisible()
  await page.locator('[data-session-menu]').tap()
  await page.getByRole('menuitem', { name: 'Close' }).click()
  await page.getByRole('button', { name: 'Sessions' }).tap()
  // The shell is still alive on the host for five minutes, so it belongs here.
  await expect(page.getByText('Reattach', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: /term-1/ })).toBeVisible()
  // term-3 is a shell this browser never created — another device, or an
  // earlier page life. Reattaching it has to push the id generator past 3:
  // the counter is module state this page raised to 1 by opening term-1, so
  // without that the next two temporary shells walk straight into term-3 and
  // the operator gets a second row onto a shell that is already open.
  await expect(page.getByRole('button', { name: /term-3/ })).toBeVisible()
  await page.getByRole('button', { name: /term-3/ }).tap()
  await page.getByRole('button', { name: 'Sessions' }).tap()
  await page.getByRole('button', { name: 'New temporary session' }).tap()
  await page.getByRole('button', { name: 'Sessions' }).tap()
  await expect.poll(() => page.locator('[data-rail-key]').evaluateAll(els => els.map(el => el.getAttribute('data-rail-key'))))
    .toEqual(['pty:term-3', 'pty:term-4'])
})

test('with tmux a temporary shell is a NEW shell: the id generator starts past the sessions the server already has', async ({ page }) => {
  // tmux mode never adopts the server's PTY sessions as tabs — a temporary
  // shell is a door the operator opens on purpose — but its ids come from the
  // same `term-N` generator, module state that restarts at zero on every page
  // load. The server hands an id it already knows straight back as that live
  // shell, so without the boot fetch the operator asking for a temporary shell
  // lands inside the one an earlier page load, or another device signed in as
  // the same operator, left running.
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
    else if (url.pathname.endsWith('/ai/tools')) data = { tmux: { installed: true, supported: true, version: '3.4', min_version: '3.2' }, systemd_run: true, accounts: ['tester'], panel_account: 'tester', account: 'tester', tools: {} }
    // The shell an earlier page load left behind: still alive, and not a tab
    // in this browser.
    else if (url.pathname.endsWith('/terminal/sessions')) data = { sessions: [{ session_id: 'term-1', last_use: '2026-09-18T00:00:00Z', attached: false, reader_count: 0 }] }
    else if (url.pathname.endsWith('/terminal/info')) data = { shell_user: 'tester', hostname: 'fixture', home: '/home/tester', shell: '/bin/bash', is_root: false }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, socket => { sockets.push(socket.url()) })
  await page.goto('/terminal')
  await expect(page.getByText('No open sessions')).toBeVisible()
  await page.getByRole('button', { name: 'Sessions', exact: true }).tap()
  await page.getByRole('button', { name: 'Tools & accounts' }).tap()
  await page.getByRole('button', { name: 'Open a temporary shell without tmux' }).tap()
  await expect(page.locator('[data-terminal-session="active"]')).toBeVisible()
  await page.getByRole('button', { name: 'Sessions', exact: true }).tap()
  await expect.poll(() => page.locator('[data-rail-key]').evaluateAll(els => els.map(el => el.getAttribute('data-rail-key'))))
    .toEqual(['pty:term-2'])
  // The id on the wire is what decides whether the server creates a shell or
  // returns one, so assert that and not only the row.
  await expect.poll(() => sockets.map(u => new URL(u).searchParams.get('session_id')).filter(Boolean))
    .toEqual(['term-2'])
})
