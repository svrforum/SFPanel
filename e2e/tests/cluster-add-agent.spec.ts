import { test, expect, type Page } from '@playwright/test'

// Adding an agent from the node list: the dialog makes an agent token, hands
// out the two join lines `sfpanel cluster token --agent` prints, and waits for
// the agent to show up. The old token page lives on under "Add a manager
// instead". Mocked API.
test.use({ viewport: { width: 1280, height: 900 }, locale: 'en-US' })

const pin = 'sha256:' + 'ab'.repeat(32)
const installer = 'curl -fsSL https://raw.githubusercontent.com/svrforum/SFPanel/main/scripts/install.sh'
const base = { status: 'online', role: 'voter', grpc_address: '192.0.2.10:3629', joined_at: '2026-01-01T00:00:00Z', last_seen: '2026-01-01T00:00:00Z' }
const leader = { ...base, id: 'm-1', name: 'node-a', kind: 'manager', api_address: '192.0.2.10:3628' }
const agent1 = { ...base, id: 'agent-1', name: 'agent-01', kind: 'agent', role: '', api_address: '198.51.100.1:3628' }
const joined = { ...base, id: 'agent-7', name: 'agent-07', kind: 'agent', role: '', api_address: '198.51.100.7:3628' }

const hours = (h: number) => new Date(Date.now() + h * 3600_000).toISOString()

interface Opts {
  /** What POST /cluster/token answers, in order; the last one repeats. */
  tokens?: Record<string, unknown>[]
  /** The node list for the n-th GET /cluster/nodes after the first token (1-based). */
  nodesAfterToken?: (n: number) => unknown[]
  pending?: () => unknown[]
  revokeStatus?: number
}

interface Call { method: string; path: string; body: unknown }

async function mock(page: Page, opts: Opts = {}) {
  const calls: Call[] = []
  const tokens = opts.tokens ?? [{ token: 'tok123', kind: 'agent', expires_at: hours(24), advertise_address: '192.0.2.10', grpc_port: 3629, ca_pin: pin }]
  let minted = 0
  let polls = 0
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    const w = window as unknown as { clusterChanged: number }
    w.clusterChanged = 0
    window.addEventListener('sfpanel:cluster-changed', () => { w.clusterChanged++ })
  })
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace('/api/v1', '')
    if (req.method() !== 'GET') calls.push({ method: req.method(), path, body: req.postData() ? req.postDataJSON() : null })
    if (req.method() === 'DELETE' && path.startsWith('/cluster/tokens/') && opts.revokeStatus === 404) {
      await route.fulfill({ status: 404, json: { success: false, error: { code: 'NOT_FOUND', message: 'token not found' } } })
      return
    }
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: true, name: 'lab', node_count: 2, leader_id: 'm-1', local_id: 'm-1', is_leader: true }
    else if (path === '/cluster/nodes') {
      let nodes: unknown[] = [leader, agent1]
      if (minted > 0 && opts.nodesAfterToken) nodes = opts.nodesAfterToken(++polls)
      data = { nodes, local_id: 'm-1', is_leader: true }
    } else if (path === '/cluster/overview') data = { name: 'lab', node_count: 2, leader_id: 'm-1', nodes: [leader, agent1], metrics: [] }
    else if (path === '/cluster/events') data = { events: [] }
    else if (path === '/cluster/tokens' && req.method() === 'GET') data = { tokens: opts.pending?.() ?? [] }
    else if (path === '/cluster/token') data = tokens[Math.min(minted++, tokens.length - 1)]
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  return calls
}

async function openDialog(page: Page) {
  await page.goto('/cluster/nodes')
  await page.getByRole('button', { name: 'Add agent' }).click()
  const dialog = page.getByRole('dialog', { name: 'Add an agent' })
  await expect(dialog).toBeVisible()
  return dialog
}

const line = (page: Page, text: string) => page.getByRole('dialog').getByText(text, { exact: true })

test('shows the agent join lines for an agent token', async ({ page }) => {
  const calls = await mock(page)
  const dialog = await openDialog(page)
  const join = `sfpanel agent join 192.0.2.10:3629 tok123 --ca ${pin}`

  await expect(line(page, `sudo ${join}`)).toBeVisible()
  await expect(line(page, `${installer} | sudo bash -s -- --agent 192.0.2.10:3629 tok123 --ca ${pin}`)).toBeVisible()
  await expect(dialog.getByText('Server without SFPanel')).toBeVisible()
  await expect(dialog.getByText('Server with SFPanel')).toBeVisible()
  await expect(dialog.getByText(/^Token valid for 2[34]:\d\d:\d\d$/)).toBeVisible()
  await expect(dialog.getByText('Waiting for the agent to join…')).toBeVisible()
  // An agent token, with the server's default lifetime.
  expect(calls.filter((c) => c.path === '/cluster/token').map((c) => c.body)).toEqual([{ ttl: '', kind: 'agent' }])
})

