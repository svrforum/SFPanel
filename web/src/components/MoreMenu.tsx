import { useEffect, useId, useState } from 'react'
import { Link, useNavigate, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { Drawer } from 'vaul'
import { ChevronDown, LogOut } from 'lucide-react'
import { NodeStatusMark } from '@/components/cluster/NodeStatusMark'
import { ServerList } from '@/components/server/ServerList'
import { serverRole } from '@/components/server/serverRole'
import { useServerScope } from '@/hooks/useServerScope'
import { BOTTOM_NAV_ITEMS, isTerminalRoute, moreMenuGroups, navOwner, type NavEntry } from '@/lib/navigation'
import { cn } from '@/lib/utils'
import { api } from '@/lib/api'

interface MoreMenuProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Opened from the terminal, which hides the bottom bar: its other tabs lead the drawer. */
  fromTerminal?: boolean
}

/**
 * The phone's menu beyond the bottom bar. In a cluster: the server the pages
 * act on, which opens the list to pick another in place; the pages for that
 * server under its name; the pages about all servers under theirs; then
 * settings, which belongs to neither, and logging out. On a single server
 * there is no other to tell its pages from: they share one grid, unheaded.
 */
export default function MoreMenu({ open, onOpenChange, fromTerminal }: MoreMenuProps) {
  const navigate = useNavigate()
  const { pathname } = useLocation()
  const { t } = useTranslation()
  const { clusterEnabled, current, currentName, switchTo, reloadNodes } = useServerScope()
  const cluster = clusterEnabled === true
  const [listOpen, setListOpen] = useState(false)
  const idBase = useId()
  const owner = navOwner(pathname)

  // The list shows who is in the cluster now: asked for again on every open.
  useEffect(() => {
    if (open) reloadNodes()
  }, [open, reloadNodes])

  const close = () => onOpenChange(false)

  const pick = (id: string) => {
    switchTo(id)
    setListOpen(false)
  }

  const handleLogout = () => {
    void api.logout()
    close()
    navigate('/login')
  }

  const tile = (entry: NavEntry) => {
    // The entry a page belongs to, tabs included: /processes lights System.
    // Marked by a bar and a heavier label as well as the tint (the colours
    // alone differ by 1.2:1 in lightness).
    const active = owner?.entry === entry
    return (
      <Link
        key={entry.to}
        to={entry.to}
        onClick={close}
        aria-current={active ? 'page' : undefined}
        className={cn(
          'flex flex-col items-center gap-1.5 rounded-xl py-3 px-1 transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring',
          active ? 'bg-primary/10 text-primary' : 'text-muted-foreground active:bg-secondary/80',
        )}
      >
        <entry.icon aria-hidden="true" className="h-5 w-5" />
        <span className={cn('text-[12px] leading-tight text-center', active ? 'font-semibold' : 'font-medium')}>
          {/* A bottom-bar tab by the bar's own name. */}
          {t(entry.mobileLabelKey ?? entry.labelKey)}
        </span>
        {active && <span aria-hidden="true" className="h-0.5 w-5 rounded-full bg-primary" />}
      </Link>
    )
  }
  const grid = (entries: NavEntry[]) => <div className="grid grid-cols-4 gap-2 py-2">{entries.map(tile)}</div>

  const groups = moreMenuGroups(cluster)
  const items = (group: NavEntry['group']) => groups.find((g) => g.group === group)?.items ?? []
  const section = (group: 'server' | 'fleet', title: string) => {
    const headingId = `${idBase}-${group}`
    return (
      <section aria-labelledby={headingId} className="pt-2">
        <h3 id={headingId} className="truncate px-1 text-[12px] font-semibold text-muted-foreground">{title}</h3>
        {grid(items(group))}
      </section>
    )
  }

  const role = serverRole(current, t)
  const state = current ? t(`cluster.status.${current.status}`, { defaultValue: current.status }) : ''
  const listId = `${idBase}-servers`

  return (
    <Drawer.Root open={open} onOpenChange={onOpenChange}>
      <Drawer.Portal>
        <Drawer.Overlay className="fixed inset-0 bg-black/40 z-50" />
        <Drawer.Content aria-describedby={undefined} className="fixed bottom-0 left-0 right-0 z-50 bg-card rounded-t-2xl outline-none">
          <div className="mx-auto w-12 h-1.5 rounded-full bg-muted-foreground/20 mt-3 mb-2" />
          <Drawer.Title className="sr-only">{t('layout.mobileNav.more')}</Drawer.Title>

          <div className="overflow-y-auto px-4 pb-safe" style={{ maxHeight: '70vh' }}>
            {cluster && (
              <div className="pb-2 mb-2 border-b border-border">
                <button
                  type="button"
                  onClick={() => setListOpen((o) => !o)}
                  aria-expanded={listOpen}
                  aria-controls={listOpen ? listId : undefined}
                  className="flex items-center gap-2 w-full px-3 py-2.5 rounded-xl bg-secondary/50 text-left transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring"
                >
                  <NodeStatusMark status={current?.status ?? ''} />
                  <span className="truncate text-[13px] font-medium">{currentName}</span>
                  {/* Online is the usual case: said to a screen reader only. */}
                  {state && (
                    <span className={current?.status === 'online' ? 'sr-only' : 'shrink-0 text-[12px] text-muted-foreground'}>{state}</span>
                  )}
                  {role && <span className="shrink-0 text-[12px] text-muted-foreground">· {role}</span>}
                  <ChevronDown aria-hidden="true" className={cn('h-3.5 w-3.5 text-muted-foreground ml-auto shrink-0 transition-transform', listOpen && 'rotate-180')} />
                </button>
                {listOpen && (
                  <div id={listId} className="mt-1 flex flex-col rounded-xl bg-secondary/30">
                    <ServerList onPick={pick} />
                  </div>
                )}
              </div>
            )}

            {/* The bottom bar's other tabs, which the terminal hides. */}
            {fromTerminal && (
              <div className="border-b border-border">
                {grid(BOTTOM_NAV_ITEMS.filter((e) => !isTerminalRoute(e.to)))}
              </div>
            )}

            {cluster ? (
              <>
                {section('server', currentName)}
                {section('fleet', t('layout.groups.fleet'))}
                <div className="mt-2 border-t border-border">{grid(items('tail'))}</div>
              </>
            ) : (
              grid(groups.flatMap((g) => g.items))
            )}

            <div className="border-t border-border mt-2 pt-2 pb-4">
              <button
                type="button"
                onClick={handleLogout}
                className="flex items-center gap-2 w-full rounded-xl py-3 px-4 text-destructive active:bg-secondary/80 transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                <LogOut aria-hidden="true" className="h-5 w-5" />
                <span className="text-[13px] font-medium">{t('layout.logout')}</span>
              </button>
            </div>
          </div>
        </Drawer.Content>
      </Drawer.Portal>
    </Drawer.Root>
  )
}
