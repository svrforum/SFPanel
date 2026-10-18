import { test, expect, type Page } from '@playwright/test'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// Settings → Alerts against the shapes the alert handlers are tested with.
// Both sides read internal/feature/alert/testdata/page_contract.json: the Go
// tests post its channel and rule bodies to CreateChannel and CreateRule, pin
// ListRules to its rule rows and its rule types to what the server evaluates,
// and this spec checks the page sends the bodies, reads the rows and offers
// exactly those types. The page sent config as an object and the handler
// bound a string from the first release until #116, so adding a channel
// failed for every type while each side looked right on its own; and the rule
// form offered three types nothing evaluated until #132. Mocked API.

type ChannelBody = {
  name: string
  type: 'discord' | 'telegram' | 'webhook'
  config: Record<string, string>
  enabled: boolean
}

type RuleRow = {
  id: number
  name: string
  type: string
  condition: string
  cooldown: number
  enabled: boolean
  evaluated: boolean
}

const contract: { channel_creates: ChannelBody[]; rule_creates: { name: string }[]; rule_rows: RuleRow[]; rule_types: string[] } =
  JSON.parse(readFileSync(resolve(__dirname, '../../internal/feature/alert/testdata/page_contract.json'), 'utf8'))

// A channel for the rule form to pick: it refuses a rule without one.
const channel = { id: 1, name: 'ops', type: 'webhook', config: '{"webhook_url":"***hook"}', enabled: true, created_at: '2026-01-01T00:00:00Z' }

type Sent = { channels: unknown[]; rules: { type: string; severity: string; node_scope: string; node_ids: string }[]; deleted: string[] }

async function mock(page: Page, { channels = [] as unknown[], nodes = { nodes: [] } as unknown } = {}): Promise<Sent> {
  const sent: Sent = { channels: [], rules: [], deleted: [] }
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const path = new URL(req.url()).pathname
    let data: unknown = {}
    if (path.endsWith('/auth/setup-status')) data = { setup_required: false }
    else if (path.endsWith('/cluster/status')) data = { enabled: false }
    else if (path.endsWith('/cluster/nodes')) data = nodes
    else if (path.endsWith('/alerts/channels') && req.method() === 'POST') {
      sent.channels.push(req.postDataJSON())
      data = { id: sent.channels.length }
    } else if (path.endsWith('/alerts/channels')) data = channels
    else if (path.endsWith('/alerts/rules') && req.method() === 'POST') {
      sent.rules.push(req.postDataJSON())
      data = { id: 100 + sent.rules.length }
    } else if (path.endsWith('/alerts/rules')) data = contract.rule_rows
    else if (/\/alerts\/rules\/\d+$/.test(path) && req.method() === 'DELETE') sent.deleted.push(path)
    else if (path.endsWith('/alerts/history')) data = { items: [], total: 0, page: 1, limit: 20 }
    await route.fulfill({ json: { success: true, data } })
  })
  return sent
}

test('adding a channel sends each type in the shape the handler is tested with', async ({ page }) => {
  const sent = await mock(page)
  await page.goto('/settings?tab=alerts')

  for (const body of contract.channel_creates) {
    // While the form is open the header button reads Cancel, so this name
    // is the header's when closed and the submit's when open.
    await page.getByRole('button', { name: 'Add Channel', exact: true }).click()
    const name = page.getByPlaceholder('e.g. Ops Team Discord')
    await name.fill(body.name)
    await page.getByRole('combobox').click()
    await page.getByRole('option', { name: body.type, exact: false }).click()
    if (body.type === 'telegram') {
      await page.getByPlaceholder('123456:ABC-DEF...').fill(body.config.bot_token)
      await page.getByPlaceholder('-1001234567890').fill(body.config.chat_id)
    } else {
      // Discord and webhook share the one URL field, under different placeholders.
      await page.getByPlaceholder(body.type === 'discord'
        ? 'https://discord.com/api/webhooks/...'
        : 'https://your-receiver.example.com/hook').fill(body.config.webhook_url)
    }
    await page.getByRole('button', { name: 'Add Channel', exact: true }).click()
    // The form closes once the server has accepted the channel.
    await expect(name).toBeHidden()
  }

  expect(sent.channels).toEqual(contract.channel_creates)
})

test('a host-metric rule lists the threshold its condition carries', async ({ page }) => {
  await mock(page)
  await page.goto('/settings?tab=alerts')

  const { name, condition, cooldown } = contract.rule_rows.find(r => r.evaluated && r.type === 'cpu')!
  const { threshold } = JSON.parse(condition) as { threshold: number }
  await expect(page.getByText(name, { exact: true })).toBeVisible()
  // Page-wide and exact: the Login row carries the same condition text, and
  // listed the same threshold until #132.
  await expect(page.getByText(`Threshold: ${threshold}% | Cooldown: ${cooldown}s`, { exact: true })).toBeVisible()
})