test('drops sudo as root and adds a valid name', async ({ page }) => {
  await mock(page)
  const dialog = await openDialog(page)
  const args = `192.0.2.10:3629 tok123 --ca ${pin}`

  await dialog.getByRole('checkbox', { name: /Run as root, without sudo/ }).click()
  await expect(line(page, `sfpanel agent join ${args}`)).toBeVisible()
  await expect(line(page, `${installer} | bash -s -- --agent ${args}`)).toBeVisible()
  await expect(dialog.getByText(/sudo (bash|sfpanel)/)).toHaveCount(0)

  const name = dialog.getByRole('textbox', { name: 'Name (optional)' })
  await name.fill('agent-07')
  await expect(line(page, `sfpanel agent join ${args} --name agent-07`)).toBeVisible()
  await expect(line(page, `${installer} | bash -s -- --agent ${args} --name agent-07`)).toBeVisible()

  // A name the agent would refuse: no command that cannot work.
  await name.fill('bad name!')
  await expect(dialog.getByText('Letters, digits, dot, dash and underscore; starts with a letter or digit; up to 63 characters.')).toBeVisible()
  await expect(dialog.getByText(/sfpanel agent join/)).toHaveCount(0)
  await expect(name).toHaveAttribute('aria-invalid', 'true')
})

test('says when the agent has joined', async ({ page }) => {
  await mock(page, {
    nodesAfterToken: (n) => (n < 3 ? [leader, agent1] : [leader, agent1, joined]),
  })
  const dialog = await openDialog(page)
  await dialog.getByRole('textbox', { name: 'Name (optional)' }).fill('agent-07')

  await expect(dialog.getByText('agent-07 joined the cluster.')).toBeVisible({ timeout: 15000 })
  await expect(dialog.getByText(/sfpanel agent join/)).toHaveCount(0)
  expect(await page.evaluate(() => (window as unknown as { clusterChanged: number }).clusterChanged)).toBeGreaterThan(0)

  await dialog.getByRole('button', { name: 'Open node' }).click()
  await expect(page).toHaveURL(/\/dashboard$/)
  expect(await page.evaluate(() => localStorage.getItem('sfpanel_current_node'))).toBe('agent-7')
})

// An older leader ignores the kind and makes a manager token: pasted into an
// agent command, it would hand that server the cluster's secrets. Nobody sees
// it, so it is revoked straight away, once: an older leader answers an id it
// does not know with 401, which would sign the operator out.
// The token's id is the first 12 hex digits of its raw half (token.go's
// tokenFingerprint, the same since v0.19.0).
const oldRaw = '0123456789abcdef'.repeat(3)
const oldToken = `${oldRaw}.${'f'.repeat(64)}`
test('revokes the manager token an old leader made, once, and says so', async ({ page }) => {
  const calls = await mock(page, { tokens: [{ token: oldToken, expires_at: hours(24), advertise_address: '192.0.2.10', grpc_port: 3629, ca_pin: pin }] })
  const dialog = await openDialog(page)
  await expect(dialog.getByText("The cluster's leader runs a version without agents and made a manager token instead. Update the cluster first. The token it made has been revoked.")).toBeVisible()
  await expect(dialog.getByText(/sfpanel agent join/)).toHaveCount(0)
  await expect(dialog.getByText(new RegExp(oldRaw))).toHaveCount(0)
  await expect(dialog.getByText('Waiting for the agent to join…')).toHaveCount(0)
  // Re-renders (the countdown ticks every second) do not revoke again.
  await page.waitForTimeout(2500)
  expect(calls.filter((c) => c.method === 'DELETE').map((c) => c.path)).toEqual(['/cluster/tokens/0123456789ab'])
})

test('says an old leader\'s token is still valid when it could not be revoked', async ({ page }) => {
  const calls = await mock(page, { revokeStatus: 404, tokens: [{ token: oldToken, expires_at: hours(24), advertise_address: '192.0.2.10', grpc_port: 3629, ca_pin: pin }] })
  const dialog = await openDialog(page)
  await expect(dialog.getByText(/It is still valid: revoke it under “Add a manager instead”\.$/)).toBeVisible()
  await expect(dialog.getByText(/has been revoked/)).toHaveCount(0)
  expect(calls.filter((c) => c.method === 'DELETE').map((c) => c.path)).toEqual(['/cluster/tokens/0123456789ab'])
})

