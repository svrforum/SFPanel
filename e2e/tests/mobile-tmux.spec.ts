import { test, expect, type Page, type CDPSession } from '@playwright/test'
import { spawn, spawnSync, type ChildProcess } from 'node:child_process'
import { join, resolve } from 'node:path'
import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'

test.use({ viewport: { width: 412, height: 860 }, hasTouch: true })

// A Codex stand-in: full screen, SGR mouse, one row per wheel report.
const PAGER = `python3 -I ${resolve(__dirname, '../fixtures/wheel-pager.py')}`
const PANEL_JS = resolve(__dirname, '../../android/app/src/main/assets/panel.js')

const needsTmux = () => test.skip(spawnSync('tmux', ['-V']).status !== 0 || spawnSync('python3', ['--version']).status !== 0,
  'Requires tmux and Python 3 for the isolated PTY fixture')

// The terminal page against a real tmux (fixtures/tmux-pty.py) running `cmd`.
// With `touch`, the bundle says so, the fixture binds it as the panel's attach
// does, and the scrollback route copies the fixture's own pane the way the
// panel's endpoint does (GET /ai/sessions/:id/scrollback); without, this is a
// panel from before touch scrolling.
async function openTerminal(page: Page, peers: ChildProcess[], dirs: string[], { touch, cmd, android, scrollback, numbered = true, latest = true }: {
  touch?: boolean; cmd?: string; android: boolean
  /** the panel takes the latest frame (GET /ai/tools attach_latest); false: a panel from before it */
  latest?: boolean
  /** the pane prints the numbers 1-300 (else: anything on its first row) */
  numbered?: boolean
  /** stands in for the endpoint: how long it takes, or that it fails */
  scrollback?: { delayMs?: number; fail?: boolean }
}) {
  const dir = mkdtempSync(join(tmpdir(), 'sfp-touch-'))
  dirs.push(dir)
  // The control frames the page sent, other than resizes.
  const frames: string[] = []
  const tmux = (...args: string[]) => spawnSync('tmux', ['-S', join(dir, 'socket'), ...args]).stdout?.toString() ?? ''
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
    else if (path.endsWith('/ai/tools')) data = { tmux: { installed: true, supported: true, version: '3.4', min_version: '3.2' }, systemd_run: true, accounts: ['tester'], panel_account: 'tester', account: 'tester', tools: {}, ...(touch ? { touch_scroll: true, touch_history: true } : {}), ...(touch && latest ? { attach_latest: true } : {}) }
    else if (path.endsWith('/scrollback')) {
      if (scrollback?.delayMs) await new Promise(done => setTimeout(done, scrollback.delayMs))
      if (scrollback?.fail) {
        await route.fulfill({ status: 500, json: { success: false, error: { code: 'INTERNAL_ERROR', message: 'x' } } })
        return
      }
      // As the endpoint answers (internal/feature/ai/attach.go SessionScrollback).
      const [alternate, mode, cols, rows] = tmux('display-message', '-p', '-t', 'test', '#{alternate_on} #{pane_in_mode} #{pane_width} #{pane_height}').trim().split(' ')
      const busy = alternate === '1' || mode === '1'
      data = {
        alternate: alternate === '1', copy_mode: mode === '1', cols: Number(cols), rows: Number(rows),
        text: busy ? '' : tmux('capture-pane', '-p', '-e', '-J', '-t', 'test', '-S', '-2000').replace(/\n$/, ''),
      }
    }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\/ai\/attach/, socket => {
    const peer = spawn('python3', [resolve(__dirname, '../fixtures/tmux-pty.py')], {
      env: { ...process.env, SFPANEL_TEST_TMUX_DIR: dir, ...(cmd ? { SFPANEL_TEST_TMUX_CMD: cmd } : {}), ...(touch ? { SFPANEL_TEST_TOUCH_SCROLL: '1' } : {}) },
    })
    peers.push(peer)
    peer.stdout.on('data', chunk => socket.send(chunk))
    socket.onMessage(message => {
      const text = message.toString()
      let control: { type?: string } = {}
      if (typeof message === 'string' && text.startsWith('{')) try { control = JSON.parse(text) } catch { /* input */ }
      if (control.type === 'latest') {
        frames.push('latest')
        // As the panel answers it (internal/feature/ai/attach.go leaveCopyMode);
        // a panel from before it writes the frame into the session as text.
        if (latest) {
          tmux('if-shell', '-F', '-t', 'test', '#{pane_in_mode}', 'send-keys -X -t test cancel')
          return
        }
      }
      const value = control.type === 'resize' ? text : JSON.stringify({ input: Buffer.from(message).toString('base64') })
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
  if (numbered) await expect.poll(firstLine).toBeGreaterThan(200)
  else await expect.poll(async () => (await row(0)).trim().length).toBeGreaterThan(0)
  // Settled: the app's script resizes the terminal (it hides the web key
  // bar), and tmux redraws for the new size after the fit's debounce.
  let seen = ''
  await expect.poll(async () => {
    const now = `${await firstLine()}/${await terminal.evaluate(el => (el as any).__termRef.current.rows)}`
    const still = now === seen
    seen = now
    return still
  }, { intervals: [300] }).toBe(true)
  // The history view: open or not, how many pixels back, the number on its
  // first whole row (row 0 is the extra one it slides in from the top), and
  // how far that row is slid — the part of a line between two lines.
  const view = () => page.evaluate(() => {
    const el = document.querySelector('[data-history-view]') as HTMLElement | null
    if (!el || el.style.visibility === 'hidden') return { open: false, offset: 0, top: NaN, shift: NaN }
    const rows = el.querySelectorAll('.xterm-rows > div')
    const shift = Number(/translateY\((-?[\d.]+)px\)/.exec((el.querySelector('.xterm') as HTMLElement).style.transform)?.[1])
    return { open: true, offset: Number(el.dataset.offset), top: Number.parseInt(rows[1]?.textContent ?? '', 10), shift }
  })
  const geometry = await terminal.evaluate(el => {
    const t = (el as any).__termRef.current
    return { cell: el.querySelector('.xterm-screen')!.clientHeight / t.rows, rows: t.rows }
  })
  return { terminal, firstLine, pagerStatus, view, tmux, frames, ...geometry }
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

// The tmux server goes first: the fixture's own kill-server runs only once it
// has wound down, by which time its socket would be gone with the directory,
// and the server — and a pager that never sees EOF — would live on.
const cleanup = (peers: ChildProcess[], dirs: string[]) => {
  for (const dir of dirs) spawnSync('tmux', ['-S', join(dir, 'socket'), 'kill-server'])
  for (const peer of peers) peer.kill()
  for (const dir of dirs) rmSync(dir, { recursive: true, force: true })
}

for (const android of [false, true]) {
  const where = android ? 'Android' : 'web'

  test(`terminal history visibly scrolls through real tmux (${where})`, async ({ page }) => {
    needsTmux()
    const peers: ChildProcess[] = [], dirs: string[] = []
    try {
      const { terminal, firstLine } = await openTerminal(page, peers, dirs, { android })
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
      cleanup(peers, dirs)
    }
  })

  // Through tmux every line was a round trip and a redraw; the history view
  // moves the page's own copy, a pixel for a pixel, and tmux does nothing.
  test(`a finger moves the history view a pixel at a time (${where})`, async ({ page }) => {
    needsTmux()
    const peers: ChildProcess[] = [], dirs: string[] = []
    try {
      const { terminal, firstLine, view, cell } = await openTerminal(page, peers, dirs, { touch: true, android })
      const live = await firstLine()
      const box = (await terminal.boundingBox())!
      const client = await page.context().newCDPSession(page)
      const x = box.x + box.width / 2, y = box.y + box.height * 0.3
      const back = Math.round(10.5 * cell)
      await drag(page, client, x, y, back, 3)
      // Ten lines and half of one: ten rows up, and the half slid in.
      await expect.poll(view).toMatchObject({ open: true, offset: back, top: live - 10, shift: -cell + (back - 10 * cell) })
      expect(await firstLine()).toBe(live)
      // Laid exactly over the live screen.
      expect(await page.evaluate(() => {
        const r = (el: Element) => { const b = el.getBoundingClientRect(); return [b.left, b.top, b.width, b.height].map(Math.round).join(',') }
        return r(document.querySelector('[data-history-view]')!) === r(document.querySelector('[data-terminal-session="active"] .xterm-screen')!)
      })).toBe(true)
      await drag(page, client, x, y + back, -4 * cell, 5)
      await expect.poll(view).toMatchObject({ open: true, offset: back - 4 * cell, top: live - 6 })
      // Up past the bottom: back on the live screen once the finger lifts.
      await drag(page, client, x, y + back, -back, 7)
      await expect.poll(view).toMatchObject({ open: false })
      expect(await firstLine()).toBe(live)
      await client.detach()
    } finally {
      cleanup(peers, dirs)
    }
  })

  // A program in the alternate screen (Codex outside inline mode) keeps its
  // history to itself: ten lines of finger move it ten lines through tmux.
  // Before the Shift wheel pairs, xterm made each touch move one wheel report,
  // and under 50 px 0.3 of one: Codex moved two lines for ten.
  test(`a finger's line is one line of a program in the alternate screen (${where})`, async ({ page }) => {
    needsTmux()
    const peers: ChildProcess[] = [], dirs: string[] = []
    try {
      const { terminal, firstLine, pagerStatus, view, cell } = await openTerminal(page, peers, dirs, { touch: true, cmd: PAGER, android })
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
      // Every Shift report stayed in tmux: the program saw plain wheel reports only.
      await expect.poll(pagerStatus).toBe("wheel=30 other=b''")
      expect((await view()).open).toBe(false)
      await client.detach()
    } finally {
      cleanup(peers, dirs)
    }
  })
}

// A flick goes on after the finger lifts and comes to rest by itself; flicked
// back down to the bottom, the view gives way to the live screen.
test('a flick carries the history view on, and back at the bottom it closes (Android)', async ({ page }) => {
  needsTmux()
  const peers: ChildProcess[] = [], dirs: string[] = []
  try {
    const { terminal, view, cell } = await openTerminal(page, peers, dirs, { touch: true, android: true })
    const box = (await terminal.boundingBox())!
    const client = await page.context().newCDPSession(page)
    const x = box.x + box.width / 2, y = box.y + box.height * 0.2
    await drag(page, client, x, y, 8 * cell, 32, false)
    await expect.poll(async () => (await view()).offset).toBeGreaterThan(8 * cell + 10 * cell)
    let last = -1
    await expect.poll(async () => { const now = (await view()).offset; const still = now === last; last = now; return still }, { intervals: [400] }).toBe(true)
    await drag(page, client, x, y + 10 * cell, -10 * cell, 40, false)
    await expect.poll(view, { timeout: 8000 }).toMatchObject({ open: false })
    await client.detach()
  } finally {
    cleanup(peers, dirs)
  }
})

test('a tap, typing or the latest button returns to the live screen', async ({ page }) => {
  needsTmux()
  const peers: ChildProcess[] = [], dirs: string[] = []
  try {
    const { terminal, view, cell } = await openTerminal(page, peers, dirs, { touch: true, android: false })
    const box = (await terminal.boundingBox())!
    const client = await page.context().newCDPSession(page)
    const x = box.x + box.width / 2, y = box.y + box.height * 0.3
    const open = async () => {
      await drag(page, client, x, y, 6 * cell, 8)
      await expect.poll(view).toMatchObject({ open: true })
    }
    await open()
    await page.locator('[data-history-latest]').click()
    await expect.poll(view).toMatchObject({ open: false })

    await open()
    await client.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] })
    await client.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
    await expect.poll(view).toMatchObject({ open: false })

    await open()
    await page.evaluate(() => (document.querySelector('[data-terminal-session="active"] .xterm-helper-textarea') as HTMLTextAreaElement).focus())
    await page.keyboard.press('x')
    await expect.poll(view).toMatchObject({ open: false })

    // The key bar and the Android app's keys send through __wsRef, with no
    // keyboard event on the page.
    await open()
    await page.evaluate(() => {
      const el = document.querySelector('[data-terminal-session="active"]') as any
      el.__wsRef.current.send(new TextEncoder().encode('\r'))
    })
    await expect.poll(view).toMatchObject({ open: false })
    await client.detach()
  } finally {
    cleanup(peers, dirs)
  }
})

// The page buttons move a page, less one line of overlap: the history view
// for a shell's history, tmux for a program in the alternate screen. The web
// key bar called scrollPages, which a tmux session's alternate buffer cannot
// do at all; the app's dock dispatches one wheel event of the page's pixels,
// which xterm made one report — one line of Codex.
for (const receiver of ['history', 'program'] as const) {
  test(`the key bar's page buttons move a page of ${receiver}`, async ({ page }) => {
    needsTmux()
    const peers: ChildProcess[] = [], dirs: string[] = []
    try {
      const { firstLine, view, rows, cell } = await openTerminal(page, peers, dirs, { touch: true, cmd: receiver === 'program' ? PAGER : undefined, android: false })
      const at = await firstLine()
      await page.getByRole('button', { name: 'Scroll up', exact: true }).first().click()
      if (receiver === 'history') await expect.poll(view).toMatchObject({ open: true, offset: Math.round((rows - 1) * cell), top: at - (rows - 1) })
      else await expect.poll(firstLine).toBe(at - (rows - 1))
      await page.getByRole('button', { name: 'Scroll down', exact: true }).first().click()
      if (receiver === 'history') await expect.poll(view).toMatchObject({ open: false })
      else await expect.poll(firstLine).toBe(at)
    } finally {
      cleanup(peers, dirs)
    }
  })

  test(`the app's page buttons move a page of ${receiver}`, async ({ page }) => {
    needsTmux()
    const peers: ChildProcess[] = [], dirs: string[] = []
    try {
      const { firstLine, view, rows, cell } = await openTerminal(page, peers, dirs, { touch: true, cmd: receiver === 'program' ? PAGER : undefined, android: true })
      const at = await firstLine()
      // What MainActivity.scroll(-1) runs: a page is the session element's height.
      await page.evaluate(() => {
        const e = document.querySelector('[data-terminal-session="active"]') as any
        ;(window as any).__sfpanelScrollTerminal(e, -1 * e.clientHeight)
      })
      if (receiver === 'history') await expect.poll(view).toMatchObject({ open: true, offset: Math.round((rows - 1) * cell), top: at - (rows - 1) })
      else await expect.poll(firstLine).toBe(at - (rows - 1))
    } finally {
      cleanup(peers, dirs)
    }
  })
}

// A finger that rests on the history view before it moves still scrolls it.
// On the live screen that hold is a long press, and a fired press keeps the
// rest of the drag for selecting; over the copy there is nothing to select.
test('a finger that rests on the history view and then drags scrolls it', async ({ page }) => {
  needsTmux()
  const peers: ChildProcess[] = [], dirs: string[] = []
  try {
    const { terminal, view, cell } = await openTerminal(page, peers, dirs, { touch: true, android: false })
    const box = (await terminal.boundingBox())!
    const client = await page.context().newCDPSession(page)
    const x = box.x + box.width / 2, y = box.y + box.height * 0.3
    await drag(page, client, x, y, 3 * cell, 3)
    await expect.poll(view).toMatchObject({ open: true, offset: 3 * cell })
    await client.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] })
    await page.waitForTimeout(800)
    for (let i = 1; i <= 8; i++) {
      await client.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x, y: y + i * cell / 2 }] })
      await page.waitForTimeout(16)
    }
    await client.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
    await expect.poll(view).toMatchObject({ open: true, offset: 7 * cell })
    // And nothing was selected on the live screen underneath.
    expect(await page.locator('[data-terminal-selection]').count()).toBe(0)
    await client.detach()
  } finally {
    cleanup(peers, dirs)
  }
})

