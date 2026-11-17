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

async function openFiles(page: Page, path = '/srv') {
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
  // Backspace from the listing goes back up.
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

test('a link to a directory is a folder: no editor, and a click goes into it', async ({ page }) => {
  const f = await openFiles(page)
  const row = page.locator('[data-row-index="1"]')
  await expect(f.nameOf(1)).toContainText('web')
  await expect(row.getByRole('button', { name: 'Edit', exact: true })).toHaveCount(0)
  await f.nameOf(1).click()
  await expect.poll(() => new URL(page.url()).searchParams.get('path')).toBe('/srv/web')
  await expect(f.nameOf(0)).toHaveText('compose.yml')
})
