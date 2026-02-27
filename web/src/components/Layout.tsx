import { NavLink, Outlet, useNavigate } from 'react-router-dom'
import { LayoutDashboard, Container, FolderOpen, Clock, FileText, Package, Settings, LogOut, Activity, Terminal, Network, HardDrive, Shield } from 'lucide-react'
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
  { to: '/network', labelKey: 'layout.nav.network', icon: Network },
  { to: '/disk', labelKey: 'layout.nav.disk', icon: HardDrive },
  { to: '/firewall', labelKey: 'layout.nav.firewall', icon: Shield },
  { to: '/packages', labelKey: 'layout.nav.packages', icon: Package },
  { to: '/terminal', labelKey: 'layout.nav.terminal', icon: Terminal },
  { to: '/settings', labelKey: 'layout.nav.settings', icon: Settings },
]

export default function Layout() {
  const navigate = useNavigate()
  const { t } = useTranslation()

  const handleLogout = () => {
    api.clearToken()
    navigate('/login')
  }

  return (
    <div className="flex h-screen bg-background">
      <aside className="w-60 bg-card border-r border-border flex flex-col">
        <div className="px-5 py-6">
          <h1 className="text-lg font-bold tracking-tight text-foreground">SFPanel</h1>
          <p className="text-xs text-muted-foreground mt-0.5">{t('layout.tagline')}</p>
        </div>

        <nav className="flex-1 px-3 space-y-0.5">
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) =>
                cn(
                  'flex items-center gap-3 px-3 py-2 rounded-xl text-[13px] font-medium transition-all duration-200',
                  isActive
                    ? 'bg-primary/10 text-primary'
                    : 'text-muted-foreground hover:bg-accent hover:text-foreground'
                )
              }
            >
              <item.icon className="h-[18px] w-[18px]" />
              {t(item.labelKey)}
            </NavLink>
          ))}
        </nav>

        <div className="px-3 pb-4 pt-2 border-t border-border">
          <button
            onClick={handleLogout}
            className="flex items-center gap-3 px-3 py-2 rounded-xl text-[13px] font-medium text-muted-foreground hover:bg-accent hover:text-foreground transition-all duration-200 w-full"
          >
            <LogOut className="h-[18px] w-[18px]" />
            {t('layout.logout')}
          </button>
        </div>
      </aside>

      <main className="flex-1 overflow-auto p-8">
        <Outlet />
      </main>
    </div>
  )
}