// A copy's lines are joined (-J) and wrap again in the view: its buffer is
// counted in rows, or the oldest wrapped lines fall off the top.
test('the top of a wrapped history is in reach', async ({ page }) => {
  needsTmux()
  const peers: ChildProcess[] = [], dirs: string[] = []
  try {
    const cmd = `sh -c 'for i in $(seq 1 150); do printf "L%s-%0200d\\n" $i 0; done; exec sleep 120'`
    const { terminal, view, cell } = await openTerminal(page, peers, dirs, { touch: true, cmd, android: false, numbered: false })
    const box = (await terminal.boundingBox())!
    const client = await page.context().newCDPSession(page)
    const x = box.x + box.width / 2, y = box.y + box.height * 0.3
    await drag(page, client, x, y, 4 * cell, 8)
    await expect.poll(view).toMatchObject({ open: true })
    // 150 lines of 203 characters are 750 rows at a phone's 47 columns:
    // further back than that, in whole drags that rest before lifting.
    for (let i = 0; i < 14; i++) await drag(page, client, x, y, 60 * cell, 480)
    expect(await page.evaluate(() => document.querySelectorAll('[data-history-view] .xterm-rows > div')[1]?.textContent?.trim().slice(0, 3))).toBe('L1-')
    await client.detach()
  } finally {
    cleanup(peers, dirs)
  }
})

