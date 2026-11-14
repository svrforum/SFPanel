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
import { useTerminalCompact, useTerminalFocus } from '@/lib/terminalLayout'
import { DEFAULT_FONT_SIZE, FONT_SIZE_EVENT, GRID_EVENT, MAX_FONT_SIZE, MIN_FONT_SIZE, clampFontSize } from '@/lib/terminalFont'
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
import { SelectTextDialog } from '@/pages/terminal/components/SelectTextDialog'
import { bufferText, SELECT_TEXT_EVENT } from '@/lib/terminalText'
import { SEARCH_RESULTS_EVENT, type SearchDirection, type SearchOutcome } from '@/lib/terminalSearch'
import { useAISessions } from '@/pages/terminal/hooks/useAISessions'
import { usePtyTabs } from '@/pages/terminal/hooks/usePtyTabs'

const nodeSuffix = () => api.currentNode || 'local'
const accountKey = () => `sfpanel_ai_account:${nodeSuffix()}`
// The key the PTY-only page used for its active tab; the value is now
// namespaced, and only a `tmux:` one is restored (see parseActiveKey).
const activeStorageKey = () => `sfpanel_terminal_active:${nodeSuffix()}`
const RAIL_KEY = 'sfpanel_terminal_rail'
const FONT_SIZE_KEY = 'sfpanel_terminal_fontsize'

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
 * and is empty in the compact layout (a phone, a tablet: lib/terminalLayout),
 * where the rail lives in a drawer — see the spec's §2 and §10 for what the
 * Android app and the e2e fixtures select on.
 */
