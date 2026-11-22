import { describe, expect, it } from 'vitest'
import type { LanScanHost, LanScanNetwork, LanScanResult, LanScanStatus, NetworkInterfaceInfo, PortMapRow } from '@/types/api'
import {
  addressParts,
  addressValue,
  columnsFor,
  defaultScanner,
  deviceName,
  exposure,
  filterSegment,
  mapPort,
  networkEntries,
  rowsOf,
  serverInterfaces,
  serverPorts,
  shownCount,
  type MapNode,
} from './netmap'

const NET = '10.20.30.0/24'
const mac = (n: number) => `02:42:ac:14:1e:${n.toString(16).padStart(2, '0')}`

function host(n: number, extra: Partial<LanScanHost> = {}): LanScanHost {
  return { ip: `10.20.30.${n}`, mac: mac(n), vendor: '', hostname: '', self: false, timed_out: false, ports: [], ...extra }
}

function result(hosts: LanScanHost[], extra: Partial<LanScanResult> = {}): LanScanResult {
  return {
    network: NET,
    interface: 'eth0',
    address: '10.20.30.26',
    gateway: '10.20.30.1',
    ports: true,
    started_at: '2026-11-22T02:12:04Z',
    finished_at: '2026-11-22T02:13:08Z',
    hosts,
    ...extra,
  }
}

function network(n: number, extra: Partial<LanScanNetwork> = {}): LanScanNetwork {
  return { cidr: NET, interface: 'eth0', address: `10.20.30.${n}`, mac: mac(n), gateway: '10.20.30.1', scannable: true, reason: '', ...extra }
}

function status(extra: Partial<LanScanStatus> = {}): LanScanStatus {
  return { installed: true, networks: [], running: null, last: null, results: [], ...extra }
}

function node(id: string, n: number | null, extra: Partial<MapNode> = {}): MapNode {
  return { id, name: id, online: true, macs: n === null ? [] : [mac(n)], addresses: n === null ? [] : [`10.20.30.${n}`], guests: [], ...extra }
}

/** The one segment a single node's single result makes. */
function segmentFrom(nodes: MapNode[], scanner: string, hosts: LanScanHost[], extra: Partial<LanScanResult> = {}) {
  const entries = networkEntries(nodes, new Map([[scanner, status({ networks: [network(26)], results: [result(hosts, extra)] })]]))
  expect(entries).toHaveLength(1)
  return entries[0].segment!
}

