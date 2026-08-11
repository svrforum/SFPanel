import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Outlet } from 'react-router-dom'
import { Layers, Box, Image, HardDrive, Network, Trash2 } from 'lucide-react'
import DockerPrune from '@/components/docker/DockerPrune'
import { SubNavTabs } from '@/components/SubNavTabs'

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

      <SubNavTabs items={navItems}>
        <div className="flex-1 shrink-0" />
        <button
          onClick={() => setPruneOpen(true)}
          className="flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-[13px] font-medium text-muted-foreground hover:text-foreground transition-all duration-200 whitespace-nowrap shrink-0 outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/40"
        >
          <Trash2 className="h-3.5 w-3.5 shrink-0" />
          {t('docker.sidebar.prune')}
        </button>
      </SubNavTabs>

      {/* Content */}
      <div className="min-h-[calc(100vh-220px)]">
        <Outlet />
      </div>

      <DockerPrune open={pruneOpen} onOpenChange={setPruneOpen} />
    </div>
  )
}
