import { describe, expect, it } from 'vitest'
import type { LanRack, LanRackItem, NetworkInterfaceInfo } from '@/types/api'
import { fits, gridRows, mergeRacks, minUnits, move, newId, nodePorts, place, placed, rackDoc, rackProblem, remove, resize, roomAt, slots, speedLabel, type Rack } from './rack'

const item = (id: string, u: number, height = 1, extra: Partial<LanRackItem> = {}): LanRackItem => ({ id, u, height, kind: 'text', text: id, ...extra })
const rack = (items: LanRackItem[], units = 12): Rack => ({ id: 'r', name: 'Rack', units, items, updatedAt: 0 })
const stored = (id: string, at: string, extra: Partial<LanRack['doc']> = {}): LanRack => ({ id, updated_at: at, doc: { name: id, units: 12, deleted: false, items: [], ...extra } })

describe('the nodes\' racks as one', () => {
  it('takes the copy written last, whichever node has it', () => {
    const racks = mergeRacks([
      [stored('a', '2026-11-22T10:00:00Z', { name: 'old' })],
      [stored('a', '2026-11-22T11:00:00.500Z', { name: 'new', items: [item('x', 1)] }), stored('b', '2026-11-22T09:00:00Z', { name: 'B' })],
      [],
    ])
    expect(racks.map((r) => [r.id, r.name, r.items.length])).toEqual([['b', 'B', 0], ['a', 'new', 1]])
    expect(racks[1].updatedAt).toBe(Date.parse('2026-11-22T11:00:00.500Z'))
  })

  it('has no rack where the last word was to delete it, and has it where it was made again after', () => {
    expect(mergeRacks([[stored('a', '2026-11-22T10:00:00Z')], [stored('a', '2026-11-22T11:00:00Z', { deleted: true })]])).toEqual([])
    expect(mergeRacks([[stored('a', '2026-11-22T12:00:00Z')], [stored('a', '2026-11-22T11:00:00Z', { deleted: true })]]).map((r) => r.id)).toEqual(['a'])
  })

  it('orders racks by name, numbers as numbers', () => {
    const racks = mergeRacks([[stored('1', 't', { name: 'Rack 10' }), stored('2', 't', { name: 'Rack 2' }), stored('3', 't', { name: 'Closet' })]])
    expect(racks.map((r) => r.name)).toEqual(['Closet', 'Rack 2', 'Rack 10'])
  })

  it('reads a rack no taller than a rack can be, and never without a unit', () => {
    const racks = mergeRacks([[stored('a', 't', { units: 400 }), stored('b', 't', { units: 0 })]])
    expect(racks.map((r) => r.units)).toEqual([60, 1])
  })

  it('writes a rack as it is, and a deletion as a mark with nothing in it', () => {
    const r = { ...rack([item('x', 2, 2)]), name: '  Main  ' }
    expect(rackDoc(r)).toEqual({ name: 'Main', units: 12, deleted: false, items: [item('x', 2, 2)] })
    expect(rackDoc(r, true)).toEqual({ name: '', units: 12, deleted: true, items: [] })
  })
})

