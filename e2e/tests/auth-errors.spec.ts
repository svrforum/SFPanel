import { test, expect } from '@playwright/test'

// What the three sign-in screens say when something fails. A proxy's own 502
// page reached the login form as "HTTP 502", and an unreachable server as
// "Unable to connect to server" over the browser's "Failed to fetch": neither
// said where to look, and neither was read out. Mocked API.
test.use({ locale: 'en-US' })

test('a proxy error page at sign-in says where to look, and is read out', async ({ page }) => {
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname
    if (path.endsWith('/auth/login')) {
      await route.fulfill({ status: 502, contentType: 'text/html', body: '<html><body>Bad Gateway</body></html>' })
      return
    }
    await route.fulfill({ json: { success: true, data: path.endsWith('/auth/setup-status') ? { setup_required: false } : {} } })
  })
  await page.goto('/login')
  await expect(page).toHaveTitle('Sign In · SFPanel')
  await expect(page.getByRole('main')).toBeVisible()
  await page.getByLabel('Username').fill('admin')
  await page.getByLabel('Password').fill('a-password-here')
  await page.getByRole('button', { name: 'Sign In' }).click()
  await expect(page.getByRole('alert')).toHaveText('The panel could not answer (HTTP 502). Check the panel service and any reverse proxy in front of it, then try again.')
})

test('a server that cannot be reached says what to check, not the browser\'s words', async ({ page }) => {
  await page.route(/unreachable\.example\.invalid/, route => route.abort('connectionrefused'))
  await page.goto('/connect')
  await page.getByLabel('Server URL').fill('https://unreachable.example.invalid:3628')
  await page.getByRole('button', { name: 'Connect', exact: true }).click()
  const alert = page.getByRole('alert')
  await expect(alert).toHaveText('Could not reach the server. Check the address, the port and http/https, or run the Connection Diagnostic below.')
  await expect(alert).not.toContainText('Failed to fetch')
})

test('wrong credentials mark both fields; the 2FA step is a step, not an error', async ({ page }) => {
  let answer: { code: string; message: string } = { code: 'INVALID_CREDENTIALS', message: 'invalid credentials' }
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname
    if (path.endsWith('/auth/login')) {
      await route.fulfill({ status: 401, json: { success: false, error: answer } })
      return
    }
    await route.fulfill({ json: { success: true, data: path.endsWith('/auth/setup-status') ? { setup_required: false } : {} } })
  })
  await page.goto('/login')
  const username = page.getByLabel('Username')
  const password = page.getByLabel('Password')
  await expect(username).toHaveAttribute('autocomplete', 'username')
  await expect(password).toHaveAttribute('autocomplete', 'current-password')
  await username.fill('admin')
  await password.fill('a-password-here')
  await page.getByRole('button', { name: 'Sign In' }).click()
  await expect(page.getByRole('alert')).toHaveText('Invalid username or password')
  for (const field of [username, password]) {
    await expect(field).toHaveAttribute('aria-invalid', 'true')
    await expect(field).toHaveAccessibleDescription('Invalid username or password')
  }

  answer = { code: 'TOTP_REQUIRED', message: '2FA code required' }
  await page.getByRole('button', { name: 'Sign In' }).click()
  const code = page.getByLabel('2FA Code')
  await expect(code).toHaveAttribute('inputmode', 'numeric')
  await expect(code).toHaveAttribute('autocomplete', 'one-time-code')
  await expect(username).not.toHaveAttribute('aria-invalid', 'true')
  await expect(page.getByRole('alert')).not.toHaveClass(/text-destructive/)
})
