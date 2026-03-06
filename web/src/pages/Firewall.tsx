import { useTranslation } from 'react-i18next'
import { NavLink, Outlet } from 'react-router-dom'
import { ShieldCheck, Network, ShieldAlert, Container, ScrollText } from 'lucide-react'

const navItems = [
  { to: '/firewall/rules', icon: ShieldCheck, label: 'firewall.tabs.rules' },
  { to: '/firewall/ports', icon: Network, label: 'firewall.tabs.ports' },
  { to: '/firewall/fail2ban', icon: ShieldAlert, label: 'firewall.tabs.fail2ban' },
  { to: '/firewall/docker', icon: Container, label: 'firewall.tabs.docker' },
  { to: '/firewall/logs', icon: ScrollText, label: 'firewall.tabs.logs' },
]

export default function Firewall() {
  const { t } = useTranslation()

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-[22px] font-bold tracking-tight">{t('firewall.title')}</h1>
      </div>

      {/* Sub-navigation tabs */}
      <div className="flex items-center gap-1 bg-secondary/30 rounded-xl p-1">
        {navItems.map(({ to, icon: Icon, label }) => (
          <NavLink
            key={to}
            to={to}
            className={({ isActive }) =>
              `flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] font-medium transition-all duration-200 ${
                isActive
                  ? 'bg-card card-shadow text-foreground'
                  : 'text-muted-foreground hover:text-foreground'
              }`
            }
          >
            <Icon className="h-3.5 w-3.5" />
            {t(label)}
          </NavLink>
        ))}
      </div>

      {/* Content */}
      <div className="min-h-[calc(100vh-220px)]">
        <Outlet />
      </div>
    </div>
  )
}
