import { test, expect, Page } from '@playwright/test'

const ADMIN_USER = 'admin'
const ADMIN_PASS = 'TestPass123!'

// Force English locale for consistent test selectors
async function setEnglishLocale(page: Page) {
  await page.goto('/')
  await page.evaluate(() => localStorage.setItem('sfpanel_language', 'en'))
}

// Helper: login and return authenticated page
async function login(page: Page) {
  await setEnglishLocale(page)
  await page.goto('/login')
  await page.fill('input[type="text"], input[name="username"], input[placeholder*="user" i], input[placeholder*="아이디" i]', ADMIN_USER)
  await page.fill('input[type="password"]', ADMIN_PASS)
  await page.click('button[type="submit"]')
  await page.waitForURL(/\/(dashboard|setup)/, { timeout: 10000 })
}

// ===================================================
// 1. SETUP WIZARD — First Run
// ===================================================
test.describe('1. Setup Wizard', () => {
  test('should show setup page on first visit', async ({ page }) => {
    await setEnglishLocale(page)
    await page.goto('/')
    // Should redirect to /setup since no admin exists
    await page.waitForURL(/\/(setup|login)/, { timeout: 10000 })
    const url = page.url()
    expect(url).toMatch(/\/(setup|login)/)
  })

  test('should create admin account via setup wizard', async ({ page }) => {
    await setEnglishLocale(page)
    await page.goto('/setup')
    await page.waitForLoadState('networkidle')

    // Find and fill username field
    const usernameInput = page.locator('input').first()
    await usernameInput.clear()
    await usernameInput.fill(ADMIN_USER)

    // Fill password fields
    const passwordInputs = page.locator('input[type="password"]')
    await passwordInputs.nth(0).fill(ADMIN_PASS)
    if (await passwordInputs.count() > 1) {
      await passwordInputs.nth(1).fill(ADMIN_PASS)
    }

    // Submit
    await page.click('button[type="submit"]')

    // Should redirect to dashboard after successful setup
    await page.waitForURL('**/dashboard', { timeout: 10000 })
    expect(page.url()).toContain('/dashboard')
  })

  test('setup should not be available after admin is created', async ({ page }) => {
    const res = await page.request.get('/api/v1/auth/setup-status')
    const json = await res.json()
    expect(json.success).toBe(true)
    expect(json.data.setup_required).toBe(false)
  })
})

// ===================================================
// 2. LOGIN FLOW
// ===================================================
test.describe('2. Login', () => {
  test('should reject invalid credentials', async ({ page }) => {
    await page.goto('/login')
    await page.waitForLoadState('networkidle')

    const usernameInput = page.locator('input').first()
    await usernameInput.fill(ADMIN_USER)
    await page.locator('input[type="password"]').first().fill('wrongpassword')
    await page.click('button[type="submit"]')

    // Should stay on login page and show error
    await page.waitForTimeout(1000)
    expect(page.url()).toContain('/login')
  })

  test('should login with valid credentials', async ({ page }) => {
    await login(page)
    expect(page.url()).toContain('/dashboard')
  })

  test('should redirect unauthenticated users to login', async ({ page }) => {
    // Clear any stored token
    await page.goto('/login')
    await page.evaluate(() => localStorage.clear())
    await page.goto('/dashboard')
    await page.waitForURL('**/login', { timeout: 10000 })
    expect(page.url()).toContain('/login')
  })
})

// ===================================================
// 3. DASHBOARD
// ===================================================
test.describe('3. Dashboard', () => {
  test.beforeEach(async ({ page }) => {
    await login(page)
  })

  test('should display host information', async ({ page }) => {
    await page.waitForLoadState('networkidle')

    // Check that system info section loads
    // Look for common host info keywords
    const content = await page.textContent('body')
    expect(content).toBeTruthy()

    // Verify API returns valid system info
    const token = await page.evaluate(() => localStorage.getItem('token'))
    const res = await page.request.get('/api/v1/system/info', {
      headers: { Authorization: `Bearer ${token}` },
    })
    const json = await res.json()
    expect(json.success).toBe(true)
    expect(json.data.host.hostname).toBeTruthy()
    expect(json.data.host.os).toBe('linux')
    expect(json.data.metrics.cpu).toBeGreaterThanOrEqual(0)
    expect(json.data.metrics.mem_total).toBeGreaterThan(0)
  })

  test('should show metrics cards', async ({ page }) => {
    await page.waitForLoadState('networkidle')
    // Wait a bit for WebSocket data to arrive
    await page.waitForTimeout(3000)

    // Check that the page has loaded meaningful content
    // Look for percentage values (e.g., "45.2%") or metric labels
    const body = await page.textContent('body')
    // Should have some content that looks like metrics
    expect(body?.length).toBeGreaterThan(100)
  })
})

