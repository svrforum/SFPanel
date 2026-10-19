import { test, expect, type Page } from '@playwright/test'

// The join command is pasted on another server, and a Proxmox VE host has no
// sudo: its operator is root, and there a command that starts with sudo stops
// the && chain before anything is installed. Mocked API.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const pin = 'sha256:' + 'ab'.repeat(32)
const node = { id: 'node-a', name: 'pve1', role: 'voter', status: 'online', api_address: '192.0.2.10:3628', grpc_address: '192.0.2.10:3629', joined_at: '2026-01-01T00:00:00Z', last_seen: '2026-01-01T00:00:00Z' }

async function mock(page: Page) {
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '')
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: true, name: 'homelab', node_count: 1, leader_id: 'node-a', local_id: 'node-a', is_leader: true }
    else if (path === '/cluster/nodes') data = { nodes: [node], local_id: 'node-a', is_leader: true }
    else if (path === '/cluster/overview') data = { name: 'homelab', node_count: 1, leader_id: 'node-a', nodes: [node], metrics: [] }
    else if (path === '/cluster/events') data = { events: [] }
    else if (path === '/cluster/tokens') data = { tokens: [] }
    else if (path === '/cluster/token') data = { token: 'tok123', expires_at: '2026-01-02T00:00:00Z', advertise_address: '192.0.2.10', grpc_port: 3629, ca_pin: pin }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
}

test('the join commands leave sudo out for a server whose operator is root', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/tokens')
  await page.getByRole('button', { name: 'Generate Token' }).click()

  const join = `sfpanel cluster join 192.0.2.10:3629 tok123 --ca ${pin}`
  const installer = 'curl -fsSL https://raw.githubusercontent.com/svrforum/SFPanel/main/scripts/install.sh'
  await expect(page.getByText(`sudo ${join}`, { exact: true })).toBeVisible()
  await expect(page.getByText(`${installer} | sudo bash && sudo ${join}`, { exact: true })).toBeVisible()

  await page.getByRole('checkbox', { name: /Run as root, without sudo/ }).click()
  await expect(page.getByText(join, { exact: true })).toBeVisible()
  await expect(page.getByText(`${installer} | bash && ${join}`, { exact: true })).toBeVisible()
  await expect(page.getByText(/sudo (bash|sfpanel)/)).toHaveCount(0)
})
