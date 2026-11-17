import { test, expect } from '@playwright/test'

// #109. The interface dialog opened a DHCP interface in Static and filled it
// with every address the kernel lists, SLAAC and link-local IPv6 included, so
// saving pinned them in netplan. REST is a fixture.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const ens3 = {
  name: 'ens3', type: 'ethernet', state: 'up', mac_address: '02:42:ac:11:00:02', mtu: 1500, speed: 1000, is_default: true, driver: 'virtio_net',
  tx_bytes: 0, rx_bytes: 0, tx_packets: 0, rx_packets: 0, tx_errors: 0, rx_errors: 0,
  addresses: [
    { address: '192.0.2.15', prefix: 24, family: 'ipv4' },
    { address: '2001:db8::5054:ff:fe12:3456', prefix: 64, family: 'ipv6' },
    { address: 'fe80::5054:ff:fe12:3456', prefix: 64, family: 'ipv6' },
  ],
}

test('a DHCP interface opens in DHCP, and Static starts from its IPv4 only', async ({ page }) => {
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '')
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: false }
    else if (path === '/network/status') data = { interfaces: [ens3], routes: [], dns: { servers: [] }, bonds: [] }
    else if (path === '/network/interfaces/ens3') data = { ...ens3, config: { dhcp4: true, dhcp6: false, addresses: [], gateway4: '', gateway6: '', dns: [] } }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/network/interfaces')
  await page.getByRole('button', { name: 'Configure' }).first().click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('radio', { name: 'DHCP', exact: true })).toHaveAttribute('aria-checked', 'true')
  await dialog.getByRole('radio', { name: 'Static', exact: true }).click()
  await expect(dialog.locator('#cfg-addresses')).toHaveValue('192.0.2.15/24')
})

// The floating Apply button sat under the phone's bottom tab bar, and the bar
// that keeps an applied change appeared at the top of the page with nothing
// said: sixty seconds later the change rolled back. The bar now takes Apply's
// place above the tab bar, and the focus goes to its Keep it.
test('on a phone, Apply clears the tab bar and Keep it takes the focus', async ({ page }) => {
  await page.setViewportSize({ width: 360, height: 740 })
  const posts: string[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace('/api/v1', '')
    let data: unknown = {}
    if (req.method() !== 'GET') posts.push(`${req.method()} ${path}`)
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: false }
    else if (path === '/network/status') data = { interfaces: [ens3], routes: [], dns: { servers: [] }, bonds: [] }
    else if (path === '/network/interfaces/ens3' && req.method() === 'GET') data = { ...ens3, config: { dhcp4: true, dhcp6: false, addresses: [], gateway4: '', gateway6: '', dns: [] } }
    else if (path === '/network/apply') data = { message: 'applied', rollback_pending: true, timeout: 60, deadline: Date.now() + 60_000 }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/network/interfaces')
  await page.getByRole('button', { name: 'Configure' }).first().click()
  await page.getByRole('dialog').getByLabel('MTU').fill('1400')
  await page.getByRole('dialog').getByRole('button', { name: 'Save' }).click()

  // What a tap on the button's middle lands on is the button, not the tab bar.
  const onTop = async (name: string) => {
    const box = (await page.getByRole('button', { name }).boundingBox())!
    return page.evaluate(([x, y, n]) => document.elementFromPoint(x, y)?.closest('button')?.textContent?.includes(n) ?? false,
      [box.x + box.width / 2, box.y + box.height / 2, name] as const)
  }
  await expect(page.getByRole('button', { name: 'Apply Changes' })).toBeVisible()
  // The save toast shares the toaster's spot above the tab bar for its few
  // seconds; the button is what is left there after it.
  await expect(page.getByText('Configuration saved')).toHaveCount(0, { timeout: 10_000 })
  expect(await onTop('Apply Changes')).toBe(true)

  await page.getByRole('button', { name: 'Apply Changes' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Apply Changes' }).click()
  const keep = page.getByRole('button', { name: 'Keep it' })
  await expect(keep).toBeFocused()
  expect(await onTop('Keep it')).toBe(true)
  await expect(page.getByRole('alert').filter({ hasText: 'Confirm the network change' })).toBeVisible()
  await keep.click()
  await expect.poll(() => posts).toEqual(['PUT /network/interfaces/ens3', 'POST /network/apply', 'POST /network/apply/confirm'])
})
