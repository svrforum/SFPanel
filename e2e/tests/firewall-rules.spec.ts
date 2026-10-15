import { test, expect, type Page } from '@playwright/test'

// #110. A lockout refusal showed the server's English, ?force=true and all;
// every rule seemed to appear twice, its IPv6 copy unmarked; and the port field
// refused the "80, 443" its own example shows. REST is a fixture; `posts`
// records what the page sends.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const rules = [
  { number: 1, to: '22/tcp', action: 'ALLOW IN', from: 'Anywhere', v6: false },
  { number: 2, to: '22/tcp', action: 'ALLOW IN', from: 'Anywhere', v6: true },
]
const ok = { status: 200, json: { success: true, data: {} } }

const ufwStatus = { active: false, default_incoming: 'deny', default_outgoing: 'allow' }

async function open(page: Page, answer: (path: string) => { status: number; json: unknown } = () => ok, status: unknown = ufwStatus, waitForRules = true) {
  const posts: { path: string; body: string }[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace('/api/v1', '')
    if (req.method() === 'POST') {
      posts.push({ path, body: req.postData() ?? '' })
      const { status, json } = answer(path)
      await route.fulfill({ status, json })
      return
    }
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: false }
    else if (path === '/firewall/status') data = status
    else if (path === '/firewall/rules') data = { rules, total: rules.length }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/firewall/rules')
  if (waitForRules) await expect(page.getByRole('row', { name: /22\/tcp/ })).toHaveCount(2)
  return posts
}

test('an IPv6 rule is marked as one', async ({ page }) => {
  await open(page)
  await expect(page.getByText('IPv6', { exact: true })).toHaveCount(1)
})

test('a lockout refusal is worded in the panel language', async ({ page }) => {
  await open(page, (path) => path === '/firewall/enable'
    ? { status: 409, json: { success: false, error: { code: 'UFW_LOCKOUT', message: 'Enabling UFW with no ALLOW rule for SSH (port 22) or the panel port would lock you out. Add an allow rule first, or pass ?force=true to confirm.' } } }
    : ok)
  await page.getByRole('button', { name: 'Enable' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Enable' }).click()
  const lockout = page.getByRole('dialog').filter({ hasText: 'This change may lock you out' })
  await expect(lockout.getByText(/This change would block incoming SSH \(22\) or panel access/)).toBeVisible()
  await expect(page.getByText(/force=true/)).toHaveCount(0)
})

test('a port list is sent the way ufw takes it', async ({ page }) => {
  const posts = await open(page)
  await page.getByRole('button', { name: 'Add Rule' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Port').fill('80, 443')
  await dialog.getByRole('button', { name: 'Add Rule' }).click()
  await expect.poll(() => posts.find(p => p.path === '/firewall/rules')?.body ?? '').toContain('"port":"80,443"')
})

// A Proxmox host's firewall is pve-firewall. The page said "Inactive" and
// offered to enable ufw, whose default deny would cut the web UI on 8006.
test('a Proxmox host shows pve-firewall and no ufw controls', async ({ page }) => {
  await open(page, () => ok, {
    active: false, default_incoming: '', default_outgoing: '', installed: false, manager: 'pve-firewall',
    pve: { enabled: false, running: true, raw: 'Status: disabled/running' },
  }, false)
  await expect(page.getByRole('heading', { name: 'Firewall is managed by Proxmox' })).toBeVisible()
  await expect(page.getByText('Datacenter firewall')).toBeVisible()
  await expect(page.getByText('Disabled', { exact: true })).toBeVisible()
  await expect(page.getByText('Running', { exact: true })).toBeVisible()
  await expect(page.getByRole('link', { name: 'Open the Proxmox web UI' })).toHaveAttribute('href', /^https:\/\/[^/]+:8006$/)
  await expect(page.getByRole('button', { name: 'Enable' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Add Rule' })).toHaveCount(0)
  await expect(page.getByText('Inactive')).toHaveCount(0)
})

test('without ufw the page says it is not installed', async ({ page }) => {
  await open(page, () => ok, { active: false, default_incoming: '', default_outgoing: '', installed: false, manager: 'none' }, false)
  await expect(page.getByText('Not installed', { exact: true })).toBeVisible()
  await expect(page.getByText(/ufw is not installed on this host/)).toBeVisible()
  await expect(page.getByText('Inactive')).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Enable' })).toHaveCount(0)
})

// Only the lockout guard can be forced. Proxmox's refusal is a 409 too, and
// offering "Proceed anyway" for it would only get the same answer again.
test('a Proxmox refusal is not offered as a lockout to force', async ({ page }) => {
  await open(page, (path) => path === '/firewall/enable'
    ? { status: 409, json: { success: false, error: { code: 'FIREWALL_MANAGED_BY_PVE', message: "This host's firewall is managed by Proxmox VE (pve-firewall)." } } }
    : ok)
  await page.getByRole('button', { name: 'Enable' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Enable' }).click()
  await expect(page.getByText(/Change it in the Proxmox web UI \(:8006\) under Datacenter → Firewall/)).toBeVisible()
  await expect(page.getByRole('dialog').filter({ hasText: 'This change may lock you out' })).toHaveCount(0)
})

test('a lockout of the Proxmox web UI says so', async ({ page }) => {
  await open(page, (path) => path === '/firewall/enable'
    ? { status: 409, json: { success: false, error: { code: 'UFW_LOCKOUT', message: 'Enabling UFW with no ALLOW rule for the Proxmox web UI (port 8006) would cut this node\'s management.', details: { port: '8006' } } } }
    : ok)
  await page.getByRole('button', { name: 'Enable' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Enable' }).click()
  const lockout = page.getByRole('dialog').filter({ hasText: 'This change may lock you out' })
  await expect(lockout.getByText(/would block the Proxmox web UI \(8006\)/)).toBeVisible()
})
