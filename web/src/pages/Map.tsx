import { Suspense, useEffect, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { Outlet, useMatch } from 'react-router-dom'
import { RefreshCw } from 'lucide-react'
import { ListLoadState } from '@/components/ListLoadState'
import { SubNavTabs } from '@/components/SubNavTabs'
import { Button } from '@/components/ui/button'
import { useServerScope } from '@/hooks/useServerScope'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'
import { navTabs } from '@/lib/navigation'
import { cn } from '@/lib/utils'
import type { MapContext } from './map/mapContext'
import { settleMovedView, useClusterBoard, useNodeBoard } from './running/useRunningBoard'

// The servers — which are online, and a Proxmox host's guests — are read again
// on the running board's timer; scans, ports and racks only when the operator asks.
const REFRESH_MS = 15_000

/**
 * The network map: what nmap found on the networks the servers are on, and
 * the racks they sit in. In a cluster it is every server's at once — the scan
 * of each network is asked of a server picked on the page, not of the server
 * the picker holds; on a single server it is that server's.
 */
export default function MapPage() {
  const { clusterEnabled } = useServerScope()
  // A network or rack view the running board kept may have sent the operator
  // here; now that they have arrived, the board is the board again.
  useEffect(() => { settleMovedView() }, [])
  if (clusterEnabled === true) return <ClusterMap />
  if (clusterEnabled === false) return <ServerMap />
  return <Frame><Loading /></Frame>
}

function Loading() {
  const { t } = useTranslation()
  return <p role="status" className="text-[13px] text-muted-foreground">{t('common.loading')}</p>
}

/**
 * The page's column and head. The devices read best at a column's width; a
 * row of racks takes all the width there is, as the running board's canvas does.
 */
function Frame({ subtitle, onRefresh, loading, children }: {
  subtitle?: string
  onRefresh?: () => void
  loading?: boolean
  children: ReactNode
}) {
  const { t } = useTranslation()
  const wide = useMatch('/map/rack') !== null
  return (
    <div className={cn('mx-auto space-y-4', !wide && 'max-w-[1400px]')}>
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h1 className="text-[22px] font-bold tracking-tight">{t('map.title')}</h1>
          {subtitle && <p className="mt-1 break-words text-[13px] text-muted-foreground">{subtitle}</p>}
        </div>
        {onRefresh && (
          <Button variant="outline" size="sm" className="rounded-xl" onClick={onRefresh} disabled={loading}>
            <RefreshCw className={cn(loading && 'animate-spin motion-reduce:animate-none')} aria-hidden="true" />
            {t('common.refresh')}
          </Button>
        )}
      </header>
      <SubNavTabs items={navTabs('/map')} label={t('map.title')} />
      {/* A tab is first drawn when the servers answer — a change of state, not a
          navigation — and its code is fetched then. Without a boundary of its own
          the whole panel, menu and all, would give way to the app's loader meanwhile. */}
      <Suspense fallback={<Loading />}>{children}</Suspense>
    </div>
  )
}

function ClusterMap() {
  const { t } = useTranslation()
  const { state, refresh } = useClusterBoard()
  const [round, setRound] = useState(0)
  const [query, setQuery] = useState('')
  useVisibleInterval(refresh, REFRESH_MS)

  // The shell took this panel for a cluster member and this load's status says otherwise.
  if (state.phase === 'standalone') return <ServerMap />
  const onRefresh = () => { refresh(); setRound((r) => r + 1) }
  return (
    <Frame subtitle={t('map.subtitleCluster')} onRefresh={onRefresh} loading={state.phase === 'ready' ? state.board.header.loading : state.phase === 'loading'}>
      {state.phase === 'ready' ? (
        <Outlet context={{ sections: state.board.sections, scope: 'cluster', round, query, setQuery } satisfies MapContext} />
      ) : (
        <ListLoadState loading={state.phase === 'loading'} error={state.phase === 'error' ? state.error : null} errorTitle={t('running.overviewError')} onRetry={refresh} skeletonCount={3} />
      )}
    </Frame>
  )
}

function ServerMap() {
  const { t } = useTranslation()
  const { board, refresh } = useNodeBoard()
  const [round, setRound] = useState(0)
  const [query, setQuery] = useState('')
  useVisibleInterval(refresh, REFRESH_MS)

  return (
    <Frame subtitle={t('map.subtitleServer')} onRefresh={() => { refresh(); setRound((r) => r + 1) }} loading={board.header.loading}>
      <Outlet context={{ sections: board.sections, scope: 'node', round, query, setQuery } satisfies MapContext} />
    </Frame>
  )
}
