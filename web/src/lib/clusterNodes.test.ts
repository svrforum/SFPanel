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
  otherManagerAddress,
  referenceVersion,
  roleChangeFailure,
  roleChangePhase,
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

describe('roleChangePhase', () => {
  const min = 60_000
  const convert = { nodeId: 'm-2', target: 'agent' as const, startedAt: 0 }
  const promote = { nodeId: 'a-1', target: 'manager' as const, startedAt: 0 }

  it('a conversion is done once the node answers as an agent', () => {
    expect(roleChangePhase(convert, [node({ id: 'm-2', kind: 'manager' })], min)).toBe('running')
    // Between leaving Raft and registering again the node is not listed at all.
    expect(roleChangePhase(convert, [], min)).toBe('running')
    expect(roleChangePhase(convert, [node({ id: 'm-2', kind: 'agent', role: '', status: 'offline' })], min)).toBe('running')
    expect(roleChangePhase(convert, [node({ id: 'm-2', kind: 'agent', role: '' })], min)).toBe('done')
  })

  it('a conversion that has not ended in three minutes has timed out', () => {
    expect(roleChangePhase(convert, [node({ id: 'm-2', kind: 'manager' })], 3 * min)).toBe('running')
    expect(roleChangePhase(convert, [node({ id: 'm-2', kind: 'manager' })], 3 * min + 1)).toBe('timeout')
    expect(roleChangePhase(convert, [node({ id: 'm-2', kind: 'agent', role: '' })], 10 * min)).toBe('done')
  })

  it('a promotion is done once the node votes, not when it is a manager', () => {
    expect(roleChangePhase(promote, [node({ id: 'a-1', kind: 'agent', role: '' })], min)).toBe('running')
    expect(roleChangePhase(promote, [node({ id: 'a-1', kind: 'manager', role: 'nonvoter' })], min)).toBe('running')
    expect(roleChangePhase(promote, [node({ id: 'a-1', kind: 'manager', role: 'voter' })], min)).toBe('done')
  })

  it('a manager not voting after two minutes points at the Raft port, never at a timeout', () => {
    const nonvoter = [node({ id: 'a-1', kind: 'manager', role: 'nonvoter' })]
    expect(roleChangePhase(promote, nonvoter, 2 * min + 1)).toBe('raftPort')
    expect(roleChangePhase(promote, nonvoter, 30 * min)).toBe('raftPort')
    // Still an agent: asking again is the answer.
    expect(roleChangePhase(promote, [node({ id: 'a-1', kind: 'agent', role: '' })], 3 * min + 1)).toBe('timeout')
  })
})

describe('roleChangeFailure', () => {
  const err = (status: number, code: string, message: string) => Object.assign(new Error(message), { status, code })

  it('a refusal carries its reason in the message', () => {
    expect(roleChangeFailure(err(409, 'CLUSTER_CONVERT_REFUSED', 'a rotation is pending'), 'CLUSTER_CONVERT_REFUSED'))
      .toEqual({ kind: 'refused', message: 'a rotation is pending' })
    expect(roleChangeFailure(err(502, 'CLUSTER_PROMOTE_REFUSED', 'not an agent'), 'CLUSTER_PROMOTE_REFUSED'))
      .toEqual({ kind: 'refused', message: 'not an agent' })
  })

  it('a node that did not answer is not a refusal: the change may be under way', () => {
    expect(roleChangeFailure(err(502, 'INTERNAL_ERROR', 'EOF'), 'CLUSTER_CONVERT_REFUSED').kind).toBe('noAnswer')
    // The same code from anything else is an ordinary failure.
    expect(roleChangeFailure(err(500, 'INTERNAL_ERROR', 'cluster CA unavailable'), 'CLUSTER_CONVERT_REFUSED'))
      .toEqual({ kind: 'other', message: 'cluster CA unavailable' })
    expect(roleChangeFailure(err(409, 'CLUSTER_PROMOTE_REFUSED', 'x'), 'CLUSTER_CONVERT_REFUSED').kind).toBe('other')
  })
})

describe('otherManagerAddress', () => {
  it('names another manager that answers', () => {
    const nodes = [
      node({ id: 'm-1', kind: 'manager', api_address: '192.0.2.1:3628', status: 'offline' }),
      node({ id: 'm-2', kind: 'manager', api_address: '192.0.2.2:3628' }),
      node({ id: 'a-1', kind: 'agent', role: '', api_address: '198.51.100.1:3628' }),
      node({ id: 'm-3', api_address: '192.0.2.3:3628' }),
    ]
    expect(otherManagerAddress(nodes, 'm-2')).toBe('192.0.2.3:3628')
    // None answers: one that may come back is better than nothing.
    expect(otherManagerAddress(nodes.slice(0, 3), 'm-2')).toBe('192.0.2.1:3628')
    expect(otherManagerAddress([nodes[1], nodes[2]], 'm-2')).toBe('')
  })
})
