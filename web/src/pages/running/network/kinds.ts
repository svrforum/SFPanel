import {
  Box, Cctv, Cpu, HardDrive, Monitor, MonitorSmartphone, Network, Printer, Router, Server, Smartphone, Tv, Wifi, type LucideIcon,
} from 'lucide-react'
import { DEVICE_KINDS, type MapDevice } from '@/lib/netmap'

// What a device is drawn as, in the network view's cards and the rack's units.

type LabelKind = (typeof DEVICE_KINDS)[number]
export type Kind = 'gateway' | 'server' | 'vm' | 'lxc' | LabelKind | 'device'

const isLabelKind = (kind: string | undefined): kind is LabelKind => !!kind && (DEVICE_KINDS as readonly string[]).includes(kind)

/** What a device is drawn as: what the network makes it first, then what the operator said, else a device. */
export const kindOf = (d: MapDevice): Kind =>
  d.gateway ? 'gateway' : d.node ? 'server' : d.guest ? d.guest.kind : isLabelKind(d.label?.kind) ? d.label.kind : 'device'

export const ICON: Record<Kind, LucideIcon> = {
  gateway: Router, server: Server, vm: Box, lxc: Box, router: Router, switch: Network, ap: Wifi, nas: HardDrive, pc: Monitor, phone: Smartphone,
  tv: Tv, camera: Cctv, printer: Printer, iot: Cpu, other: MonitorSmartphone, device: MonitorSmartphone,
}
const NETWORK_TONE = 'bg-warning/15 text-warning'
const SERVER_TONE = 'bg-[color-mix(in_srgb,var(--primary)_12%,var(--card))] text-primary'
const PLAIN_TONE = 'bg-secondary text-secondary-foreground'
export const ICON_TONE: Record<Kind, string> = {
  gateway: NETWORK_TONE, router: NETWORK_TONE, switch: NETWORK_TONE, ap: NETWORK_TONE,
  server: SERVER_TONE,
  vm: PLAIN_TONE, lxc: PLAIN_TONE, nas: PLAIN_TONE, pc: PLAIN_TONE, phone: PLAIN_TONE, tv: PLAIN_TONE, camera: PLAIN_TONE, printer: PLAIN_TONE, iot: PLAIN_TONE, other: PLAIN_TONE,
  device: 'bg-secondary text-muted-foreground',
}
