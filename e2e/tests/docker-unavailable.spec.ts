import { test, expect, type Page } from '@playwright/test'

// A node without Docker answers every /docker and /compose route with 503
// DOCKER_UNAVAILABLE and an English message, which the pages showed as sent:
// English in a Korean panel. The client now words that code itself.
//
// A server where Docker is not installed is offered the install above the
// Docker pages, the Packages page's Docker card, and the pages read again
// once it is in. One where Docker is installed but stopped is not.
test.use({ viewport: { width: 1280, height: 800 } })

const NOT_INSTALLED = { installed: false, version: '', running: false, compose_available: false }
const INSTALLED = { installed: true, version: '27.0.0', running: true, compose_available: true }

const seen = '2026-01-01T00:00:00Z'
const nodes = [
  { id: 'm-1', name: 'node-a', kind: 'manager', role: 'voter', status: 'online', joined_at: seen, last_seen: seen, api_address: '192.0.2.1:3628', grpc_address: '192.0.2.1:3629' },
  { id: 'agent-1', name: 'agent-01', kind: 'agent', role: '', status: 'online', joined_at: seen, last_seen: seen, api_address: '198.51.100.1:3628', grpc_address: '198.51.100.1:3629' },
]

/**
 * Mocks the panel's API. /packages/docker-status answers statuses in turn,
 * the last one again after that. requests has the path and query of every
 * request, installs those of every Docker install; /docker/compose answers
 * an empty list once an install ran.
 */
async function mock(page: Page, opts: { lang?: 'ko' | 'en'; cluster?: boolean; statuses?: object[]; installFails?: boolean } = {}) {
  const lang = opts.lang ?? 'en'
  const statuses = opts.statuses ?? [{}]
  const requests: string[] = []
  const installs: string[] = []
  let statusReads = 0
  await page.addInitScript((lang) => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('sfpanel_language', lang)
    localStorage.setItem('i18nextLng', lang)
  }, lang)
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname.replace('/api/v1', '')
    requests.push(path + url.search)
    if (req.method() === 'POST' && path === '/packages/install-docker') {
      installs.push(path + url.search)
      if (opts.installFails) {
        await route.fulfill({ status: 500, json: { success: false, error: { code: 'INTERNAL_ERROR', message: 'fixture' } } })
        return
      }
      await route.fulfill({ status: 200, contentType: 'text/event-stream', body: 'data: Installing\n\ndata: [DONE]\n\n' })
      return
    }
    if (path === '/docker/compose' && installs.length > 0) {
      await route.fulfill({ json: { success: true, data: [] } })
      return
    }
    if (path.startsWith('/docker/') || path.startsWith('/compose/')) {
      await route.fulfill({ status: 503, json: { success: false, error: { code: 'DOCKER_UNAVAILABLE', message: 'Docker is not available on this node. Install it from Packages, or check that the daemon is running.' } } })
      return
    }
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/auth/ws-ticket') data = { ticket: 'fixture-ticket' }
    else if (path === '/packages/docker-status') data = statuses[Math.min(statusReads++, statuses.length - 1)]
    else if (path === '/cluster/status') data = opts.cluster
      ? { enabled: true, name: 'lab', node_count: 2, leader_id: 'm-1', local_id: 'm-1', is_leader: true }
      : { enabled: false }
    else if (path === '/cluster/nodes') {
      if (opts.cluster) {
        await new Promise((resolve) => setTimeout(resolve, 800))
        data = { nodes, local_id: 'm-1', is_leader: true }
      } else data = []
    }
    else if (path === '/cluster/overview') data = { name: 'lab', node_count: 2, leader_id: 'm-1', nodes, metrics: [] }
    else if (path === '/cluster/events') data = { events: [] }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  return { requests, installs }
}

