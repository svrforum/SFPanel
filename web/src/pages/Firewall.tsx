import { useTranslation } from 'react-i18next'
import { Outlet } from 'react-router-dom'
import { ShieldCheck, Network, ShieldAlert, Container, ScrollText, Map } from 'lucide-react'
import { SubNavTabs } from '@/components/SubNavTabs'

const navItems = [
  { to: '/firewall/rules', icon: ShieldCheck, label: 'firewall.tabs.rules' },
  { to: '/firewall/ports', icon: Network, label: 'firewall.tabs.ports' },
  { to: '/firewall/portmap', icon: Map, label: 'firewall.tabs.portmap' },
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

      <SubNavTabs items={navItems} />

      {/* Content */}
      <div className="min-h-[calc(100vh-220px)]">
        <Outlet />
      </div>
    </div>
  )
}
