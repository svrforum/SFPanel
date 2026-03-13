import { useState, useEffect, useRef, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Terminal as TerminalIcon, Plus, X, Minus, Search } from 'lucide-react'
import { Terminal as XTerm } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import { SearchAddon } from '@xterm/addon-search'
import '@xterm/xterm/css/xterm.css'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

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
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return `term-${crypto.randomUUID()}`
  }
  tabCounter++
  return `term-${Date.now()}-${tabCounter}`
}

function loadTabs(): Tab[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw) {
      const tabs = JSON.parse(raw) as Tab[]
      if (Array.isArray(tabs) && tabs.length > 0) {
        for (const t of tabs) {
          const match = t.id.match(/^term-(\d+)$/)
          if (match) tabCounter = Math.max(tabCounter, parseInt(match[1], 10))
        }
        return tabs.map((tab) => (
          /^term-[0-9a-f-]{16,}$/i.test(tab.id) ? tab : { ...tab, id: generateTabId() }
        ))
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

function TerminalSession({ sessionId, active, fontSize }: { sessionId: string; active: boolean; fontSize: number }) {
  const containerRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<XTerm | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const fitAddonRef = useRef<FitAddon | null>(null)
  const searchAddonRef = useRef<SearchAddon | null>(null)
  const initialized = useRef(false)

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
    term.loadAddon(fitAddon)
    term.loadAddon(new WebLinksAddon())
    term.loadAddon(searchAddon)
    term.open(containerRef.current)
    fitAddon.fit()
    termRef.current = term
    fitAddonRef.current = fitAddon
    searchAddonRef.current = searchAddon

    const token = api.getToken()
    if (!token) {
      term.writeln('\r\n\x1b[31mNot authenticated. Please log in.\x1b[0m')
      return
    }

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsUrl = `${protocol}//${window.location.host}/ws/terminal?session_id=${encodeURIComponent(sessionId)}`
    const ws = new WebSocket(wsUrl, api.getWebSocketProtocols())
    wsRef.current = ws

    ws.binaryType = 'arraybuffer'

    ws.onopen = () => {
      term.focus()
      const { cols, rows } = term
      ws.send(JSON.stringify({ type: 'resize', cols, rows }))
    }

    ws.onmessage = (event) => {
      if (event.data instanceof ArrayBuffer) {
        term.write(new Uint8Array(event.data))
      } else {
        term.write(event.data)
      }
    }

    ws.onerror = () => {
      term.writeln('\r\n\x1b[31mWebSocket error\x1b[0m')
    }

    ws.onclose = () => {
      term.writeln('\r\n\x1b[33mConnection closed\x1b[0m')
    }

    const onDataDisposable = term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(new TextEncoder().encode(data))
      }
    })

    const onResizeDisposable = term.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'resize', cols, rows }))
      }
    })

    const handleResize = () => {
      fitAddon.fit()
    }
    window.addEventListener('resize', handleResize)

    return () => {
      window.removeEventListener('resize', handleResize)
      onDataDisposable.dispose()
      onResizeDisposable.dispose()
      ws.close()
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

  // Expose search addon
  useEffect(() => {
    const el = containerRef.current
    if (el && searchAddonRef.current) {
      (el as any).__searchAddon = searchAddonRef.current
    }
  }, [])

  return (
    <div
      ref={containerRef}
      className={cn(
        'w-full h-full',
        active ? 'block' : 'hidden'
      )}
    />
  )
}

