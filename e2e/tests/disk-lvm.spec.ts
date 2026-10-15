import { test, expect, type Page } from '@playwright/test'

// VG pve on a Proxmox VE LVM-thin install: the pool behind local-lvm with a
// guest's disk in it, root, and a volume of the operator's. lvm2 warns about
// an old PV header on every report, which emptied the whole tab. REST is a
// fixture; `resizes` records the resize requests.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const warning = 'WARNING: PV /dev/sda3 in VG pve is using an old PV header, modify the VG to update.'
const lv = (name: string, attr: string, extra: Record<string, unknown> = {}) => ({
  name, vg_name: 'pve', size: '10.00g', attr, path: `/dev/pve/${name}`, pool_lv: '', data_percent: '', metadata_percent: '', ...extra,
})
const lvs = [
  lv('data', 'twi-aotz--', { path: '', size: '<40.00g', data_percent: '81.20', metadata_percent: '3.10', owner: 'storage local-lvm' }),
  lv('root', '-wi-ao----'),
  lv('vm-100-disk-0', 'Vwi-aotz--', { pool_lv: 'data', data_percent: '45.00', owner: 'VMID 100' }),
  lv('scratch', '-wi-a-----', { size: '5.00g' }),
]

async function open(page: Page, resize?: (body: Record<string, unknown>) => { status: number; json: unknown }) {
  const resizes: Record<string, unknown>[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const url = new URL(req.url())
    const p = url.pathname.replace('/api/v1', '')
    if (p === '/lvm/lvs/resize' && req.method() === 'POST' && resize) {
      const body = req.postDataJSON()
      resizes.push(body)
      await route.fulfill(resize(body))
      return
    }
    let data: unknown = {}
    const asked = url.searchParams.get('warnings') === '1'
    if (p === '/auth/setup-status') data = { setup_required: false }
    else if (p === '/cluster/status') data = { enabled: false }
    else if (p === '/lvm/pvs') data = asked ? { items: [{ name: '/dev/sda3', vg_name: 'pve', size: '<99.50g', free: '<12.38g', attr: 'a--' }], warnings: [warning] } : []
    else if (p === '/lvm/vgs') data = asked ? { items: [{ name: 'pve', size: '<99.50g', free: '<12.38g', pv_count: 1, lv_count: 4, attr: 'wz--n-' }], warnings: [warning] } : []
    else if (p === '/lvm/lvs') data = asked ? { items: lvs, warnings: [] } : []
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/disk/lvm')
  return resizes
}

test('LVM warnings are shown, once, over lists that still load', async ({ page }) => {
  await open(page)
  const banner = page.getByRole('alert').filter({ hasText: 'LVM reported:' })
  await expect(banner).toBeVisible()
  await expect(banner.getByText(warning)).toHaveCount(1)
  await expect(page.getByRole('tab', { name: /Physical Volumes \(PV\) \(1\)/ })).toBeVisible()
})

// A flat list put the pool among the guests' disks, with Delete and Resize on
// every one of them.
test('thin volumes sit under their pool, and Proxmox-owned volumes keep their hands off', async ({ page }) => {
  await open(page)
  await page.getByRole('tab', { name: /Logical Volumes \(LV\) \(4\)/ }).click()
  await expect(page.getByRole('row').locator('td:first-child')).toHaveText([/^data/, /^vm-100-disk-0/, /^root/, /^scratch/])

  const pool = page.getByRole('row', { name: /^data/ })
  await expect(pool).toContainText('thin pool')
  await expect(pool).toContainText('storage local-lvm')
  await expect(pool).toContainText('Data 81.2%')
  await expect(pool).toContainText('Metadata 3.1%')
  // A storage's pool grows from here; it is not removed from under Proxmox.
  await expect(pool.getByRole('button', { name: 'Resize' })).toBeVisible()
  await expect(pool.getByRole('button', { name: 'Delete' })).toHaveCount(0)

  const guest = page.getByRole('row', { name: /^vm-100-disk-0/ })
  await expect(guest).toContainText('VMID 100')
  await expect(guest.getByRole('button')).toHaveCount(0)

  const mine = page.getByRole('row', { name: /^scratch/ })
  await expect(mine.getByRole('button', { name: 'Resize' })).toBeVisible()
  await expect(mine.getByRole('button', { name: 'Delete' })).toBeVisible()
})

// LVM asks before it reduces a volume and, with no terminal, the answer was
// always no: a shrink never worked. The server now refuses one without the
// shrink flag, and the page asks the operator to type the volume's name.
test('a shrink is confirmed by name before it is sent', async ({ page }) => {
  const resizes = await open(page, body => body.shrink
    ? { status: 200, json: { success: true, data: { message: 'logical volume pve/scratch resized to 4G' } } }
    : { status: 409, json: { success: false, error: { code: 'LV_SHRINK_CONFIRM_REQUIRED', message: 'pve/scratch would get smaller; confirm the shrink', details: { lv: 'pve/scratch' } } } })
  await page.getByRole('tab', { name: /Logical Volumes \(LV\)/ }).click()
  await page.getByRole('row', { name: /^scratch/ }).getByRole('button', { name: 'Resize' }).click()
  await page.getByRole('dialog').getByLabel('New Size').fill('4G')
  await page.getByRole('dialog').getByRole('button', { name: 'Resize' }).click()

  const confirm = page.getByRole('dialog', { name: 'Shrink scratch?' })
  await expect(confirm).toContainText('goes from 5.00g to 4G')
  await expect(confirm.getByRole('button', { name: 'Shrink' })).toBeDisabled()
  await confirm.getByPlaceholder('scratch').fill('scratch')
  await confirm.getByRole('button', { name: 'Shrink' }).click()
  await expect(page.getByText('LV resized')).toBeVisible()
  expect(resizes).toEqual([
    { vg: 'pve', name: 'scratch', size: '4G', shrink: false },
    { vg: 'pve', name: 'scratch', size: '4G', shrink: true },
  ])
})
