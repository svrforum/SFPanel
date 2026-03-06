import { useState, useEffect, type FormEvent, type ChangeEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Download, Upload, RefreshCw, AlertCircle } from 'lucide-react'

export default function Settings() {
  const { t, i18n } = useTranslation()

  // Password change state
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [passwordLoading, setPasswordLoading] = useState(false)

  // 2FA state
  const [twoFAEnabled, setTwoFAEnabled] = useState(false)
  const [twoFASecret, setTwoFASecret] = useState('')
  const [twoFAUrl, setTwoFAUrl] = useState('')
  const [twoFACode, setTwoFACode] = useState('')
  const [showTwoFASetup, setShowTwoFASetup] = useState(false)
  const [twoFALoading, setTwoFALoading] = useState(false)

  // Terminal timeout state
  const [terminalTimeout, setTerminalTimeout] = useState('30')
  const [terminalTimeoutLoading, setTerminalTimeoutLoading] = useState(false)

  // Max upload size state
  const [maxUploadSize, setMaxUploadSize] = useState('1024')
  const [maxUploadSizeLoading, setMaxUploadSizeLoading] = useState(false)

  // System info state
  const [systemInfo, setSystemInfo] = useState<any>(null)
  const [panelVersion, setPanelVersion] = useState('...')

  // Update state
  const [updateChecking, setUpdateChecking] = useState(false)
  const [updateInfo, setUpdateInfo] = useState<{ latest_version: string; update_available: boolean; release_notes: string } | null>(null)
  const [updating, setUpdating] = useState(false)
  const [updateStep, setUpdateStep] = useState('')
  const [updateError, setUpdateError] = useState('')

  // Backup state
  const [backupLoading, setBackupLoading] = useState(false)
  const [restoreLoading, setRestoreLoading] = useState(false)

  useEffect(() => {
    loadSystemInfo()
    loadSettings()
  }, [])

  async function loadSettings() {
    try {
      const data = await api.getSettings()
      if (data.terminal_timeout !== undefined) {
        setTerminalTimeout(data.terminal_timeout)
      }
      if (data.max_upload_size !== undefined) {
        setMaxUploadSize(data.max_upload_size)
      }
    } catch {
      // ignore
    }
  }

  async function handleSaveTerminalTimeout(e: FormEvent) {
    e.preventDefault()
    const val = parseInt(terminalTimeout, 10)
    if (isNaN(val) || val < 0) {
      toast.error(t('settings.invalidTimeout'))
      return
    }
    setTerminalTimeoutLoading(true)
    try {
      await api.updateSettings({ terminal_timeout: String(val) })
      toast.success(t('settings.settingsSaved'))
    } catch (err: any) {
      toast.error(err.message || t('settings.settingsSaveFailed'))
    } finally {
      setTerminalTimeoutLoading(false)
    }
  }

  async function handleSaveMaxUploadSize(e: FormEvent) {
    e.preventDefault()
    const val = parseInt(maxUploadSize, 10)
    if (isNaN(val) || val < 1) {
      toast.error(t('settings.invalidMaxUploadSize'))
      return
    }
    setMaxUploadSizeLoading(true)
    try {
      await api.updateSettings({ max_upload_size: String(val) })
      toast.success(t('settings.settingsSaved'))
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('settings.settingsSaveFailed')
      toast.error(message)
    } finally {
      setMaxUploadSizeLoading(false)
    }
  }

  async function loadSystemInfo() {
    try {
      const data = await api.getSystemInfo()
      setSystemInfo(data)
      if (data.version) {
        setPanelVersion(`v${data.version}`)
      }
    } catch {
      // ignore
    }
  }

  async function handleChangePassword(e: FormEvent) {
    e.preventDefault()

    if (newPassword !== confirmPassword) {
      toast.error(t('settings.passwordMismatch'))
      return
    }

    if (newPassword.length < 8) {
      toast.error(t('settings.passwordMinLength'))
      return
    }

    setPasswordLoading(true)
    try {
      await api.changePassword(currentPassword, newPassword)
      toast.success(t('settings.passwordChanged'))
      setCurrentPassword('')
      setNewPassword('')
      setConfirmPassword('')
    } catch (err: any) {
      toast.error(err.message || t('settings.passwordChangeFailed'))
    } finally {
      setPasswordLoading(false)
    }
  }

  async function handleSetup2FA() {
    setTwoFALoading(true)
    try {
      const data = await api.setup2FA()
      setTwoFASecret(data.secret)
      setTwoFAUrl(data.url)
      setShowTwoFASetup(true)
    } catch (err: any) {
      toast.error(err.message || t('settings.twoFASetupFailed'))
    } finally {
      setTwoFALoading(false)
    }
  }

  async function handleVerify2FA(e: FormEvent) {
    e.preventDefault()
    if (!twoFACode || twoFACode.length !== 6) {
      toast.error(t('settings.invalidCode'))
      return
    }

    setTwoFALoading(true)
    try {
      await api.verify2FA(twoFASecret, twoFACode)
      toast.success(t('settings.twoFASuccess'))
      setTwoFAEnabled(true)
      setShowTwoFASetup(false)
      setTwoFACode('')
      setTwoFASecret('')
      setTwoFAUrl('')
    } catch (err: any) {
      toast.error(err.message || t('settings.twoFACodeInvalid'))
    } finally {
      setTwoFALoading(false)
    }
  }

  function getTwoFAButtonLabel(): string {
    if (twoFALoading) return t('settings.settingUp')
    if (twoFAEnabled) return t('settings.reconfigure2FA')
    return t('settings.enable2FA')
  }

  async function handleCheckUpdate() {
    setUpdateChecking(true)
    setUpdateError('')
    try {
      const data = await api.checkUpdate()
      setUpdateInfo(data)
      if (!data.update_available) {
        toast.success(t('settings.upToDate'))
      }
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : 'Failed'
      toast.error(message)
    } finally {
      setUpdateChecking(false)
    }
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
              fetch('/api/v1/auth/setup-status')
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
    setRestoreLoading(true)
    try {
      await api.restoreBackup(file)
      toast.success(t('settings.restoreSuccess'))
      setTimeout(() => {
        const check = setInterval(() => {
          fetch('/api/v1/auth/setup-status')
            .then(() => { clearInterval(check); window.location.reload() })
            .catch(() => {})
        }, 2000)
      }, 3000)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('settings.restoreFailed')
      toast.error(message)
    } finally {
      setRestoreLoading(false)
      e.target.value = ''
    }
  }

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-[22px] font-bold tracking-tight">{t('settings.title')}</h1>
        <p className="text-[13px] text-muted-foreground mt-1">{t('settings.subtitle')}</p>
      </div>

      {/* Language */}
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <h3 className="text-[15px] font-semibold">{t('settings.language')}</h3>
        <p className="text-[13px] text-muted-foreground mt-1 mb-4">{t('settings.languageDescription')}</p>
        <div className="flex items-center gap-2">
          <Button
            variant={i18n.language === 'en' ? 'default' : 'outline'}
            size="sm"
            onClick={() => i18n.changeLanguage('en')}
            className="rounded-xl"
          >
            English
          </Button>
          <Button
            variant={i18n.language?.startsWith('ko') ? 'default' : 'outline'}
            size="sm"
            onClick={() => i18n.changeLanguage('ko')}
            className="rounded-xl"
          >
            한국어
          </Button>
        </div>
      </div>

      {/* Terminal */}
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <h3 className="text-[15px] font-semibold">{t('settings.terminal')}</h3>
        <p className="text-[13px] text-muted-foreground mt-1 mb-4">{t('settings.terminalDescription')}</p>
        <form onSubmit={handleSaveTerminalTimeout} className="space-y-4 max-w-md">
          <div className="space-y-2">
            <Label htmlFor="terminal-timeout" className="text-[13px]">{t('settings.terminalTimeout')}</Label>
            <div className="flex items-center gap-2">
              <Input
                id="terminal-timeout"
                type="number"
                min="0"
                value={terminalTimeout}
                onChange={(e) => setTerminalTimeout(e.target.value)}
                className="w-24 rounded-xl"
              />
              <span className="text-[13px] text-muted-foreground">{t('settings.minutes')}</span>
            </div>
            <p className="text-[11px] text-muted-foreground">{t('settings.terminalTimeoutHint')}</p>
          </div>
          <Button type="submit" disabled={terminalTimeoutLoading} className="rounded-xl">
            {terminalTimeoutLoading ? t('common.saving') : t('common.save')}
          </Button>
        </form>
      </div>

      {/* File Upload */}
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <h3 className="text-[15px] font-semibold">{t('settings.fileUpload')}</h3>
        <p className="text-[13px] text-muted-foreground mt-1 mb-4">{t('settings.fileUploadDescription')}</p>
        <form onSubmit={handleSaveMaxUploadSize} className="space-y-4 max-w-md">
          <div className="space-y-2">
            <Label htmlFor="max-upload-size" className="text-[13px]">{t('settings.maxUploadSize')}</Label>
            <div className="flex items-center gap-2">
              <Input
                id="max-upload-size"
                type="number"
                min="1"
                value={maxUploadSize}
                onChange={(e) => setMaxUploadSize(e.target.value)}
                className="w-24 rounded-xl"
              />
              <span className="text-[13px] text-muted-foreground">MB</span>
            </div>
            <p className="text-[11px] text-muted-foreground">{t('settings.maxUploadSizeHint')}</p>
          </div>
          <Button type="submit" disabled={maxUploadSizeLoading} className="rounded-xl">
            {maxUploadSizeLoading ? t('common.saving') : t('common.save')}
          </Button>
        </form>
      </div>

      {/* Change Password */}
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <h3 className="text-[15px] font-semibold">{t('settings.changePassword')}</h3>
        <p className="text-[13px] text-muted-foreground mt-1 mb-4">{t('settings.changePasswordDescription')}</p>
        <form onSubmit={handleChangePassword} className="space-y-4 max-w-md">
          <div className="space-y-2">
            <Label htmlFor="current-password" className="text-[13px]">{t('settings.currentPassword')}</Label>
            <Input
              id="current-password"
              type="password"
              value={currentPassword}
              onChange={(e) => setCurrentPassword(e.target.value)}
              placeholder={t('settings.currentPasswordPlaceholder')}
              required
              className="rounded-xl"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="new-password" className="text-[13px]">{t('settings.newPassword')}</Label>
            <Input
              id="new-password"
              type="password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              placeholder={t('settings.newPasswordPlaceholder')}
              required
              minLength={8}
              className="rounded-xl"
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="confirm-password" className="text-[13px]">{t('settings.confirmNewPassword')}</Label>
            <Input
              id="confirm-password"
              type="password"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              placeholder={t('settings.confirmNewPasswordPlaceholder')}
              required
              minLength={8}
              className="rounded-xl"
            />
          </div>
          <Button type="submit" disabled={passwordLoading} className="rounded-xl">
            {passwordLoading ? t('settings.changing') : t('settings.changePassword')}
          </Button>
        </form>
      </div>

      {/* 2FA Management */}
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <div className="flex items-center gap-3">
          <h3 className="text-[15px] font-semibold">{t('settings.twoFA')}</h3>
          {twoFAEnabled ? (
            <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#00c471]/10 text-[#00c471]">{t('settings.twoFAEnabled')}</span>
          ) : (
            <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-secondary text-muted-foreground">{t('settings.twoFANotConfigured')}</span>
          )}
        </div>
        <p className="text-[13px] text-muted-foreground mt-1 mb-4">{t('settings.twoFADescription')}</p>
        {!showTwoFASetup ? (
          <Button onClick={handleSetup2FA} disabled={twoFALoading} className="rounded-xl">
            {getTwoFAButtonLabel()}
          </Button>
        ) : (
          <div className="space-y-4 max-w-md">
            <div className="bg-secondary/30 p-4 rounded-xl space-y-3">
              <p className="text-[13px] font-medium">{t('settings.scanQR')}</p>
              <div className="bg-white p-4 rounded-xl inline-block">
                <img
                  src={`https://api.qrserver.com/v1/create-qr-code/?size=200x200&data=${encodeURIComponent(twoFAUrl)}`}
                  alt="2FA QR Code"
                  width={200}
                  height={200}
                />
              </div>
              <div className="space-y-1">
                <Label className="text-[11px] text-muted-foreground">{t('settings.secretKey')}</Label>
                <code className="block bg-background px-3 py-2 rounded-lg text-[13px] font-mono break-all select-all">
                  {twoFASecret}
                </code>
              </div>
            </div>
            <form onSubmit={handleVerify2FA} className="space-y-3">
              <div className="space-y-2">
                <Label htmlFor="totp-code" className="text-[13px]">{t('settings.verificationCode')}</Label>
                <Input
                  id="totp-code"
                  type="text"
                  value={twoFACode}
                  onChange={(e) => setTwoFACode(e.target.value.replace(/\D/g, '').slice(0, 6))}
                  placeholder={t('settings.verificationPlaceholder')}
                  maxLength={6}
                  required
                  autoFocus
                  className="rounded-xl text-center text-lg tracking-[0.3em]"
                />
              </div>
              <div className="flex gap-2">
                <Button type="submit" disabled={twoFALoading} className="rounded-xl">
                  {twoFALoading ? t('settings.verifying') : t('settings.verifyAndEnable')}
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => {
                    setShowTwoFASetup(false)
                    setTwoFACode('')
                  }}
                  className="rounded-xl"
                >
                  {t('common.cancel')}
                </Button>
              </div>
            </form>
          </div>
        )}
      </div>

      {/* Panel Update */}
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <h3 className="text-[15px] font-semibold">{t('settings.update')}</h3>
        <p className="text-[13px] text-muted-foreground mt-1 mb-4">{t('settings.updateDescription')}</p>

        <div className="flex items-center gap-6 mb-4">
          <div className="space-y-1">
            <p className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('settings.currentVersion')}</p>
            <p className="text-[13px] font-medium">{panelVersion}</p>
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
            <p className="text-[13px] font-medium">{panelVersion}</p>
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
                    ? `${Math.floor(systemInfo.host.uptime / 3600)}h ${Math.floor((systemInfo.host.uptime % 3600) / 60)}m`
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
