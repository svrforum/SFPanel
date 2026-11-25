import { useEffect, useState, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useSearchParams } from 'react-router-dom'
import { api } from '@/lib/api'
import { formatBytes, downloadBlob } from '@/lib/utils'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { FileText, RefreshCw, Radio, ArrowDown, Trash2, Eye, Search, ChevronLeft, ChevronRight, X, Download, Plus } from 'lucide-react'
import { Input } from '@/components/ui/input'
import { hasParsedView } from '@/lib/logParsers'
import { pickLogSource } from '@/lib/logSources'
import { useConfirm } from '@/components/ConfirmDialog'
import { GuideAccordion } from '@/components/GuideAccordion'
import { ListLoadState } from '@/components/ListLoadState'
import { LiveLogSocket } from '@/components/logviewer/LiveLogSocket'
import { LogSearchBar } from '@/components/logviewer/LogSearchBar'
import { LogTable } from '@/components/logviewer/LogTable'
import { useLogViewer } from '@/components/logviewer/useLogViewer'
import { appendLogLines, LINE_COUNT_OPTIONS, type LineCount } from '@/components/logviewer/logViewUtils'

interface LogSource {
  id: string
  name: string
  path: string
  size: number
  exists: boolean
  custom: boolean
  custom_id?: number
  // Read from the journal: the file is absent (no rsyslog) and path is the
  // journalctl selection.
  journal?: boolean
}

interface LogResponse {
  source: string
  lines: string[]
  total_lines: number
}

