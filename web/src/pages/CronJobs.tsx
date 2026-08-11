import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Plus,
  Pencil,
  Trash2,
  RefreshCw,
  Play,
  Pause,
  Zap,
  Loader2,
  CheckCircle2,
  AlertCircle,
  ScrollText,
} from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import type { CronJob } from '@/types/api'
import { Button } from '@/components/ui/button'
import { useConfirm } from '@/components/ConfirmDialog'
import { GuideAccordion } from '@/components/GuideAccordion'
import { ListLoadState } from '@/components/ListLoadState'
import { StatusPill } from '@/components/StatusPill'
import { CronJobDialog } from '@/pages/cron/components/CronJobDialog'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

const SCHEDULE_KEYS: Record<string, string> = {
  '* * * * *': 'cron.scheduleDesc.everyMinute',
  '*/5 * * * *': 'cron.scheduleDesc.every5Minutes',
  '*/15 * * * *': 'cron.scheduleDesc.every15Minutes',
  '*/30 * * * *': 'cron.scheduleDesc.every30Minutes',
  '0 * * * *': 'cron.scheduleDesc.everyHour',
  '0 */2 * * *': 'cron.scheduleDesc.every2Hours',
  '0 */6 * * *': 'cron.scheduleDesc.every6Hours',
  '0 */12 * * *': 'cron.scheduleDesc.every12Hours',
  '0 0 * * *': 'cron.scheduleDesc.dailyMidnight',
  '0 0 * * 0': 'cron.scheduleDesc.weeklySunday',
  '0 0 * * 1': 'cron.scheduleDesc.weeklyMonday',
  '0 0 1 * *': 'cron.scheduleDesc.monthlyFirst',
  '0 0 1 1 *': 'cron.scheduleDesc.yearlyJan1',
  '@reboot': 'cron.scheduleDesc.atReboot',
  '@daily': 'cron.scheduleDesc.daily',
  '@hourly': 'cron.scheduleDesc.hourly',
  '@weekly': 'cron.scheduleDesc.weekly',
  '@monthly': 'cron.scheduleDesc.monthly',
  '@yearly': 'cron.scheduleDesc.yearly',
  '@annually': 'cron.scheduleDesc.yearly',
}

