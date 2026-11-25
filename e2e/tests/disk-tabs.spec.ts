import { test, expect, type Page } from '@playwright/test'

// The disk tab strip keeps the everyday pages as tabs and folds partitions,
// LVM and RAID into one Setup menu, whose button names the page it has open.
// The addresses stay the same. REST is a fixture.
test.use({ locale: 'en-US' })
async function open(page: Page, path: string) {
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
    else if (p.startsWith('/disks/smartmontools')) data = { installed: true }
    else if (p === '/disks/overview' || p === '/disks/iostat' || p === '/docker/volumes') data = []
    else if (p.startsWith('/lvm/') || p === '/raid') data = []
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto(path)
}
const strip = (page: Page) => page.getByRole('navigation', { name: 'Disks' })

test('the disk tabs keep the everyday five and fold the setup work into one menu', async ({ page }) => {
  await open(page, '/disk/overview')
  await expect.poll(() => strip(page).getByRole('link').evaluateAll((a) => a.map((l) => l.getAttribute('href'))))
    .toEqual(['/disk/overview', '/disk/usage', '/disk/filesystems', '/disk/swap', '/disk/network-shares'])
  const setup = strip(page).getByRole('button', { name: 'Setup', exact: true })
  // A closed menu is not on the page.
  await expect(page.getByRole('menuitem', { name: 'RAID' })).toBeHidden()
  await setup.click()
  await expect(page.getByRole('menuitem')).toHaveText(['Partitions', 'LVM', 'RAID'])
  await page.getByRole('menuitem', { name: 'RAID' }).click()
  await expect(page).toHaveURL(/\/disk\/raid$/)
  await expect(strip(page).getByRole('button', { name: 'Setup: RAID' })).toHaveAttribute('aria-current', 'page')
  await expect(strip(page).getByRole('link', { name: 'Overview' })).not.toHaveAttribute('aria-current', 'page')
})

test('a setup page opened by its address names itself on the menu', async ({ page }) => {
  await open(page, '/disk/lvm')
  await expect(strip(page).getByRole('button', { name: 'Setup: LVM' })).toHaveAttribute('aria-current', 'page')
  // Inside the open menu the page that is open is told apart by sight too,
  // not only to a screen reader: heavier, with a check.
  await strip(page).getByRole('button', { name: 'Setup: LVM' }).click()
  const lvm = page.getByRole('menuitem', { name: 'LVM' })
  const raid = page.getByRole('menuitem', { name: 'RAID' })
  await expect(lvm).toHaveAttribute('aria-current', 'page')
  await expect(lvm).toHaveCSS('font-weight', '600')
  await expect(lvm.locator('svg')).toHaveCount(2)
  await expect(raid).not.toHaveAttribute('aria-current', 'page')
  await expect(raid).not.toHaveCSS('font-weight', '600')
  await expect(raid.locator('svg')).toHaveCount(1)
})

test('the keyboard opens the menu, picks a page and leaves focus on the menu button', async ({ page }) => {
  await open(page, '/disk/overview')
  await strip(page).getByRole('button', { name: 'Setup', exact: true }).focus()
  await page.keyboard.press('Enter'); await page.keyboard.press('ArrowDown'); await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/\/disk\/(partitions|lvm)$/)
  await expect(strip(page).getByRole('button', { name: /^Setup: / })).toBeFocused()
})

test('on a phone the chosen setup page is in view, and the open menu fits the screen', async ({ page }) => {
  await page.setViewportSize({ width: 360, height: 800 }); await open(page, '/disk/raid')
  const setup = strip(page).getByRole('button', { name: 'Setup: RAID' })
  await expect(setup).toBeInViewport(); await setup.click()
  for (const name of ['Partitions', 'LVM', 'RAID']) await expect(page.getByRole('menuitem', { name })).toBeInViewport()
})
