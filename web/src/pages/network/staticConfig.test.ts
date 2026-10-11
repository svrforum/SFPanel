import { describe, expect, it } from 'vitest'
import type { InterfaceConfig, NetworkInterfaceInfo } from '@/types/api'
import { initialConfig } from './staticConfig'

const iface = (addresses: NetworkInterfaceInfo['addresses']) =>
  ({ name: 'ens3', type: 'ethernet', addresses } as NetworkInterfaceInfo)

// What the kernel lists for a DHCP interface on a network with SLAAC.
const leased = iface([
  { address: '192.0.2.15', prefix: 24, family: 'ipv4' },
  { address: '2001:db8::5054:ff:fe12:3456', prefix: 64, family: 'ipv6' },
  { address: 'fe80::5054:ff:fe12:3456', prefix: 64, family: 'ipv6' },
])
const saved = (cfg: Partial<InterfaceConfig>): InterfaceConfig => ({ dhcp4: false, dhcp6: false, addresses: [], dns: [], ...cfg })

describe('initialConfig', () => {
  it('opens a DHCP interface in DHCP, with only its IPv4 ready for Static', () => {
    expect(initialConfig(leased, saved({ dhcp4: true }))).toEqual({ mode: 'dhcp', addresses: ['192.0.2.15/24'] })
  })

  it('opens a static interface with the addresses it saved, IPv6 included', () => {
    const cfg = saved({ addresses: ['192.0.2.15/24', '2001:db8::15/64'] })
    expect(initialConfig(leased, cfg)).toEqual({ mode: 'static', addresses: ['192.0.2.15/24', '2001:db8::15/64'] })
  })

  it('falls back to the live IPv4 when the config cannot be read', () => {
    expect(initialConfig(leased, null)).toEqual({ mode: 'static', addresses: ['192.0.2.15/24'] })
    expect(initialConfig(iface([]), null)).toEqual({ mode: 'dhcp', addresses: [] })
  })
})
