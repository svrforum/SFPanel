import { test, expect, type Page } from '@playwright/test'

// #108. An interface brought up before its first peer answered "peers": null
// and the page showed nothing but an error; a new peer's config sent all its
// traffic through a server that does not forward it. REST is a fixture.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const wg0 = { name: 'wg0', active: true, public_key: 'c2VydmVyLXB1YmxpYy1rZXk=', listen_port: 51820, address: '10.8.0.1/24', dns: '', peers: null }

async function open(page: Page) {
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
    else if (path === '/network/wireguard/status') data = { installed: true, version: 'v1.0.20210914' }
    else if (path === '/network/wireguard/interfaces') data = [wg0]
    else if (path === '/network/wireguard/keypair') data = { private_key: 'Y2xpZW50LXByaXZhdGU=', public_key: 'Y2xpZW50LXB1YmxpYw==' }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/network/wireguard')
}

test('an interface without peers shows, with none', async ({ page }) => {
  await open(page)
  await expect(page.getByText('wg0').first()).toBeVisible()
  await expect(page.getByText('Peers (0)')).toBeVisible()
})

test('a new peer routes the VPN subnet, not everything', async ({ page }) => {
  await open(page)
  await page.getByRole('button', { name: 'Add Peer' }).first().click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByLabel('Routed through the VPN (AllowedIPs)')).toHaveValue('10.8.0.0/24')
  await dialog.getByLabel('Client address', { exact: true }).fill('10.8.0.2/32')
  await dialog.getByRole('button', { name: 'Create peer' }).click()
  await expect(dialog.locator('pre')).toContainText('AllowedIPs = 10.8.0.0/24')
  await expect(dialog.locator('pre')).not.toContainText('0.0.0.0/0')
})

// Bringing an interface down ran on one tap; over the tunnel, it is the tap
// that cuts the operator's own way in.
test('bringing an interface down asks first', async ({ page }) => {
  await open(page)
  const downs: string[] = []
  await page.route('**/api/v1/network/wireguard/interfaces/wg0/down', async route => {
    downs.push(route.request().method())
    await route.fulfill({ json: { success: true, data: { message: 'wg0 down' } } })
  })
  await page.getByRole('button', { name: 'Bring Down' }).click()
  const dialog = page.getByRole('dialog', { name: 'Bring wg0 down' })
  await expect(dialog).toContainText('If you reach this panel through this tunnel, you are cut off at once')
  await dialog.getByRole('button', { name: 'Cancel' }).click()
  expect(downs).toEqual([])

  await page.getByRole('button', { name: 'Bring Down' }).click()
  await page.getByRole('dialog', { name: 'Bring wg0 down' }).getByRole('button', { name: 'Bring Down' }).click()
  await expect.poll(() => downs).toEqual(['POST'])
})
