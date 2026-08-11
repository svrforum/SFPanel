import { useState } from 'react'
import { useNavigate, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Drawer } from 'vaul'
import { LogOut, Monitor, ChevronDown } from 'lucide-react'
import { cn, nodeStatusColor } from '@/lib/utils'
import { MORE_MENU_ITEMS } from '@/lib/navigation'
import { api } from '@/lib/api'
import { useClusterNodes } from '@/hooks/useClusterNodes'

interface MoreMenuProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

// Derived from the single registry: everything that isn't a bottom-bar tab.
const menuItems = MORE_MENU_ITEMS.map((i) => ({
  path: i.to,
  icon: i.icon,
  label: i.labelKey,
}))

export default function MoreMenu({ open, onOpenChange }: MoreMenuProps) {
  const navigate = useNavigate()
  const location = useLocation()
  const { t } = useTranslation()
  const { clusterStatus, nodes, localId, selectedNode, currentNode, selectNode } = useClusterNodes()
  const [nodeOpen, setNodeOpen] = useState(false)

  const clusterEnabled = clusterStatus?.enabled ?? false

  const handleNodeSelect = (nodeId: string) => {
    selectNode(nodeId === localId ? null : nodeId)
    setNodeOpen(false)
  }

  const handleNavigate = (path: string) => {
    navigate(path)
    onOpenChange(false)
  }

  const handleLogout = () => {
    void api.logout()
    onOpenChange(false)
    navigate('/login')
  }

  return (
    <Drawer.Root open={open} onOpenChange={onOpenChange}>
      <Drawer.Portal>
        <Drawer.Overlay className="fixed inset-0 bg-black/40 z-50" />
        <Drawer.Content className="fixed bottom-0 left-0 right-0 z-50 bg-card rounded-t-2xl outline-none">
          <div className="mx-auto w-12 h-1.5 rounded-full bg-muted-foreground/20 mt-3 mb-2" />
          <Drawer.Title className="sr-only">Menu</Drawer.Title>

          <div className="overflow-y-auto px-4 pb-safe" style={{ maxHeight: '70vh' }}>
            {/* Mobile node selector */}
            {clusterEnabled && nodes.length > 0 && (
              <div className="pb-2 mb-2 border-b border-border">
                <button
                  onClick={() => setNodeOpen(!nodeOpen)}
                  aria-expanded={nodeOpen}
                  className="flex items-center gap-2 w-full px-3 py-2.5 rounded-xl bg-secondary/50 transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
                >
                  <Monitor className="h-4 w-4 text-muted-foreground shrink-0" />
                  <span className={cn('h-2 w-2 rounded-full shrink-0', nodeStatusColor(currentNode?.status || ''))} />
                  <span className="text-[13px] font-medium truncate">
                    {currentNode?.name || t('layout.cluster.selectNode')}
                  </span>
                  <ChevronDown className={cn('h-3.5 w-3.5 text-muted-foreground ml-auto shrink-0 transition-transform', nodeOpen && 'rotate-180')} />
                </button>
                {nodeOpen && (
                  <div className="mt-1 rounded-xl bg-secondary/30 py-1">
                    {nodes.map((node) => (
                      <button
                        key={node.id}
                        onClick={() => handleNodeSelect(node.id)}
                        className={cn(
                          'flex items-center gap-2 w-full px-3 py-2 text-[13px] transition-colors rounded-lg outline-none focus-visible:ring-2 focus-visible:ring-ring/40',
                          (selectedNode === node.id || (!selectedNode && node.id === localId))
                            ? 'bg-primary/10 text-primary'
                            : 'text-foreground/80'
                        )}
                      >
                        <span className={cn('h-2 w-2 rounded-full shrink-0', nodeStatusColor(node.status))} />
                        <span className="truncate">{node.name}</span>
                        {node.id === localId && (
                          <span className="text-[10px] text-muted-foreground">({t('layout.cluster.localNode')})</span>
                        )}
                      </button>
                    ))}
                  </div>
                )}
              </div>
            )}

            <div className="grid grid-cols-4 gap-2 py-2">
              {menuItems.map(({ path, icon: Icon, label }) => {
                const isActive = location.pathname.startsWith(path)
                return (
                  <button
                    key={path}
                    onClick={() => handleNavigate(path)}
                    className={cn(
                      'flex flex-col items-center gap-1.5 rounded-xl py-3 px-1 transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40',
                      isActive
                        ? 'bg-primary/10 text-primary'
                        : 'text-muted-foreground active:bg-secondary/80'
                    )}
                  >
                    <Icon className="h-5 w-5" />
                    <span className="text-[11px] font-medium leading-tight text-center">
                      {t(label)}
                    </span>
                  </button>
                )
              })}
            </div>

            <div className="border-t border-border mt-2 pt-2 pb-4">
              <button
                onClick={handleLogout}
                className="flex items-center gap-2 w-full rounded-xl py-3 px-4 text-destructive active:bg-secondary/80 transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
              >
                <LogOut className="h-5 w-5" />
                <span className="text-[13px] font-medium">{t('layout.logout')}</span>
              </button>
            </div>
          </div>
        </Drawer.Content>
      </Drawer.Portal>
    </Drawer.Root>
  )
}
