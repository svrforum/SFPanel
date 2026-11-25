import { useState, useEffect, useCallback, useLayoutEffect, useMemo, useRef } from 'react'
import { Link, NavLink, Outlet, useNavigate, useLocation } from 'react-router-dom'
import { LogOut, PanelLeftClose, PanelLeftOpen, Coffee, ArrowUpCircle } from 'lucide-react'

import GithubIcon from '@/components/GithubIcon'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'
import { NAV_ITEMS, documentTitle, isServerSection, isTerminalRoute } from '@/lib/navigation'
import { selectedNodeName } from '@/lib/clusterNodes'
import { keepTitlePrefix } from '@/lib/aiSessions'
import { CLUSTER_TAB_ITEMS } from '@/components/cluster/datacenterMenu'
import { useTerminalCompact, useTerminalFocus } from '@/lib/terminalLayout'
import { NODE_CHANGED, type NodeChangedDetail } from '@/lib/nodeSelection'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'
import { ErrorBoundary } from '@/components/ErrorBoundary'
import ClusterSidebar from '@/components/cluster/ClusterSidebar'
import SidebarSkeleton from '@/components/SidebarSkeleton'
import Logo, { LogoMark } from '@/components/Logo'
import BottomNav from '@/components/BottomNav'
import MoreMenu from '@/components/MoreMenu'
import type { ClusterNode, ClusterStatus, DashboardOverview } from '@/types/api'

const navItems = NAV_ITEMS

