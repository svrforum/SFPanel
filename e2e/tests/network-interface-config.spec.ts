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
