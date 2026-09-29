import { useState, useEffect, lazy, Suspense } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useSearchParams } from 'react-router-dom'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'

// Tab panels are code-split: each tab pulls in its own state + handlers
// only when the user opens it, keeping the initial settings chunk small.
const General = lazy(() => import('@/pages/settings/General'))
const Security = lazy(() => import('@/pages/settings/Security'))
const Maintenance = lazy(() => import('@/pages/settings/Maintenance'))
const Performance = lazy(() => import('@/pages/settings/Performance'))
const TLSCertificate = lazy(() => import('@/pages/settings/TLSCertificate'))
const AlertSettings = lazy(() => import('@/pages/settings/AlertSettings'))
const Audit = lazy(() => import('@/pages/settings/Audit'))

// Consolidated to 4 tabs (was 6): account merges security + general(language);
// system merges maintenance(update/backup) + performance(limits). Alerts and
// audit stay as-is.
const VALID_TABS = ['account', 'system', 'alerts', 'audit']

export default function Settings() {
  const { t } = useTranslation()
  const [searchParams, setSearchParams] = useSearchParams()
  const [clusterEnabled, setClusterEnabled] = useState(false)

  useEffect(() => {
    api.getClusterStatus(true)
      .then((s) => setClusterEnabled(s.enabled))
      .catch(() => {})
  }, [])

  // In cluster mode: filter tabs by context.
  //   ?scope=node → per-node SQLite settings: system (update/backup + terminal
  //                 timeout + upload limit, all node-local) and audit.
  //   otherwise   → cluster-wide settings: account only (password/2FA hit the
  //                 replicated FSM admin row; language is browser-local).
  //                 Single-node deployments show all 4 (no scope split).
  //
  // Alerts moved to the node scope because that is where they actually live.
  // alert_rules and alert_channels are ordinary local SQLite tables — nothing
  // in internal/cluster replicates them — and the evaluator reads the local
  // database every sixty seconds. Presenting them as cluster-wide meant a rule
  // created here existed on no other node, while the rule form's node picker
  // offered to target one of those other nodes, producing a rule that lived
  // where it could never be evaluated and reported itself Active forever.
  const scope = searchParams.get('scope')
  const isNodeScope = clusterEnabled && scope === 'node'
  const visibleTabs = clusterEnabled
    ? (isNodeScope ? ['system', 'alerts', 'audit'] : ['account'])
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
    if (value === visibleTabs[0]) {
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
        {/* In cluster mode the page has two halves and each entry point opens
            one of them: the desktop sidebar has a link to each, but a phone's
            More menu and the Android app reach only one, so the account half —
            password and 2FA — was unreachable from a phone. The page itself
            now offers both, whichever door it was opened by. */}
        {clusterEnabled && (
          <nav aria-label={t('settings.scopeLabel')} className="mt-4 grid grid-cols-2 gap-1 rounded-xl bg-secondary/50 p-1 sm:inline-grid sm:min-w-[28rem]">
            {[
              { node: false, to: '/settings', label: t('settings.scopeCluster'), hint: t('settings.scopeClusterHint') },
              { node: true, to: '/settings?scope=node', label: t('settings.scopeNode'), hint: t('settings.scopeNodeHint') },
            ].map((s) => {
              const current = s.node === isNodeScope
              return (
                <Link
                  key={s.to}
                  to={s.to}
                  aria-current={current ? 'page' : undefined}
                  className={cn(
                    'rounded-lg px-3 py-2 text-left transition-colors',
                    current ? 'bg-background shadow-sm' : 'text-muted-foreground hover:text-foreground',
                  )}
                >
                  <span className="block text-[13px] font-medium">{s.label}</span>
                  <span className="block text-[11px] text-muted-foreground">{s.hint}</span>
                </Link>
              )
            })}
          </nav>
        )}
      </div>

      <Tabs value={activeTab} onValueChange={handleTabChange} className="w-full">
        <TabsList className="bg-secondary/50 rounded-xl p-1 h-auto">
          {visibleTabs.includes('account') && <TabsTrigger value="account" className="rounded-lg text-[13px] px-4 py-2">{t('settings.tabAccount')}</TabsTrigger>}
          {visibleTabs.includes('system') && <TabsTrigger value="system" className="rounded-lg text-[13px] px-4 py-2">{t('settings.tabSystem')}</TabsTrigger>}
          {visibleTabs.includes('alerts') && <TabsTrigger value="alerts" className="rounded-lg text-[13px] px-4 py-2">{t('settings.tabAlerts')}</TabsTrigger>}
          {visibleTabs.includes('audit') && <TabsTrigger value="audit" className="rounded-lg text-[13px] px-4 py-2">{t('settings.tabAuditLog')}</TabsTrigger>}
        </TabsList>

        <TabsContent value="account">
          <Suspense fallback={fallback}>{activeTab === 'account' && (<><Security /><General /></>)}</Suspense>
        </TabsContent>
        <TabsContent value="system">
          <Suspense fallback={fallback}>{activeTab === 'system' && (<><TLSCertificate /><Maintenance clusterEnabled={clusterEnabled} /><Performance /></>)}</Suspense>
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
