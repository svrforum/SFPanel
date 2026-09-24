import { test, expect, type Page, type Route, type WebSocketRoute } from '@playwright/test'

// Fixture-only: every API and websocket request is intercepted, including navigation targets.
const metrics = {
  cpu: 28, mem_total: 16 * 1024 ** 3, mem_used: 8 * 1024 ** 3, mem_percent: 50,
  swap_total: 0, swap_used: 0, swap_percent: 0, disk_total: 100 * 1024 ** 3,
  disk_used: 40 * 1024 ** 3, disk_percent: 40, net_bytes_sent: 1024 ** 3,
  net_bytes_recv: 2 * 1024 ** 3, timestamp: Date.now(),
}
const containers = [
  { Id: 'healthy-id', Names: ['/worker'], Image: 'worker:latest', State: 'running', Status: 'Up 1 hour', cpu_avg_1h: 30, Ports: [], Labels: {}, Created: 1 },
  { Id: 'unhealthy-id', Names: ['/api-service'], Image: 'api:latest', State: 'running', Status: 'Up 1 hour (unhealthy)', cpu_avg_1h: 5, Ports: [], Labels: {}, Created: 1 },
]
const filesystem = (mount: string, percent: number) => ({ source: '/dev/sda1', fstype: 'ext4', size: 100 * 1024 ** 3, used: percent * 1024 ** 3, available: (100 - percent) * 1024 ** 3, use_percent: percent, mount_point: mount })

async function fixture(page: Page, override?: (route: Route, url: URL) => Promise<boolean>) {
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('sfpanel_language', 'en')
  })
  const sockets: WebSocketRoute[] = []
  await page.routeWebSocket(/\/ws\//, (socket) => { sockets.push(socket) })
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    if (override && await override(route, url)) return
    const path = url.pathname.replace('/api/v1', '')
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/auth/ws-ticket') data = { ticket: 'fixture-ticket' }
    else if (path === '/cluster/status') data = { enabled: false }
    else if (path === '/system/overview') data = {
      host: { hostname: 'demo-server', os: 'linux', platform: 'Ubuntu', platform_version: '24.04', kernel: '6.8.0', uptime: 86400, num_cpu: 8 },
      metrics, version: 'test', metrics_history: [], update_info: { update_available: true, latest_version: '99.0' },
    }
    else if (path === '/system/metrics-history') data = Array.from({ length: 60 }, (_, i) => ({ time: metrics.timestamp - (59 - i) * 60000, cpu: 20 + i % 10, mem_percent: 50, disk_percent: 40 }))
    else if (path === '/system/processes') data = [{ pid: 123, name: 'node-worker', cpu: 22, memory: 12, status: 'running' }]
    else if (path === '/system/backup/schedule') data = { schedule: { enabled: true, last_status: 'error', last_run: '2026-09-23T00:00:00Z' }, files: [] }
    else if (path === '/filesystems') data = [filesystem('/', 40), filesystem('/data', 92)]
    else if (path === '/network/interfaces') data = [{ is_default: true, state: 'up', addresses: [{ family: 'ipv4', address: '192.0.2.10' }] }]
    else if (path === '/docker/containers') data = containers
    else if (/\/docker\/containers\/[^/]+\/metrics$/.test(path)) data = [{ ts: metrics.timestamp - 60000, cpu_percent: 240, mem_percent: 25 }, { ts: metrics.timestamp, cpu_percent: 150, mem_percent: 30 }]
    else if (path.endsWith('/inspect')) data = { state: 'exited', hostname: 'api-container', image: 'api:latest', ports: [], mounts: [], networks: [], env: [] }
    else if (path === '/logs/read') data = { lines: url.searchParams.get('source') === 'syslog' ? ['Sep 23 09:00:00 demo-server service started'] : [] }
    else if (/batch|history|events/.test(path)) data = []
    await route.fulfill({ json: { success: true, data } })
  })
  return { sockets }
}
const panel = (page: Page) => page.getByRole('region', { name: 'Docker Containers', exact: true })

