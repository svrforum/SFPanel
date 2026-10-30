import { test, expect, type Page } from '@playwright/test'

// The security page rotates the cluster's four secrets and follows each one
// node by node through GET /cluster/rotation, which only the leader answers.
// A refusal names whom it waits for; a JWT rotation ends this very session; a
// CA rotation is stopped only before promote. Mocked API, time fixed where a
// countdown is read.
test.use({ viewport: { width: 1400, height: 1000 }, locale: 'en-US', timezoneId: 'UTC' })

const NOW = new Date('2026-10-29T09:30:00Z')
const OLD = `sha256:${'a'.repeat(64)}`
const NEXT = `sha256:${'b'.repeat(64)}`

interface StateOpts {
  proxyPhase?: string
  /** null: the cluster never rotated its CA. */
  caPhase?: string | null
  reason?: string
}

function state(o: StateOpts = {}) {
  const proxyPhase = o.proxyPhase ?? 'next'
  const caPhase = o.caPhase === undefined ? 'reissue' : o.caPhase
  const counting = caPhase === 'trust' || caPhase === 'reissue'
  return {
    jwt: {
      generation: 1,
      rotated_at: '2026-10-29T09:00:00Z',
      managers: [
        { id: 'm-1', name: 'manager-01', online: true, generation: 1, can_rotate: true },
        { id: 'm-2', name: 'manager-02', online: false, can_rotate: true },
      ],
    },
    proxy: {
      generation: 2,
      phase: proxyPhase,
      ...(proxyPhase === 'next' ? { waiting: ['manager-02 (offline)'] } : {}),
      managers: [
        { id: 'm-1', name: 'manager-01', online: true, generation: 2, can_rotate: true },
        { id: 'm-2', name: 'manager-02', online: false, generation: 1, can_rotate: true },
      ],
    },
    agent_keys: {
      generation: 1,
      started_at: '2026-10-29T09:10:00Z',
      pending: 1,
      agents: [
        { id: 'a-1', name: 'agent-01', online: true, pending: true, issued: true, can_rotate: true },
        { id: 'a-2', name: 'agent-02', online: true, pending: false, issued: false, can_rotate: false },
      ],
    },
    ca: {
      current_pin: OLD,
      raft_tls: true,
      ...(caPhase ? {
        rotation: {
          id: 'r-1', seq: 2, phase: caPhase,
          old_pin: OLD, next_pin: NEXT,
          started_at: '2026-10-29T09:20:00Z', phase_at: '2026-10-29T09:21:00Z', phase_timeout_seconds: 1800,
          ...(counting ? { deadline: '2026-10-29T09:51:00Z', waiting: ['agent-01 (still presents a certificate from the current CA)'] } : {}),
          ...(o.reason ? { reason: o.reason } : {}),
          nodes: [
            { id: 'm-1', name: 'manager-01', kind: 'manager', online: true, trusts: [OLD, NEXT], cert_ca: NEXT, can_rotate: true },
            { id: 'a-1', name: 'agent-01', kind: 'agent', online: true, trusts: [OLD, NEXT], cert_ca: OLD, can_rotate: true },
          ],
        },
      } : {}),
    },
  }
}

interface Reply { status: number; body: unknown }
const accepted = (data: unknown): Reply => ({ status: 202, body: { success: true, data } })
const refused = (status: number, code: string, message: string): Reply => ({ status, body: { success: false, error: { code, message } } })

interface Call { method: string; path: string; body: unknown }

interface Opts {
  /** What GET /cluster/rotation answers: the state, or 503 when the leader cannot be confirmed. */
  rotation?: () => ReturnType<typeof state> | 503
  /** POST path → its answer, and what happens once it is answered. */
  posts?: Record<string, { reply: Reply; then?: () => void }>
}

