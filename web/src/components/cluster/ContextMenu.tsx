import { NavLink } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import {
  LayoutDashboard, Container, FolderOpen, Clock, FileText, Activity,
  Cog, Network, HardDrive, Shield, Package, Terminal, Store,
  Server, KeyRound, Settings, PanelLeftClose, PanelLeftOpen,
} from 'lucide-react'
import { cn } from '@/lib/utils'
import type { TreeSelection } from './TreePanel'

interface ContextMenuProps {
  selection: TreeSelection
  nodeName: string
  collapsed: boolean
  onToggleCollapse: () => void
}

interface MenuItem {
  to: string
  labelKey: string
  icon: React.ElementType
  matchEnd?: boolean
}

const datacenterMenuItems: MenuItem[] = [
  { to: '/cluster/overview', labelKey: 'cluster.nav.overview', icon: LayoutDashboard },
  { to: '/cluster/nodes', labelKey: 'cluster.nav.nodes', icon: Server },
  { to: '/cluster/tokens', labelKey: 'cluster.nav.tokens', icon: KeyRound },
  { to: '/settings', labelKey: 'layout.nav.settings', icon: Settings, matchEnd: true },
]

const nodeMenuItems: MenuItem[] = [
  { to: '/dashboard', labelKey: 'layout.nav.dashboard', icon: LayoutDashboard },
  { to: '/docker', labelKey: 'layout.nav.docker', icon: Container },
  { to: '/appstore', labelKey: 'layout.nav.appstore', icon: Store },
  { to: '/files', labelKey: 'layout.nav.files', icon: FolderOpen },
  { to: '/cron', labelKey: 'layout.nav.cron', icon: Clock },
  { to: '/logs', labelKey: 'layout.nav.logs', icon: FileText },
  { to: '/processes', labelKey: 'layout.nav.processes', icon: Activity },
  { to: '/services', labelKey: 'layout.nav.services', icon: Cog },
  { to: '/network', labelKey: 'layout.nav.networkVpn', icon: Network },
  { to: '/disk', labelKey: 'layout.nav.disk', icon: HardDrive },
  { to: '/firewall', labelKey: 'layout.nav.firewall', icon: Shield },
  { to: '/packages', labelKey: 'layout.nav.packages', icon: Package },
  { to: '/terminal', labelKey: 'layout.nav.terminal', icon: Terminal },
  { to: '/settings?scope=node', labelKey: 'layout.nav.settings', icon: Settings },
]

export default function ContextMenu({ selection, nodeName, collapsed, onToggleCollapse }: ContextMenuProps) {
  const { t } = useTranslation()
  const isDatacenter = selection.type === 'datacenter'
  const items = isDatacenter ? datacenterMenuItems : nodeMenuItems
  const title = isDatacenter ? t('cluster.title') : nodeName

  if (collapsed) {
    return (
      <div className="w-[42px] bg-card border-r border-border flex flex-col h-full shrink-0">
        <button
          onClick={onToggleCollapse}
          className="flex items-center justify-center py-3 border-b border-border hover:bg-accent transition-colors"
          title="Expand menu"
        >
          <PanelLeftOpen className="h-4 w-4 text-foreground/60" />
        </button>
        <nav className="flex-1 min-h-0 overflow-y-auto no-scrollbar flex flex-col items-center gap-0.5 py-2">
          {items.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.to === '/dashboard' || item.matchEnd}
              title={t(item.labelKey)}
              className={({ isActive }) =>
                cn(
                  'w-8 h-8 rounded-lg flex items-center justify-center transition-colors',
                  isActive ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:bg-accent'
                )
              }
            >
              <item.icon className="h-4 w-4" />
            </NavLink>
          ))}
        </nav>
      </div>
    )
  }

  return (
    <div className="w-[180px] bg-card border-r border-border flex flex-col h-full shrink-0">
      {/* Header */}
      <div className="px-4 py-3 border-b border-border flex items-start justify-between">
        <div className="min-w-0">
          <p className="text-[10px] font-semibold text-muted-foreground uppercase tracking-wider">
            {isDatacenter ? 'Datacenter' : 'Node'}
          </p>
          <p className="text-[13px] font-semibold text-foreground truncate mt-0.5">{title}</p>
        </div>
        <button onClick={onToggleCollapse} className="p-1.5 rounded-lg hover:bg-accent border border-border transition-colors mt-0.5 shrink-0" title="Collapse menu">
          <PanelLeftClose className="h-4 w-4 text-foreground/60" />
        </button>
      </div>

      {/* Flat menu items */}
      <nav className="flex-1 min-h-0 overflow-y-auto no-scrollbar px-2 py-2 space-y-0.5">
        {items.map((item) => (
          <NavLink
            key={item.to}
            to={item.to}
            end={item.to === '/dashboard' || item.matchEnd}
            className={({ isActive }) =>
              cn(
                'flex items-center gap-2.5 px-2.5 py-2 rounded-lg text-[12px] font-medium transition-colors',
                isActive
                  ? 'bg-primary/10 text-primary'
                  : 'text-muted-foreground hover:bg-accent hover:text-foreground'
              )
            }
          >
            <item.icon className="h-4 w-4 shrink-0" />
            <span className="truncate">{t(item.labelKey)}</span>
          </NavLink>
        ))}
      </nav>
    </div>
  )
}