test('desktop prioritizes problems and shortcuts, with actionable cards and container details', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 })
  const { sockets } = await fixture(page)
  await page.goto('/dashboard')
  await expect(page.getByRole('heading', { name: 'demo-server' })).toBeVisible()
  const issues = page.getByRole('region', { name: 'Needs Attention', exact: true })
  await expect(issues).toContainText('Last backup failed')
  await expect(issues).toContainText('92%')
  await expect(issues).toContainText('1 container needs attention')
  const quick = page.getByRole('navigation', { name: 'Quick Actions' })
  const resources = page.getByRole('region', { name: 'Resources', exact: true })
  expect((await issues.boundingBox())!.y).toBeLessThan((await quick.boundingBox())!.y)
  expect((await quick.boundingBox())!.y).toBeLessThan((await resources.boundingBox())!.y)
  await expect(resources.getByRole('link', { name: /CPU Usage/ })).toHaveAttribute('href', '/processes')
  await expect(resources.getByRole('link', { name: /Disk/ })).toHaveAttribute('href', '/disk/filesystems')
  await expect(resources.getByRole('link', { name: /Network/ })).toHaveCount(1)
  await expect(resources.getByRole('link', { name: /Network/ }).getByRole('progressbar')).toHaveCount(0)
  await expect(page.locator('#server-details')).not.toHaveAttribute('open')
  await expect(page.locator('#resource-history').getByRole('img')).toBeVisible()
  await expect(panel(page).getByRole('link').nth(1)).toContainText('api-service')
  await expect.poll(() => sockets.length).toBeGreaterThan(0)
  await expect(page.getByText('Waiting for resource metrics', { exact: true })).toBeVisible()
  sockets.at(-1)!.send(JSON.stringify(metrics))
  sockets.at(-1)!.send(JSON.stringify({ ...metrics, timestamp: metrics.timestamp + 2000, net_bytes_sent: metrics.net_bytes_sent + 2048 }))
  await expect(page.getByText('Resource metrics live', { exact: true })).toBeVisible()
  await expect(resources.getByRole('link', { name: /Network/ })).toContainText('1.0 KB/s')
  await page.screenshot({ path: '/tmp/sfpanel-dashboard-desktop.png', fullPage: true })
  await panel(page).getByRole('link', { name: /api-service/ }).focus()
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/\/docker\/containers\?container=unhealthy-id$/)
  await expect(page.getByRole('dialog')).toContainText('api-service')
  await expect(page.getByRole('dialog')).toContainText('api-container')
  await page.keyboard.press('Escape')
  await expect(page.getByRole('dialog')).not.toBeVisible()
  await expect(page).toHaveURL(/\/docker\/containers$/)
})

for (const width of [360, 390, 768]) {
  test(`mobile ${width}px: no page overflow, accessible shortcuts and expandable details`, async ({ page }) => {
    await page.setViewportSize({ width, height: 844 })
    await fixture(page)
    await page.goto('/dashboard')
    await expect(panel(page).getByText('api-service')).toBeVisible()
    expect((await page.getByRole('navigation', { name: 'Quick Actions' }).boundingBox())!.y).toBeLessThan(450)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth && document.querySelector('main')!.scrollWidth <= document.querySelector('main')!.clientWidth)).toBeTruthy()
    if (width === 390) await page.screenshot({ path: '/tmp/sfpanel-dashboard-mobile-top.png' })
    const history = page.locator('#resource-history')
    if (width < 768) {
      await expect(history.getByRole('img')).not.toBeVisible()
      const toggle = history.getByRole('button', { name: 'Resource History', exact: true })
      await expect(toggle).toHaveAttribute('aria-expanded', 'false')
      await toggle.click()
      await expect(toggle).toHaveAttribute('aria-expanded', 'true')
      await expect(history.getByRole('img')).toBeVisible()
      expect((await history.locator('.uplot').boundingBox())!.width).toBeGreaterThan(100)
      await page.locator('#recent-logs').getByRole('button', { name: 'Recent Logs', exact: true }).click()
    }
    await expect(page.locator('#recent-logs').getByText(/service started/)).toBeVisible()
    await history.getByRole('button', { name: '4h', exact: true }).click()
    await expect(history.getByRole('button', { name: '4h', exact: true })).toHaveAttribute('aria-pressed', 'true')
    await page.locator('#server-details summary').click()
    await expect(page.locator('#server-details').getByText('6.8.0')).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth && document.querySelector('main')!.scrollWidth <= document.querySelector('main')!.clientWidth)).toBeTruthy()
    if (width === 390) await page.screenshot({ path: '/tmp/sfpanel-dashboard-mobile.png', fullPage: true })
  })
}

