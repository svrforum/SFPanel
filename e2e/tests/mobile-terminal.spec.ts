import { test, expect } from '@playwright/test'
import { resolve } from 'node:path'

// These tests never contact a host shell: REST and WebSocket are both fixtures.
// Run against a frontend dev server or a built panel; no admin seed is needed.
test.use({ viewport: { width: 412, height: 860 }, hasTouch: true, locale: 'en-US' })

for (const path of ['/terminal', '/ai']) {
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
    if (path === '/ai') {
      // A clipping frame must not become an extra scroll container when the
      // WebView brings its focused textarea into view above the keyboard.
      const frame = page.locator('[data-ai-workspace] > .overflow-clip')
      expect(await frame.evaluate(el => { el.scrollTop = 100; return el.scrollTop })).toBe(0)
    }
    for (const button of await bar.getByRole('button').all()) {
      const box = await button.boundingBox()
      expect(box?.height).toBeGreaterThanOrEqual(48)
    }
    if (path === '/ai') {
      await page.addScriptTag({ path: resolve(__dirname, '../../android/app/src/main/assets/panel.js') })
      const overview = page.locator('[data-ai-workspace] > :first-child')
      await expect(overview).toBeHidden()
      await expect(bar).toBeHidden()
      await expect.poll(async () => (await terminal.boundingBox())?.height ?? 0).toBeGreaterThan(450)
      // Compact mode must retain account/tool controls on demand.
      await page.evaluate(() => document.documentElement.setAttribute('data-ai-tools-open', ''))
      await expect(overview).toBeVisible()
      await expect(page.getByRole('combobox', { name: 'Run as' })).toBeVisible()
    }
  })
}
