import { useTranslation } from 'react-i18next'
import { Outlet, useOutletContext } from 'react-router-dom'
import { SubNavTabs } from '@/components/SubNavTabs'
import { navTabs } from '@/lib/navigation'

/**
 * Services, processes, scheduled tasks, packages and performance tuning: one
 * menu entry, a tab each, every page at the address it always had (tuning's,
 * /tuning, is the one new one). The shell's context is handed on to the
 * pages, as if this layer were not there.
 */
export default function System() {
  const { t } = useTranslation()
  return (
    <div className="space-y-4">
      <h1 className="text-[22px] font-bold tracking-tight">{t('layout.nav.system')}</h1>
      <SubNavTabs items={navTabs('/services')} label={t('layout.nav.system')} />
      <Outlet context={useOutletContext()} />
    </div>
  )
}
