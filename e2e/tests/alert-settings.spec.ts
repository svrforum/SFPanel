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

async function mock(page: Page, { channels = [] as unknown[], nodes = { nodes: [] } as unknown, failing = new Set<string>() } = {}): Promise<Sent> {
  const sent: Sent = { channels: [], rules: [], deleted: [] }
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const path = new URL(req.url()).pathname
    if (req.method() === 'GET' && [...failing].some(f => path.endsWith(f))) {
      await route.fulfill({ status: 500, json: { success: false, error: { code: 'INTERNAL_ERROR', message: 'database is locked' } } })
      return
    }
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
      await page.getByPlaceholder('123456:ABC-DEF…').fill(body.config.bot_token)
      await page.getByPlaceholder('-1001234567890').fill(body.config.chat_id)
    } else {
      // Discord and webhook share the one URL field, under different placeholders.
      await page.getByPlaceholder(body.type === 'discord'
        ? 'https://discord.com/api/webhooks/…'
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
  // The question names the rule, and the button says what it does.
  const dialog = page.getByRole('dialog', { name: `Delete rule "${dead.name}"?` })
  await dialog.getByRole('button', { name: 'Delete rule', exact: true }).click()
  await expect.poll(() => sent.deleted).toEqual([`/api/v1/alerts/rules/${dead.id}`])
})

