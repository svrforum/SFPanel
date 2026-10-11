import { test, expect, type Page } from '@playwright/test'

// Reported on the forum: on the Docker pages a long container name ran on over
// the image and status columns and the row buttons over the date; one long
// image name pushed the size, date and delete button out of the images card;
// and every stack opened at once. REST is a fixture.
test.use({ locale: 'en-US' })

const longImage = 'registry.example.com/a-very-long-organization-name/some-really-long-image-repository-name:2026.10.11-build-1234567890abcdef'

const container = (i: number, project: string, image: string) => ({
  Id: `c${i}`.padEnd(64, '0'),
  Names: [`/${project}-service-number-${i}-with-a-long-name`],
  Image: image,
  State: 'running',
  Status: 'Up 3 days (healthy)',
  Created: 1760000000 - i * 3600,
  Ports: [{ PrivatePort: 8080, PublicPort: 18000 + i, Type: 'tcp', IP: '0.0.0.0' }, { PrivatePort: 443, PublicPort: 8443 + i, Type: 'tcp', IP: '::' }],
  Labels: { 'com.docker.compose.project': project, 'com.docker.compose.service': `service-${i}-with-a-rather-long-service-name` },
  cpu_avg_1h: 1.2,
})

const containers = [
  container(1, 'monitoring', longImage),
  container(2, 'monitoring', 'grafana/grafana:11.2.0'),
  container(7, 'media', longImage),
  container(8, 'media', 'jellyfin/jellyfin:10.10.0'),
  { ...container(9, 'alerts', 'nginx:latest'), Status: 'Up 1 hour (unhealthy)' },
]

const images = [
  { Id: 'sha256:' + 'a'.repeat(64), RepoTags: [longImage], Size: 812345678, Created: 1760000000, in_use: true, used_by: ['monitoring-service-number-1-with-a-long-name'] },
  { Id: 'sha256:' + 'b'.repeat(64), RepoTags: ['grafana/grafana:11.2.0'], Size: 412345678, Created: 1750000000, in_use: true, used_by: ['monitoring-service-number-2'] },
]

async function open(page: Page, path: string, width: number) {
  await page.setViewportSize({ width, height: 900 })
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const p = new URL(route.request().url()).pathname.replace('/api/v1', '')
    let data: unknown = {}
    if (p === '/auth/setup-status') data = { setup_required: false }
    else if (p === '/cluster/status') data = { enabled: false }
    else if (p === '/docker/containers') data = containers
    else if (p === '/docker/containers/stats/batch') data = {}
    else if (p === '/docker/images') data = images
    else if (p.startsWith('/docker/compose')) data = []
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto(path)
}

// A container's name in the table; the full name is its title.
const nameCell = (page: Page, n: number, project = 'media') =>
  page.locator('table').getByTitle(`${project}-service-number-${n}-with-a-long-name`, { exact: true })

// The table row holding that container.
const row = (page: Page, n: number) => page.locator('table tbody tr')
  .filter({ has: page.getByTitle(`media-service-number-${n}-with-a-long-name`, { exact: true }) })

// Table cells whose content paints outside their column. A clipped cell
// (ellipsis) stays inside it. scrollWidth only sees text running off the
// right; the row buttons are right-aligned and spill off the left, over the
// date, so each element's box is checked as well.
function spilling(page: Page) {
  return page.evaluate(() => [...document.querySelectorAll('table tbody td')]
    .filter((td) => {
      if (getComputedStyle(td).overflow !== 'visible') return false
      const cell = td.getBoundingClientRect()
      return td.scrollWidth > td.clientWidth + 1 || [...td.querySelectorAll('*')].some((el) => {
        const box = el.getBoundingClientRect()
        return box.width > 0 && (box.left < cell.left - 1 || box.right > cell.right + 1)
      })
    })
    .map((td) => (td as HTMLElement).innerText.trim().slice(0, 40) || 'row buttons'))
}

test('stacks start collapsed, open on demand, and stay open after a reload', async ({ page }) => {
  await open(page, '/docker/containers', 1280)
  await expect(page.locator('table').getByText('media', { exact: true })).toBeVisible()
  await expect(nameCell(page, 7)).toHaveCount(0)
  await page.getByRole('button', { name: 'Expand all' }).click()
  await expect(nameCell(page, 7)).toBeVisible()
  await page.reload()
  await expect(nameCell(page, 7)).toBeVisible()
})

// Collapsing by default must not hide a container that needs attention.
test('a stack with an unhealthy container starts open', async ({ page }) => {
  await open(page, '/docker/containers', 1280)
  await expect(nameCell(page, 9, 'alerts')).toBeVisible()
  await expect(nameCell(page, 7)).toHaveCount(0)
})

test('a search shows what it found inside a collapsed stack', async ({ page }) => {
  await open(page, '/docker/containers', 1280)
  await page.getByPlaceholder('Search containers...').fill('jellyfin')
  await expect(nameCell(page, 8)).toBeVisible()
})

test('a stack can still be closed during a search', async ({ page }) => {
  await open(page, '/docker/containers', 1280)
  await page.getByPlaceholder('Search containers...').fill('service-number')
  await expect(nameCell(page, 7)).toBeVisible()
  await page.locator('table').getByText('media', { exact: true }).click()
  await expect(nameCell(page, 7)).toHaveCount(0)
  await expect(nameCell(page, 1, 'monitoring')).toBeVisible()
})

for (const width of [1440, 1100]) {
  test(`at ${width}px long names stay in their columns and every row button is on screen`, async ({ page }) => {
    await open(page, '/docker/containers', width)
    await page.getByRole('button', { name: 'Expand all' }).click()
    await expect(nameCell(page, 7)).toBeVisible()
    expect(await spilling(page)).toEqual([])
    const del = await row(page, 7).getByRole('button', { name: 'Delete' }).boundingBox()
    expect(del!.x + del!.width).toBeLessThanOrEqual(width)
  })
}

test('a long image name leaves the other columns and the delete button in view', async ({ page }) => {
  await open(page, '/docker/images', 1100)
  await expect(page.locator('table').getByTitle('grafana/grafana:11.2.0', { exact: true })).toBeVisible()
  expect(await spilling(page)).toEqual([])
  const del = await page.locator('table tbody tr').filter({ has: page.getByTitle(longImage, { exact: true }) }).getByRole('button', { name: 'Delete' }).boundingBox()
  const card = await page.locator('table').locator('..').boundingBox()
  expect(del!.x + del!.width).toBeLessThanOrEqual(card!.x + card!.width)
})

// A touch screen raises every icon button to a 44px target (index.css), so
// the row buttons need a wider column there.
test.describe('on a touch screen', () => {
  test.use({ hasTouch: true })

  test('the larger row buttons still fit their column', async ({ page }) => {
    await open(page, '/docker/containers', 1100)
    expect(await page.evaluate(() => matchMedia('(pointer: coarse)').matches)).toBe(true)
    await page.getByRole('button', { name: 'Expand all' }).click()
    await expect(nameCell(page, 7)).toBeVisible()
    expect(await spilling(page)).toEqual([])
  })
})
