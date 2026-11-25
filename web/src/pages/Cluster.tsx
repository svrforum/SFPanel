import { useEffect, useRef } from 'react'
import { NavLink, Navigate, Outlet, useLocation, useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { CLUSTER_TAB_ITEMS } from '@/components/cluster/datacenterMenu'
import type { LayoutOutletContext } from '@/components/Layout'

export default function Cluster() {
  const { t } = useTranslation()
  const { pathname } = useLocation()
  const ctx = useOutletContext<LayoutOutletContext | undefined>()
  // The tab row, only while the shell believes this panel is in a cluster: a
  // standalone one has the form alone.
  const tabs = ctx?.clusterEnabled === true
  const row = useRef<HTMLDivElement>(null)

  // The row hides its scrollbar, so a tab opened from a link or a bookmark
  // could sit cut off past the edge with nothing to say the row scrolls.
  // Again when the row appears: with no saved mode it waits for the status.
  useEffect(() => {
    row.current?.querySelector<HTMLElement>('[aria-current="page"]')?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
  }, [pathname, tabs])

  // A standalone panel has a cluster to create or join and nothing else: the
  // other tabs each said only "Cluster not enabled". Only a definite answer
  // sends it there — a cluster node whose status read is late or fails keeps
  // the page it asked for. The fleet board has one server's counterpart.
  // `enabled: false` also comes from a member whose cluster failed to start
  // (`configured: true`); the overview is right for it too, since it says the
  // cluster is not running instead of offering create and join.
  const standalone = ctx?.clusterStatus?.enabled === false
  if (standalone && pathname !== '/cluster/overview') {
    return <Navigate to={pathname === '/cluster/running' ? '/running' : '/cluster/overview'} replace />
  }

  return (
    <div className="space-y-6">
      <h1 className="text-[22px] font-bold tracking-tight">{t('cluster.title')}</h1>
      {/* The tabs do not fit a phone's width: the row scrolls rather than cutting the last off. */}
      {tabs && (
        <div ref={row} className="flex items-center gap-1 overflow-x-auto no-scrollbar">
          {CLUSTER_TAB_ITEMS.map((tab) => (
            <NavLink
              key={tab.to}
              to={tab.to}
              className={({ isActive }) =>
                cn(
                  'flex shrink-0 items-center gap-2 px-4 py-2 rounded-xl whitespace-nowrap text-[13px] font-medium transition-colors outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring',
                  isActive
                    ? 'bg-primary/10 text-primary'
                    : 'text-muted-foreground hover:bg-accent hover:text-foreground'
                )
              }
            >
              <tab.icon className="h-4 w-4" />
              {t(tab.labelKey)}
            </NavLink>
          ))}
        </div>
      )}
      <Outlet />
    </div>
  )
}
