import { test, expect, type Page } from '@playwright/test'

// The sidebar tree was one flat list, sorted by name. With forty agents the
// managers were lost among them, nothing narrowed the list down, and the
// collapsed rail — the default — grew past the window and took the sign-out
// button with it. A node that had left the cluster stayed selected for good:
// every page asked the manager for it and got "Node not found". Mocked API:
// three managers and forty agents in groups db and web, or in none.
test.use({ viewport: { width: 1280, height: 800 }, locale: 'en-US' })

const base = { role: 'voter', status: 'online', grpc_address: '192.0.2.1:3629', joined_at: '2026-01-01T00:00:00Z', last_seen: '2026-01-01T00:00:00Z' }
const managers = ['m-1', 'm-2', 'm-3'].map((id, i) => ({ ...base, id, name: id, kind: 'manager', api_address: `192.0.2.${i + 1}:3628` }))
const groupOf = (i: number) => (i % 3 === 0 ? 'db' : i % 3 === 1 ? 'web' : '')
const agents = Array.from({ length: 40 }, (_, k) => {
  const i = k + 1
  const group = groupOf(i)
  return {
    ...base, id: `agent-${i}`, name: `agent-${i}`, kind: 'agent', role: '',
    api_address: `198.51.100.${i}:3628`, labels: group ? { group } : {},
  }
})
// Listed out of order on purpose: the tree sorts.
const nodes = [...agents.slice().reverse(), ...managers.slice().reverse()]

async function mock(page: Page, opts: { storage?: Record<string, string>; list?: () => unknown[]; nodesDown?: boolean; versions?: Record<string, string> } = {}) {
  const requests: string[] = []
  await page.addInitScript((storage) => {
    sessionStorage.setItem('token', 'fixture-token')
    // Only on the first load: a reload must find what the page itself stored.
    if (sessionStorage.getItem('fixture-seeded')) return
    sessionStorage.setItem('fixture-seeded', '1')
    for (const [k, v] of Object.entries(storage)) localStorage.setItem(k, v)
  }, opts.storage ?? {})
  await page.route('**/api/v1/**', async (route) => {
    const url = new URL(route.request().url())
    const path = url.pathname.replace('/api/v1', '')
    requests.push(path + url.search)
    let data: unknown = {}
    if (path === '/auth/setup-status') data = { setup_required: false }
    else if (path === '/cluster/status') data = { enabled: true, name: 'lab', node_count: nodes.length, leader_id: 'm-1', local_id: 'm-1', is_leader: true }
    else if (path === '/cluster/nodes') {
      if (opts.nodesDown) {
        // What a follower answers with the leader unreachable.
        await route.fulfill({ status: 503, json: { success: false, error: { code: 'CLUSTER_NOT_LEADER', message: 'leader unreachable' } } })
        return
      }
      data = { nodes: opts.list?.() ?? nodes, local_id: 'm-1', is_leader: true }
    }
    else if (path === '/cluster/overview') data = { name: 'lab', node_count: nodes.length, leader_id: 'm-1', nodes, metrics: [] }
    else if (path === '/cluster/events') data = { events: [] }
    else if (path === '/system/overview' && opts.versions) data = { version: opts.versions[url.searchParams.get('node') ?? 'm-1'] }
    else if (url.searchParams.get('node') === 'gone') {
      // What the manager answers for a node it does not know (proxy.go).
      await route.fulfill({ status: 400, json: { success: false, error: { code: 'INVALID_REQUEST', message: 'Node not found: gone' } } })
      return
    }
    await route.fulfill({ json: { success: true, data } })
  })
  await page.routeWebSocket(/\/ws\//, () => {})
  return requests
}

const EXPANDED = { 'sfpanel-cluster-tree-collapsed': 'false' }
const treeNodes = (page: Page) => page.locator('[data-tree-node]')

test('groups the tree into managers and agent groups', async ({ page }) => {
  await mock(page, { storage: EXPANDED })
  await page.goto('/cluster/overview')

  const managersSection = page.getByRole('region', { name: 'Managers' })
  await expect(managersSection.locator('[data-tree-node]')).toHaveText(['m-1', 'm-2', 'm-3'])
  await expect(page.getByRole('region', { name: 'db' }).locator('[data-tree-node]').first()).toHaveText('agent-3')
  await expect(page.getByRole('region', { name: 'web' }).locator('[data-tree-node]').first()).toHaveText('agent-1')
  await expect(page.getByRole('region', { name: 'No group' }).locator('[data-tree-node]').first()).toHaveText('agent-2')
  // In this order, each heading a button that folds its section.
  const heads = page.getByRole('region').getByRole('button', { expanded: true })
  await expect(heads).toHaveText([/^Managers/, /^db/, /^web/, /^No group/])
  await expect(treeNodes(page)).toHaveCount(43)
})

test('searches the tree', async ({ page }) => {
  await mock(page, { storage: EXPANDED })
  await page.goto('/cluster/overview')
  await expect(treeNodes(page)).toHaveCount(43)

  await page.getByRole('searchbox', { name: 'Search nodes' }).fill('agent-37')
  await expect(treeNodes(page)).toHaveText(['agent-37'])
  await expect(page.getByRole('region', { name: 'web' })).toBeVisible()
  await expect(page.getByRole('region', { name: 'Managers' })).toHaveCount(0)
})

test('remembers a collapsed group', async ({ page }) => {
  await mock(page, { storage: EXPANDED })
  await page.goto('/cluster/overview')
  const db = page.getByRole('region', { name: 'db' })
  await expect(db.locator('[data-tree-node]')).toHaveCount(13)

  await db.getByRole('button', { name: /^db/ }).click()
  await expect(db.getByRole('button', { name: /^db/ })).toHaveAttribute('aria-expanded', 'false')
  await expect(db.locator('[data-tree-node]')).toHaveCount(0)
  await expect(page.getByRole('region', { name: 'web' }).locator('[data-tree-node]')).toHaveCount(14)

  await page.reload()
  await expect(page.getByRole('region', { name: 'web' }).locator('[data-tree-node]')).toHaveCount(14)
  await expect(db.getByRole('button', { name: /^db/ })).toHaveAttribute('aria-expanded', 'false')
  await expect(db.locator('[data-tree-node]')).toHaveCount(0)
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem('sfpanel-tree-collapsed') ?? '[]'))).toEqual(['group:db'])
})

