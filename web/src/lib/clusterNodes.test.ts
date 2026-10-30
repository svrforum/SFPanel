import { describe, expect, it } from 'vitest'
import type { ClusterNode } from '@/types/api'
import {
  ALL_NODES,
  compareVersions,
  detectJoined,
  filterFromQuery,
  filterNodes,
  isAgent,
  isManager,
  isOutdated,
  lastSeenAgo,
  nodeGroup,
  nodeGroups,
  nodeRoleKind,
  referenceVersion,
  sortNodes,
} from './clusterNodes'

function node(over: Partial<ClusterNode> & { id: string }): ClusterNode {
  return {
    name: over.id,
    api_address: '',
    grpc_address: '',
    role: 'voter',
    status: 'online',
    joined_at: '',
    last_seen: '',
    ...over,
  }
}

const manager = (name: string) => node({ id: name, name, role: 'voter' })
const agent = (name: string) => node({ id: name, name, kind: 'agent', role: '' })

describe('node kinds', () => {
  it('reads a record without a kind as a manager', () => {
    expect(isManager({})).toBe(true)
    expect(isAgent({})).toBe(false)
  })

  it('takes an agent only from its kind, not from an empty role', () => {
    expect(nodeRoleKind(node({ id: 'a1', kind: 'agent', role: '' }), 'm1')).toBe('agent')
    // An older leader's record: no kind, and a role the screen must not read as "agent".
    expect(nodeRoleKind(node({ id: 'm2', role: '' }), 'm1')).toBe('manager')
    expect(nodeRoleKind(node({ id: 'm1', role: 'voter' }), 'm1')).toBe('leader')
    expect(nodeRoleKind(node({ id: 'm3', role: 'nonvoter' }), 'm1')).toBe('nonvoter')
  })
})

describe('versions', () => {
  it('compares versions by number', () => {
    expect(compareVersions('0.79.10', '0.79.9')).toBeGreaterThan(0)
    expect(compareVersions('v0.79.3', '0.79.3')).toBe(0)
    expect(compareVersions('0.80.0-rc1', '0.80.0')).toBeLessThan(0)
    expect(compareVersions('0.80.0', '0.80.0-rc1')).toBeGreaterThan(0)
    expect(compareVersions('0.79', '0.79.0')).toBe(0)
    expect(compareVersions('dev', '0.79.0')).toBe(0)
  })

  it('marks a node outdated only against a known reference', () => {
    expect(isOutdated('0.79.3', '0.79.4')).toBe(true)
    expect(isOutdated('0.79.4', '0.79.4')).toBe(false)
    expect(isOutdated(undefined, '0.79.4')).toBe(false)
    expect(isOutdated('0.79.4', undefined)).toBe(false)
    expect(referenceVersion(undefined, ['0.79.3', '0.79.10', undefined])).toBe('0.79.10')
    expect(referenceVersion('0.80.0', ['0.79.3'])).toBe('0.80.0')
    expect(referenceVersion(undefined, [undefined])).toBeUndefined()
  })
})

describe('groups', () => {
  it('reads the group label trimmed, and lists the named groups by name', () => {
    expect(nodeGroup({ labels: { group: ' db ' } })).toBe('db')
    expect(nodeGroup({})).toBe('')
    const nodes = [
      node({ id: 'a', labels: { group: 'web' } }),
      node({ id: 'b', labels: { group: 'db' } }),
      node({ id: 'c' }),
      node({ id: 'd', labels: { group: 'web' } }),
      node({ id: 'e', labels: { group: '  ' } }),
    ]
    expect(nodeGroups(nodes)).toEqual(['db', 'web'])
  })
})

describe('sortNodes', () => {
  it('sorts managers first, the leader first among them, then by name', () => {
    const input = [agent('agent-10'), manager('m-z'), agent('agent-2'), manager('m-a')]
    const out = sortNodes(input, 'm-z').map((n) => n.name)
    expect(out).toEqual(['m-z', 'm-a', 'agent-2', 'agent-10'])
    // A new array: the caller's list keeps its order.
    expect(input.map((n) => n.name)).toEqual(['agent-10', 'm-z', 'agent-2', 'm-a'])
  })
})

