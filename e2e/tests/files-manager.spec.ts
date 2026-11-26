import { test, expect, type Page } from '@playwright/test'

// The file manager's keyboard, deletes, failures and directory links. REST is
// a fixture: nothing here reads or changes a real filesystem.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const owner = { uid: 1000, gid: 1000, user: 'tester', group: 'tester' }
const entry = (dir: string, name: string, extra: Record<string, unknown> = {}) => ({
  name, path: `${dir}/${name}`, isDir: false, size: 120, modTime: '2026-01-01T00:00:00Z', mode: '-rw-r--r--', kind: 'text', owner, ...extra,
})
const LISTINGS: Record<string, unknown[]> = {
  '/srv': [
    entry('/srv', 'data', { isDir: true, size: 4096, mode: 'drwxr-xr-x' }),
    // A link to a directory, as /bin is on a merged-/usr system.
    entry('/srv', 'web', { size: 8, mode: 'lrwxrwxrwx', linkTarget: '/opt/web', targetIsDir: true }),
    entry('/srv', 'notes.txt'),
    entry('/srv', 'mounted.txt'),
  ],
  '/srv/data': [entry('/srv/data', 'inside.txt')],
  '/srv/web': [entry('/srv/web', 'compose.yml')],
}

/** `slow` holds the listings that answer late, with the delay in ms. */
async function openFiles(page: Page, path = '/srv', slow: Record<string, number> = {}) {
  const deleted: string[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const url = new URL(req.url())
    const p = url.pathname.replace('/api/v1', '')
    let data: unknown = {}
    if (p === '/auth/setup-status') data = { setup_required: false }
    else if (p === '/cluster/status') data = { enabled: false }
    else if (p === '/files/trash') data = { entries: [], retentionDays: 7 }
    else if (p === '/files' && req.method() === 'DELETE') {
      const target = url.searchParams.get('path') ?? ''
      deleted.push(target)
      // The trash is on another filesystem than this one.
      data = { path: target, trashed: !target.endsWith('mounted.txt') }
    } else if (p === '/files') {
      const delay = slow[url.searchParams.get('path') ?? '/']
      if (delay) await new Promise((resolve) => setTimeout(resolve, delay))
      const listing = LISTINGS[url.searchParams.get('path') ?? '/']
      if (!listing) {
        await route.fulfill({ status: 403, json: { success: false, error: { code: 'PERMISSION_DENIED', message: 'permission denied' } } })
        return
      }
      data = listing
    }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto(`/files?path=${encodeURIComponent(path)}`)
  const table = page.locator('table')
  return { table, deleted, nameOf: (i: number) => table.locator(`[data-row-index="${i}"] [data-row-open]`) }
}

test('the arrows move the focus between rows, and Enter elsewhere presses what has it', async ({ page }) => {
  const f = await openFiles(page)
  await expect(f.nameOf(0)).toHaveText('data')
  await f.nameOf(0).focus()
  await page.keyboard.press('ArrowDown')
  await expect(f.nameOf(1)).toBeFocused()
  await page.keyboard.press('End')
  await expect(f.nameOf(3)).toBeFocused()
  // Enter on New Folder used to open the row the arrows had reached.
  await page.getByRole('button', { name: 'New Folder' }).focus()
  await page.keyboard.press('Enter')
  await expect(page.getByRole('dialog', { name: 'Create New Folder' })).toBeVisible()
  expect(new URL(page.url()).searchParams.get('path')).toBe('/srv')
  await page.keyboard.press('Escape')
  // Enter on a row's name opens it, as the button does.
  await f.nameOf(0).focus()
  await page.keyboard.press('Enter')
  await expect.poll(() => new URL(page.url()).searchParams.get('path')).toBe('/srv/data')
  // Backspace from the listing goes back up. From the new listing: a name
  // focused in the one being replaced goes with it, and the key with it.
  await expect(f.nameOf(0)).toHaveText('inside.txt')
  await f.nameOf(0).focus()
  await page.keyboard.press('Backspace')
  await expect.poll(() => new URL(page.url()).searchParams.get('path')).toBe('/srv')
})

test('keys pressed in a dialog stay in the dialog', async ({ page }) => {
  const f = await openFiles(page)
  await f.nameOf(2).focus()
  await page.keyboard.press('ArrowDown')
  await page.locator('[data-row-index="2"]').getByRole('button', { name: 'Actions' }).click()
  await page.getByRole('menuitem', { name: 'Permissions' }).click()
  const dialog = page.getByRole('dialog', { name: 'Permissions' })
  const box = dialog.getByRole('checkbox', { name: 'Everyone Write' })
  await expect(box).not.toBeChecked()
  await box.focus()
  await page.keyboard.press('Space')
  // The box ticked, and no row behind the dialog was selected.
  await expect(box).toBeChecked()
  await expect(page.getByText(/\d+ selected/)).toHaveCount(0)
  await page.keyboard.press('Backspace')
  expect(new URL(page.url()).searchParams.get('path')).toBe('/srv')
  await expect(dialog).toBeVisible()
  await expect(dialog.getByRole('textbox', { name: 'Owner' })).toHaveValue('tester')
})

test('a delete says it went to the trash, or that it could not', async ({ page }) => {
  const f = await openFiles(page)
  await page.locator('[data-row-index="3"]').getByRole('button', { name: 'Delete', exact: true }).click()
  const confirm = page.getByRole('dialog', { name: 'Delete "notes.txt"?' })
  await expect(confirm).toContainText('It moves to the trash')
  await confirm.getByRole('button', { name: 'Delete', exact: true }).click()
  await expect(page.getByText('"notes.txt" moved to the trash')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Open trash' })).toBeVisible()
  await page.locator('[data-row-index="2"]').getByRole('button', { name: 'Delete', exact: true }).click()
  await page.getByRole('dialog', { name: 'Delete "mounted.txt"?' }).getByRole('button', { name: 'Delete', exact: true }).click()
  await expect(page.getByText('"mounted.txt" was on another disk than the trash and has been deleted permanently')).toBeVisible()
  expect(f.deleted).toEqual(['/srv/notes.txt', '/srv/mounted.txt'])
})

test('a folder that cannot be listed says why, with a retry, not that it is empty', async ({ page }) => {
  await openFiles(page, '/srv/locked')
  await expect(page.getByText('Could not load this folder')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Retry' })).toBeVisible()
  await expect(page.getByText('This folder is empty')).toHaveCount(0)
})

// A listing that answers after the folder changed belongs to the folder left
// behind: leaving a folder still loading showed its entries under /srv.
test('a listing that answers after the folder changed is dropped', async ({ page }) => {
  const f = await openFiles(page, '/srv', { '/srv/data': 1500 })
  const crumbs = page.getByRole('navigation', { name: 'Current path' })
  const here = crumbs.locator('[aria-current="page"]')
  const late = page.waitForResponse((r) => new URL(r.url()).searchParams.get('path') === '/srv/data')
  await f.nameOf(0).click()
  await expect(here).toHaveText('data')
  await crumbs.getByRole('button', { name: 'srv', exact: true }).click()
  await expect(here).toHaveText('srv')
  await expect(f.nameOf(0)).toHaveText('data')
  await late
  await page.waitForTimeout(500)
  await expect(f.nameOf(0)).toHaveText('data')
  await expect(page.getByText('inside.txt')).toHaveCount(0)
})

test('a link to a directory is a folder: no editor, and a click goes into it', async ({ page }) => {
  const f = await openFiles(page)
  const row = page.locator('[data-row-index="1"]')
  await expect(f.nameOf(1)).toContainText('web')
  await expect(row.getByRole('button', { name: 'Edit', exact: true })).toHaveCount(0)
  await f.nameOf(1).click()
  await expect.poll(() => new URL(page.url()).searchParams.get('path')).toBe('/srv/web')
  await expect(f.nameOf(0)).toHaveText('compose.yml')
})

test('the folder on screen and each folder row offer a terminal opened there', async ({ page }) => {
  await page.addInitScript(() => { const push = history.pushState.bind(history); (window as any).__pushed = []; history.pushState = (s, u, url) => { (window as any).__pushed.push(String(url)); push(s, u, url) } })
  const { table } = await openFiles(page)
  await expect(page.getByRole('link', { name: 'Open a terminal here' })).toHaveAttribute('href', '/terminal?dir=%2Fsrv')
  await table.getByRole('row', { name: /notes\.txt/ }).getByRole('button', { name: 'Actions' }).click()
  await expect(page.getByRole('menuitem', { name: 'Open a terminal here' })).toHaveCount(0)
  await page.keyboard.press('Escape')
  await table.getByRole('row', { name: /\bdata\b/ }).getByRole('button', { name: 'Actions' }).click()
  await page.getByRole('menuitem', { name: 'Open a terminal here' }).click()
  await expect.poll(() => page.evaluate(() => (window as any).__pushed)).toContain('/terminal?dir=%2Fsrv%2Fdata')
})

// Below the small breakpoint the link shows only its icon; a title alone is a
// tooltip, which not every screen reader reads as the name.
test('on a narrow screen the toolbar terminal link keeps its name with its text hidden', async ({ page }) => {
  await page.setViewportSize({ width: 600, height: 800 })
  await openFiles(page)
  const link = page.locator('a[href="/terminal?dir=%2Fsrv"]')
  await expect(link).toBeVisible()
  await expect(link.getByText('Open a terminal here')).toBeHidden()
  await expect(link).toHaveAttribute('aria-label', 'Open a terminal here')
})

// The toolbar is wider than a laptop's window once a sidebar is beside it. It
// is meant to wrap; it did not, and its buttons were drawn over the search box.
test('at a laptop width the toolbar wraps instead of covering the search box', async ({ page }) => {
  await openFiles(page)
  const search = page.getByRole('textbox', { name: 'Search this folder' })
  await expect(search).toBeVisible()
  // The controls whose boxes fall on the search box's, other than its own.
  const covered = await search.evaluate((input) => {
    const r = input.getBoundingClientRect()
    return [...document.querySelectorAll('button, a')]
      .filter((el) => !input.parentElement!.contains(el))
      .filter((el) => {
        const b = el.getBoundingClientRect()
        return b.width > 0 && b.left < r.right && b.right > r.left && b.top < r.bottom && b.bottom > r.top
      })
      .map((el) => el.getAttribute('aria-label') || el.textContent?.trim() || el.tagName)
  })
  expect(covered).toEqual([])
})
