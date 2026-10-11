import { test, expect, type Page } from '@playwright/test'

// The delete button sent the partition's name (nvme0n1p3) where the server
// takes its number, so every delete was refused as an invalid partition
// number. A partition the kernel holds — an LVM physical volume, a RAID
// member, a ZFS vdev — is refused with DEVICE_IN_USE, shown in the panel's
// language. REST is a fixture; `deletes` records the request paths.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const disk = {
  name: 'nvme0n1', size: 512110190592, type: 'disk', fstype: '', mountpoint: '', model: 'Example NVMe', serial: 'X1',
  rotational: false, readonly: false, transport: 'nvme', state: 'live', vendor: '',
  children: [{ name: 'nvme0n1p3', size: 107374182400, type: 'part', fstype: 'LVM2_member', mountpoint: '' }],
}

async function deletePartition(page: Page, answer: { status: number; json: unknown }) {
  const deletes: string[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace('/api/v1', '')
    if (req.method() === 'DELETE') {
      deletes.push(path)
      await route.fulfill({ status: answer.status, json: answer.json })
      return
    }
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: false }
    else if (path === '/disks/overview') data = [disk]
    else if (path === '/disks/iostat') data = []
    else if (path === '/docker/volumes') data = []
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/disk/partitions')
  await page.getByRole('row', { name: /nvme0n1p3/ }).getByRole('button', { name: 'Delete' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByPlaceholder('nvme0n1p3').fill('nvme0n1p3')
  await dialog.getByRole('button', { name: 'Delete' }).click()
  return deletes
}

test('deleting a partition sends its number', async ({ page }) => {
  const deletes = await deletePartition(page, { status: 200, json: { success: true, data: { message: 'partition 3 deleted from nvme0n1' } } })
  await expect(page.getByText('Partition deleted')).toBeVisible()
  expect(deletes).toEqual(['/disks/nvme0n1/partitions/3'])
})

test('a partition in use is refused with the reason in the panel language', async ({ page }) => {
  await deletePartition(page, {
    status: 409,
    json: { success: false, error: { code: 'DEVICE_IN_USE', message: '/dev/nvme0n1p3 is in use (LVM, RAID, ZFS or swap) — release it first' } },
  })
  await expect(page.getByText('This device is in use (mounted, LVM, RAID, ZFS or swap). Release it first.')).toBeVisible()
})

// #107. A new disk has no partition table and parted will not make a partition
// there; the page had no way past it.
test('an empty disk offers a GPT partition table', async ({ page }) => {
  const posts: string[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace('/api/v1', '')
    if (req.method() === 'POST') {
      posts.push(path)
      await route.fulfill({ json: { success: true, data: { message: 'GPT partition table created on vdb' } } })
      return
    }
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: false }
    else if (path === '/disks/overview') data = [{ ...disk, name: 'vdb', pttype: '', children: [] }]
    else if (path === '/disks/iostat') data = []
    else if (path === '/docker/volumes') data = []
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/disk/partitions')
  await page.getByRole('button', { name: 'Create GPT partition table' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByPlaceholder('vdb').fill('vdb')
  await dialog.getByRole('button', { name: 'Create GPT partition table' }).click()
  await expect.poll(() => posts).toEqual(['/disks/vdb/partition-table'])
})

// #113. The dialog's file system choice was only the type parted records, so
// the new partition came out empty. It now formats unless told not to, and a
// partition that was made but not formatted is reported and listed.
async function createPartition(page: Page, answer: { status: number; json: unknown }) {
  const bodies: Record<string, unknown>[] = []
  let overviews = 0
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace('/api/v1', '')
    if (req.method() === 'POST' && path === '/disks/vdb/partitions') {
      bodies.push(req.postDataJSON())
      await route.fulfill({ status: answer.status, json: answer.json })
      return
    }
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: false }
    else if (path === '/disks/overview') { overviews++; data = [{ ...disk, name: 'vdb', pttype: 'gpt', children: [] }] }
    else if (path === '/disks/iostat') data = []
    else if (path === '/docker/volumes') data = []
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/disk/partitions')
  await page.getByRole('button', { name: 'Create Partition' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Start').fill('0%')
  await dialog.getByLabel('End').fill('100%')
  return { dialog, bodies, overviews: () => overviews }
}

test('a new partition is formatted unless the box is cleared', async ({ page }) => {
  const { dialog, bodies } = await createPartition(page, {
    status: 200, json: { success: true, data: { message: '/dev/vdb1 created and formatted as ext4', partition: 'vdb1' } },
  })
  await expect(dialog.getByRole('checkbox', { name: 'Format with this file system after creating' })).toBeChecked()
  await dialog.getByRole('button', { name: 'Create', exact: true }).click()
  await expect(page.getByText('Partition created and formatted as ext4')).toBeVisible()
  expect(bodies).toEqual([{ start: '0%', end: '100%', fs_type: 'ext4', format: true }])

  await page.getByRole('button', { name: 'Create Partition' }).click()
  await dialog.getByLabel('Start').fill('0%')
  await dialog.getByLabel('End').fill('100%')
  await dialog.getByRole('checkbox', { name: 'Format with this file system after creating' }).click()
  await expect(dialog.getByText(/only sets the partition type/)).toBeVisible()
  await dialog.getByRole('button', { name: 'Create', exact: true }).click()
  await expect.poll(() => bodies.length).toBe(2)
  expect(bodies[1]).toEqual({ start: '0%', end: '100%', fs_type: 'ext4', format: false })
})

test('a partition made but not formatted is reported and listed', async ({ page }) => {
  const { dialog, overviews } = await createPartition(page, {
    status: 500,
    json: { success: false, error: { code: 'PARTITION_NOT_FORMATTED', message: 'created /dev/vdb1 but could not format it' } },
  })
  const before = overviews()
  await dialog.getByRole('button', { name: 'Create', exact: true }).click()
  await expect(page.getByText('The partition was created but could not be formatted. Format it from the File Systems tab.')).toBeVisible()
  await expect.poll(overviews).toBeGreaterThan(before)
})
