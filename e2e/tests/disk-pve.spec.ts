import { test, expect, type Page } from '@playwright/test'

// A Proxmox VE host on ZFS, as the disk list answers for it: the boot disk
// with its BIOS boot and EFI System partitions (unmounted, as
// proxmox-boot-tool keeps them) and the pool member, a data disk with a DOS
// extended partition, and two zvols the server marks as guests' volumes. REST
// is a fixture.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const part = (name: string, extra: Record<string, unknown> = {}) => ({
  name, size: 1073741824, type: 'part', fstype: '', mountpoint: '', model: '', serial: '',
  rotational: false, readonly: false, transport: '', state: '', vendor: '', ...extra,
})
const disk = (name: string, extra: Record<string, unknown> = {}) => ({
  ...part(name), type: 'disk', size: 2000398934016, model: 'Example NVMe 2TB', serial: 'X1', transport: 'nvme', state: 'live', ...extra,
})

const disks = [
  disk('zd0', { size: 17179869184, fstype: 'swap', mountpoint: '[SWAP]', model: '', transport: '', owner: 'zvol rpool/swap' }),
  disk('nvme0n1', {
    pttype: 'gpt', children: [
      part('nvme0n1p1', { size: 1031168, parttype: '21686148-6449-6e6f-744e-656564454649', parttypename: 'BIOS boot', boot: true }),
      part('nvme0n1p2', { fstype: 'vfat', parttype: 'c12a7328-f81f-11d2-ba4b-00a0c93ec93b', parttypename: 'EFI System', boot: true }),
      part('nvme0n1p3', { size: 1999323037696, fstype: 'zfs_member', label: 'rpool', parttype: '6a898cc3-1dd2-11b2-99a6-080020736631', parttypename: 'Solaris /usr & Apple ZFS' }),
    ],
  }),
  disk('sdb', {
    model: 'Example HDD', transport: 'sata', rotational: true, pttype: 'dos', children: [
      part('sdb1', { fstype: 'ext4', parttype: '0x83', parttypename: 'Linux' }),
      part('sdb2', { parttype: '0x5', parttypename: 'Extended' }),
      part('sdb5', { fstype: 'ext4', parttype: '0x83', parttypename: 'Linux' }),
    ],
  }),
  disk('zd16', {
    size: 34359738368, model: '', transport: '', pttype: 'gpt', owner: 'VMID 100',
    children: [part('zd16p1', { fstype: 'ext4', parttype: '0fc63daf-8483-4772-8e79-3d69d8477de4', parttypename: 'Linux filesystem' })],
  }),
]

async function open(page: Page, path: string) {
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const p = new URL(route.request().url()).pathname.replace('/api/v1', '')
    let data: unknown = {}
    if (p === '/auth/setup-status') data = { setup_required: false }
    else if (p === '/cluster/status') data = { enabled: false }
    else if (p === '/disks/overview') data = disks
    else if (p.startsWith('/disks/smartmontools')) data = { installed: true }
    else if (p === '/disks/iostat' || p === '/docker/volumes' || p === '/raid' || p === '/filesystems') data = []
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto(path)
}

// lsblk lists every guest's zvol beside the host's disks: a card per VM disk
// with the guest's own partitions and a SMART button with nothing behind it.
test('guest volumes sit in a collapsed group, without SMART', async ({ page }) => {
  await open(page, '/disk/overview')
  await expect(page.getByRole('region', { name: /^nvme0n1 / })).toBeVisible()
  await expect(page.getByRole('region', { name: /^zd16 / })).toHaveCount(0)
  await expect(page.getByText('2 disks')).toBeVisible()
  await expect(page.getByRole('button', { name: 'S.M.A.R.T.' })).toHaveCount(2)

  const group = page.getByRole('button', { name: /Guest volumes \(2\)/ })
  await expect(group).toHaveAttribute('aria-expanded', 'false')
  await expect(page.getByText('VMID 100')).toHaveCount(0)
  await group.click()
  await expect(page.getByText('VMID 100')).toBeVisible()
  await expect(page.getByText('zvol rpool/swap')).toBeVisible()
  await expect(page.getByRole('button', { name: 'S.M.A.R.T.' })).toHaveCount(2)

  // Proxmox keeps its ESPs unmounted; the label says what they are.
  await expect(page.getByRole('region', { name: /^nvme0n1 / }).getByText('Boot', { exact: true })).toHaveCount(2)
})