// A fling moves the view from inside an animation frame, and xterm paints new
// rows on the next one: the shift has to go in with the rows, or the text jumps
// a row back at every line it crosses. Sampled every frame, a line's place on
// screen only ever moves one way.
test('a flick through the history view never jumps back a row (Android)', async ({ page }) => {
  needsTmux()
  const peers: ChildProcess[] = [], dirs: string[] = []
  try {
    const { terminal, view, cell } = await openTerminal(page, peers, dirs, { touch: true, android: true })
    const box = (await terminal.boundingBox())!
    const client = await page.context().newCDPSession(page)
    const x = box.x + box.width / 2, y = box.y + box.height * 0.2
    await drag(page, client, x, y, 2 * cell, 8)
    await expect.poll(view).toMatchObject({ open: true })
    await page.evaluate((cell) => {
      const w = window as any
      w.__samples = []
      w.__sampling = true
      const sample = () => {
        const el = document.querySelector('[data-history-view]') as HTMLElement | null
        if (el) {
          const top = Number.parseInt(el.querySelectorAll('.xterm-rows > div')[1]?.textContent ?? '', 10)
          const shift = Number(/translateY\((-?[\d.]+)px\)/.exec((el.querySelector('.xterm') as HTMLElement).style.transform)?.[1])
          // Where line 0 would be, in pixels above the view's top.
          if (Number.isFinite(top) && Number.isFinite(shift)) w.__samples.push(top * cell - (shift + cell))
        }
        if (w.__sampling) requestAnimationFrame(sample)
      }
      requestAnimationFrame(sample)
    }, cell)
    await drag(page, client, x, y, 6 * cell, 48, false)
    let last = -1
    await expect.poll(async () => { const now = (await view()).offset; const still = now === last; last = now; return still }, { intervals: [400] }).toBe(true)
    const samples: number[] = await page.evaluate(() => { (window as any).__sampling = false; return (window as any).__samples })
    expect(samples.length).toBeGreaterThan(20)
    const backwards = samples.slice(1).filter((v, i) => v > samples[i] + 0.5)
    expect(backwards).toEqual([])
    await client.detach()
  } finally {
    cleanup(peers, dirs)
  }
})

