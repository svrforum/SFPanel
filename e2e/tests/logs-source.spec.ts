import { test, expect, type Page } from '@playwright/test'

// Firewall › Logs is gone: its address opens the Logs screen on the firewall
// source, and the Logs screen says in its address which source it shows
// (`?source=`), rewriting only that parameter in place. REST is a fixture;
// `reads` records which source each log read asked for.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const sources = [
  { id: 'syslog', name: 'System Log', path: '/var/log/syslog', size: 4096, exists: true, custom: false },
  { id: 'firewall', name: 'Firewall', path: '/var/log/ufw.log', size: 2048, exists: true, custom: false },
  { id: 'fail2ban', name: 'Fail2ban', path: '/var/log/fail2ban.log', size: 0, exists: false, custom: false },
]

async function fixture(page: Page) {
  const reads: string[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const url = new URL(route.request().url())
    const p = url.pathname.replace('/api/v1', '')
    let data: unknown = {}
    if (p === '/auth/setup-status') data = { setup_required: false }
    else if (p === '/cluster/status') data = { enabled: false }
    else if (p === '/logs/sources') data = sources
    else if (p === '/logs/read') {
      reads.push(url.searchParams.get('source') ?? '')
      data = { lines: [], total_lines: 0 }
    }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  return reads
}

test('an old firewall-logs address opens the logs screen on the firewall source', async ({ page }) => {
  const reads = await fixture(page)
  await page.goto('/cluster/overview')
  await page.goto('/firewall/logs')
  await expect(page).toHaveURL(/\/logs\?source=firewall$/)
  await expect.poll(() => reads).toEqual(['firewall'])
  // Replaced, not pushed: Back leaves the screen instead of bouncing off the
  // old address back onto it.
  await page.goBack()
  await expect(page).toHaveURL(/\/cluster\/overview$/)
})

test('the firewall tabs have no logs tab', async ({ page }) => {
  await fixture(page)
  await page.goto('/firewall/rules')
  const main = page.getByRole('main')
  await expect(main.getByRole('link', { name: 'Firewall Rules', exact: true })).toBeVisible()
  await expect(main.getByRole('link', { name: 'Logs', exact: true })).toHaveCount(0)
})

test('an unknown source falls back without asking for it', async ({ page }) => {
  const reads = await fixture(page)
  await page.goto('/logs?source=nope')
  await expect.poll(() => reads).toEqual(['syslog'])
  await expect(page).toHaveURL(/source=syslog/)
})

test('a source this server does not have is not read', async ({ page }) => {
  const reads = await fixture(page)
  await page.goto('/logs?source=fail2ban')
  await expect.poll(() => reads).toEqual(['syslog'])
  expect(reads).not.toContain('fail2ban')
  await expect(page).toHaveURL(/source=syslog/)
})

test('choosing a source rewrites only that parameter', async ({ page }) => {
  const reads = await fixture(page)
  await page.goto('/logs?source=syslog&keep=1')
  await expect.poll(() => reads).toEqual(['syslog'])
  const before = await page.evaluate(() => history.length)
  await page.getByRole('main').getByRole('button', { name: /Firewall/ }).click()
  await expect.poll(() => reads).toEqual(['syslog', 'firewall'])
  await expect.poll(() => {
    const params = new URL(page.url()).searchParams
    return [params.get('source'), params.get('keep')]
  }).toEqual(['firewall', '1'])
  expect(await page.evaluate(() => history.length)).toBe(before)
})

test('a bare logs address takes back the source on screen', async ({ page }) => {
  const reads = await fixture(page)
  await page.goto('/logs?source=firewall')
  await expect.poll(() => reads).toEqual(['firewall'])
  const before = await page.evaluate(() => history.length)
  // The menu's own Logs link, not one in "Other records": it pushes a bare /logs.
  await page.getByRole('complementary').getByRole('navigation').getByRole('link', { name: 'Logs', exact: true }).click()
  await expect(page).toHaveURL(/\/logs\?source=firewall$/)
  // The link's own push is the one new entry; the source is written back in place.
  expect(await page.evaluate(() => history.length)).toBe(before + 1)
  // Firewall stays on screen: nothing else is read.
  await expect(page.getByRole('main').getByRole('button', { name: /Firewall/ })).toHaveClass(/bg-primary\/10/)
  expect(reads).toEqual(['firewall'])
})

test('on a phone, back to the list leaves no source in the address', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const reads = await fixture(page)
  await page.goto('/logs?source=firewall&keep=1')
  await expect.poll(() => reads).toEqual(['firewall'])
  await page.getByRole('main').getByRole('button', { name: 'Back', exact: true }).click()
  await expect(page.getByRole('main').getByRole('button', { name: /System Log/ })).toBeVisible()
  // Nothing is on screen, so the write-back above must not put a source back.
  await expect(page).toHaveURL(/\/logs\?keep=1$/)
})

test('other records are a link away', async ({ page }) => {
  await fixture(page)
  await page.goto('/logs')
  // The sidebar has links by the same names: only these three count.
  const other = page.getByRole('navigation', { name: 'Other records' })
  await expect(other.getByRole('link', { name: 'Services', exact: true })).toHaveAttribute('href', '/services')
  await expect(other.getByRole('link', { name: 'Scheduled Tasks', exact: true })).toHaveAttribute('href', '/cron')
  await expect(other.getByRole('link', { name: 'Audit Log', exact: true })).toHaveAttribute('href', '/settings?scope=node&tab=audit')
})