describe('a scanned network', () => {
  it('tells the gateway, the nodes and the rest apart, each list by address', () => {
    const nodes = [node('alpha', 26), node('beta', 9)]
    const s = segmentFrom(nodes, 'alpha', [host(26, { self: true }), host(200), host(9), host(1, { hostname: 'router.lan' }), host(10)])
    expect(s.gateways.map((d) => [d.ip, d.gatewayFor])).toEqual([['10.20.30.1', ['alpha']]])
    expect(s.gatewayAddress).toBe('10.20.30.1')
    // .9 before .26: by the number, not by the text.
    expect(s.servers.map((d) => [d.ip, d.node?.id])).toEqual([['10.20.30.9', 'beta'], ['10.20.30.26', 'alpha']])
    expect(s.devices.map((d) => d.ip)).toEqual(['10.20.30.10', '10.20.30.200'])
    expect(s.count).toBe(5)
    expect(s.scannedBy).toEqual({ id: 'alpha', name: 'alpha' })
    expect(s.finishedAt).toBe(Date.parse('2026-11-22T02:13:08Z'))
  })

  it('knows the node that scanned by what the node said, its interfaces read or not', () => {
    const s = segmentFrom([node('alpha', null)], 'alpha', [host(26, { self: true, mac: '' }), host(1)])
    expect(s.servers.map((d) => [d.ip, d.node?.id, d.scanner])).toEqual([['10.20.30.26', 'alpha', true]])
    expect(s.servers[0].key).toBe('ip:10.20.30.26')
  })

  it('names a node by an interface address in any case, and not by one two nodes share', () => {
    const upper = segmentFrom([node('alpha', 26), node('beta', null, { macs: [mac(9).toUpperCase()] })], 'alpha', [host(26, { self: true }), host(9)])
    expect(upper.servers.map((d) => d.node?.id)).toEqual(['beta', 'alpha'])
    const cloned = segmentFrom([node('alpha', 26), node('beta', 9), node('gamma', 9, { addresses: [] })], 'alpha', [host(26, { self: true }), host(9)])
    expect(cloned.servers.map((d) => d.node?.id)).toEqual(['alpha'])
    expect(cloned.devices.map((d) => d.ip)).toEqual(['10.20.30.9'])
  })

  it('hangs guests under their host, and a guest that is a node keeps being a node', () => {
    const nodes = [
      node('alpha', 26),
      node('pve', 10, {
        guests: [
          { vmid: 111, title: 'web', kind: 'vm', running: true, macs: [mac(40).toUpperCase()] },
          { vmid: 208, title: 'beta-vm', kind: 'vm', running: true, macs: [mac(20)] },
          { vmid: 300, title: 'box', kind: 'lxc', running: true, macs: [mac(30)] },
        ],
      }),
      node('beta', 20),
    ]
    const s = segmentFrom(nodes, 'alpha', [host(26, { self: true }), host(10), host(20), host(30), host(40), host(50), host(1)])
    expect(s.servers.map((d) => d.node?.id)).toEqual(['pve', 'alpha'])
    const pve = s.servers[0]
    expect(pve.children.map((d) => [d.ip, d.node?.id ?? null, d.guest?.vmid, d.guest?.kind])).toEqual([
      ['10.20.30.20', 'beta', 208, 'vm'],
      ['10.20.30.30', null, 300, 'lxc'],
      ['10.20.30.40', null, 111, 'vm'],
    ])
    expect(pve.children[0].guest).toEqual({ hostId: 'pve', hostName: 'pve', vmid: 208, kind: 'vm', title: 'beta-vm' })
    expect(s.devices.map((d) => d.ip)).toEqual(['10.20.30.50'])
    expect(shownCount(s)).toBe(6)
  })

  it('leaves a guest among the devices when its host is not on this network', () => {
    const nodes = [node('alpha', 26), node('pve', null, { guests: [{ vmid: 111, title: 'web', kind: 'vm', running: true, macs: [mac(40)] }] })]
    const s = segmentFrom(nodes, 'alpha', [host(26, { self: true }), host(40)])
    expect(s.devices.map((d) => [d.ip, d.guest?.title])).toEqual([['10.20.30.40', 'web']])
  })

  it('keeps a gateway that is a guest on top, not under its host', () => {
    const nodes = [node('alpha', 26), node('pve', 10, { guests: [{ vmid: 100, title: 'router', kind: 'vm', running: true, macs: [mac(1)] }] })]
    const s = segmentFrom(nodes, 'alpha', [host(26, { self: true }), host(10), host(1)])
    expect(s.gateways.map((d) => d.guest?.title)).toEqual(['router'])
    expect(s.servers.find((d) => d.node?.id === 'pve')?.children).toEqual([])
  })

  it('takes the running one of two guests with one address, and neither of two that both run', () => {
    const guests = (running: boolean) => [
      { vmid: 111, title: 'original', kind: 'vm' as const, running: true, macs: [mac(40)] },
      { vmid: 112, title: 'restored', kind: 'vm' as const, running, macs: [mac(40)] },
    ]
    const one = segmentFrom([node('alpha', 26), node('pve', 10, { guests: guests(false) })], 'alpha', [host(26, { self: true }), host(10), host(40)])
    expect(one.servers[0].children.map((d) => d.guest?.title)).toEqual(['original'])
    const both = segmentFrom([node('alpha', 26), node('pve', 10, { guests: guests(true) })], 'alpha', [host(26, { self: true }), host(10), host(40)])
    expect(both.servers[0].children).toEqual([])
    expect(both.devices.map((d) => d.guest)).toEqual([null])
  })

  it('never makes a node a guest of itself, nor two hosts guests of each other', () => {
    const self = segmentFrom(
      [node('pve', 10, { guests: [{ vmid: 1, title: 'itself', kind: 'vm', running: true, macs: [mac(10)] }] })],
      'pve',
      [host(10, { self: true })],
    )
    expect(self.servers.map((d) => [d.node?.id, d.guest])).toEqual([['pve', null]])
    const loop = segmentFrom(
      [
        node('a', 10, { guests: [{ vmid: 1, title: 'b-vm', kind: 'vm', running: true, macs: [mac(20)] }] }),
        node('b', 20, { guests: [{ vmid: 2, title: 'a-vm', kind: 'vm', running: true, macs: [mac(10)] }] }),
      ],
      'a',
      [host(10, { self: true }), host(20)],
    )
    // One of the two claims is kept; drawn, every device appears once.
    expect(shownCount(loop)).toBe(2)
    expect(loop.servers).toHaveLength(1)
    expect(loop.servers[0].children).toHaveLength(1)
    expect(loop.servers[0].children[0].children).toEqual([])
  })

  it('carries the ports in order, and what the port check gave up on', () => {
    const s = segmentFrom([node('alpha', 26)], 'alpha', [
      host(26, { self: true }),
      host(40, { ports: [{ port: 8443, proto: 'tcp' }, { port: 22, proto: 'tcp' }, { port: 80, proto: 'tcp' }] }),
      host(41, { timed_out: true }),
    ])
    expect(s.devices[0].ports.map((p) => [p.port, p.name, p.href])).toEqual([
      [22, 'SSH', null],
      [80, null, 'http://10.20.30.40/'],
      [8443, null, 'https://10.20.30.40:8443/'],
    ])
    expect(s.devices[1].timedOut).toBe(true)
    expect(s.portsChecked).toBe(true)
  })

  it('says the gateway address where the scan did not find it', () => {
    const s = segmentFrom([node('alpha', 26)], 'alpha', [host(26, { self: true })])
    expect(s.gateways).toEqual([])
    expect(s.gatewayAddress).toBe('10.20.30.1')
  })
})