describe('filterNodes', () => {
  const nodes = [
    node({ id: 'm1', name: 'node-a', api_address: '192.0.2.10:3628', labels: { group: 'db' } }),
    node({ id: 'm2', name: 'node-b', status: 'suspect' }),
    node({ id: 'a1', name: 'agent-01', kind: 'agent', role: '', grpc_address: '198.51.100.7:3629', labels: { group: 'db', zone: 'east' } }),
    node({ id: 'a2', name: 'agent-02', kind: 'agent', role: '', status: 'offline' }),
    node({ id: 'a3', name: 'agent-03', kind: 'agent', role: '', status: 'joining', labels: { group: 'web' } }),
  ]
  const ids = (f: Partial<typeof ALL_NODES>) => filterNodes(nodes, { ...ALL_NODES, ...f }).map((n) => n.id)

  it('passes everything with no filter', () => {
    expect(ids({})).toEqual(['m1', 'm2', 'a1', 'a2', 'a3'])
  })

  it('filters by kind', () => {
    expect(ids({ kind: 'manager' })).toEqual(['m1', 'm2'])
    expect(ids({ kind: 'agent' })).toEqual(['a1', 'a2', 'a3'])
  })

  it('counts suspect and joining as offline', () => {
    expect(ids({ status: 'online' })).toEqual(['m1', 'a1'])
    expect(ids({ status: 'offline' })).toEqual(['m2', 'a2', 'a3'])
  })

  it('reads the empty group as "no group" and null as every group', () => {
    expect(ids({ group: 'db' })).toEqual(['m1', 'a1'])
    expect(ids({ group: '' })).toEqual(['m2', 'a2'])
    expect(ids({ group: null })).toEqual(['m1', 'm2', 'a1', 'a2', 'a3'])
  })

  it('matches the query against name, addresses and k=v labels, ignoring case', () => {
    expect(ids({ query: 'AGENT-0' })).toEqual(['a1', 'a2', 'a3'])
    expect(ids({ query: '192.0.2.10' })).toEqual(['m1'])
    expect(ids({ query: '198.51.100.7' })).toEqual(['a1'])
    expect(ids({ query: 'group=db' })).toEqual(['m1', 'a1'])
    expect(ids({ query: 'zone=east' })).toEqual(['a1'])
    expect(ids({ query: '  ' })).toEqual(['m1', 'm2', 'a1', 'a2', 'a3'])
  })

  it('combines the filters', () => {
    expect(ids({ kind: 'agent', status: 'offline', group: '' })).toEqual(['a2'])
  })
})

describe('filterFromQuery', () => {
  it('starts from every node when the address says nothing', () => {
    expect(filterFromQuery(new URLSearchParams(''))).toEqual(ALL_NODES)
  })

  it('takes the overview link\'s kind and status, and a group and query', () => {
    expect(filterFromQuery(new URLSearchParams('kind=agent&status=offline&group=db&q=edge'))).toEqual({
      kind: 'agent', status: 'offline', group: 'db', query: 'edge',
    })
  })

  it('reads an empty group as "no group", and ignores values it does not know', () => {
    expect(filterFromQuery(new URLSearchParams('group=&kind=robot&status=asleep'))).toEqual({ ...ALL_NODES, group: '' })
  })
})

describe('lastSeenAgo', () => {
  const now = Date.parse('2026-10-31T12:00:00Z')

  it('says how long ago a node answered', () => {
    expect(lastSeenAgo('2026-10-31T11:55:00Z', 'en', now)).toBe('5 minutes ago')
  })

  it('has nothing to say for a node that never answered', () => {
    // Go's zero time parses as a real date in year 1.
    expect(lastSeenAgo('0001-01-01T00:00:00Z', 'en', now)).toBe('')
    expect(lastSeenAgo('', 'en', now)).toBe('')
  })
})

describe('detectJoined', () => {
  const before = new Set(['m-1', 'agent-1'])
  const known = [node({ id: 'm-1' }), node({ id: 'agent-1', kind: 'agent', role: '' })]

  it('finds an agent that was not there when the token was made', () => {
    const fresh = node({ id: 'agent-2', name: 'agent-07', kind: 'agent', role: '' })
    expect(detectJoined(before, [...known, fresh])).toBe(fresh)
  })

  it('ignores the agents that were already there', () => {
    expect(detectJoined(before, known)).toBeUndefined()
  })

  // Somebody else may add a manager meanwhile; it did not use this token.
  it('ignores a new manager, and a record with no kind', () => {
    expect(detectJoined(before, [...known, node({ id: 'm-2', kind: 'manager' }), node({ id: 'm-3' })])).toBeUndefined()
  })

  it('with a name, waits for that name only', () => {
    const other = node({ id: 'agent-2', name: 'agent-08', kind: 'agent', role: '' })
    const named = node({ id: 'agent-3', name: 'agent-07', kind: 'agent', role: '' })
    expect(detectJoined(before, [...known, other], 'agent-07')).toBeUndefined()
    expect(detectJoined(before, [...known, other, named], 'agent-07')).toBe(named)
  })
})
