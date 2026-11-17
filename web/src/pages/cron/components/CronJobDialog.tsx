import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import type { CronJob } from '@/types/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { cronFormErrors, type CronFormErrors } from '@/pages/cron/cronForm'

const PRESETS: { labelKey: string; value: string }[] = [
  { labelKey: 'cron.presetEveryMinute', value: '* * * * *' },
  { labelKey: 'cron.presetEveryHour', value: '0 * * * *' },
  { labelKey: 'cron.presetDaily', value: '0 0 * * *' },
  { labelKey: 'cron.presetWeekly', value: '0 0 * * 0' },
  { labelKey: 'cron.presetMonthly', value: '0 0 1 * *' },
]

// Create/edit form for a crontab entry. `job` null means create.
export function CronJobDialog({
  open,
  job,
  onOpenChange,
  onSaved,
  describeSchedule,
}: {
  open: boolean
  job: CronJob | null
  onOpenChange: (open: boolean) => void
  onSaved: () => Promise<void> | void
  /** Natural-language preview for known schedules (owned by the page's SCHEDULE_KEYS). */
  describeSchedule: (schedule: string) => string | null
}) {
  const { t } = useTranslation()
  const [formSchedule, setFormSchedule] = useState('')
  const [formCommand, setFormCommand] = useState('')
  const [saving, setSaving] = useState(false)
  // Set by a save that was refused here, cleared as the field is edited.
  const [errors, setErrors] = useState<CronFormErrors>({})

  // Reset the form each time the dialog opens (create: blank, edit: job values)
  useEffect(() => {
    if (!open) return
    setFormSchedule(job?.schedule ?? '')
    setFormCommand(job?.command ?? '')
    setErrors({})
  }, [open, job])

  const handleSave = async () => {
    const found = cronFormErrors(formSchedule, formCommand)
    setErrors(found)
    if (found.schedule || found.command) return
    setSaving(true)
    try {
      if (job) {
        await api.updateCronJob(job.id, formSchedule.trim(), formCommand.trim(), job.enabled)
        toast.success(t('cron.updateSuccess'))
      } else {
        await api.createCronJob(formSchedule.trim(), formCommand.trim())
        toast.success(t('cron.createSuccess'))
      }
      onOpenChange(false)
      await onSaved()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('cron.saveFailed')
      toast.error(message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onOpenChange(false)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {job ? t('cron.editTitle') : t('cron.createTitle')}
          </DialogTitle>
          <DialogDescription>
            {job ? t('cron.editDescription') : t('cron.createDescription')}
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="cron-schedule">{t('cron.schedule')}</Label>
            <Input
              id="cron-schedule"
              placeholder="* * * * *"
              value={formSchedule}
              onChange={(e) => { setFormSchedule(e.target.value); setErrors((x) => ({ ...x, schedule: undefined })) }}
              aria-invalid={!!errors.schedule}
              aria-describedby={errors.schedule ? 'cron-schedule-error' : undefined}
              autoCapitalize="off"
              autoCorrect="off"
              spellCheck={false}
              className="font-mono"
            />
            {errors.schedule && (
              <p id="cron-schedule-error" className="text-xs text-destructive">{t(errors.schedule)}</p>
            )}
            <p className="text-xs text-muted-foreground">
              {t('cron.scheduleHint')}: <code className="bg-muted px-1 py-0.5 rounded">* * * * *</code>{' '}
              ({t('cron.scheduleFormat')})
            </p>
          </div>

          <div className="space-y-2">
            <Label>{t('cron.presets')}</Label>
            <div className="flex flex-wrap gap-2">
              {PRESETS.map((preset) => (
                <Button
                  key={preset.value}
                  type="button"
                  variant={formSchedule === preset.value ? 'default' : 'outline'}
                  size="sm"
                  aria-pressed={formSchedule === preset.value}
                  onClick={() => { setFormSchedule(preset.value); setErrors((x) => ({ ...x, schedule: undefined })) }}
                >
                  {t(preset.labelKey)}
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
              onChange={(e) => { setFormCommand(e.target.value); setErrors((x) => ({ ...x, command: undefined })) }}
              aria-invalid={!!errors.command}
              aria-describedby={errors.command ? 'cron-command-error' : undefined}
              autoCapitalize="off"
              autoCorrect="off"
              spellCheck={false}
              className="font-mono w-full"
            />
            {errors.command && (
              <p id="cron-command-error" className="text-xs text-destructive">{t(errors.command)}</p>
            )}
          </div>

          {formSchedule && describeSchedule(formSchedule) && (
            <div className="rounded-md bg-muted px-3 py-2 text-sm">
              <span className="text-muted-foreground">{t('cron.preview')}: </span>
              <span className="font-medium">{describeSchedule(formSchedule)}</span>
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
            {t('common.cancel')}
          </Button>
          <Button onClick={handleSave} disabled={saving}>
            {saving ? t('common.saving') : t('common.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