// Without a copy the gesture still scrolls, through tmux.
test('a drag back scrolls through tmux when the copy cannot be had', async ({ page }) => {
  needsTmux()
  const peers: ChildProcess[] = [], dirs: string[] = []
  try {
    const { terminal, firstLine, view, cell } = await openTerminal(page, peers, dirs, { touch: true, android: false, scrollback: { fail: true } })
    const before = await firstLine()
    const box = (await terminal.boundingBox())!
    const client = await page.context().newCDPSession(page)
    await drag(page, client, box.x + box.width / 2, box.y + box.height * 0.3, 8 * cell, 8)
    await expect.poll(firstLine).toBe(before - 8)
    expect((await view()).open).toBe(false)
    await client.detach()
  } finally {
    cleanup(peers, dirs)
  }
})

// A program in the alternate screen waits for the answer once; the gestures
// after it go to tmux at once, while the panel is asked again behind them —
// including one that starts while that question is still out.
test('a program in the alternate screen waits for the copy only once', async ({ page }) => {
  needsTmux()
  const peers: ChildProcess[] = [], dirs: string[] = []
  try {
    const { terminal, firstLine, cell } = await openTerminal(page, peers, dirs, { touch: true, cmd: PAGER, android: false, scrollback: { delayMs: 1500 } })
    const box = (await terminal.boundingBox())!
    const client = await page.context().newCDPSession(page)
    const x = box.x + box.width / 2, y = box.y + box.height * 0.3
    let at = await firstLine()
    await drag(page, client, x, y, 4 * cell, 8)
    await expect.poll(firstLine, { timeout: 5000 }).toBe(at - 4)
    at -= 4
    for (let i = 0; i < 2; i++) {
      const t0 = Date.now()
      await drag(page, client, x, y, 4 * cell, 8)
      await expect.poll(firstLine, { intervals: [50] }).toBe(at - 4)
      at -= 4
      expect(Date.now() - t0).toBeLessThan(1000)
    }
    await client.detach()
  } finally {
    cleanup(peers, dirs)
  }
})

