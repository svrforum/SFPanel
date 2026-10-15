import { describe, expect, it } from 'vitest'
import type { NetworkInterfaceInfo } from '@/types/api'
import { classifyInterfaces } from './classifyInterfaces'

const link = (name: string, type: string, kind: string | undefined, master?: string, speed = -1) =>
  ({ name, type, kind, master, speed, addresses: [] } as unknown as NetworkInterfaceInfo)

const names = (list: NetworkInterfaceInfo[]) => list.map((i) => i.name)

describe('classifyInterfaces', () => {
  // A Proxmox node: the NIC is a port of vmbr0; VM 100's NIC has the firewall
  // on (tap into fwbr, fwpr/fwln veth pair to vmbr0); container 101's veth;
  // veths and bridges report a speed of 10000.
  it('puts a Proxmox node\'s guest plumbing in its own group', () => {
    const groups = classifyInterfaces([
      link('lo', 'loopback', ''),
      link('enp1s0', 'ethernet', '', 'vmbr0', 1000),
      link('vmbr0', 'bridge', 'bridge', undefined, 1000),
      link('tap100i0', 'virtual', 'tun', 'fwbr100i0', 10),
      link('fwbr100i0', 'bridge', 'bridge', undefined, 10000),
      link('fwpr100p0', 'virtual', 'veth', 'vmbr0', 10000),
      link('fwln100i0', 'virtual', 'veth', 'fwbr100i0', 10000),
      link('veth101i0', 'virtual', 'veth', 'vmbr0', 10000),
      link('bond0', 'bond', 'bond'),
      link('vmbr0.20', 'vlan', 'vlan'),
      link('nic1', 'ethernet', ''),
      link('wg0', 'virtual', 'wireguard'),
    ])
    expect(names(groups.physical)).toEqual(['enp1s0', 'nic1'])
    expect(names(groups.bridges)).toEqual(['vmbr0'])
    expect(names(groups.bonds)).toEqual(['bond0'])
    expect(names(groups.vlans)).toEqual(['vmbr0.20'])
    expect(names(groups.guests)).toEqual(['tap100i0', 'fwbr100i0', 'fwpr100p0', 'fwln100i0', 'veth101i0'])
    expect(names(groups.virtual)).toEqual(['wg0'])
    expect(names(groups.loopback)).toEqual(['lo'])
    expect(groups.docker).toEqual([])
  })

  it('keeps Docker\'s bridges and their veths as Docker', () => {
    const groups = classifyInterfaces([
      link('docker0', 'bridge', 'bridge'),
      link('br-0873e8f86664', 'bridge', 'bridge'),
      link('veth0e83f2b', 'virtual', 'veth', 'br-0873e8f86664', 10000),
      link('tailscale0', 'virtual', 'tun'),
      link('enp2s0', 'ethernet', '', undefined, 1000),
    ])
    expect(names(groups.docker)).toEqual(['docker0', 'br-0873e8f86664', 'veth0e83f2b'])
    expect(names(groups.virtual)).toEqual(['tailscale0'])
    expect(names(groups.physical)).toEqual(['enp2s0'])
  })

  // An older node sends no kind: grouped by name, as before.
  it('groups an older node\'s interfaces by name', () => {
    const groups = classifyInterfaces([
      link('eth0', 'ethernet', undefined, undefined, 1000),
      link('veth1234', 'virtual', undefined),
      link('wg0', 'virtual', undefined),
    ])
    expect(names(groups.physical)).toEqual(['eth0'])
    expect(names(groups.docker)).toEqual(['veth1234'])
    expect(names(groups.virtual)).toEqual(['wg0'])
  })
})
