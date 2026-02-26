import { useState, useEffect, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

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

  // System info state
  const [systemInfo, setSystemInfo] = useState<any>(null)

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

  async function loadSystemInfo() {
    try {
      const data = await api.getSystemInfo()
      setSystemInfo(data)
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

      {/* System Info */}
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <h3 className="text-[15px] font-semibold">{t('settings.systemInfo')}</h3>
        <p className="text-[13px] text-muted-foreground mt-1 mb-4">{t('settings.systemInfoDescription')}</p>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div className="space-y-1">
            <p className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('settings.version')}</p>
            <p className="text-[13px] font-medium">v0.1.0</p>
          </div>
          {systemInfo?.host && (
            <>
              <div className="space-y-1">
                <p className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('dashboard.hostname')}</p>
                <p className="text-[13px] font-medium">{systemInfo.host.hostname || 'N/A'}</p>
              </div>
              <div className="space-y-1">
                <p className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('settings.operatingSystem')}</p>
                <p className="text-[13px] font-medium">{systemInfo.host.os || 'N/A'} {systemInfo.host.platform || ''}</p>
              </div>
              <div className="space-y-1">
                <p className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('dashboard.kernel')}</p>
                <p className="text-[13px] font-medium">{systemInfo.host.kernel_version || 'N/A'}</p>
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
