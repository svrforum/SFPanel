import { describe, expect, it } from 'vitest'
import type { ComposeProjectWithStatus, Container, ContainerPort, PortMapRow, PveGuest } from '@/types/api'
import {
  attention,
  exitedAgo,
  filterContents,
  filterGuests,
  guestKey,
  guestResources,
  guestServers,
  guestState,
  hostListeners,
  linkHost,
  nodeContents,
  portChips,
  runningGuests,
  serviceName,
  stackState,
  totals,
  withinDay,
  type BoardNode,
  type GuestHost,
} from './running'

const PROJECT = 'com.docker.compose.project'

function ctr(name: string, state: string, status: string, extra: Partial<Container> = {}): Container {
  return { Id: `${name}-id`, Names: [`/${name}`], Image: `${name}:latest`, State: state, Status: status, Ports: [], Created: 0, Labels: {}, ...extra }
}
const up = (name: string, project?: string, ports: ContainerPort[] = []) =>
  ctr(name, 'running', 'Up 2 hours', { Labels: project ? { [PROJECT]: project } : {}, Ports: ports })
const managed = (name: string): ComposeProjectWithStatus => ({
  name, compose_file: 'compose.yaml', has_env: false, path: `/opt/stacks/${name}`, service_count: 0, running_count: 0, real_status: 'stopped',
})
const tcp = (PublicPort: number, IP = '0.0.0.0', PrivatePort = PublicPort): ContainerPort => ({ IP, PrivatePort, PublicPort, Type: 'tcp' })
const udp = (PublicPort: number, IP = '0.0.0.0'): ContainerPort => ({ IP, PrivatePort: PublicPort, PublicPort, Type: 'udp' })

describe('nodeContents grouping', () => {
  it('gives every managed stack a tile, even one with no containers', () => {
    const { stacks, standalone } = nodeContents([managed('monitoring')], [], null)
    expect(stacks).toHaveLength(1)
    expect(stacks[0]).toMatchObject({ name: 'monitoring', managed: true, running: 0, total: 0, state: 'stopped', services: [], ports: [] })
    expect(standalone).toEqual([])
  })

  it('gives a project seen only in container labels a tile of its own', () => {
    const { stacks } = nodeContents([managed('wiki')], [up('wiki-app-1', 'wiki'), up('scratch-web-1', 'scratch')], null)
    expect(stacks.map((s) => [s.name, s.managed, s.total])).toEqual([['scratch', false, 1], ['wiki', true, 1]])
  })

  it('matches a managed stack to its label whatever the case of the directory', () => {
    const { stacks } = nodeContents([managed('Wiki')], [up('wiki-app-1', 'wiki')], null)
    expect(stacks.map((s) => [s.name, s.managed, s.total])).toEqual([['Wiki', true, 1]])
  })

  it('puts a container with no compose label among the standalone ones', () => {
    const { stacks, standalone } = nodeContents([], [up('speedtest'), up('photos-server-1', 'photos')], null)
    expect(stacks.map((s) => s.name)).toEqual(['photos'])
    expect(standalone.map((c) => c.name)).toEqual(['speedtest'])
  })

  it('counts running containers of a stack against all of its containers', () => {
    const { stacks } = nodeContents([], [
      up('photos-server-1', 'photos'), up('photos-web-1', 'photos'), up('photos-redis-1', 'photos'),
      ctr('photos-machine-learning-1', 'restarting', 'Restarting (1) 5 seconds ago', { Labels: { [PROJECT]: 'photos' } }),
    ], null)
    expect(stacks[0]).toMatchObject({ running: 3, total: 4, state: 'partial' })
  })
})

describe('stackState', () => {
  it('reads n of m running as a word', () => {
    expect(stackState(3, 3)).toBe('running')
    expect(stackState(3, 4)).toBe('partial')
    expect(stackState(1, 4)).toBe('partial')
    expect(stackState(0, 2)).toBe('stopped')
    expect(stackState(0, 0)).toBe('stopped')
  })
})