test('loading, failure, empty and recovery differ; a later error retains last successful data', async ({ page }) => {
  await page.clock.install()
  let mode: 'loading' | 'failed' | 'empty' | 'healthy' = 'loading'
  let release: (() => void) | undefined
  await fixture(page, async (route, url) => {
    if (!url.pathname.endsWith('/docker/containers')) return false
    if (mode === 'loading') await new Promise<void>((resolve) => { release = resolve })
    if (mode === 'failed') await route.fulfill({ status: 503, json: { success: false, error: 'Fixture unavailable' } })
    else await route.fulfill({ json: { success: true, data: mode === 'empty' ? [] : containers } })
    return true
  })
  await page.goto('/dashboard')
  await expect(panel(page).getByText('Loading...')).toBeVisible()
  await expect(panel(page).getByText('No containers', { exact: true })).toHaveCount(0)
  mode = 'failed'
  await expect.poll(() => !!release).toBeTruthy()
  release!()
  await expect(panel(page).getByText('Unable to load data')).toBeVisible()
  await expect(panel(page).getByText('No containers', { exact: true })).toHaveCount(0)
  mode = 'empty'
  await panel(page).getByRole('button', { name: 'Retry' }).click()
  await expect(panel(page).getByText('No containers', { exact: true })).toBeVisible()
  mode = 'healthy'
  await page.clock.fastForward(30001)
  await expect(panel(page).getByText('api-service')).toBeVisible()
  mode = 'failed'
  const lastUpdate = await panel(page).locator('time').getAttribute('datetime')
  await page.clock.fastForward(30001)
  await expect(panel(page).getByText('Refresh failed · showing last known data')).toBeVisible()
  await expect(panel(page).getByText('api-service')).toBeVisible()
  await expect(panel(page).locator('time')).toHaveAttribute('datetime', lastUpdate!)
  mode = 'healthy'
  await panel(page).getByRole('button', { name: 'Retry' }).click()
  await expect(panel(page).getByText('Refresh failed · showing last known data')).toHaveCount(0)
})

test('customized shortcuts persist across reloads', async ({ page }) => {
  await fixture(page)
  await page.goto('/dashboard')
  await page.getByRole('button', { name: 'Edit shortcuts' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('checkbox', { name: 'Logs', exact: true }).uncheck()
  await dialog.getByRole('checkbox', { name: 'Services', exact: true }).check()
  await dialog.getByRole('button', { name: 'Done', exact: true }).click()
  await page.reload()
  const shortcuts = page.getByRole('navigation', { name: 'Quick Actions' })
  await expect(shortcuts.getByRole('link', { name: 'Services', exact: true })).toBeVisible()
  await expect(shortcuts.getByRole('link', { name: 'Logs', exact: true })).toHaveCount(0)
  await shortcuts.getByRole('link', { name: 'Services', exact: true }).focus()
  await expect(shortcuts.getByRole('link', { name: 'Services', exact: true })).toBeFocused()
})

test('changing chart range retires older in-flight responses', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 })
  let release: (() => void) | undefined
  await fixture(page, async (route, url) => {
    if (!url.pathname.endsWith('/system/metrics-history')) return false
    if (url.searchParams.get('range') === '4h') {
      await new Promise<void>((resolve) => { release = resolve })
      await route.fulfill({ json: { success: true, data: [{ time: Date.now(), cpu: 99, mem_percent: 99, disk_percent: 99 }] } })
    } else await route.fulfill({ json: { success: true, data: [] } })
    return true
  })
  await page.goto('/dashboard')
  const history = page.locator('#resource-history')
  await expect(history.getByText('No history for this time range')).toBeVisible()
  await history.getByRole('button', { name: '4h', exact: true }).click()
  await expect.poll(() => !!release).toBeTruthy()
  await history.getByRole('button', { name: '12h', exact: true }).click()
  await expect(history.getByText('No history for this time range')).toBeVisible()
  const oldResponse = page.waitForResponse((response) => response.url().includes('/system/metrics-history?range=4h'))
  release!()
  await oldResponse
  await page.evaluate(() => new Promise(requestAnimationFrame))
  await expect(history.getByRole('button', { name: '12h', exact: true })).toHaveAttribute('aria-pressed', 'true')
  await expect(history.getByRole('img')).toHaveCount(0)
})

