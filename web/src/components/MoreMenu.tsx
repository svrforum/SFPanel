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
} from 'lucide-react'
import { cn } from '@/lib/utils'
import { api } from '@/lib/api'

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