describe('serviceName', () => {
  it('drops the project prefix and the replica suffix', () => {
    expect(serviceName('photos-server-1', 'photos')).toBe('server')
    expect(serviceName('/photos-machine-learning-1', 'photos')).toBe('machine-learning')
    expect(serviceName('photos_redis_1', 'photos')).toBe('redis')
    expect(serviceName('wiki-app-12', 'wiki')).toBe('app')
  })

  it('keeps a container_name that does not start with the project', () => {
    expect(serviceName('jellyfin', 'media')).toBe('jellyfin')
    expect(serviceName('redis-1', 'cache')).toBe('redis-1')
  })

  it('lists a replicated service once, by its worst container', () => {
    const { stacks } = nodeContents([], [
      up('shop-web-1', 'shop'),
      ctr('shop-web-2', 'restarting', 'Restarting (1) 3 seconds ago', { Labels: { [PROJECT]: 'shop' } }),
      up('shop-db-1', 'shop'),
    ], null)
    expect(stacks[0].services.map((s) => [s.name, s.container.health])).toEqual([['db', 'running'], ['web', 'restarting']])
  })
})

describe('portChips', () => {
  const host = '192.0.2.10'

  it('shows published ports only', () => {
    expect(portChips([{ IP: '', PrivatePort: 80, PublicPort: 0, Type: 'tcp' }], host)).toEqual([])
  })

  it('makes one chip per port and protocol', () => {
    const chips = portChips([tcp(8096), tcp(8096, '::'), tcp(53), udp(53), udp(53, '::')], host)
    expect(chips.map((c) => `${c.port}/${c.proto}`)).toEqual(['53/tcp', '53/udp', '8096/tcp'])
  })

  it('links an HTTP port on the node host, over https for 443 and 8443', () => {
    const chips = portChips([tcp(8096), tcp(443), tcp(8443), tcp(80)], host)
    expect(chips.map((c) => c.href)).toEqual([
      'http://192.0.2.10/',
      'https://192.0.2.10/',
      'http://192.0.2.10:8096/',
      'https://192.0.2.10:8443/',
    ])
  })

  it('calls a loopback-bound port internal and never links it', () => {
    const chips = portChips([tcp(5000, '127.0.0.1'), tcp(5001, '::1')], host)
    expect(chips.map((c) => [c.port, c.internal, c.href])).toEqual([[5000, true, null], [5001, true, null]])
  })

  it('is public when any of its bindings is', () => {
    const [chip] = portChips([tcp(3000, '127.0.0.1'), tcp(3000, '0.0.0.0')], host)
    expect(chip).toMatchObject({ internal: false, href: 'http://192.0.2.10:3000/' })
  })

  it('leaves UDP and the non-HTTP ports as plain chips', () => {
    const chips = portChips([udp(51820), tcp(22), tcp(5432), tcp(1883), tcp(6379), tcp(27017)], host)
    expect(chips.map((c) => [c.port, c.proto, c.internal, c.href])).toEqual([
      [22, 'tcp', false, null], [1883, 'tcp', false, null], [5432, 'tcp', false, null],
      [6379, 'tcp', false, null], [27017, 'tcp', false, null], [51820, 'udp', false, null],
    ])
  })

  it('links nothing when the node has no known host', () => {
    expect(portChips([tcp(8080)], null)[0].href).toBeNull()
  })

  it('brackets an IPv6 host in the link', () => {
    expect(portChips([tcp(8080)], '[2001:db8::2]')[0].href).toBe('http://[2001:db8::2]:8080/')
  })
})

