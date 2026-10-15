import { test, expect, type Page } from '@playwright/test'

// #112. A rule pinned to a container's address blocks nothing once the
// container is handed another one, as a reboot does. The server marks such a
// rule stale; the page must say so, and must not count it towards a port with
// the same number being blocked. REST is a fixture.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

async function open(page: Page, firewall: unknown) {
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
    else if (path === '/firewall/docker') data = firewall
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/firewall/docker')
}

// The container behind host port 443 came back as 172.19.0.4; the rule still
// names 172.19.0.5:443, so the server lists it by the container's port.
const published = { container_name: 'npg-ui', container_ip: '172.19.0.4', host_port: 443, container_port: 443, protocol: 'tcp', host_ip: '0.0.0.0' }

test('a rule tied to an old container address is shown as not blocking', async ({ page }) => {
  await open(page, { ports: [published], rules: [{ number: 1, port: 443, protocol: 'tcp', source: '', action: 'drop', stale: true }] })
  await expect(page.getByRole('alert')).toContainText('1 rule(s) are tied to container addresses that no longer serve their ports')
  await expect(page.getByRole('row', { name: /^1 443/ }).getByText('Not blocking')).toBeVisible()
  await expect(page.getByRole('row', { name: /npg-ui/ }).getByText('Open', { exact: true })).toBeVisible()
})

test('a rule on the host port blocks it', async ({ page }) => {
  await open(page, { ports: [published], rules: [{ number: 1, port: 443, protocol: 'tcp', source: '', action: 'drop' }] })
  await expect(page.getByRole('row', { name: /npg-ui/ }).getByText('Blocked', { exact: true })).toBeVisible()
  await expect(page.getByRole('alert')).toHaveCount(0)
  await expect(page.getByText('Not blocking')).toHaveCount(0)
})

// A host without Docker — a Proxmox host, say — has neither chain. The tab
// showed that as an error.
test('a host without Docker shows no error', async ({ page }) => {
  await open(page, { ports: [], rules: [], docker_installed: false })
  await expect(page.getByText('Docker is not installed on this host')).toBeVisible()
  await expect(page.getByText('DOCKER-USER chain not found')).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Add Rule' })).toHaveCount(0)
})
