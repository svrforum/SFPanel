import type { ReactNode } from 'react'
import { useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { AlertTriangle, ChevronDown } from 'lucide-react'
import { ServerSwitcher } from '@/components/server/ServerSwitcher'
import { serverRole } from '@/components/server/serverRole'
import { useServerScope } from '@/hooks/useServerScope'
import { isTerminalRoute } from '@/lib/navigation'
import { scopeKind } from '@/lib/serverScope'
import { cn } from '@/lib/utils'

/**
 * One line above every page in a cluster, saying what the page acts on: the
 * server it names, as the button that picks another (outlined when it is not
 * the server this panel runs on); all servers, with how many answer; or where
 * a setting is saved. A server the address or the store named that the
 * cluster does not have is said to be unknown, so the server shown in its
 * place never changes without a word. Drawn in the Android app too. Not on
 * the terminal, whose rows it would take, nor on a single server, which has
 * nothing to tell apart. Sticky only where a window has the room.
 */
export function ScopeBar() {
  const { t } = useTranslation()
  const { pathname, search } = useLocation()
  const { clusterEnabled, nodes, selected, current, currentName, missing } = useServerScope()
  const kind = scopeKind(pathname, search)
  if (clusterEnabled !== true || isTerminalRoute(pathname) || kind === null) return null

  let scope: ReactNode
  if (kind === 'server') {
    const remote = selected !== null
    const label = [t('layout.scope.server', { name: currentName }), serverRole(current, t)].filter(Boolean).join(' · ')
    scope = (
      <ServerSwitcher>
        <button
          type="button"
          data-remote={remote ? 'true' : 'false'}
          className={cn(
            'inline-flex min-w-0 max-w-full items-center gap-1.5 rounded-lg border px-2.5 py-1 font-medium text-foreground transition-colors hover:bg-accent outline-none focus-visible:ring-2 focus-visible:ring-ring',
            remote ? 'border-warning bg-warning/10' : 'border-border',
          )}
        >
          <span className="truncate">{label}</span>
          <ChevronDown aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        </button>
      </ServerSwitcher>
    )
  } else if (kind === 'fleet') {
    // Counted from the node list; before it comes, only what the page is about.
    const online = nodes.filter((n) => n.status === 'online').length
    scope = <p>{nodes.length > 0 ? t('layout.scope.fleet', { total: nodes.length, online }) : t('layout.groups.fleet')}</p>
  } else {
    scope = <p>{kind === 'savedFleet' ? t('layout.scope.savedFleet') : t('layout.scope.savedServer', { name: currentName })}</p>
  }

  // Edge to edge across the view's padding (Layout's <main>: px-5 py-4, md:p-8).
  // A sticky box sticks inside its scroller's padding, so top is that padding
  // taken back: top-0 would hold it 2rem down, over the page's first line.
  return (
    <div
      data-scope-bar
      className="z-10 -mx-5 -mt-4 mb-4 flex min-h-11 flex-wrap items-center gap-x-3 gap-y-1 border-b border-border bg-background px-5 py-1.5 text-[13px] text-muted-foreground md:sticky md:-top-8 md:-mx-8 md:-mt-8 md:mb-6 md:px-8"
    >
      {scope}
      {/* Always in place, empty until needed: a screen reader hears a notice
          only from a region that was there before it. */}
      <p role="status" className="flex min-w-0 items-center gap-1.5 font-medium text-warning">
        {missing && (
          <>
            <AlertTriangle aria-hidden="true" className="h-3.5 w-3.5 shrink-0" />
            <span className="truncate">{`${t('layout.scope.notFound')} · ${missing.name}`}</span>
          </>
        )}
      </p>
    </div>
  )
}
