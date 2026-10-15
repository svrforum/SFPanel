import { test, expect, type Page } from '@playwright/test'

// Accepting a peer's route over the LAN this host is on sent its replies to
// that LAN into the tunnel. The server refuses it with the peer and prefix;
// the page asks, and only then accepts anyway. REST is a fixture.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const status = {
  installed: true, daemon_running: true, version: '1.96.4', backend_state: 'Running',
  self: { hostname: 'pve1', tailscale_ip: '100.64.0.1', tailscale_ipv6: '', online: true, os: 'linux', exit_node_option: false },
  tailnet_name: 'example.ts.net', magic_dns_suffix: 'example.ts.net', auth_url: '',
  accept_routes: false, advertise_exit_node: false, current_exit_node: '',
}

async function open(page: Page) {
  const puts: string[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace('/api/v1', '')
    if (path === '/network/tailscale/preferences') {
      puts.push(url.search)
      if (url.search !== '?force=true') {
        await route.fulfill({ status: 409, json: { success: false, error: {
          code: 'TAILSCALE_ROUTE_OVERLAP', message: 'router1 routes 192.0.2.0/24, which overlaps a network this host is directly on.',
          details: { prefix: '192.0.2.0/24', peer: 'router1' },
        } } })
        return
      }
      await route.fulfill({ json: { success: true, data: { message: 'Preferences updated' } } })
      return
    }
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: false }
    else if (path === '/network/tailscale/status') data = status
    else if (path === '/network/tailscale/peers') data = []
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/network/tailscale')
  return puts
}

test('accepting a route over this host\'s LAN asks first', async ({ page }) => {
  const puts = await open(page)
  await page.getByRole('switch', { name: /Accept Subnet Routes/ }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toContainText('router1 advertises 192.0.2.0/24, which overlaps a network this host is directly on')
  await dialog.getByRole('button', { name: 'Accept anyway' }).click()
  await expect.poll(() => puts).toEqual(['', '?force=true'])
})

test('declining leaves the routes alone', async ({ page }) => {
  const puts = await open(page)
  await page.getByRole('switch', { name: /Accept Subnet Routes/ }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Cancel' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  expect(puts).toEqual([''])
})
