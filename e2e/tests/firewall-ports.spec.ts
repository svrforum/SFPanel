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

// A host as it looks in use: both address families, a resolver on loopback,
// a container open to anywhere, rules with a source.
const HOST = [
  row({ port: 22, process: { pid: 812, name: 'sshd' }, addresses: ['0.0.0.0', '::'], firewall: { action: 'ALLOW', scope: 'Anywhere', rule_id: 1, source: 'ufw' } }),
  row({ port: 53, proto: 'udp', process: { pid: 640, name: 'systemd-resolve' }, addresses: ['127.0.0.53', '127.0.0.54'] }),
  row({ port: 3628, process: { pid: 1203, name: 'sfpanel' }, addresses: ['::'], firewall: { action: 'ALLOW', scope: '192.0.2.0/24', rule_id: 2, source: 'ufw' } }),
  row({ port: 8443, process: { pid: 2345, name: 'docker-proxy' }, addresses: ['0.0.0.0', '::'], containers: [{ id: 'c2', name: 'nextcloud-app', stack: 'nextcloud' }], firewall: { action: 'ALLOW', scope: 'Anywhere', rule_id: 3, source: 'ufw' } }),
  row({ port: 8000, process: { pid: 31337, name: 'docker-proxy' }, addresses: ['0.0.0.0', '::'], containers: [{ id: 'c3', name: 'paperless-ngx-webserver-1', stack: 'paperless-ngx' }], firewall: { action: 'ALLOW', scope: 'Anywhere (v6)', rule_id: 4, source: 'ufw' } }),
  row({ port: 41641, proto: 'udp', process: { pid: 980, name: 'tailscaled' }, addresses: ['0.0.0.0', '::'] }),
]

const UFW = { active: true, default_incoming: 'deny', default_outgoing: 'allow', installed: true, manager: 'ufw' }
const PVE = { active: false, default_incoming: '', default_outgoing: '', installed: false, manager: 'pve-firewall' }

// `rows` is the port map; `afterRule` is what it says once a rule has been
// posted. `reads` records each read of the port map, true for one after a
// post: in dev, StrictMode reads it twice on mount, so only the reads after
// count. `statusDelay` holds the firewall status back that many milliseconds;
// `statusAnswered` says whether it has been answered yet.
async function open(
  page: Page,
  status: unknown,
  { at = '/firewall/ports', rows = ROWS, afterRule = rows, statusDelay = 0 }: { at?: string; rows?: unknown[]; afterRule?: unknown[]; statusDelay?: number } = {},
) {
  const posts: unknown[] = []
  const reads: boolean[] = []
  const answered = { status: false }
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
    localStorage.setItem('sfpanel-sidebar-collapsed', 'false')
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
      data = posts.length > 0 ? afterRule : rows
    }
    else if (path === '/firewall/status') {
      if (statusDelay) await new Promise((r) => setTimeout(r, statusDelay))
      await route.fulfill({ json: { success: true, data: status } })
      answered.status = true
      return
    }
    else if (path === '/docker/containers') data = []
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto(at)
  return { posts, reads, statusAnswered: () => answered.status }
}

const ruleButtons = (page: Page) => page.getByRole('button', { name: /^Add rule for/ })

test('a Proxmox host offers no rule for a port', async ({ page }) => {
  await open(page, PVE)
  await expect(page.getByRole('row', { name: /sshd/ })).toBeVisible()
  await expect(ruleButtons(page)).toHaveCount(0)
})

// Until the status says ufw, no rule is offered: a Proxmox host answering
// late flashed a button that its firewall would refuse.
test('a Proxmox host that answers late offers no rule meanwhile', async ({ page }) => {
  const { statusAnswered } = await open(page, PVE, { statusDelay: 1500 })
  await expect(page.getByRole('row', { name: /sshd/ })).toBeVisible()
  let polls = 0
  while (!statusAnswered()) {
    expect(await ruleButtons(page).count()).toBe(0)
    polls++
    await page.waitForTimeout(50)
  }
  expect(polls).toBeGreaterThan(5)
  await page.waitForTimeout(300)
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
  const { posts, reads } = await open(page, UFW, { afterRule: ROWS.map((r) => (r.port === 53 ? { ...r, firewall: added } : r)) })
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
  await open(page, UFW, { at: '/firewall/portmap' })
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

// A 1024 px screen with the sidebar open has no room for every column: the
// address gives way, and the rule button stays on screen rather than past the
// table's right edge. The address is still searched, and a phone card shows it.
test.describe('at 1024 px', () => {
  test.use({ viewport: { width: 1024, height: 768 } })

  test('the rule button fits and the address column gives way', async ({ page }) => {
    await open(page, UFW, { rows: HOST })
    await expect(page.getByRole('row', { name: /nextcloud-app/ })).toBeVisible()
    // The standalone sidebar, open at its full 240 px.
    await expect.poll(async () => (await page.locator('aside').first().boundingBox())?.width).toBe(240)
    const button = ruleButtons(page).first()
    await expect(button).toBeAttached()
    const box = await button.boundingBox()
    expect(box).not.toBeNull()
    expect(box!.x).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width).toBeLessThanOrEqual(1024)
    // Nor clipped by the table's own sideways scroll.
    const table = await page.locator('[data-slot="table-container"]').first().boundingBox()
    expect(box!.x + box!.width).toBeLessThanOrEqual(table!.x + table!.width)
    await expect(page.getByRole('columnheader', { name: 'Process' })).toBeVisible()
    await expect(page.getByRole('columnheader', { name: 'Address' })).toBeHidden()
  })
})

test('at 1280 px the address column is shown, and the rule button still fits', async ({ page }) => {
  await open(page, UFW, { rows: HOST })
  await expect(page.getByRole('row', { name: /nextcloud-app/ })).toBeVisible()
  await expect(page.getByRole('columnheader', { name: 'Address' })).toBeVisible()
  const box = await ruleButtons(page).first().boundingBox()
  expect(box).not.toBeNull()
  expect(box!.x + box!.width).toBeLessThanOrEqual(1280)
})
