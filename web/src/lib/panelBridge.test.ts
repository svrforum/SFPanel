import { describe, expect, it } from 'vitest'
import type { ClusterNode } from '@/types/api'
import { SERVER_EVENT, bridgeServer, buildBridge, publishBridge, type PanelBridge } from './panelBridge'

const t = (k: string) => `T(${k})`

function node(over: Partial<ClusterNode> & { id: string }): ClusterNode {
  return { name: over.id, api_address: '', grpc_address: '', role: 'voter', status: 'online', joined_at: '', last_seen: '', ...over }
}

describe('the menu the app draws', () => {
  it('builds server, all-servers and tail groups with every tab', () => {
    const b = buildBridge(true, { name: 'agent-1', role: 'agent', attached: false }, t)
    expect(b.v).toBe(1)
    expect(b.nav.map((g) => g.group)).toEqual(['server', 'fleet', 'tail'])
    expect(b.nav.map((g) => g.title)).toEqual(['agent-1', 'T(layout.groups.fleet)', ''])
    expect(b.nav[0].items).toHaveLength(9)
    const system = b.nav[0].items.find((i) => i.path === '/services')!
    expect(system.label).toBe('T(layout.nav.system)')
    expect(system.tabs.map((x) => x.path)).toEqual(['/services', '/processes', '/cron', '/packages', '/tuning'])
    expect(system.tabs.map((x) => x.label)).toEqual(['T(layout.nav.services)', 'T(layout.nav.processes)', 'T(layout.nav.cron)', 'T(layout.nav.packages)', 'T(layout.nav.tuning)'])
    expect(b.nav[0].items.find((i) => i.path === '/files')!.tabs).toEqual([])
  })

  it('a single server has no titles and no cluster-only items', () => {
    const b = buildBridge(false, null, t)
    expect(b.server).toBeNull()
    expect(b.nav.map((g) => g.title)).toEqual(['', '', ''])
    expect(b.nav.flatMap((g) => g.items).map((i) => i.path)).toHaveLength(12)
    expect(b.nav.flatMap((g) => g.items).map((i) => i.path)).not.toContain('/cluster/running')
  })

  it('a cluster whose server is not known yet titles its server group with nothing', () => {
    expect(buildBridge(true, null, t).nav[0].title).toBe('')
  })

  it('every path is a same-origin path', () => {
    for (const cluster of [true, false]) for (const g of buildBridge(cluster, null, t).nav) for (const i of g.items)
      for (const p of [i.path, ...i.tabs.map((x) => x.path)]) expect(p).toMatch(/^\/(?!\/)/)
  })
})

describe('the server the app names', () => {
  it('names the server only in a cluster with a known node', () => {
    const agent = node({ id: 'agent-1', kind: 'agent', role: '' })
    const manager = node({ id: 'm-1' })
    expect(bridgeServer({ clusterEnabled: false, current: manager, selected: null })).toBeNull()
    expect(bridgeServer({ clusterEnabled: null, current: manager, selected: null })).toBeNull()
    expect(bridgeServer({ clusterEnabled: true, current: undefined, selected: 'agent-1' })).toBeNull()
    expect(bridgeServer({ clusterEnabled: true, current: agent, selected: 'agent-1' })).toEqual({ name: 'agent-1', role: 'agent', attached: false })
    expect(bridgeServer({ clusterEnabled: true, current: manager, selected: null })).toEqual({ name: 'm-1', role: 'manager', attached: true })
  })

  it('announces a new server once, not a relabelled one', () => {
    const sent: Event[] = []
    const target = { dispatchEvent: (e: Event) => (sent.push(e), true) } as { __sfpanel?: PanelBridge; dispatchEvent(e: Event): boolean }
    const s = { name: 'm-1', role: 'manager', attached: true } as const
    publishBridge(target, buildBridge(true, s, t))
    publishBridge(target, buildBridge(true, s, (k) => k))
    publishBridge(target, buildBridge(true, { ...s, name: 'm-2', attached: false }, t))
    expect(sent.map((e) => (e as CustomEvent).detail?.name)).toEqual(['m-1', 'm-2'])
    expect(sent.map((e) => e.type)).toEqual([SERVER_EVENT, SERVER_EVENT])
    expect(target.__sfpanel?.server?.name).toBe('m-2')
  })

  it('keeps the relabelled menu even when it announces nothing', () => {
    const target = { dispatchEvent: () => true } as { __sfpanel?: PanelBridge; dispatchEvent(e: Event): boolean }
    const s = { name: 'm-1', role: 'manager', attached: true } as const
    publishBridge(target, buildBridge(true, s, t))
    publishBridge(target, buildBridge(true, s, (k) => `ko:${k}`))
    expect(target.__sfpanel?.nav[1].title).toBe('ko:layout.groups.fleet')
  })
})
