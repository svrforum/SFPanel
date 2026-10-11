import type { InterfaceConfig, NetworkInterfaceInfo } from '@/types/api'

/**
 * The mode and the static addresses the interface config dialog opens with.
 *
 * Both come from the saved netplan config once the dialog has it. Before that,
 * or where the panel cannot read the config, the live interface stands in —
 * its IPv4 addresses only. The kernel also lists link-local (fe80::) and SLAAC
 * addresses, and "Static" used to copy them all into the file, pinning
 * addresses router advertisements hand out anyway. An IPv6 address the
 * operator set by hand is in the saved config.
 *
 * The dialog also opened in "Static" for any interface with an address, a
 * DHCP one included.
 */
export function initialConfig(
  iface: NetworkInterfaceInfo,
  saved?: InterfaceConfig | null,
): { mode: 'dhcp' | 'static'; addresses: string[] } {
  const live = iface.addresses
    .filter((a) => a.family === 'ipv4' || a.family === 'inet')
    .map((a) => `${a.address}/${a.prefix}`)
  if (saved?.dhcp4) return { mode: 'dhcp', addresses: live }
  if (saved && saved.addresses.length > 0) return { mode: 'static', addresses: saved.addresses }
  return { mode: live.length > 0 && iface.type !== 'loopback' ? 'static' : 'dhcp', addresses: live }
}
