import { useState, useEffect } from 'react'
import { useNavigate, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Drawer } from 'vaul'
import {
  Activity,
  Server,
  Store,
  FolderOpen,
  Clock,
  FileText,
  Cog,
  Network,
  HardDrive,
  Shield,
  Package,
  Settings,
  LogOut,
  Monitor,
  ChevronDown,
} from 'lucide-react'
import { cn } from '@/lib/utils'
import { api } from '@/lib/api'
import type { ClusterStatus, ClusterNode } from '@/types/api'

interface MoreMenuProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

const menuItems = [
  { path: '/processes', icon: Activity, label: 'layout.nav.processes' },
  { path: '/cluster', icon: Server, label: 'layout.nav.cluster' },
  { path: '/appstore', icon: Store, label: 'layout.nav.appstore' },
  { path: '/files', icon: FolderOpen, label: 'layout.nav.files' },
  { path: '/cron', icon: Clock, label: 'layout.nav.cron' },
  { path: '/logs', icon: FileText, label: 'layout.nav.logs' },
  { path: '/services', icon: Cog, label: 'layout.nav.services' },
  { path: '/network', icon: Network, label: 'layout.nav.networkVpn' },
  { path: '/disk', icon: HardDrive, label: 'layout.nav.disk' },
  { path: '/firewall', icon: Shield, label: 'layout.nav.firewall' },
  { path: '/packages', icon: Package, label: 'layout.nav.packages' },
  { path: '/settings', icon: Settings, label: 'layout.nav.settings' },
] as const

export default function MoreMenu({ open, onOpenChange }: MoreMenuProps) {
  const navigate = useNavigate()
  const location = useLocation()
  const { t } = useTranslation()
  const [clusterEnabled, setClusterEnabled] = useState(false)
  const [nodes, setNodes] = useState<ClusterNode[]>([])
  const [localId, setLocalId] = useState('')
  const [selectedNode, setSelectedNode] = useState<string | null>(api.currentNode)
  const [nodeOpen, setNodeOpen] = useState(false)

  useEffect(() => {
    if (!open) return
    api.getClusterStatus(true)
      .then((status: ClusterStatus) => {
        setClusterEnabled(status.enabled)
        if (status.enabled && status.local_id) {
          setLocalId(status.local_id)
          setSelectedNode(api.currentNode)
          api.getClusterNodes(true).then((data) => setNodes(data.nodes)).catch(() => {})
        }
      })
      .catch(() => {})
  }, [open])

  const handleNodeSelect = (nodeId: string) => {
    const newNode = nodeId === localId ? null : nodeId
    setSelectedNode(newNode)
    api.setCurrentNode(newNode)
    window.dispatchEvent(new Event('sfpanel:node-changed'))
    setNodeOpen(false)
  }

  const statusColor = (status: string) => {
    switch (status) {
      case 'online': return 'bg-[#00c471]'
      case 'suspect': return 'bg-[#f59e0b]'
      case 'offline': return 'bg-[#f04452]'
      default: return 'bg-muted-foreground'
    }
  }

  const currentNode = selectedNode
    ? nodes.find((n) => n.id === selectedNode)
    : nodes.find((n) => n.id === localId)

  const handleNavigate = (path: string) => {
    navigate(path)
    onOpenChange(false)
  }

  const handleLogout = () => {
    api.clearToken()
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
                  className="flex items-center gap-2 w-full px-3 py-2.5 rounded-xl bg-secondary/50 transition-colors"
                >
                  <Monitor className="h-4 w-4 text-muted-foreground shrink-0" />
                  <span className={cn('h-2 w-2 rounded-full shrink-0', statusColor(currentNode?.status || ''))} />
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
                          'flex items-center gap-2 w-full px-3 py-2 text-[13px] transition-colors rounded-lg',
                          (selectedNode === node.id || (!selectedNode && node.id === localId))
                            ? 'bg-primary/10 text-primary'
                            : 'text-foreground/80'
                        )}
                      >
                        <span className={cn('h-2 w-2 rounded-full shrink-0', statusColor(node.status))} />
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
                      'flex flex-col items-center gap-1.5 rounded-xl py-3 px-1 transition-colors',
                      isActive
                        ? 'bg-primary/10 text-[#3182f6]'
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
                className="flex items-center gap-2 w-full rounded-xl py-3 px-4 text-[#f04452] active:bg-secondary/80 transition-colors"
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