export default function TerminalPage() {
  const { t } = useTranslation()
  const compact = useTerminalCompact()
  const [focus, setFocus] = useTerminalFocus()
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
  const searchInputRef = useRef<HTMLInputElement>(null)
  const { sessions, loaded, refresh } = useAISessions()
  const pty = usePtyTabs()
  // `pty` is a fresh object every render, so the memos and the fetch below
  // depend on these three values rather than on the hook's result.
  const { adopt: adoptPty, note: notePty, tabs: ptyTabs } = pty

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
  // A stored key that is not `tmux:` names a temporary tab — the bare id the
  // PTY-only page wrote, or the `pty:` key the effect below wrote before the
  // last reload — and no temporary tab survives a page load. `parseActiveKey`
  // has already refused to restore it, which is what lets the removal stick:
  // a value still held in `active` would be written straight back. The old
  // page's tab LIST goes too; nothing reads a tab back from storage.
  useEffect(() => {
    try {
      const raw = localStorage.getItem(activeStorageKey())
      if (raw && !raw.startsWith('tmux:')) localStorage.removeItem(activeStorageKey())
      localStorage.removeItem(`sfpanel_terminal_tabs:${nodeSuffix()}`)
    } catch { /* private mode */ }
  }, [])
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
    () => buildRail(sessions, ptyTabs, { fallback, temporaryLabel: t('terminal.rail.temporary') }),
    [sessions, ptyTabs, fallback, t],
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

  // The server's PTY sessions — authoritative for both the 다시 연결 list and,
  // in fallback mode, for which temporary tabs exist at all (see usePtyTabs:
  // tabs are never read back from storage).
  const [ptySessions, setPtySessions] = useState<TerminalSessionInfo[]>([])
  const ptyFetchSeq = useRef(0)
  // Adopting happens on the FIRST successful list only. A later list still
  // reports the session behind a tab the operator has just closed — closing a
  // tab does not end its PTY — so adopting again would put that tab straight
  // back instead of offering the shell under 다시 연결.
  const adopted = useRef(false)
  const tabCount = ptyTabs.length
  const loadPtySessions = useCallback(() => {
    const seq = ++ptyFetchSeq.current
    api.getTerminalSessions()
      .then((r) => {
        const list = r.sessions || []
        // Before the seq guard on purpose: `note` only ever raises the id
        // counter, so even a superseded answer is worth having, and an id the
        // generator has not been told about is one it can hand out twice.
        notePty(list.map((s) => s.session_id))
        if (seq !== ptyFetchSeq.current) return
        setPtySessions(list)
        if (fallback && !adopted.current) {
          adopted.current = true
          adoptPty(list.map((s) => s.session_id))
        }
      })
      .catch(() => { if (seq === ptyFetchSeq.current) setPtySessions([]) })
  }, [fallback, adoptPty, notePty])
  // Issued at mount in BOTH modes, and re-issued whenever the tab COUNT
  // changes. At mount because the id generator restarts at zero on every page
  // load while the server's sessions outlive it: tmux mode never adopts, so
  // without this first answer the temporary shell the operator opens asks for
  // `term-1` and the server hands back the one an earlier page load — or
  // another device — left running (see usePtyTabs `note`). On a count change
  // because closing a tab does not end its PTY session, and a session opened
  // after the last fetch is missing from ptySessions, so the shell just closed
  // would be listed nowhere. The count and not `ptyTabs`, so a rename fetches
  // nothing; in fallback mode `fallback` flipping true costs one more fetch,
  // which is the one that adopts.
  useEffect(() => { loadPtySessions() }, [tabCount, loadPtySessions])
  // Listed inside the temporary group: the sessions this browser has no tab
  // for. Filtered at render, so the list narrows the instant a tab opens and
  // does not wait on the refetch above.
  const reattachable = useMemo(
    () => ptySessions.filter((s) => !ptyTabs.some((tb) => tb.id === s.session_id)),
    [ptySessions, ptyTabs],
  )

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
    setFontSize((prev) => clampFontSize(prev + delta))
  }, [])
  // A session's pinch ends on a size of its own choosing, and the active
  // session says how many columns the size in use comes to (lib/terminalFont).
  const [cols, setCols] = useState<number | null>(null)
  useEffect(() => {
    const onSize = (e: Event) => setFontSize(clampFontSize((e as CustomEvent<number>).detail))
    const onGrid = (e: Event) => setCols((e as CustomEvent<{ cols: number }>).detail.cols)
    window.addEventListener(FONT_SIZE_EVENT, onSize)
    window.addEventListener(GRID_EVENT, onGrid)
    return () => {
      window.removeEventListener(FONT_SIZE_EVENT, onSize)
      window.removeEventListener(GRID_EVENT, onGrid)
    }
  }, [])
  // Search goes through the session (__search): a tmux session's history is
  // not in the browser, and the session knows where it is. The outcome says
  // whether anything matched and how far the search reached; the count comes
  // on its own, once the matches are marked.
  const [searchOutcome, setSearchOutcome] = useState<SearchOutcome | null>(null)
  const [searchCount, setSearchCount] = useState<{ index: number; count: number } | null>(null)
  const searchSeq = useRef(0)
  const runSearch = useCallback((query: string, direction: SearchDirection) => {
    const seq = ++searchSeq.current
    if (!query) {
      forEachActiveSession((el) => el.__searchEnd?.())
      setSearchOutcome(null)
      setSearchCount(null)
      return
    }
    forEachActiveSession((el) => {
      void el.__search?.(query, direction).then((outcome) => {
        if (seq !== searchSeq.current) return
        setSearchOutcome(outcome)
        if (!outcome.found) setSearchCount(null)
      })
    })
  }, [])
  useEffect(() => {
    const onResults = (e: Event) => setSearchCount((e as CustomEvent<{ index: number; count: number }>).detail)
    window.addEventListener(SEARCH_RESULTS_EVENT, onResults)
    return () => window.removeEventListener(SEARCH_RESULTS_EVENT, onResults)
  }, [])
  const handleSearch = useCallback((query: string) => {
    setSearchQuery(query)
    setSearchCount(null)
    runSearch(query, 'older')
  }, [runSearch])
  const handleSearchNext = useCallback(() => runSearch(searchQuery, 'newer'), [runSearch, searchQuery])
  const handleSearchPrev = useCallback(() => runSearch(searchQuery, 'older'), [runSearch, searchQuery])
  const closeSearch = useCallback(() => {
    setSearchOpen(false)
    setSearchQuery('')
    runSearch('', 'older')
    // The keyboard goes back to the terminal; not on a phone, where that
    // raises the on-screen one.
    if (!window.matchMedia('(pointer: coarse)').matches) forEachActiveSession((el) => el.__termRef?.current?.focus())
  }, [runSearch])
  const toggleSearch = useCallback(() => {
    if (searchOpen) closeSearch()
    else { setSearchOpen(true); setTimeout(() => searchInputRef.current?.focus(), 0) }
  }, [searchOpen, closeSearch])
  // Another session has another history: the search does not carry over.
  const onSelect = useCallback((key: string) => {
    if (searchOpen) closeSearch()
    setActive(key)
    setDrawerOpen(false)
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
  // The active session's output as selectable text, opened from the header
  // menu, a long press on the terminal, or the Android app. A tmux session's
  // history lives in tmux — the browser holds only its screen — so it is asked
  // for; the screen stands in if that fails. null while it loads.
  const [selectOpen, setSelectOpen] = useState(false)
  const [selectText, setSelectText] = useState<string | null>(null)
  const selectRequest = useRef(0)
  const activeTmuxId = activeItem?.kind === 'tmux' ? activeItem.id : null
  const openSelectText = useCallback(() => {
    let screen = ''
    forEachActiveSession((el) => {
      const term = el.__termRef?.current
      if (term && !screen) screen = bufferText(term.buffer.active)
    })
    const request = ++selectRequest.current
    setSelectOpen(true)
    if (!activeTmuxId) {
      setSelectText(screen)
      return
    }
    setSelectText(null)
    api.getAISessionText(activeTmuxId)
      .then((r) => { if (selectRequest.current === request) setSelectText(r.text) })
      .catch(() => { if (selectRequest.current === request) setSelectText(screen) })
  }, [activeTmuxId])
  useEffect(() => {
    window.addEventListener(SELECT_TEXT_EVENT, openSelectText)
    return () => window.removeEventListener(SELECT_TEXT_EVENT, openSelectText)
  }, [openSelectText])
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
      // Ctrl+Shift+F and ⌘F from anywhere (the terminal leaves them to this
      // handler); plain Ctrl+F from anywhere but the terminal, whose key it is.
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'f') {
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
  const searchStatus = !searchQuery || !searchOutcome ? ''
    : !searchOutcome.found ? t('terminal.searchNone')
    : !searchCount || searchCount.count === 0 ? ''
    : searchCount.index < 0 ? String(searchCount.count)
    : `${searchCount.index + 1}/${searchCount.count}`
  const search = {
    open: searchOpen, query: searchQuery, inputRef: searchInputRef, status: searchStatus,
    hint: searchQuery && searchOutcome?.scope === 'screen' ? t('terminal.searchScreenOnly') : '',
    onToggle: toggleSearch, onQuery: handleSearch, onNext: handleSearchNext, onPrev: handleSearchPrev, onClose: closeSearch,
  }
  const shownAccount = account || tools?.panel_account || ''

  return (
    <div data-ai-workspace data-select-text className={cn('flex h-full overflow-hidden', !compact && 'p-4')}>
      {/* Always the first child, empty in the compact layout: see the component comment. */}
      <aside className={cn(compact ? 'hidden' : 'flex', 'flex-col shrink-0 bg-console border border-r-0 border-console-border rounded-l-2xl overflow-hidden', collapsed ? 'w-14' : 'w-[272px]')}>
        {!compact && <SessionRail {...railProps} collapsed={collapsed} onToggleCollapsed={() => setCollapsed((c) => !c)} />}
      </aside>

      <div className={cn('flex-1 flex flex-col min-h-0 min-w-0 overflow-clip bg-console', !compact && 'rounded-r-2xl border border-console-border')}>
        <SessionHeader item={activeItem} hostInfo={hostInfo} fontSize={fontSize} cols={activeItem ? cols : null} onFontSize={adjustFontSize} search={search}
          onClear={clearTerminal} onSelectText={openSelectText} onRename={onRename} onAction={onAction}
          compact={compact}
          drawer={compact ? { onOpen: () => setDrawerOpen(true), waiting } : undefined}
          focus={compact ? undefined : { on: focus, onToggle: () => setFocus(!focus) }} />
        {fallback && tools && (
          <div className="shrink-0 px-3 pt-3 space-y-2">
            <p className="text-[12px] text-console-muted">{t('terminal.fallback.banner')}</p>
            <TmuxBanner tools={tools} onChanged={loadTools} />
          </div>
        )}
        <div className="flex-1 min-h-0 relative">
          {activeItem?.kind !== 'pty' && (
            <SessionPane session={activeItem?.kind === 'tmux' ? activeItem.session : null} fallback={fallback} fontSize={fontSize}
              touchScroll={tools?.touch_scroll === true}
              touchHistory={tools?.touch_history === true}
              attachLatest={tools?.attach_latest === true} onNew={onNew}
              onRestart={(s) => { void onAction('restart', { kind: 'tmux', id: s.id, session: s }) }}
              onRemoveEnded={(s) => { void onAction('removeEnded', { kind: 'tmux', id: s.id, session: s }) }} />
          )}
          <PtyPane tabs={ptyTabs} activeId={activeItem?.kind === 'pty' ? activeItem.id : null} fontSize={fontSize} />
        </div>
        {compact && <MobileTerminalBar onSendKey={sendKey} />}
      </div>

      {compact && (
        <Sheet open={drawerOpen} onOpenChange={setDrawerOpen}>
          <SheetContent side="left" className="w-[85vw] max-w-sm p-0 gap-0 bg-console text-console-foreground border-console-border" showCloseButton={false}>
            <SheetTitle className="sr-only">{t('terminal.rail.sessions')}</SheetTitle>
            <SessionRail {...railProps} collapsed={false} dashboardLink />
          </SheetContent>
        </Sheet>
      )}

      <SelectTextDialog open={selectOpen} text={selectText} onClose={() => setSelectOpen(false)}
        returnFocus={() => forEachActiveSession((el) => el.__termRef?.current?.focus())} />
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