test('a missing container link explains the problem without opening another container', async ({ page }) => {
  await fixture(page)
  await page.goto('/docker/containers?container=missing')
  await expect(page.getByText(/The selected container was not found/)).toBeVisible()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.getByRole('button', { name: 'Dismiss', exact: true }).click()
  await expect(page).toHaveURL(/\/docker\/containers$/)
})

test('Korean mobile separates live metrics from failed sources and retries each source', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  let unavailable = true
  const failed = new Set(['/filesystems', '/system/backup/schedule', '/logs/read'])
  const { sockets } = await fixture(page, async (route, url) => {
    if (unavailable && failed.has(url.pathname.replace('/api/v1', ''))) {
      await route.fulfill({ status: 503, json: { success: false, error: 'Fixture unavailable' } })
      return true
    }
    return false
  })
  await page.addInitScript(() => localStorage.setItem('sfpanel_language', 'ko'))
  await page.goto('/dashboard')
  await expect(page.getByText('4개 항목을 갱신하지 못했습니다.', { exact: false })).toBeVisible()
  await expect.poll(() => sockets.length).toBeGreaterThan(0)
  sockets.at(-1)!.send(JSON.stringify(metrics))
  await expect(page.getByText('자원 수치 실시간', { exact: true })).toBeVisible()
  await page.screenshot({ path: '/tmp/sfpanel-dashboard-mobile-ko.png' })
  unavailable = false
  for (const region of [page.locator('#resources'), page.getByRole('region', { name: '백업', exact: true })]) {
    await region.getByRole('button', { name: '다시 시도' }).click()
    await expect(region.getByText('정보를 불러오지 못했습니다')).toHaveCount(0)
  }
  const logs = page.locator('#recent-logs')
  await logs.getByRole('button', { name: '다시 시도' }).first().click()
  await logs.getByRole('button', { name: '다시 시도' }).click()
  await expect(page.getByText(/개 항목을 갱신하지 못했습니다/)).toHaveCount(0)
  expect(await page.evaluate(() => document.querySelector('main')!.scrollWidth <= document.querySelector('main')!.clientWidth)).toBeTruthy()
  await page.locator('main').evaluate((element) => element.scrollTo(0, 0))
  await page.screenshot({ path: '/tmp/sfpanel-dashboard-mobile-ko-recovered.png' })
})

test('healthy resources and intentionally stopped containers do not raise an alarm', async ({ page }) => {
  await fixture(page, async (route, url) => {
    const path = url.pathname.replace('/api/v1', '')
    let data: unknown
    if (path === '/docker/containers') data = [{ ...containers[0], State: 'exited', Status: 'Exited (0) 2 hours ago' }]
    else if (path === '/filesystems') data = [filesystem('/', 40)]
    else if (path === '/system/backup/schedule') data = { schedule: { enabled: false, last_status: 'error' } }
    else return false
    await route.fulfill({ json: { success: true, data } })
    return true
  })
  await page.goto('/dashboard')
  await expect(panel(page).getByText('worker')).toBeVisible()
  await expect(page.getByRole('region', { name: 'Backup', exact: true })).toContainText('off')
  await expect(page.getByRole('region', { name: 'Needs Attention', exact: true })).toHaveCount(0)
})