describe('linkHost', () => {
  it("uses the browser's own host for the node the browser is attached to", () => {
    expect(linkHost(true, '192.0.2.1:3628', 'panel.example')).toBe('panel.example')
  })

  it("uses the host part of another node's api_address", () => {
    expect(linkHost(false, '192.0.2.2:3628', 'panel.example')).toBe('192.0.2.2')
    expect(linkHost(false, 'node-b.example:3628', 'panel.example')).toBe('node-b.example')
    expect(linkHost(false, 'https://192.0.2.3:3628', 'panel.example')).toBe('192.0.2.3')
  })

  it('brackets IPv6 literals', () => {
    expect(linkHost(false, '[2001:db8::2]:3628', 'panel.example')).toBe('[2001:db8::2]')
    expect(linkHost(true, undefined, '2001:db8::5')).toBe('[2001:db8::5]')
    expect(linkHost(true, undefined, '[2001:db8::5]')).toBe('[2001:db8::5]')
  })

  it('has no host for a node with no address', () => {
    expect(linkHost(false, '', 'panel.example')).toBeNull()
    expect(linkHost(false, undefined, 'panel.example')).toBeNull()
  })
})

describe('exitedAgo and withinDay', () => {
  it("reads Docker's own duration words", () => {
    expect(exitedAgo('Exited (1) 2 hours ago')).toEqual({ value: 2, unit: 'hour' })
    expect(exitedAgo('Exited (1) About an hour ago')).toEqual({ value: 1, unit: 'hour' })
    expect(exitedAgo('Exited (137) About a minute ago')).toEqual({ value: 1, unit: 'minute' })
    expect(exitedAgo('Exited (1) Less than a second ago')).toEqual({ value: 0, unit: 'second' })
    expect(exitedAgo('Exited (0) 3 days ago')).toEqual({ value: 3, unit: 'day' })
    expect(exitedAgo('Exited (2) 5 weeks ago')).toEqual({ value: 5, unit: 'week' })
    expect(exitedAgo('Up 2 hours')).toBeNull()
    expect(exitedAgo('Exited (1) yesterday')).toBeNull()
  })

  it('takes seconds, minutes and up to 24 hours as the last day', () => {
    expect(withinDay({ value: 40, unit: 'second' })).toBe(true)
    expect(withinDay({ value: 59, unit: 'minute' })).toBe(true)
    expect(withinDay({ value: 24, unit: 'hour' })).toBe(true)
    expect(withinDay({ value: 25, unit: 'hour' })).toBe(false)
    expect(withinDay({ value: 1, unit: 'day' })).toBe(false)
    expect(withinDay(null)).toBe(false)
  })
})

