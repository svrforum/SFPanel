import { NavLink, Outlet } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Server, LayoutDashboard, KeyRound } from 'lucide-react'
import { cn } from '@/lib/utils'

const tabs = [
  { to: 'overview', labelKey: 'cluster.nav.overview', icon: LayoutDashboard },
  { to: 'nodes', labelKey: 'cluster.nav.nodes', icon: Server },
  { to: 'tokens', labelKey: 'cluster.nav.tokens', icon: KeyRound },
]

export default function Cluster() {
  const { t } = useTranslation()

  return (
    <div className="space-y-6">
      <h1 className="text-[22px] font-bold tracking-tight">{t('cluster.title')}</h1>
      <div className="flex items-center gap-1">
        {tabs.map((tab) => (
          <NavLink
            key={tab.to}
            to={tab.to}
            className={({ isActive }) =>
              cn(
                'flex items-center gap-2 px-4 py-2 rounded-xl text-[13px] font-medium transition-colors',
                isActive
                  ? 'bg-primary/10 text-primary'
                  : 'text-muted-foreground hover:bg-accent hover:text-foreground'
              )
            }
          >
            <tab.icon className="h-4 w-4" />
            {t(tab.labelKey)}
          </NavLink>
        ))}
      </div>
      <Outlet />
    </div>
  )
}