async function mock(page: Page, opts: Opts = {}) {
  const calls: Call[] = []
  await page.addInitScript(() => {
    if (!sessionStorage.getItem('signedOut')) sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
  })
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace('/api/v1', '')
    if (req.method() !== 'GET') calls.push({ method: req.method(), path, body: req.postData() ? req.postDataJSON() : null })
    const post = req.method() === 'POST' ? opts.posts?.[path] : undefined
    if (post) {
      await route.fulfill({ status: post.reply.status, json: post.reply.body })
      post.then?.()
      return
    }
    if (path === '/cluster/rotation') {
      const s = opts.rotation?.() ?? state()
      if (s === 503) {
        await route.fulfill({ status: 503, json: { success: false, error: { code: 'INTERNAL_ERROR', message: 'this node could not confirm it leads the cluster' } } })
        return
      }
      await route.fulfill({ json: { success: true, data: s } })
      return
    }
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: true, name: 'lab', node_count: 4, leader_id: 'm-1', local_id: 'm-1', is_leader: true }
    else if (path === '/cluster/nodes') data = { nodes: [], local_id: 'm-1', is_leader: true }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  return calls
}

const card = (page: Page, key: 'jwt' | 'proxy' | 'agent-keys' | 'ca') => page.getByTestId(`rotation-${key}`)
const posts = (calls: Call[]) => calls.filter((c) => c.method === 'POST').map((c) => c.path)

test('shows where each rotation stands', async ({ page }) => {
  await page.clock.setFixedTime(NOW)
  await mock(page)
  await page.goto('/cluster/security')

  // The cluster page's tab; the sidebar's datacenter menu has the same link.
  await expect(page.getByRole('main').getByRole('link', { name: 'Security' })).toHaveAttribute('aria-current', 'page')
  await expect(page.getByText("Rotate the cluster's secrets after a manager is converted to an agent or removed")).toBeVisible()

  const jwt = card(page, 'jwt')
  await expect(jwt).toContainText('JWT key')
  await expect(jwt).toContainText('Generation 1')
  // A manager whose heartbeat has not said which key it runs.
  await expect(jwt.getByRole('row', { name: /manager-02/ })).toContainText('—')

  const proxy = card(page, 'proxy')
  await expect(proxy).toContainText('Generation 2')
  await expect(proxy.getByTestId('phase')).toHaveText('next')
  await expect(proxy.getByRole('list', { name: 'Waiting for' })).toHaveText('manager-02 (offline)')

  const keys = card(page, 'agent-keys')
  await expect(keys).toContainText('1 agent still to switch')
  await expect(keys.getByRole('columnheader', { name: 'Supports rotation' })).toBeVisible()
  await expect(keys.getByRole('row', { name: /agent-02/ })).toContainText('Runs a version that does not support rotation')
  await expect(keys.getByRole('row', { name: /agent-02/ })).toContainText('Switched')
  await expect(keys.getByRole('row', { name: /agent-01/ })).toContainText('Runs a version that supports rotation')

  const ca = card(page, 'ca')
  await expect(ca.locator('[aria-current="step"]')).toHaveText('reissue')
  await expect(ca.getByRole('list', { name: 'Waiting for' })).toHaveText('agent-01 (still presents a certificate from the current CA)')
  await expect(ca).toContainText('Goes back at 09:51 AM unless the phase completes')
  await expect(ca.getByTestId('countdown')).toHaveText('21:00')
  // The agent still presents a certificate from the current CA, the manager from the next.
  await expect(ca.getByRole('row', { name: /agent-01/ }).getByTestId('cert-ca')).toHaveText('Current')
  await expect(ca.getByRole('row', { name: /manager-01/ }).getByTestId('cert-ca')).toHaveText('Next')

  // The countdown moves with the clock.
  await page.clock.setFixedTime(new Date(NOW.getTime() + 61_000))
  await expect(ca.getByTestId('countdown')).toHaveText('19:59')
})

