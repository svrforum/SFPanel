import { test, expect, type Route } from '@playwright/test'

// Issue #54: a returning browser was sent to the login form while its 7-day
// httpOnly refresh cookie sat unused. The guard now gives the server one silent
// POST /auth/refresh before it decides.
//
// Fixtures only — every /api/v1 call is fulfilled in the browser, so this spec
// needs a front end and nothing else. Point it at a dev server:
//
//   (cd web && SFPANEL_DEV_API=http://127.0.0.1:59999 npx vite --port 5199)
//   PLAYWRIGHT_BASE_URL=http://localhost:5199 npx playwright test session-restore
//
// The dead SFPANEL_DEV_API keeps the dev proxy from forwarding anything this
// spec does not intercept (a websocket, say) to a real panel.

// fulfil answers the handful of endpoints a cold /dashboard load touches. The
// shapes are the minimum each caller reads; everything else gets an empty
// object, which is enough because the assertions are about routing, not content.
async function fulfil(route: Route, refresh: () => { status: number; body: unknown }) {
  const url = new URL(route.request().url())
  if (url.pathname.endsWith('/auth/refresh')) {
    const answer = refresh()
    await route.fulfill({ status: answer.status, json: answer.body })
    return
  }
  let data: unknown = {}
  if (url.pathname.endsWith('/auth/setup-status')) data = { setup_required: false }
  else if (url.pathname.endsWith('/cluster/status')) data = { enabled: false }
  else if (url.pathname.endsWith('/system/overview')) data = { version: 'test' }
  await route.fulfill({ json: { success: true, data } })
}

test.describe('Session restore', () => {
  test('a returning browser gets its session back from the refresh cookie', async ({ page, context, baseURL }) => {
    const calls: string[] = []
    await context.addCookies([{
      name: 'sfpanel_refresh',
      value: 'fixture-refresh',
      domain: new URL(baseURL!).hostname,
      path: '/api/v1/auth',
      httpOnly: true,
      secure: false,
      sameSite: 'Strict',
    }])
    await page.addInitScript(() => localStorage.setItem('sfpanel_language', 'en'))
    await page.route('**/api/v1/**', async (route) => {
      calls.push(`${route.request().method()} ${new URL(route.request().url()).pathname}`)
      await fulfil(route, () => ({ status: 200, body: { success: true, data: { token: 'restored-token' } } }))
    })

    await page.goto('/dashboard')

    // Wait on the outcome, not on `goto`: the boot refresh is taken under a
    // cross-tab lock, so it leaves the page a tick after load rather than
    // during it. The restored token in sessionStorage is where it belongs —
    // a closed tab still drops it, and only the cookie survives.
    await expect.poll(() => page.evaluate(() => sessionStorage.getItem('token'))).toBe('restored-token')
    await expect(page).toHaveURL(/\/dashboard$/)
    expect(calls.filter((c) => c.endsWith('/auth/refresh'))).toHaveLength(1)
  })

  test('a browser with no cookie still lands on the login form after one attempt', async ({ page }) => {
    const calls: string[] = []
    await page.addInitScript(() => localStorage.setItem('sfpanel_language', 'en'))
    await page.route('**/api/v1/**', async (route) => {
      calls.push(`${route.request().method()} ${new URL(route.request().url()).pathname}`)
      await fulfil(route, () => ({
        status: 401,
        body: { success: false, error: { code: 'INVALID_TOKEN', message: 'Invalid refresh token' } },
      }))
    })

    await page.goto('/dashboard')

    await expect(page).toHaveURL(/\/login$/)
    expect(calls.filter((c) => c.endsWith('/auth/refresh'))).toHaveLength(1)
  })

  // The refresh cookie rotates on every use and the server revokes the whole
  // family when an already-consumed one comes back, so two tabs opened together
  // — a bookmark folder, the browser's own session restore — must not present
  // it at the same time. Both still refresh: sessionStorage is per-tab, so the
  // second tab needs its own access token. What must not happen is an overlap.
  test('two tabs booting together never hold the same refresh cookie at once', async ({ context, baseURL }) => {
    await context.addCookies([{
      name: 'sfpanel_refresh',
      value: 'fixture-refresh',
      domain: new URL(baseURL!).hostname,
      path: '/api/v1/auth',
      httpOnly: true,
      secure: false,
      sameSite: 'Strict',
    }])

    const windows: { start: number; end: number }[] = []
    await context.route('**/api/v1/**', async (route) => {
      if (new URL(route.request().url()).pathname.endsWith('/auth/refresh')) {
        const start = Date.now()
        // Held open long enough that an unserialised second tab would still be
        // inside the first tab's rotation — which is the replay the server
        // answers with "Session revoked".
        await new Promise((resolve) => setTimeout(resolve, 300))
        await route.fulfill({ json: { success: true, data: { token: 'restored-token' } } })
        windows.push({ start, end: Date.now() })
        return
      }
      await fulfil(route, () => ({ status: 200, body: { success: true, data: {} } }))
    })

    const first = await context.newPage()
    const second = await context.newPage()
    for (const tab of [first, second]) {
      await tab.addInitScript(() => localStorage.setItem('sfpanel_language', 'en'))
    }

    await Promise.all([first.goto('/dashboard'), second.goto('/dashboard')])

    for (const tab of [first, second]) {
      await expect.poll(() => tab.evaluate(() => sessionStorage.getItem('token'))).toBe('restored-token')
      await expect(tab).toHaveURL(/\/dashboard$/)
    }

    expect(windows).toHaveLength(2)
    windows.sort((a, b) => a.start - b.start)
    expect(windows[1].start).toBeGreaterThanOrEqual(windows[0].end)
  })
})
