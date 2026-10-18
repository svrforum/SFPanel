import { test, expect } from '@playwright/test'

// An install stream answers 200 and reports a failure in its own lines —
// "ERROR: …", then [DONE]. The install surfaces announced success all the
// same: the dialog turned red and a "Codex installed" toast came up over it.
// The tools sheet stands in for the five of them; they share runStream.
test('a failure the install stream reports is not announced as success', async ({ page }) => {
  const failure = 'Failed to download nvm install script: offline'
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
  })
  const installed = { installed: true, version: '1.0.0', path: '/root/.local/bin/tool', latest: '1.0.0', update_available: false, logged_in: true }
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname
    if (path.endsWith('/ai/tools/codex/install-stream')) {
      await route.fulfill({
        status: 200,
        headers: { 'Content-Type': 'text/event-stream' },
        body: `data: >>> tester has no Node.js: installing nvm and Node.js LTS first ...\n\ndata: ERROR: ${failure}\n\ndata: [DONE]\n\n`,
      })
      return
    }
    let data: unknown = {}
    if (path.endsWith('/auth/setup-status')) data = { setup_required: false }
    else if (path.endsWith('/cluster/status')) data = { enabled: false }
    else if (path.endsWith('/ai/sessions') || path.endsWith('/terminal/sessions')) data = []
    else if (path.endsWith('/ai/tools')) data = {
      tmux: { installed: true, supported: true, version: '3.4', min_version: '3.2' }, systemd_run: true,
      accounts: ['tester'], panel_account: 'tester', account: 'tester',
      // Only Codex is missing, so its Install is the one Install button.
      tools: { claude: installed, gemini: installed, codex: { installed: false, version: '', path: '', latest: '0.163.0', update_available: false, logged_in: false } },
    }
    else if (path.endsWith('/ai/dirs')) data = { recent: [], stacks: [], home: '/root' }
    else if (path.endsWith('/ai/profiles')) data = { profiles: [] }
    await route.fulfill({ json: { success: true, data } })
  })

  await page.goto('/terminal')
  await page.getByRole('button', { name: 'Tools & accounts' }).first().click()
  await page.getByRole('dialog', { name: 'Tools & accounts' }).getByRole('button', { name: 'Install' }).click()

  const output = page.getByRole('dialog', { name: 'Installing Codex…' })
  await expect(output.getByText('Operation failed — check the log below')).toBeVisible()
  // The toast says what failed, in the stream's words…
  await expect(page.getByText(failure, { exact: true })).toBeVisible()
  // …and nothing says it worked.
  await expect(page.getByText('Codex installed')).toHaveCount(0)
  // The dialog already shows the line; the failure is not echoed under it.
  const log = await output.locator('pre').innerText()
  expect(log.split(failure).length - 1).toBe(1)
})
