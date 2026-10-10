import { test, expect } from '@playwright/test'

// A node without Docker answers every /docker and /compose route with 503
// DOCKER_UNAVAILABLE and an English message, which the pages showed as sent:
// English in a Korean panel. The client now words that code itself.
test.use({ viewport: { width: 1280, height: 800 } })

test('a Korean panel says Docker is missing in Korean', async ({ page }) => {
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('sfpanel_language', 'ko')
    localStorage.setItem('i18nextLng', 'ko')
  })
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '')
    if (path.startsWith('/docker/') || path.startsWith('/compose/')) {
      await route.fulfill({ status: 503, json: { success: false, error: { code: 'DOCKER_UNAVAILABLE', message: 'Docker is not available on this node. Install it from Packages, or check that the daemon is running.' } } })
      return
    }
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: false }
    else if (path === '/cluster/nodes') data = []
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/docker/stacks')
  await expect(page.getByText('이 노드에서는 Docker를 쓸 수 없습니다', { exact: false }).first()).toBeVisible()
  await expect(page.getByText('Docker is not available on this node')).toHaveCount(0)
})
