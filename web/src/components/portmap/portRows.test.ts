import { describe, expect, it } from 'vitest'
import type { PortMapRow } from '@/types/api'
import { canAddRule, filterPortRows } from './portRows'

const row = (o: Partial<PortMapRow> & { port: number }): PortMapRow => ({ proto: 'tcp', state: 'listening', firewall: null, containers: [], process: null, ...o })
const ROWS = [
  row({ port: 22, process: { pid: 812, name: 'sshd' }, addresses: ['0.0.0.0', '::'] }),
  row({ port: 53, proto: 'udp', process: { pid: 640, name: 'resolver' } }),           // an older node: no addresses
  row({ port: 8080, state: 'bound', containers: [{ id: 'c1', name: 'web', stack: 'site' }], addresses: ['0.0.0.0'] }),
]

describe('filterPortRows', () => {
  it('finds a row by port, process, pid, address, container and stack', () => {
    expect(filterPortRows(ROWS, ' SSHD ', 'all').map(r => r.port)).toEqual([22])
    expect(filterPortRows(ROWS, '640', 'all').map(r => r.port)).toEqual([53])
    expect(filterPortRows(ROWS, '::', 'all').map(r => r.port)).toEqual([22])
    expect(filterPortRows(ROWS, 'site', 'all').map(r => r.port)).toEqual([8080])
  })
  it('filters by protocol', () => expect(filterPortRows(ROWS, '', 'udp').map(r => r.port)).toEqual([53]))
  it('finds a row without addresses by its port and not by an address', () => {
    expect(filterPortRows(ROWS, '53', 'all').map(r => r.port)).toEqual([53])
    expect(filterPortRows(ROWS, '0.0.0.0', 'all').map(r => r.port)).toEqual([22, 8080])
  })
})

describe('canAddRule', () => {
  it('offers a rule only where something listens', () => {
    expect(ROWS.map(canAddRule)).toEqual([true, true, false])
  })
})
