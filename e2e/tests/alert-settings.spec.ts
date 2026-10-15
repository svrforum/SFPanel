import { test, expect, type Page } from '@playwright/test'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'

// Settings → Alerts against the shapes the alert handlers are tested with.
// Both sides read internal/feature/alert/testdata/page_contract.json: the Go
// tests post its channel bodies to CreateChannel and pin ListRules to its rule
// row, and this spec checks the page sends the one and reads the other. The
// page sent config as an object and the handler bound a string from the first
// release until #116, so adding a channel failed for every type while each
// side looked right on its own. Mocked API.

type ChannelBody = {
  name: string
  type: 'discord' | 'telegram' | 'webhook'
  config: Record<string, string>
  enabled: boolean
}

const contract: { channel_creates: ChannelBody[]; rule_row: { name: string; condition: string; cooldown: number } } =
  JSON.parse(readFileSync(resolve(__dirname, '../../internal/feature/alert/testdata/page_contract.json'), 'utf8'))

async function mock(page: Page, posted: unknown[]) {
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
    else if (path.endsWith('/cluster/nodes')) data = { nodes: [] }
    else if (path.endsWith('/alerts/channels') && req.method() === 'POST') {
      posted.push(req.postDataJSON())
      data = { id: posted.length }
    } else if (path.endsWith('/alerts/channels')) data = []
    else if (path.endsWith('/alerts/rules')) data = [contract.rule_row]
    else if (path.endsWith('/alerts/history')) data = { items: [], total: 0, page: 1, limit: 20 }
    await route.fulfill({ json: { success: true, data } })
  })
}

test('adding a channel sends each type in the shape the handler is tested with', async ({ page }) => {
  const posted: unknown[] = []
  await mock(page, posted)
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

  expect(posted).toEqual(contract.channel_creates)
})

test('a host-metric rule lists the threshold its condition carries', async ({ page }) => {
  await mock(page, [])
  await page.goto('/settings?tab=alerts')

  const { name, condition, cooldown } = contract.rule_row
  const { threshold } = JSON.parse(condition) as { threshold: number }
  await expect(page.getByText(name, { exact: true })).toBeVisible()
  await expect(page.getByText(`Threshold: ${threshold}% | Cooldown: ${cooldown}s`, { exact: true })).toBeVisible()
})
