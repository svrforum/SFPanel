import { useState, useEffect, useCallback, useLayoutEffect, useMemo, useRef } from 'react'
import { Outlet, useNavigate, useLocation } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'
import { documentTitle, isTerminalRoute, navOwner, pageTitleKey } from '@/lib/navigation'
import { selectedNodeName } from '@/lib/clusterNodes'
import { CLUSTER_MODE_KEY, LOCAL_ID_KEY } from '@/lib/serverScope'
import { keepTitlePrefix } from '@/lib/aiSessions'
import { useTerminalCompact, useTerminalFocus } from '@/lib/terminalLayout'
import { NODE_CHANGED } from '@/lib/nodeSelection'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'
import { useClusterUpdateCount } from '@/hooks/useClusterUpdateCount'
import { ServerScopeContext, useServerScopeState } from '@/hooks/useServerScope'
import { ErrorBoundary } from '@/components/ErrorBoundary'
import Sidebar from '@/components/Sidebar'
import SidebarSkeleton from '@/components/SidebarSkeleton'
import BottomNav from '@/components/BottomNav'
import MoreMenu from '@/components/MoreMenu'
import { ScopeBar } from '@/components/server/ScopeBar'
import type { ClusterStatus, DashboardOverview } from '@/types/api'

const SIDEBAR_KEY = 'sfpanel-sidebar-collapsed'

// Shared with pages via <Outlet context>. The overview payload Layout already
// fetches for the sidebar version display is tagged with the node scope and
// fetch time so the Dashboard can reuse it instead of issuing a duplicate
// /system/overview call on first entry. data === null means the fetch failed
// (consumers should fall back to their own fetch).
export interface LayoutOutletContext {
  overview: { data: DashboardOverview | null; node: string | null; at: number } | null
  /** Cluster name of the server the pages act on (api.currentNode, else the attached node); null standalone, before the node list, or when the list lacks it. */
  serverName: string | null
  /** The shell's belief about cluster mode: the last /cluster/status answer, seeded from localStorage; null before any. */
  clusterEnabled: boolean | null
  /** This page load's latest /cluster/status answer; null until one arrives. */
  clusterStatus: ClusterStatus | null
  /** Open the phone's More drawer; from the terminal it leads with the bottom bar's other tabs, which the terminal hides. */
  openMore: (opts?: { fromTerminal?: boolean }) => void
}

