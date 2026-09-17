import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import type { AISession, AITools, TerminalInfo, TerminalSession as TerminalSessionInfo } from '@/types/api'
import { aiErrorMessage, sessionInfoLine, titlePrefix, waitingCount } from '@/lib/aiSessions'
import { buildRail, findItem, parseActiveKey, pickActive, type RailItem } from '@/lib/sessionRail'
import { cn } from '@/lib/utils'
import { OutputDialog, useSSEOutput } from '@/components/OutputDialog'
import { useConfirm } from '@/components/ConfirmDialog'
import { useIsMobile } from '@/hooks/useIsMobile'
import MobileTerminalBar from '@/components/MobileTerminalBar'
import { Sheet, SheetContent, SheetTitle } from '@/components/ui/sheet'
import type { TerminalSessionElement } from '@/pages/terminal/components/TerminalSession'
import { SessionRail, type RailAction } from '@/pages/terminal/components/SessionRail'
import { SessionHeader } from '@/pages/terminal/components/SessionHeader'
import { SessionPane } from '@/pages/terminal/components/SessionPane'
import { PtyPane } from '@/pages/terminal/components/PtyPane'
import { ToolsSheet } from '@/pages/terminal/components/ToolsSheet'
import { TmuxBanner } from '@/pages/terminal/components/TmuxBanner'
import { NewSessionDialog } from '@/pages/terminal/components/NewSessionDialog'
import { useAISessions } from '@/pages/terminal/hooks/useAISessions'
import { usePtyTabs } from '@/pages/terminal/hooks/usePtyTabs'

const nodeSuffix = () => api.currentNode || 'local'
const accountKey = () => `sfpanel_ai_account:${nodeSuffix()}`
// The key the PTY-only page used for its active tab; the value is now
// namespaced (see parseActiveKey), and a bare value from before is a PTY tab.
const activeStorageKey = () => `sfpanel_terminal_active:${nodeSuffix()}`
const RAIL_KEY = 'sfpanel_terminal_rail'
const FONT_SIZE_KEY = 'sfpanel_terminal_fontsize'
const MIN_FONT_SIZE = 10
const MAX_FONT_SIZE = 24
const DEFAULT_FONT_SIZE = 14

function readLS(key: string): string {
  try { return localStorage.getItem(key) || '' } catch { return '' }
}
function writeLS(key: string, value: string) {
  try { localStorage.setItem(key, value) } catch { /* private mode */ }
}
function loadFontSize(): number {
  const n = parseInt(readLS(FONT_SIZE_KEY), 10)
  return Number.isFinite(n) && n >= MIN_FONT_SIZE && n <= MAX_FONT_SIZE ? n : DEFAULT_FONT_SIZE
}

// The active pane's session element — search / clear / key forwarding reach
// into the DOM contract TerminalSession exposes (data-terminal-session + __refs).
function forEachActiveSession(fn: (el: TerminalSessionElement) => void) {
  document.querySelectorAll('[data-terminal-session="active"]').forEach((el) => fn(el as TerminalSessionElement))
}

/**
 * The one terminal page. Sessions are tmux sessions (shell or an AI tool)
 * listed in a rail grouped by directory; the PTY engine is the fallback when
 * tmux is missing or too old, and the temporary-shell door in the tools
 * panel. The rail <aside> is always the first child of [data-ai-workspace]
 * and is empty on a phone, where the rail lives in a drawer — see the spec's
 * §2 and §10 for what the Android app and the e2e fixtures select on.
 */
