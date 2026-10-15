import { test, expect, type Page } from '@playwright/test'

// Debian 12+ and Proxmox VE write no /var/log/syslog, auth.log or kern.log:
// those sources now read the journal and say so. On Proxmox the tuning page
// shows what Proxmox keeps for itself instead of recommending it. REST is a
// fixture.
test.use({ viewport: { width: 1280, height: 900 }, locale: 'en-US' })

const sources = [
  { id: 'kern', name: 'Kernel Log', path: 'journalctl _TRANSPORT=kernel', size: 0, exists: true, custom: false, journal: true },
  { id: 'dpkg', name: 'Package Manager', path: '/var/log/dpkg.log', size: 2048, exists: true, custom: false },
  { id: 'fail2ban', name: 'Fail2ban', path: '/var/log/fail2ban.log', size: 0, exists: false, custom: false },
]
const tuning = {
  managed: [
    { key: 'net.ipv4.ip_forward', current: '0', recommended: '', description: 'Routing between the host’s networks', applied: false },
    { key: 'net.bridge.bridge-nf-call-iptables', current: '0', recommended: '', description: 'Off unless the Proxmox firewall is on', applied: false },
  ],
  categories: [{ name: 'memory', benefit: 'benefit_memory', caution: 'caution_memory', params: [], applied: 0, total: 0 }],
  total_params: 0, applied: 0, pending_rollback: false, rollback_remaining: 0,
  system_info: { cpu_cores: 8, total_ram: 64 * 1024 ** 3, kernel: '6.14.8-2-pve' },
}

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
    else if (p === '/logs/sources') data = sources
    else if (p === '/logs/read') data = { source: 'kern', lines: ['2026-10-12T00:18:40.123456+09:00 pve1 kernel: vmbr0: port 2(tap100i0) entered forwarding state'], total_lines: 1 }
    else if (p === '/system/tuning') data = tuning
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto(path)
}

test('a source read from the journal says so in place of a file size', async ({ page }) => {
  await open(page, '/logs')
  const kern = page.getByRole('button', { name: /Kernel Log/ })
  await expect(kern).toContainText('journalctl _TRANSPORT=kernel')
  await expect(kern).toContainText('Journal')
  await expect(kern).toBeEnabled()
  await expect(page.getByRole('button', { name: /Package Manager/ })).toContainText('2.0 KB')
  await expect(page.getByRole('button', { name: /Fail2ban/ })).toBeDisabled()
  await expect(page.getByText(/tap100i0\) entered forwarding state/)).toBeVisible()
})

test('on Proxmox the tuning page shows what Proxmox manages', async ({ page }) => {
  await open(page, '/settings?tab=system')
  await expect(page.getByRole('heading', { name: 'Managed by Proxmox' })).toBeVisible()
  await expect(page.getByText(/Datacenter or Node → Firewall → Options/)).toBeVisible()
  await expect(page.getByText('net.ipv4.ip_forward', { exact: true })).toBeVisible()
  await expect(page.getByText('net.bridge.bridge-nf-call-iptables', { exact: true })).toBeVisible()
})
