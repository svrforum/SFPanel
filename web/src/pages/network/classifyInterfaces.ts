import type { NetworkInterfaceInfo } from '@/types/api'

export interface InterfaceGroups {
  physical: NetworkInterfaceInfo[]
  bridges: NetworkInterfaceInfo[]
  bonds: NetworkInterfaceInfo[]
  vlans: NetworkInterfaceInfo[]
  virtual: NetworkInterfaceInfo[]
  loopback: NetworkInterfaceInfo[]
  // A Proxmox host's guest plumbing: VM taps, the firewall bridge and veth
  // pair pve-firewall puts in front of each, and container veths.
  guests: NetworkInterfaceInfo[]
  docker: NetworkInterfaceInfo[]
}

// tap100i0, fwbr100i0, fwpr100p0, fwln100i0: VM 100's first NIC.
const PVE_GUEST_LINK = /^(tap\d+i\d+|fwbr\d+i\d+|fwpr\d+p\d+|fwln\d+i\d+)$/
const GUEST_BRIDGE = /^(vmbr|fwbr)/
const DOCKER_BRIDGE = /^(docker\d+|br-[0-9a-f]{12})$/

// Groups the interfaces by what the kernel says each link is (kind) and whose
// port it is (master). Names and speeds misled: on Proxmox every guest NIC's
// tap, firewall bridge and veth pair sat among the host's own interfaces, a
// veth reports 10 Gbps, and a container's veth is not Docker's.
export function classifyInterfaces(interfaces: NetworkInterfaceInfo[]): InterfaceGroups {
  const groups: InterfaceGroups = {
    physical: [], bridges: [], bonds: [], vlans: [], virtual: [], loopback: [], guests: [], docker: [],
  }
  for (const iface of interfaces) {
    groups[groupOf(iface)].push(iface)
  }
  return groups
}

function groupOf(iface: NetworkInterfaceInfo): keyof InterfaceGroups {
  if (iface.type === 'loopback' || iface.name === 'lo') return 'loopback'
  // An older node does not send the kind: group by name, as it always was.
  if (iface.kind === undefined) {
    if (iface.name.startsWith('docker') || iface.name.startsWith('br-') || iface.name.startsWith('veth')) return 'docker'
    if (/^(eth|en|wl|ww)/.test(iface.name) || iface.bond_info || iface.type === 'bond' || iface.speed > 0) return 'physical'
    return 'virtual'
  }
  const master = iface.master ?? ''
  if (DOCKER_BRIDGE.test(iface.name) || DOCKER_BRIDGE.test(master)) return 'docker'
  if (PVE_GUEST_LINK.test(iface.name) || ((iface.kind === 'veth' || iface.kind === 'tun') && GUEST_BRIDGE.test(master))) {
    return 'guests'
  }
  if (iface.kind === 'bond' || iface.bond_info) return 'bonds'
  if (iface.kind === 'bridge') return 'bridges'
  if (iface.kind === 'vlan') return 'vlans'
  // A NIC has no kind of its own, and a device behind it.
  if (iface.kind === '' && iface.type === 'ethernet') return 'physical'
  return 'virtual'
}
