import { test, expect, type Page } from '@playwright/test'

// "Add UFW Rule" on every listening port, on a host whose firewall is not
// ufw: a Proxmox host refuses ufw changes, and a host without ufw cannot make
// them. REST is a fixture.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const sshd = { protocol: 'tcp', address: '0.0.0.0', port: 22, pid: 812, process: 'sshd' }

async function open(page: Page, status: unknown) {
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
    else if (path === '/firewall/ports') data = { ports: [sshd], total: 1 }
    else if (path === '/firewall/status') data = status
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/firewall/ports')
  await expect(page.getByRole('row', { name: /sshd/ })).toBeVisible()
}

test('a Proxmox host offers no ufw rule for a port', async ({ page }) => {
  await open(page, { active: false, default_incoming: '', default_outgoing: '', installed: false, manager: 'pve-firewall' })
  await expect(page.getByRole('button', { name: 'Add UFW Rule' })).toHaveCount(0)
})

test('a host managed with ufw does', async ({ page }) => {
  await open(page, { active: true, default_incoming: 'deny', default_outgoing: 'allow', installed: true, manager: 'ufw' })
  await expect(page.getByRole('button', { name: 'Add UFW Rule' })).toHaveCount(1)
})