// A boot partition is never mounted on Proxmox VE, so "not mounted" said
// nothing; and deleting a DOS extended partition deletes what is inside it.
test('boot and extended partitions cannot be deleted from the list', async ({ page }) => {
  await open(page, '/disk/partitions')
  await expect(page.locator('select option')).toHaveText([/^nvme0n1 /, /^sdb /])

  for (const name of ['nvme0n1p1', 'nvme0n1p2']) {
    const row = page.getByRole('row', { name: new RegExp(`^${name}`) })
    await expect(row.getByText('Boot', { exact: true })).toBeVisible()
    await expect(row.getByRole('button', { name: 'Delete' })).toBeDisabled()
    // Why, in words in the row: a disabled button's title reaches no one.
    await expect(row).toContainText('Needed to boot')
  }
  await expect(page.getByRole('row', { name: /^nvme0n1p2/ })).toContainText('EFI System')
  await expect(page.getByRole('row', { name: /^nvme0n1p3/ }).getByRole('button', { name: 'Delete' })).toBeEnabled()

  await page.locator('select').selectOption('sdb')
  const extended = page.getByRole('row', { name: /^sdb2/ })
  await expect(extended).toContainText('Extended (holds sdb5)')
  await expect(extended.getByRole('button', { name: 'Delete' })).toBeDisabled()
  await expect(extended).toContainText('Delete the logical partitions inside it first.')
  await expect(page.getByRole('row', { name: /^sdb5/ }).getByRole('button', { name: 'Delete' })).toBeEnabled()
})

test('RAID offers neither guest volumes nor boot or extended partitions', async ({ page }) => {
  await open(page, '/disk/raid')
  await page.getByRole('button', { name: 'Create Array' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByText('nvme0n1p3')).toBeVisible()
  await expect(dialog.getByText('sdb5')).toBeVisible()
  for (const name of ['nvme0n1p1', 'nvme0n1p2', 'sdb2', 'zd16', 'zd16p1', 'zd0']) {
    await expect(dialog.getByText(name, { exact: true })).toHaveCount(0)
  }
})

// Format took a typed device name, and its confirmation asked for the same
// name again: a typo reached a guest's disk or a data disk. The device is now
// picked from those nothing holds, and the confirmation shows what is on it,
// its label too: the label is what tells one unmounted disk from another.
// What the server still refuses comes back worded in the panel's language.
test('format offers only free devices, shows what is on one, and a refusal is worded', async ({ page }) => {
  await open(page, '/disk/filesystems')
  await page.route('**/api/v1/filesystems/format', route => route.fulfill({
    status: 409,
    json: { success: false, error: { code: 'DEVICE_IN_USE', message: '/dev/nvme0n1p3 is in use (LVM, RAID, ZFS or swap) — release it first' } },
  }))
  await page.getByRole('button', { name: 'Format', exact: true }).click()
  const form = page.getByRole('dialog')
  // No guest volume, no boot partition, no extended partition.
  await expect(form.getByLabel('Device').getByRole('option')).toHaveText(['Pick a device', /^nvme0n1p3 /, /^sdb1 /, /^sdb5 /])
  // Nothing picked: Format says what is missing instead of greying out.
  await form.getByRole('button', { name: 'Format' }).click()
  await expect(form.getByText('Required', { exact: true })).toBeVisible()
  await expect(form.getByLabel('Device')).toHaveAttribute('aria-invalid', 'true')
  await expect(form.getByLabel('Device')).toBeFocused()
  await form.getByLabel('Device').selectOption('nvme0n1p3')
  await form.getByRole('button', { name: 'Format' }).click()
  const confirm = page.getByRole('dialog').filter({ hasText: 'To confirm, type:' })
  await expect(confirm.getByRole('definition')).toHaveText(['1.8 TB', 'zfs_member', 'rpool', 'Solaris /usr & Apple ZFS', 'nvme0n1', 'Example NVMe 2TB'])
  await expect(confirm.getByRole('term').nth(2)).toHaveText('Label on it now')
  await confirm.getByPlaceholder('nvme0n1p3').fill('nvme0n1p3')
  await confirm.getByRole('button', { name: 'Format' }).click()
  await expect(page.getByText('This device is in use (mounted, LVM, RAID, ZFS or swap). Release it first.')).toBeVisible()
})
