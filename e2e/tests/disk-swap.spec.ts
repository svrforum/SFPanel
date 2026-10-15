import { test, expect } from '@playwright/test'

// Swap on a device ran mkswap on whatever was named, at one click: a data
// partition, a guest's zvol, a disk with a partition table. Now the operator
// types the device's name, and a device that holds data is refused until
// they say to overwrite it. REST is a fixture; `creates` records the bodies.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

test('swap on a device is typed out, and overwriting data is asked for', async ({ page }) => {
  const creates: Record<string, unknown>[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const p = new URL(req.url()).pathname.replace('/api/v1', '')
    if (p === '/swap' && req.method() === 'POST') {
      const body = req.postDataJSON()
      creates.push(body)
      await route.fulfill(body.force
        ? { json: { success: true, data: { message: 'swap enabled on sdb2' } } }
        : { status: 409, json: { success: false, error: { code: 'DISK_NOT_EMPTY', message: '/dev/sdb2 already holds ext4; creating swap there loses it', details: { device: 'sdb2', signature: 'ext4' } } } })
      return
    }
    let data: unknown = {}
    if (p === '/auth/setup-status') data = { setup_required: false }
    else if (p === '/cluster/status') data = { enabled: false }
    else if (p === '/swap') data = { total: 0, used: 0, free: 0, swappiness: 60, entries: [] }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/disk/swap')

  await page.getByRole('button', { name: 'Create Swap' }).click()
  const form = page.getByRole('dialog', { name: 'Create Swap' })
  await form.getByRole('button', { name: 'Partition-based' }).click()
  await form.getByLabel('Device').fill('/dev/sdb2')
  await form.getByRole('button', { name: 'Create', exact: true }).click()

  const confirm = page.getByRole('dialog', { name: 'Make /dev/sdb2 swap?' })
  await expect(confirm.getByRole('button', { name: 'Create' })).toBeDisabled()
  expect(creates).toEqual([])
  await confirm.getByPlaceholder('/dev/sdb2').fill('/dev/sdb2')
  await confirm.getByRole('button', { name: 'Create' }).click()

  const overwrite = page.getByRole('dialog', { name: '/dev/sdb2 holds data' })
  await expect(overwrite).toContainText('/dev/sdb2 already holds ext4. Making it swap erases it.')
  await overwrite.getByRole('button', { name: 'Overwrite' }).click()
  await expect(page.getByText('Swap created')).toBeVisible()
  expect(creates).toEqual([
    { type: 'partition', device: '/dev/sdb2', force: false },
    { type: 'partition', device: '/dev/sdb2', force: true },
  ])
})