test('the collapsed rail scrolls and keeps sign-out', async ({ page }) => {
  // No stored choice: the rail is the default.
  await mock(page)
  await page.goto('/cluster/overview')
  // The rail lists the managers, then db, web and the agents with no group.
  const last = page.getByRole('button', { name: 'agent-38', exact: true })
  await expect(last).toBeAttached()
  // Only with every node drawn: before the list answers, the rail is short.
  const signOut = page.getByRole('button', { name: 'Logout' })
  await expect(signOut).toBeInViewport()
  await expect(last).not.toBeInViewport()

  await last.scrollIntoViewIfNeeded()
  await expect(last).toBeInViewport()
  await expect(signOut).toBeInViewport()
  // The cluster's name is still the datacenter button's title.
  await expect(page.getByTitle('lab', { exact: true })).toBeVisible()
})

test('falls back to this node when the selected one is gone', async ({ page }) => {
  const requests = await mock(page, {
    storage: {
      sfpanel_current_node: 'gone',
      'sfpanel-cluster-selection': JSON.stringify({ type: 'node', nodeId: 'gone' }),
    },
  })
  await page.goto('/dashboard')

  await expect(page.getByText('gone is no longer in the cluster, so this node is shown instead.')).toBeVisible()
  expect(await page.evaluate(() => localStorage.getItem('sfpanel_current_node'))).toBeNull()
  // Until the node list answered, the page asked for the gone node; then it
  // was drawn again for this node, and asked without it.
  const lastGone = () => requests.findLastIndex((r) => r.includes('node=gone'))
  expect(lastGone()).toBeGreaterThanOrEqual(0)
  await expect.poll(() => requests.slice(lastGone() + 1).filter((r) => !r.startsWith('/cluster/')).length).toBeGreaterThan(0)
  // The tree marks this node.
  await expect.poll(() => page.evaluate(() => localStorage.getItem('sfpanel-cluster-selection'))).toBe(JSON.stringify({ type: 'node', nodeId: 'm-1' }))
})

