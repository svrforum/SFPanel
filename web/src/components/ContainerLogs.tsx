import { useState, useEffect, useRef, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Search, X, ChevronUp, ChevronDown, Download } from 'lucide-react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { SearchAddon } from '@xterm/addon-search'
import '@xterm/xterm/css/xterm.css'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

interface ContainerLogsProps {
  containerId: string
}

export default function ContainerLogs({ containerId }: ContainerLogsProps) {
  const { t } = useTranslation()
  const terminalRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal | null>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const searchAddonRef = useRef<SearchAddon | null>(null)
  const logLinesRef = useRef<string[]>([])
  const [searchOpen, setSearchOpen] = useState(false)
  const [searchQuery, setSearchQuery] = useState('')

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
    a.download = `container-${containerId.substring(0, 12)}-logs.txt`
    a.click()
    URL.revokeObjectURL(url)
  }, [containerId])

  useEffect(() => {
    if (!terminalRef.current) return

    const term = new Terminal({
      cursorBlink: false,
      disableStdin: true,
      fontSize: 13,
      fontFamily: 'Menlo, Monaco, "Courier New", monospace',
      theme: {
        background: '#1e1e1e',
        foreground: '#d4d4d4',
      },
      convertEol: true,
      scrollback: 5000,
    })

    const fitAddon = new FitAddon()
    const searchAddon = new SearchAddon()
    term.loadAddon(fitAddon)
    term.loadAddon(searchAddon)
    term.open(terminalRef.current)
    fitAddon.fit()
    termRef.current = term
    searchAddonRef.current = searchAddon
    logLinesRef.current = []

    term.writeln(t('terminal.connectingLogs'))

    const token = api.getToken()
    if (!token) {
      term.writeln(`\r\n${t('terminal.notAuthenticated')}`)
      return
    }

    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsUrl = `${protocol}//${window.location.host}/ws/docker/containers/${containerId}/logs?token=${token}`
    const ws = new WebSocket(wsUrl)
    wsRef.current = ws

    ws.onopen = () => {
      term.writeln(`${t('terminal.connected')}\r\n`)
    }

    ws.onmessage = (event) => {
      const data = event.data as string
      logLinesRef.current.push(data.replace(/\n$/, ''))
      term.write(data)
    }

    ws.onerror = () => {
      term.writeln(`\r\n${t('terminal.wsError')}`)
    }

    ws.onclose = () => {
      term.writeln(`\r\n${t('terminal.connectionClosed')}`)
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
  }, [containerId, t])

  // Update search when query changes
  useEffect(() => {
    if (searchAddonRef.current && searchQuery) {
      searchAddonRef.current.findNext(searchQuery)
    }
  }, [searchQuery])

  return (
    <div className="space-y-1">
      {/* Toolbar */}
      <div className="flex items-center justify-end gap-1">
        <Button
          variant="ghost"
          size="icon-xs"
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
          title={t('logs.download')}
          onClick={handleDownload}
        >
          <Download className="h-3.5 w-3.5" />
        </Button>
      </div>

      {/* Search bar */}
      {searchOpen && (
        <div className="flex items-center gap-1 px-1">
          <div className="relative flex-1">
            <Search className="absolute left-2 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground" />
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
              className="h-7 pl-7 text-xs"
              autoFocus
            />
          </div>
          <Button variant="ghost" size="icon-xs" onClick={handleSearchPrev} title={t('terminal.prev')}>
            <ChevronUp className="h-3.5 w-3.5" />
          </Button>
          <Button variant="ghost" size="icon-xs" onClick={handleSearchNext} title={t('terminal.next')}>
            <ChevronDown className="h-3.5 w-3.5" />
          </Button>
          <Button
            variant="ghost"
            size="icon-xs"
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

      <div
        ref={terminalRef}
        className="h-[400px] w-full rounded-md overflow-hidden"
      />
    </div>
  )
}
