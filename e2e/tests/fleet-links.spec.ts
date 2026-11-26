import { test, expect, type Locator, type Page } from '@playwright/test'

// A server's screen leads to the group task that does the same on several
// servers: package updates, a package install and a service restart open the
// task's run setup with this server picked and the value filled in, for the
// operator to add the rest. A single server offers none. Mocked API: m-1
// (attached, leader) and agent-1; the node list answers late, as a busy
// cluster's does.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const seen = '2026-01-01T00:00:00Z'
const nodes = [
  { id: 'm-1', name: 'node-a', kind: 'manager', role: 'voter', status: 'online', joined_at: seen, last_seen: seen, api_address: '192.0.2.1:3628', grpc_address: '192.0.2.1:3629' },
  { id: 'agent-1', name: 'agent-01', kind: 'agent', role: '', status: 'online', joined_at: seen, last_seen: seen, api_address: '198.51.100.1:3628', grpc_address: '198.51.100.1:3629' },
]

// The built-ins as the server lists them: Korean names from the binary.
const builtin = (file: string, name: string) => ({ id: `builtin/${file}`, name, source: { kind: 'builtin' }, read_only: true })
const BUILTINS = [
  builtin('package-update', '패키지 업데이트'),
  builtin('package-install', '패키지 설치'),
  builtin('service-restart', '서비스 재시작'),
  builtin('command', '명령 실행'),
  builtin('reboot', '재부팅 (필요할 때만)'),
  builtin('agent-install', '에이전트 설치 (SSH)'),
]
const PACKAGE_INSTALL = {
  ...BUILTINS[1],
  yaml: 'name: 패키지 설치\n',
  task: {
    name: '패키지 설치',
    inputs: [{ name: 'packages', label: '설치할 패키지', required: true }],
    targets: {},
    batch: 1,
    stop_on_failure: true,
    steps: [{ kind: 'packages', name: '패키지 설치' }],
  },
}

/**
 * Mocks the panel's API. plans has the body of every run preview asked for,
 * each answered 500, so nothing runs.
 */
async function mock(page: Page, opts: { standalone?: boolean; nodes?: typeof nodes } = {}) {
  const listed = opts.nodes ?? nodes
  const plans: unknown[] = []
  await page.addInitScript(() => {
    sessionStorage.setItem('token', 'fixture-token')
    localStorage.setItem('i18nextLng', 'en')
    localStorage.setItem('sfpanel_language', 'en')
  })
  await page.route('**/api/v1/**', async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname.replace('/api/v1', '')
    if (req.method() === 'POST' && path === '/cluster/task-runs/plan') {
      plans.push(req.postDataJSON())
      await route.fulfill({ status: 500, json: { success: false, error: { code: 'INTERNAL_ERROR', message: 'fixture' } } })
      return
    }
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/auth/ws-ticket') data = { ticket: 'fixture-ticket' }
    else if (path === '/cluster/status') data = opts.standalone
      ? { enabled: false }
      : { enabled: true, name: 'lab', node_count: listed.length, leader_id: 'm-1', local_id: 'm-1', is_leader: true }
    else if (path === '/cluster/nodes') {
      await new Promise((resolve) => setTimeout(resolve, 800))
      data = { nodes: listed, local_id: 'm-1', is_leader: true }
    }
    else if (path === '/cluster/overview') data = { name: 'lab', node_count: listed.length, leader_id: 'm-1', nodes: listed, metrics: [] }
    else if (path === '/cluster/events') data = { events: [] }
    else if (path === '/packages/docker-status') data = { installed: true, version: '27.0.0', running: true, compose_available: true }
    else if (path === '/packages/node-status') data = { installed: false, version: '', nvm_installed: false, npm_version: '' }
    else if (path === '/packages/updates') data = {
      updates: [
        { name: 'curl', current_version: '8.5.0-1', new_version: '8.5.0-2', arch: 'amd64' },
        { name: 'openssl', current_version: '3.0.13-1', new_version: '3.0.13-2', arch: 'amd64' },
      ],
      total: 2,
      last_checked: '2026-11-17T10:00:00Z',
    }
    else if (path === '/packages/transaction') data = { transaction: null }
    else if (path === '/packages/search') data = { packages: [{ name: 'nginx', description: 'small, powerful, scalable web/proxy server', installed: false }], total: 1, query: 'nginx' }
    else if (path === '/system/services') data = {
      services: [{ name: 'cron.service', description: 'Regular background program processing daemon', load_state: 'loaded', active_state: 'active', sub_state: 'running', enabled: 'enabled' }],
      total: 1,
    }
    else if (path === '/cluster/tasks') data = BUILTINS
    else if (path === '/cluster/task-runs') data = []
    else if (path.startsWith('/cluster/tasks/') && decodeURIComponent(path.slice('/cluster/tasks/'.length)) === 'builtin/package-install') data = PACKAGE_INSTALL
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  return { plans }
}

const hrefOf = async (l: Locator) => new URL((await l.getAttribute('href'))!, 'http://x')

test('the update card opens a group package update on the server in the address', async ({ page }) => {
  await mock(page)
  await page.goto('/packages?node=agent-1')
  const link = page.getByRole('link', { name: 'Update on several servers…', exact: true })
  await expect(link).toBeVisible()
  const u = await hrefOf(link)
  expect(u.pathname).toBe('/cluster/tasks/builtin%2Fpackage-update')
  expect(Object.fromEntries(u.searchParams)).toEqual({ nodes: 'agent-1' })
})

test('a search row opens a group install of that package, and the form previews exactly that', async ({ page }) => {
  const { plans } = await mock(page)
  await page.goto('/packages?node=agent-1')
  await page.getByRole('textbox', { name: 'Search packages' }).fill('nginx')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  const link = page.getByRole('row', { name: /nginx/ }).getByRole('link', { name: 'nginx: install on several servers…', exact: true })
  await expect(link).toHaveText('Install on several servers…')
  expect(await link.getAttribute('href')).toBe('/cluster/tasks/builtin%2Fpackage-install?nodes=agent-1&input.packages=nginx')
  await link.click()
  const setup = page.getByTestId('task-pane-setup')
  await expect(setup.getByLabel('Packages to install')).toHaveValue('nginx')
  // The value is read, then left out of the address.
  await expect(page).toHaveURL(/\/cluster\/tasks\/builtin%2Fpackage-install\?nodes=agent-1$/)
  await expect(setup.getByRole('checkbox', { name: 'agent-01', exact: true })).toBeChecked()
  await expect(setup.getByRole('checkbox', { name: 'node-a', exact: true })).not.toBeChecked()
  await setup.getByRole('button', { name: 'Preview' }).click()
  await expect.poll(() => plans.length).toBe(1)
  expect(plans[0]).toMatchObject({ task_id: 'builtin/package-install', inputs: { packages: 'nginx' }, targets: { nodes: ['agent-1'] } })
})

test("a service's menu opens a group restart of that unit", async ({ page }) => {
  await mock(page)
  await page.goto('/services?node=agent-1')
  await page.getByRole('button', { name: 'Actions for cron.service' }).click()
  const item = page.getByRole('menuitem', { name: 'Restart on several servers…', exact: true })
  await expect(item).toBeVisible()
  const u = await hrefOf(item)
  expect(u.pathname).toBe('/cluster/tasks/builtin%2Fservice-restart')
  expect(Object.fromEntries(u.searchParams)).toEqual({ nodes: 'agent-1', 'input.service': 'cron.service' })
})

test('a single server offers no group task', async ({ page }) => {
  await mock(page, { standalone: true })
  await page.goto('/packages')
  await page.getByRole('textbox', { name: 'Search packages' }).fill('nginx')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page.getByRole('row', { name: /nginx/ })).toBeVisible()
  await expect(page.getByRole('link', { name: /on several servers/i })).toHaveCount(0)
  await page.goto('/services')
  await page.getByRole('button', { name: 'Actions for cron.service' }).click()
  await expect(page.getByRole('menuitem', { name: 'Restart' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: /several servers/ })).toHaveCount(0)
})

