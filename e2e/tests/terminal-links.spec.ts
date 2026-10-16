import { test, expect, type Page } from '@playwright/test'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// A link in terminal output did nothing when tapped in the Android app. xterm's
// web-links addon opened an empty window and set its address afterwards; the
// app's WebView has no second window, so that window.open() handed back the
// page itself and spent the tap's user activation, and the app refused the
// address that followed as a navigation the user had not started. The address
// now goes in the window.open call itself. REST and WebSocket are fixtures.
test.use({ viewport: { width: 412, height: 860 }, hasTouch: true, locale: 'en-US' })

const LINK = 'https://example.com/docs?from=terminal'

// Opens the terminal with LINK on its first row and returns a point inside it.
async function openTerminal(page: Page) {
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'links-test-token')
    localStorage.setItem('i18nextLng', 'en')
    // Record what each window.open call is given, then let it through.
    const calls: unknown[][] = []
    const open = window.open.bind(window)
    Object.assign(window, { __openCalls: calls })
    window.open = (...args: Parameters<typeof window.open>) => { calls.push(args); return open(...args) }
  })
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname
    let data: unknown = {}
    if (path.endsWith('/auth/setup-status')) data = { setup_required: false }
    else if (path.endsWith('/auth/ws-ticket')) data = { ticket: 'links-test-ticket' }
    else if (path.endsWith('/cluster/status')) data = { enabled: false }
    else if (path.endsWith('/ai/sessions')) data = [{ id: 'abcdef123456', title: 'Shell', tool: 'shell', run_as: 'tester', cwd: '/tmp', state: 'working', persistence: 'service', attached: true, created_at: '2026-09-15T00:00:00Z' }]
    else if (path.endsWith('/ai/tools')) data = { tmux: { installed: true, supported: true, version: '3.4', min_version: '3.2' }, systemd_run: true, accounts: ['tester'], panel_account: 'tester', account: 'tester', tools: {} }
    else if (path.endsWith('/terminal/sessions')) data = { sessions: [] }
    else if (path.endsWith('/terminal/info')) data = { shell_user: 'tester', hostname: 'fixture', home: '/home/tester', shell: '/bin/bash', is_root: false }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.context().route('https://example.com/**', route => route.fulfill({ contentType: 'text/plain', body: 'outside' }))
  await page.goto('/terminal')
  const terminal = page.locator('[data-terminal-session="active"]')
  await expect.poll(() => terminal.evaluate(el => Boolean((el as HTMLElement & { __termRef?: { current?: unknown } }).__termRef?.current))).toBe(true)
  // Drawn the way tmux leaves the terminal: alternate screen, mouse tracking on.
  await terminal.evaluate((el, link) => new Promise<void>(done => {
    const term = (el as HTMLElement & { __termRef: { current: { write: (s: string, cb: () => void) => void } } }).__termRef.current
    term.write('\x1b[?1049h\x1b[?1000h\x1b[?1006h\x1b[H\x1b[2J' + 'docs: ' + link + '\r\n', done)
  }), LINK)
  return terminal.evaluate(el => {
    const term = (el as HTMLElement & { __termRef: { current: { cols: number; rows: number } } }).__termRef.current
    const r = el.querySelector('.xterm-screen')!.getBoundingClientRect()
    return { x: r.left + 12.5 * (r.width / term.cols), y: r.top + 0.5 * (r.height / term.rows) }
  })
}

const openCalls = (page: Page) => page.evaluate(() => (window as unknown as { __openCalls: unknown[][] }).__openCalls)

test('in the Android app a tapped link hands its address to window.open', async ({ page }) => {
  const at = await openTerminal(page)
  await page.evaluate(readFileSync(resolve(__dirname, '../../android/app/src/main/assets/panel.js'), 'utf8'))
  await page.touchscreen.tap(at.x, at.y)
  await expect.poll(() => openCalls(page)).toEqual([[LINK, '_blank', 'noopener,noreferrer']])
  expect(new URL(page.url()).pathname).toBe('/terminal')
})

test('in a browser a clicked link opens in a new tab and the panel stays', async ({ page, context }) => {
  const at = await openTerminal(page)
  const tab = context.waitForEvent('page')
  await page.mouse.click(at.x, at.y)
  const opened = await tab
  await expect.poll(() => opened.url()).toBe(LINK)
  expect(new URL(page.url()).pathname).toBe('/terminal')
})