export default function TerminalPage() {
  const { t } = useTranslation()
  const [tabs, setTabs] = useState<Tab[]>(() => loadTabs())
  const [activeTab, setActiveTab] = useState<string>(() => loadActiveTab())
  const [fontSize, setFontSize] = useState(() => loadFontSize())
  const [editingTabId, setEditingTabId] = useState<string | null>(null)
  const [editingTabName, setEditingTabName] = useState('')
  const [searchOpen, setSearchOpen] = useState(false)
  const [searchQuery, setSearchQuery] = useState('')
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
    const termContainers = document.querySelectorAll('[class*="w-full h-full"][class*="block"]')
    termContainers.forEach(el => {
      const addon = (el as any).__searchAddon
      if (addon && query) {
        addon.findNext(query)
      }
    })
  }, [])

  const handleSearchNext = useCallback(() => {
    const termContainers = document.querySelectorAll('[class*="w-full h-full"][class*="block"]')
    termContainers.forEach(el => {
      const addon = (el as any).__searchAddon
      if (addon && searchQuery) addon.findNext(searchQuery)
    })
  }, [searchQuery])

  const handleSearchPrev = useCallback(() => {
    const termContainers = document.querySelectorAll('[class*="w-full h-full"][class*="block"]')
    termContainers.forEach(el => {
      const addon = (el as any).__searchAddon
      if (addon && searchQuery) addon.findPrevious(searchQuery)
    })
  }, [searchQuery])

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

  // Create initial tab if none exist
  useEffect(() => {
    if (tabs.length === 0) {
      addTab()
    } else if (!activeTab || !tabs.find(t => t.id === activeTab)) {
      setActiveTab(tabs[0].id)
    }
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <div className="flex flex-col h-[calc(100vh-3rem)] -m-6">
      {/* Tab Bar */}
      <div className="flex items-center bg-[#1a1b26] border-b border-[#292e42] px-2 shrink-0">
        <div className="flex items-center gap-0.5 overflow-x-auto py-1 flex-1">
          {tabs.map((tab) => (
            <div
              key={tab.id}
              className={cn(
                'flex items-center gap-1.5 px-3 py-1.5 rounded-t text-xs cursor-pointer select-none group transition-colors',
                activeTab === tab.id
                  ? 'bg-[#24283b] text-[#c0caf5]'
                  : 'text-[#565f89] hover:text-[#a9b1d6] hover:bg-[#1f2335]'
              )}
              onClick={() => setActiveTab(tab.id)}
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
                  className="bg-transparent border-b border-[#7aa2f7] outline-none text-[#c0caf5] w-20 text-xs"
                  autoFocus
                />
              ) : (
                <span>{tab.title}</span>
              )}
              <button
                className={cn(
                  'ml-1 rounded p-0.5 transition-colors',
                  'opacity-0 group-hover:opacity-100',
                  activeTab === tab.id && 'opacity-60',
                  'hover:bg-[#414868] hover:text-[#f7768e]'
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
            className="h-6 w-6 p-0 text-[#565f89] hover:text-[#c0caf5] hover:bg-[#1f2335]"
            onClick={() => adjustFontSize(-1)}
            title={t('terminal.fontSmaller')}
          >
            <Minus className="h-3 w-3" />
          </Button>
          <span className="text-[10px] text-[#565f89] min-w-[20px] text-center">{fontSize}</span>
          <Button
            variant="ghost"
            size="sm"
            className="h-6 w-6 p-0 text-[#565f89] hover:text-[#c0caf5] hover:bg-[#1f2335]"
            onClick={() => adjustFontSize(1)}
            title={t('terminal.fontLarger')}
          >
            <Plus className="h-3 w-3" />
          </Button>
          <div className="w-px h-4 bg-[#292e42] mx-1" />
          {/* Search */}
          <Button
            variant="ghost"
            size="sm"
            className={cn(
              "h-6 w-6 p-0 hover:bg-[#1f2335]",
              searchOpen ? 'text-[#7aa2f7]' : 'text-[#565f89] hover:text-[#c0caf5]'
            )}
            onClick={() => {
              setSearchOpen(!searchOpen)
              if (!searchOpen) setTimeout(() => searchInputRef.current?.focus(), 0)
              else setSearchQuery('')
            }}
            title={t('terminal.search')}
          >
            <Search className="h-3.5 w-3.5" />
          </Button>
          <div className="w-px h-4 bg-[#292e42] mx-1" />
          {/* New tab */}
          <Button
            variant="ghost"
            size="sm"
            className="h-6 w-6 p-0 text-[#565f89] hover:text-[#c0caf5] hover:bg-[#1f2335]"
            onClick={addTab}
            title={t('terminal.newTab')}
          >
            <Plus className="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>

      {/* Search bar */}
      {searchOpen && (
        <div className="flex items-center gap-2 bg-[#1f2335] border-b border-[#292e42] px-3 py-1.5">
          <Search className="h-3.5 w-3.5 text-[#565f89]" />
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
            className="h-6 text-xs bg-[#1a1b26] border-[#292e42] text-[#c0caf5] flex-1 max-w-xs"
            autoFocus
          />
          <Button
            variant="ghost"
            size="sm"
            className="h-6 px-2 text-xs text-[#565f89] hover:text-[#c0caf5] hover:bg-[#1a1b26]"
            onClick={handleSearchPrev}
          >
            {t('terminal.prev')}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="h-6 px-2 text-xs text-[#565f89] hover:text-[#c0caf5] hover:bg-[#1a1b26]"
            onClick={handleSearchNext}
          >
            {t('terminal.next')}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            className="h-6 w-6 p-0 text-[#565f89] hover:text-[#c0caf5] hover:bg-[#1a1b26]"
            onClick={() => { setSearchOpen(false); setSearchQuery('') }}
          >
            <X className="h-3.5 w-3.5" />
          </Button>
        </div>
      )}

      {/* Terminal Area */}
      <div className="flex-1 bg-[#1a1b26] relative min-h-0">
        {tabs.map((tab) => (
          <TerminalSession
            key={tab.id}
            sessionId={tab.id}
            active={activeTab === tab.id}
            fontSize={fontSize}
          />
        ))}
        {tabs.length === 0 && (
          <div className="flex items-center justify-center h-full text-[#565f89]">
            <div className="text-center">
              <TerminalIcon className="h-12 w-12 mx-auto mb-3 opacity-50" />
              <p>{t('terminal.noTabs')}</p>
              <Button
                variant="outline"
                size="sm"
                className="mt-3 border-[#414868] text-[#a9b1d6] hover:bg-[#1f2335]"
                onClick={addTab}
              >
                <Plus className="h-4 w-4 mr-1" />
                {t('terminal.newTab')}
              </Button>
            </div>
          </div>
        )}
      </div>
    </div>
  )
}
