import { useState, useEffect, useRef, useCallback, type RefObject } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { Terminal as TerminalIcon, Plus, X, Minus, Search, Eraser, History } from 'lucide-react'
import { Terminal as XTerm } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { SearchAddon } from '@xterm/addon-search'
import { Unicode11Addon } from '@xterm/addon-unicode11'
import '@xterm/xterm/css/xterm.css'
import { api } from '@/lib/api'
import type { TerminalSession as TerminalSessionInfo } from '@/types/api'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { toast } from 'sonner'

interface Tab {
  id: string
  title: string
}

const STORAGE_KEY = 'sfpanel_terminal_tabs'
const ACTIVE_TAB_KEY = 'sfpanel_terminal_active'
const FONT_SIZE_KEY = 'sfpanel_terminal_fontsize'

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
    const raw = localStorage.getItem(STORAGE_KEY)
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
  localStorage.setItem(STORAGE_KEY, JSON.stringify(tabs))
}

function loadActiveTab(): string {
  return localStorage.getItem(ACTIVE_TAB_KEY) || ''
}

function saveActiveTab(id: string) {
  localStorage.setItem(ACTIVE_TAB_KEY, id)
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

// Each TerminalSession imperatively attaches its xterm instance, websocket
// ref, and search addon to its DOM container so the parent (which renders
// many sessions and reaches into the active one for search/clear/key
// forwarding) can find them by querying the DOM. This sidesteps lifting a
// dynamic list of refs to the parent.
interface TerminalSessionElement extends HTMLElement {
  __searchAddon?: SearchAddon
  __termRef?: RefObject<XTerm | null>
  __wsRef?: RefObject<WebSocket | null>
}

function TerminalSession({ sessionId, active, fontSize }: { sessionId: string; active: boolean; fontSize: number }) {
  const containerRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<XTerm | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const fitAddonRef = useRef<FitAddon | null>(null)
  const searchAddonRef = useRef<SearchAddon | null>(null)
  const initialized = useRef(false)
  const { t } = useTranslation()

  useEffect(() => {
    if (!containerRef.current || initialized.current) return
    initialized.current = true

    const term = new XTerm({
      cursorBlink: true,
      fontSize,
      fontFamily: '"JetBrains Mono", "Fira Code", Menlo, Monaco, "Courier New", monospace',
      theme: {
        background: '#1a1b26',
        foreground: '#c0caf5',
        cursor: '#c0caf5',
        cursorAccent: '#1a1b26',
        selectionBackground: '#33467c',
        selectionForeground: '#c0caf5',
        black: '#15161e',
        red: '#f7768e',
        green: '#9ece6a',
        yellow: '#e0af68',
        blue: '#7aa2f7',
        magenta: '#bb9af7',
        cyan: '#7dcfff',
        white: '#a9b1d6',
        brightBlack: '#414868',
        brightRed: '#f7768e',
        brightGreen: '#9ece6a',
        brightYellow: '#e0af68',
        brightBlue: '#7aa2f7',
        brightMagenta: '#bb9af7',
        brightCyan: '#7dcfff',
        brightWhite: '#c0caf5',
      },
      scrollback: 10000,
      allowProposedApi: true,
    })

    const fitAddon = new FitAddon()
    const searchAddon = new SearchAddon()
    const unicode11Addon = new Unicode11Addon()
    term.loadAddon(fitAddon)
    term.loadAddon(new WebLinksAddon())
    term.loadAddon(searchAddon)
    term.loadAddon(unicode11Addon)
    term.unicode.activeVersion = '11'
    term.open(containerRef.current)
    fitAddon.fit() // immediate baseline

    // Time-debounced fit. The mobile soft keyboard fires a BURST of viewport
    // resizes across its open/close animation; fitting on each one churns the
    // PTY row count and leaves piles of blank rows (the "공백" after toggling the
    // keyboard). Instead, fit once ~140ms after the size settles, skip it when
    // the dimensions didn't actually change, and anchor to the bottom so no gap
    // shows above the prompt.
    let fitTimer = 0
    const safeFit = () => {
      clearTimeout(fitTimer)
      fitTimer = window.setTimeout(() => {
        try {
          const dims = fitAddon.proposeDimensions()
          if (dims && (dims.rows !== term.rows || dims.cols !== term.cols)) {
            fitAddon.fit()
            term.scrollToBottom()
          }
        } catch { /* container not laid out yet */ }
      }, 140)
    }
    // Re-fit once the monospace webfont is ready: its cell metrics differ from
    // the fallback, and fitting with fallback metrics yields a wrong row count.
    document.fonts?.ready.then(() => safeFit()).catch(() => {})
    termRef.current = term
    fitAddonRef.current = fitAddon
    searchAddonRef.current = searchAddon

    const token = api.getToken()
    if (!token) {
      term.writeln('\r\n\x1b[31m' + t('terminal.notAuthenticated') + '\x1b[0m')
      return
    }

    // WS setup is async (ticket mint). connect() is re-invocable so a dropped
    // socket transparently reconnects to the SAME session_id: the server keeps
    // the PTY alive (stable session key, scrollback replay, 5-min idle grace),
    // so a transient drop (Wi-Fi blip, sleep, reverse-proxy idle timeout)
    // resumes the live session instead of leaving a dead terminal. The
    // term-level input/resize listeners are registered once and always target
    // the current socket via wsRef.
    let disposed = false
    let wsCleanup: (() => void) | null = null
    let reconnectTimer = 0
    let stableTimer = 0
    let attempts = 0
    const maxReconnectAttempts = 6

    const onDataDisposable = term.onData((data) => {
      const sock = wsRef.current
      if (sock && sock.readyState === WebSocket.OPEN) {
        sock.send(new TextEncoder().encode(data))
      }
    })
    const onResizeDisposable = term.onResize(({ cols, rows }) => {
      const sock = wsRef.current
      if (sock && sock.readyState === WebSocket.OPEN) {
        sock.send(JSON.stringify({ type: 'resize', cols, rows }))
      }
    })

    const connect = async () => {
      const wsUrl = await api.buildWsUrl('/ws/terminal', { session_id: sessionId })
      if (disposed) return
      const ws = new WebSocket(wsUrl)
      wsRef.current = ws
      ws.binaryType = 'arraybuffer'

      ws.onopen = () => {
        term.focus()
        const { cols, rows } = term
        ws.send(JSON.stringify({ type: 'resize', cols, rows }))
        // Only clear the backoff once the socket has proven stable, so a server
        // that accepts then instantly drops can't spin in a tight reconnect loop.
        stableTimer = window.setTimeout(() => { attempts = 0 }, 3000)
      }

      ws.onmessage = (event) => {
        if (event.data instanceof ArrayBuffer) {
          term.write(new Uint8Array(event.data))
        } else {
          term.write(event.data)
        }
      }

      ws.onerror = () => {
        // Swallow: onclose fires next and drives the reconnect/disconnect notice.
      }

      ws.onclose = () => {
        clearTimeout(stableTimer)
        if (disposed) return
        if (attempts < maxReconnectAttempts) {
          const delay = Math.min(1000 * 2 ** attempts, 10000)
          attempts += 1
          term.writeln('\r\n\x1b[33m' + t('terminal.reconnecting') + '\x1b[0m')
          reconnectTimer = window.setTimeout(() => { void connect() }, delay)
        } else {
          term.writeln('\r\n\x1b[31m' + t('terminal.disconnected') + '\x1b[0m')
        }
      }
    }

    void connect()

    wsCleanup = () => {
      clearTimeout(reconnectTimer)
      clearTimeout(stableTimer)
      onDataDisposable.dispose()
      onResizeDisposable.dispose()
      const sock = wsRef.current
      if (sock) {
        sock.onclose = null // intentional teardown — must not schedule a reconnect
        sock.close()
      }
    }

    // ResizeObserver fires AFTER the container's box actually changes (keyboard
    // open/close via --app-h, orientation, tab switch), so the fit measures the
    // real post-reflow height — unlike a visualViewport 'resize' that can fire
    // before the CSS height reflows. window/visualViewport stay as a fallback
    // for browsers that miss some container resizes.
    const container = containerRef.current
    const ro = new ResizeObserver(() => safeFit())
    if (container) ro.observe(container)
    const handleResize = () => safeFit()
    window.addEventListener('resize', handleResize)
    window.visualViewport?.addEventListener('resize', handleResize)

    // xterm v6's viewport scrolls only via wheel events, so on a touch device a
    // drag can't reach the scrollback (the .xterm-viewport has no natively
    // scrollable area). Translate a vertical touch-drag into term.scrollLines so
    // mobile can scroll up through output history.
    let touchY = 0
    let touchAccum = 0
    const cellPx = () => {
      const screenEl = container?.querySelector('.xterm-screen') as HTMLElement | null
      return screenEl && term.rows ? screenEl.clientHeight / term.rows : 17
    }
    const onTouchStart = (e: TouchEvent) => {
      if (e.touches.length !== 1) return
      touchY = e.touches[0].clientY
      touchAccum = 0
    }
    const onTouchMove = (e: TouchEvent) => {
      if (e.touches.length !== 1) return
      const y = e.touches[0].clientY
      touchAccum += touchY - y
      touchY = y
      const lines = Math.trunc(touchAccum / cellPx())
      if (lines !== 0) {
        term.scrollLines(lines)
        touchAccum -= lines * cellPx()
        e.preventDefault()
      }
    }
    // Capture phase so we run before any xterm internal touch handler that
    // might stopPropagation; passive:false on touchmove so preventDefault works.
    container?.addEventListener('touchstart', onTouchStart, { capture: true, passive: true })
    container?.addEventListener('touchmove', onTouchMove, { capture: true, passive: false })

    // Re-fit when the terminal gains focus (user tapped to type). This
    // self-corrects the size when the page loaded with the keyboard already up
    // and the viewport-change events that normally drive the fit never fired —
    // the second fit lands after the keyboard's open animation settles.
    const onFocusIn = () => { safeFit(); window.setTimeout(safeFit, 400) }
    container?.addEventListener('focusin', onFocusIn)

    return () => {
      disposed = true
      clearTimeout(fitTimer)
      ro.disconnect()
      window.removeEventListener('resize', handleResize)
      window.visualViewport?.removeEventListener('resize', handleResize)
      container?.removeEventListener('touchstart', onTouchStart, { capture: true })
      container?.removeEventListener('touchmove', onTouchMove, { capture: true })
      container?.removeEventListener('focusin', onFocusIn)
      wsCleanup?.()
      term.dispose()
    }
  }, [sessionId]) // eslint-disable-line react-hooks/exhaustive-deps

  // Update font size dynamically
  useEffect(() => {
    if (termRef.current) {
      termRef.current.options.fontSize = fontSize
      fitAddonRef.current?.fit()
    }
  }, [fontSize])

  // Re-fit and focus when tab becomes active
  useEffect(() => {
    if (active && fitAddonRef.current && termRef.current) {
      setTimeout(() => {
        fitAddonRef.current?.fit()
        termRef.current?.focus()
      }, 50)
    }
  }, [active])

  // Expose search addon and ws for parent access
  useEffect(() => {
    const el = containerRef.current as TerminalSessionElement | null
    if (!el) return
    if (searchAddonRef.current) el.__searchAddon = searchAddonRef.current
    el.__wsRef = wsRef
    el.__termRef = termRef
  }, [])

  // data-terminal-session is a stable hook for the parent's active-session
  // lookup (search/clear/key forwarding). Querying by Tailwind class substrings
  // broke silently when a className was reordered during the UI-polish churn;
  // this attribute is decoupled from styling.
  return (
    <div
      ref={containerRef}
      data-terminal-session={active ? 'active' : 'inactive'}
      className={cn(
        // touch-none: xterm v6's viewport isn't natively touch-scrollable, so we
        // drive scrollback from a touch-drag handler (see the effect above) —
        // disable the browser's own touch gestures here so they can't preempt it.
        'w-full h-full touch-none',
        active ? 'block' : 'hidden'
      )}
    />
  )
}

function MobileTerminalBar({ onSendKey }: { onSendKey: (data: string) => void }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [ctrlActive, setCtrlActive] = useState(false)
  const [altActive, setAltActive] = useState(false)

  const sendKey = (key: string) => {
    let data = key
    if (ctrlActive) {
      // Convert to ctrl sequence: Ctrl+C = \x03, Ctrl+D = \x04, etc.
      if (key.length === 1) {
        const code = key.toUpperCase().charCodeAt(0) - 64
        if (code > 0 && code < 32) data = String.fromCharCode(code)
      }
      setCtrlActive(false)
    }
    if (altActive) {
      data = '\x1b' + key
      setAltActive(false)
    }
    onSendKey(data)
  }

  const keys = [
    { label: 'Esc', data: '\x1b' },
    { label: 'Tab', data: '\t' },
    { label: 'Ctrl', toggle: 'ctrl' as const },
    { label: 'Alt', toggle: 'alt' as const },
    { label: '↑', data: '\x1b[A' },
    { label: '↓', data: '\x1b[B' },
    { label: '←', data: '\x1b[D' },
    { label: '→', data: '\x1b[C' },
    { label: '|', data: '|' },
    { label: '/', data: '/' },
    { label: '~', data: '~' },
    { label: '-', data: '-' },
  ]

  return (
    <div className="md:hidden shrink-0 bg-card border-t border-border">
      {/* Special keys row */}
      <div className="flex items-center gap-0.5 px-1 py-1 overflow-x-auto no-scrollbar">
        {keys.map((k) => (
          <button
            key={k.label}
            className={cn(
              'shrink-0 px-2.5 py-1.5 rounded text-[11px] font-medium transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0',
              (k.toggle === 'ctrl' && ctrlActive) || (k.toggle === 'alt' && altActive)
                ? 'bg-primary text-primary-foreground'
                : 'bg-secondary text-foreground active:bg-accent'
            )}
            onClick={() => {
              if (k.toggle === 'ctrl') { setCtrlActive(!ctrlActive); setAltActive(false) }
              else if (k.toggle === 'alt') { setAltActive(!altActive); setCtrlActive(false) }
              else if (k.data) sendKey(k.data)
            }}
          >
            {k.label}
          </button>
        ))}
      </div>
      {/* Navigation row */}
      <div className="flex items-center justify-around h-10 pb-safe border-t border-border">
        <button
          onClick={() => navigate('/dashboard')}
          className="flex flex-col items-center justify-center flex-1 h-full text-muted-foreground active:text-foreground outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/40"
        >
          <span className="text-[10px] font-medium">← {t('layout.mobileNav.dashboard')}</span>
        </button>
        <button
          onClick={() => onSendKey('\x03')}
          className="flex flex-col items-center justify-center flex-1 h-full text-destructive active:opacity-70 outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/40"
        >
          <span className="text-[10px] font-semibold">Ctrl+C</span>
        </button>
        <button
          onClick={() => onSendKey('\x04')}
          className="flex flex-col items-center justify-center flex-1 h-full text-warning active:opacity-70 outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/40"
        >
          <span className="text-[10px] font-semibold">Ctrl+D</span>
        </button>
        <button
          onClick={() => onSendKey('\x1a')}
          className="flex flex-col items-center justify-center flex-1 h-full text-muted-foreground active:text-foreground outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring/40"
        >
          <span className="text-[10px] font-semibold">Ctrl+Z</span>
        </button>
      </div>
    </div>
  )
}

export default function TerminalPage() {
  const { t } = useTranslation()
  // Make sure there's always at least one tab on first render — guarantees
  // the rest of the page can safely assume tabs[0] exists, and removes the
  // setState-in-effect pattern that used to call addTab() during mount.
  const [tabs, setTabs] = useState<Tab[]>(() => {
    const persisted = loadTabs()
    if (persisted.length > 0) return persisted
    return [{ id: generateTabId(), title: `Terminal ${tabCounter}` }]
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
  const editInputRef = useRef<HTMLInputElement>(null)
  const searchInputRef = useRef<HTMLInputElement>(null)

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

  const addTab = useCallback(() => {
    const id = generateTabId()
    const num = tabCounter
    setTabs(prev => [...prev, { id, title: `Terminal ${num}` }])
    setActiveTab(id)
  }, [])

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

  const reattachSession = useCallback((sessionId: string) => {
    setTabs(prev => {
      if (prev.find(tb => tb.id === sessionId)) return prev
      return [...prev, { id: sessionId, title: `Reattached ${sessionId.slice(0, 8)}` }]
    })
    setActiveTab(sessionId)
    setReattachOpen(false)
  }, [])

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
    // Find the active terminal's search addon
    const termContainers = document.querySelectorAll('[data-terminal-session="active"]')
    termContainers.forEach(el => {
      const addon = (el as TerminalSessionElement).__searchAddon
      if (addon && query) {
        addon.findNext(query)
      }
    })
  }, [])

  const handleSearchNext = useCallback(() => {
    const termContainers = document.querySelectorAll('[data-terminal-session="active"]')
    termContainers.forEach(el => {
      const addon = (el as TerminalSessionElement).__searchAddon
      if (addon && searchQuery) addon.findNext(searchQuery)
    })
  }, [searchQuery])

  const handleSearchPrev = useCallback(() => {
    const termContainers = document.querySelectorAll('[data-terminal-session="active"]')
    termContainers.forEach(el => {
      const addon = (el as TerminalSessionElement).__searchAddon
      if (addon && searchQuery) addon.findPrevious(searchQuery)
    })
  }, [searchQuery])

  const clearTerminal = useCallback(() => {
    const termContainers = document.querySelectorAll('[data-terminal-session="active"]')
    termContainers.forEach(el => {
      const termRef = (el as TerminalSessionElement).__termRef
      const wsRef = (el as TerminalSessionElement).__wsRef
      if (termRef?.current) termRef.current.clear()
      if (wsRef?.current && wsRef.current.readyState === WebSocket.OPEN) {
        // Send Ctrl-L (0x0c) — the universal "clear screen" terminal
        // signal that any TUI (vim/less/mysql) intercepts correctly.
        // The previous literal 'clear\r' executed as a shell command
        // only when the cursor was at a $ prompt and was meaningless
        // (or actively harmful, e.g. typing 'clear' inside an editor)
        // anywhere else.
        wsRef.current.send(new TextEncoder().encode('\x0c'))
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
      if (e.key === 'Escape' && searchOpen) {
        setSearchOpen(false)
        setSearchQuery('')
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [searchOpen])

  const sendKeyToActiveTerminal = useCallback((data: string) => {
    const termContainers = document.querySelectorAll('[data-terminal-session="active"]')
    termContainers.forEach(el => {
      const wsRef = (el as TerminalSessionElement).__wsRef
      const termRef = (el as TerminalSessionElement).__termRef
      if (wsRef?.current && wsRef.current.readyState === WebSocket.OPEN) {
        wsRef.current.send(new TextEncoder().encode(data))
      }
      termRef?.current?.focus()
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
          <div className="relative">
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