// ===================================================
// 4. DOCKER — Containers
// ===================================================
test.describe('4. Docker Containers', () => {
  test.beforeEach(async ({ page }) => {
    await login(page)
    await page.goto('/docker')
    await page.waitForLoadState('networkidle')
  })

  test('should display Docker page with tabs', async ({ page }) => {
    // Check for Docker tabs
    const tabs = page.locator('[role="tablist"]')
    await expect(tabs).toBeVisible({ timeout: 5000 })

    // Verify tab names exist
    const tabTexts = await page.locator('[role="tab"]').allTextContents()
    expect(tabTexts.some(t => t.includes('Container'))).toBe(true)
    expect(tabTexts.some(t => t.includes('Image'))).toBe(true)
    expect(tabTexts.some(t => t.includes('Volume'))).toBe(true)
    expect(tabTexts.some(t => t.includes('Network'))).toBe(true)
    expect(tabTexts.some(t => t.includes('Compose'))).toBe(true)
  })

  test('should list running containers', async ({ page }) => {
    // Containers tab should be active by default
    await page.waitForTimeout(2000)

    // Verify via API that containers exist
    const token = await page.evaluate(() => localStorage.getItem('token'))
    const res = await page.request.get('/api/v1/docker/containers', {
      headers: { Authorization: `Bearer ${token}` },
    })
    const json = await res.json()
    expect(json.success).toBe(true)
    expect(json.data.length).toBeGreaterThan(0)

    // Check that container names appear on the page
    const body = await page.textContent('body')
    // At least one container name should be visible
    const firstContainer = json.data[0]
    const containerName = firstContainer.Names[0].replace('/', '')
    expect(body).toContain(containerName)
  })

  test('should show container action buttons', async ({ page }) => {
    await page.waitForTimeout(2000)
    // Look for action buttons (start/stop/restart/delete)
    const buttons = page.locator('button, [role="button"]')
    const count = await buttons.count()
    expect(count).toBeGreaterThan(0)
  })
})

// ===================================================
// 5. DOCKER — Images
// ===================================================
test.describe('5. Docker Images', () => {
  test.beforeEach(async ({ page }) => {
    await login(page)
    await page.goto('/docker')
    await page.waitForLoadState('networkidle')
  })

  test('should list Docker images', async ({ page }) => {
    // Click Images tab
    await page.click('[role="tab"]:has-text("Image")')
    await page.waitForTimeout(2000)

    // Verify via API
    const token = await page.evaluate(() => localStorage.getItem('token'))
    const res = await page.request.get('/api/v1/docker/images', {
      headers: { Authorization: `Bearer ${token}` },
    })
    const json = await res.json()
    expect(json.success).toBe(true)
    expect(json.data.length).toBeGreaterThan(0)
  })
})

// ===================================================
// 6. DOCKER — Volumes
// ===================================================
test.describe('6. Docker Volumes', () => {
  test.beforeEach(async ({ page }) => {
    await login(page)
    await page.goto('/docker')
    await page.waitForLoadState('networkidle')
  })

  test('should list Docker volumes', async ({ page }) => {
    await page.click('[role="tab"]:has-text("Volume")')
    await page.waitForTimeout(2000)

    const token = await page.evaluate(() => localStorage.getItem('token'))
    const res = await page.request.get('/api/v1/docker/volumes', {
      headers: { Authorization: `Bearer ${token}` },
    })
    const json = await res.json()
    expect(json.success).toBe(true)
    expect(Array.isArray(json.data)).toBe(true)
  })
})

// ===================================================
// 7. DOCKER — Networks
// ===================================================
test.describe('7. Docker Networks', () => {
  test.beforeEach(async ({ page }) => {
    await login(page)
    await page.goto('/docker')
    await page.waitForLoadState('networkidle')
  })

  test('should list Docker networks', async ({ page }) => {
    await page.click('[role="tab"]:has-text("Network")')
    await page.waitForTimeout(2000)

    const token = await page.evaluate(() => localStorage.getItem('token'))
    const res = await page.request.get('/api/v1/docker/networks', {
      headers: { Authorization: `Bearer ${token}` },
    })
    const json = await res.json()
    expect(json.success).toBe(true)
    expect(json.data.length).toBeGreaterThan(0)
  })
})

// ===================================================
// 8. DOCKER — Compose
// ===================================================
test.describe('8. Docker Compose', () => {
  test.beforeEach(async ({ page }) => {
    await login(page)
    await page.goto('/docker')
    await page.waitForLoadState('networkidle')
  })

  test('should show Compose tab with project list', async ({ page }) => {
    await page.click('[role="tab"]:has-text("Compose")')
    await page.waitForTimeout(2000)

    const token = await page.evaluate(() => localStorage.getItem('token'))
    const res = await page.request.get('/api/v1/docker/compose', {
      headers: { Authorization: `Bearer ${token}` },
    })
    const json = await res.json()
    expect(json.success).toBe(true)
    expect(Array.isArray(json.data)).toBe(true)
  })
})