export default function Layout() {
  const navigate = useNavigate()
  const { t } = useTranslation()
  const [collapsed, setCollapsed] = useState(() => {
    return localStorage.getItem(SIDEBAR_KEY) === 'true'
  })
  const [updateAvailable, setUpdateAvailable] = useState(false)
  const [panelVersion, setPanelVersion] = useState('')
  const [overview, setOverview] = useState<LayoutOutletContext['overview']>(null)
  const [nodeKey, setNodeKey] = useState(0)
  const [more, setMore] = useState({ open: false, fromTerminal: false })
  const openMore = useCallback((opts?: { fromTerminal?: boolean }) => setMore({ open: true, fromTerminal: opts?.fromTerminal === true }), [])
  // Closing keeps where it was opened from, so the drawer does not change as it slides away.
  const setMoreOpen = useCallback((open: boolean) => setMore((m) => ({ ...m, open })), [])
  // null = mode not yet known. Seeded from the last answer so a reload draws
  // the menu for its mode at once, instead of a single server's menu that
  // turns into the cluster's when /cluster/status replies.
  const [clusterEnabled, setClusterEnabled] = useState<boolean | null>(() => {
    const saved = localStorage.getItem(CLUSTER_MODE_KEY)
    return saved === null ? null : saved === 'true'
  })
  const [clusterStatus, setClusterStatus] = useState<ClusterStatus | null>(null)
  // The server the pages act on, for every surface that shows or changes it,
  // and kept in the address. The overview's hostname names the attached
  // server while no node list does.
  const hostname = overview?.node === api.currentNode ? overview?.data?.host?.hostname ?? '' : ''
  const scope = useServerScopeState({ clusterEnabled, status: clusterStatus, hostname })
  // The cluster's counterpart of a single server's update badge.
  const updateCount = useClusterUpdateCount(clusterEnabled === true)
  const location = useLocation()
  const isTerminal = isTerminalRoute(location.pathname)
  // The terminal has the window to itself on a touch device, and on a desktop
  // when the operator chose focus: the sidebar stays mounted and is only not
  // shown.
  const terminalCompact = useTerminalCompact()
  const [terminalFocus] = useTerminalFocus()
  const sidebarHidden = isTerminal && (terminalCompact || terminalFocus)

  // A server the cluster does not have was replaced by the attached one. The
  // line above the page says so; the terminal draws no such line, so there it
  // is said once, when it happens.
  const shownMissing = useRef(scope.missing)
  useEffect(() => {
    if (scope.missing === shownMissing.current) return
    shownMissing.current = scope.missing
    if (scope.missing && isTerminal) toast.warning(`${t('layout.scope.notFound')} · ${scope.missing.name}`)
  }, [scope.missing, isTerminal, t])

  // The page's own name in the tab and the history list: it said "SFPanel" on
  // every route. A menu entry's pages carry the entry's name, except a tab
  // that is a screen of its own.
  const owner = navOwner(location.pathname)
  const titleKey = pageTitleKey(location.pathname)
  const pageLabel = titleKey ? t(titleKey) : location.pathname === '/' ? null : t('layout.notFound.title')
  // In a cluster, a server page also names the server it acts on: the one
  // picked, else the one this panel runs on. The pages about all servers and
  // settings act on no one server, and a server whose name the list has not
  // given is left out: the picker's stand-in name (the hostname, the id) is
  // not the cluster's.
  const serverName = clusterEnabled === true ? selectedNodeName(scope.nodes, scope.selected, scope.localId) : null
  const serverInTitle = owner?.entry.scope === 'server'
  const pageTitle = documentTitle(pageLabel, serverInTitle ? serverName : null)
  // Twice, around the terminal page's own effect, which prefixes "(n) " to the
  // title it finds and takes the prefix off again on cleanup. The layout
  // effect runs before any page's passive effect in the same commit, so the
  // page finds this title; the passive one runs after the page's cleanup on
  // the way out, so the next page has its own name whatever that cleanup left.
  // On the terminal the layout effect keeps the page's count: the server's
  // name arrives with the node list, after the page has counted.
  useLayoutEffect(() => {
    document.title = isTerminal ? keepTitlePrefix(document.title, pageTitle) : pageTitle
  }, [pageTitle, isTerminal])
  useEffect(() => {
    if (!isTerminal) document.title = pageTitle
  }, [pageTitle, isTerminal])

  // Moving to another section takes focus to the new view, as a page load
  // would: it was left on the link in the sidebar or on <body>, so a keyboard
  // or screen-reader user started from the top of the menu again. Not on the
  // first load, not for a tab or a query string within one menu entry (the
  // tab just pressed keeps focus), and not when the page already put focus
  // somewhere inside the view itself (the terminal, an autofocused field).
  const mainRef = useRef<HTMLElement>(null)
  const sectionKey = owner?.entry.to ?? location.pathname
  const shownSection = useRef(sectionKey)
  useEffect(() => {
    if (shownSection.current === sectionKey) return
    shownSection.current = sectionKey
    const main = mainRef.current
    if (main && !main.contains(document.activeElement)) main.focus({ preventScroll: true })
  }, [sectionKey])

  useEffect(() => {
    const handler = () => setNodeKey((k) => k + 1)
    window.addEventListener(NODE_CHANGED, handler)
    return () => window.removeEventListener(NODE_CHANGED, handler)
  }, [])

  // Latched once the server definitively says "not clustered". A standalone
  // node's answer can't change while the page lives — cluster init/join
  // restarts the process, which reloads the SPA — so polling on forever would
  // be pure noise for the majority of installs.
  const notClustered = useRef(false)

  const loadClusterStatus = useCallback(() => {
    if (notClustered.current) return
    api.getClusterStatus()
      .then((status) => {
        if (!status.enabled) notClustered.current = true
        setClusterStatus(status)
        setClusterEnabled(status.enabled)
        localStorage.setItem(CLUSTER_MODE_KEY, String(status.enabled))
        // Kept so the next load can tell this server's own id in the address
        // before this answer comes.
        if (status.enabled && status.local_id) localStorage.setItem(LOCAL_ID_KEY, status.local_id)
        else if (status.enabled === false) localStorage.removeItem(LOCAL_ID_KEY)
      })
      .catch(() => {
        // Keep the mode we already believe — a transient failure must not tear
        // the sidebar down. Only a definitive answer switches which one renders.
        setClusterEnabled((prev) => prev ?? false)
      })
  }, [])

  // The shell's one source of cluster status: the menu, the server picker and
  // the pages read this answer instead of asking again, so a first paint waits
  // on one round-trip, not a chain of them. Polled, so a leader change shows
  // in the server picker.
  useVisibleInterval(loadClusterStatus, 15000)
  // A change the page made itself (a new leader) shows now, not at the next poll.
  useEffect(() => {
    window.addEventListener('sfpanel:cluster-changed', loadClusterStatus)
    return () => window.removeEventListener('sfpanel:cluster-changed', loadClusterStatus)
  }, [loadClusterStatus])

  useEffect(() => {
    localStorage.setItem(SIDEBAR_KEY, String(collapsed))
  }, [collapsed])

  useEffect(() => {
    // The dashboard overview already carries panel version + update info,
    // so this one call covers what Layout used to do with two: getSystemInfo
    // + checkUpdate. Eliminates one GitHub round-trip per dashboard mount
    // (checkUpdate hits the release index).
    // Again on every node switch: the version shown is the open node's, and
    // the Dashboard reuses this payload only for the node it was fetched for.
    // An answer for the node left behind is dropped.
    let stale = false
    const node = api.currentNode
    api.getDashboardOverview()
      .then((data) => {
        if (stale) return
        setPanelVersion(data.version || '')
        setUpdateAvailable(!!data.update_info?.update_available)
        setOverview({ data, node, at: Date.now() })
      })
      .catch(() => {
        if (stale) return
        // Not the previous node's version under this one's name.
        setPanelVersion('')
        setUpdateAvailable(false)
        setOverview({ data: null, node, at: Date.now() })
      })
    return () => { stale = true }
  }, [nodeKey])

  const outletContext = useMemo<LayoutOutletContext>(
    () => ({ overview, serverName, clusterEnabled, clusterStatus, openMore }),
    [overview, serverName, clusterEnabled, clusterStatus, openMore],
  )

  // Track the visual viewport height so the app shell shrinks when the mobile
  // soft keyboard opens, instead of staying 100vh and pushing content (terminal
  // input, bottom nav) under the keyboard — which makes the whole page scroll.
  // Falls back to 100dvh via CSS until/if visualViewport is unavailable.
  useEffect(() => {
    const vv = window.visualViewport
    if (!vv) return
    const apply = () => document.documentElement.style.setProperty('--app-h', `${vv.height}px`)
    apply()
    vv.addEventListener('resize', apply)
    vv.addEventListener('scroll', apply)
    // Some mobile browsers (notably Samsung Internet) report a stale viewport
    // height right after a load that happens while the soft keyboard is already
    // open, and never fire a corrected 'resize' — leaving --app-h (and the
    // terminal sized off it) wrong, which shows as a big blank gap. Re-read a
    // few times so it settles to the real value.
    const timers = [150, 350, 700, 1200].map((d) => window.setTimeout(apply, d))
    return () => {
      vv.removeEventListener('resize', apply)
      vv.removeEventListener('scroll', apply)
      timers.forEach(clearTimeout)
    }
  }, [])

  // Under the menu: what is behind leads to where it is updated. A cluster
  // counts its servers behind and sends to the overview, whose banner counts
  // the same; a single server sends to its own update.
  const update = clusterEnabled === true
    ? (updateCount > 0 ? { label: t('layout.updateCount', { count: updateCount }), to: '/cluster/overview' } : null)
    : (updateAvailable ? { label: t('layout.updateAvailable'), to: '/settings?tab=system' } : null)
  const versionTo = clusterEnabled === true ? '/settings?scope=node&tab=system' : '/settings?tab=system'

  const handleLogout = () => {
    // Fire-and-forget — even if the server is unreachable we still want to
    // navigate away. api.logout() clears local state on its own.
    void api.logout()
    navigate('/login')
  }

  return (
    <ServerScopeContext.Provider value={scope}>
    <div className="flex overflow-hidden bg-background" style={{ height: 'var(--app-h, 100dvh)' }}>
      {/* First thing a keyboard reaches: past the whole menu to the view. */}
      <a
        href="#main-content"
        onClick={(e) => { e.preventDefault(); mainRef.current?.focus() }}
        className="sr-only focus:not-sr-only focus:fixed focus:left-3 focus:top-3 focus:z-[100] focus:rounded-xl focus:bg-card focus:px-4 focus:py-2.5 focus:text-[13px] focus:font-medium focus:text-foreground focus:shadow-lg focus:outline-2 focus:outline-ring"
      >
        {t('layout.skipToContent')}
      </a>
      {/* First visit only: no remembered mode yet, so which menu to draw
          cannot be known without guessing. Hold the slot rather than
          collapsing it. */}
      {clusterEnabled === null && !sidebarHidden ? <SidebarSkeleton /> : (
        <div className={cn('hidden h-full shrink-0', !sidebarHidden && 'md:flex')}>
          <Sidebar
            collapsed={collapsed}
            onToggleCollapsed={() => setCollapsed((c) => !c)}
            version={panelVersion}
            update={update}
            versionTo={versionTo}
            onLogout={handleLogout}
          />
        </div>
      )}

      <main id="main-content" ref={mainRef} tabIndex={-1} className={cn(
        // min-w-0 lets this flex child shrink below its content width, and
        // overflow-x-hidden stops an over-wide child (e.g. a non-wrapping
        // toolbar) from making the WHOLE page slide left/right on mobile.
        // Vertical scroll stays; intended horizontal-scroll rows keep their own
        // overflow-x-auto so they still scroll internally.
        // tabIndex -1 takes programmatic focus (the skip link, a section
        // change) without a ring: nothing to press is shown focused.
        "flex-1 min-h-0 min-w-0 outline-none",
        isTerminal ? "p-0 overflow-hidden" : "overflow-y-auto overflow-x-hidden px-5 py-4 pb-bottom-nav md:p-8 md:pb-8"
      )}>
        <ScopeBar />
        {/* The nodeKey key moved from <Outlet> to the boundary: remounting the
            boundary remounts the outlet tree identically, and also clears any
            caught error when the user switches nodes. The boundary itself keeps
            a page crash from blanking the whole shell (sidebar/nav stay up). */}
        <ErrorBoundary key={nodeKey}>
          <Outlet context={outletContext} />
        </ErrorBoundary>
      </main>

      <BottomNav onMorePress={() => openMore()} />
      <MoreMenu open={more.open} fromTerminal={more.fromTerminal} onOpenChange={setMoreOpen} />
    </div>
    </ServerScopeContext.Provider>
  )
}