// --- Latest output, in a session whose history lives in tmux ---------------

const paneMode = (tmux: (...args: string[]) => string) => tmux('display-message', '-p', '-t', 'test', '#{pane_in_mode}').trim()

// With no copy to be had a drag scrolls through tmux, which puts the pane in
// copy mode: where "latest output" had nothing to move.
async function scrollBackThroughTmux(page: Page, opened: Awaited<ReturnType<typeof openTerminal>>) {
  const box = (await opened.terminal.boundingBox())!
  const client = await page.context().newCDPSession(page)
  await drag(page, client, box.x + box.width / 2, box.y + box.height * 0.3, 6 * opened.cell, 8)
  await client.detach()
  await expect.poll(() => paneMode(opened.tmux)).toBe('1')
}

test('the latest button brings a pane back from tmux copy mode', async ({ page }) => {
  needsTmux()
  const peers: ChildProcess[] = [], dirs: string[] = []
  try {
    const opened = await openTerminal(page, peers, dirs, { touch: true, android: false, scrollback: { fail: true } })
    const live = await opened.firstLine()
    await scrollBackThroughTmux(page, opened)
    await expect.poll(opened.firstLine).toBeLessThan(live)
    await page.locator('[data-mobile-terminal-bar]').getByRole('button', { name: 'Latest output' }).tap()
    await expect.poll(() => paneMode(opened.tmux)).toBe('0')
    await expect.poll(opened.firstLine).toBe(live)
  } finally {
    cleanup(peers, dirs)
  }
})