test('charts support exact keyboard readouts, series selection, data tables, resizing and themes', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 })
  await fixture(page)
  await page.goto('/dashboard')
  const history = page.locator('#resource-history')
  const chart = history.getByRole('img')
  await expect(chart).toBeVisible()
  await chart.focus()
  await page.keyboard.press('Home')
  await expect(history.getByRole('button', { name: /CPU 20.0%/ })).toBeVisible()
  await page.keyboard.press('End')
  await expect(history.getByRole('button', { name: /CPU 29.0%/ })).toBeVisible()
  await history.getByRole('button', { name: /Memory 50.0%/ }).click()
  await expect(history.getByRole('button', { name: /Memory 50.0%/ })).toHaveAttribute('aria-pressed', 'false')
  await history.getByRole('button', { name: /Disk \(\/\)/ }).click()
  await expect(history.getByRole('button', { name: /CPU 29.0%/ })).toBeDisabled()
  await history.locator('summary').click()
  await expect(history.getByRole('table')).toBeVisible()
  await expect(history.getByRole('row')).toHaveCount(61)
  await page.evaluate(() => document.documentElement.classList.add('dark'))
  await expect(history.locator('.uplot')).toHaveCount(1)
  await history.getByRole('button', { name: '24h', exact: true }).click()
  await expect(history.getByRole('img')).toBeVisible()
  await page.setViewportSize({ width: 900, height: 1000 })
  await expect.poll(async () => (await history.locator('.uplot').boundingBox())!.width).toBeLessThan(650)
  await history.scrollIntoViewIfNeeded()
  await page.screenshot({ path: '/tmp/sfpanel-charts-dark.png' })
})

test('container graphs retain CPU above 100% and recover after range errors', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 1000 })
  let failed = true
  await fixture(page, async (route, url) => {
    if (url.pathname.endsWith('/healthy-id/metrics') && url.searchParams.get('range') === '6h' && failed) {
      await route.fulfill({ status: 503, json: { success: false, error: 'Fixture unavailable' } })
      return true
    }
    return false
  })
  await page.goto('/docker/containers?container=healthy-id')
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('tab', { name: 'History', exact: true }).click()
  await expect(dialog.getByText(/CPU 100% = one full core/)).toBeVisible()
  const chart = dialog.getByRole('img', { name: /Container resource history/ })
  await expect(chart).toHaveAttribute('aria-label', /0–250%/)
  await chart.focus()
  await page.keyboard.press('Home')
  await expect(dialog.getByRole('button', { name: /CPU \(per core\) 240.0%/ })).toBeVisible()
  await dialog.getByRole('button', { name: '6h', exact: true }).click()
  await expect(dialog.getByText('Unable to load data')).toBeVisible()
  await expect(dialog.getByText('No samples collected in this time range.')).toHaveCount(0)
  failed = false
  await dialog.getByRole('button', { name: 'Retry' }).click()
  await expect(chart).toHaveAttribute('aria-label', /0–250%/)
  await expect(dialog.getByText('Unable to load data')).toHaveCount(0)
  await page.screenshot({ path: '/tmp/sfpanel-container-chart.png' })
})

test.describe('chart touch controls', () => {
  test.use({ hasTouch: true, viewport: { width: 390, height: 844 } })
  test('a touch selects a sample while vertical swipes still scroll the page', async ({ page }) => {
    await fixture(page)
    await page.goto('/dashboard')
    const history = page.locator('#resource-history')
    await history.getByRole('button', { name: 'Resource History', exact: true }).click()
    const chart = history.getByRole('img')
    await chart.scrollIntoViewIfNeeded()
    const rect = (await history.locator('.u-over').boundingBox())!
    await page.touchscreen.tap(rect.x + rect.width / 3, rect.y + rect.height / 2)
    await expect(history.getByText('Selected time', { exact: true })).toBeVisible()
    const before = await page.locator('main').evaluate((element) => element.scrollTop)
    const session = await page.context().newCDPSession(page)
    const x = rect.x + rect.width / 2
    const y = rect.y + rect.height * 0.8
    await session.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] })
    for (const offset of [20, 45, 70, 100]) {
      await session.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x, y: y - offset }] })
    }
    await session.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
    await expect.poll(() => page.locator('main').evaluate((element) => element.scrollTop)).toBeGreaterThan(before)
  })
})
