import { test, expect, type Page } from '@playwright/test'

// What the file manager does with an upload the server refuses. REST is a
// fixture: each test answers POST /files/upload itself, with the multipart body
// it was sent, and nothing reads or changes a real filesystem.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const owner = { uid: 1000, gid: 1000, user: 'tester', group: 'tester' }
const entry = (dir: string, name: string, extra: Record<string, unknown> = {}) => ({
  name, path: `${dir}/${name}`, isDir: false, size: 120, modTime: '2026-01-01T00:00:00Z', mode: '-rw-r--r--', kind: 'text', owner, ...extra,
})
const LISTINGS: Record<string, unknown[]> = {
  '/srv': [entry('/srv', 'data', { isDir: true, size: 4096, mode: 'drwxr-xr-x' }), entry('/srv', 'notes.txt')],
}

interface UploadAnswer { status: number; json: unknown }

async function openFiles(page: Page, opts: { upload: (body: string) => UploadAnswer }) {
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const url = new URL(req.url())
    const p = url.pathname.replace('/api/v1', '')
    if (p === '/files/upload' && req.method() === 'POST') {
      await route.fulfill(opts.upload(req.postData() ?? ''))
      return
    }
    let data: unknown = {}
    if (p === '/auth/setup-status') data = { setup_required: false }
    else if (p === '/cluster/status') data = { enabled: false }
    else if (p === '/settings') data = { max_upload_size: '1' }
    else if (p === '/files/trash') data = { entries: [], retentionDays: 7 }
    else if (p === '/files') data = LISTINGS[url.searchParams.get('path') ?? '/'] ?? []
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/files?path=%2Fsrv')
  await expect(page.locator('table [data-row-index="0"] [data-row-open]')).toHaveText('data')
}

test('a file that exists already is replaced only after asking', async ({ page }) => {
  const bodies: string[] = []
  await openFiles(page, { upload: (body) => { bodies.push(body); return bodies.length === 1
    ? { status: 409, json: { success: false, error: { code: 'DESTINATION_EXISTS', message: "'notes.txt' already exists" } } }
    : { status: 200, json: { success: true, data: { message: 'file uploaded' } } } } })
  await page.locator('input[type="file"]').setInputFiles({ name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('hello') })
  const ask = page.getByRole('alertdialog').or(page.getByRole('dialog')).filter({ hasText: 'Replace existing file?' })
  await expect(ask).toBeVisible()                                   // the 409 reached the page as a status
  await ask.getByRole('button', { name: 'Replace' }).click()
  await expect.poll(() => bodies.length).toBe(2)
  expect(bodies[1]).toMatch(/name="overwrite"\r\n\r\ntrue/)
})
