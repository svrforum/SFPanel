import { test, expect, type Page } from '@playwright/test'

// The new-session dialog's directory suggestions used to be a <datalist>,
// which Android renders as a native sheet the page cannot size or place: it
// covered the dialog, the key bar and the keyboard, and listed a directory
// twice when it was both recent and a stack. These checks run on a phone
// viewport against a mocked API, so they need nothing but the SPA.
test.use({ viewport: { width: 412, height: 860 }, hasTouch: true })

const stacks = ['/opt/stacks/alpha', '/opt/stacks/gamma', ...Array.from({ length: 10 }, (_, i) => `/opt/stacks/app${i}`)]
const dirs = { recent: ['/opt/stacks/alpha', '/srv/beta'], stacks, home: '/root' }
const unique = new Set([...dirs.recent, ...dirs.stacks, dirs.home]).size

type Post = { path: string; body: unknown }

/**
 * The SPA against a mocked API on a single server, with tmux unless `tmux` is
 * false (the PTY fallback). Every POST is kept with its body, and every
 * history.replaceState leaves the address it produced in window.__replaced,
 * so a test can tell a replace from a push.
 */
async function mock(page: Page, { tmux = true } = {}): Promise<Post[]> {
  const posts: Post[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    const w = window as unknown as { __replaced: string[] }
    w.__replaced = []
    const replace = history.replaceState.bind(history)
    history.replaceState = (...args: Parameters<History['replaceState']>) => {
      replace(...args)
      w.__replaced.push(location.href)
    }
  })
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const path = new URL(req.url()).pathname
    if (req.method() === 'POST') {
      let body: unknown = null
      try { body = req.postDataJSON() } catch { /* not JSON */ }
      posts.push({ path, body })
    }
    let data: unknown = {}
    if (path.endsWith('/auth/setup-status')) data = { setup_required: false }
    else if (path.endsWith('/cluster/status')) data = { enabled: false }
    else if (path.endsWith('/ai/sessions') || path.endsWith('/terminal/sessions')) data = []
    else if (path.endsWith('/ai/tools')) data = { tmux: tmux ? { installed: true, supported: true, version: '3.4', min_version: '3.2' } : { installed: false, supported: false, version: '', min_version: '3.2' }, systemd_run: true, accounts: ['tester'], panel_account: 'tester', account: 'tester', tools: {} }
    else if (path.endsWith('/ai/dirs')) data = dirs
    else if (path.endsWith('/ai/profiles')) data = { profiles: [] }
    await route.fulfill({ json: { success: true, data } })
  })
  return posts
}

test('directory suggestions are drawn inside the dialog, once each, and pick without submitting', async ({ page }) => {
  const posts = await mock(page)

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
  expect(posts.filter(p => p.path.endsWith('/ai/sessions'))).toEqual([])

  // Escape closes the list and leaves the dialog open.
  await field.press('ArrowDown')
  await expect(list).toBeVisible()
  await field.press('Escape')
  await expect(list).toBeHidden()
  await expect(dialog).toBeVisible()
})

// Files and the Docker stacks link to /terminal?dir=<folder>. The folder is
// the dialog's opening value and is never posted on its own: the operator
// still presses Create.
test('?dir= opens a new session in that folder, and leaves the address by replace', async ({ page }) => {
  const posts = await mock(page)
  await page.goto('/terminal?dir=%2Fsrv%2Fmy%20app')
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('combobox', { name: 'Working directory' })).toHaveValue('/srv/my app')
  await expect(page).toHaveURL(/\/terminal$/)
  expect(await page.evaluate(() => (window as unknown as { __replaced: string[] }).__replaced.some((u) => !u.includes('dir=')))).toBe(true)
  await dialog.getByRole('button', { name: 'Create session' }).click()
  await expect.poll(() => posts.find((p) => p.path.endsWith('/ai/sessions'))?.body).toMatchObject({ cwd: '/srv/my app' })
})

test('a folder that is not absolute is not taken', async ({ page }) => {
  await mock(page)
  await page.goto('/terminal?dir=srv')
  await page.waitForTimeout(1000)
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page).toHaveURL(/\/terminal$/)
})

// The folder is read once and taken off the address, so a reload — or Back
// to this entry — is the plain terminal again.
test('a reload does not open it again', async ({ page }) => {
  await mock(page)
  await page.goto('/terminal?dir=%2Fsrv%2Fbeta')
  await expect(page.getByRole('dialog')).toBeVisible()
  await page.reload()
  await page.waitForTimeout(1000)
  await expect(page.getByRole('dialog')).toHaveCount(0)
})

// The folder belongs to the one opening it came with: once that dialog is
// closed, New session opens where the last session started, as before.
test('after that dialog is closed, New session opens as before', async ({ page }) => {
  await mock(page)
  await page.addInitScript(() => localStorage.setItem('sfpanel_ai_last:local', JSON.stringify({ cwd: '/opt/stacks/gamma' })))
  await page.goto('/terminal?dir=%2Fsrv%2Fbeta')
  const dialog = page.getByRole('dialog')
  const field = dialog.getByRole('combobox', { name: 'Working directory' })
  await expect(field).toHaveValue('/srv/beta')
  await dialog.getByRole('button', { name: 'Cancel' }).click()
  await expect(dialog).toHaveCount(0)
  await page.getByRole('button', { name: 'New session' }).first().click()
  await expect(field).toHaveValue('/opt/stacks/gamma')
})

// The temporary shell is not started in a folder, so without tmux there is no
// dialog to put it in: the folder is dropped and the page is as it would be.
test('without tmux the folder is dropped, not opened in the tmux dialog', async ({ page }) => {
  await mock(page, { tmux: false })
  await page.goto('/terminal?dir=%2Fsrv%2Fbeta')
  await expect(page.getByText(/Without tmux, sessions live in this browser only/)).toBeVisible()
  await page.waitForTimeout(1000)
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page).toHaveURL(/\/terminal$/)
})
