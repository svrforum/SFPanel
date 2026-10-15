import { test, expect, type Page } from '@playwright/test'

// A Proxmox host's network is ifupdown2's, and every guest hangs off its
// bridges: every edit control could only fail, and each guest NIC's tap,
// firewall bridge and veth pair sat among the host's own interfaces with a
// Configure button. REST is a fixture.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const base = { state: 'up', mtu: 1500, speed: -1, is_default: false, driver: '', tx_bytes: 0, rx_bytes: 0, tx_packets: 0, rx_packets: 0, tx_errors: 0, rx_errors: 0, addresses: [] }
const link = (name: string, type: string, kind: string, mac: string, extra: Record<string, unknown> = {}) =>
  ({ ...base, name, type, kind, mac_address: mac, ...extra })

const pveLinks = [
  link('lo', 'loopback', '', ''),
  link('enp1s0', 'ethernet', '', '02:42:ac:10:00:01', { master: 'vmbr0', speed: 1000 }),
  link('vmbr0', 'bridge', 'bridge', '02:42:ac:10:00:01', { is_default: true, speed: 1000, addresses: [{ address: '192.0.2.10', prefix: 24, family: 'ipv4' }] }),
  link('tap100i0', 'virtual', 'tun', '02:42:ac:10:01:00', { master: 'fwbr100i0' }),
  link('fwbr100i0', 'bridge', 'bridge', '02:42:ac:10:01:01', { speed: 10000 }),
  link('fwpr100p0', 'virtual', 'veth', '02:42:ac:10:01:02', { master: 'vmbr0', speed: 10000 }),
  link('fwln100i0', 'virtual', 'veth', '02:42:ac:10:01:03', { master: 'fwbr100i0', speed: 10000 }),
  link('veth101i0', 'virtual', 'veth', '02:42:ac:10:01:04', { master: 'vmbr0', speed: 10000 }),
]

async function open(page: Page, interfaces: unknown[], capabilities: unknown) {
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
    else if (path === '/network/status') data = { interfaces, routes: [], dns: { servers: ['192.0.2.53'], search: [] }, bonds: [] }
    else if (path === '/network/capabilities') data = capabilities
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/network/interfaces')
  await expect(page.getByText('vmbr0', { exact: true }).first()).toBeVisible()
}

test('a Proxmox host shows its network read-only, with the guests out of the way', async ({ page }) => {
  await open(page, pveLinks, { stack: 'ifupdown2', platform: 'pve', writable: false, reason: 'Proxmox VE manages this host\'s network' })
  await expect(page.getByRole('note')).toContainText("This host's network is managed by Proxmox")
  await expect(page.getByRole('link', { name: 'Open the Proxmox web UI' })).toHaveAttribute('href', /^https:\/\/[^/]+:8006$/)
  await expect(page.getByRole('button', { name: 'Configure' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Edit' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'New Bond' })).toHaveCount(0)

  await expect(page.getByRole('heading', { name: /Bridges/ })).toBeVisible()
  await expect(page.getByText('Port of vmbr0')).toBeVisible()
  const guests = page.getByRole('button', { name: /VM & CT/ })
  await expect(guests).toHaveAttribute('aria-expanded', 'false')
  await expect(guests).toContainText('5')
  await expect(page.getByText('tap100i0')).toHaveCount(0)
  await guests.click()
  await expect(page.getByText('tap100i0')).toBeVisible()
})

// Where the panel does write the network, a port is still configured through
// its bridge, never by itself.
test('a port of a bridge offers no Configure', async ({ page }) => {
  await open(page, pveLinks.slice(0, 3), { stack: 'netplan', platform: 'ubuntu', writable: true })
  await expect(page.getByRole('note')).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Configure' })).toHaveCount(1)
  await expect(page.getByRole('button', { name: 'Edit' })).toHaveCount(1)
  await expect(page.getByRole('button', { name: 'New Bond' })).toHaveCount(1)
})