describe('ports as chips', () => {
  it('links what a browser may open, over https where that is the custom', () => {
    expect(mapPort('10.20.30.5', 443, 'tcp').href).toBe('https://10.20.30.5/')
    expect(mapPort('10.20.30.5', 8006, 'tcp').href).toBe('https://10.20.30.5:8006/')
    expect(mapPort('10.20.30.5', 3000, 'tcp').href).toBe('http://10.20.30.5:3000/')
  })

  it('does not link a port nothing says speaks HTTP, nor a UDP one', () => {
    expect(mapPort('10.20.30.5', 62078, 'tcp')).toEqual({ port: 62078, proto: 'tcp', name: null, href: null })
    expect(mapPort('10.20.30.5', 445, 'tcp')).toEqual({ port: 445, proto: 'tcp', name: 'SMB', href: null })
    expect(mapPort('10.20.30.5', 443, 'udp').href).toBeNull()
  })
})

describe('networks across nodes', () => {
  it('draws the latest of the scans of one network, and lists both nodes on it', () => {
    const older = result([host(26, { self: true }), host(9), host(1), host(77)], { finished_at: '2026-11-22T01:00:00Z' })
    const newer = result([host(9, { self: true }), host(26), host(1)], { finished_at: '2026-11-22T02:00:00Z', address: '10.20.30.9' })
    const entries = networkEntries(
      [node('alpha', 26), node('beta', 9)],
      new Map([
        ['alpha', status({ networks: [network(26)], results: [older] })],
        ['beta', status({ networks: [network(9)], results: [newer] })],
      ]),
    )
    expect(entries).toHaveLength(1)
    expect(entries[0].segment?.scannedBy.id).toBe('beta')
    // The older scan's .77 is not remembered: the map is one scan.
    expect(entries[0].segment?.count).toBe(3)
    expect(entries[0].scanners.map((s) => s.nodeId)).toEqual(['beta', 'alpha'])
    expect(defaultScanner(entries[0])?.nodeId).toBe('beta')
  })

  it('keeps two sites apart that use the same addresses', () => {
    const here = result([host(26, { self: true }), host(1)])
    const there = result([host(9, { self: true, mac: mac(109) }), host(1, { mac: mac(101) })], { address: '10.20.30.9' })
    const entries = networkEntries(
      [node('alpha', 26), node('remote', 109)],
      new Map([
        ['alpha', status({ networks: [network(26)], results: [here] })],
        ['remote', status({ networks: [network(9, { mac: mac(109) })], results: [there] })],
      ]),
    )
    expect(entries.map((e) => [e.segment?.scannedBy.id, e.scanners.map((s) => s.nodeId)])).toEqual([
      ['alpha', ['alpha']],
      ['remote', ['remote']],
    ])
  })

  it('puts a node the scan saw with that scan, and one it did not by its network and gateway', () => {
    const entries = networkEntries(
      [node('alpha', 26), node('seen', 9), node('down-then', 50)],
      new Map([
        ['alpha', status({ networks: [network(26)], results: [result([host(26, { self: true }), host(9), host(1)])] })],
        ['seen', status({ networks: [network(9)] })],
        ['down-then', status({ networks: [network(50)] })],
      ]),
    )
    expect(entries).toHaveLength(1)
    expect(entries[0].scanners.map((s) => s.nodeId)).toEqual(['alpha', 'seen', 'down-then'])
  })

  it('lists a network no scan covers, with the nodes on it', () => {
    const entries = networkEntries(
      [node('alpha', 26), node('beta', 9), node('far', 5)],
      new Map([
        ['alpha', status({ networks: [network(26)] })],
        ['beta', status({ networks: [network(9)], installed: false })],
        ['far', status({ networks: [network(5, { cidr: '10.9.0.0/16', gateway: '10.9.0.1', scannable: false, reason: 'too_large' })] })],
      ]),
    )
    expect(entries.map((e) => [e.cidr, e.segment, e.scanners.map((s) => s.nodeId)])).toEqual([
      ['10.9.0.0/16', null, ['far']],
      ['10.20.30.0/24', null, ['alpha', 'beta']],
    ])
    expect(defaultScanner(entries[1])?.nodeId).toBe('alpha')
    // Too large to scan: nobody can be asked.
    expect(defaultScanner(entries[0])).toBeNull()
    expect(entries[0].scanners[0].reason).toBe('too_large')
  })

  it('asks the next node that can when the one that scanned last no longer can', () => {
    const base = { networks: [network(26)], results: [result([host(26, { self: true }), host(9)])] }
    const offline = networkEntries([node('alpha', 26, { online: false }), node('beta', 9)], new Map([['alpha', status(base)], ['beta', status({ networks: [network(9)] })]]))
    expect(defaultScanner(offline[0])?.nodeId).toBe('beta')
    const noTool = networkEntries([node('alpha', 26), node('beta', 9)], new Map([['alpha', status({ ...base, installed: false })], ['beta', status({ networks: [network(9)], installed: false })]]))
    expect(defaultScanner(noTool[0])).toBeNull()
  })

  it('shows a scan that is running on the network it is of', () => {
    const entries = networkEntries(
      [node('alpha', 26)],
      new Map([['alpha', status({ networks: [network(26)], running: { network: NET, ports: false, started_at: '2026-11-22T03:00:00Z' } })]]),
    )
    expect(entries[0].running).toEqual({ nodeId: 'alpha', nodeName: 'alpha', network: NET, ports: false, startedAt: Date.parse('2026-11-22T03:00:00Z') })
  })

  it('leaves out a node that is not in the cluster any more', () => {
    expect(networkEntries([], new Map([['gone', status({ networks: [network(26)], results: [result([host(26, { self: true })])] })]]))).toEqual([])
  })
})

