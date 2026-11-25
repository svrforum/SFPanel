import { test, expect, type Page } from '@playwright/test'

// Firewall › Open Ports is the port map: one table of every port, with its
// addresses, a search and a protocol filter, and a rule to add on each row
// something listens on. The old Port Map address opens it. REST is a fixture.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const row = (o: Record<string, unknown> & { port: number }) => ({ proto: 'tcp', state: 'listening', firewall: null, containers: [], process: null, ...o })
const ROWS = [
  row({ port: 22, process: { pid: 812, name: 'sshd' }, addresses: ['0.0.0.0', '::'] }),
  // An older node sends no addresses.
  row({ port: 53, proto: 'udp', process: { pid: 640, name: 'resolver' }, firewall: { action: 'ALLOW', scope: 'Anywhere', rule_id: 1, source: 'ufw' } }),
  // Published by Docker with no listener on the host: the container port rules tab's.
  row({ port: 8080, state: 'bound', containers: [{ id: 'c1', name: 'web', stack: 'site' }], addresses: ['0.0.0.0'] }),
]

const UFW = { active: true, default_incoming: 'deny', default_outgoing: 'allow', installed: true, manager: 'ufw' }
const PVE = { active: false, default_incoming: '', default_outgoing: '', installed: false, manager: 'pve-firewall' }

// `afterRule` is what the port map says once a rule has been posted. `reads`
// records each read of the port map, true for one after a post: in dev,
// StrictMode reads it twice on mount, so only the reads after count.
async function open(page: Page, status: unknown, at = '/firewall/ports', afterRule = ROWS) {
  const posts: unknown[] = []
  const reads: boolean[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace('/api/v1', '')
    if (req.method() === 'POST' && path === '/firewall/rules') {
      posts.push(req.postDataJSON())
      await route.fulfill({ json: { success: true, data: { message: 'ok', output: '' } } })
      return
    }
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: false }
    else if (path === '/system/portmap') {
      reads.push(posts.length > 0)
      data = posts.length > 0 ? afterRule : ROWS
    }
    else if (path === '/firewall/status') data = status
    else if (path === '/docker/containers') data = []
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto(at)
  return { posts, reads }
}

const ruleButtons = (page: Page) => page.getByRole('button', { name: /^Add rule for/ })

test('a Proxmox host offers no rule for a port', async ({ page }) => {
  await open(page, PVE)
  await expect(page.getByRole('row', { name: /sshd/ })).toBeVisible()
  await expect(ruleButtons(page)).toHaveCount(0)
})

test('a ufw host offers a rule where something listens', async ({ page }) => {
  await open(page, UFW)
  await expect(page.getByRole('row', { name: /sshd/ })).toBeVisible()
  await expect(ruleButtons(page)).toHaveCount(2)
  await expect(page.getByRole('row', { name: /8080/ }).getByRole('button')).toHaveCount(0)
})

test("a rule from a row posts that row's port and protocol", async ({ page }) => {
  const { posts } = await open(page, UFW)
  await page.getByRole('button', { name: 'Add rule for 53/UDP' }).click()
  await page.getByRole('dialog', { name: 'Add rule for 53/UDP' }).getByRole('button', { name: 'Add Rule' }).click()
  await expect.poll(() => posts).toEqual([expect.objectContaining({ port: '53', protocol: 'udp', action: 'allow' })])
})

// The firewall column showed the old rule until Refresh was pressed.
test('an added rule shows in its row at once', async ({ page }) => {
  const added = { action: 'ALLOW', scope: '198.51.100.0/24', rule_id: 2, source: 'ufw' }
  const { posts, reads } = await open(page, UFW, '/firewall/ports', ROWS.map((r) => (r.port === 53 ? { ...r, firewall: added } : r)))
  const row = page.getByRole('row', { name: /\b53\b/ })
  await expect(row.getByText('Anywhere')).toBeVisible()
  await page.getByRole('button', { name: 'Add rule for 53/UDP' }).click()
  const dialog = page.getByRole('dialog', { name: 'Add rule for 53/UDP' })
  await dialog.getByLabel('Source IP').fill('198.51.100.0/24')
  await dialog.getByRole('button', { name: 'Add Rule' }).click()
  await expect.poll(() => posts).toEqual([expect.objectContaining({ port: '53', protocol: 'udp', from: '198.51.100.0/24' })])
  await expect.poll(() => reads.filter((afterPost) => afterPost).length).toBe(1)
  await expect(row.getByText('198.51.100.0/24')).toBeVisible()
  await expect(row.getByText('Anywhere')).toHaveCount(0)
})

test("an older node's row has no address", async ({ page }) => {
  await open(page, UFW)
  await expect(page.getByRole('row', { name: /sshd/ })).toBeVisible()
  const headers = await page.getByRole('columnheader').allTextContents()
  const address = headers.indexOf('Address')
  expect(address).toBeGreaterThanOrEqual(0)
  await expect(page.getByRole('row', { name: /\b53\b/ }).getByRole('cell').nth(address)).toHaveText('—')
  await expect(page.getByRole('row', { name: /sshd/ }).getByRole('cell').nth(address)).toHaveText('0.0.0.0, ::')
})

test("a search finds a container's stack", async ({ page }) => {
  await open(page, UFW)
  await expect(page.getByRole('row', { name: /sshd/ })).toBeVisible()
  await page.getByPlaceholder('Search ports, processes, addresses, containers…').fill('site')
  await expect(page.getByRole('row', { name: /8080/ })).toBeVisible()
  await expect(page.getByRole('row', { name: /\b22\b/ })).toHaveCount(0)
  await expect(page.getByRole('row', { name: /\b53\b/ })).toHaveCount(0)
})

test('the port map address opens the one table', async ({ page }) => {
  await open(page, UFW, '/firewall/portmap')
  await expect(page).toHaveURL(/\/firewall\/ports$/)
  const main = page.getByRole('main')
  for (const tab of ['Firewall Rules', 'Open Ports', 'Access Blocking', 'Container Port Rules']) {
    await expect(main.getByRole('link', { name: tab, exact: true })).toHaveCount(1)
  }
  await expect(main.getByRole('link', { name: 'Port Map' })).toHaveCount(0)
  await expect(main.getByRole('link', { name: 'Logs' })).toHaveCount(0)
})

test('a container in a row opens that container', async ({ page }) => {
  await open(page, UFW)
  await page.getByRole('row', { name: /8080/ }).getByRole('link', { name: /web/ }).click()
  await expect(page).toHaveURL(/\/docker\/containers\?container=c1$/)
})
