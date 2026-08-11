import { useTranslation } from 'react-i18next'
import { Outlet } from 'react-router-dom'
import { HardDrive, LayoutGrid, Layers, Database, Server, MemoryStick, PieChart } from 'lucide-react'
import { SubNavTabs } from '@/components/SubNavTabs'

const navItems = [
  { to: '/disk/overview', icon: LayoutGrid, label: 'disk.tabs.overview' },
  { to: '/disk/usage', icon: PieChart, label: 'disk.tabs.usage' },
  { to: '/disk/partitions', icon: HardDrive, label: 'disk.tabs.partitions' },
  { to: '/disk/filesystems', icon: Database, label: 'disk.tabs.filesystems' },
  { to: '/disk/lvm', icon: Layers, label: 'disk.tabs.lvm' },
  { to: '/disk/raid', icon: Server, label: 'disk.tabs.raid' },
  { to: '/disk/swap', icon: MemoryStick, label: 'disk.tabs.swap' },
]

export default function Disk() {
  const { t } = useTranslation()

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-[22px] font-bold tracking-tight">{t('disk.title')}</h1>
      </div>

      <SubNavTabs items={navItems} />

      {/* Content */}
      <div className="min-h-[calc(100vh-220px)]">
        <Outlet />
      </div>
    </div>
  )
}
