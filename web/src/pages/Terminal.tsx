import { useState, useEffect, useRef, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Terminal as TerminalIcon, Plus, X, Minus, Search, Eraser, History, ShieldAlert, User as UserIcon } from 'lucide-react'
import { api } from '@/lib/api'
import type { TerminalSession as TerminalSessionInfo, TerminalInfo } from '@/types/api'
import { cn } from '@/lib/utils'
import { TerminalSession, type TerminalSessionElement } from '@/pages/terminal/components/TerminalSession'
import MobileTerminalBar from '@/components/MobileTerminalBar'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { toast } from 'sonner'

interface Tab {
  id: string
  title: string
}

// Tabs map 1:1 to server PTY sessions and each node keeps its own session map,
// so persist tabs PER NODE. A single global key reused the same tab id as the
// session_id on every node, spawning a duplicate PTY per tab on each node
// switch (orphaned until the 5-min idle reaper). Font size is a global pref.
const STORAGE_KEY_BASE = 'sfpanel_terminal_tabs'
const ACTIVE_TAB_KEY_BASE = 'sfpanel_terminal_active'
const FONT_SIZE_KEY = 'sfpanel_terminal_fontsize'

const nodeSuffix = () => api.currentNode || 'local'
const tabsKey = () => `${STORAGE_KEY_BASE}:${nodeSuffix()}`
const activeTabKey = () => `${ACTIVE_TAB_KEY_BASE}:${nodeSuffix()}`

const MIN_FONT_SIZE = 10
const MAX_FONT_SIZE = 24
const DEFAULT_FONT_SIZE = 14

let tabCounter = 0

function generateTabId() {
  tabCounter++
  return `term-${tabCounter}`
}

function loadTabs(): Tab[] {
  try {
    const raw = localStorage.getItem(tabsKey())
    if (raw) {
      const tabs = JSON.parse(raw) as Tab[]
      if (Array.isArray(tabs) && tabs.length > 0) {
        for (const t of tabs) {
          const match = t.id.match(/^term-(\d+)$/)
          if (match) {
            tabCounter = Math.max(tabCounter, parseInt(match[1], 10))
          }
        }
        return tabs
      }
    }
  } catch { /* ignore */ }
  return []
}

function saveTabs(tabs: Tab[]) {
  localStorage.setItem(tabsKey(), JSON.stringify(tabs))
}

function loadActiveTab(): string {
  return localStorage.getItem(activeTabKey()) || ''
}

function saveActiveTab(id: string) {
  localStorage.setItem(activeTabKey(), id)
}

function loadFontSize(): number {
  const stored = localStorage.getItem(FONT_SIZE_KEY)
  if (stored) {
    const n = parseInt(stored, 10)
    if (n >= MIN_FONT_SIZE && n <= MAX_FONT_SIZE) return n
  }
  return DEFAULT_FONT_SIZE
}

function saveFontSize(size: number) {
  localStorage.setItem(FONT_SIZE_KEY, String(size))
}

// Single lookup path for the active tab's session element — search / clear /
// key forwarding all reach into the DOM contract TerminalSession exposes
// (data-terminal-session + __refs). Five callbacks used to repeat this
// querySelectorAll + cast loop.
function forEachActiveSession(fn: (el: TerminalSessionElement) => void) {
  document.querySelectorAll('[data-terminal-session="active"]').forEach(el => {
    fn(el as TerminalSessionElement)
  })
}