const SIDEBAR_KEY = 'sfpanel-sidebar-collapsed'
const CLUSTER_MODE_KEY = 'sfpanel-cluster-mode'

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
  const [moreOpen, setMoreOpen] = useState(false)
  // null = mode not yet known. Seeded from the last answer so a reload draws
  // the correct sidebar immediately instead of assuming standalone, painting
  // the standard sidebar, then swapping it for the cluster one once
  // /cluster/status replies — that swap left the shell with NO sidebar while
  // ClusterSidebar waited on its own probe.
  const [clusterEnabled, setClusterEnabled] = useState<boolean | null>(() => {
    const saved = localStorage.getItem(CLUSTER_MODE_KEY)
    return saved === null ? null : saved === 'true'
  })
  const [clusterStatus, setClusterStatus] = useState<ClusterStatus | null>(null)
  // The tree's node list, for the name of the server the pages act on.
  const [clusterNodes, setClusterNodes] = useState<ClusterNode[]>([])
  const location = useLocation()
  const isTerminal = isTerminalRoute(location.pathname)
  // The terminal has the window to itself on a touch device, and on a desktop
  // when the operator chose focus: the sidebars stay mounted (their state and
  // the cluster tree's polling survive) and are only not shown.
  const terminalCompact = useTerminalCompact()
  const [terminalFocus] = useTerminalFocus()
  const sidebarHidden = isTerminal && (terminalCompact || terminalFocus)

  // The page's own name in the tab and the history list: it said "SFPanel" on
  // every route. A section's sub-pages carry the section's name, except a
  // cluster tab that is a screen of its own.
  const within = (to: string) => location.pathname === to || location.pathname.startsWith(to + '/')
  const section = navItems.find((i) => within(i.to))
  const ownTab = CLUSTER_TAB_ITEMS.find((i) => i.ownTitle && within(i.to))
  const pageLabel = ownTab ? t(ownTab.labelKey) : section ? t(section.labelKey) : location.pathname === '/' ? null : t('layout.notFound.title')
  // In a cluster, a server page also names the server it acts on: the one
  // picked, else the one this panel runs on. Read at render — a node switch
  // re-renders through nodeKey. The cluster's own screens and settings act on
  // no one server, and a server whose name the list has not given is left out.
  const serverName = clusterEnabled === true ? selectedNodeName(clusterNodes, api.currentNode, clusterStatus?.local_id ?? '') : null
  const serverInTitle = !!section && !ownTab && isServerSection(section.to)
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
  // first load, not for a sub-tab or a query string within one section (the
  // tab just pressed keeps focus), and not when the page already put focus
  // somewhere inside the view itself (the terminal, an autofocused field).
  const mainRef = useRef<HTMLElement>(null)
  const sectionKey = section?.to ?? location.pathname
  const shownSection = useRef(sectionKey)
  useEffect(() => {
    if (shownSection.current === sectionKey) return
    shownSection.current = sectionKey
    const main = mainRef.current
    if (main && !main.contains(document.activeElement)) main.focus({ preventScroll: true })
  }, [sectionKey])

  const handleNodeChanged = useCallback((nodeId: string | null) => {
    setNodeKey((k) => k + 1)
    window.dispatchEvent(new CustomEvent<NodeChangedDetail>(NODE_CHANGED, { detail: { nodeId } }))
  }, [])

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
      })
      .catch(() => {
        // Keep the mode we already believe — a transient failure must not tear
        // the sidebar down. Only a definitive answer switches which one renders.
        setClusterEnabled((prev) => prev ?? false)
      })
  }, [])

  // Single source of cluster status for the shell: ClusterSidebar used to run
  // its own copy of this call, so first paint cost two *sequential* round-trips
  // (Layout's, then the sidebar's) before anything could render. Polling (vs
  // the previous one-shot) keeps leader/stale changes visible in the tree.
  useVisibleInterval(loadClusterStatus, 15000)
  // A change the page made itself (a rename) shows now, not at the next poll.
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
    () => ({ overview, serverName, clusterEnabled, clusterStatus }),
    [overview, serverName, clusterEnabled, clusterStatus],
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

  const versionState = updateAvailable ? t('layout.updateAvailable') : panelVersion ? t('layout.upToDate') : ''

  const handleLogout = () => {
    // Fire-and-forget — even if the server is unreachable we still want to
    // navigate away. api.logout() clears local state on its own.
    void api.logout()
    navigate('/login')
  }

  return (
    <div className="flex overflow-hidden bg-background" style={{ height: 'var(--app-h, 100dvh)' }}>
      {/* First thing a keyboard reaches: past the whole menu to the view. */}
      <a
        href="#main-content"
        onClick={(e) => { e.preventDefault(); mainRef.current?.focus() }}
        className="sr-only focus:not-sr-only focus:fixed focus:left-3 focus:top-3 focus:z-[100] focus:rounded-xl focus:bg-card focus:px-4 focus:py-2.5 focus:text-[13px] focus:font-medium focus:text-foreground focus:shadow-lg focus:outline-2 focus:outline-ring"
      >
        {t('layout.skipToContent')}
      </a>
      {/* First visit only: no remembered mode yet, so neither sidebar can be
          chosen without guessing. Hold the slot rather than collapsing it. */}
      {clusterEnabled === null && !sidebarHidden && <SidebarSkeleton />}

      {/* Cluster dual-panel sidebar */}
      {clusterEnabled === true && (
        <div className={cn('hidden h-full shrink-0', !sidebarHidden && 'md:flex')}>
          <ClusterSidebar
            status={clusterStatus}
            panelVersion={panelVersion}
            onLogout={handleLogout}
            onNodeChanged={handleNodeChanged}
            onNodes={setClusterNodes}
          />
        </div>
      )}

      {/* Standard sidebar (non-cluster mode) */}
      {clusterEnabled === false && <aside className={cn(
        'bg-card border-r border-border flex-col transition-[width] duration-300 ease-in-out shrink-0 hidden h-full',
        !sidebarHidden && 'md:flex',
        collapsed ? 'w-[68px]' : 'w-60'
      )}>
        <div className={cn('flex items-center', collapsed ? 'px-3 py-6 justify-center' : 'px-4 py-4')}>
          {collapsed ? (
            <Link to="/dashboard" aria-label="SFPanel" className="rounded-lg focus:outline-none focus-visible:ring-2 focus-visible:ring-ring">
              <LogoMark className="h-8 w-8" />
            </Link>
          ) : (
            <Link to="/dashboard" aria-label="SFPanel" className="block w-full rounded-lg focus:outline-none focus-visible:ring-2 focus-visible:ring-ring">
              <Logo />
            </Link>
          )}
        </div>

        <nav className={cn('flex-1 min-h-0 overflow-y-auto no-scrollbar space-y-0.5', collapsed ? 'px-2' : 'px-3')}>
          {navItems.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              title={collapsed ? t(item.labelKey) : undefined}
              className={({ isActive }) =>
                cn(
                  'relative flex items-center rounded-xl text-[13px] font-medium transition duration-200 outline-none focus-visible:ring-2 focus-visible:ring-ring',
                  collapsed ? 'justify-center px-0 py-2.5' : 'gap-3 px-3 py-2',
                  isActive
                    ? 'bg-primary/10 text-primary'
                    : 'text-muted-foreground hover:bg-accent hover:text-foreground'
                )
              }
            >
              <item.icon className="h-[18px] w-[18px] shrink-0" />
              {!collapsed && t(item.labelKey)}
            </NavLink>
          ))}
        </nav>

        {/* Sidebar bottom (fixed, never pushed off-screen) */}
        <div className="shrink-0 mt-auto">
        {/* Version info */}
        <div className={cn('border-t border-border', collapsed ? 'px-2 py-2' : 'px-4 py-3')}>
          {collapsed ? (
            <div className="flex flex-col items-center gap-1.5">
              {/* The dot's colour was all that told an update from up to
                  date, and the name said only "Settings": the state is in
                  the name and the title, and an update is an arrow. */}
              <button
                onClick={() => navigate('/settings?scope=node&tab=system')}
                title={[panelVersion ? `v${panelVersion}` : 'SFPanel', versionState].filter(Boolean).join(' · ')}
                aria-label={[t('layout.nav.settings'), panelVersion && `v${panelVersion}`, versionState].filter(Boolean).join(' · ')}
                className="flex flex-col items-center gap-1 w-full rounded-lg outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                <span className="text-[12px] font-medium text-muted-foreground">
                  {panelVersion ? `v${panelVersion.split('.').slice(0, 2).join('.')}` : '…'}
                </span>
                {updateAvailable ? (
                  <ArrowUpCircle className="h-3 w-3 text-primary" aria-hidden="true" />
                ) : panelVersion ? (
                  <span className="h-1.5 w-1.5 rounded-full bg-success" aria-hidden="true" />
                ) : null}
              </button>
              <div className="flex items-center gap-1 pt-0.5">
                <a
                  href="https://github.com/svrforum/SFPanel"
                  target="_blank"
                  rel="noopener noreferrer"
                  title="GitHub"
                  aria-label="GitHub"
                  className="text-muted-foreground hover:text-foreground transition-colors rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
                >
                  <GithubIcon className="h-3.5 w-3.5" />
                </a>
                <a
                  href="https://buymeacoffee.com/svrforum"
                  target="_blank"
                  rel="noopener noreferrer"
                  title="Buy me a coffee"
                  aria-label="Buy me a coffee"
                  className="text-muted-foreground hover:text-[#FFDD00] transition-colors rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
                >
                  <Coffee className="h-3.5 w-3.5" />
                </a>
              </div>
            </div>
          ) : (
            <div className="space-y-2">
              <button
                onClick={() => navigate('/settings?scope=node&tab=system')}
                aria-label={t('layout.nav.settings')}
                className="flex items-center justify-between w-full group rounded-lg outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                <div>
                  <p className="text-[12px] font-medium text-muted-foreground">SFPanel</p>
                  <p className="text-[12px] font-semibold text-foreground/80">
                    {panelVersion ? `v${panelVersion}` : '…'}
                  </p>
                </div>
                {updateAvailable ? (
                  <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[12px] font-semibold bg-primary/10 text-primary">
                    {t('layout.updateAvailable')}
                  </span>
                ) : panelVersion ? (
                  <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[12px] font-semibold bg-success/10 text-success">
                    {t('layout.upToDate')}
                  </span>
                ) : null}
              </button>
              <div className="flex items-center gap-2.5 text-[12px] text-muted-foreground">
                <a
                  href="https://github.com/svrforum/SFPanel"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1 hover:text-foreground transition-colors"
                  title="GitHub"
                >
                  <GithubIcon className="h-3 w-3" />
                  <span>GitHub</span>
                </a>
                <a
                  href="https://buymeacoffee.com/svrforum"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="inline-flex items-center gap-1 hover:text-[#FFDD00] transition-colors"
                  title="Buy me a coffee"
                >
                  <Coffee className="h-3 w-3" />
                  <span>{t('layout.sponsor')}</span>
                </a>
              </div>
            </div>
          )}
        </div>

        <div className={cn('pb-4 pt-2 border-t border-border', collapsed ? 'px-2' : 'px-3')}>
          <button
            onClick={() => setCollapsed(!collapsed)}
            className={cn(
              'flex items-center rounded-xl text-[13px] font-medium text-muted-foreground hover:bg-accent hover:text-foreground transition duration-200 w-full outline-none focus-visible:ring-2 focus-visible:ring-ring',
              collapsed ? 'justify-center px-0 py-2.5' : 'gap-3 px-3 py-2'
            )}
            title={collapsed ? t('layout.expand') : t('layout.collapse')}
            aria-label={collapsed ? t('layout.expand') : t('layout.collapse')}
          >
            {collapsed ? <PanelLeftOpen className="h-[18px] w-[18px]" /> : <PanelLeftClose className="h-[18px] w-[18px]" />}
            {!collapsed && t('layout.collapse')}
          </button>
          <button
            onClick={handleLogout}
            className={cn(
              'flex items-center rounded-xl text-[13px] font-medium text-muted-foreground hover:bg-accent hover:text-foreground transition duration-200 w-full outline-none focus-visible:ring-2 focus-visible:ring-ring',
              collapsed ? 'justify-center px-0 py-2.5' : 'gap-3 px-3 py-2'
            )}
            title={collapsed ? t('layout.logout') : undefined}
            aria-label={t('layout.logout')}
          >
            <LogOut className="h-[18px] w-[18px] shrink-0" />
            {!collapsed && t('layout.logout')}
          </button>
        </div>
        </div>
      </aside>}

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
        {/* The nodeKey key moved from <Outlet> to the boundary: remounting the
            boundary remounts the outlet tree identically, and also clears any
            caught error when the user switches nodes. The boundary itself keeps
            a page crash from blanking the whole shell (sidebar/nav stay up). */}
        <ErrorBoundary key={nodeKey}>
          <Outlet context={outletContext} />
        </ErrorBoundary>
      </main>

      <BottomNav onMorePress={() => setMoreOpen(true)} />
      <MoreMenu open={moreOpen} onOpenChange={setMoreOpen} />
    </div>
  )
}
