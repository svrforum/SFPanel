import { useEffect, useState, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { downloadBlob } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { RefreshCw, Radio, ArrowDown, Trash2, Search, Download } from 'lucide-react'
import { hasParsedView } from '@/lib/logParsers'
import { ListLoadState } from '@/components/ListLoadState'
import { LiveLogSocket } from '@/components/logviewer/LiveLogSocket'
import { LogSearchBar } from '@/components/logviewer/LogSearchBar'
import { LogTable } from '@/components/logviewer/LogTable'
import { useLogViewer } from '@/components/logviewer/useLogViewer'
import { appendLogLines, LINE_COUNT_OPTIONS, type LineCount } from '@/components/logviewer/logViewUtils'

type FirewallLogSource = 'firewall' | 'fail2ban'

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
  // A failed read: the empty viewer would read as "no logs".
  const [logError, setLogError] = useState<string | null>(null)
  const [viewMode, setViewMode] = useState<'raw' | 'parsed'>('parsed')

  // Live WebSocket state (connection itself lives in <LiveLogSocket />)
  const [wsConnected, setWsConnected] = useState(false)

  // Shared viewer machinery: parsing, virtual scroll, search, Ctrl+F
  const viewer = useLogViewer({ sourceId: selectedSource, lines: logLines, viewMode, autoScroll })

  // Fetch logs
  useEffect(() => {
    loadLog(selectedSource, lineCount)
  }, [selectedSource, lineCount])

  async function loadLog(source: string, lines: number) {
    setLogLoading(true)
    setLogError(null)
    try {
      const data: LogResponse = await api.readLog(source, lines)
      setLogLines(data.lines)
      setTotalLines(data.total_lines)
    } catch (err: unknown) {
      setLogError(err instanceof Error ? err.message : String(err))
      setLogLines([])
      setTotalLines(0)
    } finally {
      setLogLoading(false)
    }
  }

  // Append live-tail batches (capped by the shared slack window)
  const handleLiveLines = useCallback((batch: string[]) => {
    setLogLines((prev) => appendLogLines(prev, batch))
  }, [])

  function handleToggleLive() {
    if (isLive) {
      setIsLive(false)
      setWsConnected(false)
    } else {
      setIsLive(true)
    }
  }

  // Stop the live tail when switching sources
  useEffect(() => {
    setIsLive(false)
    setWsConnected(false)
  }, [selectedSource])

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

  // Download
  const sourceName = selectedSource === 'firewall' ? t('firewall.logs.sourceFirewall') : t('firewall.logs.sourceFail2ban')

  const handleDownload = useCallback(() => {
    if (logLines.length === 0) return
    const blob = new Blob([logLines.join('\n')], { type: 'text/plain' })
    downloadBlob(blob, `${selectedSource}-${new Date().toISOString().slice(0, 19).replace(/:/g, '-')}.log`)
  }, [logLines, selectedSource])

  return (
    <div className="space-y-4">
      {/* Header */}
      <div>
        <h2 className="text-[15px] font-semibold">{t('firewall.logs.title')}</h2>
        <p className="text-[12px] text-muted-foreground mt-0.5">{t('firewall.logs.description')}</p>
      </div>

      {/* Live tail connection (mounted only while live mode is on) */}
      {isLive && (
        <LiveLogSocket
          source={selectedSource}
          onLines={handleLiveLines}
          onConnectedChange={setWsConnected}
        />
      )}

      {/* Source toggle + Toolbar */}
      <div className="flex flex-wrap items-center gap-2">
        {/* Source toggle */}
        <div role="group" aria-label={t('logs.sources')} className="flex items-center bg-secondary/50 rounded-xl p-0.5 mr-2">
          <button
            type="button"
            aria-pressed={selectedSource === 'firewall'}
            onClick={() => handleSourceChange('firewall')}
            className={`px-3 py-1 rounded-lg text-xs font-medium transition outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-0 ${
              selectedSource === 'firewall'
                ? 'bg-background text-foreground shadow-sm'
                : 'text-muted-foreground hover:text-foreground'
            }`}
          >
            {t('firewall.logs.sourceFirewall')}
          </button>
          <button
            type="button"
            aria-pressed={selectedSource === 'fail2ban'}
            onClick={() => handleSourceChange('fail2ban')}
            className={`px-3 py-1 rounded-lg text-xs font-medium transition outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-0 ${
              selectedSource === 'fail2ban'
                ? 'bg-background text-foreground shadow-sm'
                : 'text-muted-foreground hover:text-foreground'
            }`}
          >
            {t('firewall.logs.sourceFail2ban')}
          </button>
        </div>

        {/* Line count */}
        <div role="group" aria-labelledby="firewall-log-lines-label" className="flex items-center gap-1 mr-2">
          <span id="firewall-log-lines-label" className="text-xs text-muted-foreground mr-1">{t('logs.lines')}:</span>
          {LINE_COUNT_OPTIONS.map((count) => (
            <Button
              key={count}
              variant={lineCount === count ? 'default' : 'outline'}
              size="xs"
              aria-pressed={lineCount === count}
              onClick={() => setLineCount(count)}
            >
              {count.toLocaleString()}
            </Button>
          ))}
        </div>

        {/* Raw / Parsed toggle */}
        {hasParsedView(selectedSource) && (
          <div role="group" aria-label={t('logs.viewMode')} className="flex items-center bg-secondary/50 rounded-xl p-0.5">
            <button
              type="button"
              aria-pressed={viewMode === 'raw'}
              onClick={() => setViewMode('raw')}
              className={`px-3 py-1 rounded-lg text-xs font-medium transition outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-0 ${
                viewMode === 'raw'
                  ? 'bg-background text-foreground shadow-sm'
                  : 'text-muted-foreground hover:text-foreground'
              }`}
            >
              {t('logs.viewRaw')}
            </button>
            <button
              type="button"
              aria-pressed={viewMode === 'parsed'}
              onClick={() => setViewMode('parsed')}
              className={`px-3 py-1 rounded-lg text-xs font-medium transition outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-0 ${
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
          aria-pressed={isLive}
          className={isLive ? 'bg-red-600 hover:bg-red-700 text-white' : ''}
        >
          <Radio className={`h-3.5 w-3.5 ${isLive ? 'animate-pulse' : ''}`} />
          {t('logs.live')}
          {isLive && wsConnected && (
            <span className="ml-1 h-1.5 w-1.5 rounded-full bg-white animate-pulse" />
          )}
        </Button>

        {/* Auto-scroll */}
        <Button
          variant={autoScroll ? 'default' : 'outline'}
          size="sm"
          onClick={() => setAutoScroll(!autoScroll)}
          aria-pressed={autoScroll}
          title={t('logs.autoScroll')}
          aria-label={t('logs.autoScroll')}
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
          aria-label={t('logs.refresh')}
        >
          <RefreshCw className={`h-3.5 w-3.5 ${logLoading ? 'animate-spin' : ''}`} />
        </Button>

        {/* Search */}
        <Button
          ref={viewer.setSearchToggleEl}
          variant={viewer.searchOpen ? 'default' : 'outline'}
          size="icon-sm"
          aria-pressed={viewer.searchOpen}
          onClick={viewer.toggleSearch}
          title={t('logs.search')}
          aria-label={t('logs.search')}
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
          aria-label={t('logs.download')}
        >
          <Download className="h-3.5 w-3.5" />
        </Button>

        {/* Clear */}
        <Button
          variant="outline"
          size="icon-sm"
          onClick={handleClear}
          title={t('logs.clear')}
          aria-label={t('logs.clear')}
        >
          <Trash2 className="h-3.5 w-3.5" />
        </Button>
      </div>

      {/* Search bar */}
      <LogSearchBar viewer={viewer} />

      {/* Log info bar */}
      <div className="flex items-center justify-between px-3 py-1.5 bg-secondary/50 border border-b-0 rounded-t-xl text-xs text-muted-foreground">
        <div className="flex items-center gap-3">
          <span className="font-medium">{sourceName}</span>
          <span>{logLines.length.toLocaleString()} {t('logs.linesShown')}</span>
        </div>
        <div className="flex items-center gap-3">
          {totalLines > 0 && (
            <span>{t('logs.totalLines', { count: totalLines })}</span>
          )}
          {isLive && (
            <span className="flex items-center gap-1">
              <span className={`h-1.5 w-1.5 rounded-full ${wsConnected ? 'bg-green-500' : 'bg-red-500'}`} />
              {wsConnected ? t('logs.connected') : t('logs.connecting')}
            </span>
          )}
        </div>
      </div>

      {/* Log content — virtualized */}
      {logError ? (
        <div className="rounded-b-xl border border-t-0 -mt-4 p-3">
          <ListLoadState
            loading={false}
            error={logError}
            errorTitle={t('logs.loadLogFailed')}
            onRetry={handleRefresh}
          />
        </div>
      ) : (
        <LogTable
          viewer={viewer}
          label={sourceName}
          lines={logLines}
          loading={logLoading}
          loadingText={t('logs.loading')}
          emptyText={t('logs.empty')}
          className="max-h-[calc(100vh-380px)] border-t-0 -mt-4"
        />
      )}
    </div>
  )
}
