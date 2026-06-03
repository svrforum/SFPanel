import { useState, useEffect, type ChangeEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { formatUptime, formatBytes } from '@/lib/utils'
import type { HostInfo, BackupScheduleConfig, BackupFile } from '@/types/api'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Download, Upload, RefreshCw, AlertCircle, Clock, Play, Trash2 } from 'lucide-react'
import { useApiAction } from '@/hooks/useApiAction'

type MaintenanceProps = {
  clusterEnabled: boolean
}

export default function Maintenance({ clusterEnabled }: MaintenanceProps) {
  const { t } = useTranslation()

  // System info state
  const [systemInfo, setSystemInfo] = useState<{ host: HostInfo; version?: string } | null>(null)
  const [panelVersion, setPanelVersion] = useState('...')

  // Update state
  const [updateInfo, setUpdateInfo] = useState<{ latest_version: string; update_available: boolean; release_notes: string } | null>(null)
  const [updating, setUpdating] = useState(false)
  const [updateStep, setUpdateStep] = useState('')
  const [updateError, setUpdateError] = useState('')

  // Backup state
  const [backupLoading, setBackupLoading] = useState(false)
  const [restoreLoading, setRestoreLoading] = useState(false)

  // Scheduled backup state
  const [schedule, setSchedule] = useState<BackupScheduleConfig | null>(null)
  const [backupFiles, setBackupFiles] = useState<BackupFile[]>([])
  const [scheduleEnabled, setScheduleEnabled] = useState(false)
  const [intervalHours, setIntervalHours] = useState('24')
  const [retention, setRetention] = useState('7')
  const [savingSchedule, setSavingSchedule] = useState(false)
  const [runningNow, setRunningNow] = useState(false)

  async function loadBackupSchedule() {
    try {
      const data = await api.getBackupSchedule()
      setSchedule(data.schedule)
      setBackupFiles(data.files ?? [])
      setScheduleEnabled(data.schedule.enabled)
      setIntervalHours(String(data.schedule.interval_hours))
      setRetention(String(data.schedule.retention))
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : String(err))
    }
  }

  useEffect(() => {
    loadBackupSchedule()
  }, [])

  useEffect(() => {
    api.getSystemInfo()
      .then((data) => {
        setSystemInfo(data)
        if (data.version) setPanelVersion(data.version)
      })
      .catch(() => { /* ignore */ })
  }, [])

  const { run: runCheckUpdate, loading: updateChecking } = useApiAction(
    api.checkUpdate.bind(api),
    {
      errorMsg: 'Failed',
      onSuccess: (data) => {
        setUpdateInfo(data)
        if (!data.update_available) {
          toast.success(t('settings.upToDate'))
        }
      },
    },
  )

  async function handleCheckUpdate() {
    setUpdateError('')
    await runCheckUpdate()
  }

  async function handleRunUpdate() {
    if (!window.confirm(t('settings.updateConfirm'))) return
    setUpdating(true)
    setUpdateStep('')
    setUpdateError('')
    try {
      await api.runUpdateStream((event) => {
        setUpdateStep(event.step)
        if (event.step === 'error') {
          setUpdateError(event.message)
          setUpdating(false)
        }
        if (event.step === 'complete') {
          setTimeout(() => {
            const check = setInterval(() => {
              fetch(`${api.apiBase}/auth/setup-status`)
                .then(() => { clearInterval(check); window.location.reload() })
                .catch(() => {})
            }, 2000)
          }, 3000)
        }
      })
    } catch {
      setUpdating(false)
      setUpdateError('Connection lost')
    }
  }

  async function handleDownloadBackup() {
    // In cluster mode the local SQLite snapshot is not a complete picture:
    // admin + jwt_secret + cluster_node state live in the Raft FSM and
    // restoring this backup on a leader would rewind replicated state,
    // on a follower it would desync immediately. Warn loudly before
    // letting the operator proceed.
    if (clusterEnabled && !window.confirm(t('settings.backupClusterWarn'))) {
      return
    }
    setBackupLoading(true)
    try {
      const blob = await api.downloadBackup()
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `sfpanel-backup-${new Date().toISOString().slice(0, 10)}.tar.gz`
      a.click()
      URL.revokeObjectURL(url)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('settings.backupFailed')
      toast.error(message)
    } finally {
      setBackupLoading(false)
    }
  }

  async function handleRestoreBackup(e: ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    if (!file) return
    if (!window.confirm(t('settings.restoreConfirm'))) {
      e.target.value = ''
      return
    }
    // Same cluster caveat as download — restoring a single-node backup
    // onto a cluster member desyncs replicated state.
    if (clusterEnabled && !window.confirm(t('settings.restoreClusterWarn'))) {
      e.target.value = ''
      return
    }
    setRestoreLoading(true)
    try {
      await api.restoreBackup(file)
      toast.success(t('settings.restoreSuccess'))
      // Poll until the panel comes back, but cap at 60 attempts (≈2 min)
      // so a corrupted DB doesn't leave the user staring at a spinner
      // forever with no error.
      setTimeout(() => {
        let attempts = 0
        const maxAttempts = 60
        const check = setInterval(() => {
          attempts++
          fetch(`${api.apiBase}/auth/setup-status`)
            .then(() => { clearInterval(check); window.location.reload() })
            .catch(() => {
              if (attempts >= maxAttempts) {
                clearInterval(check)
                toast.error(t('settings.restoreNoReturn'))
                setRestoreLoading(false)
              }
            })
        }, 2000)
      }, 3000)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('settings.restoreFailed')
      toast.error(message)
    } finally {
      e.target.value = ''
      // Don't clear restoreLoading here — the polling loop is still in
      // flight. The catch path inside the poll clears it on timeout;
      // success reloads the page so the state is moot.
    }
  }

  async function handleSaveSchedule() {
    const hours = Number(intervalHours)
    const keep = Number(retention)
    if (!Number.isInteger(hours) || hours < 1 || hours > 168) {
      toast.error(t('settings.backupSchedule.intervalInvalid'))
      return
    }
    if (!Number.isInteger(keep) || keep < 1 || keep > 100) {
      toast.error(t('settings.backupSchedule.retentionInvalid'))
      return
    }
    setSavingSchedule(true)
    try {
      await api.updateBackupSchedule({ enabled: scheduleEnabled, interval_hours: hours, retention: keep })
      toast.success(t('settings.backupSchedule.saved'))
      await loadBackupSchedule()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : String(err))
    } finally {
      setSavingSchedule(false)
    }
  }

  async function handleRunNow() {
    setRunningNow(true)
    try {
      const res = await api.runBackupNow()
      toast.success(t('settings.backupSchedule.runSuccess', { name: res.name }))
      await loadBackupSchedule()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : String(err))
    } finally {
      setRunningNow(false)
    }
  }

  async function handleDownloadBackupFile(name: string) {
    try {
      const blob = await api.downloadBackupFile(name)
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = name
      a.click()
      URL.revokeObjectURL(url)
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : String(err))
    }
  }

  async function handleDeleteBackupFile(name: string) {
    if (!window.confirm(t('settings.backupSchedule.deleteConfirm', { name }))) return
    try {
      await api.deleteBackupFile(name)
      toast.success(t('settings.backupSchedule.deleted'))
      await loadBackupSchedule()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <div className="space-y-6 mt-6">
      {/* Panel Update */}
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <h3 className="text-[15px] font-semibold">{t('settings.update')}</h3>
        <p className="text-[13px] text-muted-foreground mt-1 mb-4">{t('settings.updateDescription')}</p>

        <div className="flex items-center gap-6 mb-4">
          <div className="space-y-1">
            <p className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('settings.currentVersion')}</p>
            <p className="text-[13px] font-medium">v{panelVersion}</p>
          </div>
          {updateInfo?.update_available && (
            <div className="space-y-1">
              <p className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('settings.latestVersion')}</p>
              <p className="text-[13px] font-medium text-[#3182f6]">v{updateInfo.latest_version}</p>
            </div>
          )}
        </div>

        {updating ? (
          <div className="space-y-2">
            <div className="flex items-center gap-2">
              <RefreshCw className="h-4 w-4 animate-spin text-[#3182f6]" />
              <span className="text-[13px]">
                {updateStep && t(`settings.updateStep.${updateStep}`, { defaultValue: updateStep })}
              </span>
            </div>
            {updateError && (
              <div className="flex items-center gap-2 text-[#f04452]">
                <AlertCircle className="h-4 w-4" />
                <span className="text-[13px]">{updateError}</span>
              </div>
            )}
          </div>
        ) : (
          <div className="flex gap-2">
            <Button onClick={handleCheckUpdate} disabled={updateChecking} className="rounded-xl" variant="outline">
              {updateChecking ? t('settings.checking') : t('settings.checkForUpdates')}
            </Button>
            {updateInfo?.update_available && (
              <Button onClick={handleRunUpdate} className="rounded-xl">
                {t('settings.updateNow')}
              </Button>
            )}
          </div>
        )}

        {updateInfo?.update_available && updateInfo.release_notes && (
          <details className="mt-4">
            <summary className="text-[13px] font-medium cursor-pointer">{t('settings.releaseNotes')}</summary>
            <pre className="mt-2 text-[12px] text-muted-foreground whitespace-pre-wrap bg-secondary/50 rounded-xl p-3 max-h-48 overflow-auto">
              {updateInfo.release_notes}
            </pre>
          </details>
        )}
      </div>

      {/* Settings Backup */}
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <h3 className="text-[15px] font-semibold">{t('settings.backup')}</h3>
        <p className="text-[13px] text-muted-foreground mt-1 mb-4">{t('settings.backupDescription')}</p>

        <div className="bg-secondary/40 rounded-xl p-3 mb-4">
          <p className="text-[11px] text-muted-foreground uppercase tracking-wider mb-2">{t('settings.backupIncludes')}</p>
          <div className="flex flex-col gap-1.5">
            <div className="flex items-center gap-2">
              <span className="h-1.5 w-1.5 rounded-full bg-[#3182f6]" />
              <span className="text-[12px] text-foreground/80">sfpanel.db</span>
              <span className="text-[11px] text-muted-foreground">— {t('settings.backupItemDB')}</span>
            </div>
            <div className="flex items-center gap-2">
              <span className="h-1.5 w-1.5 rounded-full bg-[#3182f6]" />
              <span className="text-[12px] text-foreground/80">config.yaml</span>
              <span className="text-[11px] text-muted-foreground">— {t('settings.backupItemConfig')}</span>
            </div>
            <div className="flex items-center gap-2">
              <span className="h-1.5 w-1.5 rounded-full bg-[#3182f6]" />
              <span className="text-[12px] text-foreground/80">compose/*</span>
              <span className="text-[11px] text-muted-foreground">— {t('settings.backupItemCompose')}</span>
            </div>
          </div>
          <p className="text-[11px] text-muted-foreground mt-2 flex items-center gap-1">
            <AlertCircle className="h-3 w-3 shrink-0" />
            {t('settings.backupDockerDataNote')}
          </p>
        </div>

        <div className="flex flex-wrap gap-3">
          <Button onClick={handleDownloadBackup} disabled={backupLoading} variant="outline" className="rounded-xl">
            <Download className="h-4 w-4 mr-2" />
            {backupLoading ? t('settings.downloadingBackup') : t('settings.downloadBackup')}
          </Button>

          <Button
            variant="outline"
            className="rounded-xl"
            disabled={restoreLoading}
            onClick={() => document.getElementById('restore-file-input')?.click()}
          >
            <Upload className="h-4 w-4 mr-2" />
            {restoreLoading ? t('settings.restoring') : t('settings.restoreUpload')}
          </Button>
          <input
            id="restore-file-input"
            type="file"
            accept=".tar.gz,.tgz"
            onChange={handleRestoreBackup}
            className="hidden"
            disabled={restoreLoading}
          />
        </div>
      </div>

      {/* Scheduled Backups */}
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <h3 className="text-[15px] font-semibold">{t('settings.backupSchedule.title')}</h3>
        <p className="text-[13px] text-muted-foreground mt-1 mb-4">{t('settings.backupSchedule.description')}</p>

        <label className="flex items-center gap-2 mb-4 cursor-pointer">
          <input
            type="checkbox"
            checked={scheduleEnabled}
            onChange={(e) => setScheduleEnabled(e.target.checked)}
            className="h-4 w-4 rounded accent-[#3182f6]"
          />
          <span className="text-[13px] font-medium">{t('settings.backupSchedule.enable')}</span>
        </label>

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4 mb-4">
          <div className="space-y-1">
            <label className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('settings.backupSchedule.intervalLabel')}</label>
            <Input
              type="number"
              min={1}
              max={168}
              value={intervalHours}
              onChange={(e) => setIntervalHours(e.target.value)}
              className="rounded-xl"
            />
            <p className="text-[11px] text-muted-foreground">{t('settings.backupSchedule.intervalHint')}</p>
          </div>
          <div className="space-y-1">
            <label className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('settings.backupSchedule.retentionLabel')}</label>
            <Input
              type="number"
              min={1}
              max={100}
              value={retention}
              onChange={(e) => setRetention(e.target.value)}
              className="rounded-xl"
            />
            <p className="text-[11px] text-muted-foreground">{t('settings.backupSchedule.retentionHint')}</p>
          </div>
        </div>

        {schedule?.last_run && (
          <div className="bg-secondary/40 rounded-xl p-3 mb-4">
            <div className="flex items-center gap-2 mb-1">
              <Clock className="h-3.5 w-3.5 text-muted-foreground" />
              <span className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('settings.backupSchedule.lastRun')}</span>
              <span className="text-[12px] font-medium">{new Date(schedule.last_run).toLocaleString()}</span>
              <span className={`text-[12px] font-medium ${schedule.last_status === 'error' ? 'text-[#f04452]' : 'text-[#16a34a]'}`}>
                {schedule.last_status === 'error' ? t('settings.backupSchedule.statusError') : t('settings.backupSchedule.statusSuccess')}
              </span>
            </div>
            {schedule.last_status === 'error' && schedule.last_error && (
              <p className="text-[12px] text-[#f04452] mt-1">{schedule.last_error}</p>
            )}
          </div>
        )}

        <div className="flex flex-wrap gap-3 mb-6">
          <Button onClick={handleSaveSchedule} disabled={savingSchedule} className="rounded-xl">
            {savingSchedule ? t('settings.backupSchedule.saving') : t('settings.backupSchedule.save')}
          </Button>
          <Button onClick={handleRunNow} disabled={runningNow} variant="outline" className="rounded-xl">
            <Play className="h-4 w-4 mr-2" />
            {runningNow ? t('settings.backupSchedule.running') : t('settings.backupSchedule.runNow')}
          </Button>
        </div>

        <p className="text-[11px] text-muted-foreground uppercase tracking-wider mb-2">{t('settings.backupSchedule.existingTitle')}</p>
        {backupFiles.length === 0 ? (
          <p className="text-[13px] text-muted-foreground">{t('settings.backupSchedule.empty')}</p>
        ) : (
          <div className="flex flex-col gap-2">
            {backupFiles.map((file) => (
              <div key={file.name} className="flex items-center gap-3 bg-secondary/40 rounded-xl px-3 py-2">
                <div className="min-w-0 flex-1">
                  <p className="text-[13px] font-medium truncate">{file.name}</p>
                  <p className="text-[11px] text-muted-foreground">
                    {formatBytes(file.size)} · {new Date(file.mod_time).toLocaleString()}
                  </p>
                </div>
                <Button
                  onClick={() => handleDownloadBackupFile(file.name)}
                  variant="outline"
                  size="sm"
                  className="rounded-xl shrink-0"
                >
                  <Download className="h-4 w-4 mr-1" />
                  {t('settings.backupSchedule.download')}
                </Button>
                <Button
                  onClick={() => handleDeleteBackupFile(file.name)}
                  variant="outline"
                  size="sm"
                  className="rounded-xl shrink-0 text-[#f04452] hover:text-[#f04452]"
                >
                  <Trash2 className="h-4 w-4 mr-1" />
                  {t('settings.backupSchedule.delete')}
                </Button>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* System Info */}
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <h3 className="text-[15px] font-semibold">{t('settings.systemInfo')}</h3>
        <p className="text-[13px] text-muted-foreground mt-1 mb-4">{t('settings.systemInfoDescription')}</p>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div className="space-y-1">
            <p className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('settings.version')}</p>
            <p className="text-[13px] font-medium">v{panelVersion}</p>
          </div>
          {systemInfo?.host && (
            <>
              <div className="space-y-1">
                <p className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('dashboard.hostname')}</p>
                <p className="text-[13px] font-medium">{systemInfo.host.hostname || 'N/A'}</p>
              </div>
              <div className="space-y-1">
                <p className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('settings.operatingSystem')}</p>
                <p className="text-[13px] font-medium">
                  {systemInfo.host.platform || systemInfo.host.os || 'N/A'}
                  {systemInfo.host.platform_version ? ` ${systemInfo.host.platform_version}` : ''}
                </p>
              </div>
              <div className="space-y-1">
                <p className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('dashboard.kernel')}</p>
                <p className="text-[13px] font-medium">{systemInfo.host.kernel || 'N/A'}</p>
              </div>
              <div className="space-y-1">
                <p className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('dashboard.uptime')}</p>
                <p className="text-[13px] font-medium">
                  {systemInfo.host.uptime
                    ? formatUptime(systemInfo.host.uptime)
                    : 'N/A'}
                </p>
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
