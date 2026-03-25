import { useState, useEffect } from 'react'
import { NavLink, Outlet, useNavigate, useLocation } from 'react-router-dom'
import { LayoutDashboard, Container, FolderOpen, Clock, FileText, Package, Settings, LogOut, Activity, Terminal, Network, HardDrive, Shield, Cog, PanelLeftClose, PanelLeftOpen, Store, Server } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'
import NodeSelector from '@/components/NodeSelector'
import BottomNav from '@/components/BottomNav'
import MoreMenu from '@/components/MoreMenu'

const navItems = [
  { to: '/dashboard', labelKey: 'layout.nav.dashboard', icon: LayoutDashboard },
  { to: '/cluster', labelKey: 'layout.nav.cluster', icon: Server },
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
  const [panelVersion, setPanelVersion] = useState('')
  const [nodeKey, setNodeKey] = useState(0)
  const [moreOpen, setMoreOpen] = useState(false)
  const location = useLocation()
  const isTerminal = location.pathname === '/terminal'

  useEffect(() => {
    const handler = () => setNodeKey((k) => k + 1)
    window.addEventListener('sfpanel:node-changed', handler)
    return () => window.removeEventListener('sfpanel:node-changed', handler)
  }, [])

  useEffect(() => {
    localStorage.setItem(SIDEBAR_KEY, String(collapsed))
  }, [collapsed])

  useEffect(() => {
    api.getSystemInfo()
      .then((data) => {
        if (data.version) setPanelVersion(data.version)
      })
      .catch(() => {})
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
        'bg-card border-r border-border flex-col transition-all duration-300 ease-in-out shrink-0 hidden md:flex',
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
            </NavLink>
          ))}
        </nav>

        {/* Cluster node selector */}
        <NodeSelector collapsed={collapsed} />

        {/* Version info */}
        <div className={cn('border-t border-border', collapsed ? 'px-2 py-2' : 'px-4 py-3')}>
          {collapsed ? (
            <button
              onClick={() => navigate('/settings')}
              title={panelVersion ? `v${panelVersion}` : 'SFPanel'}
              className="flex flex-col items-center gap-1 w-full"
            >
              <span className="text-[10px] font-medium text-muted-foreground">
                {panelVersion ? `v${panelVersion.split('.').slice(0, 2).join('.')}` : '...'}
              </span>
              {updateAvailable ? (
                <span className="h-1.5 w-1.5 rounded-full bg-[#3182f6]" />
              ) : panelVersion ? (
                <span className="h-1.5 w-1.5 rounded-full bg-[#00c471]" />
              ) : null}
            </button>
          ) : (
            <button
              onClick={() => navigate('/settings')}
              className="flex items-center justify-between w-full group"
            >
              <div>
                <p className="text-[11px] font-medium text-muted-foreground">SFPanel</p>
                <p className="text-[12px] font-semibold text-foreground/80">
                  {panelVersion ? `v${panelVersion}` : '...'}
                </p>
              </div>
              {updateAvailable ? (
                <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-semibold bg-[#3182f6]/10 text-[#3182f6]">
                  {t('layout.updateAvailable')}
                </span>
              ) : panelVersion ? (
                <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-semibold bg-[#00c471]/10 text-[#00c471]">
                  {t('layout.upToDate')}
                </span>
              ) : null}
            </button>
          )}
        </div>

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

      <main className={cn(
        "flex-1",
        isTerminal ? "p-0 overflow-hidden" : "overflow-auto px-5 py-4 pb-bottom-nav md:p-8 md:pb-8"
      )}>
        <Outlet key={nodeKey} />
      </main>

      <BottomNav onMorePress={() => setMoreOpen(true)} />
      <MoreMenu open={moreOpen} onOpenChange={setMoreOpen} />
    </div>
  )
}