// ===================================================
// 9. SITES
// ===================================================
test.describe('9. Sites Management', () => {
  test.beforeEach(async ({ page }) => {
    await login(page)
    await page.goto('/sites')
    await page.waitForLoadState('networkidle')
  })

  test('should display Sites page', async ({ page }) => {
    const body = await page.textContent('body')
    expect(body).toBeTruthy()
    // Page should load without errors
    const url = page.url()
    expect(url).toContain('/sites')
  })

  test('should show empty sites list initially', async ({ page }) => {
    const token = await page.evaluate(() => localStorage.getItem('token'))
    const res = await page.request.get('/api/v1/sites', {
      headers: { Authorization: `Bearer ${token}` },
    })
    const json = await res.json()
    expect(json.success).toBe(true)
    expect(json.data).toEqual([])
  })
})

// ===================================================
// 10. SETTINGS
// ===================================================
test.describe('10. Settings', () => {
  test.beforeEach(async ({ page }) => {
    await login(page)
    await page.goto('/settings')
    await page.waitForLoadState('networkidle')
  })

  test('should display Settings page', async ({ page }) => {
    const url = page.url()
    expect(url).toContain('/settings')

    // Should show password change section
    const body = await page.textContent('body')
    expect(body).toBeTruthy()
  })

  test('should reject password change with wrong current password', async ({ page }) => {
    const token = await page.evaluate(() => localStorage.getItem('token'))
    const res = await page.request.post('/api/v1/auth/change-password', {
      headers: {
        Authorization: `Bearer ${token}`,
        'Content-Type': 'application/json',
      },
      data: {
        current_password: 'wrongpassword',
        new_password: 'NewPass456!',
      },
    })
    const json = await res.json()
    expect(json.success).toBe(false)
  })

  test('should change password successfully via API', async ({ page }) => {
    const token = await page.evaluate(() => localStorage.getItem('token'))
    const newPass = 'NewPass456!'

    // Change password
    const res = await page.request.post('/api/v1/auth/change-password', {
      headers: {
        Authorization: `Bearer ${token}`,
        'Content-Type': 'application/json',
      },
      data: {
        current_password: ADMIN_PASS,
        new_password: newPass,
      },
    })
    const json = await res.json()
    expect(json.success).toBe(true)

    // Verify login with new password works
    const loginRes = await page.request.post('/api/v1/auth/login', {
      headers: { 'Content-Type': 'application/json' },
      data: {
        username: ADMIN_USER,
        password: newPass,
      },
    })
    const loginJson = await loginRes.json()
    expect(loginJson.success).toBe(true)
    expect(loginJson.data.token).toBeTruthy()

    // Change password back for other tests
    const revertRes = await page.request.post('/api/v1/auth/change-password', {
      headers: {
        Authorization: `Bearer ${loginJson.data.token}`,
        'Content-Type': 'application/json',
      },
      data: {
        current_password: newPass,
        new_password: ADMIN_PASS,
      },
    })
    expect((await revertRes.json()).success).toBe(true)
  })
})

// ===================================================
// 11. NAVIGATION
// ===================================================
test.describe('11. Navigation', () => {
  test.beforeEach(async ({ page }) => {
    await login(page)
  })

  test('should navigate between all pages via sidebar', async ({ page }) => {
    // Dashboard
    await page.click('a[href="/dashboard"], nav >> text=Dashboard')
    await page.waitForURL('**/dashboard')
    expect(page.url()).toContain('/dashboard')

    // Docker
    await page.click('a[href="/docker"], nav >> text=Docker')
    await page.waitForURL('**/docker')
    expect(page.url()).toContain('/docker')

    // Sites
    await page.click('a[href="/sites"], nav >> text=Sites')
    await page.waitForURL('**/sites')
    expect(page.url()).toContain('/sites')

    // Settings
    await page.click('a[href="/settings"], nav >> text=Settings')
    await page.waitForURL('**/settings')
    expect(page.url()).toContain('/settings')
  })
})

// ===================================================
// 12. API SECURITY
// ===================================================
test.describe('12. API Security', () => {
  test('should reject requests without token', async ({ request }) => {
    const endpoints = [
      '/api/v1/system/info',
      '/api/v1/docker/containers',
      '/api/v1/docker/images',
      '/api/v1/sites',
    ]

    for (const endpoint of endpoints) {
      const res = await request.get(endpoint)
      const json = await res.json()
      expect(json.success).toBe(false)
      expect(json.error.code).toMatch(/MISSING_TOKEN|INVALID_TOKEN/)
    }
  })

  test('should reject requests with invalid token', async ({ request }) => {
    const res = await request.get('/api/v1/system/info', {
      headers: { Authorization: 'Bearer invalid.token.here' },
    })
    const json = await res.json()
    expect(json.success).toBe(false)
  })

  test('health endpoint should be public', async ({ request }) => {
    const res = await request.get('/api/v1/health')
    const json = await res.json()
    expect(json.success).toBe(true)
  })
})
