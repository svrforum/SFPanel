import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Plus,
  Pencil,
  Trash2,
  RefreshCw,
  Play,
  Pause,
  Info,
  ChevronDown,
  ChevronUp,
} from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import type { CronJob } from '@/types/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
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

interface SchedulePreset {
  label: string
  value: string
}

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

  const describeSchedule = (schedule: string): string | null => {
    const key = SCHEDULE_KEYS[schedule]
    return key ? t(key) : null
  }

  const [jobs, setJobs] = useState<CronJob[]>([])
  const [loading, setLoading] = useState(true)
  const [actionLoading, setActionLoading] = useState<number | null>(null)
  const [showAllTypes, setShowAllTypes] = useState(false)

  // Create/Edit dialog state
  const [dialogOpen, setDialogOpen] = useState(false)
  const [editingJob, setEditingJob] = useState<CronJob | null>(null)
  const [formSchedule, setFormSchedule] = useState('')
  const [formCommand, setFormCommand] = useState('')
  const [saving, setSaving] = useState(false)

  // Delete dialog state
  const [deleteTarget, setDeleteTarget] = useState<CronJob | null>(null)
  const [showGuide, setShowGuide] = useState(false)

  const presets: SchedulePreset[] = [
    { label: t('cron.presetEveryMinute'), value: '* * * * *' },
    { label: t('cron.presetEveryHour'), value: '0 * * * *' },
    { label: t('cron.presetDaily'), value: '0 0 * * *' },
    { label: t('cron.presetWeekly'), value: '0 0 * * 0' },
    { label: t('cron.presetMonthly'), value: '0 0 1 * *' },
  ]

  const fetchJobs = useCallback(async () => {
    try {
      setLoading(true)
      const data = await api.getCronJobs()
      setJobs(data || [])
    } catch (err: unknown) {
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
    setFormSchedule('')
    setFormCommand('')
    setDialogOpen(true)
  }

  const openEditDialog = (job: CronJob) => {
    setEditingJob(job)
    setFormSchedule(job.schedule)
    setFormCommand(job.command)
    setDialogOpen(true)
  }

  const closeDialog = () => {
    setDialogOpen(false)
    setEditingJob(null)
    setFormSchedule('')
    setFormCommand('')
  }

  const handleSave = async () => {
    if (!formSchedule.trim() || !formCommand.trim()) return
    setSaving(true)
    try {
      if (editingJob) {
        await api.updateCronJob(editingJob.id, formSchedule.trim(), formCommand.trim(), editingJob.enabled)
        toast.success(t('cron.updateSuccess'))
      } else {
        await api.createCronJob(formSchedule.trim(), formCommand.trim())
        toast.success(t('cron.createSuccess'))
      }
      closeDialog()
      await fetchJobs()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('cron.saveFailed')
      toast.error(message)
    } finally {
      setSaving(false)
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

  const handleDelete = async () => {
    if (!deleteTarget) return
    setActionLoading(deleteTarget.id)
    try {
      await api.deleteCronJob(deleteTarget.id)
      toast.success(t('cron.deleteSuccess'))
      setDeleteTarget(null)
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
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-[22px] font-bold tracking-tight">{t('cron.title')}</h1>
          <p className="text-[13px] text-muted-foreground mt-1">{t('cron.subtitle')}</p>
        </div>
        <div className="flex items-center gap-2">
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
      <div className="bg-card rounded-2xl card-shadow overflow-hidden">
        <button
          onClick={() => setShowGuide(!showGuide)}
          className="w-full flex items-center gap-2.5 px-4 py-3 text-left hover:bg-secondary/30 transition-colors"
        >
          <Info className="h-4 w-4 text-primary shrink-0" />
          <span className="text-[13px] font-medium flex-1">{t('cron.guideTitle')}</span>
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
                { num: '1', title: t('cron.guideTitle'), desc: t('cron.guideWhat') },
                { num: '2', title: 'root', desc: t('cron.guideWho') },
                { num: '3', title: t('cron.guideSchedule'), desc: t('cron.guideHow') },
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
            <div className="flex flex-wrap gap-x-4 gap-y-1 pt-1">
              <span className="text-[11px] text-muted-foreground">
                <span className="font-medium text-foreground">{t('cron.guideFile')}</span> /var/spool/cron/crontabs/root
              </span>
              <span className="text-[11px] text-muted-foreground">
                <span className="font-medium text-foreground">{t('cron.guideLog')}</span> /var/log/syslog
              </span>
            </div>
          </div>
        )}
      </div>

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

      {/* Mobile card view */}
      <div className="md:hidden space-y-2">
        {filteredJobs.length === 0 && !loading && (
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
                      title={t('common.edit')}
                      disabled={actionLoading === job.id}
                      onClick={() => openEditDialog(job)}
                    >
                      <Pencil className="h-4 w-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      title={t('common.delete')}
                      disabled={actionLoading === job.id}
                      onClick={() => setDeleteTarget(job)}
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
      <div className="hidden md:block bg-card rounded-2xl card-shadow overflow-hidden">
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
              {filteredJobs.length === 0 && !loading && (
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
                        <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-primary/10 text-primary">{t('cron.typeJob')}</span>
                      )}
                      {job.type === 'env' && (
                        <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-secondary text-muted-foreground">{t('cron.typeEnv')}</span>
                      )}
                      {job.type === 'comment' && (
                        <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-secondary text-muted-foreground">{t('cron.typeComment')}</span>
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
                          disabled={actionLoading === job.id}
                          onClick={() => openEditDialog(job)}
                        >
                          <Pencil />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon-xs"
                          title={t('common.delete')}
                          disabled={actionLoading === job.id}
                          onClick={() => setDeleteTarget(job)}
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
      <Dialog open={dialogOpen} onOpenChange={(open) => !open && closeDialog()}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {editingJob ? t('cron.editTitle') : t('cron.createTitle')}
            </DialogTitle>
            <DialogDescription>
              {editingJob ? t('cron.editDescription') : t('cron.createDescription')}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="cron-schedule">{t('cron.schedule')}</Label>
              <Input
                id="cron-schedule"
                placeholder="* * * * *"
                value={formSchedule}
                onChange={(e) => setFormSchedule(e.target.value)}
                className="font-mono"
              />
              <p className="text-xs text-muted-foreground">
                {t('cron.scheduleHint')}: <code className="bg-muted px-1 py-0.5 rounded">* * * * *</code>{' '}
                ({t('cron.scheduleFormat')})
              </p>
            </div>

            <div className="space-y-2">
              <Label>{t('cron.presets')}</Label>
              <div className="flex flex-wrap gap-2">
                {presets.map((preset) => (
                  <Button
                    key={preset.value}
                    type="button"
                    variant={formSchedule === preset.value ? 'default' : 'outline'}
                    size="sm"
                    onClick={() => setFormSchedule(preset.value)}
                  >
                    {preset.label}
                  </Button>
                ))}
              </div>
            </div>

            <div className="space-y-2">
              <Label htmlFor="cron-command">{t('cron.command')}</Label>
              <Input
                id="cron-command"
                placeholder={t('cron.commandPlaceholder')}
                value={formCommand}
                onChange={(e) => setFormCommand(e.target.value)}
                className="font-mono w-full"
              />
            </div>

            {formSchedule && describeSchedule(formSchedule) && (
              <div className="rounded-md bg-muted px-3 py-2 text-sm">
                <span className="text-muted-foreground">{t('cron.preview')}: </span>
                <span className="font-medium">{describeSchedule(formSchedule)}</span>
              </div>
            )}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={closeDialog} disabled={saving}>
              {t('common.cancel')}
            </Button>
            <Button
              onClick={handleSave}
              disabled={saving || !formSchedule.trim() || !formCommand.trim()}
            >
              {saving ? t('common.saving') : t('common.save')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete confirmation dialog */}
      <Dialog open={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('cron.deleteTitle')}</DialogTitle>
            <DialogDescription>
              {t('cron.deleteConfirm')}
            </DialogDescription>
          </DialogHeader>
          {deleteTarget && (
            <div className="rounded-md bg-muted px-3 py-2 space-y-1">
              <p className="text-sm">
                <span className="text-muted-foreground">{t('cron.schedule')}: </span>
                <code className="font-mono text-xs">{deleteTarget.schedule}</code>
              </p>
              <p className="text-sm">
                <span className="text-muted-foreground">{t('cron.command')}: </span>
                <code className="font-mono text-xs break-all">{deleteTarget.command}</code>
              </p>
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button
              variant="destructive"
              onClick={handleDelete}
              disabled={actionLoading === deleteTarget?.id}
            >
              {t('common.delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