test('a Korean panel says Docker is missing in Korean', async ({ page }) => {
  await mock(page, { lang: 'ko' })
  await page.goto('/docker/stacks')
  await expect(page.getByText('이 서버에서는 Docker를 쓸 수 없습니다', { exact: false }).first()).toBeVisible()
  await expect(page.getByText('Docker is not available on this node')).toHaveCount(0)
})

test('a server without Docker offers its install above the Docker pages', async ({ page }) => {
  await mock(page, { statuses: [NOT_INSTALLED] })
  await page.goto('/docker/stacks')
  await expect(page.getByRole('button', { name: 'Install Docker' })).toBeVisible()
  await page.getByRole('main').getByRole('link', { name: 'Containers' }).click()
  await expect(page).toHaveURL(/\/docker\/containers$/)
  await expect(page.getByRole('button', { name: 'Install Docker' })).toBeVisible()
})

test('a Docker that is installed but stopped is not offered an install', async ({ page }) => {
  const { requests } = await mock(page, { statuses: [{ ...INSTALLED, running: false }] })
  await page.goto('/docker/stacks')
  await expect.poll(() => requests.some((r) => r.startsWith('/packages/docker-status'))).toBe(true)
  await page.waitForTimeout(500)
  // The card itself, not only its button: on an installed Docker the card
  // shows the engine's state and has no install button to find.
  await expect(page.getByRole('heading', { name: 'Docker Status' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Install Docker' })).toHaveCount(0)
})

test('the install runs on the server in the address, and the pages read again after it', async ({ page }) => {
  const { requests, installs } = await mock(page, { cluster: true, statuses: [NOT_INSTALLED, NOT_INSTALLED, INSTALLED] })
  await page.goto('/docker/stacks?node=agent-1')
  await page.getByRole('button', { name: 'Install Docker' }).click()
  await expect.poll(() => installs).toEqual(['/packages/install-docker?node=agent-1'])
  await expect(page.getByRole('button', { name: 'Install Docker' })).toHaveCount(0)
  // within 5 s: DockerStacks reads the list again by itself only every 15 s (DockerStacks.tsx useVisibleInterval)
  await expect.poll(() => requests.filter((r) => r.startsWith('/docker/compose') && r.includes('node=agent-1')).length, { timeout: 5000 }).toBeGreaterThanOrEqual(2)
  expect(requests.filter((r) => r.startsWith('/packages/docker-status')).every((r) => r.includes('node=agent-1'))).toBe(true)
  // The output dialog hides the page from the accessibility tree while it is
  // open; once it is closed, the card is seen to be gone.
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.getByRole('heading', { name: 'Docker Status' })).toHaveCount(0)
})

// The card goes with the install, and with it the button the output dialog
// would hand the focus back to: closing the dialog left the keyboard on the
// page's body. The focus goes to the tab open under the card instead.
test('after the install the focus comes back to the open tab', async ({ page }) => {
  await mock(page, { statuses: [NOT_INSTALLED, NOT_INSTALLED, INSTALLED] })
  await page.goto('/docker/stacks')
  await page.getByRole('button', { name: 'Install Docker' }).click()
  await expect(page.getByRole('dialog')).toContainText('Installing')
  // Not by role: the open dialog hides the page from the accessibility tree.
  await expect(page.locator('button', { hasText: 'Install Docker' })).toHaveCount(0)
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.getByRole('navigation', { name: 'Docker' }).getByRole('link', { name: 'Stacks' })).toBeFocused()
})

// The output dialog opens from the page's own button, not a dialog trigger,
// and Radix gives the focus back to a trigger only: closing it left the
// keyboard on the page's body. An install that could not start keeps its
// card, and the focus goes back to the button that opened the dialog.
test('an install that could not start gives the focus back to its button', async ({ page }) => {
  await mock(page, { statuses: [NOT_INSTALLED], installFails: true })
  await page.goto('/docker/stacks')
  await page.getByRole('button', { name: 'Install Docker' }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await expect(page.getByRole('dialog').getByRole('button', { name: 'Close' }).last()).toBeEnabled()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Install Docker' })).toBeFocused()
})
