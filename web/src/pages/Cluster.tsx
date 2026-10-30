import { useEffect, useRef } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { CLUSTER_TAB_ITEMS } from '@/components/cluster/datacenterMenu'

export default function Cluster() {
  const { t } = useTranslation()
  const { pathname } = useLocation()
  const row = useRef<HTMLDivElement>(null)

  // The row hides its scrollbar, so a tab opened from a link or a bookmark
  // could sit cut off past the edge with nothing to say the row scrolls.
  useEffect(() => {
    row.current?.querySelector<HTMLElement>('[aria-current="page"]')?.scrollIntoView({ block: 'nearest', inline: 'nearest' })
  }, [pathname])

  return (
    <div className="space-y-6">
      <h1 className="text-[22px] font-bold tracking-tight">{t('cluster.title')}</h1>
      {/* Four tabs do not fit a phone's width: the row scrolls rather than cutting the last off. */}
      <div ref={row} className="flex items-center gap-1 overflow-x-auto no-scrollbar">
        {CLUSTER_TAB_ITEMS.map((tab) => (
          <NavLink
            key={tab.to}
            to={tab.to}
            className={({ isActive }) =>
              cn(
                'flex shrink-0 items-center gap-2 px-4 py-2 rounded-xl whitespace-nowrap text-[13px] font-medium transition-colors outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/40',
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
      <Outlet />
    </div>
  )
}
