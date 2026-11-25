import { useTranslation } from 'react-i18next'
import { Outlet } from 'react-router-dom'
import { SubNavTabs } from '@/components/SubNavTabs'
import { navTabs } from '@/lib/navigation'

export default function Network() {
  const { t } = useTranslation()

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-[22px] font-bold tracking-tight">{t('network.title')}</h1>
      </div>

      <SubNavTabs items={navTabs('/network')} />

      {/* Content */}
      <div className="min-h-[calc(100vh-220px)]">
        <Outlet />
      </div>
    </div>
  )
}
