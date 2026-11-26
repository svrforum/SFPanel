import {
  Activity,
  Box,
  Boxes,
  Cable,
  Clock,
  Cog,
  Container,
  Cpu,
  Database,
  FileText,
  FolderOpen,
  Gauge,
  Globe,
  HardDrive,
  Image,
  Layers,
  LayoutDashboard,
  LayoutGrid,
  ListChecks,
  MemoryStick,
  Network,
  Package,
  PieChart,
  Radar,
  Router,
  Server,
  Settings,
  Shield,
  ShieldAlert,
  ShieldCheck,
  Store,
  Terminal,
  Waypoints,
  type LucideIcon,
} from 'lucide-react'

/** Where an entry sits in the menu: the chosen server's pages, the pages about all servers, then settings. */
export type NavGroup = 'server' | 'fleet' | 'tail'
/** What an entry acts on: one server, all of them, or both (settings holds account and server settings). */
export type NavScope = 'server' | 'fleet' | 'mixed'

export interface NavTab {
  to: string
  labelKey: string
  icon: LucideIcon
  /**
   * A screen of its own rather than a part of its entry's page: the browser
   * tab carries the tab's name, and a dashboard shortcut can point at it.
   */
  ownTitle?: boolean
  /**
   * Drawn in the tab strip inside its one Setup menu, which stands where the
   * first such tab would; its address is its own. Lists that are not the
   * strip (the Android app's) show it as a tab.
   */
  fold?: 'setup'
}

export interface NavEntry {
  to: string
  labelKey: string
  icon: LucideIcon
  group: NavGroup
  scope: NavScope
  /** The tab strip at the top of the entry's pages; the first tab is where the entry leads. */
  tabs?: NavTab[]
  /** Only in a cluster: a single server has nothing to show there. */
  clusterOnly?: boolean
  /** Shown as one of the four mobile bottom-bar tabs. */
  bottomNav?: boolean
  /** Bottom-bar label when it differs from the sidebar label (shorter). */
  mobileLabelKey?: string
}

/**
 * The single menu registry. Every navigation surface (desktop sidebar, mobile
 * bottom bar, the More drawer, the tab strips, the dashboard shortcuts, the
 * page title) derives from this list, in this order.
 */
