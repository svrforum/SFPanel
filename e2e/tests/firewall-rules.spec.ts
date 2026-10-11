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

async function open(page: Page, answer: (path: string) => { status: number; json: unknown } = () => ok) {
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
    else if (path === '/firewall/status') data = { active: false, default_incoming: 'deny', default_outgoing: 'allow' }
    else if (path === '/firewall/rules') data = { rules, total: rules.length }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/firewall/rules')
  await expect(page.getByRole('row', { name: /22\/tcp/ })).toHaveCount(2)
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