// Node ids are UUIDs; the operator knows the node by its name.
test('a node that leaves while shown is named by its name', async ({ page }) => {
  const leaving = { ...base, id: '3f2b9c1e-6a4d-4b8e-9f0a-1c2d3e4f5a6b', name: 'agent-07', kind: 'agent', role: '', api_address: '198.51.100.7:3628' }
  let list: unknown[] = [...nodes, leaving]
  await mock(page, {
    storage: {
      sfpanel_current_node: leaving.id,
      'sfpanel-cluster-selection': JSON.stringify({ type: 'node', nodeId: leaving.id }),
    },
    list: () => list,
  })
  await page.goto('/dashboard')
  await expect(page.getByText('agent-07', { exact: true }).first()).toBeVisible()

  list = nodes
  await page.evaluate(() => window.dispatchEvent(new Event('sfpanel:cluster-changed')))
  await expect(page.getByText('agent-07 is no longer in the cluster, so this node is shown instead.')).toBeVisible()
  await expect(page.getByText(/3f2b9c1e/)).toHaveCount(0)
})

// The menu's header named a node it had not looked up yet "Unknown node", and
// kept saying so while the leader could not be reached.
test('the node menu does not call a node unknown before the list says so', async ({ page }) => {
  const requests = await mock(page, {
    storage: {
      sfpanel_current_node: 'agent-5',
      'sfpanel-cluster-selection': JSON.stringify({ type: 'node', nodeId: 'agent-5' }),
    },
    nodesDown: true,
  })
  await page.goto('/dashboard')
  await expect.poll(() => requests.filter((r) => r === '/cluster/nodes').length).toBeGreaterThan(0)
  await expect(page.getByRole('link', { name: 'Dashboard' }).first()).toBeVisible()
  await expect(page.getByText('Unknown node')).toHaveCount(0)
})

// The footer showed the version of the node selected when the page loaded,
// whichever node was open after that.
test('the version under the tree is the open node\'s', async ({ page }) => {
  await mock(page, { storage: EXPANDED, versions: { 'm-1': '0.80.0', 'agent-5': '0.79.3' } })
  await page.goto('/cluster/overview')
  const footer = page.getByRole('button', { name: 'Logout' }).locator('..')
  await expect(footer).toHaveText('v0.80.0')

  await page.locator('[data-tree-node]', { hasText: /^agent-5$/ }).click()
  await expect(footer).toHaveText('v0.79.3')
  await page.locator('[data-tree-node]', { hasText: /^m-1$/ }).click()
  await expect(footer).toHaveText('v0.80.0')
})

test('shows a change the page reports at once', async ({ page }) => {
  let list: unknown[] = nodes
  await mock(page, { storage: EXPANDED, list: () => list })
  await page.goto('/cluster/overview')
  await expect(treeNodes(page)).toHaveCount(43)

  list = [...nodes, { ...base, id: 'agent-41', name: 'agent-41', kind: 'agent', role: '', api_address: '198.51.100.41:3628' }]
  await page.evaluate(() => window.dispatchEvent(new Event('sfpanel:cluster-changed')))
  // Well inside the tree's 15-second poll.
  await expect(page.getByRole('region', { name: 'No group' }).getByText('agent-41', { exact: true })).toBeVisible({ timeout: 3000 })
})

test.describe('phone', () => {
  test.use({ viewport: { width: 412, height: 860 }, hasTouch: true })

  test('a phone picks a node from grouped, searchable sections', async ({ page }) => {
    await mock(page)
    await page.goto('/cluster/overview')
    await page.getByRole('button', { name: 'More' }).tap()
    const drawer = page.getByRole('dialog')
    await drawer.getByRole('button', { name: 'm-1' }).tap()

    await expect(drawer.getByRole('region', { name: 'Managers' }).getByRole('button')).toHaveCount(3)
    await expect(drawer.getByRole('region', { name: 'db' }).getByRole('button')).toHaveCount(13)
    await drawer.getByRole('searchbox', { name: 'Search nodes' }).fill('agent-37')
    await expect(drawer.getByRole('region')).toHaveCount(1)
    await drawer.getByRole('region', { name: 'web' }).getByRole('button', { name: 'agent-37' }).tap()
    await expect.poll(() => page.evaluate(() => localStorage.getItem('sfpanel_current_node'))).toBe('agent-37')
  })
})
