import { useEffect, useState, useRef, useCallback, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { FileText, RefreshCw, Radio, ArrowDown, Trash2, Search, ChevronUp, ChevronDown, X, Download } from 'lucide-react'
import { hasParsedView, getParser, parseLogLines, type LogEntry, type ParsedLogEntry, type ColumnDef } from '@/lib/logParsers'

type FirewallLogSource = 'firewall' | 'fail2ban'
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
  const cleaned = line.replace(/"error":""/g, '')
  const upper = cleaned.toUpperCase()
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

interface LogResponse {
  source: string
  lines: string[]
  total_lines: number
}

export default function FirewallLogs() {
  const { t } = useTranslation()

  const [selectedSource, setSelectedSource] = useState<FirewallLogSource>('firewall')
  const [logLines, setLogLines] = useState<string[]>([])
  const [isLive, setIsLive] = useState(false)
  const [autoScroll, setAutoScroll] = useState(true)
  const [lineCount, setLineCount] = useState<LineCount>(500)
  const [logLoading, setLogLoading] = useState(false)
  const [totalLines, setTotalLines] = useState(0)
  const [viewMode, setViewMode] = useState<'raw' | 'parsed'>('parsed')

  // Search
  const [searchOpen, setSearchOpen] = useState(false)
  const [searchQuery, setSearchQuery] = useState('')
  const [currentMatch, setCurrentMatch] = useState(0)
  const searchInputRef = useRef<HTMLInputElement>(null)

  // WebSocket
  const [wsConnected, setWsConnected] = useState(false)
  const wsRef = useRef<WebSocket | null>(null)
  const logContainerRef = useRef<HTMLDivElement>(null)
  const autoScrollRef = useRef(autoScroll)

  useEffect(() => {
    autoScrollRef.current = autoScroll
  }, [autoScroll])

  const scrollToBottom = useCallback(() => {
    if (logContainerRef.current) {
      logContainerRef.current.scrollTop = logContainerRef.current.scrollHeight
    }
  }, [])

  useEffect(() => {
    if (autoScroll) {
      scrollToBottom()
    }
  }, [logLines, autoScroll, scrollToBottom])

  // Fetch logs
  useEffect(() => {
    loadLog(selectedSource, lineCount)
  }, [selectedSource, lineCount])

  async function loadLog(source: string, lines: number) {
    setLogLoading(true)
    try {
      const data: LogResponse = await api.readLog(source, lines)
      setLogLines(data.lines)
      setTotalLines(data.total_lines)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : String(err)
      toast.error(message || t('logs.loadLogFailed'))
      setLogLines([])
      setTotalLines(0)
    } finally {
      setLogLoading(false)
    }
  }

  // WebSocket
  const connectWebSocket = useCallback((source: string) => {
    if (wsRef.current) {
      wsRef.current.close()
      wsRef.current = null
    }

    const wsUrl = api.buildWsUrl('/ws/logs', { source })
    const ws = new WebSocket(wsUrl)

    ws.onopen = () => {
      setWsConnected(true)
    }

    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data)
        if (data.line !== undefined) {
          setLogLines((prev) => [...prev, data.line])
        } else if (data.lines && Array.isArray(data.lines)) {
          setLogLines((prev) => [...prev, ...data.lines])
        }
      } catch {
        if (typeof event.data === 'string' && event.data.trim()) {
          setLogLines((prev) => [...prev, event.data])
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

  function handleToggleLive() {
    if (isLive) {
      disconnectWebSocket()
      setIsLive(false)
    } else {
      setIsLive(true)
      connectWebSocket(selectedSource)
    }
  }

  // Disconnect WS on source change
  useEffect(() => {
    if (isLive) {
      disconnectWebSocket()
      setIsLive(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedSource])

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      if (wsRef.current) {
        wsRef.current.close()
      }
    }
  }, [])

  function handleSourceChange(source: FirewallLogSource) {
    setSelectedSource(source)
    setViewMode(hasParsedView(source) ? 'parsed' : 'raw')
  }

  function handleRefresh() {
    loadLog(selectedSource, lineCount)
  }

  function handleClear() {
    setLogLines([])
    setTotalLines(0)
  }

  // Search
  const searchLower = searchQuery.toLowerCase()
  const matchingLines = searchQuery
    ? logLines.reduce<number[]>((acc, line, i) => {
        if (line.toLowerCase().includes(searchLower)) acc.push(i)
        return acc
      }, [])
    : []

  const scrollToLine = useCallback((lineIndex: number) => {
    const container = logContainerRef.current
    if (!container) return
    const row = container.querySelector(`[data-line="${lineIndex}"]`)
    if (row) {
      row.scrollIntoView({ block: 'center', behavior: 'smooth' })
    }
  }, [])

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

  useEffect(() => {
    if (matchingLines.length > 0) {
      setCurrentMatch(0)
      scrollToLine(matchingLines[0])
    } else {
      setCurrentMatch(0)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchQuery])

  // Ctrl+F
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

  // Parsed entries
  const parsedEntries = useMemo<LogEntry[]>(() => {
    if (viewMode !== 'parsed' || !hasParsedView(selectedSource)) return []
    return parseLogLines(selectedSource, logLines)
  }, [selectedSource, viewMode, logLines])

  const activeParser = getParser(selectedSource)

  // Download
  const handleDownload = useCallback(() => {
    if (logLines.length === 0) return
    const content = logLines.join('\n')
    const blob = new Blob([content], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `${selectedSource}-${new Date().toISOString().slice(0, 19).replace(/:/g, '-')}.log`
    a.click()
    URL.revokeObjectURL(url)
  }, [logLines, selectedSource])

  return (
    <div className="space-y-4">
      {/* Header */}
      <div>
        <h2 className="text-[15px] font-semibold">{t('firewall.logs.title')}</h2>
        <p className="text-[11px] text-muted-foreground mt-0.5">{t('firewall.logs.description')}</p>
      </div>

      {/* Source toggle + Toolbar */}
      <div className="flex flex-wrap items-center gap-2">
        {/* Source toggle */}
        <div className="flex items-center bg-secondary/50 rounded-xl p-0.5 mr-2">
          <button
            onClick={() => handleSourceChange('firewall')}
            className={`px-3 py-1 rounded-lg text-xs font-medium transition-all ${
              selectedSource === 'firewall'
                ? 'bg-background text-foreground shadow-sm'
                : 'text-muted-foreground hover:text-foreground'
            }`}
          >
            {t('firewall.logs.sourceFirewall')}
          </button>
          <button
            onClick={() => handleSourceChange('fail2ban')}
            className={`px-3 py-1 rounded-lg text-xs font-medium transition-all ${
              selectedSource === 'fail2ban'
                ? 'bg-background text-foreground shadow-sm'
                : 'text-muted-foreground hover:text-foreground'
            }`}
          >
            {t('firewall.logs.sourceFail2ban')}
          </button>
        </div>

        {/* Line count */}
        <div className="flex items-center gap-1 mr-2">
          <span className="text-xs text-muted-foreground mr-1">{t('firewall.logs.lines')}:</span>
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
              {t('firewall.logs.rawView')}
            </button>
            <button
              onClick={() => setViewMode('parsed')}
              className={`px-3 py-1 rounded-lg text-xs font-medium transition-all ${
                viewMode === 'parsed'
                  ? 'bg-background text-foreground shadow-sm'
                  : 'text-muted-foreground hover:text-foreground'
              }`}
            >
              {t('firewall.logs.parsedView')}
            </button>
          </div>
        )}

        <div className="flex-1" />

        {/* Live toggle */}
        <Button
          variant={isLive ? 'default' : 'outline'}
          size="sm"
          onClick={handleToggleLive}
          className={isLive ? 'bg-red-600 hover:bg-red-700 text-white' : ''}
        >
          <Radio className={`h-3.5 w-3.5 ${isLive ? 'animate-pulse' : ''}`} />
          {t('firewall.logs.liveMode')}
          {isLive && wsConnected && (
            <span className="ml-1 h-1.5 w-1.5 rounded-full bg-white animate-pulse" />
          )}
        </Button>

        {/* Auto-scroll */}
        <Button
          variant={autoScroll ? 'default' : 'outline'}
          size="sm"
          onClick={() => setAutoScroll(!autoScroll)}
          title={t('logs.autoScroll')}
        >
          <ArrowDown className="h-3.5 w-3.5" />
        </Button>

        {/* Refresh */}
        <Button
          variant="outline"
          size="icon-sm"
          onClick={handleRefresh}
          disabled={logLoading}
          title={t('logs.refresh')}
        >
          <RefreshCw className={`h-3.5 w-3.5 ${logLoading ? 'animate-spin' : ''}`} />
        </Button>

        {/* Search */}
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
        <div className="flex items-center gap-2 px-3 py-2 bg-secondary/50 border-0 rounded-xl">
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
          <Button variant="ghost" size="icon-xs" onClick={() => goToMatch('prev')} disabled={matchingLines.length === 0}>
            <ChevronUp className="h-4 w-4" />
          </Button>
          <Button variant="ghost" size="icon-xs" onClick={() => goToMatch('next')} disabled={matchingLines.length === 0}>
            <ChevronDown className="h-4 w-4" />
          </Button>
          <Button variant="ghost" size="icon-xs" onClick={() => { setSearchOpen(false); setSearchQuery('') }}>
            <X className="h-4 w-4" />
          </Button>
        </div>
      )}

      {/* Log info bar */}
      <div className="flex items-center justify-between px-3 py-1.5 bg-secondary/50 border border-b-0 rounded-t-xl text-xs text-muted-foreground">
        <div className="flex items-center gap-3">
          <span className="font-medium">{selectedSource === 'firewall' ? t('firewall.logs.sourceFirewall') : 'Fail2ban'}</span>
          <span>{logLines.length.toLocaleString()} {t('firewall.logs.lines')}</span>
        </div>
        <div className="flex items-center gap-3">
          {totalLines > 0 && (
            <span>{t('logs.totalLines', { count: totalLines })}</span>
          )}
          {isLive && (
            <span className="flex items-center gap-1">
              <span className={`h-1.5 w-1.5 rounded-full ${wsConnected ? 'bg-green-500' : 'bg-red-500'}`} />
              {wsConnected ? t('firewall.logs.connected') : t('firewall.logs.connecting')}
            </span>
          )}
        </div>
      </div>

      {/* Log content */}
      <div
        ref={logContainerRef}
        className="min-h-[500px] max-h-[calc(100vh-380px)] overflow-auto rounded-b-xl border border-t-0 font-mono text-sm -mt-4"
        style={{ backgroundColor: '#1e1e1e' }}
      >
        {logLoading && logLines.length === 0 ? (
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
              <p>{t('firewall.logs.noLogs')}</p>
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
  )
}