test('offers a new token once the old one expires', async ({ page }) => {
  await mock(page, {
    tokens: [
      { token: 'tok-old', kind: 'agent', expires_at: new Date(Date.now() + 3000).toISOString(), advertise_address: '192.0.2.10', grpc_port: 3629, ca_pin: pin },
      { token: 'tok-new', kind: 'agent', expires_at: hours(24), advertise_address: '192.0.2.10', grpc_port: 3629, ca_pin: pin },
    ],
  })
  const dialog = await openDialog(page)
  await expect(dialog.getByText(/tok-old/).first()).toBeVisible()
  await expect(dialog.getByText('The token has expired.')).toBeVisible({ timeout: 10000 })
  await expect(dialog.getByText(/tok-old/)).toHaveCount(0)

  await dialog.getByRole('button', { name: 'New token' }).click()
  await expect(line(page, `sudo sfpanel agent join 192.0.2.10:3629 tok-new --ca ${pin}`)).toBeVisible()
  await expect(dialog.getByText('The token has expired.')).toHaveCount(0)
})

test('takes the old tokens address to the dialog', async ({ page }) => {
  await mock(page)
  await page.goto('/cluster/tokens')
  await expect(page).toHaveURL(/\/cluster\/nodes\?add=agent$/)
  await expect(page.getByRole('dialog', { name: 'Add an agent' })).toBeVisible()
  // The tab is gone with the page it led to.
  await expect(page.getByRole('link', { name: 'Tokens' })).toHaveCount(0)

  // Closed, it does not open again on the next visit to the same address.
  await page.getByRole('dialog').getByRole('button', { name: 'Close' }).first().click()
  await expect(page).toHaveURL(/\/cluster\/nodes$/)
})

// The join command is pasted on another server, and a Proxmox VE host has no
// sudo: there a command that starts with sudo stops the && chain before
// anything is installed. (Was cluster-tokens.spec.ts.)
test('a manager token under Advanced leaves sudo out for a server whose operator is root', async ({ page }) => {
  await mock(page, {
    tokens: [
      { token: 'tok-agent', kind: 'agent', expires_at: hours(24), advertise_address: '192.0.2.10', grpc_port: 3629, ca_pin: pin },
      { token: 'tok123', kind: 'manager', expires_at: hours(24), advertise_address: '192.0.2.10', grpc_port: 3629, ca_pin: pin },
    ],
  })
  const dialog = await openDialog(page)
  await dialog.getByText('Add a manager instead').click()
  await expect(dialog.getByText('A manager holds the cluster\'s JWT key, CA key and admin account. Add one only to a server you trust as much as this one.')).toBeVisible()
  await dialog.getByRole('button', { name: 'Generate Token' }).click()

  const join = `sfpanel cluster join 192.0.2.10:3629 tok123 --ca ${pin}`
  const advanced = dialog.locator('details')
  await expect(advanced.getByText(`sudo ${join}`, { exact: true })).toBeVisible()
  await expect(advanced.getByText(`${installer} | sudo bash && sudo ${join}`, { exact: true })).toBeVisible()

  await advanced.getByRole('checkbox', { name: /Run as root, without sudo/ }).click()
  await expect(advanced.getByText(join, { exact: true })).toBeVisible()
  await expect(advanced.getByText(`${installer} | bash && ${join}`, { exact: true })).toBeVisible()
  await expect(advanced.getByText(/sudo (bash|sfpanel)/)).toHaveCount(0)
})

// The leader answers 404 for a token that was used or expired meanwhile. A 401
// there used to sign the operator out.
test('revoking a token that is gone keeps the operator signed in', async ({ page }) => {
  let pending = [{ id: 'tid-1', masked: 'tok1…', expires_at: hours(2), created_by: 'admin', kind: 'agent' }]
  const calls = await mock(page, { pending: () => pending, revokeStatus: 404 })
  const dialog = await openDialog(page)
  await dialog.getByText('Add a manager instead').click()

  const list = dialog.locator('details')
  await expect(list.getByText('tok1…')).toBeVisible()
  await expect(list.getByText('Agent', { exact: true })).toBeVisible()
  // Gone on the leader by the time it is revoked.
  pending = []
  await list.getByRole('button', { name: 'Revoke' }).click()
  await page.getByRole('alertdialog').or(page.getByRole('dialog').last()).getByRole('button', { name: 'Confirm' }).click()

  await expect(list.getByText('No active tokens')).toBeVisible()
  expect(calls.some((c) => c.method === 'DELETE' && c.path === '/cluster/tokens/tid-1')).toBe(true)
  await expect(page).toHaveURL(/\/cluster\/nodes/)
  await expect(page.getByText('token not found')).toHaveCount(0)
  await expect(page.getByRole('dialog', { name: 'Add an agent' })).toBeVisible()
})
