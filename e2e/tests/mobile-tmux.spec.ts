import { test, expect, type Page, type CDPSession } from '@playwright/test'
import { spawn, spawnSync, type ChildProcess } from 'node:child_process'
import { resolve } from 'node:path'
import { readFileSync } from 'node:fs'

test.use({ viewport: { width: 412, height: 860 }, hasTouch: true })

// A Codex stand-in: full screen, SGR mouse, one row per wheel report.
const PAGER = `python3 -I ${resolve(__dirname, '../fixtures/wheel-pager.py')}`
const PANEL_JS = resolve(__dirname, '../../android/app/src/main/assets/panel.js')

const needsTmux = () => test.skip(spawnSync('tmux', ['-V']).status !== 0 || spawnSync('python3', ['--version']).status !== 0,
  'Requires tmux and Python 3 for the isolated PTY fixture')

// The terminal page against a real tmux (fixtures/tmux-pty.py) running `cmd`.
// With `touch`, the bundle says so and the fixture binds it as the panel's
// attach does; without, this is a panel from before touch scrolling.
async function openTerminal(page: Page, peers: ChildProcess[], { touch, cmd, android }: { touch?: boolean; cmd?: string; android: boolean }) {
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname
    let data: unknown = {}
    if (path.endsWith('/auth/setup-status')) data = { setup_required: false }
    else if (path.endsWith('/auth/ws-ticket')) data = { ticket: 'fixture-ticket' }
    else if (path.endsWith('/cluster/status')) data = { enabled: false }
    else if (path.endsWith('/ai/sessions')) data = [{ id: 'abcdef123456', title: 'History test', tool: 'codex', run_as: 'tester', cwd: '/tmp', state: 'working', persistence: 'service', attached: true }]
    else if (path.endsWith('/ai/tools')) data = { tmux: { installed: true, supported: true, version: '3.4', min_version: '3.2' }, systemd_run: true, accounts: ['tester'], panel_account: 'tester', account: 'tester', tools: {}, ...(touch ? { touch_scroll: true } : {}) }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\/ai\/attach/, socket => {
    const peer = spawn('python3', [resolve(__dirname, '../fixtures/tmux-pty.py')], {
      env: { ...process.env, ...(cmd ? { SFPANEL_TEST_TMUX_CMD: cmd } : {}), ...(touch ? { SFPANEL_TEST_TOUCH_SCROLL: '1' } : {}) },
    })
    peers.push(peer)
    peer.stdout.on('data', chunk => socket.send(chunk))
    socket.onMessage(message => {
      const text = message.toString()
      const value = text.startsWith('{') && JSON.parse(text).type === 'resize'
        ? text : JSON.stringify({ input: Buffer.from(message).toString('base64') })
      peer.stdin.write(value + '\n')
    })
    socket.onClose(() => peer.kill())
  })
  await page.goto('/terminal')
  const terminal = page.locator('[data-terminal-session="active"]')
  await expect(terminal).toBeVisible()
  // Match native evaluateJavascript while leaving the server CSP intact.
  if (android) await page.evaluate(readFileSync(PANEL_JS, 'utf8'))
  const row = (n: number) => terminal.evaluate((el, n) => {
    const t = (el as any).__termRef.current
    return t.buffer.active.getLine(n)?.translateToString(true) ?? ''
  }, n)
  // The pager's own count line, wherever the pane's last row falls.
  const pagerStatus = () => terminal.evaluate(el => {
    const t = (el as any).__termRef.current
    for (let i = 0; i < t.rows; i++) {
      const text = t.buffer.active.getLine(i)?.translateToString(true) ?? ''
      if (text.startsWith('wheel=')) return text
    }
    return ''
  })
  // Both fixtures print the numbers 1-300, newest at the bottom: the top row
  // says how far back the view is.
  const firstLine = async () => Number.parseInt(await row(0), 10)
  await expect.poll(firstLine).toBeGreaterThan(200)
  // Settled: the app's script resizes the terminal (it hides the web key
  // bar), and tmux redraws for the new size after the fit's debounce.
  let seen = ''
  await expect.poll(async () => {
    const now = `${await firstLine()}/${await terminal.evaluate(el => (el as any).__termRef.current.rows)}`
    const still = now === seen
    seen = now
    return still
  }, { intervals: [300] }).toBe(true)
  const geometry = await terminal.evaluate(el => {
    const t = (el as any).__termRef.current
    return { cell: el.querySelector('.xterm-screen')!.clientHeight / t.rows, rows: t.rows }
  })
  return { terminal, firstLine, pagerStatus, ...geometry }
}

// A finger dragged by dy px in steps of `step` px, a 16 ms frame apart by the
// events' own clock — the one the release velocity is read from; through CDP
// the moves themselves arrive a frame or two apart, which would read as a slow
// drag. With rest, the finger stops 150 ms before lifting, as a reading finger
// does, so nothing flings; without, it lifts mid-motion: a flick.
async function drag(page: Page, client: CDPSession, x: number, y: number, dy: number, step: number, rest = true) {
  const t0 = Date.now() / 1000
  let frame = 0
  const at = () => t0 + 0.016 * frame++
  await client.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }], timestamp: at() })
  for (let moved = 0; moved < Math.abs(dy);) {
    moved = Math.min(Math.abs(dy), moved + step)
    await client.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x, y: y + Math.sign(dy) * moved }], timestamp: at() })
    if (rest) await page.waitForTimeout(16)
  }
  await client.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [], timestamp: rest ? at() + 0.15 : at() })
}