export default function Logs() {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const [searchParams, setSearchParams] = useSearchParams()

  // Sources
  const [sources, setSources] = useState<LogSource[]>([])
  const [sourcesLoading, setSourcesLoading] = useState(true)
  // A failed first load: an empty list would read as "no sources".
  const [sourcesError, setSourcesError] = useState<string | null>(null)

  // Log state
  const [selectedSource, setSelectedSource] = useState<string | null>(null)
  const [logLines, setLogLines] = useState<string[]>([])
  const [isLive, setIsLive] = useState(false)
  const [autoScroll, setAutoScroll] = useState(true)
  const [lineCount, setLineCount] = useState<LineCount>(500)
  const [logLoading, setLogLoading] = useState(false)
  const [totalLines, setTotalLines] = useState(0)
  // A failed read: the empty viewer would read as "no log entries".
  const [logError, setLogError] = useState<string | null>(null)

  // View mode
  const [viewMode, setViewMode] = useState<'raw' | 'parsed'>('parsed')

  // Custom source dialog
  const [addDialogOpen, setAddDialogOpen] = useState(false)
  const [newSourceName, setNewSourceName] = useState('')
  const [newSourcePath, setNewSourcePath] = useState('')
  const [addingSource, setAddingSource] = useState(false)
  // Set by the first press of Add: a field left empty says so from then on.
  const [addTried, setAddTried] = useState(false)

  // Live WebSocket state (connection itself lives in <LiveLogSocket />)
  const [wsConnected, setWsConnected] = useState(false)

  // Shared viewer machinery: parsing, virtual scroll, search, Ctrl+F
  const viewer = useLogViewer({ sourceId: selectedSource, lines: logLines, viewMode, autoScroll })

  // Fetch log sources on mount
  useEffect(() => {
    loadSources()
    // We intentionally only run loadSources on mount; including it as a dep
    // would cause re-fetches every render because the function is redefined.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  async function loadSources() {
    setSourcesLoading(true)
    setSourcesError(null)
    try {
      const data = await api.getLogSources()
      setSources(data)
      // The source the address asks for, else the first one this server has
      if (!selectedSource) {
        const picked = pickLogSource(data, searchParams.get('source'))
        if (picked) {
          setSelectedSource(picked)
          setViewMode(hasParsedView(picked) ? 'parsed' : 'raw')
        }
        setSourceParam(picked)
      }
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('logs.loadSourcesFailed')
      if (sources.length === 0) setSourcesError(message)
      else toast.error(message)
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
    // Lines are arriving: the read that failed earlier no longer describes what
    // there is to show, and its block would hide them.
    setLogError(null)
    setLogLines((prev) => appendLogLines(prev, batch))
  }, [])

  function handleToggleLive() {
    if (isLive) {
      setIsLive(false)
      setWsConnected(false)
    } else {
      if (!selectedSource) {
        toast.error(t('logs.selectSourceFirst'))
        return
      }
      setIsLive(true)
    }
  }

  // Stop the live tail when switching sources
  useEffect(() => {
    setIsLive(false)
    setWsConnected(false)
  }, [selectedSource])

  // The address says which source is open. Replaced, not pushed, and only
  // `source`: every other parameter stays as it was.
  function setSourceParam(id: string | null) {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev)
      if (id) next.set('source', id)
      else next.delete('source')
      return next
    }, { replace: true })
  }

  function handleSourceSelect(sourceId: string) {
    const source = sources.find((s) => s.id === sourceId)
    if (source && !source.exists) return
    setSelectedSource(sourceId)
    setSourceParam(sourceId)
    setViewMode(hasParsedView(sourceId) ? 'parsed' : 'raw')
  }

  function handleRefresh() {
    if (selectedSource) loadLog(selectedSource, lineCount)
  }

  function handleClear() {
    setLogLines([])
    setTotalLines(0)
  }

  // Said under the field, not in a toast after the press.
  const sourceNameError = addTried && !newSourceName.trim()
  const sourcePathError = newSourcePath.trim() ? !newSourcePath.trim().startsWith('/') : addTried

  function openAddDialog(open: boolean) {
    setAddDialogOpen(open)
    if (open) setAddTried(false)
  }

  async function handleAddSource() {
    const name = newSourceName.trim()
    const path = newSourcePath.trim()
    setAddTried(true)
    if (!name) {
      document.getElementById('log-source-name')?.focus()
      return
    }
    if (!path.startsWith('/')) {
      document.getElementById('log-source-path')?.focus()
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

  async function handleDeleteSource(source: LogSource) {
    if (!source.custom || !source.custom_id) return
    const ok = await confirm({
      title: t('logs.deleteSource'),
      description: t('logs.deleteSourceConfirm', { name: source.name, path: source.path }),
      confirmLabel: t('logs.deleteSource'),
      danger: true,
    })
    if (!ok) return
    try {
      await api.deleteCustomLogSource(source.custom_id)
      toast.success(t('logs.sourceDeleted'))
      if (selectedSource === source.id) {
        setSelectedSource(null)
        setSourceParam(null)
        setLogLines([])
      }
      loadSources()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('logs.deleteSourceFailed')
      toast.error(message)
    }
  }

  const selectedSourceData = sources.find((s) => s.id === selectedSource)

  // Download logs
  const handleDownload = useCallback(() => {
    if (logLines.length === 0) return
    const blob = new Blob([logLines.join('\n')], { type: 'text/plain' })
    downloadBlob(blob, `${selectedSourceData?.name || 'log'}-${new Date().toISOString().slice(0, 19).replace(/:/g, '-')}.log`)
  }, [logLines, selectedSourceData])

  return (
    <div className="space-y-6">
      {/* Page header */}
      <div>
        <h1 className="text-[22px] font-bold tracking-tight">{t('logs.title')}</h1>
        <p className="text-[13px] text-muted-foreground mt-1">{t('logs.subtitle')}</p>
      </div>

      {/* How it works */}
      <GuideAccordion
        title={t('logs.guideTitle')}
        steps={[
          { num: '1', title: t('logs.guideStep1Title'), desc: t('logs.guideStep1Desc') },
          { num: '2', title: t('logs.guideStep2Title'), desc: t('logs.guideStep2Desc') },
          { num: '3', title: t('logs.guideStep3Title'), desc: t('logs.guideStep3Desc') },
        ]}
        facts={[
          { label: t('logs.guideStreaming'), value: 'WebSocket (tail -F)' },
          { label: t('logs.guideSearch'), value: 'Ctrl+F' },
          { label: t('logs.guideParsed'), value: 'Firewall, Auth, Fail2ban, SFPanel' },
        ]}
      />

      {/* Live tail connection (mounted only while live mode is on) */}
      {isLive && selectedSource && (
        <LiveLogSocket
          source={selectedSource}
          onLines={handleLiveLines}
          onConnectedChange={setWsConnected}
        />
      )}

      <div className="flex flex-col md:flex-row gap-6">
        {/* Left sidebar: log sources */}
        <div className={`w-full md:w-72 shrink-0 space-y-2 ${selectedSource ? 'hidden md:block' : ''}`}>
          <div className="flex items-center justify-between mb-3">
            <h2 className="text-sm font-semibold text-muted-foreground uppercase tracking-wider">
              {t('logs.sources')}
            </h2>
            <Button
              variant="ghost"
              size="icon-xs"
              onClick={() => openAddDialog(true)}
              title={t('logs.addSource')}
              aria-label={t('logs.addSource')}
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
          ) : sourcesError && sources.length === 0 ? (
            <ListLoadState
              loading={false}
              error={sourcesError}
              errorTitle={t('logs.loadSourcesFailed')}
              onRetry={loadSources}
            />
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
                  className={`w-full text-left rounded-xl p-3 transition duration-200 outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-0 ${
                    selectedSource === source.id
                      ? 'bg-primary/10 ring-1 ring-primary/20'
                      : source.exists
                        ? 'bg-card card-shadow hover:card-shadow-hover'
                        : 'bg-secondary/50 opacity-50 cursor-not-allowed'
                  }`}
                >
                  <div className={`flex items-start gap-2 ${source.custom && source.custom_id ? 'pr-8' : ''}`}>
                    <FileText className={`h-4 w-4 mt-0.5 shrink-0 ${
                      selectedSource === source.id
                        ? 'text-primary'
                        : source.exists
                          ? 'text-muted-foreground'
                          : 'text-muted-foreground/50'
                    }`} />
                    <div className="min-w-0 flex-1">
                      <p className="text-[13px] font-medium truncate">{source.name}</p>
                      <p className="text-[12px] text-muted-foreground truncate mt-0.5" title={source.path}>
                        {source.path}
                      </p>
                      <div className="flex items-center gap-2 mt-1.5">
                        {source.journal ? (
                          <span className="inline-flex items-center px-1.5 py-0 rounded-full text-[12px] font-medium bg-secondary text-muted-foreground">
                            {t('logs.journal')}
                          </span>
                        ) : source.exists ? (
                          <span className="inline-flex items-center px-1.5 py-0 rounded-full text-[12px] font-medium bg-secondary text-muted-foreground">
                            {formatBytes(source.size)}
                          </span>
                        ) : (
                          <span className="inline-flex items-center px-1.5 py-0 rounded-full text-[12px] font-medium bg-secondary/50 text-muted-foreground">
                            {t('logs.notFound')}
                          </span>
                        )}
                        {source.custom && (
                          <span className="inline-flex items-center px-1.5 py-0 rounded-full text-[12px] font-medium bg-primary/10 text-primary">
                            {t('logs.customSource')}
                          </span>
                        )}
                      </div>
                    </div>
                  </div>
                </button>
                {/* 28px, and the source's text stops short of it (pr-8): a
                    20px box sat on top of the source's own button. */}
                {source.custom && source.custom_id && (
                  <button
                    onClick={(e) => { e.stopPropagation(); handleDeleteSource(source) }}
                    className="absolute top-1.5 right-1.5 h-7 w-7 rounded-md flex items-center justify-center opacity-100 md:can-hover:opacity-0 md:group-hover:opacity-100 md:group-focus-within:opacity-100 transition-opacity bg-destructive/10 hover:bg-destructive/20 text-destructive outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-0"
                    title={t('logs.deleteSource')}
                    aria-label={t('logs.deleteSource')}
                  >
                    <X className="h-3.5 w-3.5" />
                  </button>
                )}
              </div>
            ))
          )}

          {/* Records kept elsewhere: shown whatever became of the list above */}
          <nav aria-label={t('logs.otherRecords')} className="pt-4">
            <h2 className="mb-1 text-sm font-semibold text-muted-foreground uppercase tracking-wider">
              {t('logs.otherRecords')}
            </h2>
            <ul>
              {[
                { to: '/services', label: t('layout.nav.services') },
                { to: '/cron', label: t('layout.nav.cron') },
                { to: '/settings?scope=node&tab=audit', label: t('settings.tabAuditLog') },
              ].map((link) => (
                <li key={link.to}>
                  <Link
                    to={link.to}
                    className="flex min-h-11 items-center justify-between gap-2 rounded-xl px-3 text-[13px] font-medium text-foreground hover:bg-secondary outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-0"
                  >
                    {link.label}
                    <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground" />
                  </Link>
                </li>
              ))}
            </ul>
          </nav>
        </div>

        {/* Main log content area — hidden on mobile when no source selected */}
        <div className={`flex-1 min-w-0 flex flex-col ${!selectedSource ? 'hidden md:flex' : ''}`}>
          {/* Toolbar */}
          <div className="flex flex-wrap items-center gap-2 mb-3">
            {/* Back button (mobile only) */}
            <Button
              variant="ghost"
              size="icon-sm"
              className="rounded-xl md:hidden"
              onClick={() => { setSelectedSource(null); setSourceParam(null) }}
              aria-label={t('common.back')}
            >
              <ChevronLeft className="h-4 w-4" />
            </Button>

            {/* Line count selector */}
            <div role="group" aria-labelledby="log-lines-label" className="flex items-center gap-1 mr-2 overflow-x-auto no-scrollbar">
              <span id="log-lines-label" className="text-xs text-muted-foreground mr-1 shrink-0">{t('logs.lines')}:</span>
              {LINE_COUNT_OPTIONS.map((count) => (
                <Button
                  key={count}
                  variant={lineCount === count ? 'default' : 'outline'}
                  size="xs"
                  className="rounded-xl shrink-0"
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
              disabled={!selectedSource}
              aria-pressed={isLive}
              className={`rounded-xl ${isLive ? 'bg-red-600 hover:bg-red-700 text-white' : ''}`}
            >
              <Radio className={`h-3.5 w-3.5 ${isLive ? 'animate-pulse' : ''}`} />
              <span className="sr-only sm:not-sr-only">{t('logs.live')}</span>
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
              aria-pressed={autoScroll}
              title={t('logs.autoScroll')}
            >
              <ArrowDown className="h-3.5 w-3.5" />
              <span className="sr-only sm:not-sr-only">{t('logs.autoScroll')}</span>
            </Button>

            {/* Refresh */}
            <Button
              variant="outline"
              size="icon-sm"
              className="rounded-xl"
              onClick={handleRefresh}
              disabled={!selectedSource || logLoading}
              title={t('logs.refresh')}
              aria-label={t('logs.refresh')}
            >
              <RefreshCw className={`h-3.5 w-3.5 ${logLoading ? 'animate-spin' : ''}`} />
            </Button>

            {/* Search toggle */}
            <Button
              ref={viewer.setSearchToggleEl}
              variant={viewer.searchOpen ? 'default' : 'outline'}
              size="icon-sm"
              className="rounded-xl"
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
              className="rounded-xl"
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
              className="rounded-xl"
              onClick={handleClear}
              title={t('logs.clear')}
              aria-label={t('logs.clear')}
            >
              <Trash2 className="h-3.5 w-3.5" />
            </Button>
          </div>

          {/* Search bar */}
          <LogSearchBar viewer={viewer} className="mb-3" />

          {/* Log info bar: each item keeps its words on one line (a phone
              broke "lines shown" mid-word); the path gives way first. */}
          <div className="flex items-center justify-between gap-3 px-3 py-1.5 bg-secondary/50 border border-b-0 rounded-t-xl text-xs text-muted-foreground">
            <div className="flex min-w-0 items-center gap-3">
              {selectedSourceData ? (
                <>
                  <span className="flex shrink-0 items-center gap-1.5 whitespace-nowrap">
                    <Eye className="h-3 w-3" />
                    {selectedSourceData.name}
                  </span>
                  <span className="min-w-0 truncate" title={selectedSourceData.path}>{selectedSourceData.path}</span>
                </>
              ) : (
                <span>{t('logs.selectSource')}</span>
              )}
            </div>
            <div className="flex shrink-0 items-center gap-3 whitespace-nowrap">
              {totalLines > 0 && totalLines !== logLines.length && (
                <span>{t('logs.totalLines', { count: totalLines })}</span>
              )}
              <span>{logLines.length.toLocaleString()} {t('logs.linesShown')}</span>
              {isLive && (
                <span className="flex items-center gap-1">
                  <span className={`h-1.5 w-1.5 rounded-full ${wsConnected ? 'bg-green-500' : 'bg-red-500'}`} />
                  {wsConnected ? t('logs.connected') : t('logs.connecting')}
                </span>
              )}
            </div>
          </div>

          {/* Log content — virtualized */}
          {selectedSource && logError ? (
            <div className="rounded-b-xl border p-3">
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
              label={selectedSourceData?.name ?? t('logs.title')}
              lines={logLines}
              loading={logLoading}
              loadingText={t('logs.loading')}
              emptyText={t('logs.empty')}
              className="flex-1 max-h-[calc(100vh-320px)] overflow-x-auto"
              placeholder={
                !selectedSource ? (
                  <div className="text-center space-y-2">
                    <FileText className="h-12 w-12 mx-auto text-gray-500" />
                    <p>{t('logs.selectSourcePrompt')}</p>
                  </div>
                ) : undefined
              }
            />
          )}
        </div>
      </div>

      {/* Add Custom Source Dialog */}
      <Dialog open={addDialogOpen} onOpenChange={openAddDialog}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{t('logs.addSourceTitle')}</DialogTitle>
          </DialogHeader>
          <div className="space-y-4 pt-2">
            <div className="space-y-2">
              <label htmlFor="log-source-name" className="text-[13px] font-medium">{t('logs.sourceName')}</label>
              <Input
                id="log-source-name"
                value={newSourceName}
                onChange={(e) => setNewSourceName(e.target.value)}
                placeholder={t('logs.sourceNamePlaceholder')}
                aria-invalid={sourceNameError || undefined}
                aria-describedby={sourceNameError ? 'log-source-name-error' : undefined}
                className="h-9 rounded-xl text-base md:text-[13px]"
              />
              {sourceNameError && (
                <p id="log-source-name-error" className="text-[12px] text-destructive">{t('logs.nameRequired')}</p>
              )}
            </div>
            <div className="space-y-2">
              <label htmlFor="log-source-path" className="text-[13px] font-medium">{t('logs.sourcePath')}</label>
              <Input
                id="log-source-path"
                value={newSourcePath}
                onChange={(e) => setNewSourcePath(e.target.value)}
                placeholder={t('logs.sourcePathPlaceholder')}
                aria-invalid={sourcePathError || undefined}
                aria-describedby={sourcePathError ? 'log-source-path-error' : undefined}
                className="h-9 rounded-xl text-base md:text-[13px] font-mono"
              />
              {sourcePathError && (
                <p id="log-source-path-error" className="text-[12px] text-destructive">{t('logs.pathInvalid')}</p>
              )}
            </div>
            <div className="flex justify-end gap-2 pt-2">
              <Button variant="outline" onClick={() => openAddDialog(false)} className="rounded-xl">
                {t('common.cancel')}
              </Button>
              <Button
                onClick={handleAddSource}
                disabled={addingSource}
                className="rounded-xl"
              >
                {addingSource ? t('common.saving') : t('logs.addSource')}
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}
