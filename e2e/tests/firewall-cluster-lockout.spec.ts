import { test, expect, type Page } from '@playwright/test'

// A cluster node refuses a firewall change that would close the ports the
// other nodes reach it on: 409 UFW_LOCKOUT with details.cluster_ports. The
// rules page worded every lockout as SSH or the panel and dropped the ports,
// and the listening-ports page showed the refusal as a toast with no way to
// go ahead. Mocked API; the refusals are the ones firewall_ufw.go sends — a
// denied port names that one port, enabling names every port left without an
// allow rule.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const rules = [{ number: 1, to: '22/tcp', action: 'ALLOW IN', from: 'Anywhere', v6: false }]
const ports = [{ protocol: 'tcp', address: '0.0.0.0', port: 3629, pid: 901, process: 'sfpanel' }]

const lockout = (message: string, clusterPorts: string) => ({
  status: 409,
  json: { success: false, error: { code: 'UFW_LOCKOUT', message, details: { cluster_ports: clusterPorts } } },
})
const denyRefusal = lockout('Refusing to deny port 3629 — this would close cluster port 3629, which the rest of the cluster reaches this node on. Pass ?force=true to confirm.', '3629')
const enableRefusal = lockout('Enabling UFW with no ALLOW rule for cluster port(s) 3629, 3630 would cut this node off from the rest of its cluster. Add an allow rule first, or pass ?force=true to confirm.', '3629, 3630')

async function open(page: Page, at: '/firewall/rules' | '/firewall/ports', active: boolean, shown = rules) {
  const posts: string[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname.replace('/api/v1', '')
    if (req.method() === 'POST' && (path === '/firewall/rules' || path === '/firewall/enable')) {
      posts.push(`${path}${url.search}`)
      if (url.searchParams.get('force') === 'true') await route.fulfill({ json: { success: true, data: { message: 'ok', output: '' } } })
      else await route.fulfill(path === '/firewall/enable' ? enableRefusal : denyRefusal)
      return
    }
    if (req.method() === 'DELETE') {
      // Deleting the only allow for a cluster port is refused the same way.
      posts.push(`DELETE ${path}${url.search}`)
      if (url.searchParams.get('force') === 'true') await route.fulfill({ json: { success: true, data: { message: 'ok' } } })
      else await route.fulfill(lockout('Deleting this rule would leave no ALLOW for cluster port(s) 3629, which the rest of the cluster reaches this node on. Pass ?force=true to confirm.', '3629'))
      return
    }
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: false }
    else if (path === '/firewall/status') data = { active, default_incoming: 'deny', default_outgoing: 'allow', installed: true, manager: 'ufw' }
    else if (path === '/firewall/rules') data = { rules: shown, total: shown.length }
    else if (path === '/firewall/ports') data = { ports, total: ports.length }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto(at)
  return posts
}

const clusterBody = (ports: string) => ports.includes(',')
  ? `This would close cluster ports ${ports}, which the other nodes use to reach this one. Allow them first, or apply it anyway only if you are sure.`
  : `This would close cluster port ${ports}, which the other nodes use to reach this one. Allow it first, or apply it anyway only if you are sure.`
// Titled for the cluster, not "may lock you out": the operator can still reach
// the server, so the generic "proceed only if you can still reach it" test
// would pass while the node drops out of its cluster.
const lockoutDialog = (page: Page) => page.getByRole('dialog').filter({ hasText: 'This change may cut this node off from the cluster' })

async function expectClusterLockout(page: Page, ports: string) {
  await expect(lockoutDialog(page).getByText(clusterBody(ports))).toBeVisible()
  await expect(lockoutDialog(page).getByText(/SSH \(22\)/)).toHaveCount(0)
  await expect(lockoutDialog(page).getByText(/lock you out|can still reach this server/)).toHaveCount(0)
}

async function deny(page: Page, dialogName: string) {
  const dialog = page.getByRole('dialog', { name: dialogName })
  await dialog.getByRole('combobox').first().click()
  await page.getByRole('option', { name: 'Deny' }).click()
  await dialog.getByRole('button', { name: 'Add Rule' }).click()
}

test('a rule that closes a cluster port names it', async ({ page }) => {
  const posts = await open(page, '/firewall/rules', true)
  await expect(page.getByRole('row', { name: /22\/tcp/ })).toBeVisible()
  await page.getByRole('button', { name: 'Add Rule' }).click()
  await page.getByRole('dialog').getByLabel('Port').fill('3629')
  await deny(page, 'Add Rule')
  await expectClusterLockout(page, '3629')
  await lockoutDialog(page).getByRole('button', { name: 'Proceed anyway' }).click()
  await expect.poll(() => posts).toEqual(['/firewall/rules', '/firewall/rules?force=true'])
  await expect(lockoutDialog(page)).toHaveCount(0)
})

test('enabling ufw without the cluster ports names them all', async ({ page }) => {
  await open(page, '/firewall/rules', false)
  await page.getByRole('button', { name: 'Enable' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Enable' }).click()
  await expectClusterLockout(page, '3629, 3630')
})

test('the ports page words the same refusal and can go ahead', async ({ page }) => {
  const posts = await open(page, '/firewall/ports', true)
  await page.getByRole('row', { name: /sfpanel/ }).getByRole('button', { name: 'Add UFW Rule' }).click()
  await deny(page, 'Add UFW Rule')
  await expectClusterLockout(page, '3629')
  await lockoutDialog(page).getByRole('button', { name: 'Proceed anyway' }).click()
  await expect.poll(() => posts).toEqual(['/firewall/rules', '/firewall/rules?force=true'])
  await expect(lockoutDialog(page)).toHaveCount(0)
})

// Editing adds the replacement first, so the same refusal comes from the add;
// it was a toast in the SSH wording with no way to go ahead. Going ahead
// forces the delete of the old allow too, which the guard refuses for the
// same port.
test('editing an allowed cluster port into a deny asks the same way', async ({ page }) => {
  const posts = await open(page, '/firewall/rules', true, [
    ...rules,
    { number: 2, to: '3629/tcp', action: 'ALLOW IN', from: 'Anywhere', v6: false },
  ])
  await page.getByRole('row', { name: /3629\/tcp/ }).getByRole('button', { name: 'Edit Rule' }).click()
  const edit = page.getByRole('dialog', { name: 'Edit Rule' })
  await edit.getByRole('combobox').first().click()
  await page.getByRole('option', { name: 'Deny' }).click()
  await edit.getByRole('button', { name: 'Save' }).click()
  await expectClusterLockout(page, '3629')
  expect(posts).toEqual(['/firewall/rules'])
  await lockoutDialog(page).getByRole('button', { name: 'Proceed anyway' }).click()
  await expect.poll(() => posts).toEqual(['/firewall/rules', '/firewall/rules?force=true', 'DELETE /firewall/rules/2?force=true'])
  await expect(lockoutDialog(page)).toHaveCount(0)
  await expect(edit).toHaveCount(0)
})