test('a rule of a type nothing evaluates is listed as not alerting, and can only be deleted', async ({ page }) => {
  const sent = await mock(page)
  await page.goto('/settings?tab=alerts')

  const live = contract.rule_rows.find(r => r.evaluated)!
  const dead = contract.rule_rows.find(r => !r.evaluated)!
  // Stored enabled, as the page always created rules: the list said Active.
  expect(dead.enabled).toBe(true)

  const row = page.getByRole('listitem').filter({ hasText: dead.name })
  await expect(row.getByText('Not alerting', { exact: true })).toBeVisible()
  await expect(row.getByText(`The panel does not evaluate ${dead.type} rules, so this one has never sent an alert and never will. Delete it.`, { exact: true })).toBeVisible()
  await expect(row.getByText('Active', { exact: true })).toHaveCount(0)
  await expect(row.getByText(/Threshold/)).toHaveCount(0)
  await expect(row.getByRole('button', { name: /^(Enable|Disable)$/ })).toHaveCount(0)

  // Only that row: the evaluated one is listed as it always was.
  const other = page.getByRole('listitem').filter({ hasText: live.name })
  await expect(other.getByText('Active', { exact: true })).toBeVisible()
  await expect(other.getByRole('button', { name: 'Disable', exact: true })).toBeVisible()
  await expect(other.getByText('Not alerting', { exact: true })).toHaveCount(0)

  await row.getByRole('button', { name: 'Delete', exact: true }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Confirm', exact: true }).click()
  await expect.poll(() => sent.deleted).toEqual([`/api/v1/alerts/rules/${dead.id}`])
})

test('the rule form offers the types the server evaluates, and only those', async ({ page }) => {
  const sent = await mock(page, { channels: [channel] })
  await page.goto('/settings?tab=alerts')

  // One rule through the form for each type it offers. The Type select is the
  // form's first combobox; nothing else on the page has one while the
  // channel form is closed.
  await page.getByRole('button', { name: 'Add Rule', exact: true }).click()
  await page.getByRole('combobox').first().click()
  const offered = await page.getByRole('option').count()
  await page.keyboard.press('Escape')
  for (let i = 0; i < offered; i++) {
    if (i > 0) await page.getByRole('button', { name: 'Add Rule', exact: true }).click()
    const name = page.getByPlaceholder('e.g. CPU Overload Warning')
    await name.fill(`rule ${i}`)
    await page.getByRole('combobox').first().click()
    await page.getByRole('option').nth(i).click()
    await page.getByRole('checkbox', { name: channel.name }).click()
    await page.getByRole('button', { name: 'Add Rule', exact: true }).click()
    // The form closes once the server has accepted the rule.
    await expect(name).toBeHidden()
  }

  expect(sent.rules.map(r => r.type).sort()).toEqual([...contract.rule_types].sort())
})

test('an OOM rule shows the Critical it always fires with, instead of a choice', async ({ page }) => {
  const sent = await mock(page, { channels: [channel] })
  await page.goto('/settings?tab=alerts')

  await page.getByRole('button', { name: 'Add Rule', exact: true }).click()
  await page.getByPlaceholder('e.g. CPU Overload Warning').fill('oom')
  // The form's comboboxes, in order: Type, Severity, Node Scope.
  const type = page.getByRole('combobox').nth(0)
  const severity = page.getByRole('combobox').nth(1)
  await expect(severity).toBeEnabled()
  await type.click()
  await page.getByRole('option', { name: 'Container OOM Kill', exact: true }).click()
  await expect(severity).toBeDisabled()
  await expect(severity).toHaveText('Critical')
  await expect(page.getByText('An OOM kill is always sent as Critical.', { exact: true })).toBeVisible()
  await page.getByRole('checkbox', { name: channel.name }).click()
  await page.getByRole('button', { name: 'Add Rule', exact: true }).click()

  await expect.poll(() => sent.rules.map(r => [r.type, r.severity])).toEqual([['container_oom', 'critical']])
})

test('a rule scoped to specific nodes is not sent until a node is picked', async ({ page }) => {
  // A cluster node: the picker offers this node only.
  const sent = await mock(page, { channels: [channel], nodes: { nodes: [{ id: 'node-a', name: 'node-a' }], local_id: 'node-a' } })
  await page.goto('/settings?tab=alerts')

  await page.getByRole('button', { name: 'Add Rule', exact: true }).click()
  await page.getByPlaceholder('e.g. CPU Overload Warning').fill('scoped')
  await page.getByRole('checkbox', { name: channel.name }).click()
  // The form's comboboxes, in order: Type, Severity, Node Scope.
  await page.getByRole('combobox').nth(2).click()
  await page.getByRole('option', { name: 'Specific Nodes', exact: true }).click()
  await page.getByRole('button', { name: 'Add Rule', exact: true }).click()
  await expect(page.getByText('Select a node, or apply the rule to all nodes', { exact: true })).toBeVisible()
  expect(sent.rules).toEqual([])

  await page.getByRole('button', { name: 'node-a', exact: true }).click()
  await page.getByRole('button', { name: 'Add Rule', exact: true }).click()
  await expect.poll(() => sent.rules.map(r => [r.node_scope, r.node_ids])).toEqual([['specific', '["node-a"]']])
})

// The down rule's "also alert on intentional stops" box is a key in the
// condition text, which only the dispatcher reads: the Go test creates this
// body and checks the rule it makes fires for a docker stop.
test('a container-down rule sends the stop option in the shape the dispatcher is tested with', async ({ page }) => {
  const sent = await mock(page, { channels: [channel] })
  await page.goto('/settings?tab=alerts')

  const [body] = contract.rule_creates
  await page.getByRole('button', { name: 'Add Rule', exact: true }).click()
  const name = page.getByPlaceholder('e.g. CPU Overload Warning')
  await name.fill(body.name)
  // The rule form's first select is its type; the channel form is closed.
  await page.getByRole('combobox').first().click()
  await page.getByRole('option', { name: 'Container Down', exact: true }).click()
  await page.getByRole('checkbox', { name: 'Also alert when a container is stopped on purpose' }).click()
  await page.getByRole('checkbox', { name: channel.name }).click()
  await page.getByRole('button', { name: 'Add Rule', exact: true }).click()
  // The form closes once the server has accepted the rule.
  await expect(name).toBeHidden()

  expect(sent.rules).toEqual(contract.rule_creates)
})