describe('attention', () => {
  const node = (id: string, containers: Container[], projects: ComposeProjectWithStatus[] = [], extra: Partial<BoardNode> = {}): BoardNode => ({
    id, name: `${id}-name`, status: 'online', contents: nodeContents(projects, containers, null), ...extra,
  })
  const exited = (name: string, status: string, project?: string) =>
    ctr(name, 'exited', status, { Labels: project ? { [PROJECT]: project } : {} })

  it('flags a node that is suspect or offline, and not one that is joining', () => {
    const items = attention([
      node('a', [], [], { status: 'offline', contents: null, lastSeen: '2026-11-20T00:00:00Z' }),
      node('b', [], [], { status: 'suspect', contents: null }),
      node('c', [], [], { status: 'joining', contents: null }),
      node('d', []),
    ])
    expect(items.map((i) => [i.rank, i.nodeId, i.subject.kind, i.opens.kind])).toEqual([[1, 'a', 'node', 'nodes'], [1, 'b', 'node', 'nodes']])
  })

  it('folds a restarting container into its stack that runs in part, and opens the stack', () => {
    const items = attention([node('a', [
      up('photos-server-1', 'photos'), up('photos-web-1', 'photos'), up('photos-redis-1', 'photos'),
      ctr('photos-machine-learning-1', 'restarting', 'Restarting (1) 5 seconds ago', { Labels: { [PROJECT]: 'photos' } }),
    ])])
    expect(items).toHaveLength(1)
    expect(items[0]).toMatchObject({ rank: 2, subject: { kind: 'stack' }, partial: { running: 3, total: 4 }, opens: { kind: 'stack' } })
    expect(items[0].problems.map((p) => [p.kind, p.container.service])).toEqual([['restarting', 'machine-learning']])
  })

  it('flags a stack that runs in part with nothing else wrong at the third rank', () => {
    const items = attention([node('a', [up('wiki-app-1', 'wiki'), exited('wiki-db-1', 'Exited (0) 2 hours ago', 'wiki')])])
    expect(items.map((i) => [i.rank, i.subject.kind, i.opens.kind])).toEqual([[3, 'stack', 'stack']])
    expect(items[0].problems).toEqual([])
  })

  it('flags an unhealthy or dead container and opens its logs', () => {
    const items = attention([node('a', [
      ctr('api', 'running', 'Up 1 hour (unhealthy)'),
      ctr('zombie', 'dead', 'Dead'),
    ])])
    expect(items.map((i) => [i.rank, i.problems[0].kind, i.opens.kind])).toEqual([[2, 'unhealthy', 'logs'], [2, 'dead', 'logs']])
  })

  it('flags a non-zero exit within the last day, with its code and age', () => {
    const items = attention([node('a', [exited('backup-agent', 'Exited (1) 2 hours ago')])])
    expect(items).toHaveLength(1)
    expect(items[0]).toMatchObject({ rank: 4, subject: { kind: 'container' }, opens: { kind: 'logs' } })
    expect(items[0].problems[0]).toMatchObject({ kind: 'exited', code: 1, age: { value: 2, unit: 'hour' } })
  })

  it('does not flag a non-zero exit from three days ago', () => {
    expect(attention([node('a', [exited('old-test', 'Exited (1) 3 days ago')])])).toEqual([])
    expect(attention([node('a', [exited('old-job-1', 'Exited (1) 3 days ago', 'jobs')])])).toEqual([])
  })

  it('does not flag a non-zero exit in words it cannot read', () => {
    expect(attention([node('a', [exited('odd', 'Exited (1) yesterday')])])).toEqual([])
  })

  it('takes a stack with nothing running and no recent failure as stopped on purpose', () => {
    const items = attention([node('a', [
      exited('monitoring-prom-1', 'Exited (0) 2 hours ago', 'monitoring'),
      exited('monitoring-grafana-1', 'Exited (0) 2 hours ago', 'monitoring'),
    ], [managed('monitoring'), managed('archive')])])
    expect(items).toEqual([])
  })

  it('opens the logs of the one container that failed in a stack that is down', () => {
    const items = attention([node('a', [exited('backup-agent', 'Exited (1) 2 hours ago', 'backup')], [managed('backup')])])
    expect(items).toHaveLength(1)
    expect(items[0]).toMatchObject({ rank: 4, subject: { kind: 'stack' }, opens: { kind: 'logs', container: { name: 'backup-agent' } } })
    expect(items[0].partial).toBeUndefined()
  })

  it('opens the stack when more than one of its containers failed', () => {
    const items = attention([node('a', [
      exited('etl-a-1', 'Exited (1) 2 hours ago', 'etl'),
      ctr('etl-b-1', 'restarting', 'Restarting (2) 1 second ago', { Labels: { [PROJECT]: 'etl' } }),
    ])])
    expect(items.map((i) => [i.rank, i.opens.kind, i.problems.map((p) => p.kind)])).toEqual([[2, 'stack', ['restarting', 'exited']]])
  })

  it('orders by rule: nodes, failing containers, stacks in part, recent exits; then by node, then by name', () => {
    const items = attention([
      node('a', [
        exited('zeta', 'Exited (1) 10 minutes ago'),
        up('wiki-app-1', 'wiki'), exited('wiki-db-1', 'Exited (0) 1 hour ago', 'wiki'),
        ctr('alpha', 'restarting', 'Restarting (1) 2 seconds ago'),
      ]),
      node('b', [ctr('beta', 'running', 'Up 3 hours (unhealthy)')]),
      node('c', [], [], { status: 'offline', contents: null }),
    ])
    expect(items.map((i) => [i.rank, i.nodeId, i.subject.kind === 'node' ? i.nodeId : i.subject.kind === 'stack' ? i.subject.stack.name : i.subject.container.name]))
      .toEqual([[1, 'c', 'c'], [2, 'a', 'alpha'], [2, 'b', 'beta'], [3, 'a', 'wiki'], [4, 'a', 'zeta']])
  })
})