// A cluster of one server has no other to add: the run setup would be a form
// with one box. The links wait for the node list, as moving a stack does.
test('a cluster of one server offers no group task', async ({ page }) => {
  await mock(page, { nodes: [nodes[0]] })
  await page.goto('/packages')
  // The list names the server: by then a link would be drawn.
  await expect(page.getByRole('button', { name: 'Choose a server: node-a' })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Upgrade All' })).toBeVisible()
  await expect(page.getByRole('link', { name: /on several servers/i })).toHaveCount(0)
  await page.getByRole('textbox', { name: 'Search packages' }).fill('nginx')
  await page.getByRole('button', { name: 'Search', exact: true }).click()
  await expect(page.getByRole('row', { name: /nginx/ })).toBeVisible()
  await expect(page.getByRole('link', { name: /on several servers/i })).toHaveCount(0)
  await page.goto('/services')
  await expect(page.getByRole('button', { name: 'Choose a server: node-a' })).toBeVisible()
  await page.getByRole('button', { name: 'Actions for cron.service' }).click()
  await expect(page.getByRole('menuitem', { name: 'Restart' })).toBeVisible()
  await expect(page.getByRole('menuitem', { name: /several servers/ })).toHaveCount(0)
})

// The card of tools installed on other screens links them as the menu does:
// a server's screen names the server shown, the network map names none.
test("the tools installed elsewhere lead to the same server's screens", async ({ page }) => {
  await mock(page)
  await page.goto('/packages?node=agent-1')
  const card = page.getByRole('region', { name: 'Installed from other screens' })
  await expect(card.getByRole('link', { name: 'Network / VPN › WireGuard' })).toBeVisible()
  expect(await card.getByRole('link').evaluateAll((a) => a.map((l) => l.getAttribute('href')))).toEqual([
    '/terminal?node=agent-1', '/network/wireguard?node=agent-1', '/network/tailscale?node=agent-1', '/firewall/fail2ban?node=agent-1',
    '/disk/overview?node=agent-1', '/disk/network-shares?node=agent-1', '/map',
  ])
})

// The dev tools card points to the terminal for the AI CLIs: in a cluster, the
// terminal of the server whose packages are shown.
test("the dev tools card leads to the terminal of the server shown", async ({ page }) => {
  await mock(page)
  await page.goto('/packages?node=agent-1')
  await expect(page.getByRole('link', { name: /^Claude, Codex and Gemini CLI are installed/ })).toHaveAttribute('href', '/terminal?node=agent-1')
})
