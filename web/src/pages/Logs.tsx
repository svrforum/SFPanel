import { useEffect, useState, useRef, useCallback, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { FileText, RefreshCw, Radio, ArrowDown, Trash2, Eye, Search, ChevronUp, ChevronDown, X, Download, Plus } from 'lucide-react'
import { Input } from '@/components/ui/input'
import { hasParsedView, getParser, parseLogLines, type LogEntry, type ParsedLogEntry, type ColumnDef } from '@/lib/logParsers'

interface LogSource {
  id: string
  name: string
  path: string
  size: number
  exists: boolean
  custom: boolean
  custom_id?: number
}

interface LogResponse {
  source: string
  lines: string[]
  total_lines: number
}

type LineCount = 100 | 500 | 1000 | 5000

const LINE_COUNT_OPTIONS: LineCount[] = [100, 500, 1000, 5000]


function highlightText(text: string, query: string) {
  if (!query) return text
  const parts: Array<{ text: string; match: boolean }> = []
  const lower = text.toLowerCase()
  const qLower = query.toLowerCase()
  let lastIndex = 0
  let idx = lower.indexOf(qLower)
  while (idx !== -1) {
    if (idx > lastIndex) {
      parts.push({ text: text.slice(lastIndex, idx), match: false })
    }
    parts.push({ text: text.slice(idx, idx + query.length), match: true })
    lastIndex = idx + query.length
    idx = lower.indexOf(qLower, lastIndex)
  }
  if (lastIndex < text.length) {
    parts.push({ text: text.slice(lastIndex), match: false })
  }
  return (
    <>
      {parts.map((part, i) =>
        part.match ? (
          <mark key={i} className="bg-yellow-400/80 text-black rounded-sm px-0.5">{part.text}</mark>
        ) : (
          <span key={i}>{part.text}</span>
        )
      )}
    </>
  )
}

function getLogLevel(line: string): 'error' | 'warn' | 'info' | 'debug' | null {
  // Skip empty JSON fields like "error":"" that cause false positives
  const cleaned = line.replace(/"error":""/g, '')
  const upper = cleaned.toUpperCase()
  // Common patterns: [ERROR], ERROR:, level=error, "level":"error", FATAL, CRITICAL, PANIC
  if (/\b(ERROR|FATAL|CRITICAL|PANIC|EMERG)\b/.test(upper)) return 'error'
  if (/\b(WARN|WARNING)\b/.test(upper)) return 'warn'
  if (/\b(INFO|NOTICE)\b/.test(upper)) return 'info'
  if (/\b(DEBUG|TRACE)\b/.test(upper)) return 'debug'
  return null
}

const LOG_LEVEL_COLORS: Record<string, string> = {
  error: 'border-l-2 border-l-red-500/70',
  warn: 'border-l-2 border-l-yellow-500/70',
  info: 'border-l-2 border-l-blue-500/50',
  debug: 'border-l-2 border-l-gray-500/40',
}

const LOG_LEVEL_TEXT_COLORS: Record<string, string> = {
  error: 'text-red-400',
  warn: 'text-yellow-400',
  info: 'text-gray-200',
  debug: 'text-gray-500',
}

export default function Logs() {
  const { t } = useTranslation()

  // Sources
  const [sources, setSources] = useState<LogSource[]>([])
  const [sourcesLoading, setSourcesLoading] = useState(true)

  // Log state
  const [selectedSource, setSelectedSource] = useState<string | null>(null)
  const [logLines, setLogLines] = useState<string[]>([])
  const [isLive, setIsLive] = useState(false)
  const [autoScroll, setAutoScroll] = useState(true)
  const [lineCount, setLineCount] = useState<LineCount>(500)
  const [logLoading, setLogLoading] = useState(false)
  const [totalLines, setTotalLines] = useState(0)

  // View mode
  const [viewMode, setViewMode] = useState<'raw' | 'parsed'>('parsed')

  // Search state
  const [searchOpen, setSearchOpen] = useState(false)
  const [searchQuery, setSearchQuery] = useState('')
  const [currentMatch, setCurrentMatch] = useState(0)
  const searchInputRef = useRef<HTMLInputElement>(null)

  // Delete source dialog
  const [deleteSourceTarget, setDeleteSourceTarget] = useState<LogSource | null>(null)

  // Custom source dialog
  const [addDialogOpen, setAddDialogOpen] = useState(false)
  const [newSourceName, setNewSourceName] = useState('')
  const [newSourcePath, setNewSourcePath] = useState('')
  const [addingSource, setAddingSource] = useState(false)

  // WebSocket state
  const [wsConnected, setWsConnected] = useState(false)
  const wsRef = useRef<WebSocket | null>(null)
  const logContainerRef = useRef<HTMLDivElement>(null)
  const autoScrollRef = useRef(autoScroll)

  // Keep the ref in sync so the WS message handler can read current value
  useEffect(() => {
    autoScrollRef.current = autoScroll
  }, [autoScroll])

  // Scroll to bottom helper
  const scrollToBottom = useCallback(() => {
    if (logContainerRef.current) {
      logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight
    }
  }, [])

  // Auto-scroll when new lines arrive
  useEffect(() => {
    if (autoScroll) {
      scrollToBottom()
    }
  }, [logLines, autoScroll, scrollToBottom])

  // Fetch log sources on mount
  useEffect(() => {
    loadSources()
  }, [])

  async function loadSources() {
    setSourcesLoading(true)
    try {
      const data = await api.getLogSources()
      setSources(data)
      // Auto-select the first available source
      if (data.length > 0 && !selectedSource) {
        const firstExisting = data.find((s: LogSource) => s.exists)
        if (firstExisting) {
          setSelectedSource(firstExisting.id)
        }
      }
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('logs.loadSourcesFailed')
      toast.error(message)
    } finally {
      setSourcesLoading(false)
    }
  }

  // Fetch log content when source or lineCount changes
  useEffect(() => {
    if (selectedSource) {
      loadLog(selectedSource, lineCount)
    }
  }, [selectedSource, lineCount])

  async function loadLog(source: string, lines: number) {
    setLogLoading(true)
    try {
      const data: LogResponse = await api.readLog(source, lines)
      setLogLines(data.lines)
      setTotalLines(data.total_lines)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('logs.loadLogFailed')
      toast.error(message)
      setLogLines([])
      setTotalLines(0)
    } finally {
      setLogLoading(false)
    }
  }

  // WebSocket lifecycle
  const connectWebSocket = useCallback((source: string) => {
    // Close any existing connection
    if (wsRef.current) {
      wsRef.current.close()
      wsRef.current = null
    }

    const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsUrl = `${wsProtocol}//${window.location.host}/ws/logs?source=${encodeURIComponent(source)}`
    const ws = new WebSocket(wsUrl, api.getWebSocketProtocols())

    ws.onopen = () => {
      setWsConnected(true)
    }

    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data)
        if (data.line !== undefined) {
          setLogLines((prev) => {
            const next = [...prev, data.line]
            return next.length > 5000 ? next.slice(-5000) : next
          })
        } else if (data.lines && Array.isArray(data.lines)) {
          setLogLines((prev) => {
            const next = [...prev, ...data.lines]
            return next.length > 5000 ? next.slice(-5000) : next
          })
        }
      } catch {
        // If the message is plain text, add it as a line
        if (typeof event.data === 'string' && event.data.trim()) {
          setLogLines((prev) => {
            const next = [...prev, event.data]
            return next.length > 5000 ? next.slice(-5000) : next
          })
        }
      }
    }

    ws.onerror = () => {
      setWsConnected(false)
      toast.error(t('logs.wsError'))
    }

    ws.onclose = () => {
      setWsConnected(false)
    }

    wsRef.current = ws
  }, [t])

  const disconnectWebSocket = useCallback(() => {
    if (wsRef.current) {
      wsRef.current.close()
      wsRef.current = null
    }
    setWsConnected(false)
  }, [])

  // Toggle live streaming
  function handleToggleLive() {
    if (isLive) {
      disconnectWebSocket()
      setIsLive(false)
    } else {
      if (!selectedSource) {
        toast.error(t('logs.selectSourceFirst'))
        return
      }
      setIsLive(true)
      connectWebSocket(selectedSource)
    }
  }

  // When selected source changes, disconnect WS
  useEffect(() => {
    if (isLive) {
      disconnectWebSocket()
      setIsLive(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedSource])

  // Cleanup WS on unmount
  useEffect(() => {
    return () => {
      if (wsRef.current) {
        wsRef.current.close()
      }
    }
  }, [])

  function handleSourceSelect(sourceId: string) {
    const source = sources.find((s) => s.id === sourceId)
    if (source && !source.exists) return
    setSelectedSource(sourceId)
    setViewMode(hasParsedView(sourceId) ? 'parsed' : 'raw')
  }

  function handleRefresh() {
    if (selectedSource) {
      loadLog(selectedSource, lineCount)
    }
  }

  function handleClear() {
    setLogLines([])
    setTotalLines(0)
  }

  async function handleAddSource() {
    const name = newSourceName.trim()
    const path = newSourcePath.trim()
    if (!name || !path) return
    if (!path.startsWith('/')) {
      toast.error(t('logs.pathInvalid'))
      return
    }
    setAddingSource(true)
    try {
      await api.addCustomLogSource(name, path)
      toast.success(t('logs.sourceAdded'))
      setAddDialogOpen(false)
      setNewSourceName('')
      setNewSourcePath('')
      loadSources()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('logs.addSourceFailed')
      toast.error(message)
    } finally {
      setAddingSource(false)
    }
  }

  function handleDeleteSourceClick(source: LogSource) {
    if (!source.custom || !source.custom_id) return
    setDeleteSourceTarget(source)
  }

  async function handleConfirmDeleteSource() {
    if (!deleteSourceTarget?.custom_id) return
    try {
      await api.deleteCustomLogSource(deleteSourceTarget.custom_id)
      toast.success(t('logs.sourceDeleted'))
      if (selectedSource === deleteSourceTarget.id) {
        setSelectedSource(null)
        setLogLines([])
      }
      setDeleteSourceTarget(null)
      loadSources()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('logs.deleteSourceFailed')
      toast.error(message)
    }
  }

  // Search: find matching line indices
  const searchLower = searchQuery.toLowerCase()
  const matchingLines = searchQuery
    ? logLines.reduce<number[]>((acc, line, i) => {
        if (line.toLowerCase().includes(searchLower)) acc.push(i)
        return acc
      }, [])
    : []

  // Scroll to a matched line
  const scrollToLine = useCallback((lineIndex: number) => {
    const container = logContainerRef.current
    if (!container) return
    const row = container.querySelector(`[data-line="${lineIndex}"]`)
    if (row) {
      row.scrollIntoView({ block: 'center', behavior: 'smooth' })
    }
  }, [])

  // Navigate matches
  const goToMatch = useCallback((direction: 'next' | 'prev') => {
    if (matchingLines.length === 0) return
    let next: number
    if (direction === 'next') {
      next = currentMatch + 1 >= matchingLines.length ? 0 : currentMatch + 1
    } else {
      next = currentMatch - 1 < 0 ? matchingLines.length - 1 : currentMatch - 1
    }
    setCurrentMatch(next)
    scrollToLine(matchingLines[next])
  }, [matchingLines, currentMatch, scrollToLine])

  // When search query changes, reset to first match and scroll
  useEffect(() => {
    if (matchingLines.length > 0) {
      setCurrentMatch(0)
      scrollToLine(matchingLines[0])
    } else {
      setCurrentMatch(0)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchQuery])

  // Keyboard shortcut: Ctrl+F to open search
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

  const selectedSourceData = sources.find((s) => s.id === selectedSource)

  // Parsed log entries (memoized)
  const parsedEntries = useMemo<LogEntry[]>(() => {
    if (!selectedSource || viewMode !== 'parsed' || !hasParsedView(selectedSource)) return []
    return parseLogLines(selectedSource, logLines)
  }, [selectedSource, viewMode, logLines])

  const activeParser = selectedSource ? getParser(selectedSource) : null

  // Download logs
  const handleDownload = useCallback(() => {
    if (logLines.length === 0) return
    const content = logLines.join('\n')
    const blob = new Blob([content], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${selectedSourceData?.name || 'log'}-${new Date().toISOString().slice(0, 19).replace(/:/g, '-')}.log`
    a.click()
    URL.revokeObjectURL(url)
  }, [logLines, selectedSourceData])

  return (
    <div className="space-y-6">
      {/* Page header */}
      <div>
        <h1 className="text-[22px] font-bold tracking-tight">{t('logs.title')}</h1>
        <p className="text-[13px] text-muted-foreground mt-1">{t('logs.subtitle')}</p>
      </div>

      <div className="flex flex-col lg:flex-row gap-6">
        {/* Left sidebar: log sources */}
        <div className="w-full lg:w-72 shrink-0 space-y-2">
          <div className="flex items-center justify-between mb-3">
            <h2 className="text-sm font-semibold text-muted-foreground uppercase tracking-wider">
              {t('logs.sources')}
            </h2>
            <Button
              variant="ghost"
              size="icon-xs"
              onClick={() => setAddDialogOpen(true)}
              title={t('logs.addSource')}
              className="h-6 w-6"
            >
              <Plus className="h-3.5 w-3.5" />
            </Button>
          </div>
          {sourcesLoading ? (
            <div className="space-y-2">
              {[1, 2, 3].map((i) => (
                <div key={i} className="h-20 bg-secondary animate-pulse rounded-xl" />
              ))}
            </div>
          ) : sources.length === 0 ? (
            <div className="bg-card rounded-2xl card-shadow py-6 text-center text-[13px] text-muted-foreground">
              {t('logs.noSources')}
            </div>
          ) : (
            sources.map((source) => (
              <div key={source.id} className="relative group">
                <button
                  onClick={() => handleSourceSelect(source.id)}
                  disabled={!source.exists}
                  className={`w-full text-left rounded-xl p-3 transition-all duration-200 ${
                    selectedSource === source.id
                      ? 'bg-primary/10 ring-1 ring-primary/20'
                      : source.exists
                        ? 'bg-card card-shadow hover:card-shadow-hover'
                        : 'bg-secondary/50 opacity-50 cursor-not-allowed'
                  }`}
                >
                  <div className="flex items-start gap-2">
                    <FileText className={`h-4 w-4 mt-0.5 shrink-0 ${
                      selectedSource === source.id
                        ? 'text-primary'
                        : source.exists
                          ? 'text-muted-foreground'
                          : 'text-muted-foreground/50'
                    }`} />
                    <div className="min-w-0 flex-1">
                      <p className="text-[13px] font-medium truncate">{source.name}</p>
                      <p className="text-[11px] text-muted-foreground truncate mt-0.5" title={source.path}>
                        {source.path}
                      </p>
                      <div className="flex items-center gap-2 mt-1.5">
                        {source.exists ? (
                          <span className="inline-flex items-center px-1.5 py-0 rounded-full text-[10px] font-medium bg-secondary text-muted-foreground">
                            {formatBytes(source.size)}
                          </span>
                        ) : (
                          <span className="inline-flex items-center px-1.5 py-0 rounded-full text-[10px] font-medium bg-secondary/50 text-muted-foreground">
                            {t('logs.notFound')}
                          </span>
                        )}
                        {source.custom && (
                          <span className="inline-flex items-center px-1.5 py-0 rounded-full text-[10px] font-medium bg-[#3182f6]/10 text-[#3182f6]">
                            {t('logs.customSource')}
                          </span>
                        )}
                      </div>
                    </div>
                  </div>
                </button>
                {source.custom && source.custom_id && (
                  <button
                    onClick={(e) => { e.stopPropagation(); handleDeleteSourceClick(source) }}
                    className="absolute top-2 right-2 h-5 w-5 rounded-md flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity bg-destructive/10 hover:bg-destructive/20 text-destructive"
                    title={t('logs.deleteSource')}
                  >
                    <X className="h-3 w-3" />
                  </button>
                )}
              </div>
            ))
          )}
        </div>

        {/* Main log content area */}
        <div className="flex-1 min-w-0 flex flex-col">
          {/* Toolbar */}
          <div className="flex flex-wrap items-center gap-2 mb-3">
            {/* Line count selector */}
            <div className="flex items-center gap-1 mr-2">
              <span className="text-xs text-muted-foreground mr-1">{t('logs.lines')}:</span>
              {LINE_COUNT_OPTIONS.map((count) => (
                <Button
                  key={count}
                  variant={lineCount === count ? 'default' : 'outline'}
                  size="xs"
                  onClick={() => setLineCount(count)}
                >
                  {count.toLocaleString()}
                </Button>
              ))}
            </div>

            {/* Raw / Parsed toggle */}
            {hasParsedView(selectedSource) && (
              <div className="flex items-center bg-secondary/50 rounded-xl p-0.5">
                <button
                  onClick={() => setViewMode('raw')}
                  className={`px-3 py-1 rounded-lg text-xs font-medium transition-all ${
                    viewMode === 'raw'
                      ? 'bg-background text-foreground shadow-sm'
                      : 'text-muted-foreground hover:text-foreground'
                  }`}
                >
                  {t('logs.viewRaw')}
                </button>
                <button
                  onClick={() => setViewMode('parsed')}
                  className={`px-3 py-1 rounded-lg text-xs font-medium transition-all ${
                    viewMode === 'parsed'
                      ? 'bg-background text-foreground shadow-sm'
                      : 'text-muted-foreground hover:text-foreground'
                  }`}
                >
                  {t('logs.viewParsed')}
                </button>
              </div>
            )}

            <div className="flex-1" />

            {/* Live toggle */}
            <Button
              variant={isLive ? 'default' : 'outline'}
              size="sm"
              onClick={handleToggleLive}
              disabled={!selectedSource}
              className={isLive ? 'bg-red-600 hover:bg-red-700 text-white' : ''}
            >
              <Radio className={`h-3.5 w-3.5 ${isLive ? 'animate-pulse' : ''}`} />
              {t('logs.live')}
              {isLive && wsConnected && (
                <span className="ml-1 h-1.5 w-1.5 rounded-full bg-white animate-pulse" />
              )}
            </Button>

            {/* Auto-scroll toggle */}
            <Button
              variant={autoScroll ? 'default' : 'outline'}
              size="sm"
              onClick={() => setAutoScroll(!autoScroll)}
              title={t('logs.autoScroll')}
            >
              <ArrowDown className="h-3.5 w-3.5" />
              {t('logs.autoScroll')}
            </Button>

            {/* Refresh */}
            <Button
              variant="outline"
              size="icon-sm"
              onClick={handleRefresh}
              disabled={!selectedSource || logLoading}
              title={t('logs.refresh')}
            >
              <RefreshCw className={`h-3.5 w-3.5 ${logLoading ? 'animate-spin' : ''}`} />
            </Button>

            {/* Search toggle */}
            <Button
              variant={searchOpen ? 'default' : 'outline'}
              size="icon-sm"
              onClick={() => {
                setSearchOpen(!searchOpen)
                if (!searchOpen) setTimeout(() => searchInputRef.current?.focus(), 0)
                if (searchOpen) setSearchQuery('')
              }}
              title={t('logs.search')}
            >
              <Search className="h-3.5 w-3.5" />
            </Button>

            {/* Download */}
            <Button
              variant="outline"
              size="icon-sm"
              onClick={handleDownload}
              disabled={logLines.length === 0}
              title={t('logs.download')}
            >
              <Download className="h-3.5 w-3.5" />
            </Button>

            {/* Clear */}
            <Button
              variant="outline"
              size="icon-sm"
              onClick={handleClear}
              title={t('logs.clear')}
            >
              <Trash2 className="h-3.5 w-3.5" />
            </Button>
          </div>

          {/* Search bar */}
          {searchOpen && (
            <div className="flex items-center gap-2 mb-3 px-3 py-2 bg-secondary/50 border-0 rounded-xl">
              <Search className="h-4 w-4 text-muted-foreground shrink-0" />
              <Input
                ref={searchInputRef}
                value={searchQuery}
                onChange={(e) => setSearchQuery(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault()
                    goToMatch(e.shiftKey ? 'prev' : 'next')
                  }
                  if (e.key === 'Escape') {
                    setSearchOpen(false)
                    setSearchQuery('')
                  }
                }}
                placeholder={t('logs.searchPlaceholder')}
                className="h-7 text-sm flex-1"
                autoFocus
              />
              {searchQuery && (
                <span className="text-xs text-muted-foreground whitespace-nowrap">
                  {matchingLines.length > 0
                    ? `${currentMatch + 1} / ${matchingLines.length}`
                    : t('logs.noMatches')}
                </span>
              )}
              <Button
                variant="ghost"
                size="icon-xs"
                onClick={() => goToMatch('prev')}
                disabled={matchingLines.length === 0}
                title={t('logs.prevMatch')}
              >
                <ChevronUp className="h-4 w-4" />
              </Button>
              <Button
                variant="ghost"
                size="icon-xs"
                onClick={() => goToMatch('next')}
                disabled={matchingLines.length === 0}
                title={t('logs.nextMatch')}
              >
                <ChevronDown className="h-4 w-4" />
              </Button>
              <Button
                variant="ghost"
                size="icon-xs"
                onClick={() => { setSearchOpen(false); setSearchQuery('') }}
              >
                <X className="h-4 w-4" />
              </Button>
            </div>
          )}

          {/* Log info bar */}
          <div className="flex items-center justify-between px-3 py-1.5 bg-secondary/50 border border-b-0 rounded-t-xl text-xs text-muted-foreground">
            <div className="flex items-center gap-3">
              {selectedSourceData ? (
                <>
                  <span className="flex items-center gap-1.5">
                    <Eye className="h-3 w-3" />
                    {selectedSourceData.name}
                  </span>
                  <span>{selectedSourceData.path}</span>
                </>
              ) : (
                <span>{t('logs.selectSource')}</span>
              )}
            </div>
            <div className="flex items-center gap-3">
              {totalLines > 0 && (
                <span>{t('logs.totalLines', { count: totalLines })}</span>
              )}
              <span>{logLines.length.toLocaleString()} {t('logs.linesShown')}</span>
              {isLive && (
                <span className="flex items-center gap-1">
                  <span className={`h-1.5 w-1.5 rounded-full ${wsConnected ? 'bg-green-500' : 'bg-red-500'}`} />
                  {wsConnected ? t('logs.connected') : t('logs.disconnected')}
                </span>
              )}
            </div>
          </div>

          {/* Log content */}
          <div
            ref={logContainerRef}
            className="flex-1 min-h-[500px] max-h-[calc(100vh-320px)] overflow-auto rounded-b-xl border font-mono text-sm"
            style={{ backgroundColor: '#1e1e1e' }}
          >
            {!selectedSource ? (
              <div className="flex items-center justify-center h-full min-h-[500px] text-gray-500">
                <div className="text-center space-y-2">
                  <FileText className="h-12 w-12 mx-auto text-gray-600" />
                  <p>{t('logs.selectSourcePrompt')}</p>
                </div>
              </div>
            ) : logLoading && logLines.length === 0 ? (
              <div className="flex items-center justify-center h-full min-h-[500px] text-gray-500">
                <div className="text-center space-y-2">
                  <RefreshCw className="h-8 w-8 mx-auto text-gray-600 animate-spin" />
                  <p>{t('logs.loading')}</p>
                </div>
              </div>
            ) : logLines.length === 0 ? (
              <div className="flex items-center justify-center h-full min-h-[500px] text-gray-500">
                <div className="text-center space-y-2">
                  <FileText className="h-12 w-12 mx-auto text-gray-600" />
                  <p>{t('logs.empty')}</p>
                </div>
              </div>
            ) : viewMode === 'parsed' && activeParser && parsedEntries.length > 0 ? (
              <table className="border-collapse" style={{ tableLayout: 'fixed' }}>
                <colgroup>
                  <col style={{ width: '3.5rem' }} />
                  {(activeParser.columns as ColumnDef<ParsedLogEntry>[]).map((col) => (
                    <col key={col.key} style={{ width: col.width }} />
                  ))}
                </colgroup>
                <thead className="sticky top-0 z-10" style={{ backgroundColor: '#2d2d2d' }}>
                  <tr>
                    <th
                      className="select-none text-right px-3 py-1.5 text-gray-500 border-r border-gray-700/50 border-b border-b-gray-700/50 whitespace-nowrap"
                      style={{ fontSize: '11px' }}
                    >
                      #
                    </th>
                    {(activeParser.columns as ColumnDef<ParsedLogEntry>[]).map((col) => (
                      <th
                        key={col.key}
                        className="text-left px-3 py-1.5 text-gray-400 border-b border-b-gray-700/50 whitespace-nowrap text-[11px] font-semibold uppercase tracking-wider"
                      >
                        {t(col.i18nKey)}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {parsedEntries.map((entry, index) => {
                    const isMatch = searchQuery && matchingLines.includes(index)
                    const isCurrentMatch = isMatch && matchingLines[currentMatch] === index

                    if (!entry.parsed) {
                      // Fallback: show raw line spanning all columns
                      return (
                        <tr
                          key={index}
                          data-line={index}
                          className={`hover:bg-white/5 ${isCurrentMatch ? 'bg-yellow-500/20' : isMatch ? 'bg-yellow-500/10' : ''}`}
                        >
                          <td
                            className="select-none text-right px-3 py-0 text-gray-600 border-r border-gray-700/50 align-top whitespace-nowrap"
                            style={{ minWidth: '3.5rem', fontSize: '12px', lineHeight: '20px' }}
                          >
                            {index + 1}
                          </td>
                          <td
                            colSpan={activeParser.columns.length}
                            className="px-3 py-0 whitespace-pre-wrap break-all text-gray-400"
                            style={{ fontSize: '12px', lineHeight: '20px' }}
                          >
                            {searchQuery && isMatch ? highlightText(entry.rawLine, searchQuery) : entry.rawLine}
                          </td>
                        </tr>
                      )
                    }

                    return (
                      <tr
                        key={index}
                        data-line={index}
                        className={`hover:bg-white/5 ${isCurrentMatch ? 'bg-yellow-500/20' : isMatch ? 'bg-yellow-500/10' : ''}`}
                      >
                        <td
                          className="select-none text-right px-3 py-0 text-gray-600 border-r border-gray-700/50 align-top whitespace-nowrap"
                          style={{ minWidth: '3.5rem', fontSize: '12px', lineHeight: '20px' }}
                        >
                          {index + 1}
                        </td>
                        {(activeParser.columns as ColumnDef<ParsedLogEntry>[]).map((col) => {
                          const rendered = col.render(entry as ParsedLogEntry)
                          return (
                            <td
                              key={col.key}
                              className={`px-3 py-0 text-left text-gray-200 ${col.key === 'details' ? 'truncate' : 'whitespace-nowrap overflow-hidden'}`}
                              style={{ fontSize: '12px', lineHeight: '20px' }}
                              title={col.key === 'details' ? rendered.text : undefined}
                            >
                              {rendered.pill && rendered.color ? (
                                <span
                                  className="inline-flex items-center px-1.5 py-0 rounded-full text-[10px] font-medium"
                                  style={{
                                    backgroundColor: `${rendered.color}20`,
                                    color: rendered.color,
                                  }}
                                >
                                  {rendered.text}
                                </span>
                              ) : col.key === 'details' ? (
                                <span className="text-gray-300">
                                  {searchQuery && isMatch ? highlightText(rendered.text, searchQuery) : rendered.text}
                                </span>
                              ) : (
                                <span>{rendered.text}</span>
                              )}
                            </td>
                          )
                        })}
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            ) : (
              <table className="w-full border-collapse">
                <tbody>
                  {logLines.map((line, index) => {
                    const isMatch = searchQuery && matchingLines.includes(index)
                    const isCurrentMatch = isMatch && matchingLines[currentMatch] === index
                    const level = getLogLevel(line)
                    const levelBorder = level ? LOG_LEVEL_COLORS[level] : ''
                    const levelText = level ? LOG_LEVEL_TEXT_COLORS[level] : 'text-gray-200'
                    return (
                      <tr
                        key={index}
                        data-line={index}
                        className={`hover:bg-white/5 group ${isCurrentMatch ? 'bg-yellow-500/20' : isMatch ? 'bg-yellow-500/10' : ''} ${levelBorder}`}
                      >
                        <td
                          className="select-none text-right px-3 py-0 text-gray-600 border-r border-gray-700/50 align-top whitespace-nowrap"
                          style={{ minWidth: '3.5rem', fontSize: '12px', lineHeight: '20px' }}
                        >
                          {index + 1}
                        </td>
                        <td
                          className={`px-3 py-0 whitespace-pre-wrap break-all ${levelText}`}
                          style={{ fontSize: '12px', lineHeight: '20px' }}
                        >
                          {searchQuery && isMatch ? highlightText(line, searchQuery) : line}
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            )}
          </div>
        </div>
      </div>

      {/* Add Custom Source Dialog */}
      <Dialog open={addDialogOpen} onOpenChange={setAddDialogOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t('logs.addSourceTitle')}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 pt-2">
            <div className="space-y-2">
              <label className="text-[13px] font-medium">{t('logs.sourceName')}</label>
              <Input
                value={newSourceName}
                onChange={(e) => setNewSourceName(e.target.value)}
                placeholder={t('logs.sourceNamePlaceholder')}
                className="h-9 rounded-xl text-[13px]"
              />
            </div>
            <div className="space-y-2">
              <label className="text-[13px] font-medium">{t('logs.sourcePath')}</label>
              <Input
                value={newSourcePath}
                onChange={(e) => setNewSourcePath(e.target.value)}
                placeholder={t('logs.sourcePathPlaceholder')}
                className="h-9 rounded-xl text-[13px] font-mono"
              />
            </div>
            <div className="flex justify-end gap-2 pt-2">
              <Button variant="outline" onClick={() => setAddDialogOpen(false)} className="rounded-xl">
                {t('common.cancel')}
              </Button>
              <Button
                onClick={handleAddSource}
                disabled={addingSource || !newSourceName.trim() || !newSourcePath.trim()}
                className="rounded-xl"
              >
                {addingSource ? t('common.saving') : t('logs.addSource')}
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>

      {/* Delete Custom Source Confirmation Dialog */}
      <Dialog open={!!deleteSourceTarget} onOpenChange={(open) => !open && setDeleteSourceTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('logs.deleteSource')}</DialogTitle>
            <DialogDescription>{t('logs.deleteSourceConfirm')}</DialogDescription>
          </DialogHeader>
          {deleteSourceTarget && (
            <div className="rounded-xl bg-secondary/50 px-3 py-2 text-[13px]">
              <span className="font-medium">{deleteSourceTarget.name}</span>
              <span className="text-muted-foreground ml-2">{deleteSourceTarget.path}</span>
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteSourceTarget(null)} className="rounded-xl">
              {t('common.cancel')}
            </Button>
            <Button variant="destructive" onClick={handleConfirmDeleteSource} className="rounded-xl">
              {t('common.delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
