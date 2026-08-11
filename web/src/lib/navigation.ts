import {
  Activity,
  Clock,
  Cog,
  Container,
  FileText,
  FolderOpen,
  HardDrive,
  LayoutDashboard,
  Network,
  Package,
  Server,
  Settings,
  Shield,
  Store,
  Terminal,
  type LucideIcon,
} from 'lucide-react'

export interface NavEntry {
  to: string
  labelKey: string
  icon: LucideIcon
  /** Shown as one of the four mobile bottom-bar tabs. */
  bottomNav?: boolean
  /** Bottom-bar label when it differs from the sidebar label (shorter). */
  mobileLabelKey?: string
}

/**
 * The single menu registry. Every navigation surface (desktop sidebar, mobile
 * bottom bar, the More drawer, the cluster tree context menu) derives from
 * this list — previously the same tuples were copy-pasted in four files and
 * had already drifted (inconsistent More-menu subset, mixed label keys).
 */
export const NAV_ITEMS: NavEntry[] = [
  { to: '/dashboard', labelKey: 'layout.nav.dashboard', icon: LayoutDashboard, bottomNav: true, mobileLabelKey: 'layout.mobileNav.dashboard' },
  { to: '/docker', labelKey: 'layout.nav.docker', icon: Container, bottomNav: true, mobileLabelKey: 'layout.mobileNav.docker' },
  { to: '/appstore', labelKey: 'layout.nav.appstore', icon: Store },
  { to: '/files', labelKey: 'layout.nav.files', icon: FolderOpen },
  { to: '/cron', labelKey: 'layout.nav.cron', icon: Clock },
  { to: '/logs', labelKey: 'layout.nav.logs', icon: FileText, bottomNav: true },
  { to: '/processes', labelKey: 'layout.nav.processes', icon: Activity },
  { to: '/services', labelKey: 'layout.nav.services', icon: Cog },
  { to: '/network', labelKey: 'layout.nav.networkVpn', icon: Network },
  { to: '/disk', labelKey: 'layout.nav.disk', icon: HardDrive },
  { to: '/firewall', labelKey: 'layout.nav.firewall', icon: Shield },
  { to: '/packages', labelKey: 'layout.nav.packages', icon: Package },
  { to: '/terminal', labelKey: 'layout.nav.terminal', icon: Terminal, bottomNav: true, mobileLabelKey: 'layout.mobileNav.terminal' },
  { to: '/cluster', labelKey: 'layout.nav.cluster', icon: Server },
  { to: '/settings', labelKey: 'layout.nav.settings', icon: Settings },
]

/** The four mobile bottom-bar tabs — explicit order (terminal before logs). */
const BOTTOM_NAV_ORDER = ['/dashboard', '/docker', '/terminal', '/logs']
export const BOTTOM_NAV_ITEMS = BOTTOM_NAV_ORDER.map(
  (to) => NAV_ITEMS.find((i) => i.to === to)!
)

/**
 * The More drawer: exactly the registry entries NOT in the bottom bar. This is
 * the consistent subset rule the old copy-paste had drifted away from (logs
 * appeared in both surfaces; dashboard/docker/terminal in neither).
 */
export const MORE_MENU_ITEMS = NAV_ITEMS.filter((i) => !i.bottomNav)

/**
 * Per-node menu for the cluster tree context menu: every menu except the
 * cluster page itself, with settings scoped to the node.
 */
export const NODE_MENU_ITEMS = NAV_ITEMS.filter((i) => i.to !== '/cluster').map((i) =>
  i.to === '/settings' ? { ...i, to: '/settings?scope=node' } : i
)
