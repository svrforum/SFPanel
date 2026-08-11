import { KeyRound, Layers, LayoutDashboard, Server, Settings, type LucideIcon } from 'lucide-react'

export interface DatacenterMenuItem {
  to: string
  labelKey: string
  icon: LucideIcon
  matchEnd?: boolean
}

/**
 * Single definition of the datacenter-scope menu. The cluster page tabs
 * (pages/Cluster.tsx) and the cluster-mode sidebar (ContextMenu) used to
 * carry separate copies and had already drifted: the sidebar was missing the
 * Stacks entry, so cluster-wide Docker was reachable only via the page tabs.
 */
export const CLUSTER_TAB_ITEMS: DatacenterMenuItem[] = [
  { to: '/cluster/overview', labelKey: 'cluster.nav.overview', icon: LayoutDashboard },
  { to: '/cluster/nodes', labelKey: 'cluster.nav.nodes', icon: Server },
  { to: '/cluster/stacks', labelKey: 'cluster.nav.stacks', icon: Layers },
  { to: '/cluster/tokens', labelKey: 'cluster.nav.tokens', icon: KeyRound },
]

/** The sidebar additionally links to datacenter-scoped settings. */
export const DATACENTER_MENU_ITEMS: DatacenterMenuItem[] = [
  ...CLUSTER_TAB_ITEMS,
  { to: '/settings', labelKey: 'layout.nav.settings', icon: Settings, matchEnd: true },
]