export const NAV_ITEMS: NavEntry[] = [
  { to: '/terminal', labelKey: 'layout.nav.terminal', icon: Terminal, group: 'server', scope: 'server', bottomNav: true, mobileLabelKey: 'layout.mobileNav.terminal' },
  {
    to: '/dashboard', labelKey: 'layout.nav.dashboard', icon: LayoutDashboard, group: 'server', scope: 'server', bottomNav: true, mobileLabelKey: 'layout.mobileNav.dashboard',
    tabs: [
      { to: '/dashboard', labelKey: 'layout.tabs.summary', icon: LayoutDashboard },
      { to: '/running', labelKey: 'layout.tabs.runningHere', icon: Boxes, ownTitle: true },
    ],
  },
  {
    to: '/docker', labelKey: 'layout.nav.docker', icon: Container, group: 'server', scope: 'server', bottomNav: true, mobileLabelKey: 'layout.mobileNav.docker',
    tabs: [
      { to: '/docker/stacks', labelKey: 'docker.sidebar.stacks', icon: Layers },
      { to: '/docker/containers', labelKey: 'docker.sidebar.containers', icon: Box },
      { to: '/docker/images', labelKey: 'docker.sidebar.images', icon: Image },
      { to: '/docker/volumes', labelKey: 'docker.sidebar.volumes', icon: HardDrive },
      { to: '/docker/networks', labelKey: 'docker.sidebar.networks', icon: Network },
      { to: '/appstore', labelKey: 'layout.nav.appstore', icon: Store, ownTitle: true },
    ],
  },
  { to: '/files', labelKey: 'layout.nav.files', icon: FolderOpen, group: 'server', scope: 'server' },
  { to: '/logs', labelKey: 'layout.nav.logs', icon: FileText, group: 'server', scope: 'server', bottomNav: true },
  {
    to: '/services', labelKey: 'layout.nav.system', icon: Cpu, group: 'server', scope: 'server',
    tabs: [
      { to: '/services', labelKey: 'layout.nav.services', icon: Cog, ownTitle: true },
      { to: '/processes', labelKey: 'layout.nav.processes', icon: Activity, ownTitle: true },
      { to: '/cron', labelKey: 'layout.nav.cron', icon: Clock, ownTitle: true },
      { to: '/packages', labelKey: 'layout.nav.packages', icon: Package, ownTitle: true },
      { to: '/tuning', labelKey: 'layout.nav.tuning', icon: Gauge, ownTitle: true },
    ],
  },
  {
    to: '/network', labelKey: 'layout.nav.networkVpn', icon: Network, group: 'server', scope: 'server',
    tabs: [
      { to: '/network/interfaces', labelKey: 'network.sidebar.interfaces', icon: Cable },
      { to: '/network/wireguard', labelKey: 'network.sidebar.wireguard', icon: Shield },
      { to: '/network/tailscale', labelKey: 'network.sidebar.tailscale', icon: Globe },
    ],
  },
  {
    to: '/firewall', labelKey: 'layout.nav.firewall', icon: Shield, group: 'server', scope: 'server',
    tabs: [
      { to: '/firewall/rules', labelKey: 'firewall.tabs.rules', icon: ShieldCheck },
      { to: '/firewall/ports', labelKey: 'firewall.tabs.ports', icon: Network },
      { to: '/firewall/fail2ban', labelKey: 'firewall.tabs.fail2ban', icon: ShieldAlert },
      { to: '/firewall/docker', labelKey: 'firewall.tabs.docker', icon: Container },
    ],
  },
  {
    to: '/disk', labelKey: 'layout.nav.disk', icon: HardDrive, group: 'server', scope: 'server',
    tabs: [
      { to: '/disk/overview', labelKey: 'disk.tabs.overview', icon: LayoutGrid },
      { to: '/disk/usage', labelKey: 'disk.tabs.usage', icon: PieChart },
      { to: '/disk/filesystems', labelKey: 'disk.tabs.filesystems', icon: Database },
      { to: '/disk/swap', labelKey: 'disk.tabs.swap', icon: MemoryStick },
      { to: '/disk/network-shares', labelKey: 'disk.tabs.shares', icon: Network },
      { to: '/disk/partitions', labelKey: 'disk.tabs.partitions', icon: HardDrive, fold: 'setup' },
      { to: '/disk/lvm', labelKey: 'disk.tabs.lvm', icon: Layers, fold: 'setup' },
      { to: '/disk/raid', labelKey: 'disk.tabs.raid', icon: Server, fold: 'setup' },
    ],
  },
  { to: '/cluster/running', labelKey: 'layout.nav.running', icon: Boxes, group: 'fleet', scope: 'fleet', clusterOnly: true },
  {
    to: '/map', labelKey: 'layout.nav.map', icon: Waypoints, group: 'fleet', scope: 'fleet',
    tabs: [
      { to: '/map', labelKey: 'map.tabs.devices', icon: Router },
      { to: '/map/rack', labelKey: 'map.tabs.rack', icon: Server },
    ],
  },
  { to: '/cluster/tasks', labelKey: 'layout.nav.tasks', icon: ListChecks, group: 'fleet', scope: 'fleet', clusterOnly: true },
  { to: '/agents', labelKey: 'layout.nav.agents', icon: Radar, group: 'fleet', scope: 'fleet', clusterOnly: true },
  {
    to: '/cluster', labelKey: 'layout.nav.cluster', icon: Server, group: 'fleet', scope: 'fleet',
    tabs: [
      { to: '/cluster/overview', labelKey: 'cluster.nav.overview', icon: LayoutDashboard },
      { to: '/cluster/nodes', labelKey: 'cluster.nav.nodes', icon: Server },
      { to: '/cluster/security', labelKey: 'cluster.nav.security', icon: ShieldCheck },
    ],
  },
  { to: '/settings', labelKey: 'layout.nav.settings', icon: Settings, group: 'tail', scope: 'mixed' },
]

/** The menu for the panel's mode: a single server has no cluster-only entries. */
export function navFor(cluster: boolean): NavEntry[] {
  return cluster ? NAV_ITEMS : NAV_ITEMS.filter((e) => !e.clusterOnly)
}