describe('more than one way out', () => {
  // Six servers of one LAN, one of which goes out through a router of its own
  // (a firewall VM): the real cluster this was first run on.
  const guests = [{ vmid: 100, title: 'firewall', kind: 'vm' as const, running: true, macs: [mac(99)] }, { vmid: 111, title: 'sub', kind: 'vm' as const, running: true, macs: [mac(12)] }]
  const nodes = [node('alpha', 26), node('pve', 10, { guests }), node('sub', 12)]

  it('lists a network once before any scan, whatever gateway each node uses', () => {
    const entries = networkEntries(nodes, new Map([
      ['alpha', status({ networks: [network(26)] })],
      ['pve', status({ networks: [network(10)], installed: false })],
      ['sub', status({ networks: [network(12, { gateway: '10.20.30.99' })], installed: false })],
    ]))
    expect(entries.map((e) => [e.cidr, e.scanners.map((s) => s.nodeId)])).toEqual([[NET, ['alpha', 'pve', 'sub']]])
  })

  it('draws every device a node goes out through as a gateway, and says which nodes use it', () => {
    const entries = networkEntries(nodes, new Map([
      ['alpha', status({ networks: [network(26)], results: [result([host(26, { self: true }), host(10), host(12), host(99), host(1)])] })],
      ['pve', status({ networks: [network(10)] })],
      ['sub', status({ networks: [network(12, { gateway: '10.20.30.99' })] })],
    ]))
    expect(entries).toHaveLength(1)
    const s = entries[0].segment!
    expect(s.gateways.map((d) => [d.ip, d.gatewayFor, d.guest?.title ?? null])).toEqual([
      ['10.20.30.1', ['alpha', 'pve'], null],
      ['10.20.30.99', ['sub'], 'firewall'],
    ])
    // The firewall is a guest of the host, and is drawn where traffic goes through it, not inside the host.
    expect(s.servers.find((d) => d.node?.id === 'pve')?.children.map((d) => d.ip)).toEqual(['10.20.30.12'])
    expect(shownCount(s)).toBe(3)
  })

  it('puts a node that was down for the scan with it, when its gateway is a device the scan saw', () => {
    const entries = networkEntries([...nodes, node('late', 40)], new Map([
      ['alpha', status({ networks: [network(26)], results: [result([host(26, { self: true }), host(99), host(1)])] })],
      ['late', status({ networks: [network(40, { gateway: '10.20.30.99' })] })],
    ]))
    expect(entries.map((e) => e.scanners.map((s) => s.nodeId))).toEqual([['alpha', 'late']])
    expect(entries[0].segment!.gateways.map((d) => [d.ip, d.gatewayFor])).toEqual([['10.20.30.1', ['alpha']], ['10.20.30.99', ['late']]])
  })

  it('keeps apart a node whose gateway the scan knows nothing of', () => {
    const entries = networkEntries([node('alpha', 26), node('elsewhere', 40)], new Map([
      ['alpha', status({ networks: [network(26)], results: [result([host(26, { self: true }), host(1)])] })],
      ['elsewhere', status({ networks: [network(40, { gateway: '10.20.30.254' })] })],
    ]))
    expect(entries.map((e) => [!!e.segment, e.scanners.map((s) => s.nodeId)])).toEqual([[true, ['alpha']], [false, ['elsewhere']]])
  })
})

