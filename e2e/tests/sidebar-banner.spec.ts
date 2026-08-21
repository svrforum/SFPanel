import { test, expect, Page } from '@playwright/test'
import { PW_USER, PW_PASS } from './helpers'

// Covers the sidebar brand: it links to /dashboard so clicking it always
// returns home, and it renders the mark plus — when expanded — the wordmark.
//
// The brand is now an inline <svg> from components/Logo with live text beside
// it, not a <img src="banner.png">, so these assert on the svg and on the
// wordmark text rather than on an image URL. Expanded and collapsed differ by
// whether the wordmark text is present.
//
// Scoped to <aside>, i.e. the standalone sidebar, which is what CI runs. The
// cluster sidebar renders its own brand in a <div> and drops it entirely when
// the tree is collapsed, so it is not covered here.
//
// Credentials come from PW_USER / PW_PASS (default admin / TestPass123!);
// the account must have no TOTP — the form fill below can't answer 2FA.

async function maybeLogin(page: Page) {
  await page.goto('/')
  await page.waitForLoadState('networkidle')
  // If we land on /login, sign in so the sidebar renders.
  if (page.url().includes('/login')) {
    await page.evaluate(() => localStorage.setItem('sfpanel_language', 'en'))
    await page.fill('input[type="text"], input[name="username"]', PW_USER)
    await page.fill('input[type="password"]', PW_PASS)
    await page.click('button[type="submit"]')
    await page.waitForURL(/\/(dashboard|setup)/, { timeout: 10000 })
  }
}

test.describe('Sidebar brand', () => {
  test('brand is visible inside the expanded sidebar', async ({ page }) => {
    await maybeLogin(page)
    // Force sidebar expanded (hidden md:flex + not collapsed)
    await page.evaluate(() => localStorage.setItem('sfpanel-sidebar-collapsed', 'false'))
    await page.goto('/settings')
    await page.waitForLoadState('networkidle')

    const brand = page.locator('aside a[aria-label="SFPanel"]').first()
    await expect(brand.locator('svg')).toBeVisible({ timeout: 5000 })
    // Expanded shows the wordmark as real text.
    await expect(brand).toContainText('SFPanel', { timeout: 5000 })
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

  test('collapsed sidebar shows the mark only, still links to /dashboard', async ({ page }) => {
    await maybeLogin(page)
    await page.evaluate(() => localStorage.setItem('sfpanel-sidebar-collapsed', 'true'))
    await page.goto('/settings')
    await page.waitForLoadState('networkidle')

    const brand = page.locator('aside a[aria-label="SFPanel"]').first()
    await expect(brand.locator('svg')).toBeVisible({ timeout: 5000 })
    // Collapsed is the mark only — no wordmark text.
    await expect(brand).toHaveText('', { timeout: 5000 })

    await brand.click()
    await page.waitForURL('**/dashboard', { timeout: 5000 })
    expect(page.url()).toContain('/dashboard')
  })
})
