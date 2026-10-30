import { test, expect, type Locator, type Page } from '@playwright/test'

// Leader operations — node labels, handing the leadership over, removing a
// node — were offered on the leader's screen alone (#66), though the server
// forwards a follower's request to the leader. Every node offers them now,
// and a follower can take the leadership itself. The cluster's name can be
// changed from any node too (#67). Mocked API; `calls` is what the page asked
// the server to change.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const base = { role: 'voter', status: 'online', grpc_address: '192.168.1.x:3629', joined_at: '2026-01-01T00:00:00Z', last_seen: '2026-01-01T00:00:00Z' }
const nodes = [
  { ...base, id: 'node-a', name: 'alpha', api_address: '192.168.1.x:3628' },
  { ...base, id: 'node-b', name: 'beta', api_address: '192.168.1.x:3628' },
  { ...base, id: 'node-c', name: 'gamma', api_address: '192.168.1.x:3628' },
]

interface Call { method: string; path: string; body: unknown }

async function mock(page: Page, localId: string, leaderId = 'node-a') {
  const calls: Call[] = []
  let name = 'homelab'
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace('/api/v1', '')
    if (req.method() !== 'GET') {
      const body = req.postData() ? req.postDataJSON() : null
      calls.push({ method: req.method(), path, body })
      if (path === '/cluster/name') {
        const wanted = (body as { name: string }).name
        if ([...wanted].length > 64) {
          await route.fulfill({ status: 400, json: { success: false, error: { code: 'INVALID_NAME', message: 'Cluster name must be 1-64 characters with no control characters' } } })
          return
        }
        name = wanted
      }
    }
    const isLeader = localId === leaderId
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: true, name, node_count: 3, leader_id: leaderId, local_id: localId, is_leader: isLeader }
    else if (path === '/cluster/nodes') data = { nodes, local_id: localId, is_leader: isLeader }
    else if (path === '/cluster/overview') data = { name, node_count: 3, leader_id: leaderId, nodes, metrics: [] }
    else if (path === '/cluster/events') data = { events: [] }
    else if (path === '/cluster/name') data = { name }
    else if (path === '/cluster/leader-transfer') data = { message: 'Leadership transfer initiated', target_node_id: 'node-b' }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  return calls
}

const row = (page: Page, name: string) => page.getByRole('row').filter({ hasText: name })

// The row's operations live in its menu: open it, read what it offers, close it.
async function actions(page: Page, r: Locator) {
  await r.getByRole('button', { name: 'Node actions' }).click()
  const menu = page.getByRole('menu')
  await expect(menu).toBeVisible()
  const offered = {
    labels: await menu.getByRole('menuitem', { name: 'Edit group and labels' }).count(),
    transfer: await menu.getByRole('menuitem', { name: 'Transfer leadership' }).count(),
    remove: await menu.getByRole('menuitem', { name: 'Delete' }).count(),
  }
  await page.keyboard.press('Escape')
  await expect(menu).toHaveCount(0)
  return offered
}

test('a follower offers the leader operations, and can take the leadership itself', async ({ page }) => {
  const calls = await mock(page, 'node-b')
  await page.goto('/cluster/nodes')
  await expect(row(page, 'gamma')).toBeVisible()

  // The leader: labels only — it cannot be removed, and it already leads.
  expect(await actions(page, row(page, 'alpha'))).toEqual({ labels: 1, transfer: 0, remove: 0 })
  // This node: removing it is leaving, which is elsewhere.
  expect(await actions(page, row(page, 'beta'))).toEqual({ labels: 1, transfer: 1, remove: 0 })
  expect(await actions(page, row(page, 'gamma'))).toEqual({ labels: 1, transfer: 1, remove: 1 })

  await row(page, 'beta').getByRole('button', { name: 'Node actions' }).click()
  await page.getByRole('menu').getByRole('menuitem', { name: 'Transfer leadership' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Confirm' }).click()
  await expect.poll(() => calls.find(c => c.path === '/cluster/leader-transfer')?.body).toEqual({ target_node_id: 'node-b' })
})

test('the leader offers what it always did', async ({ page }) => {
  await mock(page, 'node-a')
  await page.goto('/cluster/nodes')
  await expect(row(page, 'gamma')).toBeVisible()
  expect(await actions(page, row(page, 'alpha'))).toEqual({ labels: 1, transfer: 0, remove: 0 })
  expect(await actions(page, row(page, 'beta'))).toEqual({ labels: 1, transfer: 1, remove: 1 })
  expect(await actions(page, row(page, 'gamma'))).toEqual({ labels: 1, transfer: 1, remove: 1 })
})

test('any node renames the cluster, and the sidebar shows it at once', async ({ page }) => {
  const calls = await mock(page, 'node-b')
  await page.goto('/cluster/overview')
  await expect(page.getByRole('heading', { name: 'homelab' })).toBeVisible()
  await expect(page.getByTitle('homelab')).toBeVisible()

  await page.getByRole('button', { name: 'Rename' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('textbox')).toHaveValue('homelab')
  await dialog.getByRole('textbox').fill('  lab-2  ')
  await dialog.getByRole('button', { name: 'Rename' }).click()

  await expect.poll(() => calls.find(c => c.path === '/cluster/name')).toEqual({ method: 'PUT', path: '/cluster/name', body: { name: 'lab-2' } })
  await expect(page.getByRole('heading', { name: 'lab-2' })).toBeVisible()
  // Well inside the shell's 15-second poll: the page told it.
  await expect(page.getByTitle('lab-2')).toBeVisible({ timeout: 3000 })
})

test('a refused name says why', async ({ page }) => {
  await mock(page, 'node-b')
  await page.goto('/cluster/overview')
  await page.getByRole('button', { name: 'Rename' }).click()
  await page.getByRole('dialog').getByRole('textbox').fill('x'.repeat(65))
  await page.getByRole('dialog').getByRole('button', { name: 'Rename' }).click()
  await expect(page.getByText('A name is 1–64 characters, without control characters such as line breaks.')).toBeVisible()
  await expect(page.getByRole('heading', { name: 'homelab' })).toBeVisible()
})