describe('an address two devices use', () => {
  it('says so when a node\'s own address is answered by a MAC that is not the node\'s', () => {
    // beta is at .9; what answered at .9 is something else. beta itself is not seen.
    const s = segmentFrom([node('alpha', 26), node('beta', 9)], 'alpha', [host(26, { self: true }), host(9, { mac: mac(77), vendor: 'Example Electronics' }), host(1)])
    expect(s.conflicts.map((d) => [d.ip, d.mac, d.conflict])).toEqual([['10.20.30.9', mac(77), { id: 'beta', name: 'beta' }]])
    // The impostor is not drawn as the node.
    expect(s.servers.map((d) => d.node?.id)).toEqual(['alpha'])
    expect(s.devices.map((d) => [d.ip, !!d.conflict])).toEqual([['10.20.30.9', true]])
  })

  it('does not say so of the node itself, of the node that scanned, or of a node whose interfaces were not read', () => {
    const own = segmentFrom([node('alpha', 26), node('beta', 9)], 'alpha', [host(26, { self: true }), host(9), host(1)])
    expect(own.conflicts).toEqual([])
    // The scanning node answers with no MAC of its own in nmap's eyes.
    const scanner = segmentFrom([node('alpha', 26, { macs: [mac(50)] })], 'alpha', [host(26, { self: true, mac: mac(26) })])
    expect(scanner.conflicts).toEqual([])
    const unread = segmentFrom([node('alpha', 26), node('beta', null, { addresses: ['10.20.30.9'] })], 'alpha', [host(26, { self: true }), host(9, { mac: mac(77) })])
    expect(unread.conflicts).toEqual([])
  })

  it('takes any of a node\'s interfaces for the node, in any case', () => {
    const s = segmentFrom([node('alpha', 26), node('beta', 9, { macs: [mac(60), mac(9).toUpperCase()] })], 'alpha', [host(26, { self: true }), host(9)])
    expect(s.conflicts).toEqual([])
  })

  it('names no owner for an address two nodes both report', () => {
    const s = segmentFrom([node('alpha', 26), node('beta', 9), node('gamma', 60, { addresses: ['10.20.30.60', '10.20.30.9'] })], 'alpha', [host(26, { self: true }), host(9, { mac: mac(77) })])
    expect(s.conflicts).toEqual([])
  })
})

