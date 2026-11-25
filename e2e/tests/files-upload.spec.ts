import { test, expect, type Page } from '@playwright/test'

// What the file manager does with an upload the server refuses. REST is a
// fixture: each test answers POST /files/upload itself, with the multipart body
// it was sent, and nothing reads or changes a real filesystem. GET /settings
// gives the upload limit (`limit`, MB; 1 unless a test says otherwise).
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const owner = { uid: 1000, gid: 1000, user: 'tester', group: 'tester' }
const entry = (dir: string, name: string, extra: Record<string, unknown> = {}) => ({
  name, path: `${dir}/${name}`, isDir: false, size: 120, modTime: '2026-01-01T00:00:00Z', mode: '-rw-r--r--', kind: 'text', owner, ...extra,
})
const LISTINGS: Record<string, unknown[]> = {
  '/srv': [entry('/srv', 'data', { isDir: true, size: 4096, mode: 'drwxr-xr-x' }), entry('/srv', 'notes.txt')],
}

interface UploadAnswer { status: number; json: unknown }

async function openFiles(page: Page, opts: { upload: (body: string) => UploadAnswer | Promise<UploadAnswer>; limit?: string; lang?: 'en' | 'ko' }) {
  await page.addInitScript((lang) => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('sfpanel_language', lang)
    localStorage.setItem('i18nextLng', lang)
  }, opts.lang ?? 'en')
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const url = new URL(req.url())
    const p = url.pathname.replace('/api/v1', '')
    if (p === '/files/upload' && req.method() === 'POST') {
      await route.fulfill(await opts.upload(req.postData() ?? ''))
      return
    }
    let data: unknown = {}
    if (p === '/auth/setup-status') data = { setup_required: false }
    else if (p === '/cluster/status') data = { enabled: false }
    else if (p === '/settings') data = { max_upload_size: opts.limit ?? '1' }
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

// The server's refusal of a file over its limit, naming the limit that refused it.
const tooLarge = (limit: string): UploadAnswer => ({
  status: 413, json: { success: false, error: { code: 'FILE_TOO_LARGE', message: `File exceeds the upload limit of ${limit} MB`, details: { limit_mb: limit } } },
})
const big = { name: 'big.iso', mimeType: 'application/octet-stream', buffer: Buffer.alloc(16) }

test('a file over the limit says so and offers to change the limit', async ({ page }) => {
  await openFiles(page, { upload: () => tooLarge('1') })
  await page.locator('input[type="file"]').setInputFiles(big)
  // In the panel's words, not the server's English sentence.
  await expect(page.getByText("big.iso: larger than this server's upload limit of 1 MB", { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Change limit' }).click()
  const dialog = page.getByRole('dialog', { name: 'Upload limit' })
  await expect(dialog).toBeVisible()
  // Closed, it hands the focus to Upload options: the toast's button is gone.
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
  await expect(page.getByRole('button', { name: 'Upload options' })).toBeFocused()
})

test('in Korean the refusal reads in Korean', async ({ page }) => {
  await openFiles(page, { lang: 'ko', upload: () => tooLarge('1') })
  await page.locator('input[type="file"]').setInputFiles(big)
  await expect(page.getByText('big.iso: 이 서버의 업로드 한도 1 MB보다 큽니다', { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '한도 바꾸기' })).toBeVisible()
})

// 1024 MB is the most a server takes: a dialog that can only lower the limit
// would be a dead end.
test('at the most a server takes, the refusal says so instead of offering a change', async ({ page }) => {
  await openFiles(page, { limit: '1024', upload: () => tooLarge('1024') })
  await page.locator('input[type="file"]').setInputFiles(big)
  await expect(page.getByText(/^big\.iso: over 1024 MB, the largest upload limit a server takes\./)).toBeVisible()
  await expect(page.getByRole('button', { name: 'Change limit' })).toHaveCount(0)
})

// The progress dialog is modal for the whole batch: a toast's button under it
// cannot be pressed, and a 4-second toast is gone before a long batch ends.
test('a refusal in a batch offers the change once the batch is done', async ({ page }) => {
  await openFiles(page, { upload: async (body) => {
    if (body.includes('filename="big.iso"')) return tooLarge('1')
    await new Promise((resolve) => setTimeout(resolve, 4500))
    return { status: 200, json: { success: true, data: { message: 'file uploaded' } } }
  } })
  await page.locator('input[type="file"]').setInputFiles([big, { name: 'slow.txt', mimeType: 'text/plain', buffer: Buffer.from('hello') }])
  await expect(page.getByRole('dialog', { name: 'Uploading file' })).toBeVisible()
  await expect(page.getByRole('dialog', { name: 'Uploading file' })).toBeHidden({ timeout: 10_000 })
  await expect(page.getByText("big.iso: larger than this server's upload limit of 1 MB", { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Change limit' }).click()
  await expect(page.getByRole('dialog', { name: 'Upload limit' })).toBeVisible()
})

// The limit is read before a batch; a file over it is not sent at all. The
// server stays the judge of a file just under it (the body adds the form's own bytes).
test('a file over a limit the page knows is refused without sending it', async ({ page }) => {
  const bodies: string[] = []
  await openFiles(page, { upload: (body) => { bodies.push(body); return tooLarge('1') } })
  await page.locator('input[type="file"]').setInputFiles({ ...big, buffer: Buffer.alloc(1024 * 1024 + 1) })
  await expect(page.getByText("big.iso: larger than this server's upload limit of 1 MB", { exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Change limit' })).toBeVisible()
  expect(bodies).toEqual([])
})