describe('totals', () => {
  it('leaves a node that failed to load out of the numbers and counts it', () => {
    const a: BoardNode = {
      id: 'a', name: 'a', status: 'online',
      contents: nodeContents([managed('wiki'), managed('idle')], [up('wiki-app-1', 'wiki'), up('wiki-db-1', 'wiki'), up('speedtest'), ctr('job', 'restarting', 'Restarting (1) 1 second ago')], null),
    }
    const b: BoardNode = { id: 'b', name: 'b', status: 'online', contents: null, failed: true }
    const c: BoardNode = { id: 'c', name: 'c', status: 'offline', contents: null }
    const items = attention([a, b, c])
    expect(totals([a, b, c], items)).toEqual({ nodes: 3, online: 2, stacks: 2, running: 3, attention: 2, failed: 1 })
  })
})

describe('filterContents', () => {
  const contents = nodeContents([managed('media')], [
    up('jellyfin', 'media'), up('media-sonarr-1', 'media'), up('wiki-app-1', 'wiki'), up('speedtest'),
  ], null)

  it('keeps stacks by stack, service or container name, and standalone containers by name', () => {
    expect(filterContents(contents, 'SONARR').stacks.map((s) => s.name)).toEqual(['media'])
    expect(filterContents(contents, 'jelly').stacks.map((s) => s.name)).toEqual(['media'])
    expect(filterContents(contents, 'wiki').stacks.map((s) => s.name)).toEqual(['wiki'])
    expect(filterContents(contents, 'speed')).toMatchObject({ stacks: [], standalone: [{ name: 'speedtest' }] })
    expect(filterContents(contents, '  ')).toBe(contents)
  })
})

describe('hostListeners', () => {
  it("lists what listens on the host itself, not Docker's published ports", () => {
    const row = (port: number, proto: string, process: string | null, containers: PortMapRow['containers'] = []): PortMapRow => ({
      port, proto, state: 'listening', firewall: null, containers, process: process ? { pid: 1, name: process } : null,
    })
    expect(hostListeners([
      row(3628, 'tcp', 'sfpanel'),
      row(8096, 'tcp', 'docker-proxy', [{ id: 'x', name: 'jellyfin', stack: 'media' }]),
      row(41641, 'udp', 'tailscaled'),
      row(22, 'tcp', 'sshd'),
      row(631, 'tcp', null),
    ])).toEqual([
      { port: 22, proto: 'tcp', process: 'sshd' },
      { port: 631, proto: 'tcp', process: null },
      { port: 3628, proto: 'tcp', process: 'sfpanel' },
      { port: 41641, proto: 'udp', process: 'tailscaled' },
    ])
  })
})

const GiB = 1024 ** 3
const guest = (vmid: number, name: string, extra: Partial<PveGuest> = {}): PveGuest => ({
  vmid, name, kind: 'qemu', status: 'running', cpus: 2, mem: GiB, maxmem: 4 * GiB, uptime: 60, tags: [], macs: [], ...extra,
})

describe('runningGuests', () => {
  it('lists a node\'s guests by vmid, as VMs and LXC containers', () => {
    const list = runningGuests([guest(105, 'dns', { kind: 'lxc' }), guest(101, 'app-one')])
    expect(list.map((g) => [g.vmid, g.kind, g.title])).toEqual([[101, 'vm', 'app-one'], [105, 'lxc', 'dns']])
  })

  it('titles a guest Proxmox has no name for by its kind and vmid', () => {
    expect(runningGuests([guest(130, ''), guest(131, '  ', { kind: 'lxc' })]).map((g) => g.title)).toEqual(['VM 130', 'LXC 131'])
  })

  it('keeps tags and addresses, and reads a missing list as none', () => {
    const [g] = runningGuests([{ ...guest(101, 'app-one', { tags: ['web'] }), macs: null as unknown as string[] }])
    expect(g.tags).toEqual(['web'])
    expect(g.macs).toEqual([])
  })
})

