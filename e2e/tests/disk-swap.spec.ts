import { test, expect } from '@playwright/test'

// Swap on a device ran mkswap on whatever was named, at one click: a data
// partition, a guest's zvol, a disk with a partition table. Now the device is
// picked from those nothing holds, the operator types its name beside what is
// on it, and a device that holds data is refused until they say to overwrite
// it. REST is a fixture; `creates` records the bodies.
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
    else if (p === '/disks/overview') data = [{
      name: 'sdb', size: 1000204886016, type: 'disk', fstype: '', mountpoint: '', model: 'Example HDD', pttype: 'gpt',
      children: [
        { name: 'sdb1', size: 536870912, type: 'part', fstype: 'ext4', mountpoint: '/srv' },
        { name: 'sdb2', size: 4294967296, type: 'part', fstype: 'ext4', mountpoint: '' },
      ],
    }]
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/disk/swap')

  await page.getByRole('button', { name: 'Create Swap' }).click()
  const form = page.getByRole('dialog', { name: 'Create Swap' })
  await form.getByRole('button', { name: 'Partition-based' }).click()
  // The mounted sdb1 is not offered.
  await expect(form.getByLabel('Device').getByRole('option')).toHaveText(['Pick a device', 'sdb2 — 4.0 GB · ext4 · Example HDD', 'A device that is not listed…'])
  await form.getByLabel('Device').selectOption('sdb2')
  await form.getByRole('button', { name: 'Create', exact: true }).click()

  const confirm = page.getByRole('dialog', { name: 'Make sdb2 swap?' })
  await expect(confirm.getByRole('definition')).toHaveText(['4.0 GB', 'ext4', 'sdb', 'Example HDD'])
  await expect(confirm.getByRole('button', { name: 'Create' })).toBeDisabled()
  expect(creates).toEqual([])
  await confirm.getByPlaceholder('sdb2').fill('sdb2')
  await confirm.getByRole('button', { name: 'Create' }).click()

  const overwrite = page.getByRole('dialog', { name: 'sdb2 holds data' })
  await expect(overwrite).toContainText('sdb2 already holds ext4. Making it swap erases it.')
  await overwrite.getByRole('button', { name: 'Overwrite' }).click()
  await expect(page.getByText('Swap created')).toBeVisible()
  expect(creates).toEqual([
    { type: 'partition', device: 'sdb2', force: false },
    { type: 'partition', device: 'sdb2', force: true },
  ])
})

// "Remove" only ran swapoff: the file and its fstab line stayed, and the swap
// was back after a reboot. And swapoff pulls what is in the swap into RAM,
// which the resize dialog warned about and this one did not.
test('turning swap off says what it does, and warns when RAM cannot take it', async ({ page }) => {
  const deletes: unknown[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const p = new URL(req.url()).pathname.replace('/api/v1', '')
    let data: unknown = {}
    if (p === '/swap' && req.method() === 'DELETE') {
      deletes.push(req.postDataJSON())
      data = { message: 'swap disabled on /swapfile' }
    } else if (p === '/auth/setup-status') data = { setup_required: false }
    else if (p === '/cluster/status') data = { enabled: false }
    else if (p === '/swap') data = { total: 4294967296, used: 3221225472, free: 1073741824, swappiness: 60, entries: [{ name: '/swapfile', type: 'file', size: 4294967296, used: 3221225472, priority: -2 }] }
    else if (p === '/swap/resize-check') data = { current_size_mb: 4096, disk_free_mb: 10000, max_size_mb: 14096, swap_used_mb: 3072, ram_free_mb: 512, swapoff_safe: false }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/disk/swap')

  await page.getByRole('row', { name: /\/swapfile/ }).getByRole('button', { name: 'Turn off swap' }).click()
  const dialog = page.getByRole('dialog', { name: 'Turn off swap on /swapfile' })
  await expect(dialog).toContainText('The file or partition is not deleted, and an /etc/fstab entry for it turns it back on at the next boot.')
  await expect(dialog.getByRole('alert')).toContainText('Not enough free RAM to safely disable swap.')
  await dialog.getByRole('button', { name: 'Turn off swap' }).click()
  await expect(page.getByText('Swap turned off')).toBeVisible()
  expect(deletes).toEqual([{ path: '/swapfile' }])
})
