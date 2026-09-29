import { test, expect } from '@playwright/test'

// The new-session dialog's directory suggestions used to be a <datalist>,
// which Android renders as a native sheet the page cannot size or place: it
// covered the dialog, the key bar and the keyboard, and listed a directory
// twice when it was both recent and a stack. These checks run on a phone
// viewport against a mocked API, so they need nothing but the SPA.
test.use({ viewport: { width: 412, height: 860 }, hasTouch: true })

const stacks = ['/opt/stacks/alpha', '/opt/stacks/gamma', ...Array.from({ length: 10 }, (_, i) => `/opt/stacks/app${i}`)]
const dirs = { recent: ['/opt/stacks/alpha', '/srv/beta'], stacks, home: '/root' }
const unique = new Set([...dirs.recent, ...dirs.stacks, dirs.home]).size

test('directory suggestions are drawn inside the dialog, once each, and pick without submitting', async ({ page }) => {
  const posts: string[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const path = new URL(req.url()).pathname
    if (req.method() === 'POST') posts.push(path)
    let data: unknown = {}
    if (path.endsWith('/auth/setup-status')) data = { setup_required: false }
    else if (path.endsWith('/cluster/status')) data = { enabled: false }
    else if (path.endsWith('/ai/sessions') || path.endsWith('/terminal/sessions')) data = []
    else if (path.endsWith('/ai/tools')) data = { tmux: { installed: true, supported: true, version: '3.4', min_version: '3.2' }, systemd_run: true, accounts: ['tester'], panel_account: 'tester', account: 'tester', tools: {} }
    else if (path.endsWith('/ai/dirs')) data = dirs
    else if (path.endsWith('/ai/profiles')) data = { profiles: [] }
    await route.fulfill({ json: { success: true, data } })
  })

  await page.goto('/terminal')
  await page.getByRole('button', { name: 'New session' }).first().click()
  const dialog = page.getByRole('dialog')
  const field = dialog.getByRole('combobox', { name: 'Working directory' })
  await expect(field).toHaveValue('/opt/stacks/alpha')

  // No native suggestion sheet anywhere.
  await expect(page.locator('datalist')).toHaveCount(0)
  expect(await field.getAttribute('list')).toBeNull()

  // A tap shows every suggestion — the prefilled value does not narrow it —
  // inside the dialog, each directory once.
  await field.tap()
  const list = dialog.getByRole('listbox', { name: 'Suggested directories' })
  await expect(list).toBeVisible()
  await expect(list.getByRole('option')).toHaveCount(unique)
  await expect(list.getByRole('option', { name: /alpha/ })).toHaveCount(1)
  await expect(list.getByRole('option', { name: /alpha/ })).toContainText('Recent · Stacks')
  const d = (await dialog.boundingBox())!, l = (await list.boundingBox())!
  expect(l.x).toBeGreaterThanOrEqual(d.x)
  expect(l.x + l.width).toBeLessThanOrEqual(d.x + d.width)
  expect(l.height).toBeLessThanOrEqual(13 * 16 + 4)

  // Typing narrows it; a tap on a row fills the field and closes the list.
  await field.fill('gam')
  await expect(list.getByRole('option')).toHaveCount(1)
  await list.getByRole('option', { name: /gamma/ }).tap()
  await expect(field).toHaveValue('/opt/stacks/gamma')
  await expect(list).toBeHidden()

  // Arrow + Enter picks the highlighted row instead of submitting the form.
  // Clearing the field is typing, so the full list is already open here.
  await field.fill('')
  await expect(list.getByRole('option')).toHaveCount(unique)
  await field.press('ArrowDown')
  await field.press('Enter')
  await expect(field).toHaveValue('/opt/stacks/alpha')
  await expect(dialog).toBeVisible()
  expect(posts.filter(p => p.endsWith('/ai/sessions'))).toEqual([])

  // Escape closes the list and leaves the dialog open.
  await field.press('ArrowDown')
  await expect(list).toBeVisible()
  await field.press('Escape')
  await expect(list).toBeHidden()
  await expect(dialog).toBeVisible()
})