/** Whether a path is the page itself or one below it — /services, not /servicesx. */
export function within(pathname: string, to: string): boolean {
  return pathname === to || pathname.startsWith(to + '/')
}

export interface NavOwner {
  entry: NavEntry
  tab?: NavTab
}

/**
 * The menu entry, and its tab, that a path belongs to: the longest entry or
 * tab address the path is within, the tab when an entry and its tab share one.
 * /cluster/tasks/runs/7 is group tasks', not the cluster's; null for a path no
 * entry claims.
 */
export function navOwner(pathname: string): NavOwner | null {
  let best: NavOwner | null = null
  let length = -1
  for (const entry of NAV_ITEMS) {
    if (within(pathname, entry.to) && entry.to.length > length) {
      best = { entry }
      length = entry.to.length
    }
    for (const tab of entry.tabs ?? []) {
      if (within(pathname, tab.to) && tab.to.length >= length) {
        best = { entry, tab }
        length = tab.to.length
      }
    }
  }
  return best
}

/** The label key for a page's title: its tab's when that tab is a screen of its own, else its entry's; null for a path no entry claims. */
export function pageTitleKey(pathname: string): string | null {
  const owner = navOwner(pathname)
  if (!owner) return null
  return owner.tab?.ownTitle ? owner.tab.labelKey : owner.entry.labelKey
}

/** An entry's tab strip, in the shape SubNavTabs draws (label is the i18n key). */
export function navTabs(entryTo: string): { to: string; icon: LucideIcon; label: string; fold?: 'setup' }[] {
  const entry = NAV_ITEMS.find((e) => e.to === entryTo)
  return (entry?.tabs ?? []).map((tab) => ({ to: tab.to, icon: tab.icon, label: tab.labelKey, fold: tab.fold }))
}

/**
 * Pages whose body is a full-bleed console with its own mobile key bar. The
 * app shell drops its padding and hides the bottom nav there, or the bottom
 * bar would sit on top of the key bar. Exact match — /terminal/x is not one.
 */
export const TERMINAL_ROUTES = ['/terminal']

export function isTerminalRoute(pathname: string): boolean {
  return TERMINAL_ROUTES.includes(pathname)
}

/** The four mobile bottom-bar tabs — explicit order (terminal before logs). */
const BOTTOM_NAV_ORDER = ['/dashboard', '/docker', '/terminal', '/logs']
export const BOTTOM_NAV_ITEMS = BOTTOM_NAV_ORDER.map(
  (to) => NAV_ITEMS.find((i) => i.to === to)!
)

export interface NavShortcut {
  to: string
  labelKey: string
  icon: LucideIcon
}

/**
 * The pages a dashboard shortcut can open, in menu order: every entry, and
 * every tab that is a screen of its own — services, processes, scheduled tasks
 * and packages were menu entries before they were tabs, and a shortcut saved
 * to one of them must still open it. An entry whose address is its own tab's
 * is listed once; the dashboard does not link to itself.
 */
export function shortcutCandidates(cluster: boolean): NavShortcut[] {
  return navFor(cluster).flatMap((entry) => {
    const own = (entry.tabs ?? []).filter((tab) => tab.ownTitle)
    const self = entry.to !== '/dashboard' && !own.some((tab) => tab.to === entry.to) ? [entry] : []
    return [...self, ...own].map(({ to, labelKey, icon }) => ({ to, labelKey, icon }))
  })
}

export interface MoreGroup {
  group: NavGroup
  items: NavEntry[]
}

/** The More drawer: everything the bottom bar does not carry, by group, without empty groups. */
export function moreMenuGroups(cluster: boolean): MoreGroup[] {
  const rest = navFor(cluster).filter((e) => !e.bottomNav)
  return (['server', 'fleet', 'tail'] as const)
    .map((group) => ({ group, items: rest.filter((e) => e.group === group) }))
    .filter((g) => g.items.length > 0)
}

/** The tab title: the page, the server it acts on when there is one, then the product; the product alone without a page. */
export function documentTitle(page: string | null, server: string | null): string {
  if (!page) return 'SFPanel'
  return server ? `${page} · ${server} · SFPanel` : `${page} · SFPanel`
}
