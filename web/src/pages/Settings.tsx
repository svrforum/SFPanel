import { useState, useEffect, lazy, Suspense } from 'react'
import { useTranslation } from 'react-i18next'
import { useSearchParams } from 'react-router-dom'
import { api } from '@/lib/api'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

// Tab panels are code-split: each tab pulls in its own state + handlers
// only when the user opens it, keeping the initial settings chunk small.
const General = lazy(() => import('@/pages/settings/General'))
const Security = lazy(() => import('@/pages/settings/Security'))
const Maintenance = lazy(() => import('@/pages/settings/Maintenance'))
const Performance = lazy(() => import('@/pages/settings/Performance'))
const AlertSettings = lazy(() => import('@/pages/settings/AlertSettings'))
const Audit = lazy(() => import('@/pages/settings/Audit'))

const VALID_TABS = ['general', 'security', 'system', 'tuning', 'alerts', 'audit']

export default function Settings() {
  const { t } = useTranslation()
  const [searchParams, setSearchParams] = useSearchParams()
  const [clusterEnabled, setClusterEnabled] = useState(false)

  useEffect(() => {
    api.getClusterStatus(true)
      .then((s) => setClusterEnabled(s.enabled))
      .catch(() => {})
  }, [])

  // In cluster mode: filter tabs based on context.
  //   ?scope=node → tabs that hit per-node SQLite (system/tuning/audit).
  //                 Terminal timeout + max upload size now live on the
  //                 tuning tab because those rows are also per-node (the
  //                 settings table isn't FSM-replicated).
  //   otherwise   → tabs that are truly cluster-global (replicated FSM
  //                 state or browser-local): general (language), security
  //                 (password/2FA hit the FSM admin row), alerts.
  // Single-node deployments show everything since there's no scope split.
  const scope = searchParams.get('scope')
  const isNodeScope = clusterEnabled && scope === 'node'
  const visibleTabs = clusterEnabled
    ? (isNodeScope ? ['system', 'tuning', 'audit'] : ['general', 'security', 'alerts'])
    : VALID_TABS

  const defaultTab = visibleTabs[0]
  const requestedTab = searchParams.get('tab') || ''
  const initialTab = visibleTabs.includes(requestedTab) ? requestedTab : defaultTab
  const [activeTab, setActiveTab] = useState(initialTab)

  // Sync active tab when visible tabs change (cluster status loaded).
  // setState inside an effect is intentional here: this is a derived
  // correction — when the cluster check resolves, the previously chosen
  // activeTab may no longer be in visibleTabs, so we move it. A render-
  // phase derivation would still need to call setState to persist the
  // chosen tab across handleTabChange clicks.
  useEffect(() => {
    if (!visibleTabs.includes(activeTab)) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setActiveTab(visibleTabs[0])
    }
  }, [visibleTabs.join(',')]) // eslint-disable-line react-hooks/exhaustive-deps

  const handleTabChange = (value: string) => {
    setActiveTab(value)
    if (value === 'general') {
      searchParams.delete('tab')
    } else {
      searchParams.set('tab', value)
    }
    setSearchParams(searchParams, { replace: true })
  }

  const fallback = (
    <div className="p-8 text-muted-foreground text-[13px]">{t('common.loading')}</div>
  )

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-[22px] font-bold tracking-tight">{t('settings.title')}</h1>
        <p className="text-[13px] text-muted-foreground mt-1">{t('settings.subtitle')}</p>
      </div>

      <Tabs value={activeTab} onValueChange={handleTabChange} className="w-full">
        <TabsList className="bg-secondary/50 rounded-xl p-1 h-auto">
          {visibleTabs.includes('general') && <TabsTrigger value="general" className="rounded-lg text-[13px] px-4 py-2">{t('settings.tabGeneral')}</TabsTrigger>}
          {visibleTabs.includes('security') && <TabsTrigger value="security" className="rounded-lg text-[13px] px-4 py-2">{t('settings.tabSecurity')}</TabsTrigger>}
          {visibleTabs.includes('system') && <TabsTrigger value="system" className="rounded-lg text-[13px] px-4 py-2">{t('settings.tabSystem')}</TabsTrigger>}
          {visibleTabs.includes('tuning') && <TabsTrigger value="tuning" className="rounded-lg text-[13px] px-4 py-2">{t('settings.tabTuning')}</TabsTrigger>}
          {visibleTabs.includes('alerts') && <TabsTrigger value="alerts" className="rounded-lg text-[13px] px-4 py-2">{t('settings.tabAlerts')}</TabsTrigger>}
          {visibleTabs.includes('audit') && <TabsTrigger value="audit" className="rounded-lg text-[13px] px-4 py-2">{t('settings.tabAuditLog')}</TabsTrigger>}
        </TabsList>

        <TabsContent value="general">
          <Suspense fallback={fallback}>{activeTab === 'general' && <General />}</Suspense>
        </TabsContent>
        <TabsContent value="security">
          <Suspense fallback={fallback}>{activeTab === 'security' && <Security />}</Suspense>
        </TabsContent>
        <TabsContent value="system">
          <Suspense fallback={fallback}>{activeTab === 'system' && <Maintenance clusterEnabled={clusterEnabled} />}</Suspense>
        </TabsContent>
        <TabsContent value="tuning">
          <Suspense fallback={fallback}>{activeTab === 'tuning' && <Performance />}</Suspense>
        </TabsContent>
        <TabsContent value="alerts">
          <Suspense fallback={fallback}>{activeTab === 'alerts' && <AlertSettings />}</Suspense>
        </TabsContent>
        <TabsContent value="audit">
          <Suspense fallback={fallback}>{activeTab === 'audit' && <Audit />}</Suspense>
        </TabsContent>
      </Tabs>
    </div>
  )
}