export default function CronJobs() {
  const { t } = useTranslation()
  const confirm = useConfirm()

  const describeSchedule = (schedule: string): string | null => {
    const key = SCHEDULE_KEYS[schedule]
    return key ? t(key) : null
  }

  const [jobs, setJobs] = useState<CronJob[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [actionLoading, setActionLoading] = useState<number | null>(null)
  const [showAllTypes, setShowAllTypes] = useState(false)

  // Run-now output dialog
  const [runJob, setRunJob] = useState<CronJob | null>(null)
  const [runResult, setRunResult] = useState<{ output: string; success: boolean; error?: string } | null>(null)
  const [runLoading, setRunLoading] = useState(false)

  // Create/Edit dialog state
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingJob, setEditingJob] = useState<CronJob | null>(null)

  // Cron execution logs (system journal / syslog — when jobs ran)
  const [logsOpen, setLogsOpen] = useState(false)
  const [logsContent, setLogsContent] = useState('')
  const [logsSource, setLogsSource] = useState('')
  const [logsLoading, setLogsLoading] = useState(false)

  const openLogs = useCallback(async () => {
    setLogsOpen(true)
    setLogsLoading(true)
    try {
      const res = await api.getCronLogs()
      setLogsContent(res.content)
      setLogsSource(res.source)
    } catch (err) {
      setLogsContent('')
      setLogsSource('')
      toast.error(err instanceof Error ? err.message : t('common.error'))
    } finally {
      setLogsLoading(false)
    }
  }, [t])

  const fetchJobs = useCallback(async () => {
    try {
      setLoading(true)
      setError(null)
      const data = await api.getCronJobs()
      setJobs(data || [])
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err))
      const message = err instanceof Error ? err.message : t('cron.fetchFailed')
      toast.error(message)
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchJobs()
  }, [fetchJobs])

  const filteredJobs = showAllTypes ? jobs : jobs.filter((j) => j.type === 'job')

  const jobCount = jobs.filter((j) => j.type === 'job').length

  const openCreateDialog = () => {
    setEditingJob(null)
    setDialogOpen(true)
  }

  const openEditDialog = (job: CronJob) => {
    setEditingJob(job)
    setDialogOpen(true)
  }

  const handleRunNow = async (job: CronJob) => {
    setRunJob(job)
    setRunResult(null)
    setRunLoading(true)
    try {
      const res = await api.runCronJob(job.id)
      setRunResult(res)
    } catch (err: unknown) {
      setRunResult({ output: '', success: false, error: err instanceof Error ? err.message : t('common.error') })
    } finally {
      setRunLoading(false)
    }
  }

  const handleToggleEnabled = async (job: CronJob) => {
    setActionLoading(job.id)
    try {
      await api.updateCronJob(job.id, job.schedule, job.command, !job.enabled)
      toast.success(
        job.enabled
          ? t('cron.disabled', { command: job.command })
          : t('cron.enabled', { command: job.command })
      )
      await fetchJobs()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('cron.toggleFailed')
      toast.error(message)
    } finally {
      setActionLoading(null)
    }
  }

  const handleDelete = async (job: CronJob) => {
    const ok = await confirm({
      title: t('cron.deleteTitle'),
      description: `${t('cron.deleteConfirm')} — ${t('cron.schedule')}: ${job.schedule} · ${t('cron.command')}: ${job.command}`,
      confirmLabel: t('common.delete'),
      danger: true,
    })
    if (!ok) return
    setActionLoading(job.id)
    try {
      await api.deleteCronJob(job.id)
      toast.success(t('cron.deleteSuccess'))
      await fetchJobs()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('cron.deleteFailed')
      toast.error(message)
    } finally {
      setActionLoading(null)
    }
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h1 className="text-[22px] font-bold tracking-tight">{t('cron.title')}</h1>
          <p className="text-[13px] text-muted-foreground mt-1">{t('cron.subtitle')}</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="outline" size="sm" className="rounded-xl" onClick={openLogs}>
            <ScrollText />
            {t('cron.logs')}
          </Button>
          <Button variant="outline" size="sm" className="rounded-xl" onClick={fetchJobs} disabled={loading}>
            <RefreshCw className={loading ? 'animate-spin' : ''} />
            {t('common.refresh')}
          </Button>
          <Button size="sm" className="rounded-xl" onClick={openCreateDialog}>
            <Plus />
            {t('cron.newJob')}
          </Button>
        </div>
      </div>

      {/* Guide */}
      <GuideAccordion
        title={t('cron.guideTitle')}
        steps={[
          { num: '1', title: t('cron.guideTitle'), desc: t('cron.guideWhat') },
          { num: '2', title: 'root', desc: t('cron.guideWho') },
          { num: '3', title: t('cron.guideSchedule'), desc: t('cron.guideHow') },
        ]}
        facts={[
          { label: t('cron.guideFile'), value: '/var/spool/cron/crontabs/root' },
          { label: t('cron.guideLog'), value: '/var/log/syslog' },
        ]}
      >
        <div className="rounded-lg bg-secondary/30 px-3 py-2.5 space-y-1.5">
          <p className="text-[11px] font-semibold text-foreground">{t('cron.guideSchedule')}: <code className="font-mono bg-muted px-1 py-0.5 rounded text-[10px]">{t('cron.guideScheduleDesc')}</code></p>
          <div className="flex flex-wrap gap-x-4 gap-y-1">
            <span className="text-[11px] text-muted-foreground">
              <code className="font-mono bg-muted px-1 py-0.5 rounded text-[10px]">0 3 * * *</code> — {t('cron.guideExampleDaily')}
            </span>
            <span className="text-[11px] text-muted-foreground">
              <code className="font-mono bg-muted px-1 py-0.5 rounded text-[10px]">0 0 * * 1</code> — {t('cron.guideExampleWeekly')}
            </span>
          </div>
        </div>
      </GuideAccordion>

      {/* Filter bar */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
            {t('cron.count', { count: jobCount })}
          </span>
          <label className="flex items-center gap-2 text-[13px] text-muted-foreground cursor-pointer">
            <input
              type="checkbox"
              checked={showAllTypes}
              onChange={(e) => setShowAllTypes(e.target.checked)}
              className="h-4 w-4 rounded border-gray-300"
            />
            {t('cron.showAll')}
          </label>
        </div>
      </div>

      {/* Load error / loading skeleton (first load only) */}
      {jobs.length === 0 && (
        <ListLoadState
          loading={loading}
          error={error}
          errorTitle={t('cron.loadError')}
          onRetry={fetchJobs}
        />
      )}

      {/* Mobile card view */}
      <div className={`md:hidden space-y-2 ${(error || loading) && jobs.length === 0 ? 'hidden' : ''}`}>
        {filteredJobs.length === 0 && !loading && !error && (
          <div className="text-center text-muted-foreground py-8 text-[13px]">
            {t('cron.empty')}
          </div>
        )}
        {filteredJobs.map((job) => (
          <div key={job.id} className={`bg-card rounded-2xl p-4 card-shadow ${!job.enabled && job.type === 'job' ? 'opacity-60' : ''}`}>
            {job.type === 'job' ? (
              <div className="space-y-2">
                <div className="flex items-start justify-between gap-2">
                  <div className="min-w-0 flex-1">
                    <code className="text-[13px] font-mono break-all">{job.command}</code>
                    <div className="flex items-center gap-2 mt-1.5">
                      <code className="text-[11px] font-mono bg-muted px-1.5 py-0.5 rounded">
                        {job.schedule}
                      </code>
                      {describeSchedule(job.schedule) && (
                        <span className="text-[11px] text-muted-foreground">
                          {describeSchedule(job.schedule)}
                        </span>
                      )}
                    </div>
                  </div>
                  <div className="flex items-center gap-1 shrink-0">
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      title={job.enabled ? t('cron.clickToDisable') : t('cron.clickToEnable')}
                      aria-label={job.enabled ? t('cron.clickToDisable') : t('cron.clickToEnable')}
                      disabled={actionLoading === job.id}
                      onClick={() => handleToggleEnabled(job)}
                    >
                      {job.enabled ? (
                        <Play className="h-4 w-4 text-green-600" />
                      ) : (
                        <Pause className="h-4 w-4 text-muted-foreground" />
                      )}
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      title={t('cron.runNow')}
                      aria-label={t('cron.runNow')}
                      disabled={actionLoading === job.id}
                      onClick={() => handleRunNow(job)}
                    >
                      <Zap className="h-4 w-4 text-amber-500" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      title={t('common.edit')}
                      aria-label={t('common.edit')}
                      disabled={actionLoading === job.id}
                      onClick={() => openEditDialog(job)}
                    >
                      <Pencil className="h-4 w-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      title={t('common.delete')}
                      aria-label={t('common.delete')}
                      disabled={actionLoading === job.id}
                      onClick={() => handleDelete(job)}
                    >
                      <Trash2 className="h-4 w-4" />
                    </Button>
                  </div>
                </div>
              </div>
            ) : job.type === 'comment' ? (
              <span className="text-muted-foreground text-[13px] italic">{job.raw}</span>
            ) : (
              <code className="text-[13px] font-mono text-amber-600">{job.raw}</code>
            )}
          </div>
        ))}
      </div>

      {/* Desktop table */}
      <div className={`bg-card rounded-2xl card-shadow overflow-hidden ${(error || loading) && jobs.length === 0 ? 'hidden' : 'hidden md:block'}`}>
        <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-[80px]">{t('common.status')}</TableHead>
                <TableHead className="w-[200px]">{t('cron.schedule')}</TableHead>
                <TableHead>{t('cron.command')}</TableHead>
                {showAllTypes && <TableHead className="w-[80px]">{t('cron.type')}</TableHead>}
                <TableHead className="text-right w-[120px]">{t('common.actions')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {filteredJobs.length === 0 && !loading && !error && (
                <TableRow>
                  <TableCell
                    colSpan={showAllTypes ? 5 : 4}
                    className="text-center text-muted-foreground py-8"
                  >
                    {t('cron.empty')}
                  </TableCell>
                </TableRow>
              )}
              {filteredJobs.map((job) => (
                <TableRow key={job.id} className={!job.enabled && job.type === 'job' ? 'opacity-60' : ''}>
                  <TableCell>
                    {job.type === 'job' ? (
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        title={job.enabled ? t('cron.clickToDisable') : t('cron.clickToEnable')}
                        aria-label={job.enabled ? t('cron.clickToDisable') : t('cron.clickToEnable')}
                        disabled={actionLoading === job.id}
                        onClick={() => handleToggleEnabled(job)}
                      >
                        {job.enabled ? (
                          <Play className="h-4 w-4 text-green-600" />
                        ) : (
                          <Pause className="h-4 w-4 text-muted-foreground" />
                        )}
                      </Button>
                    ) : (
                      <span className="text-muted-foreground text-xs">--</span>
                    )}
                  </TableCell>
                  <TableCell>
                    {job.type === 'job' ? (
                      <div className="space-y-1">
                        <code className="text-xs font-mono bg-muted px-1.5 py-0.5 rounded">
                          {job.schedule}
                        </code>
                        {describeSchedule(job.schedule) && (
                          <p className="text-xs text-muted-foreground">
                            {describeSchedule(job.schedule)}
                          </p>
                        )}
                      </div>
                    ) : (
                      <span className="text-muted-foreground text-xs">--</span>
                    )}
                  </TableCell>
                  <TableCell>
                    {job.type === 'comment' ? (
                      <span className="text-muted-foreground text-xs italic">{job.raw}</span>
                    ) : job.type === 'env' ? (
                      <code className="text-xs font-mono text-amber-600">{job.raw}</code>
                    ) : (
                      <code className="text-xs font-mono break-all">{job.command}</code>
                    )}
                  </TableCell>
                  {showAllTypes && (
                    <TableCell>
                      {job.type === 'job' && (
                        <StatusPill tone="primary">{t('cron.typeJob')}</StatusPill>
                      )}
                      {job.type === 'env' && (
                        <StatusPill tone="secondary">{t('cron.typeEnv')}</StatusPill>
                      )}
                      {job.type === 'comment' && (
                        <StatusPill tone="secondary">{t('cron.typeComment')}</StatusPill>
                      )}
                    </TableCell>
                  )}
                  <TableCell className="text-right">
                    {job.type === 'job' && (
                      <div className="flex items-center justify-end gap-1">
                        <Button
                          variant="ghost"
                          size="icon-xs"
                          title={t('common.edit')}
                          aria-label={t('common.edit')}
                          disabled={actionLoading === job.id}
                          onClick={() => openEditDialog(job)}
                        >
                          <Pencil />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon-xs"
                          title={t('common.delete')}
                          aria-label={t('common.delete')}
                          disabled={actionLoading === job.id}
                          onClick={() => handleDelete(job)}
                        >
                          <Trash2 />
                        </Button>
                      </div>
                    )}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
      </div>

      {/* Create/Edit dialog */}
      <CronJobDialog
        open={dialogOpen}
        job={editingJob}
        onOpenChange={(open) => {
          setDialogOpen(open)
          if (!open) setEditingJob(null)
        }}
        onSaved={fetchJobs}
        describeSchedule={describeSchedule}
      />

      {/* Run-now output dialog */}
      <Dialog open={!!runJob} onOpenChange={(open) => !open && setRunJob(null)}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              {runLoading && <Loader2 className="h-4 w-4 animate-spin" />}
              {!runLoading && runResult?.success && <CheckCircle2 className="h-4 w-4 text-success" />}
              {!runLoading && runResult && !runResult.success && <AlertCircle className="h-4 w-4 text-destructive" />}
              {t('cron.runNow')}
            </DialogTitle>
            <DialogDescription className="font-mono text-[11px] break-all">{runJob?.command}</DialogDescription>
          </DialogHeader>
          <div className="bg-zinc-950 text-zinc-100 rounded-xl p-4 max-h-80 overflow-y-auto">
            <pre className="text-xs font-mono whitespace-pre-wrap break-words">
              {runLoading
                ? t('cron.running')
                : (runResult?.output || '') + (runResult?.error ? `\n${runResult.error}` : '') || t('cron.noOutput')}
            </pre>
          </div>
          <DialogFooter>
            <Button variant="outline" className="rounded-xl" onClick={() => setRunJob(null)}>
              {t('common.close')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Cron execution logs dialog */}
      <Dialog open={logsOpen} onOpenChange={setLogsOpen}>
        <DialogContent className="sm:max-w-3xl">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              <ScrollText className="h-4 w-4" />
              {t('cron.logsTitle')}
            </DialogTitle>
            <DialogDescription>
              {t('cron.logsDescription')}{logsSource ? ` · ${logsSource}` : ''}
            </DialogDescription>
          </DialogHeader>
          <div className="bg-zinc-950 text-zinc-100 rounded-xl p-4 max-h-[60vh] overflow-auto">
            <pre className="text-[11px] font-mono whitespace-pre-wrap break-words">
              {logsLoading ? t('common.loading') : (logsContent || t('cron.noLogs'))}
            </pre>
          </div>
          <DialogFooter>
            <Button variant="outline" className="rounded-xl" onClick={openLogs} disabled={logsLoading}>
              <RefreshCw className={logsLoading ? 'animate-spin' : ''} />
              {t('common.refresh')}
            </Button>
            <Button variant="outline" className="rounded-xl" onClick={() => setLogsOpen(false)}>
              {t('common.close')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