describe('searching a segment', () => {
  const nodes = [node('alpha', 26), node('pve', 10, { guests: [{ vmid: 111, title: 'web', kind: 'vm', running: true, macs: [mac(40)] }] })]
  const s = segmentFrom(nodes, 'alpha', [
    host(26, { self: true }),
    host(10),
    host(40, { ports: [{ port: 22, proto: 'tcp' }] }),
    host(50, { vendor: 'Espressif' }),
    host(51, { hostname: 'printer.lan', ports: [{ port: 9100, proto: 'tcp' }, { port: 2222, proto: 'tcp' }] }),
    host(1),
  ])

  it('finds by address, name, maker, MAC and port', () => {
    expect(filterSegment(s, '.50').devices.map((d) => d.ip)).toEqual(['10.20.30.50'])
    expect(filterSegment(s, 'PRINTER').devices.map((d) => d.ip)).toEqual(['10.20.30.51'])
    expect(filterSegment(s, 'espressif').devices.map((d) => d.ip)).toEqual(['10.20.30.50'])
    expect(filterSegment(s, mac(51)).devices.map((d) => d.ip)).toEqual(['10.20.30.51'])
    expect(filterSegment(s, '9100').devices.map((d) => d.ip)).toEqual(['10.20.30.51'])
    expect(filterSegment(s, 'jetdirect').devices.map((d) => d.ip)).toEqual(['10.20.30.51'])
  })

  it('takes a port number whole: 22 is not 2222', () => {
    const found = filterSegment(s, '22')
    expect(found.devices).toEqual([])
    expect(found.servers.map((d) => [d.node?.id, d.children.map((c) => c.ip)])).toEqual([['pve', ['10.20.30.40']]])
  })

  it('keeps a host for the guest that matches, and all the guests of a host that does', () => {
    expect(filterSegment(s, 'web').servers.map((d) => [d.node?.id, d.children.length])).toEqual([['pve', 1]])
    expect(filterSegment(s, 'pve').servers.map((d) => [d.node?.id, d.children.length])).toEqual([['pve', 1]])
    expect(filterSegment(s, 'alpha').servers.map((d) => d.node?.id)).toEqual(['alpha'])
  })

  it('keeps the gateway, and gives the segment back for no query', () => {
    expect(filterSegment(s, 'nothing-like-this').gateways.map((d) => d.ip)).toEqual(['10.20.30.1'])
    expect(shownCount(filterSegment(s, 'nothing-like-this'))).toBe(0)
    expect(filterSegment(s, '  ')).toBe(s)
  })
})

