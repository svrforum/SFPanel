import { test, expect, type Page } from '@playwright/test'

// The migration dialog, for a stack on an agent. The target refuses an agent's
// stack that asks for host access unless the operator acknowledged it, and by
// then the agent has stopped the stack: the dialog now asks first. It also
// stops before a target the pre-flight could not reach, says why a migration
// did not start instead of "Migration failed to start", and shows what kind
// of node each one is. Mocked API.
test.use({ viewport: { width: 1400, height: 1000 }, locale: 'en-US' })

const base = { status: 'online', grpc_address: '192.0.2.1:3629', joined_at: '2026-01-01T00:00:00Z', last_seen: new Date().toISOString() }
const nodes = [
  { ...base, id: 'm-1', name: 'm-1', kind: 'manager', role: 'voter', api_address: '192.0.2.1:3628' },
  { ...base, id: 'm-2', name: 'm-2', kind: 'manager', role: 'voter', api_address: '192.0.2.2:3628' },
  { ...base, id: 'agent-1', name: 'agent-1', kind: 'agent', role: '', api_address: '198.51.100.1:3628' },
  { ...base, id: 'agent-2', name: 'agent-2', kind: 'agent', role: '', api_address: '198.51.100.2:3628' },
]
const stacks = [
  { node_id: 'm-1', node_name: 'm-1', local: true, status: 'online', stacks: [] },
  {
    node_id: 'agent-1', node_name: 'agent-1', local: false, status: 'online',
    stacks: [{ name: 'demo', compose_file: 'docker-compose.yml', has_env: false, path: '/opt/stacks/demo', service_count: 1, running_count: 1, real_status: 'running' }],
  },
]

const RISKY = 'the stack asks for host access that runs on the target as it is: '
  + 'service "web" binds sensitive host path "/var/run/docker.sock" at "/var/run/docker.sock", '
  + 'service "web" sets privileged: true'

interface Opts {
  report: { blocks: unknown[]; warnings: unknown[] }
  start?: { status: number; json: unknown }
}

async function mock(page: Page, opts: Opts) {
  const starts: { url: string; body: Record<string, unknown> }[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
  })
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace('/api/v1', '')
    if (path === '/docker/compose/demo/migrate' && req.method() === 'POST') {
      starts.push({ url: req.url(), body: req.postDataJSON() })
      if (opts.start) {
        await route.fulfill({ status: opts.start.status, json: opts.start.json })
      } else {
        await route.fulfill({
          status: 200,
          headers: { 'content-type': 'text/event-stream' },
          body: 'data: {"phase":"preflight","message":"Running pre-flight checks...","done":false}\n\n'
            + 'data: {"phase":"done","message":"Migrated","done":true}\n\n',
        })
      }
      return
    }
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: true, name: 'lab', node_count: nodes.length, leader_id: 'm-1', local_id: 'm-1', is_leader: true }
    else if (path === '/cluster/nodes') data = { nodes, local_id: 'm-1', is_leader: true }
    else if (path === '/cluster/overview') data = { name: 'lab', node_count: nodes.length, leader_id: 'm-1', nodes, metrics: [] }
    else if (path === '/cluster/events') data = { events: [] }
    else if (path === '/docker/compose/cluster-stacks') data = stacks
    else if (path === '/docker/compose/demo/migrate/preflight') data = opts.report
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  return starts
}

async function openDialog(page: Page) {
  await page.goto('/cluster/stacks')
  await page.getByRole('button', { name: 'Migrate to node' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByText('From: agent-1')).toBeVisible()
  return dialog
}

async function pickTarget(page: Page, name: string) {
  const dialog = page.getByRole('dialog')
  await dialog.getByRole('combobox', { name: 'Target node' }).click()
  await page.getByRole('option', { name: new RegExp(`^${name}\\b`) }).click()
}

test('asks to acknowledge what an agent stack is allowed to do', async ({ page }) => {
  const starts = await mock(page, { report: { blocks: [], warnings: [{ code: 'compose-risky', message: RISKY }] } })
  const dialog = await openDialog(page)
  await pickTarget(page, 'm-2')
  await dialog.getByRole('button', { name: 'Run pre-flight' }).click()

  // Each finding on its own line, under the same heading a risky save uses.
  await expect(dialog.getByText('This stack asks for host privileges')).toBeVisible()
  await expect(dialog.getByText('service "web" binds sensitive host path "/var/run/docker.sock" at "/var/run/docker.sock"', { exact: true })).toBeVisible()
  await expect(dialog.getByText('service "web" sets privileged: true', { exact: true })).toBeVisible()

  const ack = dialog.getByRole('checkbox', { name: 'I understand this stack runs on the target with the host access listed above.' })
  const startButton = dialog.getByRole('button', { name: 'Start migration' })
  await expect(ack).not.toBeChecked()
  await expect(startButton).toBeDisabled()
  await ack.click()
  await expect(startButton).toBeEnabled()
  await startButton.click()

  await expect(dialog.getByText('Migrated').first()).toBeVisible()
  expect(starts).toHaveLength(1)
  expect(starts[0].url).toContain('node=agent-1')
  expect(starts[0].body).toMatchObject({ targetNodeId: 'm-2', acknowledgeRisks: true })
})

test('does not start toward an unreachable target', async ({ page }) => {
  const starts = await mock(page, {
    report: { blocks: [], warnings: [{ code: 'target-unreachable', message: 'could not query the target node; disk/port/arch/overwrite checks were skipped' }] },
  })
  const dialog = await openDialog(page)
  await pickTarget(page, 'agent-2')
  await dialog.getByRole('button', { name: 'Run pre-flight' }).click()
  await expect(dialog.getByText('could not query the target node', { exact: false })).toBeVisible()
  await expect(dialog.getByRole('button', { name: 'Start migration' })).toBeDisabled()
  expect(starts).toHaveLength(0)
})

test('says why the migration did not start', async ({ page }) => {
  await mock(page, {
    report: { blocks: [], warnings: [] },
    start: { status: 409, json: { success: false, error: { code: 'ALREADY_EXISTS', message: 'a migration for this stack is already in progress' } } },
  })
  const dialog = await openDialog(page)
  await pickTarget(page, 'm-2')
  await dialog.getByRole('button', { name: 'Run pre-flight' }).click()
  await expect(dialog.getByText('Pre-flight passed')).toBeVisible()
  await dialog.getByRole('button', { name: 'Start migration' }).click()
  await expect(dialog.getByText('a migration for this stack is already in progress')).toBeVisible()
  await expect(dialog.getByText('Migration failed to start')).toHaveCount(0)
})

test('shows the kind of each node', async ({ page }) => {
  await mock(page, { report: { blocks: [], warnings: [] } })
  const dialog = await openDialog(page)
  // The source, on the From line.
  await expect(dialog.getByTestId('migrate-source').getByText('Agent', { exact: true })).toBeVisible()
  // Every online node but the source is a target, each with its kind.
  await dialog.getByRole('combobox', { name: 'Target node' }).click()
  const options = page.getByRole('option')
  await expect(options).toHaveCount(3)
  await expect(page.getByRole('option', { name: /^m-1\b/ })).toContainText('Leader')
  await expect(page.getByRole('option', { name: /^m-2\b/ })).toContainText('Manager')
  await expect(page.getByRole('option', { name: /^agent-2\b/ })).toContainText('Agent')
})
