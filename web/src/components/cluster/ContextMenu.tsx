import { useState } from 'react'
import { NavLink } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import {
  LayoutDashboard, Container, FolderOpen, Clock, FileText, Activity,
  Cog, Network, HardDrive, Shield, Package, Terminal, Store,
  Server, KeyRound, Settings, ChevronDown, ChevronRight, PanelLeftClose, PanelLeftOpen,
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

interface MenuGroup {
  labelKey: string
  items: MenuItem[]
}

const datacenterMenuGroups: MenuGroup[] = [
  {
    labelKey: 'cluster.title',
    items: [
      { to: '/cluster/overview', labelKey: 'cluster.nav.overview', icon: LayoutDashboard },
      { to: '/cluster/nodes', labelKey: 'cluster.nav.nodes', icon: Server },
      { to: '/cluster/tokens', labelKey: 'cluster.nav.tokens', icon: KeyRound },
    ],
  },
  {
    labelKey: 'layout.nav.settings',
    items: [
      { to: '/settings', labelKey: 'layout.nav.settings', icon: Settings, matchEnd: true },
    ],
  },
]

const nodeMenuGroups: MenuGroup[] = [
  {
    labelKey: 'clusterContextMenu.overview',
    items: [
      { to: '/dashboard', labelKey: 'layout.nav.dashboard', icon: LayoutDashboard },
    ],
  },
  {
    labelKey: 'clusterContextMenu.containers',
    items: [
      { to: '/docker', labelKey: 'layout.nav.docker', icon: Container },
      { to: '/appstore', labelKey: 'layout.nav.appstore', icon: Store },
    ],
  },
  {
    labelKey: 'clusterContextMenu.system',
    items: [
      { to: '/files', labelKey: 'layout.nav.files', icon: FolderOpen },
      { to: '/cron', labelKey: 'layout.nav.cron', icon: Clock },
      { to: '/logs', labelKey: 'layout.nav.logs', icon: FileText },
      { to: '/processes', labelKey: 'layout.nav.processes', icon: Activity },
      { to: '/services', labelKey: 'layout.nav.services', icon: Cog },
    ],
  },
  {
    labelKey: 'clusterContextMenu.infrastructure',
    items: [
      { to: '/network', labelKey: 'layout.nav.networkVpn', icon: Network },
      { to: '/disk', labelKey: 'layout.nav.disk', icon: HardDrive },
      { to: '/firewall', labelKey: 'layout.nav.firewall', icon: Shield },
      { to: '/packages', labelKey: 'layout.nav.packages', icon: Package },
    ],
  },
  {
    labelKey: 'clusterContextMenu.tools',
    items: [
      { to: '/terminal', labelKey: 'layout.nav.terminal', icon: Terminal },
      { to: '/settings?scope=node', labelKey: 'layout.nav.settings', icon: Settings },
    ],
  },
]

const COLLAPSED_KEY = 'sfpanel-ctx-menu-collapsed'

function loadCollapsed(): Record<string, boolean> {
  try {
    const saved = localStorage.getItem(COLLAPSED_KEY)
    if (saved) return JSON.parse(saved)
  } catch {}
  return {}
}

export default function ContextMenu({ selection, nodeName, collapsed, onToggleCollapse }: ContextMenuProps) {
  const { t } = useTranslation()
  const isDatacenter = selection.type === 'datacenter'
  const groups = isDatacenter ? datacenterMenuGroups : nodeMenuGroups
  const title = isDatacenter ? t('cluster.title') : nodeName

  const [collapsedGroups, setCollapsedGroups] = useState<Record<string, boolean>>(loadCollapsed)

  const toggleGroup = (key: string) => {
    setCollapsedGroups(prev => {
      const next = { ...prev, [key]: !prev[key] }
      localStorage.setItem(COLLAPSED_KEY, JSON.stringify(next))
      return next
    })
  }

  const groupLabel = (key: string) => {
    const translated = t(key, { defaultValue: '' })
    return translated || key.split('.').pop() || key
  }

  if (collapsed) {
    return (
      <div className="w-[42px] bg-card border-r border-border flex flex-col h-full shrink-0">
        <button
          onClick={onToggleCollapse}
          className="flex items-center justify-center py-3 border-b border-border hover:bg-accent transition-colors"
          title="Expand menu"
        >
          <PanelLeftOpen className="h-4 w-4 text-muted-foreground" />
        </button>
        <nav className="flex-1 min-h-0 overflow-y-auto no-scrollbar flex flex-col items-center gap-0.5 py-2">
          {groups.flatMap(g => g.items).map((item) => (
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
      {/* Section title + collapse button */}
      <div className="px-4 py-3 border-b border-border flex items-start justify-between">
        <div className="min-w-0">
          <p className="text-[10px] font-semibold text-muted-foreground uppercase tracking-wider">
            {isDatacenter ? 'Datacenter' : 'Node'}
          </p>
          <p className="text-[13px] font-semibold text-foreground truncate mt-0.5">{title}</p>
        </div>
        <button onClick={onToggleCollapse} className="p-1 rounded hover:bg-accent transition-colors mt-0.5 shrink-0" title="Collapse menu">
          <PanelLeftClose className="h-3.5 w-3.5 text-muted-foreground" />
        </button>
      </div>

      {/* Grouped menu items */}
      <nav className="flex-1 min-h-0 overflow-y-auto no-scrollbar px-2 py-1.5">
        {groups.map((group) => {
          const key = `${isDatacenter ? 'dc' : 'node'}-${group.labelKey}`
          const isCollapsed = collapsedGroups[key] ?? false

          return (
            <div key={key} className="mb-1">
              {/* Group header — collapsible */}
              <button
                onClick={() => toggleGroup(key)}
                className="w-full flex items-center gap-1 px-2 py-1 text-[10px] font-semibold text-muted-foreground uppercase tracking-wider hover:text-foreground transition-colors"
              >
                {isCollapsed
                  ? <ChevronRight className="h-3 w-3 shrink-0" />
                  : <ChevronDown className="h-3 w-3 shrink-0" />
                }
                {groupLabel(group.labelKey)}
              </button>

              {/* Items */}
              {!isCollapsed && (
                <div className="space-y-0.5">
                  {group.items.map((item) => (
                    <NavLink
                      key={item.to}
                      to={item.to}
                      end={item.to === '/dashboard' || item.matchEnd}
                      className={({ isActive }) =>
                        cn(
                          'flex items-center gap-2.5 px-2.5 py-1.5 rounded-lg text-[12px] font-medium transition-colors',
                          isActive
                            ? 'bg-primary/10 text-primary'
                            : 'text-muted-foreground hover:bg-accent hover:text-foreground'
                        )
                      }
                    >
                      <item.icon className="h-3.5 w-3.5 shrink-0" />
                      <span className="truncate">{t(item.labelKey)}</span>
                    </NavLink>
                  ))}
                </div>
              )}
            </div>
          )
        })}
      </nav>
    </div>
  )
}
