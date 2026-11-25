import { useTranslation } from 'react-i18next'
import { Outlet, useOutletContext } from 'react-router-dom'
import { SubNavTabs } from '@/components/SubNavTabs'
import { navTabs } from '@/lib/navigation'

/**
 * The dashboard's summary and what runs on this server, as tabs at their own
 * addresses. Each page draws its own heading. The dashboard reads the
 * overview the shell already fetched from the shell's context, so the context
 * is handed on.
 */
export default function DashboardTabs() {
  const { t } = useTranslation()
  return (
    <div className="space-y-4">
      <SubNavTabs items={navTabs('/dashboard')} label={t('layout.nav.dashboard')} />
      <Outlet context={useOutletContext()} />
    </div>
  )
}