describe('guestState', () => {
  it("reads Proxmox's words, and keeps one it does not know as unknown to the board", () => {
    expect(guestState('running')).toBe('running')
    expect(guestState('stopped')).toBe('stopped')
    expect(guestState('paused')).toBe('paused')
    expect(guestState('suspended')).toBe('suspended')
    expect(guestState('unknown')).toBe('unknown')
    expect(guestState('Running')).toBe('running')
    expect(guestState('prelaunch')).toBeNull()
    expect(guestState('')).toBeNull()
  })
})

describe('guestResources', () => {
  const resources = (extra: Partial<PveGuest>) => guestResources(runningGuests([guest(101, 'x', extra)])[0])

  it('gives the CPUs and the memory in use of what is allotted', () => {
    expect(resources({ cpus: 4, mem: 1.5 * GiB, maxmem: 4 * GiB })).toEqual({ cpus: '4', memory: '1.5 GB / 4.0 GB' })
  })

  it('gives a fraction of a CPU as Proxmox has it', () => {
    expect(resources({ cpus: 0.5 }).cpus).toBe('0.5')
    expect(resources({ cpus: 1.25 }).cpus).toBe('1.25')
    expect(resources({ cpus: 1 / 3 }).cpus).toBe('0.33')
  })

  it('gives only what is allotted for a guest that is not running', () => {
    expect(resources({ status: 'stopped', mem: 0, maxmem: 2 * GiB }).memory).toBe('2.0 GB')
    expect(resources({ status: 'stopped', mem: 5 * 1024 ** 2, maxmem: 2 * GiB }).memory).toBe('2.0 GB')
  })

  it('leaves out what Proxmox did not say', () => {
    expect(resources({ cpus: 0, maxmem: 0 })).toEqual({ cpus: null, memory: null })
  })
})

describe('filterGuests', () => {
  const list = runningGuests([guest(101, 'app-one'), guest(205, 'dns', { kind: 'lxc' })])

  it('finds a guest by its name or its vmid', () => {
    expect(filterGuests(list, 'APP').map((g) => g.vmid)).toEqual([101])
    expect(filterGuests(list, '205').map((g) => g.vmid)).toEqual([205])
    expect(filterGuests(list, '  ')).toBe(list)
    expect(filterGuests(list, 'nothing')).toEqual([])
  })
})

