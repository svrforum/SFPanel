import { describe, expect, it } from 'vitest'
import type { AgentInstallTailnetPeer } from '@/types/api'
import { emptyServerRow, type InstallServerRow } from './installForm'
import { addPicks, filterPeers, methodAfterPicks, pickable, pickName, SEARCH_FROM } from './installTailnet'

const peer = (name: string, ip: string, over: Partial<AgentInstallTailnetPeer> = {}): AgentInstallTailnetPeer =>
  ({ name, dns_name: `${name}.example.ts.net`, ip, os: 'linux', online: true, ssh: false, node: '', ...over })
const row = (host: string, name = ''): InstallServerRow => ({ host, port: '22', name, group: '' })

describe('pickable', () => {
  it('takes an online device that is not a node of the cluster', () => {
    expect(pickable(peer('web-01', '100.64.0.11'))).toBe(true)
  })

  it('refuses a device already in the cluster: it is installed', () => {
    expect(pickable(peer('agent-01', '100.64.0.33', { node: 'agent-01' }))).toBe(false)
  })

  it('refuses an offline device: the install needs it online', () => {
    expect(pickable(peer('db-01', '100.64.0.12', { online: false }))).toBe(false)
  })
})

describe('pickName', () => {
  it("is the device's name when the form takes it", () => {
    expect(pickName(peer('web-01', '100.64.0.11'))).toBe('web-01')
  })

  it('is empty for a name the form would refuse, so the server names itself', () => {
    expect(pickName(peer('my laptop', '100.64.0.40'))).toBe('')
    expect(pickName(peer('-web', '100.64.0.41'))).toBe('')
    expect(pickName(peer('', '100.64.0.42'))).toBe('')
  })
})

describe('addPicks', () => {
  it('a table holding only a blank row is replaced by the picks', () => {
    const { servers, added } = addPicks([emptyServerRow()], [peer('web-01', '100.64.0.11'), peer('web-02', '100.64.0.12')])
    expect(servers).toEqual([row('100.64.0.11', 'web-01'), row('100.64.0.12', 'web-02')])
    expect(added.map((p) => p.ip)).toEqual(['100.64.0.11', '100.64.0.12'])
  })

  it('picks follow the rows already filled, and blank rows go as a paste takes them', () => {
    const { servers } = addPicks([row('192.0.2.21', 'web-04'), emptyServerRow()], [peer('web-01', '100.64.0.11')])
    expect(servers).toEqual([row('192.0.2.21', 'web-04'), row('100.64.0.11', 'web-01')])
  })

  it('an IP already in the table is skipped, and not counted as added', () => {
    const { servers, added } = addPicks(
      [row(' 100.64.0.11 ', 'typed')],
      [peer('web-01', '100.64.0.11'), peer('web-02', '100.64.0.12')],
    )
    expect(servers).toEqual([row(' 100.64.0.11 ', 'typed'), row('100.64.0.12', 'web-02')])
    expect(added.map((p) => p.name)).toEqual(['web-02'])
  })

  it('a name the form refuses is left empty', () => {
    const { servers } = addPicks([emptyServerRow()], [peer('my laptop', '100.64.0.40')])
    expect(servers).toEqual([row('100.64.0.40')])
  })

  it('nothing to add leaves the table as it is', () => {
    const table = [emptyServerRow()]
    expect(addPicks(table, [])).toEqual({ servers: table, added: [] })
  })
})

describe('methodAfterPicks', () => {
  const ssh = (name: string, ip: string) => peer(name, ip, { ssh: true })

  it('switches to Tailscale SSH when every server added runs it', () => {
    expect(methodAfterPicks('password', [ssh('web-01', '100.64.0.11'), ssh('web-02', '100.64.0.12')]))
      .toEqual({ method: 'tailscale', switched: true, withoutSsh: [] })
    expect(methodAfterPicks('key', [ssh('web-01', '100.64.0.11')]))
      .toEqual({ method: 'tailscale', switched: true, withoutSsh: [] })
  })

  it('says nothing when the login is Tailscale SSH already', () => {
    expect(methodAfterPicks('tailscale', [ssh('web-01', '100.64.0.11')]))
      .toEqual({ method: 'tailscale', switched: false, withoutSsh: [] })
  })

  it('some with it and some without: names the ones without, and leaves the login', () => {
    const picks = [ssh('web-01', '100.64.0.11'), peer('web-02', '100.64.0.12'), peer('', '100.64.0.13')]
    expect(methodAfterPicks('password', picks)).toEqual({ method: 'password', switched: false, withoutSsh: ['web-02', '100.64.0.13'] })
    expect(methodAfterPicks('tailscale', picks)).toEqual({ method: 'tailscale', switched: false, withoutSsh: ['web-02', '100.64.0.13'] })
  })

  it('none with it, or none added: the login stays and nothing is said', () => {
    expect(methodAfterPicks('password', [peer('web-02', '100.64.0.12')])).toEqual({ method: 'password', switched: false, withoutSsh: [] })
    expect(methodAfterPicks('key', [])).toEqual({ method: 'key', switched: false, withoutSsh: [] })
  })
})

describe('filterPeers', () => {
  const peers = [peer('web-01', '100.64.0.11'), peer('Web-02', '100.64.0.12'), peer('db-01', '100.64.0.21')]

  it('a search of more than eight devices is offered', () => {
    expect(SEARCH_FROM).toBe(9)
  })

  it('narrows by name, without case', () => {
    expect(filterPeers(peers, 'WEB').map((p) => p.ip)).toEqual(['100.64.0.11', '100.64.0.12'])
  })

  it('narrows by IP', () => {
    expect(filterPeers(peers, '0.21').map((p) => p.name)).toEqual(['db-01'])
  })

  it('an empty search keeps every device, in the order given', () => {
    expect(filterPeers(peers, '  ')).toEqual(peers)
  })
})