test('the app\'s own latest button does too', async ({ page }) => {
  needsTmux()
  const peers: ChildProcess[] = [], dirs: string[] = []
  try {
    const opened = await openTerminal(page, peers, dirs, { touch: true, android: true, scrollback: { fail: true } })
    await scrollBackThroughTmux(page, opened)
    // MainActivity.scroll(0), as the app evaluates it.
    await opened.terminal.evaluate(el => {
      const e = el as any, t = e.__termRef.current
      e.__fitAddon?.fit()
      requestAnimationFrame(() => t.scrollLines(t.buffer.active.length))
    })
    await expect.poll(() => paneMode(opened.tmux)).toBe('0')
  } finally {
    cleanup(peers, dirs)
  }
})

test('a panel from before the latest frame is not sent it', async ({ page }) => {
  needsTmux()
  const peers: ChildProcess[] = [], dirs: string[] = []
  try {
    const opened = await openTerminal(page, peers, dirs, { touch: true, android: false, scrollback: { fail: true }, latest: false })
    await scrollBackThroughTmux(page, opened)
    await page.locator('[data-mobile-terminal-bar]').getByRole('button', { name: 'Latest output' }).tap()
    await page.waitForTimeout(500)
    // That panel would write the frame into the session as text.
    expect(opened.frames).toEqual([])
  } finally {
    cleanup(peers, dirs)
  }
})

test('the keyboard opening leaves a pane scrolled back where it is', async ({ page }) => {
  needsTmux()
  const peers: ChildProcess[] = [], dirs: string[] = []
  try {
    const opened = await openTerminal(page, peers, dirs, { touch: true, android: false, scrollback: { fail: true } })
    await scrollBackThroughTmux(page, opened)
    // The fit that follows a resize anchors the terminal to its end with the
    // same scroll the latest buttons make; it is not one of them.
    await page.setViewportSize({ width: 412, height: 560 })
    await expect.poll(() => opened.terminal.evaluate(el => (el as any).__termRef.current.rows as number)).toBeLessThan(opened.rows)
    await page.waitForTimeout(500)
    expect(opened.frames).toEqual([])
    expect(paneMode(opened.tmux)).toBe('1')
  } finally {
    cleanup(peers, dirs)
  }
})

// --- Search ---------------------------------------------------------------

// The whole rows the history view shows.
const shownRows = (page: Page) => page.evaluate(() => {
  const frame = document.querySelector('[data-history-view]') as HTMLElement | null
  if (!frame || frame.style.visibility === 'hidden') return []
  const edge = frame.getBoundingClientRect()
  return [...frame.querySelectorAll('.xterm-rows > div')]
    .filter(r => { const b = r.getBoundingClientRect(); return b.top >= edge.top - 1 && b.bottom <= edge.bottom + 1 })
    .map(r => (r.textContent ?? '').trim())
})