// The shared confirmation cleared its words the moment it was answered, so
// for the 200ms it animated away the title went blank and the red button
// turned into a blue "Confirm". Animations slowed twentyfold hold it on screen.
test('a confirmation keeps its title and its button while it closes', async ({ page }) => {
  await mock(page)
  await page.goto('/settings?tab=alerts')

  const dead = contract.rule_rows.find(r => !r.evaluated)!
  await page.getByRole('listitem').filter({ hasText: dead.name }).getByRole('button', { name: 'Delete', exact: true }).click()
  const dialog = page.getByRole('dialog')
  const title = dialog.getByRole('heading')
  await expect(title).not.toHaveText('')
  const titleText = await title.textContent()
  const confirm = dialog.locator('[data-slot="dialog-footer"] [data-slot="button"]').last()
  const label = await confirm.textContent()
  await expect(confirm).toHaveAttribute('data-variant', 'destructive')

  const cdp = await page.context().newCDPSession(page)
  await cdp.send('Animation.enable')
  await cdp.send('Animation.setPlaybackRate', { playbackRate: 0.05 })
  await confirm.click()
  await expect(dialog).toHaveAttribute('data-state', 'closed')
  await expect(title).toHaveText(titleText!)
  await expect(confirm).toHaveText(label!)
  await expect(confirm).toHaveAttribute('data-variant', 'destructive')
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

test('a rule on what a network scan finds has nothing to set, and says what makes it fire', async ({ page }) => {
  const sent = await mock(page, { channels: [channel] })
  await page.goto('/settings?tab=alerts')

  await page.getByRole('button', { name: 'Add Rule', exact: true }).click()
  await page.getByPlaceholder('e.g. CPU Overload Warning').fill('strangers')
  await page.getByRole('combobox').first().click()
  await page.getByRole('option', { name: 'Network: new device' }).click()
  // No percentage and no container pattern: a scan decides.
  await expect(page.getByRole('spinbutton', { name: /Threshold/ })).toHaveCount(0)
  await expect(page.getByTestId('rule-lan-hint')).toHaveText('Judged when a scan of a network ends (Running → Network). With automatic scans on, it checks by itself.')

  await page.getByRole('combobox').first().click()
  await page.getByRole('option', { name: 'Network: watched device gone' }).click()
  await expect(page.getByTestId('rule-lan-hint')).toContainText('a device marked “Watch” on its card is not found by two scans in a row')

  await page.getByRole('checkbox', { name: channel.name }).click()
  await page.getByRole('button', { name: 'Add Rule', exact: true }).click()
  await expect(page.getByPlaceholder('e.g. CPU Overload Warning')).toBeHidden()
  expect(sent.rules.map((r) => [r.type, r.condition])).toEqual([['lan_device_down', '{}']])
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

// A list that could not be read said it was empty: "No channels configured"
// sent an operator to set up the same channel again, and the rule form told
// them to add one first.
test('lists that could not be read say so, with a retry, not that they are empty', async ({ page }) => {
  const failing = new Set(['/alerts/channels', '/alerts/rules', '/alerts/history'])
  await mock(page, { failing })
  await page.goto('/settings?tab=alerts')
  for (const text of ['Could not load the alert channels', 'Could not load the alert rules', 'Could not load the alert history']) {
    await expect(page.getByText(text, { exact: true }).first()).toBeVisible()
  }
  for (const text of ['No channels configured', 'No rules configured', 'No alert history']) {
    await expect(page.getByText(text, { exact: true })).toHaveCount(0)
  }
  await page.getByRole('button', { name: 'Add Rule', exact: true }).click()
  await expect(page.getByText('Add a channel first', { exact: true })).toHaveCount(0)

  failing.clear()
  const retry = page.getByRole('button', { name: 'Retry' })
  for (let left = 3; left > 0; left--) {
    await expect(retry).toHaveCount(left)
    await retry.first().click()
  }
  await expect(page.getByText('No channels configured', { exact: true })).toBeVisible()
  await expect(page.getByText('No alert history', { exact: true })).toBeVisible()
  await expect(page.getByText('Could not load the alert rules', { exact: true })).toHaveCount(0)
})

// The number fields had no name at all, the others only a placeholder, and a
// refusal was a toast that left the field unmarked.
test('every field of the forms has a name, and a refusal is marked on its field', async ({ page }) => {
  await mock(page, { channels: [channel] })
  await page.goto('/settings?tab=alerts')

  await page.getByRole('button', { name: 'Add Rule', exact: true }).click()
  for (const label of ['Rule Name', 'Threshold (%)', 'Cooldown (seconds)']) await expect(page.getByLabel(label, { exact: true })).toBeVisible()
  for (const label of ['Type', 'Severity', 'Node Scope']) await expect(page.getByRole('combobox', { name: label })).toBeVisible()
  await expect(page.getByRole('group', { name: 'Notification Channels' })).toBeVisible()
  await page.getByRole('button', { name: 'Add Rule', exact: true }).click()
  const ruleName = page.getByLabel('Rule Name', { exact: true })
  await expect(ruleName).toHaveAttribute('aria-invalid', 'true')
  await expect(ruleName).toBeFocused()
  await expect(ruleName).toHaveAccessibleDescription('Rule name is required')
  await page.getByRole('button', { name: 'Cancel' }).click()

  await page.getByRole('button', { name: 'Add Channel', exact: true }).click()
  await page.getByLabel('Channel Name').fill('ops-2')
  await page.getByRole('button', { name: 'Add Channel', exact: true }).click()
  const url = page.getByLabel('Webhook URL')
  await expect(url).toHaveAttribute('aria-invalid', 'true')
  await expect(url).toBeFocused()
  await expect(url).toHaveAccessibleDescription('Webhook URL is required')
})

// A channel row did not wrap: on a phone its buttons ran past the page's
// clipped edge, and Delete could not be reached.
test.describe('a phone', () => {
  test.use({ viewport: { width: 320, height: 640 }, hasTouch: true })
  test('a channel row keeps every button on screen', async ({ page }) => {
    await mock(page, { channels: [{ ...channel, name: 'operations-team-discord' }] })
    await page.goto('/settings?tab=alerts')
    const row = page.getByText('operations-team-discord', { exact: true })
    await expect(row).toBeVisible()
    for (const name of ['Disable', 'Test', 'Delete']) {
      const box = (await page.getByRole('button', { name, exact: true }).first().boundingBox())!
      expect(box.x).toBeGreaterThanOrEqual(0)
      expect(box.x + box.width).toBeLessThanOrEqual(320)
    }
  })
})
