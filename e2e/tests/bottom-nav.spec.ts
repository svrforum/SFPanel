import { test, expect, type Page } from '@playwright/test'

// An iPhone keeps the bottom 34px of the screen for its home indicator
// (safe-area-inset-bottom). The bar took that padding inside its fixed 56px
// row, which left 22px for a 22px icon and its label, and the icons shrank to
// 5px. Chromium stands in for the phone through DevTools' safe-area override.
// Mocked API, phone viewport.
test.use({ viewport: { width: 440, height: 956 }, isMobile: true, hasTouch: true })

async function mock(page: Page) {
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname
    let data: unknown = {}
    if (path.endsWith('/auth/setup-status')) data = { setup_required: false }
    else if (path.endsWith('/cluster/status')) data = { enabled: false }
    await route.fulfill({ json: { success: true, data } })
  })
}

test('the home indicator adds to the bar instead of squeezing its icons', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster')
  const more = page.getByRole('button', { name: 'More' })
  await expect(more).toBeVisible()
  const cdp = await page.context().newCDPSession(page)
  await cdp.send('Emulation.setSafeAreaInsetsOverride', { insets: { top: 0, bottom: 34, left: 0, right: 0 } })
  // The override reaches the page, so the bar below is measured against it.
  expect(await page.evaluate(() => {
    const probe = document.createElement('div')
    probe.style.paddingBottom = 'env(safe-area-inset-bottom)'
    document.body.append(probe)
    const inset = getComputedStyle(probe).paddingBottom
    probe.remove()
    return inset
  })).toBe('34px')

  const bar = page.getByRole('navigation').filter({ has: more })
  const icons = await bar.locator('svg').evaluateAll(els => els.map(el => Math.round(el.getBoundingClientRect().height)))
  expect(icons).toEqual([22, 22, 22, 22, 22])
  // Each tab keeps the whole 56px row, clear of the indicator.
  const box = (await more.boundingBox())!
  expect(Math.round(box.height)).toBe(56)
  expect(Math.round(box.y + box.height)).toBe(956 - 34)
})

test('the bar keeps its four tabs in order, then More', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster')
  const more = page.getByRole('button', { name: 'More' })
  await expect(more).toBeVisible()
  const bar = page.getByRole('navigation').filter({ has: more })
  await expect(bar.getByRole('link')).toHaveText(['Dashboard', 'Docker', 'Terminal', 'Logs'])
  // More is the last thing in the bar, after the four tabs.
  expect(await bar.locator('a, button').evaluateAll(els => els.map(el => `${el.tagName === 'A' ? 'link' : 'button'} ${el.textContent?.trim()}`)))
    .toEqual(['link Dashboard', 'link Docker', 'link Terminal', 'link Logs', 'button More'])
})

// The bar marked a tab by its address prefix, so the dashboard's Running here
// (/running) and Docker's App Store (/appstore), tabs at addresses of their
// own, marked nothing — and neither is in More. A page marks the tab of the
// menu entry it belongs to, as the sidebar and More do.
test('the open tab is the one the page belongs to', async ({ page }) => {
  await mock(page)
  const bar = page.getByRole('navigation').filter({ has: page.getByRole('button', { name: 'More' }) })
  for (const [path, tab] of [['/running', 'Dashboard'], ['/appstore', 'Docker'], ['/docker/containers', 'Docker'], ['/dashboard', 'Dashboard'], ['/logs', 'Logs']]) {
    await page.goto(path)
    await expect(bar.getByRole('link', { name: tab }), path).toHaveAttribute('aria-current', 'page')
    await expect(bar.locator('a[aria-current="page"]'), path).toHaveCount(1)
  }
  // A page none of the four owns marks none.
  await page.goto('/files')
  await expect(page.getByRole('button', { name: 'More' })).toBeVisible()
  await expect(bar.locator('a[aria-current="page"]')).toHaveCount(0)
})
