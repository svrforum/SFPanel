import { useEffect, useState, useRef, useCallback, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { FileText, RefreshCw, Radio, ArrowDown, Trash2, Eye, Search, ChevronUp, ChevronDown, X, Download, Plus, Info } from 'lucide-react'
import { Input } from '@/components/ui/input'
import { hasParsedView, getParser, parseLogLines, type LogEntry, type ParsedLogEntry, type ColumnDef } from '@/lib/logParsers'
import { useVirtualizer } from '@tanstack/react-virtual'

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

const ROW_HEIGHT = 20

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

// Pre-compiled regexes for log level detection (avoid re-creating per call)
const RE_ERROR = /\b(ERROR|FATAL|CRITICAL|PANIC|EMERG)\b/
const RE_WARN = /\b(WARN|WARNING)\b/
const RE_INFO = /\b(INFO|NOTICE)\b/
const RE_DEBUG = /\b(DEBUG|TRACE)\b/
const RE_EMPTY_ERROR = /"error":""/g

function getLogLevel(line: string): 'error' | 'warn' | 'info' | 'debug' | null {
  const cleaned = line.replace(RE_EMPTY_ERROR, '')
  const upper = cleaned.toUpperCase()
  if (RE_ERROR.test(upper)) return 'error'
  if (RE_WARN.test(upper)) return 'warn'
  if (RE_INFO.test(upper)) return 'info'
  if (RE_DEBUG.test(upper)) return 'debug'
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

  // Guide
  const [showGuide, setShowGuide] = useState(false)

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

  // WebSocket batching: accumulate lines and flush via rAF
  const wsBatchRef = useRef<string[]>([])
  const wsRafRef = useRef<number | null>(null)

  // Keep the ref in sync so the WS message handler can read current value
  useEffect(() => {
    autoScrollRef.current = autoScroll
  }, [autoScroll])

  // Parsed log entries (memoized)
  const parsedEntries = useMemo<LogEntry[]>(() => {
    if (!selectedSource || viewMode !== 'parsed' || !hasParsedView(selectedSource)) return []
    return parseLogLines(selectedSource, logLines)
  }, [selectedSource, viewMode, logLines])

  const activeParser = selectedSource ? getParser(selectedSource) : null

  // Determine which data the virtualizer operates on
  const isParsedMode = viewMode === 'parsed' && activeParser && parsedEntries.length > 0
  const rowCount = !selectedSource || (logLoading && logLines.length === 0) || logLines.length === 0
    ? 0
    : isParsedMode
      ? parsedEntries.length
      : logLines.length

  // Virtual scrolling
  const rowVirtualizer = useVirtualizer({
    count: rowCount,
    getScrollElement: () => logContainerRef.current,
    estimateSize: () => ROW_HEIGHT,
    overscan: 30,
  })

  // Auto-scroll when new lines arrive
  useEffect(() => {
    if (autoScroll && rowCount > 0) {
      rowVirtualizer.scrollToIndex(rowCount - 1, { align: 'end' })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [logLines, autoScroll, rowCount])

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

  // Flush batched WS lines into state in a single rAF tick
  const flushWsBatch = useCallback(() => {
    wsRafRef.current = null
    const batch = wsBatchRef.current
    if (batch.length === 0) return
    wsBatchRef.current = []
    setLogLines((prev) => {
      const next = prev.concat(batch)
      return next.length > 5000 ? next.slice(-5000) : next
    })
  }, [])

  // WebSocket lifecycle
  const connectWebSocket = useCallback((source: string) => {
    if (wsRef.current) {
      wsRef.current.close()
      wsRef.current = null
    }

    const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    const wsUrl = `${wsProtocol}//${window.location.host}/ws/logs?source=${source}&token=${api.getToken()}`
    const ws = new WebSocket(wsUrl)

    ws.onopen = () => setWsConnected(true)

    ws.onmessage = (event) => {
      // Accumulate into batch buffer
      try {
        const data = JSON.parse(event.data)
        if (data.line !== undefined) {
          wsBatchRef.current.push(data.line)
        } else if (data.lines && Array.isArray(data.lines)) {
          wsBatchRef.current.push(...data.lines)
        }
      } catch {
        if (typeof event.data === 'string' && event.data.trim()) {
          wsBatchRef.current.push(event.data)
        }
      }
      // Schedule a single flush per animation frame
      if (wsRafRef.current === null) {
        wsRafRef.current = requestAnimationFrame(flushWsBatch)
      }
    }

    ws.onerror = () => {
      setWsConnected(false)
      toast.error(t('logs.wsError'))
    }

    ws.onclose = () => setWsConnected(false)

    wsRef.current = ws
  }, [t, flushWsBatch])

  const disconnectWebSocket = useCallback(() => {
    if (wsRef.current) {
      wsRef.current.close()
      wsRef.current = null
    }
    if (wsRafRef.current !== null) {
      cancelAnimationFrame(wsRafRef.current)
      wsRafRef.current = null
    }
    wsBatchRef.current = []
    setWsConnected(false)
  }, [])

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

  useEffect(() => {
    if (isLive) {
      disconnectWebSocket()
      setIsLive(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedSource])

  useEffect(() => {
    return () => {
      if (wsRef.current) wsRef.current.close()
      if (wsRafRef.current !== null) cancelAnimationFrame(wsRafRef.current)
    }
  }, [])

  function handleSourceSelect(sourceId: string) {
    const source = sources.find((s) => s.id === sourceId)
    if (source && !source.exists) return
    setSelectedSource(sourceId)
    setViewMode(hasParsedView(sourceId) ? 'parsed' : 'raw')
  }

  function handleRefresh() {
    if (selectedSource) loadLog(selectedSource, lineCount)
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

  // Memoized search: matching line indices + Set for O(1) lookup
  const matchingLines = useMemo(() => {
    if (!searchQuery) return []
    const q = searchQuery.toLowerCase()
    return logLines.reduce<number[]>((acc, line, i) => {
      if (line.toLowerCase().includes(q)) acc.push(i)
      return acc
    }, [])
  }, [searchQuery, logLines])

  const matchingSet = useMemo(() => new Set(matchingLines), [matchingLines])

  // Memoized log levels for raw view (avoid per-row regex on each render)
  const logLevels = useMemo(() => {
    if (isParsedMode) return []
    return logLines.map(getLogLevel)
  }, [logLines, isParsedMode])

  // Navigate to a virtual row by index
  const scrollToLine = useCallback((lineIndex: number) => {
    rowVirtualizer.scrollToIndex(lineIndex, { align: 'center' })
  }, [rowVirtualizer])

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

  // Virtual items
  const virtualItems = rowVirtualizer.getVirtualItems()
  const totalSize = rowVirtualizer.getTotalSize()

  return (
    <div className="space-y-6">
      {/* Page header */}
      <div>
        <h1 className="text-[22px] font-bold tracking-tight">{t('logs.title')}</h1>
        <p className="text-[13px] text-muted-foreground mt-1">{t('logs.subtitle')}</p>
      </div>

      {/* How it works */}
      <div className="bg-card rounded-2xl card-shadow overflow-hidden">
        <button
          onClick={() => setShowGuide(!showGuide)}
          className="w-full flex items-center gap-2.5 px-4 py-3 text-left hover:bg-secondary/30 transition-colors"
        >
          <Info className="h-4 w-4 text-primary shrink-0" />
          <span className="text-[13px] font-medium flex-1">{t('logs.guideTitle')}</span>
          {showGuide ? (
            <ChevronUp className="h-4 w-4 text-muted-foreground" />
          ) : (
            <ChevronDown className="h-4 w-4 text-muted-foreground" />
          )}
        </button>
        {showGuide && (
          <div className="px-4 pb-4 space-y-3 animate-in slide-in-from-top-1 duration-200">
            <div className="h-px bg-border" />
            <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
              {[
                { num: '1', title: t('logs.guideStep1Title'), desc: t('logs.guideStep1Desc') },
                { num: '2', title: t('logs.guideStep2Title'), desc: t('logs.guideStep2Desc') },
                { num: '3', title: t('logs.guideStep3Title'), desc: t('logs.guideStep3Desc') },
              ].map((step) => (
                <div key={step.num} className="flex gap-3">
                  <span className="inline-flex items-center justify-center h-5 w-5 rounded-full bg-primary/10 text-primary text-[11px] font-bold shrink-0 mt-0.5">
                    {step.num}
                  </span>
                  <div>
                    <p className="text-[12px] font-semibold">{step.title}</p>
                    <p className="text-[11px] text-muted-foreground mt-0.5 leading-relaxed">{step.desc}</p>
                  </div>
                </div>
              ))}
            </div>
            <div className="flex flex-wrap gap-x-4 gap-y-1 pt-1">
              <span className="text-[11px] text-muted-foreground">
                <span className="font-medium text-foreground">{t('logs.guideStreaming')}</span> WebSocket (tail -F)
              </span>
              <span className="text-[11px] text-muted-foreground">
                <span className="font-medium text-foreground">{t('logs.guideSearch')}</span> Ctrl+F
              </span>
              <span className="text-[11px] text-muted-foreground">
                <span className="font-medium text-foreground">{t('logs.guideParsed')}</span> Firewall, Auth, Fail2ban, SFPanel
              </span>
            </div>
          </div>
        )}
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
                  className="rounded-xl"
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
              className={`rounded-xl ${isLive ? 'bg-red-600 hover:bg-red-700 text-white' : ''}`}
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
              className="rounded-xl"
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
              className="rounded-xl"
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
              className="rounded-xl"
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
              className="rounded-xl"
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
              className="rounded-xl"
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
              {totalLines > 0 && totalLines !== logLines.length && (
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

          {/* Log content — virtualized */}
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
            ) : isParsedMode ? (
              <table className="border-collapse" style={{ tableLayout: 'fixed' }}>
                <colgroup>
                  <col style={{ width: '3.5rem' }} />
                  {(activeParser!.columns as ColumnDef<ParsedLogEntry>[]).map((col) => (
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
                    {(activeParser!.columns as ColumnDef<ParsedLogEntry>[]).map((col) => (
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
                  {/* Top spacer for virtual scroll */}
                  {virtualItems.length > 0 && virtualItems[0].start > 0 && (
                    <tr><td colSpan={activeParser!.columns.length + 1} style={{ height: virtualItems[0].start, padding: 0, border: 0 }} /></tr>
                  )}
                  {virtualItems.map((virtualRow) => {
                    const index = virtualRow.index
                    const entry = parsedEntries[index]
                    const isMatch = matchingSet.has(index)
                    const isCurrentMatch = isMatch && matchingLines[currentMatch] === index

                    if (!entry.parsed) {
                      return (
                        <tr
                          key={virtualRow.key}
                          data-line={index}
                          style={{ height: ROW_HEIGHT }}
                          className={`hover:bg-white/5 ${isCurrentMatch ? 'bg-yellow-500/20' : isMatch ? 'bg-yellow-500/10' : ''}`}
                        >
                          <td
                            className="select-none text-right px-3 py-0 text-gray-600 border-r border-gray-700/50 align-top whitespace-nowrap"
                            style={{ minWidth: '3.5rem', fontSize: '12px', lineHeight: '20px' }}
                          >
                            {index + 1}
                          </td>
                          <td
                            colSpan={activeParser!.columns.length}
                            className="px-3 py-0 whitespace-nowrap overflow-hidden text-ellipsis text-gray-400"
                            style={{ fontSize: '12px', lineHeight: '20px' }}
                            title={entry.rawLine}
                          >
                            {searchQuery && isMatch ? highlightText(entry.rawLine, searchQuery) : entry.rawLine}
                          </td>
                        </tr>
                      )
                    }

                    return (
                      <tr
                        key={virtualRow.key}
                        data-line={index}
                        style={{ height: ROW_HEIGHT }}
                        className={`hover:bg-white/5 ${isCurrentMatch ? 'bg-yellow-500/20' : isMatch ? 'bg-yellow-500/10' : ''}`}
                      >
                        <td
                          className="select-none text-right px-3 py-0 text-gray-600 border-r border-gray-700/50 align-top whitespace-nowrap"
                          style={{ minWidth: '3.5rem', fontSize: '12px', lineHeight: '20px' }}
                        >
                          {index + 1}
                        </td>
                        {(activeParser!.columns as ColumnDef<ParsedLogEntry>[]).map((col) => {
                          const rendered = col.render(entry as ParsedLogEntry)
                          return (
                            <td
                              key={col.key}
                              className={`px-3 py-0 text-left text-gray-200 whitespace-nowrap overflow-hidden ${col.key === 'details' ? 'text-ellipsis' : ''}`}
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
                  {/* Bottom spacer for virtual scroll */}
                  {virtualItems.length > 0 && (
                    <tr><td colSpan={activeParser!.columns.length + 1} style={{ height: totalSize - (virtualItems[virtualItems.length - 1]?.end ?? 0), padding: 0, border: 0 }} /></tr>
                  )}
                </tbody>
              </table>
            ) : (
              /* Raw view — virtualized */
              <div style={{ height: totalSize, position: 'relative' }}>
                <table className="w-full border-collapse" style={{ position: 'absolute', top: 0, left: 0, right: 0 }}>
                  <tbody>
                    {virtualItems.map((virtualRow) => {
                      const index = virtualRow.index
                      const line = logLines[index]
                      const isMatch = matchingSet.has(index)
                      const isCurrentMatch = isMatch && matchingLines[currentMatch] === index
                      const level = logLevels[index]
                      const levelBorder = level ? LOG_LEVEL_COLORS[level] : ''
                      const levelText = level ? LOG_LEVEL_TEXT_COLORS[level] : 'text-gray-200'
                      return (
                        <tr
                          key={virtualRow.key}
                          data-line={index}
                          style={{
                            height: ROW_HEIGHT,
                            position: 'absolute',
                            top: virtualRow.start,
                            left: 0,
                            right: 0,
                            display: 'flex',
                          }}
                          className={`hover:bg-white/5 ${isCurrentMatch ? 'bg-yellow-500/20' : isMatch ? 'bg-yellow-500/10' : ''} ${levelBorder}`}
                        >
                          <td
                            className="select-none text-right px-3 py-0 text-gray-600 border-r border-gray-700/50 whitespace-nowrap shrink-0"
                            style={{ minWidth: '3.5rem', width: '3.5rem', fontSize: '12px', lineHeight: '20px' }}
                          >
                            {index + 1}
                          </td>
                          <td
                            className={`px-3 py-0 whitespace-nowrap overflow-hidden text-ellipsis flex-1 ${levelText}`}
                            style={{ fontSize: '12px', lineHeight: '20px' }}
                            title={line}
                          >
                            {searchQuery && isMatch ? highlightText(line, searchQuery) : line}
                          </td>
                        </tr>
                      )
                    })}
                  </tbody>
                </table>
              </div>
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
