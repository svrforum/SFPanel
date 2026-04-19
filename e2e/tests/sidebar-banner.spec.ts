import { test, expect, Page } from '@playwright/test'

// Covers the recent UI change that replaced the favicon + text brand in
// the sidebar with the product banner, and made the banner a link to
// /dashboard so clicking it always returns home.
//
// Tests are resilient to either standalone or cluster sidebar because both
// render the banner as the link target with aria-label "SFPanel".

const ADMIN_USER = 'admin'
const ADMIN_PASS = 'TestPass123!'

async function maybeLogin(page: Page) {
  await page.goto('/')
  await page.waitForLoadState('networkidle')
  // If we land on /login, sign in so the sidebar renders.
  if (page.url().includes('/login')) {
    await page.evaluate(() => localStorage.setItem('sfpanel_language', 'en'))
    await page.fill('input[type="text"], input[name="username"]', ADMIN_USER)
    await page.fill('input[type="password"]', ADMIN_PASS)
    await page.click('button[type="submit"]')
    await page.waitForURL(/\/(dashboard|setup)/, { timeout: 10000 })
  }
}

test.describe('Sidebar banner', () => {
  test('banner is visible inside the expanded sidebar', async ({ page }) => {
    await maybeLogin(page)
    // Force sidebar expanded (hidden md:flex + not collapsed)
    await page.evaluate(() => localStorage.setItem('sfpanel-sidebar-collapsed', 'false'))
    await page.goto('/settings')
    await page.waitForLoadState('networkidle')

    const banner = page.locator('aside a[aria-label="SFPanel"] img[src="/banner.png"]').first()
    await expect(banner).toBeVisible({ timeout: 5000 })
  })

  test('clicking banner navigates to /dashboard', async ({ page }) => {
    await maybeLogin(page)
    await page.evaluate(() => localStorage.setItem('sfpanel-sidebar-collapsed', 'false'))
    await page.goto('/settings')
    await page.waitForLoadState('networkidle')

    // Click the sidebar banner link (aria-label="SFPanel").
    await page.locator('aside a[aria-label="SFPanel"]').first().click()
    await page.waitForURL('**/dashboard', { timeout: 5000 })
    expect(page.url()).toContain('/dashboard')
  })

  test('collapsed sidebar shows favicon, still links to /dashboard', async ({ page }) => {
    await maybeLogin(page)
    await page.evaluate(() => localStorage.setItem('sfpanel-sidebar-collapsed', 'true'))
    await page.goto('/settings')
    await page.waitForLoadState('networkidle')

    const favicon = page.locator('aside a[aria-label="SFPanel"] img[src="/favicon.png"]').first()
    await expect(favicon).toBeVisible({ timeout: 5000 })

    await favicon.click()
    await page.waitForURL('**/dashboard', { timeout: 5000 })
    expect(page.url()).toContain('/dashboard')
  })
})