test('starts a proxy rotation and follows it', async ({ page }) => {
  let phase = 'next'
  const calls = await mock(page, {
    rotation: () => state({ proxyPhase: phase, caPhase: null }),
    posts: {
      '/cluster/rotate/proxy': {
        reply: accepted({ generation: 3, message: 'Every manager takes the new internal proxy secret' }),
        then: () => { phase = 'switched' },
      },
    },
  })
  await page.goto('/cluster/security')
  const proxy = card(page, 'proxy')
  await expect(proxy.getByTestId('phase')).toHaveText('next')

  await proxy.getByRole('button', { name: 'Rotate' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toContainText('Managers take the new secret, switch to it once all of them have it, and accept the old one for two more minutes. No session ends.')
  expect(posts(calls)).toEqual([])
  await dialog.getByRole('button', { name: 'Rotate' }).click()

  await expect.poll(() => posts(calls)).toEqual(['/cluster/rotate/proxy'])
  await expect(proxy.getByTestId('phase')).toHaveText('switched')
  await expect(proxy.getByRole('list', { name: 'Waiting for' })).toHaveCount(0)
  // Still signed in: no session ends with this one.
  await expect(page).toHaveURL(/\/cluster\/security$/)
})

test('shows why a rotation was refused', async ({ page }) => {
  const calls = await mock(page, {
    rotation: () => state({ caPhase: 'done' }),
    posts: {
      '/cluster/rotate/agent-keys': {
        reply: refused(409, 'CLUSTER_ROTATION_REFUSED', 'the rotation was refused: a rotation waits for every node it changes to be online and run a version that follows it; waiting for peer-2 (offline)'),
      },
      '/cluster/rotate/ca': { reply: refused(409, 'UPDATE_IN_PROGRESS', 'a cluster update is in progress') },
    },
  })
  await page.goto('/cluster/security')

  const keys = card(page, 'agent-keys')
  await keys.getByRole('button', { name: 'Rotate' }).click()
  await expect(page.getByRole('dialog')).toContainText('Every agent takes a new key at its next heartbeat; an offline agent takes it once it is back.')
  await page.getByRole('dialog').getByRole('button', { name: 'Rotate' }).click()
  await expect.poll(() => posts(calls)).toEqual(['/cluster/rotate/agent-keys'])
  const alert = keys.getByRole('alert')
  await expect(alert).toContainText('The rotation did not start')
  await expect(alert).toContainText('waiting for peer-2 (offline)')
  // The refusal is the agent keys' own: the other cards say nothing.
  await expect(card(page, 'proxy').getByRole('alert')).toHaveCount(0)

  // An update holds a CA rotation back: the same title, in the dialog.
  await card(page, 'ca').getByRole('button', { name: 'Rotate' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('textbox').fill('lab')
  await dialog.getByRole('button', { name: 'Rotate' }).click()
  await expect(dialog.getByRole('alert')).toContainText('The rotation did not start')
  await expect(dialog.getByRole('alert')).toContainText('An update is already running.')
})

test('a JWT rotation signs the operator out', async ({ page }) => {
  const calls = await mock(page, {
    posts: {
      '/cluster/rotate/jwt': {
        reply: { status: 200, body: { success: true, data: { generation: 2, message: 'The cluster has a new JWT key: every session has ended, this one included — sign in again' } } },
      },
    },
  })
  await page.goto('/cluster/security')

  await card(page, 'jwt').getByRole('button', { name: 'Rotate' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toContainText('A new JWT key ends every session, this one included: you will sign in again. Open terminals and log streams stay open.')
  // Keep the login page from finding the fixture token again on its way in.
  await page.evaluate(() => sessionStorage.setItem('signedOut', '1'))
  await dialog.getByRole('button', { name: 'Rotate' }).click()

  await expect.poll(() => posts(calls)).toEqual(['/cluster/rotate/jwt'])
  await expect(page).toHaveURL(/\/login$/)
  expect(await page.evaluate(() => sessionStorage.getItem('token'))).toBeNull()
})

test('a CA rotation needs the cluster name and sends the phase time', async ({ page }) => {
  let phase = 'done'
  const calls = await mock(page, {
    rotation: () => state({ caPhase: phase }),
    posts: {
      '/cluster/rotate/ca': {
        reply: accepted({ rotation: { id: 'r-2', seq: 5, phase: 'trust', old_pin: OLD, next_pin: NEXT, started_at: NOW.toISOString(), phase_at: NOW.toISOString(), phase_timeout_seconds: 3600 }, message: 'The cluster CA is being replaced' }),
        then: () => { phase = 'trust' },
      },
    },
  })
  await page.goto('/cluster/security')
  const ca = card(page, 'ca')
  await expect(ca.locator('[aria-current="step"]')).toHaveText('done')

  await ca.getByRole('button', { name: 'Rotate' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toContainText('The riskiest rotation. Every node, agents included, must be online and run a version that supports it.')
  await expect(dialog).toContainText('past promote there is no way back')

  const timeout = dialog.getByRole('combobox', { name: 'Time allowed per phase' })
  await expect(timeout).toHaveText('30 minutes')
  await timeout.click()
  await page.getByRole('option', { name: '1 hour' }).click()
  await expect(timeout).toHaveText('1 hour')

  const go = dialog.getByRole('button', { name: 'Rotate' })
  await expect(go).toBeDisabled()
  await dialog.getByRole('textbox').fill('la')
  await expect(go).toBeDisabled()
  await dialog.getByRole('textbox').fill('lab')
  await go.click()

  await expect.poll(() => calls.filter((c) => c.method === 'POST')).toEqual([
    { method: 'POST', path: '/cluster/rotate/ca', body: { phase_timeout: '1h' } },
  ])
  await expect(dialog).toHaveCount(0)
  await expect(ca.locator('[aria-current="step"]')).toHaveText('trust')
})

test('offers to stop a CA rotation only before promote', async ({ page }) => {
  let phase = 'reissue'
  let reason: string | undefined
  const calls = await mock(page, {
    rotation: () => state({ caPhase: phase, reason }),
    posts: {
      '/cluster/rotate/ca/abort': {
        reply: accepted({ rotation: { id: 'r-1', seq: 3, phase: 'rollback', reason: 'aborted by the operator' }, message: 'The CA rotation goes back' }),
        then: () => { phase = 'rollback'; reason = 'aborted by the operator' },
      },
    },
  })
  await page.goto('/cluster/security')
  const ca = card(page, 'ca')
  const stop = ca.getByRole('button', { name: 'Stop and keep the current CA' })
  await expect(stop).toBeVisible()
  // No new rotation while one runs.
  await expect(ca.getByRole('button', { name: 'Rotate' })).toBeDisabled()

  await stop.click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toContainText('Stop and keep the current CA')
  expect(posts(calls)).toEqual([])
  await dialog.getByRole('button', { name: 'Stop and keep the current CA' }).click()

  await expect.poll(() => posts(calls)).toEqual(['/cluster/rotate/ca/abort'])
  // Going back is the warning branch, with its reason.
  await expect(ca.locator('[aria-current="step"]')).toHaveText('rollback')
  await expect(ca).toContainText('aborted by the operator')
  await expect(stop).toHaveCount(0)

  // Past promote there is no way back: nothing offers one.
  phase = 'promote'
  reason = undefined
  await page.reload()
  await expect(ca.locator('[aria-current="step"]')).toHaveText('promote')
  await expect(ca.getByRole('button', { name: 'Stop and keep the current CA' })).toHaveCount(0)
  await expect(ca.getByTestId('countdown')).toHaveCount(0)
})

test('keeps the last state when the leader cannot be confirmed', async ({ page }) => {
  await page.clock.install({ time: NOW })
  let leaderLost = false
  await mock(page, { rotation: () => (leaderLost ? 503 : state()) })
  await page.goto('/cluster/security')
  await expect(card(page, 'proxy').getByTestId('phase')).toHaveText('next')

  leaderLost = true
  await page.clock.runFor(5100)
  await expect(page.getByRole('status').filter({ hasText: 'Could not reach the leader' })).toContainText('this node could not confirm it leads the cluster')
  await expect(card(page, 'proxy').getByTestId('phase')).toHaveText('next')

  leaderLost = false
  await page.clock.runFor(5100)
  await expect(page.getByText('Could not reach the leader')).toHaveCount(0)
})

// Between two polls the leader may move past the point of no return, and the
// stop is refused: the rotation runs on, so it did not "not start".
test('says a refused stop did not stop the rotation', async ({ page }) => {
  const why = "the next CA is the cluster's already: a rotation cannot be undone past that point"
  const calls = await mock(page, {
    posts: { '/cluster/rotate/ca/abort': { reply: refused(409, 'CLUSTER_ROTATION_REFUSED', why) } },
  })
  await page.goto('/cluster/security')
  const ca = card(page, 'ca')
  await ca.getByRole('button', { name: 'Stop and keep the current CA' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Stop and keep the current CA' }).click()
  await expect.poll(() => posts(calls)).toEqual(['/cluster/rotate/ca/abort'])

  const alert = ca.getByRole('alert')
  await expect(alert).toContainText('The rotation was not stopped')
  await expect(alert).toContainText(why)
  await expect(alert).not.toContainText('The rotation did not start')
})
