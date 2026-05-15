import { useState, useEffect, useCallback } from 'react'
import { Link, NavLink, Outlet, useNavigate, useLocation } from 'react-router-dom'
import { LayoutDashboard, Container, FolderOpen, Clock, FileText, Package, Settings, LogOut, Activity, Terminal, Network, HardDrive, Shield, Cog, PanelLeftClose, PanelLeftOpen, Store, Server, Coffee } from 'lucide-react'

// lucide-react dropped brand icons (Github, Twitter, …) so we inline the
// official GitHub mark — small enough that it's not worth pulling another
// icon package just for one glyph.
const GithubIcon = ({ className }: { className?: string }) => (
  <svg viewBox="0 0 24 24" className={className} fill="currentColor" aria-hidden="true">
    <path d="M12 0C5.374 0 0 5.373 0 12 0 17.302 3.438 21.8 8.207 23.387c.6.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23A11.509 11.509 0 0112 5.803c1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576C20.566 21.797 24 17.3 24 12c0-6.627-5.373-12-12-12z"/>
  </svg>
)
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'
import NodeSelector from '@/components/NodeSelector'
import ClusterSidebar from '@/components/cluster/ClusterSidebar'
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
  const [clusterEnabled, setClusterEnabled] = useState(false)
  const location = useLocation()
  const isTerminal = location.pathname === '/terminal'

  const handleNodeChanged = useCallback(() => {
    setNodeKey((k) => k + 1)
    window.dispatchEvent(new Event('sfpanel:node-changed'))
  }, [])

  useEffect(() => {
    const handler = () => setNodeKey((k) => k + 1)
    window.addEventListener('sfpanel:node-changed', handler)
    return () => window.removeEventListener('sfpanel:node-changed', handler)
  }, [])

  useEffect(() => {
    api.getClusterStatus(true)
      .then((status) => setClusterEnabled(status.enabled))
      .catch(() => setClusterEnabled(false))
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
    // Fire-and-forget — even if the server is unreachable we still want to
    // navigate away. api.logout() clears local state on its own.
    void api.logout()
    navigate('/login')
  }

  return (
    <div className="flex h-screen overflow-hidden bg-background">
      {/* Cluster dual-panel sidebar */}
      {clusterEnabled && (
        <div className="hidden md:flex h-screen shrink-0">
          <ClusterSidebar
            panelVersion={panelVersion}
            onLogout={handleLogout}
            onNodeChanged={handleNodeChanged}
          />
        </div>
      )}

      {/* Standard sidebar (non-cluster mode) */}
      {!clusterEnabled && <aside className={cn(
        'bg-card border-r border-border flex-col transition-all duration-300 ease-in-out shrink-0 hidden md:flex h-screen',
        collapsed ? 'w-[68px]' : 'w-60'
      )}>
        <div className={cn('flex items-center', collapsed ? 'px-3 py-6 justify-center' : 'px-4 py-4')}>
          {collapsed ? (
            <Link to="/dashboard" aria-label="SFPanel" className="rounded-lg focus:outline-none focus-visible:ring-2 focus-visible:ring-primary/30">
              <img src="/favicon.png" alt="SFPanel" className="h-8 w-8 rounded-lg" />
            </Link>
          ) : (
            <Link to="/dashboard" aria-label="SFPanel" className="block w-full rounded-lg focus:outline-none focus-visible:ring-2 focus-visible:ring-primary/30">
              <img src="/banner.png" alt="SFPanel" className="w-full h-auto" />
            </Link>
          )}
        </div>

        <nav className={cn('flex-1 min-h-0 overflow-y-auto no-scrollbar space-y-0.5', collapsed ? 'px-2' : 'px-3')}>
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

        {/* Sidebar bottom (fixed, never pushed off-screen) */}
        <div className="shrink-0 mt-auto">
        {/* Cluster node selector */}
        <NodeSelector collapsed={collapsed} />

        {/* Version info */}
        <div className={cn('border-t border-border', collapsed ? 'px-2 py-2' : 'px-4 py-3')}>
          {collapsed ? (
            <div className="flex flex-col items-center gap-1.5">
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
              <div className="flex items-center gap-1 pt-0.5">
                <a
                  href="https://github.com/svrforum/SFPanel"
                  target="_blank"
                  rel="noopener noreferrer"
                  title="GitHub"
                  className="text-muted-foreground hover:text-foreground transition-colors"
                >
                  <GithubIcon className="h-3.5 w-3.5" />
                </a>
                <a
                  href="https://buymeacoffee.com/svrforum"
                  target="_blank"
                  rel="noopener noreferrer"
                  title="Buy me a coffee"
                  className="text-muted-foreground hover:text-[#FFDD00] transition-colors"
                >
                  <Coffee className="h-3.5 w-3.5" />
                </a>
              </div>
            </div>
          ) : (
            <div className="space-y-2">
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
              <div className="flex items-center gap-2.5 text-[11px] text-muted-foreground">
                <a
                  href="https://github.com/svrforum/SFPanel"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1 hover:text-foreground transition-colors"
                  title="GitHub"
                >
                  <GithubIcon className="h-3 w-3" />
                  <span>GitHub</span>
                </a>
                <a
                  href="https://buymeacoffee.com/svrforum"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1 hover:text-[#FFDD00] transition-colors"
                  title="Buy me a coffee"
                >
                  <Coffee className="h-3 w-3" />
                  <span>후원</span>
                </a>
              </div>
            </div>
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
        </div>
      </aside>}

      <main className={cn(
        "flex-1 min-h-0",
        isTerminal ? "p-0 overflow-hidden" : "overflow-auto px-5 py-4 pb-bottom-nav md:p-8 md:pb-8"
      )}>
        <Outlet key={nodeKey} />
      </main>

      <BottomNav onMorePress={() => setMoreOpen(true)} />
      <MoreMenu open={moreOpen} onOpenChange={setMoreOpen} />
    </div>
  )
}
