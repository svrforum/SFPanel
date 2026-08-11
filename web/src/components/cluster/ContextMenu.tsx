import { NavLink } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { PanelLeftClose, PanelLeftOpen } from 'lucide-react'
import { cn } from '@/lib/utils'
import { NODE_MENU_ITEMS } from '@/lib/navigation'
import { DATACENTER_MENU_ITEMS } from './datacenterMenu'
import type { TreeSelection } from './TreePanel'

interface ContextMenuProps {
  selection: TreeSelection
  nodeName: string
  collapsed: boolean
  onToggleCollapse: () => void
}

// Menu data comes from the shared registries: lib/navigation NODE_MENU_ITEMS
// for the node scope, datacenterMenu for the cluster scope — the local copies
// this file used to carry had already drifted (Stacks was missing here).
interface MenuItem {
  to: string
  labelKey: string
  icon: React.ElementType
  matchEnd?: boolean
}

export default function ContextMenu({ selection, nodeName, collapsed, onToggleCollapse }: ContextMenuProps) {
  const { t } = useTranslation()
  const isDatacenter = selection.type === 'datacenter'
  const items: MenuItem[] = isDatacenter ? DATACENTER_MENU_ITEMS : NODE_MENU_ITEMS
  const title = isDatacenter ? t('cluster.title') : nodeName

  if (collapsed) {
    return (
      <div className="w-[42px] bg-card border-r border-border flex flex-col h-full shrink-0">
        <button
          onClick={onToggleCollapse}
          className="flex items-center justify-center py-3 border-b border-border hover:bg-accent transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
          title={t('layout.expand')}
          aria-label={t('layout.expand')}
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
              aria-label={t(item.labelKey)}
              className={({ isActive }) =>
                cn(
                  'w-8 h-8 rounded-lg flex items-center justify-center transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0',
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
            {isDatacenter ? t('cluster.tree.datacenter') : t('cluster.tree.node')}
          </p>
          <p className="text-[13px] font-semibold text-foreground truncate mt-0.5">{title}</p>
        </div>
        <button onClick={onToggleCollapse} className="p-1.5 rounded-lg hover:bg-accent border border-border transition-colors mt-0.5 shrink-0 outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0" title={t('layout.collapse')} aria-label={t('layout.collapse')}>
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
                'flex items-center gap-2.5 px-2.5 py-2 rounded-lg text-[12px] font-medium transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0',
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