export default function TerminalPage() {
  const { t } = useTranslation()
  const isMobile = useIsMobile()
  const output = useSSEOutput()
  const confirm = useConfirm()
  const [account, setAccount] = useState<string>(() => readLS(accountKey()))
  const [tools, setTools] = useState<AITools | null>(null)
  const [toolsError, setToolsError] = useState(false)
  const [active, setActive] = useState<string | null>(() => parseActiveKey(readLS(activeStorageKey()) || null))
  const [collapsed, setCollapsed] = useState(() => readLS(RAIL_KEY) === 'collapsed')
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [toolsOpen, setToolsOpen] = useState(() => document.documentElement.hasAttribute('data-ai-tools-open'))
  const [launcherOpen, setLauncherOpen] = useState(false)
  const [fontSize, setFontSize] = useState(loadFontSize)
  const [searchOpen, setSearchOpen] = useState(false)
  const [searchQuery, setSearchQuery] = useState('')
  const [hostInfo, setHostInfo] = useState<TerminalInfo | null>(null)
  const [reattachable, setReattachable] = useState<TerminalSessionInfo[]>([])
  const searchInputRef = useRef<HTMLInputElement>(null)
  const { sessions, loaded, refresh } = useAISessions()
  const pty = usePtyTabs()

  // Promise callbacks rather than await: the effect kicks this off on mount
  // and an async body would trip react-hooks/set-state-in-effect. First load
  // asks with an empty user, which the server answers for its own account.
  const loadTools = useCallback(() => {
    api.getAITools(account).then((data) => {
      setTools(data)
      setToolsError(false)
      if (!account && data.account !== data.panel_account) setAccount(data.panel_account)
    }).catch(() => {
      setToolsError(true)
    })
  }, [account])
  useEffect(() => { loadTools() }, [loadTools])
  useEffect(() => { if (account) writeLS(accountKey(), account) }, [account])
  useEffect(() => { writeLS(FONT_SIZE_KEY, String(fontSize)) }, [fontSize])
  useEffect(() => { writeLS(RAIL_KEY, collapsed ? 'collapsed' : 'open') }, [collapsed])
  useEffect(() => { if (active) writeLS(activeStorageKey(), active) }, [active])

  // Who the PTY engine runs as, for the badge. One fetch per mount: the page
  // is scoped to a node and remounts on a node switch.
  useEffect(() => {
    let cancelled = false
    api.getTerminalInfo().then((info) => { if (!cancelled) setHostInfo(info) }).catch(() => { if (!cancelled) setHostInfo(null) })
    return () => { cancelled = true }
  }, [])

  const fallback = tools !== null && !tools.tmux.supported
  const groups = useMemo(
    () => buildRail(sessions, pty.tabs, { fallback, temporaryLabel: t('terminal.rail.temporary') }),
    [sessions, pty.tabs, fallback, t],
  )
  const activeItem = findItem(groups, active)

  // Keep the active item valid once the server's list has landed. The
  // realignment fires only when the stored key and the rail disagree, so the
  // cascading-render risk the rule guards is bounded (same shape as before).
  useEffect(() => {
    if (!loaded) return
    const next = pickActive(groups, active)
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (next !== active) setActive(next)
  }, [groups, active, loaded])

  // "(n) SFPanel" while something waits in a session that is not on screen.
  const activeSessionId = activeItem?.kind === 'tmux' ? activeItem.id : null
  const waiting = waitingCount(sessions, activeSessionId)
  useEffect(() => {
    const base = document.title.replace(/^\(\d+\) /, '')
    document.title = titlePrefix(waiting) + base
    return () => { document.title = base }
  }, [waiting])

  // The Android app opens the tools panel by toggling <html data-ai-tools-open>;
  // the attribute and the sheet's state follow each other both ways.
  useEffect(() => {
    const root = document.documentElement
    const observer = new MutationObserver(() => setToolsOpen(root.hasAttribute('data-ai-tools-open')))
    observer.observe(root, { attributes: true, attributeFilter: ['data-ai-tools-open'] })
    return () => observer.disconnect()
  }, [])
  const setToolsOpenBoth = useCallback((open: boolean) => {
    setToolsOpen(open)
    document.documentElement.toggleAttribute('data-ai-tools-open', open)
  }, [])

  // Server-side PTY sessions this browser could reattach — listed inside the
  // temporary group, so only fetched while that group is shown.
  const showPty = fallback || pty.tabs.length > 0
  const loadReattachable = useCallback(() => {
    api.getTerminalSessions()
      .then((r) => setReattachable((r.sessions || []).filter((s) => !pty.tabs.some((tb) => tb.id === s.session_id))))
      .catch(() => setReattachable([]))
  }, [pty.tabs])
  useEffect(() => { if (showPty) loadReattachable() }, [showPty, loadReattachable])

  const act = useCallback(async (fn: () => Promise<unknown>) => {
    try { await fn() } catch (err: unknown) { toast.error(aiErrorMessage(err, t)) }
    await refresh()
  }, [refresh, t])

  const openTemporaryShell = useCallback(() => {
    const id = pty.add()
    setActive(`pty:${id}`)
    setDrawerOpen(false)
  }, [pty])
  const onNew = useCallback(() => {
    if (fallback) openTemporaryShell()
    else { setLauncherOpen(true); setDrawerOpen(false) }
  }, [fallback, openTemporaryShell])
  const onReattach = useCallback((sessionId: string) => {
    setActive(`pty:${pty.reattach(sessionId)}`)
    setDrawerOpen(false)
  }, [pty])
  const onSelect = useCallback((key: string) => { setActive(key); setDrawerOpen(false) }, [])

  const onRename = useCallback((item: RailItem, title: string) => {
    if (item.kind === 'pty') pty.rename(item.id, title)
    else void act(() => api.renameAISession(item.id, title))
  }, [act, pty])

  const onAction = useCallback(async (action: RailAction, item: RailItem) => {
    if (item.kind === 'pty') {
      if (action === 'closeTab') pty.close(item.id)
      return
    }
    const s: AISession = item.session
    switch (action) {
      case 'info': toast.info(sessionInfoLine(s, t)); break
      case 'rerun': await act(() => api.rerunAISession(s.id)); break
      case 'restart': await act(() => api.restartAISession(s.id)); break
      case 'removeEnded': await act(() => api.deleteAISession(s.id)); break
      case 'kill':
        if (await confirm({ title: t('ai.tabs.closeConfirmTitle'), description: t('ai.tabs.closeConfirmDesc', { title: s.title }), danger: true, confirmLabel: t('ai.tabs.close') })) {
          await act(() => api.deleteAISession(s.id))
        }
        break
      default: break
    }
  }, [act, confirm, pty, t])

  // Toolbar: font size applies to every mounted session; search and clear
  // reach the active one.
  const adjustFontSize = useCallback((delta: number) => {
    setFontSize((prev) => Math.min(MAX_FONT_SIZE, Math.max(MIN_FONT_SIZE, prev + delta)))
  }, [])
  const handleSearch = useCallback((query: string) => {
    setSearchQuery(query)
    forEachActiveSession((el) => { if (el.__searchAddon && query) el.__searchAddon.findNext(query) })
  }, [])
  const handleSearchNext = useCallback(() => {
    forEachActiveSession((el) => { if (el.__searchAddon && searchQuery) el.__searchAddon.findNext(searchQuery) })
  }, [searchQuery])
  const handleSearchPrev = useCallback(() => {
    forEachActiveSession((el) => { if (el.__searchAddon && searchQuery) el.__searchAddon.findPrevious(searchQuery) })
  }, [searchQuery])
  const closeSearch = useCallback(() => { setSearchOpen(false); setSearchQuery('') }, [])
  const toggleSearch = useCallback(() => {
    if (searchOpen) closeSearch()
    else { setSearchOpen(true); setTimeout(() => searchInputRef.current?.focus(), 0) }
  }, [searchOpen, closeSearch])
  const clearTerminal = useCallback(() => {
    forEachActiveSession((el) => {
      if (el.__termRef?.current) el.__termRef.current.clear()
      if (el.__wsRef?.current && el.__wsRef.current.readyState === WebSocket.OPEN) {
        // Ctrl-L: the one "clear screen" every TUI interprets correctly.
        el.__wsRef.current.send(new TextEncoder().encode('\x0c'))
      }
    })
  }, [])
  const sendKey = useCallback((data: string) => {
    forEachActiveSession((el) => {
      if (el.__wsRef?.current && el.__wsRef.current.readyState === WebSocket.OPEN) {
        el.__wsRef.current.send(new TextEncoder().encode(data))
      }
      el.__termRef?.current?.focus()
    })
  }, [])

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 'f') {
        e.preventDefault()
        setSearchOpen(true)
        setTimeout(() => searchInputRef.current?.focus(), 0)
      }
      if (e.key === 'Escape' && searchOpen) closeSearch()
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [searchOpen, closeSearch])

  const railProps = {
    groups, active, fallback, reattachable,
    onSelect, onNew, onReattach, onRename, onAction,
    onOpenTools: () => { setDrawerOpen(false); setToolsOpenBoth(true) },
  }
  const search = { open: searchOpen, query: searchQuery, inputRef: searchInputRef, onToggle: toggleSearch, onQuery: handleSearch, onNext: handleSearchNext, onPrev: handleSearchPrev, onClose: closeSearch }
  const shownAccount = account || tools?.panel_account || ''

  return (
    <div data-ai-workspace className="flex h-full overflow-hidden md:p-4">
      {/* Always the first child, empty on a phone: see the component comment. */}
      <aside className={cn('hidden md:flex flex-col shrink-0 bg-console border border-r-0 border-console-border rounded-l-2xl overflow-hidden', collapsed ? 'w-14' : 'w-[272px]')}>
        {!isMobile && <SessionRail {...railProps} collapsed={collapsed} onToggleCollapsed={() => setCollapsed((c) => !c)} />}
      </aside>

      <div className="flex-1 flex flex-col min-h-0 min-w-0 overflow-clip bg-console md:rounded-r-2xl md:border md:border-console-border">
        <SessionHeader item={activeItem} hostInfo={hostInfo} fontSize={fontSize} onFontSize={adjustFontSize} search={search}
          onClear={clearTerminal} onRename={onRename} onAction={onAction}
          drawer={isMobile ? { onOpen: () => setDrawerOpen(true), waiting } : undefined} />
        {fallback && tools && (
          <div className="shrink-0 px-3 pt-3 space-y-2">
            <p className="text-[12px] text-console-muted">{t('terminal.fallback.banner')}</p>
            <TmuxBanner tools={tools} onChanged={loadTools} />
          </div>
        )}
        <div className="flex-1 min-h-0 relative">
          {activeItem?.kind !== 'pty' && (
            <SessionPane session={activeItem?.kind === 'tmux' ? activeItem.session : null} fontSize={fontSize} onNew={onNew}
              onRestart={(s) => { void onAction('restart', { kind: 'tmux', id: s.id, session: s }) }}
              onRemoveEnded={(s) => { void onAction('removeEnded', { kind: 'tmux', id: s.id, session: s }) }} />
          )}
          <PtyPane tabs={pty.tabs} activeId={activeItem?.kind === 'pty' ? activeItem.id : null} fontSize={fontSize} />
        </div>
        <MobileTerminalBar onSendKey={sendKey} />
      </div>

      {isMobile && (
        <Sheet open={drawerOpen} onOpenChange={setDrawerOpen}>
          <SheetContent side="left" className="w-[85vw] max-w-sm p-0 gap-0 bg-console text-console-foreground border-console-border" showCloseButton={false}>
            <SheetTitle className="sr-only">{t('terminal.rail.sessions')}</SheetTitle>
            <SessionRail {...railProps} collapsed={false} />
          </SheetContent>
        </Sheet>
      )}

      {/* Focus the new session only once the list containing it has landed;
          setting the key first would be undone by the realignment above. */}
      <NewSessionDialog open={launcherOpen} onOpenChange={setLauncherOpen} account={shownAccount} tools={tools}
        onCreated={(s) => { void refresh().then(() => setActive(`tmux:${s.id}`)) }}
        onOpenTools={() => setToolsOpenBoth(true)} />
      <ToolsSheet open={toolsOpen} onOpenChange={setToolsOpenBoth} tools={tools} toolsError={toolsError} account={shownAccount}
        onAccountChange={(a) => { setTools(null); setAccount(a) }} onChanged={loadTools} onSessionsChanged={() => { void refresh() }}
        output={output} onOpenTemporaryShell={openTemporaryShell} />
      <OutputDialog state={output.state} onClose={output.closeOutput} />
    </div>
  )
}
