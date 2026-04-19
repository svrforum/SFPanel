import { test, expect } from '@playwright/test'

// Covers the default-port change from 8443 → 19443. The Connect page
// (Tauri / web bootstrap URL picker) shows the new port in its placeholder
// and quick-fill examples so new users try the right port first.

test.describe('Connect page port default', () => {
  test('placeholder advertises port 19443', async ({ page }) => {
    await page.goto('/connect')
    await page.waitForLoadState('networkidle')

    const input = page.locator('input#server-url')
    await expect(input).toBeVisible()
    const placeholder = await input.getAttribute('placeholder')
    expect(placeholder).toContain(':19443')
    expect(placeholder).not.toContain(':8443')
  })

  test('example buttons use port 19443', async ({ page }) => {
    await page.goto('/connect')
    await page.waitForLoadState('networkidle')

    // Examples are rendered as buttons whose text is the example URL.
    const exampleButtons = page.locator('button', { hasText: ':19443' })
    await expect(exampleButtons.first()).toBeVisible({ timeout: 5000 })
    expect(await exampleButtons.count()).toBeGreaterThanOrEqual(1)

    const legacyButtons = page.locator('button', { hasText: ':8443' })
    expect(await legacyButtons.count()).toBe(0)
  })
})
