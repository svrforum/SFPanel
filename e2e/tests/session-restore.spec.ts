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

    await expect(page).toHaveURL(/\/dashboard$/)
    expect(calls.filter((c) => c.endsWith('/auth/refresh'))).toHaveLength(1)
    // The restored token is in sessionStorage, where it belongs — a closed tab
    // still drops it, and only the cookie survives.
    expect(await page.evaluate(() => sessionStorage.getItem('token'))).toBe('restored-token')
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
})
