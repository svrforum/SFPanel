import { useState, useEffect } from 'react'
import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import { LayoutDashboard, Container, FolderOpen, Clock, FileText, Package, Settings, LogOut, Activity, Terminal, Network, HardDrive, Shield, Cog, PanelLeftClose, PanelLeftOpen } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'

const navItems = [
  { to: '/dashboard', labelKey: 'layout.nav.dashboard', icon: LayoutDashboard },
  { to: '/docker', labelKey: 'layout.nav.docker', icon: Container },
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
  { to: '/settings', labelKey: 'layout.nav.settings', icon: Settings },
]

const SIDEBAR_KEY = 'sfpanel-sidebar-collapsed'

export default function Layout() {
  const navigate = useNavigate()
  const { t } = useTranslation()
  const [collapsed, setCollapsed] = useState(() => {
    return localStorage.getItem(SIDEBAR_KEY) === 'true'
  })
  const [updateAvailable, setUpdateAvailable] = useState(false)

  useEffect(() => {
    localStorage.setItem(SIDEBAR_KEY, String(collapsed))
  }, [collapsed])

  useEffect(() => {
    api.checkUpdate()
      .then((data) => setUpdateAvailable(data.update_available))
      .catch(() => {})
  }, [])

  const handleLogout = () => {
    api.clearToken()
    navigate('/login')
  }

  return (
    <div className="flex h-screen bg-background">
      <aside className={cn(
        'bg-card border-r border-border flex flex-col transition-all duration-300 ease-in-out shrink-0',
        collapsed ? 'w-[68px]' : 'w-60'
      )}>
        <div className={cn('flex items-center py-6', collapsed ? 'px-3 justify-center' : 'px-5')}>
          {collapsed ? (
            <h1 className="text-lg font-bold tracking-tight text-foreground">SF</h1>
          ) : (
            <div>
              <h1 className="text-lg font-bold tracking-tight text-foreground">SFPanel</h1>
              <p className="text-xs text-muted-foreground mt-0.5">{t('layout.tagline')}</p>
            </div>
          )}
        </div>

        <nav className={cn('flex-1 space-y-0.5', collapsed ? 'px-2' : 'px-3')}>
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              title={collapsed ? t(item.labelKey) : undefined}
              className={({ isActive }) =>
                cn(
                  'relative flex items-center rounded-xl text-[13px] font-medium transition-all duration-200',
                  collapsed ? 'justify-center px-0 py-2.5' : 'gap-3 px-3 py-2',
                  isActive
                    ? 'bg-primary/10 text-primary'
                    : 'text-muted-foreground hover:bg-accent hover:text-foreground'
                )
              }
            >
              <item.icon className="h-[18px] w-[18px] shrink-0" />
              {!collapsed && t(item.labelKey)}
              {item.to === '/settings' && updateAvailable && !collapsed && (
                <span className="ml-auto h-2 w-2 rounded-full bg-[#3182f6] shrink-0" />
              )}
              {item.to === '/settings' && updateAvailable && collapsed && (
                <span className="absolute top-1.5 right-1.5 h-1.5 w-1.5 rounded-full bg-[#3182f6]" />
              )}
            </NavLink>
          ))}
        </nav>

        <div className={cn('pb-4 pt-2 border-t border-border', collapsed ? 'px-2' : 'px-3')}>
          <button
            onClick={() => setCollapsed(!collapsed)}
            className={cn(
              'flex items-center rounded-xl text-[13px] font-medium text-muted-foreground hover:bg-accent hover:text-foreground transition-all duration-200 w-full',
              collapsed ? 'justify-center px-0 py-2.5' : 'gap-3 px-3 py-2'
            )}
            title={collapsed ? t('layout.expand') : t('layout.collapse')}
          >
            {collapsed ? <PanelLeftOpen className="h-[18px] w-[18px]" /> : <PanelLeftClose className="h-[18px] w-[18px]" />}
            {!collapsed && t('layout.collapse')}
          </button>
          <button
            onClick={handleLogout}
            className={cn(
              'flex items-center rounded-xl text-[13px] font-medium text-muted-foreground hover:bg-accent hover:text-foreground transition-all duration-200 w-full',
              collapsed ? 'justify-center px-0 py-2.5' : 'gap-3 px-3 py-2'
            )}
            title={collapsed ? t('layout.logout') : undefined}
          >
            <LogOut className="h-[18px] w-[18px] shrink-0" />
            {!collapsed && t('layout.logout')}
          </button>
        </div>
      </aside>

      <main className="flex-1 overflow-auto p-8">
        <Outlet />
      </main>
    </div>
  )
}
