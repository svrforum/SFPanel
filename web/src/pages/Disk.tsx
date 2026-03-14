import { useTranslation } from 'react-i18next'
import { NavLink, Outlet } from 'react-router-dom'
import { HardDrive, LayoutGrid, Layers, Database, Server, MemoryStick } from 'lucide-react'

const navItems = [
  { to: '/disk/overview', icon: LayoutGrid, label: 'disk.tabs.overview' },
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
      </div>

      {/* Content */}
      <div className="min-h-[calc(100vh-220px)]">
        <Outlet />
      </div>
    </div>
  )
}
