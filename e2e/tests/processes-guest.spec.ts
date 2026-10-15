import { test, expect, type Page } from '@playwright/test'

// On a Proxmox VE host a VM is a QEMU process: TERM to it is a power cut. The
// list names the VM, and the kill dialog offers only Resume for it (the server
// refuses the rest with PROCESS_IS_GUEST). REST is a fixture.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const proc = (pid: number, name: string, extra: Record<string, unknown> = {}) => ({
  pid, ppid: 1, name, cpu: 1, memory: 1, rss: 1 << 20, nice: 0, status: 'sleeping', user: 'root', command: name, ...extra,
})
const processes = [
  proc(4100, 'kvm', { command: '/usr/bin/kvm -id 100 -name vm100', vmid: 100 }),
  proc(4200, 'nginx'),
]

async function open(page: Page) {
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
    else if (path === '/system/processes/list') data = { processes, total: processes.length }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/processes')
}

test('a VM process is named as the VM and can only be resumed', async ({ page }) => {
  await open(page)
  const vmRow = page.getByRole('row', { name: /kvm/ })
  await expect(vmRow.getByText('VM 100', { exact: true })).toBeVisible()
  await expect(page.getByRole('row', { name: /nginx/ }).getByText(/^VM /)).toHaveCount(0)

  await vmRow.getByRole('button', { name: 'Kill Process' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByText(/This process is VM 100: killing it is pulling its power/)).toBeVisible()
  for (const name of ['SIGTERM', 'SIGKILL', 'Pause']) {
    await expect(dialog.getByRole('button', { name })).toBeDisabled()
  }
  await expect(dialog.getByRole('button', { name: 'Resume' })).toBeEnabled()
})
