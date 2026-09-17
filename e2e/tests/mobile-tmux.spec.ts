import { test, expect } from '@playwright/test'
import { spawn, spawnSync, type ChildProcess } from 'node:child_process'
import { resolve } from 'node:path'
import { readFileSync } from 'node:fs'

test.use({ viewport: { width: 412, height: 860 }, hasTouch: true })

for (const android of [false, true]) {
  test(`AI history visibly scrolls through real tmux (${android ? 'Android' : 'web'})`, async ({ page }) => {
    test.skip(spawnSync('tmux', ['-V']).status !== 0 || spawnSync('python3', ['--version']).status !== 0,
      'Requires tmux and Python 3 for the isolated PTY fixture')
    const peers: ChildProcess[] = []
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
      else if (path.endsWith('/ai/tools')) data = { tmux: { installed: true, supported: true, version: '3.4', min_version: '3.2' }, systemd_run: true, accounts: ['tester'], panel_account: 'tester', account: 'tester', tools: {} }
      await route.fulfill({ json: { success: true, data } })
    })
    await page.routeWebSocket(/\/ws\/ai\/attach/, socket => {
      const peer = spawn('python3', [resolve(__dirname, '../fixtures/tmux-pty.py')])
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
    try {
      await page.goto('/terminal')
      const terminal = page.locator('[data-terminal-session="active"]')
      await expect(terminal).toBeVisible()
      // Match native evaluateJavascript while leaving the server CSP intact.
      if (android) await page.evaluate(readFileSync(resolve(__dirname, '../../android/app/src/main/assets/panel.js'), 'utf8'))
      const firstLine = () => terminal.evaluate(el => {
        const t = (el as any).__termRef.current
        return Number.parseInt(t.buffer.active.getLine(0)?.translateToString(true) || '', 10)
      })
      await expect.poll(firstLine).toBeGreaterThan(200)
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
}
