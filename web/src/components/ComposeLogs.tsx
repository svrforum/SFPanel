import { useState, useEffect, useRef, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Search, X, ChevronUp, ChevronDown, Download, ArrowDownToLine, Circle } from 'lucide-react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { SearchAddon } from '@xterm/addon-search'
import '@xterm/xterm/css/xterm.css'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

interface ComposeLogsProps {
  project: string
  serviceNames: string[]
}

const TAIL_OPTIONS = [
  { label: '100', value: 100 },
  { label: '500', value: 500 },
  { label: '1000', value: 1000 },
  { label: 'All', value: 0 },
]

export default function ComposeLogs({ project, serviceNames }: ComposeLogsProps) {
  const { t } = useTranslation()
  const terminalRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const fitAddonRef = useRef<FitAddon | null>(null)
  const searchAddonRef = useRef<SearchAddon | null>(null)
  const logLinesRef = useRef<string[]>([])
  const [searchOpen, setSearchOpen] = useState(false)
  const [searchQuery, setSearchQuery] = useState('')
  const [connected, setConnected] = useState(false)
  const [autoScroll, setAutoScroll] = useState(true)
  const [selectedService, setSelectedService] = useState('')
  const [tail, setTail] = useState(100)

  const handleSearchNext = useCallback(() => {
    if (searchAddonRef.current && searchQuery) {
      searchAddonRef.current.findNext(searchQuery)
    }
  }, [searchQuery])

  const handleSearchPrev = useCallback(() => {
    if (searchAddonRef.current && searchQuery) {
      searchAddonRef.current.findPrevious(searchQuery)
    }
  }, [searchQuery])

  const handleDownload = useCallback(() => {
    const content = logLinesRef.current.join('\n')
    const blob = new Blob([content], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${project}${selectedService ? `-${selectedService}` : ''}-logs.txt`
    a.click()
    URL.revokeObjectURL(url)
  }, [project, selectedService])

  useEffect(() => {
    if (!terminalRef.current) return

    const term = new Terminal({
      cursorBlink: false,
      disableStdin: true,
      fontSize: 12,
      fontFamily: '"SF Mono", Menlo, Monaco, "Courier New", monospace',
      lineHeight: 1.4,
      theme: {
        background: '#0a0a0a',
        foreground: '#e5e5e5',
        selectionBackground: '#3182f644',
        selectionForeground: '#ffffff',
      },
      convertEol: true,
      scrollback: 10000,
    })

    const fitAddon = new FitAddon()
    const searchAddon = new SearchAddon()
    term.loadAddon(fitAddon)
    term.loadAddon(searchAddon)
    term.open(terminalRef.current)
    fitAddon.fit()
    termRef.current = term
    fitAddonRef.current = fitAddon
    searchAddonRef.current = searchAddon
    logLinesRef.current = []

    const token = api.getToken()
    if (!token) {
      term.writeln(`\x1b[31m${t('terminal.notAuthenticated')}\x1b[0m`)
      return () => { term.dispose() }
    }

    const wsPath = `/ws/docker/compose/${encodeURIComponent(project)}/logs`
    const extraParams: Record<string, string> = {}
    if (tail > 0) extraParams.tail = String(tail)
    if (selectedService) extraParams.service = selectedService

    const wsUrl = api.buildWsUrl(wsPath, extraParams)
    const ws = new WebSocket(wsUrl)
    wsRef.current = ws

    ws.onopen = () => {
      setConnected(true)
    }

    ws.onmessage = (event) => {
      const data = event.data as string
      logLinesRef.current.push(data.replace(/\n$/, ''))
      term.write(data)
    }

    ws.onerror = () => {
      term.writeln(`\r\n\x1b[31m${t('terminal.wsError')}\x1b[0m`)
    }

    ws.onclose = () => {
      setConnected(false)
      term.writeln(`\r\n\x1b[2m${t('terminal.disconnected')}\x1b[0m`)
    }

    const handleResize = () => {
      fitAddon.fit()
    }
    window.addEventListener('resize', handleResize)

    return () => {
      window.removeEventListener('resize', handleResize)
      ws.close()
      term.dispose()
    }
  }, [project, selectedService, tail, t])

  useEffect(() => {
    if (searchAddonRef.current && searchQuery) {
      searchAddonRef.current.findNext(searchQuery)
    }
  }, [searchQuery])

  useEffect(() => {
    if (!termRef.current || !autoScroll) return
    const term = termRef.current
    const disposable = term.onWriteParsed(() => {
      term.scrollToBottom()
    })
    return () => disposable.dispose()
  }, [autoScroll])

  return (
    <div className="bg-[#0a0a0a] rounded-2xl overflow-hidden card-shadow">
      {/* Toolbar */}
      <div className="flex items-center justify-between px-3 py-2 bg-[#111111] border-b border-white/[0.06]">
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-1.5">
            <Circle className={`h-2 w-2 fill-current ${connected ? 'text-[#00c471]' : 'text-[#f04452]'}`} />
            <span className="text-[11px] text-white/40 font-medium">
              {connected ? t('terminal.connected') : t('terminal.disconnected')}
            </span>
          </div>

          {/* Service filter */}
          <select
            value={selectedService}
            onChange={(e) => setSelectedService(e.target.value)}
            className="h-6 px-2 text-[11px] bg-white/[0.06] border border-white/[0.08] text-white/70 rounded-md focus:outline-none focus:ring-1 focus:ring-[#3182f6]/50"
          >
            <option value="">{t('docker.stacks.allServices', 'All services')}</option>
            {serviceNames.map(name => (
              <option key={name} value={name}>{name}</option>
            ))}
          </select>

          {/* Tail selector */}
          <select
            value={tail}
            onChange={(e) => setTail(Number(e.target.value))}
            className="h-6 px-2 text-[11px] bg-white/[0.06] border border-white/[0.08] text-white/70 rounded-md focus:outline-none focus:ring-1 focus:ring-[#3182f6]/50"
          >
            {TAIL_OPTIONS.map(opt => (
              <option key={opt.value} value={opt.value}>
                {opt.value === 0 ? t('docker.stacks.allLogs', 'All') : `${t('docker.stacks.lastLines', 'Last')} ${opt.label}`}
              </option>
            ))}
          </select>
        </div>
        <div className="flex items-center gap-0.5">
          <Button
            variant="ghost"
            size="icon-xs"
            className={`text-white/40 hover:text-white hover:bg-white/10 ${autoScroll ? 'text-[#3182f6]' : ''}`}
            title="Auto-scroll"
            onClick={() => {
              setAutoScroll(!autoScroll)
              if (!autoScroll) termRef.current?.scrollToBottom()
            }}
          >
            <ArrowDownToLine className="h-3.5 w-3.5" />
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            className={`text-white/40 hover:text-white hover:bg-white/10 ${searchOpen ? 'text-[#3182f6]' : ''}`}
            title={t('terminal.search')}
            onClick={() => {
              setSearchOpen(!searchOpen)
              if (searchOpen) {
                setSearchQuery('')
                searchAddonRef.current?.clearDecorations()
              }
            }}
          >
            <Search className="h-3.5 w-3.5" />
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            className="text-white/40 hover:text-white hover:bg-white/10"
            title={t('logs.download')}
            onClick={handleDownload}
          >
            <Download className="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>

      {/* Search bar */}
      {searchOpen && (
        <div className="flex items-center gap-1.5 px-3 py-1.5 bg-[#111111] border-b border-white/[0.06]">
          <div className="relative flex-1">
            <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3 w-3 text-white/30" />
            <Input
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.shiftKey ? handleSearchPrev() : handleSearchNext()
                } else if (e.key === 'Escape') {
                  setSearchOpen(false)
                  setSearchQuery('')
                  searchAddonRef.current?.clearDecorations()
                }
              }}
              placeholder={t('terminal.searchPlaceholder')}
              className="h-7 pl-7 text-[12px] bg-white/[0.06] border-white/[0.08] text-white placeholder:text-white/30 rounded-lg focus-visible:ring-[#3182f6]/50"
              autoFocus
            />
          </div>
          <Button variant="ghost" size="icon-xs" onClick={handleSearchPrev}
            className="text-white/40 hover:text-white hover:bg-white/10" title={t('terminal.prev')}>
            <ChevronUp className="h-3.5 w-3.5" />
          </Button>
          <Button variant="ghost" size="icon-xs" onClick={handleSearchNext}
            className="text-white/40 hover:text-white hover:bg-white/10" title={t('terminal.next')}>
            <ChevronDown className="h-3.5 w-3.5" />
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
            className="text-white/40 hover:text-white hover:bg-white/10"
            onClick={() => {
              setSearchOpen(false)
              setSearchQuery('')
              searchAddonRef.current?.clearDecorations()
            }}
          >
            <X className="h-3.5 w-3.5" />
          </Button>
        </div>
      )}

      {/* Terminal */}
      <div
        ref={terminalRef}
        className="h-[500px] w-full px-1 pt-1"
      />
    </div>
  )
}
