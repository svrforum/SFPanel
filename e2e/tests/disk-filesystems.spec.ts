import { test, expect } from '@playwright/test'

// #107. Docker mounts an overlay per container, and on a busy host they buried
// the disks in the filesystem list. REST is a fixture.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const overlay = (id: string) => ({ source: 'overlay', fstype: 'overlay', size: 1, used: 1, available: 0, use_percent: 50, mount_point: `/var/lib/docker/rootfs/overlayfs/${id}` })

test('Docker overlays stay hidden until asked for', async ({ page }) => {
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '')
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: false }
    else if (path === '/filesystems') data = [
      { source: '/dev/vda2', fstype: 'ext4', size: 100, used: 40, available: 60, use_percent: 40, mount_point: '/' },
      overlay('a1'), overlay('b2'), overlay('c3'),
    ]
    else if (path === '/disks/overview' || path === '/disks/iostat' || path === '/docker/volumes') data = []
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/disk/filesystems')
  await expect(page.getByRole('cell', { name: '/dev/vda2' })).toBeVisible()
  await expect(page.getByText(/rootfs\/overlayfs/)).toHaveCount(0)
  await page.getByRole('button', { name: 'Show 3 Docker overlays' }).click()
  await expect(page.getByText(/rootfs\/overlayfs/)).toHaveCount(3)
})
