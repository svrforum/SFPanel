import { useState, useEffect, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { QRCodeSVG } from 'qrcode.react'
import { useApiAction } from '@/hooks/useApiAction'

export default function Security() {
  const { t } = useTranslation()

  // Password change state
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')

  // 2FA state
  const [twoFAEnabled, setTwoFAEnabled] = useState(false)
  const [twoFASecret, setTwoFASecret] = useState('')
  const [twoFAUrl, setTwoFAUrl] = useState('')
  const [twoFACode, setTwoFACode] = useState('')
  const [showTwoFASetup, setShowTwoFASetup] = useState(false)
  const [verifyLoading, setVerifyLoading] = useState(false)

  useEffect(() => {
    api.get2FAStatus()
      .then((data) => setTwoFAEnabled(data.enabled))
      .catch(() => { /* ignore */ })
  }, [])

  // ---- handlers that fit the useApiAction shape ----

  const { run: runChangePassword, loading: passwordLoading } = useApiAction(
    api.changePassword.bind(api),
    {
      successMsg: t('settings.passwordChanged'),
      errorMsg: t('settings.passwordChangeFailed'),
      onSuccess: () => {
        setCurrentPassword('')
        setNewPassword('')
        setConfirmPassword('')
      },
    },
  )

  const { run: runSetup2FA, loading: setupLoading } = useApiAction(
    api.setup2FA.bind(api),
    {
      errorMsg: t('settings.twoFASetupFailed'),
      onSuccess: (data) => {
        setTwoFASecret(data.secret)
        setTwoFAUrl(data.url)
        setShowTwoFASetup(true)
      },
    },
  )

  const { run: runDisable2FA, loading: disableLoading } = useApiAction(
    api.disable2FA.bind(api),
    {
      successMsg: t('settings.twoFADisabled'),
      errorMsg: t('settings.twoFADisableFailed'),
      onSuccess: () => setTwoFAEnabled(false),
    },
  )

  const twoFALoading = setupLoading || disableLoading || verifyLoading

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
    await runChangePassword(currentPassword, newPassword)
  }

  async function handleDisable2FA() {
    // Confirm + collect password — destructive action that loosens auth.
    if (!window.confirm(t('settings.confirmDisable2FA'))) return
    const password = window.prompt(t('settings.disable2FAPasswordPrompt'))
    if (!password) return
    await runDisable2FA(password)
  }

  async function handleVerify2FA(e: FormEvent) {
    e.preventDefault()
    if (!twoFACode || twoFACode.length !== 6) {
      toast.error(t('settings.invalidCode'))
      return
    }
    // Bespoke flow — clears multiple pieces of state in lockstep with the
    // success path; keep imperative rather than useApiAction.
    setVerifyLoading(true)
    try {
      await api.verify2FA(twoFASecret, twoFACode)
      toast.success(t('settings.twoFASuccess'))
      setTwoFAEnabled(true)
      setShowTwoFASetup(false)
      setTwoFACode('')
      setTwoFASecret('')
      setTwoFAUrl('')
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('settings.twoFACodeInvalid')
      toast.error(message)
    } finally {
      setVerifyLoading(false)
    }
  }

  function getTwoFAButtonLabel(): string {
    if (twoFALoading) return t('settings.settingUp')
    if (twoFAEnabled) return t('settings.reconfigure2FA')
    return t('settings.enable2FA')
  }

  return (
    <div className="space-y-6 mt-6">
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
          <div className="flex flex-wrap gap-2">
            <Button onClick={() => runSetup2FA()} disabled={twoFALoading} className="rounded-xl">
              {getTwoFAButtonLabel()}
            </Button>
            {twoFAEnabled && (
              <Button
                variant="destructive"
                onClick={handleDisable2FA}
                disabled={twoFALoading}
                className="rounded-xl"
              >
                {t('settings.disable2FA')}
              </Button>
            )}
          </div>
        ) : (
          <div className="space-y-4 max-w-md">
            <div className="bg-secondary/30 p-4 rounded-xl space-y-3">
              <p className="text-[13px] font-medium">{t('settings.scanQR')}</p>
              <div className="bg-white p-4 rounded-xl inline-block">
                <QRCodeSVG value={twoFAUrl} size={200} />
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
    </div>
  )
}