describe('names and addresses', () => {
  it('names a device by the best it has', () => {
    const s = segmentFrom(
      [node('alpha', 26), node('pve', null, { guests: [{ vmid: 111, title: 'web', kind: 'vm', running: true, macs: [mac(40)] }] })],
      'alpha',
      [host(26, { self: true, hostname: 'alpha.lan' }), host(40, { hostname: 'web.lan', vendor: 'Proxmox Server Solutions GmbH' }), host(50, { hostname: 'cam.lan', vendor: 'Espressif' }), host(51, { vendor: 'Espressif' }), host(52)],
    )
    expect(deviceName(s.servers[0])).toEqual({ text: 'alpha', source: 'node' })
    expect(s.devices.map((d) => deviceName(d))).toEqual([
      { text: 'web', source: 'guest' },
      { text: 'cam.lan', source: 'hostname' },
      { text: 'Espressif', source: 'vendor' },
      null,
    ])
  })

  it('splits an address where the network ends', () => {
    expect(addressParts('192.0.2.203', 24)).toEqual({ network: '192.0.2.', host: '203' })
    expect(addressParts('192.0.2.203', 28)).toEqual({ network: '192.0.2.', host: '203' })
    expect(addressParts('10.20.3.203', 20)).toEqual({ network: '10.20.', host: '3.203' })
    expect(addressParts('10.20.3.203', 8)).toEqual({ network: '', host: '10.20.3.203' })
    expect(addressParts('fe80::1', 64)).toEqual({ network: '', host: 'fe80::1' })
  })

  it('orders addresses by their numbers', () => {
    expect(addressValue('10.20.30.9')).toBeLessThan(addressValue('10.20.30.10'))
    expect(addressValue('10.20.30.256')).toBeNaN()
    expect(addressValue('router')).toBeNaN()
  })
})

describe('rows of cards', () => {
  it('fits as many columns as there is room for, and never none', () => {
    expect(columnsFor(1000, 200, 12)).toBe(4)
    expect(columnsFor(211, 200, 12)).toBe(1)
    expect(columnsFor(212, 100, 12)).toBe(2)
    expect(columnsFor(150, 200, 12)).toBe(1)
    expect(columnsFor(0, 200, 12)).toBe(1)
  })

  it('cuts a list into rows', () => {
    expect(rowsOf([1, 2, 3, 4, 5], 2)).toEqual([[1, 2], [3, 4], [5]])
    expect(rowsOf([1, 2], 0)).toEqual([[1], [2]])
    expect(rowsOf([], 3)).toEqual([])
  })
})

