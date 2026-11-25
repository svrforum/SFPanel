import { describe, expect, it } from 'vitest'
import {
  CLUSTER_MODE_KEY,
  LOCAL_ID_KEY,
  RETIRED_KEYS,
  adoptUrlNode,
  carriesNode,
  hrefWithNode,
  menuHref,
  nodeParam,
  scopeKind,
  searchWithNode,
  storeValue,
  urlValue,
} from './serverScope'

// vitest runs these files in the node environment: no DOM, so no localStorage.
function memoryStorage(seed: Record<string, string> = {}): Storage {
  const map = new Map(Object.entries(seed))
  return {
    get length() {
      return map.size
    },
    clear: () => map.clear(),
    getItem: (key: string) => map.get(key) ?? null,
    key: (index: number) => Array.from(map.keys())[index] ?? null,
    removeItem: (key: string) => {
      map.delete(key)
    },
    setItem: (key: string, value: string) => {
      map.set(key, value)
    },
  }
}

// The ids handed to setNode, in order; an empty list means it was never called.
function adopt(search: string, seed: Record<string, string>): { calls: (string | null)[]; storage: Storage } {
  const storage = memoryStorage(seed)
  const calls: (string | null)[] = []
  adoptUrlNode(search, storage, (id) => calls.push(id))
  return { calls, storage }
}

const CLUSTER = { [CLUSTER_MODE_KEY]: 'true', [LOCAL_ID_KEY]: 'm-1' }

it('reads the node parameter', () => {
  expect(nodeParam('?node=m-2')).toBe('m-2'); expect(nodeParam('?node=')).toBeNull(); expect(nodeParam('?a=1')).toBeNull()
})

it('stores the attached server as null, and names it in the address', () => {
  expect(storeValue('m-1', 'm-1')).toBeNull(); expect(storeValue('m-2', 'm-1')).toBe('m-2'); expect(storeValue('m-2', null)).toBe('m-2')
  expect(urlValue(null, 'm-1')).toBe('m-1'); expect(urlValue('m-2', 'm-1')).toBe('m-2')
})

it('keeps a page\'s own parameters and their order', () => {
  expect(searchWithNode('?scope=node&tab=alerts', 'a-1')).toBe('?scope=node&tab=alerts&node=a-1')
  expect(searchWithNode('?node=a&tab=x', 'b')).toBe('?node=b&tab=x')
  expect(searchWithNode('?node=a&tab=x', null)).toBe('?tab=x')
  expect(searchWithNode('?node=a', null)).toBe('')
  expect(hrefWithNode('/docker/containers?container=c1&tab=logs', 'a-1')).toBe('/docker/containers?container=c1&tab=logs&node=a-1')
  expect(hrefWithNode('/dashboard?node=x', 'a-1')).toBe('/dashboard?node=a-1')
})

it('says which screens carry the server', () => {
  expect(scopeKind('/processes', '')).toBe('server'); expect(scopeKind('/terminal', '')).toBe('server')
  expect(scopeKind('/cluster/running', '')).toBe('fleet'); expect(scopeKind('/cluster/nodes', '')).toBe('fleet')
  expect(scopeKind('/settings', '?tab=account')).toBe('savedFleet'); expect(scopeKind('/settings', '?scope=node&tab=alerts')).toBe('savedServer')
  expect(scopeKind('/no-such-page', '')).toBeNull()
  expect([carriesNode('/logs', ''), carriesNode('/settings', '?scope=node'), carriesNode('/settings', ''), carriesNode('/cluster/overview', '')])
    .toEqual([true, true, false, false])
})

it('names the server shown in a server link, in a cluster only', () => {
  expect(menuHref('/services', true, 'agent-1')).toBe('/services?node=agent-1')
  expect(menuHref('/settings?scope=node&tab=system', true, 'm-1')).toBe('/settings?scope=node&tab=system&node=m-1')
  // The pages about all servers and account settings name no server.
  expect([menuHref('/cluster', true, 'm-1'), menuHref('/cluster/overview', true, 'm-1'), menuHref('/settings', true, 'm-1')])
    .toEqual(['/cluster', '/cluster/overview', '/settings'])
  // A single server, or an attached server whose id is not known yet.
  expect([menuHref('/services', false, 'm-1'), menuHref('/services', true, '')]).toEqual(['/services', '/services'])
})

describe('adoptUrlNode', () => {
  it('lets the address win over the stored server', () => {
    expect(adopt('?node=agent-1', { ...CLUSTER, sfpanel_current_node: 'm-2' }).calls).toEqual(['agent-1'])
  })

  it('stores the attached server as null', () => {
    expect(adopt('?node=m-1', { ...CLUSTER, sfpanel_current_node: 'm-2' }).calls).toEqual([null])
  })

  it('adopts on a first visit too, before the mode is known', () => {
    expect(adopt('?node=agent-1', {}).calls).toEqual(['agent-1'])
  })

  it('never adopts on a single server', () => {
    expect(adopt('?node=agent-1', { [CLUSTER_MODE_KEY]: 'false', [LOCAL_ID_KEY]: 'm-1' }).calls).toEqual([])
  })

  it('does nothing without a node parameter', () => {
    expect(adopt('', CLUSTER).calls).toEqual([])
    expect(adopt('?tab=alerts', CLUSTER).calls).toEqual([])
  })

  it('clears the retired tree keys', () => {
    const retired = Object.fromEntries(RETIRED_KEYS.map((key) => [key, 'x']))
    const cases: [string, Record<string, string>][] = [
      ['?node=agent-1', CLUSTER],
      ['', CLUSTER],
      ['?node=agent-1', { [CLUSTER_MODE_KEY]: 'false' }],
      ['?node=agent-1', {}],
    ]
    for (const [search, seed] of cases) {
      const { storage } = adopt(search, { ...seed, ...retired, 'sfpanel-sidebar-collapsed': 'true' })
      expect(RETIRED_KEYS.map((key) => storage.getItem(key))).toEqual(RETIRED_KEYS.map(() => null))
      expect(storage.getItem('sfpanel-sidebar-collapsed')).toBe('true')
      expect(storage.getItem(CLUSTER_MODE_KEY)).toBe(seed[CLUSTER_MODE_KEY] ?? null)
    }
  })
})
