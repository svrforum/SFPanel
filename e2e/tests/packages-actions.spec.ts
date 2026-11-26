import { test, expect, type Page } from '@playwright/test'

// What the Packages page does before it changes the server. Removing a
// package, removing a Node.js version and upgrading ran on one tap, the
// remove a lone red icon on a phone; a Node.js status that could not be read
// was drawn as "not installed", with its installer one tap away. Mocked API.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

interface Fixture {
  nodeStatus?: () => { status: number; json: unknown }
}

async function open(page: Page, fx: Fixture = {}) {
  const posts: string[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace('/api/v1', '')
    if (req.method() === 'POST') {
      posts.push(path)
      await route.fulfill({ json: { success: true, data: { output: '' } } })
      return
    }
    if (path === '/packages/node-status' && fx.nodeStatus) {
      const { status, json } = fx.nodeStatus()
      await route.fulfill({ status, json })
      return
    }
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: false }
    else if (path === '/packages/docker-status') data = { installed: true, version: '27.0.0', running: true, compose_available: true }
    else if (path === '/packages/node-status') data = { installed: false, version: '', nvm_installed: false, npm_version: '' }
    else if (path === '/packages/updates') data = {
      updates: [
        { name: 'curl', current_version: '8.5.0-1', new_version: '8.5.0-2', arch: 'amd64' },
        { name: 'openssl', current_version: '3.0.13-1', new_version: '3.0.13-2', arch: 'amd64' },
      ],
      total: 2,
      last_checked: '2026-11-17T10:00:00Z',
    }
    else if (path === '/packages/transaction') data = { transaction: null }
    else if (path === '/packages/search') data = { packages: [{ name: 'nginx', description: 'small, powerful, scalable web/proxy server', installed: true }], total: 1, query: 'nginx' }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/packages')
  return posts
}

test('removing a package asks first, names it and what goes with it', async ({ page }) => {
  const posts = await open(page)
  await page.getByRole('textbox', { name: 'Search packages' }).fill('nginx')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await page.getByRole('row', { name: /nginx/ }).getByRole('button', { name: 'Remove' }).click()

  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('heading', { name: 'Remove nginx?' })).toBeVisible()
  await expect(dialog).toContainText('Packages that depend on nginx may be removed with it.')
  await dialog.getByRole('button', { name: 'Cancel' }).click()
  await expect(dialog).toHaveCount(0)
  expect(posts).toEqual([])

  await page.getByRole('row', { name: /nginx/ }).getByRole('button', { name: 'Remove' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Remove package' }).click()
  await expect.poll(() => posts).toEqual(['/packages/remove'])
})

test('an upgrade asks first and says how many packages it takes', async ({ page }) => {
  const posts = await open(page)
  await page.getByRole('button', { name: 'Check for Updates' }).click()
  await expect(page.getByRole('table').getByText('openssl')).toBeVisible()
  await page.getByRole('button', { name: 'Upgrade All' }).click()

  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('heading', { name: 'Upgrade all 2 packages?' })).toBeVisible()
  await expect(dialog).toContainText('Services of the upgraded packages may restart')
  await dialog.getByRole('button', { name: 'Cancel' }).click()
  await expect(dialog).toHaveCount(0)
  expect(posts).toEqual([])
})

test('a Node.js status that could not be read is not offered an install', async ({ page }) => {
  let fail = true
  await open(page, {
    nodeStatus: () => fail
      ? { status: 500, json: { success: false, error: { code: 'INTERNAL_ERROR', message: 'nvm.sh did not load' } } }
      : { status: 200, json: { success: true, data: { installed: true, version: 'v22.11.0', nvm_installed: true, npm_version: '10.9.0' } } },
  })
  await expect(page.getByText('Could not check whether it is installed', { exact: false })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Install Node.js' })).toHaveCount(0)

  fail = false
  await page.getByRole('button', { name: 'Retry' }).click()
  await expect(page.getByText('v22.11.0')).toBeVisible()
  await expect(page.getByText('Could not check whether it is installed', { exact: false })).toHaveCount(0)
})

test('installs that live on other screens say where, with a way there', async ({ page }) => {
  await open(page)
  const card = page.getByRole('region', { name: 'Installed from other screens' })
  await expect(card).toBeVisible()
  expect(await card.getByRole('link').evaluateAll((a) => a.map((l) => l.getAttribute('href'))))
    .toEqual(['/terminal', '/network/wireguard', '/network/tailscale', '/firewall/fail2ban', '/disk/overview', '/disk/network-shares', '/map'])
  await expect(card.getByRole('link', { name: 'Network / VPN › WireGuard' })).toBeVisible()
})