describe('who can reach a port', () => {
  it('reads the addresses a port listens on', () => {
    expect(exposure(['0.0.0.0', '::'])).toEqual({ kind: 'all', addresses: [] })
    expect(exposure(['*'])).toEqual({ kind: 'all', addresses: [] })
    expect(exposure(['127.0.0.1', '::1'])).toEqual({ kind: 'local', addresses: [] })
    expect(exposure(['127.0.0.53'])).toEqual({ kind: 'local', addresses: [] })
    expect(exposure(['127.0.0.1', '10.20.30.26'])).toEqual({ kind: 'address', addresses: ['10.20.30.26'] })
    expect(exposure(['127.0.0.1', '0.0.0.0'])).toEqual({ kind: 'all', addresses: [] })
  })

  it('does not call a port local when the node did not say', () => {
    expect(exposure(undefined).kind).toBe('unknown')
    expect(exposure([]).kind).toBe('unknown')
  })

  it('splits a node\'s ports into what others reach and what only it does', () => {
    const row = (port: number, extra: Partial<PortMapRow> = {}): PortMapRow => ({ port, proto: 'tcp', state: 'listening', firewall: null, containers: [], process: null, ...extra })
    const { open, local } = serverPorts([
      row(8443, { addresses: ['0.0.0.0', '::'], containers: [{ id: 'c1', name: 'proxy', stack: 'edge' }, { id: 'c1', name: 'proxy', stack: 'edge' }], process: { pid: 1, name: 'docker-proxy' }, firewall: { action: 'ALLOW', scope: 'Anywhere', rule_id: 3, source: 'ufw' } }),
      row(53, { proto: 'udp', addresses: ['127.0.0.53'], process: { pid: 2, name: 'systemd-resolve' } }),
      row(22, { addresses: ['*'], process: { pid: 3, name: 'sshd' } }),
      row(5432, { addresses: ['10.20.30.26'], process: { pid: 4, name: 'postgres' } }),
      row(9000),
    ])
    expect(open.map((p) => [p.port, p.exposure, p.addresses, p.process, p.containers.map((c) => c.name)])).toEqual([
      [22, 'all', [], 'sshd', []],
      [5432, 'address', ['10.20.30.26'], 'postgres', []],
      [8443, 'all', [], 'docker-proxy', ['proxy']],
      [9000, 'unknown', [], null, []],
    ])
    expect(open[2].firewall).toEqual({ action: 'ALLOW', scope: 'Anywhere' })
    expect(local.map((p) => [p.port, p.proto])).toEqual([[53, 'udp']])
  })
})

describe('a node\'s interfaces', () => {
  const nic = (name: string, addresses: [string, number, string][], extra: Partial<NetworkInterfaceInfo> = {}): NetworkInterfaceInfo => ({
    name, type: 'ethernet', state: 'up', mac_address: mac(1).toUpperCase(), mtu: 1500, speed: 1000,
    addresses: addresses.map(([address, prefix, family]) => ({ address, prefix, family })),
    is_default: false, driver: '', tx_bytes: 0, rx_bytes: 0, tx_packets: 0, rx_packets: 0, tx_errors: 0, rx_errors: 0, ...extra,
  })

  it('shows those with an address, the default route\'s first, and counts the inner bridges', () => {
    const { shown, inner } = serverInterfaces([
      nic('lo', [['127.0.0.1', 8, 'ipv4']]),
      nic('tailscale0', [['fd7a:115c:a1e0::1', 128, 'ipv6'], ['100.64.0.5', 32, 'ipv4']]),
      nic('eth0', [['fe80::1', 64, 'ipv6'], ['10.20.30.26', 24, 'ipv4']], { is_default: true }),
      nic('eth1', []),
      nic('docker0', [['172.17.0.1', 16, 'ipv4']]),
      nic('br-0123456789ab', [['172.18.0.1', 16, 'ipv4']]),
      nic('veth1a2b3c', []),
      nic('vmbr0', [['10.20.31.2', 24, 'ipv4']], { state: 'down' }),
    ])
    expect(shown).toEqual([
      { name: 'eth0', addresses: ['10.20.30.26/24'], mac: mac(1), up: true, isDefault: true },
      { name: 'tailscale0', addresses: ['100.64.0.5/32', 'fd7a:115c:a1e0::1/128'], mac: mac(1), up: true, isDefault: false },
      { name: 'vmbr0', addresses: ['10.20.31.2/24'], mac: mac(1), up: false, isDefault: false },
    ])
    expect(inner).toBe(2)
  })
})
