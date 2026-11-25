import { useState, useEffect, lazy, Suspense } from 'react'
import { useTranslation } from 'react-i18next'
import { Navigate, useLocation, useSearchParams } from 'react-router-dom'
import { api } from '@/lib/api'
import { SETTINGS_TABS, settingsRedirect, settingsTab, settingsTabScope, type SettingsTab } from '@/lib/settingsTabs'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ListLoadState } from '@/components/ListLoadState'

// Tab panels are code-split: each tab pulls in its own state + handlers
// only when the user opens it, keeping the initial settings chunk small.
const General = lazy(() => import('@/pages/settings/General'))
const Security = lazy(() => import('@/pages/settings/Security'))
const Maintenance = lazy(() => import('@/pages/settings/Maintenance'))
const MovedSettings = lazy(() => import('@/pages/settings/MovedSettings'))
const TLSCertificate = lazy(() => import('@/pages/settings/TLSCertificate'))
const AlertSettings = lazy(() => import('@/pages/settings/AlertSettings'))
const Audit = lazy(() => import('@/pages/settings/Audit'))

const TAB_LABEL: Record<SettingsTab, string> = {
  account: 'settings.tabAccount',
  browser: 'settings.tabBrowser',
  system: 'settings.tabSystem',
  https: 'settings.tabHttps',
  alerts: 'settings.tabAlerts',
  audit: 'settings.tabAuditLog',
}

// The same six tabs in both modes. Where a tab's values are saved is said
// once, by the scope line above the page in a cluster, not here.
export default function Settings() {
  const { t } = useTranslation()
  const location = useLocation()
  const [searchParams, setSearchParams] = useSearchParams()
  // Starts from the mode the shell last read (Layout keeps it under this key)
  // and keeps it when this read fails: a failure used to lay a cluster
  // node's settings out as a standalone node's, with no word of it.
  const [clusterEnabled, setClusterEnabled] = useState(() => localStorage.getItem('sfpanel-cluster-mode') === 'true')
  const [statusError, setStatusError] = useState<string | null>(null)
  const [statusAttempt, setStatusAttempt] = useState(0)

  useEffect(() => {
    api.getClusterStatus()
      .then((s) => {
        setClusterEnabled(s.enabled)
        setStatusError(null)
      })
      .catch((err: unknown) => setStatusError(err instanceof Error ? err.message : String(err)))
  }, [statusAttempt])

  // The address is the open tab.
  const tab = settingsTab(location.search)
  const redirect = settingsRedirect(location.search)

  // Every other parameter stays, except the server on a tab no server holds
  // (the account, the language and theme). On a server's tab the shell puts
  // the shown server back.
  const handleTabChange = (value: string) => {
    const next = new URLSearchParams(searchParams)
    if (value === 'account') next.delete('tab')
    else next.set('tab', value)
    if (settingsTabScope(value as SettingsTab) !== 'server') next.delete('node')
    setSearchParams(next, { replace: true })
  }

  // An old ?scope= address opens the tab it meant, in its place.
  if (redirect !== null) return <Navigate replace to={{ pathname: '/settings', search: redirect }} />

  const fallback = (
    <div className="p-8 text-muted-foreground text-[13px]">{t('common.loading')}</div>
  )

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-[22px] font-bold tracking-tight">{t('settings.title')}</h1>
        <p className="text-[13px] text-muted-foreground mt-1">{t('settings.subtitle')}</p>
      </div>

      {statusError && (
        <ListLoadState
          loading={false}
          error={statusError}
          errorTitle={t('settings.clusterStatusFailed')}
          onRetry={() => { setStatusError(null); setStatusAttempt((n) => n + 1) }}
        />
      )}

      <Tabs value={tab} onValueChange={handleTabChange} className="w-full">
        {/* The list scrolls sideways on a phone; the tabs keep their width. */}
        <TabsList className="bg-secondary/50 rounded-xl p-1 h-auto">
          {SETTINGS_TABS.map((id) => (
            <TabsTrigger key={id} value={id} className="shrink-0 rounded-lg text-[13px] px-4 py-2">{t(TAB_LABEL[id])}</TabsTrigger>
          ))}
        </TabsList>

        <TabsContent value="account">
          <Suspense fallback={fallback}>{tab === 'account' && <Security />}</Suspense>
        </TabsContent>
        <TabsContent value="browser">
          <Suspense fallback={fallback}>{tab === 'browser' && <General />}</Suspense>
        </TabsContent>
        <TabsContent value="system">
          <Suspense fallback={fallback}>{tab === 'system' && (<><Maintenance clusterEnabled={clusterEnabled} /><MovedSettings /></>)}</Suspense>
        </TabsContent>
        <TabsContent value="https">
          <Suspense fallback={fallback}>{tab === 'https' && <TLSCertificate />}</Suspense>
        </TabsContent>
        <TabsContent value="alerts">
          <Suspense fallback={fallback}>{tab === 'alerts' && <AlertSettings />}</Suspense>
        </TabsContent>
        <TabsContent value="audit">
          <Suspense fallback={fallback}>{tab === 'audit' && <Audit />}</Suspense>
        </TabsContent>
      </Tabs>
    </div>
  )
}