test('a search on a phone outlives the keyboard its box raises', async ({ page }) => {
  needsTmux()
  const peers: ChildProcess[] = [], dirs: string[] = []
  try {
    const { terminal, rows } = await openTerminal(page, peers, dirs, { touch: true, android: false })
    await page.locator('[data-session-menu]').tap()
    await page.getByRole('menuitem', { name: 'Search' }).tap()
    await page.getByPlaceholder('Search in terminal...').fill('123')
    await expect.poll(() => shownRows(page)).toContain('123')
    // The keyboard resizes the terminal, and the view was laid out for the
    // size before: it closes, and the search brings its match back.
    await page.setViewportSize({ width: 412, height: 560 })
    await expect.poll(() => terminal.evaluate(el => (el as any).__termRef.current.rows as number)).toBeLessThan(rows)
    await expect.poll(() => shownRows(page), { timeout: 8000 }).toContain('123')
    await expect(page.locator('[data-search-status]')).toHaveText('1/1')
    // Left with a tap, the view stays away through the next resize: the tap
    // was to type, and that is what raises the keyboard.
    await page.locator('[data-history-view]').tap()
    await expect(page.locator('[data-history-view]')).toHaveCount(0)
    await page.setViewportSize({ width: 412, height: 700 })
    await expect.poll(() => terminal.evaluate(el => (el as any).__termRef.current.rows as number)).toBeGreaterThan(30)
    await page.waitForTimeout(800)
    await expect(page.locator('[data-history-view]')).toHaveCount(0)
  } finally {
    cleanup(peers, dirs)
  }
})

test.describe('search on a desktop', () => {
  test.use({ viewport: { width: 1280, height: 800 }, hasTouch: false, locale: 'en-US' })

  test('finds what has scrolled off a tmux session\'s screen', async ({ page }) => {
    needsTmux()
    const peers: ChildProcess[] = [], dirs: string[] = []
    try {
      const { terminal, firstLine, view, tmux } = await openTerminal(page, peers, dirs, { touch: true, android: false })
      // 123 is far above the screen, which is all the browser holds of a tmux session.
      expect(await firstLine()).toBeGreaterThan(200)
      await page.keyboard.press('Control+Shift+f')
      const box = page.getByPlaceholder('Search in terminal...')
      const status = page.locator('[data-search-status]')
      await box.fill('123')
      await expect(status).toHaveText('1/1')
      // On the screen, not only counted.
      const shown = () => shownRows(page)
      await expect.poll(shown).toContain('123')
      // 25, 125, 225 and 250–259: the newest first, Enter towards the older.
      await box.fill('25')
      await expect(status).toHaveText('13/13')
      await expect.poll(shown).toContain('259')
      await box.press('Enter')
      await expect(status).toHaveText('12/13')
      await box.press('Shift+Enter')
      await expect(status).toHaveText('13/13')
      // A mouse has no finger to drag the view with: its wheel moves it.
      const before = (await view()).offset
      const at = (await terminal.boundingBox())!
      await page.mouse.move(at.x + at.width / 2, at.y + at.height / 2)
      await page.mouse.wheel(0, -240)
      await expect.poll(async () => (await view()).offset).toBeGreaterThan(before)
      await box.fill('no such text')
      await expect(status).toHaveText('No results')
      // The search read a copy: tmux never left its live screen.
      expect(paneMode(tmux)).toBe('0')
      await box.press('Escape')
      await expect.poll(async () => (await view()).open).toBe(false)
      await expect(page.locator('[data-history-view]')).toHaveCount(0)
      // And the keyboard is the terminal's again.
      await expect.poll(() => page.evaluate(() => document.activeElement?.classList.contains('xterm-helper-textarea'))).toBe(true)
    } finally {
      cleanup(peers, dirs)
    }
  })

  test('a program in the alternate screen is searched on its screen, and the page says so', async ({ page }) => {
    needsTmux()
    const peers: ChildProcess[] = [], dirs: string[] = []
    try {
      const { firstLine } = await openTerminal(page, peers, dirs, { touch: true, cmd: PAGER, android: false })
      const top = await firstLine()
      await page.keyboard.press('Control+Shift+f')
      const box = page.getByPlaceholder('Search in terminal...')
      await box.fill('300')
      await expect(page.locator('[data-search-status]')).toHaveText('1/1')
      await expect(page.locator('[data-search-hint]')).toHaveText('Searching the visible screen only')
      await expect(page.locator('[data-history-view]')).toHaveCount(0)
      // What the program has scrolled off is its own to show.
      await box.fill(String(top - 20))
      await expect(page.locator('[data-search-status]')).toHaveText('No results')
    } finally {
      cleanup(peers, dirs)
    }
  })
})