describe('what fits where', () => {
  const r = rack([item('a', 2, 2), item('b', 6, 1)])

  it('is inside the rack and on no unit something else takes', () => {
    expect(fits(r, 1, 1)).toBe(true)
    expect(fits(r, 1, 2)).toBe(false)
    expect(fits(r, 3, 1)).toBe(false)
    expect(fits(r, 4, 2)).toBe(true)
    expect(fits(r, 4, 3)).toBe(false)
    expect(fits(r, 12, 1)).toBe(true)
    expect(fits(r, 12, 2)).toBe(false)
    expect(fits(r, 0, 1)).toBe(false)
    expect(fits(r, 7, 9)).toBe(false)
    expect(fits(r, 1.5, 1)).toBe(false)
  })

  it('is no taller than eight units, however much room there is', () => {
    const tall = rack([item('a', 1, 1)], 40)
    expect(fits(tall, 5, 8)).toBe(true)
    expect(fits(tall, 5, 9)).toBe(false)
    expect(resize(tall, 'a', 8)?.items[0].height).toBe(8)
    expect(resize(tall, 'a', 9)).toBeNull()
  })

  it('does not count the thing itself, for moving it', () => {
    expect(fits(r, 3, 2, 'a')).toBe(true)
    expect(fits(r, 5, 2, 'a')).toBe(false)
  })

  it('says how tall a thing put at a unit could be', () => {
    expect(roomAt(r, 1)).toBe(1)
    expect(roomAt(r, 2)).toBe(0)
    expect(roomAt(r, 4)).toBe(2)
    expect(roomAt(r, 7)).toBe(6)
    expect(roomAt(rack([], 20), 1)).toBe(8)
    expect(roomAt(r, 2, 'a')).toBe(4)
  })
})

describe('moves in a rack', () => {
  const r = rack([item('a', 2, 2), item('b', 6, 1)])

  it('places a thing where it fits, and nothing where it does not', () => {
    expect(place(r, item('c', 4, 2))?.items.map((i) => i.id)).toEqual(['a', 'b', 'c'])
    expect(place(r, item('c', 3, 1))).toBeNull()
    expect(place(r, item('a', 9, 1))).toBeNull()
    expect(r.items).toHaveLength(2)
  })

  it('moves a thing a unit up or down, and not into another or out of the rack', () => {
    expect(move(r, 'a', 1)?.items[0].u).toBe(3)
    expect(move(r, 'a', -1)?.items[0].u).toBe(1)
    expect(move(move(r, 'a', -1)!, 'a', -1)).toBeNull()
    expect(move(r, 'b', -2)?.items[1].u).toBe(4)
    expect(move(r, 'b', -3)).toBeNull()
    expect(move(r, 'b', 6)?.items[1].u).toBe(12)
    expect(move(r, 'b', 7)).toBeNull()
    expect(move(r, 'nobody', 1)).toBeNull()
  })

  it('makes a thing taller upward while there is room', () => {
    expect(resize(r, 'a', 3)?.items[0].height).toBe(3)
    expect(resize(r, 'a', 4)?.items[0].height).toBe(4)
    expect(resize(r, 'a', 5)).toBeNull()
    expect(resize(r, 'b', 8)).toBeNull()
    expect(resize(r, 'a', 1)?.items[0].height).toBe(1)
  })

  it('takes a thing out', () => {
    expect(remove(r, 'a').items.map((i) => i.id)).toEqual(['b'])
    expect(remove(r, 'nobody').items).toHaveLength(2)
  })
})

describe('a rack as it is drawn', () => {
  it('is read from the top down: each thing once where it starts, and each free unit', () => {
    const r = rack([item('a', 2, 2), item('b', 5, 1)], 6)
    expect(slots(r).map((s) => (s.kind === 'item' ? `${s.item.id}@${s.top}` : `free ${s.unit}`))).toEqual(['free 6', 'b@5', 'free 4', 'a@3', 'free 1'])
  })

  it('draws a thing that reaches out of a rack made shorter as far as the rack goes', () => {
    const r = rack([item('a', 3, 4)], 4)
    expect(slots(r).map((s) => (s.kind === 'item' ? `${s.item.id}@${s.top}` : `free ${s.unit}`))).toEqual(['a@4', 'free 2', 'free 1'])
    expect(gridRows(4, r.items[0])).toEqual({ start: 1, span: 2 })
  })

  it('puts a thing on the rows it takes, counted from the top', () => {
    expect(gridRows(12, item('a', 1, 1))).toEqual({ start: 12, span: 1 })
    expect(gridRows(12, item('a', 11, 2))).toEqual({ start: 1, span: 2 })
    expect(gridRows(12, item('a', 3, 4))).toEqual({ start: 7, span: 4 })
  })

  it('cannot be made shorter than what is in it', () => {
    expect(minUnits(rack([item('a', 2, 2), item('b', 9, 3)]))).toBe(11)
    expect(minUnits(rack([]))).toBe(1)
  })
})

