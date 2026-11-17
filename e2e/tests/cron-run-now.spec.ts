import { test, expect, type Locator, type Page } from '@playwright/test'

// The Cron Jobs page lays each job out twice: cards below 768px, a table
// above. "Run now" was only ever on the cards, so a desktop browser could
// disable, edit and delete a job but not run it (#133). Both layouts here run
// the same job and must send the same request and open the same output
// dialog. A job runs as root, so it is asked about first, with its command.
// The rows are what ListJobs returns for the crontab below (the id is the
// line number, so the job run is 3) and the answer is what RunJob returns for
// it. Mocked API.
//
//   # m h  dom mon dow   command
//   MAILTO=""
//   0 4 * * * cd /opt/stacks/example-app && docker compose pull --quiet && docker compose up -d --remove-orphans >> /var/log/example-app-update.log 2>&1 && docker image prune -f
//   */15 * * * * echo heartbeat
//   # 0 4 * * 0 /usr/local/bin/rotate-logs.sh
test.use({ locale: 'en-US' })

const update = 'cd /opt/stacks/example-app && docker compose pull --quiet && docker compose up -d --remove-orphans >> /var/log/example-app-update.log 2>&1 && docker image prune -f'
const jobs = [
  { id: 0, schedule: '', command: '# m h  dom mon dow   command', enabled: false, raw: '# m h  dom mon dow   command', type: 'comment' },
  { id: 1, schedule: '', command: 'MAILTO=""', enabled: true, raw: 'MAILTO=""', type: 'env' },
  { id: 2, schedule: '0 4 * * *', command: update, enabled: true, raw: `0 4 * * * ${update}`, type: 'job' },
  { id: 3, schedule: '*/15 * * * *', command: 'echo heartbeat', enabled: true, raw: '*/15 * * * * echo heartbeat', type: 'job' },
  { id: 4, schedule: '0 4 * * 0', command: '/usr/local/bin/rotate-logs.sh', enabled: false, raw: '# 0 4 * * 0 /usr/local/bin/rotate-logs.sh', type: 'job' },
]

async function open(page: Page, width: number) {
  const posts: string[] = []
  const puts: { path: string; body: unknown }[] = []
  await page.setViewportSize({ width, height: 900 })
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const req = route.request()
    const path = new URL(req.url()).pathname.replace('/api/v1', '')
    let data: unknown = {}
    if (req.method() === 'POST') {
      posts.push(path)
      data = { output: 'heartbeat', success: true }
    } else if (req.method() === 'PUT') {
      puts.push({ path, body: req.postDataJSON() })
    } else if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: false }
    else if (path === '/cron') data = jobs
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/cron')
  return { posts, puts }
}

const layouts: { name: string; width: number; action: (page: Page, name: string) => Locator }[] = [
  { name: 'the desktop table', width: 1440, action: (page, name) => page.getByRole('row', { name: /echo heartbeat/ }).getByRole('button', { name }) },
  // A card has no role to scope by: each button names the job's command.
  { name: 'a phone card', width: 700, action: (page, name) => page.getByRole('button', { name: `${name}: echo heartbeat`, exact: true }) },
]

for (const { name, width, action } of layouts) {
  test(`${name} runs a job now and shows its output`, async ({ page }) => {
    const { posts } = await open(page, width)
    // The job's other actions are on screen, so a missing Run now is the
    // button itself and not a page that never rendered.
    await expect(action(page, 'Edit')).toBeVisible()
    await expect(action(page, 'Run now')).toBeVisible()
    await action(page, 'Run now').click()

    const ask = page.getByRole('dialog')
    await expect(ask.getByRole('heading', { name: 'Run this job now?' })).toBeVisible()
    await expect(ask).toContainText('as root')
    await expect(ask.getByText('echo heartbeat', { exact: true })).toBeVisible()
    expect(posts).toEqual([])
    await ask.getByRole('button', { name: 'Run now' }).click()

    await expect.poll(() => posts).toEqual(['/cron/3/run'])
    const dialog = page.getByRole('dialog')
    await expect(dialog.getByRole('heading', { name: 'Run now' })).toBeVisible()
    await expect(dialog.getByText('echo heartbeat', { exact: true })).toBeVisible()
    await expect(dialog.getByText('heartbeat', { exact: true })).toBeVisible()
  })
}

// The card wraps a long command and keeps its buttons beside it. The table
// cell was nowrap, which the command's break-all cannot break, so one long
// line widened the table past its box and the row's actions scrolled out of
// sight to the right. It wraps from 1024px; narrower, the table scrolls.
test('a long command in the desktop table wraps and leaves its actions on screen', async ({ page }) => {
  await open(page, 1440)
  const row = page.getByRole('row', { name: /example-app/ })
  for (const name of ['Run now', 'Edit', 'Delete']) {
    await expect(row.getByRole('button', { name })).toBeInViewport({ ratio: 1 })
  }
})

// The switch was a play icon beside Run now; pressing it to run a job turned
// the job off. It is a box that says whether the job runs on its schedule.
for (const { name, width } of layouts) {
  test(`${name} shows whether a job is enabled and turns it off`, async ({ page }) => {
    const { posts, puts } = await open(page, width)
    const on = page.getByRole('checkbox', { name: 'Enabled: echo heartbeat' })
    const off = page.getByRole('checkbox', { name: 'Enabled: /usr/local/bin/rotate-logs.sh' })
    await expect(on).toBeChecked()
    await expect(off).not.toBeChecked()
    await on.click()
    await expect.poll(() => puts).toEqual([{ path: '/cron/3', body: { schedule: '*/15 * * * *', command: 'echo heartbeat', enabled: false } }])
    expect(posts).toEqual([])
  })
}
