import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { NavLink, Outlet } from 'react-router-dom'
import { Layers, Box, Image, HardDrive, Network, Trash2 } from 'lucide-react'
import DockerPrune from '@/components/docker/DockerPrune'

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
    <div className="space-y-4 docker-workspace">
      <div className="flex items-center justify-between">
        <h1 className="text-[22px] font-bold tracking-tight">{t('docker.title')}</h1>
      </div>

      <nav aria-label={t('docker.title')} className="grid grid-cols-3 gap-1 rounded-xl bg-secondary/30 p-1 sm:flex sm:flex-wrap">
        {navItems.map(({ to, icon: Icon, label }) => <NavLink key={to} to={to} className={({ isActive }) => `flex min-h-11 items-center justify-center gap-2 rounded-lg px-2 text-sm ${isActive ? 'bg-card text-primary shadow-sm' : 'text-muted-foreground'}`}><Icon className="h-4 w-4 shrink-0" />{t(label)}</NavLink>)}
        <button onClick={() => setPruneOpen(true)} className="flex min-h-11 items-center justify-center gap-2 rounded-lg px-2 text-sm text-muted-foreground sm:ml-auto"><Trash2 className="h-4 w-4" />{t('docker.improvements.globalPrune')}</button>
      </nav>

      {/* Content */}
      <div className="min-h-[calc(100vh-220px)]">
        <Outlet />
      </div>

      <DockerPrune open={pruneOpen} onOpenChange={setPruneOpen} />
    </div>
  )
}
