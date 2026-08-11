import { useTranslation } from 'react-i18next'
import { Outlet } from 'react-router-dom'
import { Cable, Shield, Globe } from 'lucide-react'
import { SubNavTabs } from '@/components/SubNavTabs'

const navItems = [
  { to: '/network/interfaces', icon: Cable, label: 'network.sidebar.interfaces' },
  { to: '/network/wireguard', icon: Shield, label: 'network.sidebar.wireguard' },
  { to: '/network/tailscale', icon: Globe, label: 'network.sidebar.tailscale' },
]

export default function Network() {
  const { t } = useTranslation()

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-[22px] font-bold tracking-tight">{t('network.title')}</h1>
      </div>

      <SubNavTabs items={navItems} />

      {/* Content */}
      <div className="min-h-[calc(100vh-220px)]">
        <Outlet />
      </div>
    </div>
  )
}
