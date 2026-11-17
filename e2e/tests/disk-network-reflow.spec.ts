import { test, expect, type Page } from '@playwright/test'

// At phone widths rows that did not wrap pushed their ends past the page, and
// the page clips sideways rather than scroll: the disk's name got no width at
// all, and the disk picker, Tailscale's Logout and the network drives' Connect
// were cut off. REST is a fixture.
test.use({ locale: 'en-US' })
const part = (name: string, extra: Record<string, unknown> = {}) => ({
  name, size: 1073741824, type: 'part', fstype: '', mountpoint: '', model: '', serial: '',
  rotational: false, readonly: false, transport: '', state: '', vendor: '', ...extra,
})
const disks = [
  { ...part('nvme0n1'), type: 'disk', size: 2000398934016, model: 'Example NVMe SSD with a long model name 2TB', serial: 'X1', transport: 'nvme', pttype: 'gpt', children: [
    part('nvme0n1p1', { size: 1031168, parttypename: 'BIOS boot', boot: true }),
    part('nvme0n1p2', { fstype: 'vfat', parttypename: 'EFI System', boot: true, mountpoint: '/boot/efi' }),
    part('nvme0n1p3', { size: 1999323037696, fstype: 'LVM2_member', mountpoint: '/var/lib/docker/some/long/mount/point' }),
  ] },
]
async function open(page: Page, path: string, extra: Record<string, unknown> = {}) {
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
    else if (p in extra) data = extra[p]
    else if (p === '/disks/iostat' || p === '/docker/volumes') data = []
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto(path)
}
// What inside the page ends past its right padding, outside a box meant to
// scroll sideways.
async function overflow(page: Page) {
  return page.evaluate(() => {
    const main = document.querySelector('main')!
    const r = main.getBoundingClientRect()
    const right = r.right - parseFloat(getComputedStyle(main).paddingRight) + 1
    const out: string[] = []
    for (const el of main.querySelectorAll('button, select, h3, span, p')) {
      const b = el.getBoundingClientRect()
      if (b.width > 0 && b.right > right && !el.closest('[class*="overflow-x-auto"]') && !el.closest('nav')) out.push(`${el.tagName} ${(el.textContent || '').slice(0, 30)} ${Math.round(b.right - right)}`)
    }
    return out
  })
}
for (const w of [320, 360]) {
  test(`overview ${w}`, async ({ page }) => {
    await page.setViewportSize({ width: w, height: 800 })
    await open(page, '/disk/overview')
    await expect(page.getByRole('heading', { name: 'nvme0n1' })).toBeVisible()
    const box = (await page.getByRole('heading', { name: 'nvme0n1' }).boundingBox())!
    expect(box.width).toBeGreaterThan(40)
    expect(await overflow(page)).toEqual([])
  })
  test(`partitions ${w}`, async ({ page }) => {
    await page.setViewportSize({ width: w, height: 800 })
    await open(page, '/disk/partitions')
    await expect(page.getByLabel('Select Disk')).toBeVisible()
    expect(await overflow(page)).toEqual([])
  })
  test(`shares ${w}`, async ({ page }) => {
    await page.setViewportSize({ width: w, height: 800 })
    await open(page, '/disk/network-shares', { '/disks/network-shares': { shares: [] }, '/disks/network-shares/tools': { cifs: { installed: true, package: 'cifs-utils' }, nfs: { installed: true, package: 'nfs-common' } } })
    await expect(page.getByRole('button', { name: 'Connect drive' })).toBeVisible()
    expect(await overflow(page)).toEqual([])
  })
  test(`raid ${w}`, async ({ page }) => {
    await page.setViewportSize({ width: w, height: 800 })
    await open(page, '/disk/raid', { '/raid': [{
      name: 'md127', level: 'raid1', state: 'clean, degraded', size: 1000204886016, total: 2, active: 1, failed: 0, spare: 0,
      devices: [{ device: 'nvme0n1p3', state: 'active', role: '0' }],
    }] })
    // The buttons sat on top of the array's name rather than past the edge.
    const name = page.getByText('md127', { exact: true })
    const box = (await name.boundingBox())!
    expect(await page.evaluate(([x, y]) => document.elementFromPoint(x, y)?.textContent, [box.x + box.width / 2, box.y + box.height / 2] as const)).toBe('md127')
    expect(await overflow(page)).toEqual([])
  })
  test(`tailscale ${w}`, async ({ page }) => {
    await page.setViewportSize({ width: w, height: 800 })
    await open(page, '/network/tailscale', { '/network/tailscale/status': {
      installed: true, daemon_running: true, version: '1.96.4', backend_state: 'Running',
      self: { hostname: 'pve1', tailscale_ip: '100.64.0.1', tailscale_ipv6: 'fd7a:115c:a1e0::1', online: true, os: 'linux', exit_node_option: false },
      tailnet_name: 'example.ts.net', magic_dns_suffix: 'example.ts.net', auth_url: '', accept_routes: false, advertise_exit_node: false, current_exit_node: '' }, '/network/tailscale/peers': [] })
    await expect(page.getByRole('button', { name: 'Logout' })).toBeVisible()
    expect(await overflow(page)).toEqual([])
  })
}
