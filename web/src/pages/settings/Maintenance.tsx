import { useState, useEffect, type ChangeEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { formatUptime } from '@/lib/utils'
import type { HostInfo } from '@/types/api'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Download, Upload, RefreshCw, AlertCircle } from 'lucide-react'
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
