import { test, expect, type Page } from '@playwright/test'

// The Packages page on a Proxmox VE host. Docker is not offered there; an
// upgrade runs in a transient unit on the node, which the page follows again
// from a banner; a repository that refuses apt — the enterprise one, without
// a subscription — is a warning beside the result, not a failure. REST and
// the streams are fixtures.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const TX = {
  id: '20261012-101500-abcd',
  kind: 'upgrade',
  packages: [],
  command: 'apt-get -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold -y dist-upgrade',
  started_at: '2026-10-12T10:15:00Z',
}
const ENTERPRISE = 'https://enterprise.proxmox.com/debian/pve'
const REFUSED = {
  code: 'APT_REPO_UNAUTHORIZED',
  message: `repository ${ENTERPRISE} refused access (401)`,
  details: { uri: ENTERPRISE },
}

interface Fixture {
  lang?: 'en' | 'ko'
  docker?: Record<string, unknown>
  running?: boolean
  // Still running after its stream was read.
  keepRunning?: boolean
  stream?: string
  updates?: Record<string, unknown>
}

async function open(page: Page, fx: Fixture = {}) {
  const lang = fx.lang ?? 'en'
  await page.addInitScript((l) => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', l)
    localStorage.setItem('sfpanel_language', l)
  }, lang)
  // The transaction runs until its stream has been read to the end.
  let streamed = false
  await page.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '')
    if (path === '/packages/transaction/stream') {
      streamed = !fx.keepRunning
      await route.fulfill({ status: 200, contentType: 'text/event-stream', body: fx.stream ?? '' })
      return
    }
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: false }
    else if (path === '/packages/docker-status')
      data = fx.docker ?? { installed: false, version: '', running: false, compose_available: false, compose_v1_only: false, proxmox_host: true }
    else if (path === '/packages/node-status') data = { installed: false, version: '', nvm_installed: false, npm_version: '' }
    else if (path === '/packages/updates') data = fx.updates ?? { updates: [], total: 0, last_checked: '2026-10-12T10:00:00Z' }
    else if (path === '/packages/transaction') {
      data = !fx.running
        ? { transaction: null }
        : streamed
          ? { transaction: { ...TX, running: false, result: 'success', exit_code: 0, warnings: [REFUSED] } }
          : { transaction: { ...TX, running: true } }
    }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  await page.goto('/packages')
}

test('a Proxmox host is told where Docker belongs instead of being offered it', async ({ page }) => {
  await open(page)
  await expect(page.getByText('Run Docker in a VM or an LXC container')).toBeVisible()
  await expect(page.getByText('can cut virtual machines off the network')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Install Docker' })).toHaveCount(0)
})

test('the guidance is in Korean on a Korean panel', async ({ page }) => {
  await open(page, { lang: 'ko' })
  await expect(page.getByText('Docker는 VM이나 LXC 컨테이너 안에서 실행하세요')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Docker 설치' })).toHaveCount(0)
})

test('Docker already on a Proxmox host shows the DROP policy it set', async ({ page }) => {
  await open(page, {
    docker: {
      installed: true,
      version: 'Docker version 26.1.5+dfsg1',
      running: true,
      compose_available: true,
      compose_v1_only: false,
      proxmox_host: true,
      forward_policy: 'DROP',
      bridge_nf_call_iptables: true,
    },
  })
  await expect(page.getByText('FORWARD policy: DROP')).toBeVisible()
  await expect(page.getByText('bridge-nf-call-iptables: on')).toBeVisible()
  await expect(page.getByText('The FORWARD policy is DROP, which Docker sets when it turns forwarding on.', { exact: false })).toBeVisible()
})

test('an upgrade still running is followed from the banner, and ends as apt ended', async ({ page }) => {
  // The log of a good upgrade on a host without a subscription carries
  // apt-get update's E: lines for the enterprise repository: the dialog goes
  // by how apt exited, not by those words.
  const stream = [
    `id: ${TX.id}`,
    '',
    'data: >>> apt-get update',
    '',
    `data: E: Failed to fetch ${ENTERPRISE}/dists/trixie/InRelease  401  Unauthorized [IP: 203.0.113.10 443]`,
    '',
    `data: WARNING: repository ${ENTERPRISE} refused access (401)`,
    '',
    'data: Setting up pve-manager (9.2.4) ...',
    '',
    'data: >>> Packages upgraded successfully',
    '',
    'data: [DONE]',
    '',
    '',
  ].join('\n')
  await open(page, { running: true, stream })

  const banner = page.getByRole('status').filter({ hasText: 'A system upgrade is running' })
  await expect(banner).toBeVisible()
  await banner.getByRole('button', { name: 'Show progress' }).click()

  const dialog = page.getByRole('dialog')
  await expect(dialog.getByText('Setting up pve-manager (9.2.4) ...')).toBeVisible()
  await expect(dialog.getByText('Operation completed')).toBeVisible()
  // The footer's button; the dialog's corner X is named Close as well.
  const close = dialog.getByRole('button', { name: 'Close' }).first()
  await expect(close).toBeEnabled()
  // The refused repository comes back worded by its code, with the way out.
  await expect(page.getByText(`The repository ${ENTERPRISE} refused access (401).`, { exact: false })).toBeVisible()
  await close.click()
  await expect(page.getByText('A system upgrade is running')).toHaveCount(0)
})

test('putting the dialog away leaves the upgrade running, and the banner brings it back', async ({ page }) => {
  // The stream has no [DONE]: apt is still at it.
  const stream = `id: ${TX.id}\n\ndata: >>> apt-get update\n\ndata: Unpacking pve-manager (9.2.4) over (9.2.3) ...\n\n`
  await open(page, { running: true, stream, keepRunning: true })
  await page.getByRole('button', { name: 'Show progress' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByText('Unpacking pve-manager (9.2.4) over (9.2.3) ...').first()).toBeVisible()
  await dialog.getByRole('button', { name: 'Hide (keeps running)' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.getByRole('status').filter({ hasText: 'A system upgrade is running' })).toBeVisible()
})

test('a repository that refuses apt is a warning beside the update list', async ({ page }) => {
  await open(page, {
    updates: {
      updates: [{ name: 'pve-manager', current_version: '9.2.3', new_version: '9.2.4', arch: 'all' }],
      total: 1,
      last_checked: '2026-10-12T10:00:00Z',
      warnings: [REFUSED],
    },
  })
  await page.getByRole('button', { name: 'Check for Updates' }).click()
  await expect(page.getByRole('table').getByText('pve-manager')).toBeVisible()
  await expect(page.getByText('Node → Updates → Repositories', { exact: false })).toBeVisible()
  await expect(page.getByText('Failed to check updates')).toHaveCount(0)
})