export default function TerminalPage() {
  const { t } = useTranslation()
  // Make sure there's always at least one tab on first render — guarantees
  // the rest of the page can safely assume tabs[0] exists, and removes the
  // setState-in-effect pattern that used to call addTab() during mount.
  const [tabs, setTabs] = useState<Tab[]>(() => {
    const persisted = loadTabs()
    if (persisted.length > 0) return persisted
    return [{ id: generateTabId(), title: t('terminal.tabTitle', { n: tabCounter, defaultValue: 'Terminal {{n}}' }) }]
  })
  const [activeTab, setActiveTab] = useState<string>(() => {
    const persisted = loadActiveTab()
    if (persisted) return persisted
    // Use the same id we just minted above so first-render is consistent.
    return ''
  })
  const [fontSize, setFontSize] = useState(() => loadFontSize())
  const [editingTabId, setEditingTabId] = useState<string | null>(null)
  const [editingTabName, setEditingTabName] = useState('')
  const [searchOpen, setSearchOpen] = useState(false)
  const [searchQuery, setSearchQuery] = useState('')
  const [reattachOpen, setReattachOpen] = useState(false)
  const [reattachSessions, setReattachSessions] = useState<TerminalSessionInfo[]>([])
  const [shellInfo, setShellInfo] = useState<TerminalInfo | null>(null)
  const editInputRef = useRef<HTMLInputElement>(null)
  const searchInputRef = useRef<HTMLInputElement>(null)
  const reattachRef = useRef<HTMLDivElement>(null)

  // Persist tabs to localStorage
  useEffect(() => {
    saveTabs(tabs)
  }, [tabs])

  useEffect(() => {
    saveActiveTab(activeTab)
  }, [activeTab])

  useEffect(() => {
    saveFontSize(fontSize)
  }, [fontSize])

  // Which account on which host the PTY will run as. Fetched once per mount:
  // the page is already scoped to a single node (see nodeSuffix above) and
  // switching nodes remounts it, so there is nothing to re-poll. A failure
  // leaves the badge hidden rather than blocking the terminal.
  useEffect(() => {
    let cancelled = false
    api
      .getTerminalInfo()
      .then((info) => {
        if (!cancelled) setShellInfo(info)
      })
      .catch(() => {
        if (!cancelled) setShellInfo(null)
      })
    return () => {
      cancelled = true
    }
  }, [])

  const addTab = useCallback(() => {
    const id = generateTabId()
    const num = tabCounter
    setTabs(prev => [...prev, { id, title: t('terminal.tabTitle', { n: num, defaultValue: 'Terminal {{n}}' }) }])
    setActiveTab(id)
  }, [t])

  const openReattach = useCallback(() => {
    setReattachOpen(prev => {
      const next = !prev
      if (next) {
        api.getTerminalSessions()
          .then((res) => {
            const openIds = new Set(tabs.map(tb => tb.id))
            setReattachSessions((res.sessions || []).filter(s => !openIds.has(s.session_id)))
          })
          .catch((err) => toast.error(String(err)))
      }
      return next
    })
  }, [tabs])

  // Close the reattach popover on outside click (same mousedown pattern as
  // the MoreMenu node picker); Escape is handled in the global keydown handler below.
  useEffect(() => {
    if (!reattachOpen) return
    const handleClickOutside = (e: MouseEvent) => {
      if (reattachRef.current && !reattachRef.current.contains(e.target as HTMLElement)) {
        setReattachOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [reattachOpen])

  const reattachSession = useCallback((sessionId: string) => {
    setTabs(prev => {
      if (prev.find(tb => tb.id === sessionId)) return prev
      return [...prev, { id: sessionId, title: t('terminal.reattachedTab', { id: sessionId.slice(0, 8), defaultValue: 'Reattached {{id}}' }) }]
    })
    setActiveTab(sessionId)
    setReattachOpen(false)
  }, [t])

  const closeTab = useCallback((id: string) => {
    setTabs(prev => {
      const next = prev.filter(t => t.id !== id)
      setActiveTab(current => {
        if (current === id && next.length > 0) {
          const idx = prev.findIndex(t => t.id === id)
          const newIdx = Math.min(idx, next.length - 1)
          return next[newIdx].id
        }
        if (next.length === 0) return ''
        return current
      })
      return next
    })
  }, [])

  const renameTab = useCallback((id: string, newName: string) => {
    const trimmed = newName.trim()
    if (!trimmed) return
    setTabs(prev => prev.map(t => t.id === id ? { ...t, title: trimmed } : t))
    setEditingTabId(null)
  }, [])

  const handleDoubleClickTab = useCallback((tab: Tab) => {
    setEditingTabId(tab.id)
    setEditingTabName(tab.title)
    setTimeout(() => editInputRef.current?.select(), 0)
  }, [])

  const adjustFontSize = useCallback((delta: number) => {
    setFontSize(prev => Math.min(MAX_FONT_SIZE, Math.max(MIN_FONT_SIZE, prev + delta)))
  }, [])

  // Terminal search
  const handleSearch = useCallback((query: string) => {
    setSearchQuery(query)
    forEachActiveSession(el => {
      if (el.__searchAddon && query) {
        el.__searchAddon.findNext(query)
      }
    })
  }, [])

  const handleSearchNext = useCallback(() => {
    forEachActiveSession(el => {
      if (el.__searchAddon && searchQuery) el.__searchAddon.findNext(searchQuery)
    })
  }, [searchQuery])

  const handleSearchPrev = useCallback(() => {
    forEachActiveSession(el => {
      if (el.__searchAddon && searchQuery) el.__searchAddon.findPrevious(searchQuery)
    })
  }, [searchQuery])

  const clearTerminal = useCallback(() => {
    forEachActiveSession(el => {
      if (el.__termRef?.current) el.__termRef.current.clear()
      if (el.__wsRef?.current && el.__wsRef.current.readyState === WebSocket.OPEN) {
        // Send Ctrl-L (0x0c) — the universal "clear screen" terminal
        // signal that any TUI (vim/less/mysql) intercepts correctly.
        // The previous literal 'clear\r' executed as a shell command
        // only when the cursor was at a $ prompt and was meaningless
        // (or actively harmful, e.g. typing 'clear' inside an editor)
        // anywhere else.
        el.__wsRef.current.send(new TextEncoder().encode('\x0c'))
      }
    })
  }, [])

  // Keyboard shortcuts
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 'f') {
        e.preventDefault()
        setSearchOpen(true)
        setTimeout(() => searchInputRef.current?.focus(), 0)
      }
      if (e.key === 'Escape') {
        if (searchOpen) {
          setSearchOpen(false)
          setSearchQuery('')
        }
        if (reattachOpen) setReattachOpen(false)
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [searchOpen, reattachOpen])

  const sendKeyToActiveTerminal = useCallback((data: string) => {
    forEachActiveSession(el => {
      if (el.__wsRef?.current && el.__wsRef.current.readyState === WebSocket.OPEN) {
        el.__wsRef.current.send(new TextEncoder().encode(data))
      }
      el.__termRef?.current?.focus()
    })
  }, [])

  // Set the active tab to the first tab when activeTab is empty or stale.
  // The initial tab is seeded by useState so we never need to call addTab()
  // from inside an effect, but a one-time activeTab realignment is still
  // needed (persistedActive may not match a seeded tab after a localStorage
  // reset). The setState here only fires when tabs/activeTab actually
  // disagree, so cascading-render risk is bounded.
  useEffect(() => {
    if (tabs.length > 0 && (!activeTab || !tabs.find(t => t.id === activeTab))) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setActiveTab(tabs[0].id)
    }
  }, [tabs, activeTab])

  return (
    <div className="flex flex-col h-full overflow-hidden">
      {/* Tab Bar */}
      <div className="flex items-center bg-card border-b border-border px-2 shrink-0">
        <div className="flex items-center gap-0.5 overflow-x-auto py-1 flex-1">
          {tabs.map((tab) => (
            <div
              key={tab.id}
              role="button"
              tabIndex={0}
              className={cn(
                'flex items-center gap-1.5 px-3 py-1.5 rounded-t text-xs cursor-pointer select-none group transition-colors shrink-0 outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0',
                activeTab === tab.id
                  ? 'bg-secondary text-foreground'
                  : 'text-muted-foreground hover:text-foreground hover:bg-accent'
              )}
              onClick={() => setActiveTab(tab.id)}
              onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); setActiveTab(tab.id) } }}
              onDoubleClick={() => handleDoubleClickTab(tab)}
            >
              <TerminalIcon className="h-3 w-3" />
              {editingTabId === tab.id ? (
                <input
                  ref={editInputRef}
                  value={editingTabName}
                  onChange={(e) => setEditingTabName(e.target.value)}
                  onBlur={() => renameTab(tab.id, editingTabName)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') renameTab(tab.id, editingTabName)
                    if (e.key === 'Escape') setEditingTabId(null)
                    e.stopPropagation()
                  }}
                  onClick={(e) => e.stopPropagation()}
                  className="bg-transparent border-b border-primary outline-none text-foreground w-20 text-xs"
                  autoFocus
                />
              ) : (
                <span>{tab.title}</span>
              )}
              <button
                aria-label={t('common.close')}
                className={cn(
                  'ml-1 rounded p-0.5 transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0',
                  'opacity-0 group-hover:opacity-100 group-focus-within:opacity-100',
                  activeTab === tab.id && 'opacity-60',
                  'hover:bg-accent hover:text-destructive'
                )}
                onClick={(e) => {
                  e.stopPropagation()
                  closeTab(tab.id)
                }}
              >
                <X className="h-3 w-3" />
              </button>
            </div>
          ))}
        </div>
        {/* Who / where. In a cluster the same page targets a different machine
            depending on the node picker, and a root prompt looks identical on
            every one of them — so name the target rather than leave it implied. */}
        {shellInfo && (
          <div
            className={cn(
              'hidden sm:flex items-center gap-1.5 shrink-0 ml-2 px-2 py-1 rounded-md border text-[11px] font-mono',
              shellInfo.is_root
                ? 'bg-warning/10 border-warning/30 text-warning'
                : 'bg-muted/50 border-transparent text-muted-foreground'
            )}
            title={
              shellInfo.is_root
                ? t('terminal.shellBadgeRootHint', {
                    host: shellInfo.hostname,
                    defaultValue: 'Running as root on {{host}} — commands here are unrestricted',
                  })
                : t('terminal.shellBadgeHint', {
                    user: shellInfo.shell_user,
                    host: shellInfo.hostname,
                    defaultValue: 'Connected as {{user}} on {{host}}',
                  })
            }
          >
            {shellInfo.is_root
              ? <ShieldAlert className="h-3 w-3 shrink-0" aria-hidden="true" />
              : <UserIcon className="h-3 w-3 shrink-0" aria-hidden="true" />}
            <span className="truncate max-w-[22ch]">
              {shellInfo.shell_user}@{shellInfo.hostname}
            </span>
          </div>
        )}

        <div className="flex items-center gap-1 ml-2 shrink-0">
          {/* Font size controls */}
          <Button
            variant="ghost"
            size="sm"
            className="h-6 w-6 p-0 text-muted-foreground hover:text-foreground hover:bg-accent"
            onClick={() => adjustFontSize(-1)}
            title={t('terminal.fontSmaller')}
            aria-label={t('terminal.fontSmaller')}
          >
            <Minus className="h-3 w-3" />
          </Button>
          <span className="text-[10px] text-muted-foreground min-w-[20px] text-center">{fontSize}</span>
          <Button
            variant="ghost"
            size="sm"
            className="h-6 w-6 p-0 text-muted-foreground hover:text-foreground hover:bg-accent"
            onClick={() => adjustFontSize(1)}
            title={t('terminal.fontLarger')}
            aria-label={t('terminal.fontLarger')}
          >
            <Plus className="h-3 w-3" />
          </Button>
          <div className="w-px h-4 bg-border mx-1" />
          {/* Search */}
          <Button
            variant="ghost"
            size="sm"
            className={cn(
              "h-6 w-6 p-0 hover:bg-accent",
              searchOpen ? 'text-primary' : 'text-muted-foreground hover:text-foreground'
            )}
            onClick={() => {
              setSearchOpen(!searchOpen)
              if (!searchOpen) setTimeout(() => searchInputRef.current?.focus(), 0)
              else setSearchQuery('')
            }}
            title={t('terminal.search')}
            aria-label={t('terminal.search')}
          >
            <Search className="h-3.5 w-3.5" />
          </Button>
          {/* Clear */}
          <Button
            variant="ghost"
            size="sm"
            className="h-6 w-6 p-0 text-muted-foreground hover:text-foreground hover:bg-accent"
            onClick={clearTerminal}
            title={t('terminal.clear')}
            aria-label={t('terminal.clear')}
          >
            <Eraser className="h-3.5 w-3.5" />
          </Button>
          <div className="w-px h-4 bg-border mx-1" />
          {/* Reattach session picker */}
          <div ref={reattachRef} className="relative">
            <Button
              variant="ghost"
              size="sm"
              className={cn(
                "h-6 w-6 p-0 hover:bg-accent",
                reattachOpen ? 'text-primary' : 'text-muted-foreground hover:text-foreground'
              )}
              onClick={openReattach}
              title={t('terminal.reattach.button')}
              aria-label={t('terminal.reattach.button')}
              aria-haspopup="true"
              aria-expanded={reattachOpen}
            >
              <History className="h-3.5 w-3.5" />
            </Button>
            {reattachOpen && (
              <div className="absolute right-0 top-8 z-20 w-72 max-h-80 overflow-y-auto rounded-xl bg-secondary border border-border shadow-lg py-1">
                <div className="px-3 py-2 text-[11px] font-semibold text-foreground border-b border-border">
                  {t('terminal.reattach.title')}
                </div>
                {reattachSessions.length === 0 ? (
                  <div className="px-3 py-3 text-[12px] text-muted-foreground">
                    {t('terminal.reattach.empty')}
                  </div>
                ) : (
                  reattachSessions.map((s) => (
                    <button
                      key={s.session_id}
                      onClick={() => reattachSession(s.session_id)}
                      className="w-full flex items-center justify-between gap-2 px-3 py-2 text-left hover:bg-accent transition-colors outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/40"
                    >
                      <div className="min-w-0">
                        <div className="font-mono text-[12px] text-foreground truncate">{s.session_id.slice(0, 12)}</div>
                        <div className="text-[10px] text-muted-foreground">
                          {new Date(s.last_use).toLocaleString()}
                          {s.attached ? ` · ${t('terminal.reattach.attached')}` : ''}
                        </div>
                      </div>
                      <span className="shrink-0 text-[10px] text-primary">{t('terminal.reattach.open')}</span>
                    </button>
                  ))
                )}
              </div>
            )}
          </div>
          {/* New tab */}
          <Button
            variant="ghost"
            size="sm"
            className="h-6 w-6 p-0 text-muted-foreground hover:text-foreground hover:bg-accent"
            onClick={addTab}
            title={t('terminal.newTab')}
            aria-label={t('terminal.newTab')}
          >
            <Plus className="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>

      {/* Search bar */}
      {searchOpen && (
        <div className="flex items-center gap-1.5 bg-muted border-b border-border px-2 md:px-3 py-1.5">
          <Search className="h-3.5 w-3.5 text-muted-foreground" />
          <Input
            ref={searchInputRef}
            value={searchQuery}
            onChange={(e) => handleSearch(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault()
                if (e.shiftKey) handleSearchPrev()
                else handleSearchNext()
              }
              if (e.key === 'Escape') {
                setSearchOpen(false)
                setSearchQuery('')
              }
            }}
            placeholder={t('terminal.searchPlaceholder')}
            className="h-6 text-xs bg-card border-border text-foreground flex-1 max-w-[10rem] md:max-w-xs"
            autoFocus
          />
          <Button
            variant="ghost"
            size="sm"
            className="h-6 px-1.5 md:px-2 text-xs text-muted-foreground hover:text-foreground hover:bg-card"
            onClick={handleSearchPrev}
          >
            <span className="hidden md:inline">{t('terminal.prev')}</span>
            <span className="md:hidden">↑</span>
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="h-6 px-1.5 md:px-2 text-xs text-muted-foreground hover:text-foreground hover:bg-card"
            onClick={handleSearchNext}
          >
            <span className="hidden md:inline">{t('terminal.next')}</span>
            <span className="md:hidden">↓</span>
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="h-6 w-6 p-0 text-muted-foreground hover:text-foreground hover:bg-card"
            onClick={() => { setSearchOpen(false); setSearchQuery('') }}
          >
            <X className="h-3.5 w-3.5" />
          </Button>
        </div>
      )}

      {/* Terminal Area */}
      <div className="flex-1 bg-card relative min-h-0">
        {tabs.map((tab) => (
          <TerminalSession
            key={tab.id}
            sessionId={tab.id}
            active={activeTab === tab.id}
            fontSize={fontSize}
          />
        ))}
        {tabs.length === 0 && (
          <div className="flex items-center justify-center h-full text-muted-foreground">
            <div className="text-center">
              <TerminalIcon className="h-12 w-12 mx-auto mb-3 opacity-50" />
              <p>{t('terminal.noTabs')}</p>
              <Button
                variant="outline"
                size="sm"
                className="mt-3 border-border text-foreground hover:bg-accent"
                onClick={addTab}
              >
                <Plus className="h-4 w-4 mr-1" />
                {t('terminal.newTab')}
              </Button>
            </div>
          </div>
        )}
      </div>

      <MobileTerminalBar onSendKey={sendKeyToActiveTerminal} />
    </div>
  )
}