for (const android of [false, true]) {
  test(`terminal history visibly scrolls through real tmux (${android ? 'Android' : 'web'})`, async ({ page }) => {
    needsTmux()
    const peers: ChildProcess[] = []
    try {
      const { terminal, firstLine } = await openTerminal(page, peers, { android })
      const before = await firstLine()
      const box = (await terminal.boundingBox())!
      const client = await page.context().newCDPSession(page)
      const swipe = async (delta: number) => {
        const x = box.x + box.width / 2, y = box.y + box.height / 2
        await client.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] })
        for (let i = 1; i <= 12; i++) {
          await client.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x, y: y + delta * i / 12 }] })
          await page.waitForTimeout(20)
        }
        await client.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
      }
      await swipe(140)
      await expect.poll(firstLine).toBeLessThan(before)
      await swipe(-200)
      await expect.poll(firstLine).toBe(before)
      await client.detach()
    } finally {
      for (const peer of peers) peer.kill()
    }
  })

  // Ten lines of finger move the view ten lines, in tmux's history and in a
  // program that scrolls itself. Before the Shift wheel pairs, xterm made each
  // touch move one wheel report, and under 50 px 0.3 of one: Codex moved two
  // lines for ten, and tmux nothing for three, then five at a time.
  for (const receiver of ['history', 'program'] as const) {
    test(`a finger's line is one line of ${receiver} (${android ? 'Android' : 'web'})`, async ({ page }) => {
      needsTmux()
      const peers: ChildProcess[] = []
      try {
        const { terminal, firstLine, pagerStatus, cell } = await openTerminal(page, peers, { touch: true, cmd: receiver === 'program' ? PAGER : undefined, android })
        const box = (await terminal.boundingBox())!
        const client = await page.context().newCDPSession(page)
        const x = box.x + box.width / 2, y = box.y + box.height * 0.3
        let at = await firstLine()
        for (const step of [3, 45]) {
          await drag(page, client, x, y, 10 * cell, step)
          await expect.poll(firstLine).toBe(at - 10)
          at -= 10
        }
        await drag(page, client, x, y + 10 * cell, -10 * cell, 7)
        await expect.poll(firstLine).toBe(at + 10)
        if (receiver === 'program') {
          // Every Shift report stayed in tmux: the program saw plain wheel reports only.
          await expect.poll(pagerStatus).toBe("wheel=30 other=b''")
        }
        await client.detach()
      } finally {
        for (const peer of peers) peer.kill()
      }
    })
  }
}

// A flick goes on after the finger lifts and comes to rest by itself.
test('a flick carries the program on after the finger lifts (Android)', async ({ page }) => {
  needsTmux()
  const peers: ChildProcess[] = []
  try {
    const { terminal, firstLine, cell } = await openTerminal(page, peers, { touch: true, cmd: PAGER, android: true })
    const box = (await terminal.boundingBox())!
    const client = await page.context().newCDPSession(page)
    const before = await firstLine()
    await drag(page, client, box.x + box.width / 2, box.y + box.height * 0.2, 8 * cell, 32, false)
    const atLift = await firstLine()
    await expect.poll(firstLine).toBeLessThan(atLift - 10)
    let last = -1
    await expect.poll(async () => { const now = await firstLine(); const still = now === last; last = now; return still }, { intervals: [400] }).toBe(true)
    expect(before - last).toBeGreaterThan(8 + 10)
    await client.detach()
  } finally {
    for (const peer of peers) peer.kill()
  }
})

// The page buttons move a page, less one line of overlap. The web key bar
// called scrollPages, which a tmux session's alternate buffer cannot do at
// all; the app's dock dispatches one wheel event of the page's pixels, which
// xterm made one report — one line of Codex.
for (const receiver of ['history', 'program'] as const) {
  test(`the key bar's page buttons move a page of ${receiver}`, async ({ page }) => {
    needsTmux()
    const peers: ChildProcess[] = []
    try {
      const { firstLine, rows } = await openTerminal(page, peers, { touch: true, cmd: receiver === 'program' ? PAGER : undefined, android: false })
      const at = await firstLine()
      await page.getByRole('button', { name: 'Scroll up', exact: true }).first().click()
      await expect.poll(firstLine).toBe(at - (rows - 1))
      await page.getByRole('button', { name: 'Scroll down', exact: true }).first().click()
      await expect.poll(firstLine).toBe(at)
    } finally {
      for (const peer of peers) peer.kill()
    }
  })

  test(`the app's page buttons move a page of ${receiver}`, async ({ page }) => {
    needsTmux()
    const peers: ChildProcess[] = []
    try {
      const { firstLine, rows } = await openTerminal(page, peers, { touch: true, cmd: receiver === 'program' ? PAGER : undefined, android: true })
      const at = await firstLine()
      // What MainActivity.scroll(-1) runs: a page is the session element's height.
      await page.evaluate(() => {
        const e = document.querySelector('[data-terminal-session="active"]') as any
        ;(window as any).__sfpanelScrollTerminal(e, -1 * e.clientHeight)
      })
      await expect.poll(firstLine).toBe(at - (rows - 1))
    } finally {
      for (const peer of peers) peer.kill()
    }
  })
}
