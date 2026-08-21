import { useState, useEffect, useCallback, useMemo, useRef } from 'react'
import { Link, NavLink, Outlet, useNavigate, useLocation } from 'react-router-dom'
import { LogOut, PanelLeftClose, PanelLeftOpen, Coffee } from 'lucide-react'

import GithubIcon from '@/components/GithubIcon'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'
import { NAV_ITEMS } from '@/lib/navigation'
import { useVisibleInterval } from '@/hooks/useVisibleInterval'
import { ErrorBoundary } from '@/components/ErrorBoundary'
import ClusterSidebar from '@/components/cluster/ClusterSidebar'
import SidebarSkeleton from '@/components/SidebarSkeleton'
import Logo, { LogoMark } from '@/components/Logo'
import BottomNav from '@/components/BottomNav'
import MoreMenu from '@/components/MoreMenu'
import type { ClusterStatus, DashboardOverview } from '@/types/api'

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
  const location = useLocation()
  const isTerminal = location.pathname === '/terminal'

  const handleNodeChanged = useCallback(() => {
    setNodeKey((k) => k + 1)
    window.dispatchEvent(new Event('sfpanel:node-changed'))
  }, [])

  useEffect(() => {
    const handler = () => setNodeKey((k) => k + 1)
    window.addEventListener('sfpanel:node-changed', handler)
    return () => window.removeEventListener('sfpanel:node-changed', handler)
  }, [])

  // Latched once the server definitively says "not clustered". A standalone
  // node's answer can't change while the page lives — cluster init/join
  // restarts the process, which reloads the SPA — so polling on forever would
  // be pure noise for the majority of installs.
  const notClustered = useRef(false)

  const loadClusterStatus = useCallback(() => {
    if (notClustered.current) return
    api.getClusterStatus(true)
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

  useEffect(() => {
    localStorage.setItem(SIDEBAR_KEY, String(collapsed))
  }, [collapsed])

  useEffect(() => {
    // The dashboard overview already carries panel version + update info,
    // so this one call covers what Layout used to do with two: getSystemInfo
    // + checkUpdate. Eliminates one GitHub round-trip per dashboard mount
    // (checkUpdate hits the release index).
    const node = api.currentNode
    api.getDashboardOverview()
      .then((data) => {
        if (data.version) setPanelVersion(data.version)
        if (data.update_info) setUpdateAvailable(data.update_info.update_available)
        setOverview({ data, node, at: Date.now() })
      })
      .catch(() => setOverview({ data: null, node, at: Date.now() }))
  }, [])

  const outletContext = useMemo<LayoutOutletContext>(() => ({ overview }), [overview])

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

  const handleLogout = () => {
    // Fire-and-forget — even if the server is unreachable we still want to
    // navigate away. api.logout() clears local state on its own.
    void api.logout()
    navigate('/login')
  }

  return (
    <div className="flex overflow-hidden bg-background" style={{ height: 'var(--app-h, 100dvh)' }}>
      {/* First visit only: no remembered mode yet, so neither sidebar can be
          chosen without guessing. Hold the slot rather than collapsing it. */}
      {clusterEnabled === null && <SidebarSkeleton />}

      {/* Cluster dual-panel sidebar */}
      {clusterEnabled === true && (
        <div className="hidden md:flex h-full shrink-0">
          <ClusterSidebar
            status={clusterStatus}
            panelVersion={panelVersion}
            onLogout={handleLogout}
            onNodeChanged={handleNodeChanged}
          />
        </div>
      )}

      {/* Standard sidebar (non-cluster mode) */}
      {clusterEnabled === false && <aside className={cn(
        'bg-card border-r border-border flex-col transition-all duration-300 ease-in-out shrink-0 hidden md:flex h-full',
        collapsed ? 'w-[68px]' : 'w-60'
      )}>
        <div className={cn('flex items-center', collapsed ? 'px-3 py-6 justify-center' : 'px-4 py-4')}>
          {collapsed ? (
            <Link to="/dashboard" aria-label="SFPanel" className="rounded-lg focus:outline-none focus-visible:ring-2 focus-visible:ring-primary/30">
              <LogoMark className="h-8 w-8" />
            </Link>
          ) : (
            <Link to="/dashboard" aria-label="SFPanel" className="block w-full rounded-lg focus:outline-none focus-visible:ring-2 focus-visible:ring-primary/30">
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
                  'relative flex items-center rounded-xl text-[13px] font-medium transition-all duration-200 outline-none focus-visible:ring-2 focus-visible:ring-ring/40',
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
              <button
                onClick={() => navigate('/settings?scope=node&tab=system')}
                title={panelVersion ? `v${panelVersion}` : 'SFPanel'}
                aria-label={t('layout.nav.settings')}
                className="flex flex-col items-center gap-1 w-full rounded-lg outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
              >
                <span className="text-[10px] font-medium text-muted-foreground">
                  {panelVersion ? `v${panelVersion.split('.').slice(0, 2).join('.')}` : '...'}
                </span>
                {updateAvailable ? (
                  <span className="h-1.5 w-1.5 rounded-full bg-primary" />
                ) : panelVersion ? (
                  <span className="h-1.5 w-1.5 rounded-full bg-success" />
                ) : null}
              </button>
              <div className="flex items-center gap-1 pt-0.5">
                <a
                  href="https://github.com/svrforum/SFPanel"
                  target="_blank"
                  rel="noopener noreferrer"
                  title="GitHub"
                  aria-label="GitHub"
                  className="text-muted-foreground hover:text-foreground transition-colors rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
                >
                  <GithubIcon className="h-3.5 w-3.5" />
                </a>
                <a
                  href="https://buymeacoffee.com/svrforum"
                  target="_blank"
                  rel="noopener noreferrer"
                  title="Buy me a coffee"
                  aria-label="Buy me a coffee"
                  className="text-muted-foreground hover:text-[#FFDD00] transition-colors rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
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
                className="flex items-center justify-between w-full group rounded-lg outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
              >
                <div>
                  <p className="text-[11px] font-medium text-muted-foreground">SFPanel</p>
                  <p className="text-[12px] font-semibold text-foreground/80">
                    {panelVersion ? `v${panelVersion}` : '...'}
                  </p>
                </div>
                {updateAvailable ? (
                  <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-semibold bg-primary/10 text-primary">
                    {t('layout.updateAvailable')}
                  </span>
                ) : panelVersion ? (
                  <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-semibold bg-success/10 text-success">
                    {t('layout.upToDate')}
                  </span>
                ) : null}
              </button>
              <div className="flex items-center gap-2.5 text-[11px] text-muted-foreground">
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
              'flex items-center rounded-xl text-[13px] font-medium text-muted-foreground hover:bg-accent hover:text-foreground transition-all duration-200 w-full outline-none focus-visible:ring-2 focus-visible:ring-ring/40',
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
              'flex items-center rounded-xl text-[13px] font-medium text-muted-foreground hover:bg-accent hover:text-foreground transition-all duration-200 w-full outline-none focus-visible:ring-2 focus-visible:ring-ring/40',
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

      <main className={cn(
        // min-w-0 lets this flex child shrink below its content width, and
        // overflow-x-hidden stops an over-wide child (e.g. a non-wrapping
        // toolbar) from making the WHOLE page slide left/right on mobile.
        // Vertical scroll stays; intended horizontal-scroll rows keep their own
        // overflow-x-auto so they still scroll internally.
        "flex-1 min-h-0 min-w-0",
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
