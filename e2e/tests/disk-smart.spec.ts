import { test, expect, type Page } from '@playwright/test'

// Starting a SMART self-test while the drive still ran one answered with
// smartctl's banner and "Can't start self-test without aborting current test",
// shown as a green "started" notice (reported on the forum). The server now
// answers 409 SMART_TEST_IN_PROGRESS, and the dialog shows the running test.
// REST is a fixture; `starts` counts the self-test requests.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const disk = { name: 'sda', size: 512110190592, type: 'disk', fstype: '', mountpoint: '', model: 'Example SSD', serial: 'X1', rotational: false, readonly: false, transport: 'sata', state: 'running', vendor: 'ATA', children: [] }

function smart(extra: Record<string, unknown> = {}) {
  return { device_path: '/dev/sda', model_name: 'Example SSD', serial_number: 'X1', firmware_version: '1', healthy: true, smart_supported: true, temperature: 30, power_on_hours: 1200, attributes: [], self_tests: [], ...extra }
}

async function mock(page: Page, info: Record<string, unknown>, start: { status: number; json: unknown }) {
  const starts: string[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace('/api/v1', '')
    if (path === '/disks/sda/smart/test' && req.method() === 'POST') {
      starts.push(req.postData() ?? '')
      await route.fulfill({ status: start.status, json: start.json })
      return
    }
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: false }
    else if (path === '/disks/overview') data = [disk]
    else if (path === '/disks/sda/smart') data = info
    else if (path.startsWith('/disks/smartmontools')) data = { installed: true }
    else if (path === '/disks/iostat') data = []
    else if (path === '/docker/volumes') data = []
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/disk/overview')
  await page.getByRole('button', { name: 'S.M.A.R.T.' }).click()
  return starts
}

test('a self-test running on the drive is shown, and no other can be started', async ({ page }) => {
  const starts = await mock(page, smart({ self_test_running: true, self_test_remaining: 74 }), { status: 200, json: { success: true, data: {} } })
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByText('Self-test running on the drive — 74% left')).toBeVisible()
  await expect(dialog.getByRole('button', { name: 'Short test' })).toBeDisabled()
  await expect(dialog.getByRole('button', { name: 'Long test' })).toBeDisabled()
  expect(starts).toEqual([])
})

test('a refused start says a test is running, not that one started', async ({ page }) => {
  await mock(page, smart(), {
    status: 409,
    json: { success: false, error: { code: 'SMART_TEST_IN_PROGRESS', message: "A self-test is already running on this device: Can't start self-test without aborting current test (74% remaining)" } },
  })
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('button', { name: 'Short test' }).click()
  await expect(page.getByText('A self-test is already running on this drive. Start another once it ends.')).toBeVisible()
  await expect(page.getByText(/Can't start self-test/)).toHaveCount(0)
  await expect(page.getByText(/Copyright|smartmontools/)).toHaveCount(0)
})

test('a started test says how long it takes', async ({ page }) => {
  await mock(page, smart(), { status: 200, json: { success: true, data: { message: 'self-test started', type: 'short', minutes: 2, output: 'Testing has begun.' } } })
  await page.getByRole('dialog').getByRole('button', { name: 'Short test' }).click()
  await expect(page.getByText('Self-test started. It takes about 2 min; the result appears in the log below.')).toBeVisible()
})
