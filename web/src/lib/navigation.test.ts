import { describe, expect, it } from 'vitest'
import ko from '../i18n/locales/ko.json'
import en from '../i18n/locales/en.json'
import { BOTTOM_NAV_ITEMS, NAV_ITEMS, TERMINAL_ROUTES, documentTitle, isTerminalRoute, moreMenuGroups, navFor, navOwner, navTabs, pageTitleKey } from './navigation'

const at = (tree: unknown, key: string): unknown =>
  key.split('.').reduce<unknown>((n, p) => (n && typeof n === 'object' ? (n as Record<string, unknown>)[p] : undefined), tree)

const CLUSTER = ['/terminal', '/dashboard', '/docker', '/files', '/logs', '/services', '/network', '/firewall', '/disk', '/cluster/running', '/cluster/tasks', '/cluster', '/settings']

describe('the menu', () => {
  it('lists 13 entries in a cluster and 11 on a single server, in sidebar order', () => {
    expect(navFor(true).map((e) => e.to)).toEqual(CLUSTER)
    expect(navFor(false).map((e) => e.to)).toEqual(CLUSTER.filter((to) => to !== '/cluster/running' && to !== '/cluster/tasks'))
  })

  it('groups nine server entries, then all-servers, then settings', () => {
    expect(navFor(true).map((e) => e.group)).toEqual([...Array(9).fill('server'), 'fleet', 'fleet', 'fleet', 'tail'])
  })

  it('keeps the bottom bar and its order', () => {
    expect(BOTTOM_NAV_ITEMS.map((e) => e.to)).toEqual(['/dashboard', '/docker', '/terminal', '/logs'])
  })

  it('gives system four tabs, the cluster three, the dashboard two, Docker six ending in the app store', () => {
    expect(navTabs('/services').map((t) => t.to)).toEqual(['/services', '/processes', '/cron', '/packages'])
    expect(navTabs('/cluster').map((t) => t.to)).toEqual(['/cluster/overview', '/cluster/nodes', '/cluster/security'])
    expect(navTabs('/dashboard').map((t) => t.to)).toEqual(['/dashboard', '/running'])
    expect(navTabs('/docker').map((t) => t.to).at(-1)).toBe('/appstore')
    expect(navTabs('/docker')).toHaveLength(6)
  })

  it('finds the owner by the longest match', () => {
    expect(navOwner('/processes')).toMatchObject({ entry: { to: '/services' }, tab: { to: '/processes' } })
    expect(navOwner('/cluster/running')?.entry.to).toBe('/cluster/running')
    expect(navOwner('/cluster/tasks/runs/7')?.entry.to).toBe('/cluster/tasks')
    expect(navOwner('/cluster/nodes')).toMatchObject({ entry: { to: '/cluster' }, tab: { to: '/cluster/nodes' } })
    expect(navOwner('/docker/stacks/web')?.tab?.to).toBe('/docker/stacks')
    // An entry that leads to its own first tab: the tab, so the page is titled by it.
    expect(navOwner('/services')).toMatchObject({ entry: { to: '/services' }, tab: { to: '/services' } })
    expect(navOwner('/servicesx')).toBeNull()
    expect(navOwner('/no-such-page')).toBeNull()
  })

  it('titles a page by its own tab where it has one, otherwise by its section', () => {
    expect(pageTitleKey('/processes')).toBe('layout.nav.processes')
    expect(pageTitleKey('/services')).toBe('layout.nav.services')
    expect(pageTitleKey('/running')).toBe('layout.tabs.runningHere')
    expect(pageTitleKey('/appstore/x')).toBe('layout.nav.appstore')
    expect(pageTitleKey('/network/wireguard')).toBe('layout.nav.networkVpn')
    expect(pageTitleKey('/dashboard')).toBe('layout.nav.dashboard')
    expect(pageTitleKey('/cluster/running')).toBe('layout.nav.running')
    expect(pageTitleKey('/')).toBeNull()
  })

  it('resolves every label key in ko and en', () => {
    const keys = NAV_ITEMS.flatMap((e) => [e.labelKey, ...(e.mobileLabelKey ? [e.mobileLabelKey] : []), ...(e.tabs ?? []).map((t) => t.labelKey)])
    for (const tree of [ko, en]) expect(keys.filter((key) => typeof at(tree, key) !== 'string')).toEqual([])
  })
})

describe('moreMenuGroups', () => {
  const view = (cluster: boolean) => moreMenuGroups(cluster).map((g) => [g.group, g.items.map((e) => e.to)])

  it('groups what the bottom bar does not carry: the server pages, all servers, then settings', () => {
    expect(view(true)).toEqual([
      ['server', ['/files', '/services', '/network', '/firewall', '/disk']],
      ['fleet', ['/cluster/running', '/cluster/tasks', '/cluster']],
      ['tail', ['/settings']],
    ])
    expect(view(false)).toEqual([
      ['server', ['/files', '/services', '/network', '/firewall', '/disk']],
      ['fleet', ['/cluster']],
      ['tail', ['/settings']],
    ])
  })

  it('leaves out the bottom bar, and with it holds every entry once', () => {
    for (const cluster of [true, false]) {
      const more = moreMenuGroups(cluster).flatMap((g) => g.items.map((e) => e.to))
      const bottom = BOTTOM_NAV_ITEMS.map((e) => e.to)
      expect(more.filter((to) => bottom.includes(to))).toEqual([])
      expect([...bottom, ...more].sort()).toEqual(navFor(cluster).map((e) => e.to).sort())
    }
  })
})

describe('isTerminalRoute', () => {
  // The one console page hosts the xterm and its own input area; the shell
  // has to drop its padding and hide the bottom nav there.
  it('covers the terminal page, and the retired AI route is no longer one', () => {
    expect(isTerminalRoute('/terminal')).toBe(true)
    expect(isTerminalRoute('/ai')).toBe(false)
    expect(TERMINAL_ROUTES).toEqual(['/terminal'])
  })

  it('matches exactly, so a child route is an ordinary page', () => {
    expect(isTerminalRoute('/terminal/x')).toBe(false)
    expect(isTerminalRoute('/docker')).toBe(false)
    expect(isTerminalRoute('')).toBe(false)
  })

  it('names routes that exist in the registry, and the registry has no AI entry', () => {
    const routes = new Set(NAV_ITEMS.map((i) => i.to))
    for (const r of TERMINAL_ROUTES) expect(routes.has(r)).toBe(true)
    expect(routes.has('/ai')).toBe(false)
  })
})

describe('documentTitle', () => {
  it('names the page, the server, then the product', () => expect(documentTitle('Docker', 'node-b')).toBe('Docker · node-b · SFPanel'))
  it('leaves the server out when there is none', () => expect(documentTitle('Docker', null)).toBe('Docker · SFPanel'))
  it('is the product alone without a page', () => expect(documentTitle(null, 'node-b')).toBe('SFPanel'))
})