describe('what is in the racks already', () => {
  it('lists the nodes and the MACs, whatever case a MAC was written in', () => {
    const got = placed([
      rack([item('a', 1, 1, { kind: 'node', node: 'n1' }), item('b', 2, 1, { kind: 'device', mac: '02:42:AC:14:1E:29' })]),
      rack([item('c', 1, 1, { kind: 'node', node: 'n2' }), item('d', 2, 1)]),
    ])
    expect([...got.nodes]).toEqual(['n1', 'n2'])
    expect([...got.macs]).toEqual(['02:42:ac:14:1e:29'])
  })
})

describe('making a rack', () => {
  it('wants a name, a height a rack can have, and room for one more', () => {
    expect(rackProblem('Main', 12, 0)).toBeNull()
    expect(rackProblem('   ', 12, 0)).toBe('name')
    expect(rackProblem('x'.repeat(65), 12, 0)).toBe('name')
    expect(rackProblem('랙'.repeat(64), 12, 0)).toBeNull()
    expect(rackProblem('Main', 0, 0)).toBe('units')
    expect(rackProblem('Main', 61, 0)).toBe('units')
    expect(rackProblem('Main', 4.5, 0)).toBe('units')
    expect(rackProblem('Main', 12, 32)).toBe('racks')
  })

  it('names new things with what the server takes for an id', () => {
    const ids = new Set(Array.from({ length: 50 }, () => newId()))
    expect(ids.size).toBe(50)
    for (const id of ids) expect(id).toMatch(/^[A-Za-z0-9_-]{8,64}$/)
  })
})

describe('a node\'s ports', () => {
  const nic = (name: string, extra: Partial<NetworkInterfaceInfo> = {}): NetworkInterfaceInfo => ({
    name, type: 'ethernet', state: 'up', mac_address: '', mtu: 1500, speed: 1000, addresses: [], is_default: false, driver: '',
    tx_bytes: 0, rx_bytes: 0, tx_packets: 0, rx_packets: 0, tx_errors: 0, rx_errors: 0, kind: '', ...extra,
  })

  it('are the sockets on the machine, by name, with their link and speed', () => {
    const ports = nodePorts([
      nic('lo', { type: 'loopback' }),
      nic('enp3s0', { speed: 2500 }),
      nic('enp2s0', { state: 'down', speed: -1 }),
      nic('eno10', { speed: 10000 }),
      nic('vmbr0', { type: 'bridge', kind: 'bridge' }),
      nic('bond0', { type: 'bond', kind: 'bond' }),
      nic('enp2s0.20', { kind: 'vlan' }),
      nic('veth1a2b', { kind: 'veth' }),
      nic('docker0', { type: 'bridge', kind: 'bridge' }),
      nic('tailscale0', { type: 'virtual', kind: 'tun' }),
      nic('wlan0', { type: 'wireless' }),
    ])
    expect(ports).toEqual([
      { name: 'eno10', up: true, speed: '10G' },
      { name: 'enp2s0', up: false, speed: '' },
      { name: 'enp3s0', up: true, speed: '2.5G' },
    ])
  })

  it('are told by name on a node that does not say what kind a link is', () => {
    const old = (name: string) => { const n = nic(name); delete n.kind; return n }
    expect(nodePorts([old('eth0'), old('veth9'), old('br-0123456789ab'), old('tap101i0'), old('eth0.5')]).map((p) => p.name)).toEqual(['eth0'])
  })

  it('say a speed as it is printed on a switch', () => {
    expect([speedLabel(10), speedLabel(100), speedLabel(1000), speedLabel(2500), speedLabel(10000), speedLabel(0), speedLabel(-1)]).toEqual(['10M', '100M', '1G', '2.5G', '10G', '', ''])
  })
})