describe('guestServers', () => {
  // Example addresses from Docker's own range: nothing here names real hardware.
  const MAC_A = '02:42:AC:11:00:02'
  const MAC_B = '02:42:AC:11:00:03'
  const MAC_C = '02:42:AC:11:00:04'
  // Built rather than written, so it reads as the all-zero address it is.
  const ZERO = Array(6).fill('00').join(':')
  const host = (nodeId: string, ...guests: PveGuest[]): GuestHost => ({ nodeId, guests: runningGuests(guests) })
  const ifaces = (entries: Record<string, string[]>) => new Map(Object.entries(entries))
  const matches = (m: Map<string, string>) => [...m].sort(([a], [b]) => a.localeCompare(b))

  it("names the node one of whose interfaces has one of the guest's addresses, whatever the case", () => {
    const m = guestServers([host('pve', guest(101, 'app-one', { macs: [MAC_B, MAC_A] }))], ifaces({ pve: [], app: ['', MAC_A.toLowerCase()] }))
    expect(matches(m)).toEqual([[guestKey('pve', 101), 'app']])
  })

  it('names nothing when no address matches, or the node did not say', () => {
    expect(guestServers([host('pve', guest(101, 'x', { macs: [MAC_A] }))], ifaces({ app: [MAC_B] })).size).toBe(0)
    expect(guestServers([host('pve', guest(101, 'x', { macs: [MAC_A] }))], ifaces({})).size).toBe(0)
  })

  // One guest and one node a call: two guests on one node, or one address on
  // two nodes, would each name nothing for a reason of their own.
  it('never matches an empty or all-zero address', () => {
    expect(guestServers([host('pve', guest(101, 'x', { macs: ['', ZERO] }))], ifaces({ app: ['', ZERO] })).size).toBe(0)
    expect(guestServers([host('pve', guest(102, 'y', { macs: [ZERO.replace(/:/g, '-')] }))], ifaces({ app: [ZERO] })).size).toBe(0)
  })

  it('never names the host the guest runs on', () => {
    expect(guestServers([host('pve', guest(101, 'x', { macs: [MAC_A] }))], ifaces({ pve: [MAC_A] })).size).toBe(0)
    // Nor does the host's own address keep the guest from naming the node it is.
    const m = guestServers([host('pve', guest(101, 'x', { macs: [MAC_A, MAC_B] }))], ifaces({ pve: [MAC_A], app: [MAC_B] }))
    expect(matches(m)).toEqual([[guestKey('pve', 101), 'app']])
  })

  it('reads an address two nodes both have as naming neither', () => {
    expect(guestServers([host('pve', guest(101, 'x', { macs: [MAC_A] }))], ifaces({ app: [MAC_A], other: [MAC_A] })).size).toBe(0)
  })

  it('names at most one node for a guest: one whose addresses point at two names none', () => {
    const m = guestServers([host('pve', guest(101, 'x', { macs: [MAC_A, MAC_B] }))], ifaces({ app: [MAC_A], other: [MAC_B] }))
    expect(m.size).toBe(0)
  })

  it('lets a node be claimed by one guest: of several, the only one running', () => {
    // A backup restored with its addresses kept: the copy is stopped.
    const m = guestServers(
      [host('pve', guest(101, 'app', { macs: [MAC_A] }), guest(901, 'app-restored', { status: 'stopped', macs: [MAC_A] }))],
      ifaces({ app: [MAC_A] }),
    )
    expect(matches(m)).toEqual([[guestKey('pve', 101), 'app']])
  })

  it('lets no guest claim a node when several could be it and not exactly one runs', () => {
    const both = (status: string) => guestServers(
      [host('pve', guest(101, 'a', { status, macs: [MAC_A] })), host('pve2', guest(201, 'b', { status, macs: [MAC_A] }))],
      ifaces({ app: [MAC_A] }),
    )
    expect(both('running').size).toBe(0)
    expect(both('stopped').size).toBe(0)
  })

  it('names a stopped guest when it is the only one that could be the node', () => {
    const m = guestServers([host('pve', guest(101, 'app', { status: 'stopped', macs: [MAC_A] }))], ifaces({ app: [MAC_A] }))
    expect(matches(m)).toEqual([[guestKey('pve', 101), 'app']])
  })

  it('breaks a loop of hosts that would each be the other\'s guest, keeping the first in order', () => {
    const m = guestServers(
      [host('a', guest(101, 'b-vm', { macs: [MAC_B] })), host('b', guest(201, 'a-vm', { macs: [MAC_A] }))],
      ifaces({ a: [MAC_A], b: [MAC_B] }),
    )
    expect(matches(m)).toEqual([[guestKey('a', 101), 'b']])
  })

  it('breaks a longer loop too, and the same input gives the same answer', () => {
    const input = [
      host('a', guest(101, 'b-vm', { macs: [MAC_B] })),
      host('b', guest(201, 'c-vm', { macs: [MAC_C] })),
      host('c', guest(301, 'a-vm', { macs: [MAC_A] })),
    ]
    const all = ifaces({ a: [MAC_A], b: [MAC_B], c: [MAC_C] })
    const m = guestServers(input, all)
    expect(matches(m)).toEqual([[guestKey('a', 101), 'b'], [guestKey('b', 201), 'c']])
    expect(matches(guestServers(input, all))).toEqual(matches(m))
  })

  it('follows nesting: a guest of a guest', () => {
    const m = guestServers(
      [host('pve', guest(101, 'inner-pve', { macs: [MAC_A] })), host('inner', guest(201, 'app', { macs: [MAC_B] }))],
      ifaces({ inner: [MAC_A], app: [MAC_B] }),
    )
    expect(matches(m)).toEqual([[guestKey('inner', 201), 'app'], [guestKey('pve', 101), 'inner']])
  })
})
