import { test, expect, type Page } from '@playwright/test'

// Settings → Account. A failed read of the 2FA state drew "Not Configured"
// and an Enable button for an account that has it on; turning 2FA off took a
// confirmation and two prompts in a row; and the password checks were toasts
// that left the field unmarked. Mocked API, standalone node.
test.use({ viewport: { width: 1280, height: 900 }, locale: 'en-US' })

type Sent = { disable: unknown[]; change: unknown[] }

async function mock(page: Page, { statusFails = () => false } = {}): Promise<Sent> {
  const sent: Sent = { disable: [], change: [] }
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const path = new URL(req.url()).pathname
    if (path.endsWith('/auth/2fa/status') && statusFails()) {
      await route.fulfill({ status: 500, json: { success: false, error: { code: 'INTERNAL_ERROR', message: 'database is locked' } } })
      return
    }
    let data: unknown = {}
    if (path.endsWith('/auth/setup-status')) data = { setup_required: false }
    else if (path.endsWith('/cluster/status')) data = { enabled: false }
    else if (path.endsWith('/auth/2fa/status')) data = { enabled: true }
    else if (path.endsWith('/auth/2fa/recovery/status')) data = { generated: true, remaining: 8 }
    else if (path.endsWith('/auth/2fa') && req.method() === 'DELETE') { sent.disable.push(req.postDataJSON()); data = { message: 'ok' } }
    else if (path.endsWith('/auth/change-password')) { sent.change.push(req.postDataJSON()); data = { message: 'ok' } }
    await route.fulfill({ json: { success: true, data } })
  })
  return sent
}

test('a 2FA state that could not be read says so, instead of offering to turn it on', async ({ page }) => {
  let fail = true
  await mock(page, { statusFails: () => fail })
  await page.goto('/settings')
  await expect(page.getByText('Could not load the 2FA status')).toBeVisible()
  await expect(page.getByText('Not Configured')).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Enable 2FA' })).toHaveCount(0)

  fail = false
  await page.getByRole('button', { name: 'Retry' }).click()
  await expect(page.getByText('Enabled', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Disable 2FA' })).toBeVisible()
})

test('turning 2FA off asks for the password and the code in one dialog', async ({ page }) => {
  const sent = await mock(page)
  await page.goto('/settings')
  await page.getByRole('button', { name: 'Disable 2FA' }).click()
  const dialog = page.getByRole('dialog', { name: 'Disable 2FA' })
  await dialog.getByLabel('Current Password').fill('a-password-here')
  const code = dialog.getByLabel('Verification Code')
  await expect(code).toHaveAttribute('inputmode', 'numeric')
  await code.fill('123')
  await dialog.getByRole('button', { name: 'Disable 2FA' }).click()
  // Short: refused beside the field, nothing sent.
  await expect(code).toHaveAttribute('aria-invalid', 'true')
  await expect(code).toBeFocused()
  await expect(dialog.getByText('Please enter a valid 6-digit code')).toBeVisible()
  expect(sent.disable).toEqual([])

  await code.fill('123456')
  await dialog.getByRole('button', { name: 'Disable 2FA' }).click()
  await expect(dialog).toBeHidden()
  expect(sent.disable).toEqual([{ password: 'a-password-here', totp_code: '123456' }])
})

test('a new password under 12 characters is refused beside its field', async ({ page }) => {
  const sent = await mock(page)
  await page.goto('/settings')
  await page.getByLabel('Current Password').fill('old-password-here')
  const next = page.getByLabel('New Password', { exact: true })
  await next.fill('short-pass')
  await page.getByLabel('Confirm New Password').fill('short-pass')
  await expect(page.getByText('Use at least 12 characters')).toBeVisible()
  await page.getByRole('button', { name: 'Change Password' }).click()
  await expect(next).toHaveAttribute('aria-invalid', 'true')
  await expect(next).toBeFocused()
  await expect(page.getByText('Password must be at least 12 characters')).toBeVisible()
  expect(sent.change).toEqual([])
})
