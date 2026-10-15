import { test, expect, type Page } from '@playwright/test'

// A stop, restart or disable on the Services page used to fire on the first
// click. Now every one is confirmed; a unit the host needs (Proxmox's web UI,
// sshd) asks for its name to be typed and sends it back as ?confirm=; and a
// refusal (networking on an ifupdown host) is worded in the panel language
// with the safe way to do it. REST is a fixture; `posts` records what the
// page sends.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const unit = (name: string, protection?: Record<string, string>) => ({
  name, description: `${name} fixture`, load_state: 'loaded', active_state: 'active', sub_state: 'running', enabled: 'enabled', protection,
})
const services = [
  unit('nginx.service'),
  unit('pveproxy.service', { stop: 'confirm', restart: 'confirm', disable: 'confirm' }),
  unit('networking.service', { stop: 'refuse', restart: 'refuse', disable: 'refuse' }),
  unit('ssh.service'),
]
const ok = { status: 200, json: { success: true, data: { message: 'done' } } }
type Answer = (pathAndQuery: string) => { status: number; json: unknown }

async function open(page: Page, answer: Answer = () => ok) {
  const posts: string[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname.replace('/api/v1', '')
    if (req.method() === 'POST') {
      posts.push(path + url.search)
      const { status, json } = answer(path + url.search)
      await route.fulfill({ status, json })
      return
    }
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: false }
    else if (path === '/system/services') data = { services, total: services.length }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/services')
  await expect(page.getByRole('row', { name: /nginx\.service/ })).toBeVisible()
  return posts
}

async function choose(page: Page, name: string, action: RegExp) {
  await page.getByRole('row', { name: new RegExp(name.replace('.', '\\.')) }).getByRole('button', { name: 'Actions' }).click()
  await page.getByRole('menuitem', { name: action }).click()
}

test('an ordinary stop is confirmed before it is sent', async ({ page }) => {
  const posts = await open(page)
  await choose(page, 'nginx.service', /^Stop/)
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByText('Stop nginx.service?')).toBeVisible()
  expect(posts).toEqual([])
  await dialog.getByRole('button', { name: 'Stop' }).click()
  await expect.poll(() => posts).toEqual(['/system/services/nginx.service/stop'])
})

test('a unit the host needs is sent only with its name typed', async ({ page }) => {
  const posts = await open(page)
  await choose(page, 'pveproxy.service', /^Stop/)
  const dialog = page.getByRole('dialog')
  const go = dialog.getByRole('button', { name: 'Stop' })
  await expect(go).toBeDisabled()
  await dialog.getByRole('textbox').fill('pveproxy')
  await expect(go).toBeDisabled()
  await dialog.getByRole('textbox').fill('pveproxy.service')
  await go.click()
  await expect.poll(() => posts).toEqual(['/system/services/pveproxy.service/stop?confirm=pveproxy.service'])
})

test('a server that asks for the name gets the typed confirmation', async ({ page }) => {
  // The list said nothing (an older node, or the list was stale); the server
  // holds the stop until the name is typed.
  const posts = await open(page, (p) => p === '/system/services/ssh.service/stop'
    ? { status: 409, json: { success: false, error: { code: 'SERVICE_CONFIRM_REQUIRED', message: 'Type ssh.service to confirm', details: { unit: 'ssh.service' } } } }
    : ok)
  await choose(page, 'ssh.service', /^Stop/)
  await page.getByRole('dialog').getByRole('button', { name: 'Stop' }).click()
  const typed = page.getByRole('dialog').filter({ hasText: 'To confirm, type:' })
  await typed.getByRole('textbox').fill('ssh.service')
  await typed.getByRole('button', { name: 'Stop' }).click()
  await expect.poll(() => posts).toEqual([
    '/system/services/ssh.service/stop',
    '/system/services/ssh.service/stop?confirm=ssh.service',
  ])
})

test('a refusal is worded in the panel language with the safe way', async ({ page }) => {
  await open(page, (p) => p.startsWith('/system/services/networking.service/')
    ? { status: 403, json: { success: false, error: { code: 'SERVICE_PROTECTED', message: 'Refusing to restart networking.service', details: { unit: 'networking.service', hint: 'ifreload -a' } } } }
    : ok)
  await choose(page, 'networking.service', /^Restart/)
  await expect(page.getByText("The panel won't do this to networking.service: the host, its cluster or its guests depend on it. To apply network changes, run 'ifreload -a' in a terminal instead.")).toBeVisible()
  await expect(page.getByText('Refusing to restart')).toHaveCount(0)
})
