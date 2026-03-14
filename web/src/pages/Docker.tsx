import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { NavLink, Outlet } from 'react-router-dom'
import { Layers, Box, Image, HardDrive, Network, Trash2 } from 'lucide-react'
import DockerPrune from '@/components/DockerPrune'

const navItems = [
  { to: '/docker/stacks', icon: Layers, label: 'docker.sidebar.stacks' },
  { to: '/docker/containers', icon: Box, label: 'docker.sidebar.containers' },
  { to: '/docker/images', icon: Image, label: 'docker.sidebar.images' },
  { to: '/docker/volumes', icon: HardDrive, label: 'docker.sidebar.volumes' },
  { to: '/docker/networks', icon: Network, label: 'docker.sidebar.networks' },
]

export default function Docker() {
  const { t } = useTranslation()
  const [pruneOpen, setPruneOpen] = useState(false)

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-[22px] font-bold tracking-tight">{t('docker.title')}</h1>
      </div>

      {/* Sub-navigation tabs */}
      <div className="flex items-center gap-1 bg-secondary/30 rounded-xl p-1 overflow-x-auto no-scrollbar">
        {navItems.map(({ to, icon: Icon, label }) => (
          <NavLink
            key={to}
            to={to}
            className={({ isActive }) =>
              `flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] font-medium transition-all duration-200 whitespace-nowrap shrink-0 ${
                isActive
                  ? 'bg-card card-shadow text-foreground'
                  : 'text-muted-foreground hover:text-foreground'
              }`
            }
          >
            <Icon className="h-3.5 w-3.5 shrink-0" />
            {t(label)}
          </NavLink>
        ))}

        <div className="flex-1 shrink-0" />
        <button
          onClick={() => setPruneOpen(true)}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] font-medium text-muted-foreground hover:text-foreground transition-all duration-200 whitespace-nowrap shrink-0"
        >
          <Trash2 className="h-3.5 w-3.5 shrink-0" />
          {t('docker.sidebar.prune')}
        </button>
      </div>

      {/* Content */}
      <div className="min-h-[calc(100vh-220px)]">
        <Outlet />
      </div>

      <DockerPrune open={pruneOpen} onOpenChange={setPruneOpen} />
    </div>
  )
}
