import { test, expect, type Page } from '@playwright/test'

// The app detail dialog renders the app's upstream README. Its relative
// addresses resolved against the panel (404s), a badge inside a link came out
// as its markdown, every re-render rebuilt its images (hundreds of repeated
// 404s while an install streamed), and a long code line pushed the dialog's
// content past its right edge. REST is a fixture; the README host is routed.
test.use({ viewport: { width: 1440, height: 900 }, locale: 'en-US' })

const RAW = 'https://raw.githubusercontent.com/example/kuma/master/'
const README = [
  '<div align="center">',
  '  <img src="./public/icon.svg" width="128" alt="Example Logo" />',
  '</div>',
  '',
  '# Example',
  '',
  '[![Sponsors](https://img.shields.io/github/sponsors/example)](https://github.com/sponsors/example) [Quick start](#-quick-start)',
  '',
  '```bash',
  'docker run -d --restart=always -p 3001:3001 -v example-data:/app/data --name example-service --health-cmd "curl -f http://localhost:3001" example/image:2',
  '```',
  '',
].join('\n')

function app(id: string, extra: Record<string, unknown> = {}) {
  return {
    id, name: id, description: { ko: '예시', en: 'Example' }, category: 'monitoring', version: '1.0.0',
    website: 'https://example.com', source: 'https://github.com/example/kuma', ports: [3001],
    env: [{ key: 'PORT', label: { ko: '외부 포트', en: 'External Port' }, type: 'port', default: '3001' }],
    features: [
      { title: { ko: '가', en: 'Alerts' }, description: { ko: '가', en: 'Alerts over many channels' } },
      { title: { ko: '나', en: 'Status pages' }, description: { ko: '나', en: 'Public status pages' } },
    ],
    installed: false,
    ...extra,
  }
}

async function mock(page: Page, detail: Record<string, unknown>) {
  const readmeRequests: string[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '')
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: false }
    else if (path === `/appstore/apps/${(detail.app as { id: string }).id}`) data = detail
    else if (path === '/appstore/apps') data = [detail.app]
    else if (path === '/appstore/categories' || path.startsWith('/appstore/installed')) data = []
    await route.fulfill({ json: { success: true, data } })
  })
  await page.route(`${RAW}**`, async route => { readmeRequests.push(route.request().url()); await route.fulfill({ status: 404, body: '' }) })
  await page.route('https://img.shields.io/**', route => route.fulfill({ status: 200, contentType: 'image/svg+xml', body: '<svg xmlns="http://www.w3.org/2000/svg" width="80" height="20"/>' }))
  await page.routeWebSocket(/\/ws\//, () => {})
  return readmeRequests
}

test('the README resolves against its repository and renders a linked badge', async ({ page }) => {
  const panelPaths: string[] = []
  page.on('request', r => { const u = new URL(r.url()); if (u.origin === new URL(page.url() || 'http://x').origin && u.pathname.includes('/public/icon.svg')) panelPaths.push(u.pathname) })
  const requests = await mock(page, { app: app('example'), compose: 'services: {}\n', readme: README, readme_base_url: RAW, installed: false })
  await page.goto('/appstore/example')
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('heading', { name: 'Example', exact: true }).last()).toBeVisible()
  await expect.poll(() => requests).toContain(`${RAW}public/icon.svg`)
  expect(panelPaths).toEqual([])
  await expect(dialog.locator('img[src="https://img.shields.io/github/sponsors/example"]')).toHaveCount(1)
  await expect(dialog).not.toContainText('![Sponsors]')
  await expect(dialog.getByText('Quick start')).toBeVisible()
  await expect(dialog.locator('a[href^="#"]')).toHaveCount(0)
})

test('re-rendering the dialog does not request the README images again', async ({ page }) => {
  const requests = await mock(page, { app: app('example'), compose: 'services: {}\n', readme: README, readme_base_url: RAW, installed: false })
  await page.goto('/appstore/example')
  await expect.poll(() => requests.length).toBe(1)
  const toggle = page.getByRole('dialog').getByRole('button', { name: /Docker Compose/ })
  for (let i = 0; i < 4; i++) await toggle.click()
  await page.waitForTimeout(500)
  expect(requests).toEqual([`${RAW}public/icon.svg`])
})

test('a long README line does not push the dialog past its right edge', async ({ page }) => {
  await mock(page, { app: app('example'), compose: 'services: {}\n', readme: README, readme_base_url: RAW, installed: false })
  await page.goto('/appstore/example')
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('button', { name: /Docker Compose/ })).toBeVisible()
  const sizes = await dialog.evaluate(d => ({ scroll: d.scrollWidth, client: d.clientWidth, right: d.getBoundingClientRect().right, content: (d.querySelector('.space-y-6') as HTMLElement).getBoundingClientRect().right }))
  expect(sizes.scroll).toBeLessThanOrEqual(sizes.client)
  expect(sizes.content).toBeLessThanOrEqual(sizes.right)
})

// The address is the app's own: an app that serves TLS itself opens at
// https:// though this panel is plain HTTP.
test('an app that serves TLS itself is opened over https', async ({ page }) => {
  const tls = app('tls-app', { scheme: 'https', ports: [81], env: [{ key: 'UI_PORT', label: { ko: 'UI', en: 'UI port' }, type: 'port', default: '81' }] })
  await mock(page, { app: tls, compose: 'services: {}\n', readme: '', installed: false })
  await page.goto('/appstore/tls-app')
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('button', { name: /^Install/ }).first().click()
  await expect(dialog.getByText(/^https:\/\/localhost:81$/)).toBeVisible()
})
