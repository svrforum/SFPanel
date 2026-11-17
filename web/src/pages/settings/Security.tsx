import { useState, useEffect, useCallback, type FormEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { useConfirm } from '@/components/ConfirmDialog'
import { ListLoadState } from '@/components/ListLoadState'
import { toast } from 'sonner'
import { copyText, downloadBlob } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { QRCodeSVG } from 'qrcode.react'
import { Copy, Download } from 'lucide-react'
import { useApiAction } from '@/hooks/useApiAction'

export default function Security() {
  const { t } = useTranslation()
  const confirm = useConfirm()

  // Password change state
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  // A check that failed, shown under its field (it was a toast that left the
  // field unmarked and went away).
  const [passwordError, setPasswordError] = useState<{ field: 'new-password' | 'confirm-password'; message: string } | null>(null)

  // 2FA state. null until the server answers: a failed read used to show
  // "Not configured" and an Enable button for an account that has 2FA on.
  const [twoFAEnabled, setTwoFAEnabled] = useState<boolean | null>(null)
  const [twoFAStatusError, setTwoFAStatusError] = useState<string | null>(null)
  const [twoFASecret, setTwoFASecret] = useState('')
  const [twoFAUrl, setTwoFAUrl] = useState('')
  const [twoFACode, setTwoFACode] = useState('')
  const [codeError, setCodeError] = useState('')
  const [showTwoFASetup, setShowTwoFASetup] = useState(false)
  const [verifyLoading, setVerifyLoading] = useState(false)

  // Turning 2FA off: one dialog with both fields the server requires, in
  // place of a confirmation and two prompts in a row.
  const [disableOpen, setDisableOpen] = useState(false)
  const [disablePassword, setDisablePassword] = useState('')
  const [disableCode, setDisableCode] = useState('')
  const [disableCodeError, setDisableCodeError] = useState('')

  // Recovery codes state
  const [recoveryGenerated, setRecoveryGenerated] = useState(false)
  const [recoveryRemaining, setRecoveryRemaining] = useState(0)
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>([])
  const [recoveryLoading, setRecoveryLoading] = useState(false)

  const loadTwoFAStatus = useCallback(() => {
    setTwoFAStatusError(null)
    api.get2FAStatus()
      .then((data) => setTwoFAEnabled(data.enabled))
      .catch((err: unknown) => setTwoFAStatusError(err instanceof Error ? err.message : String(err)))
  }, [])

  useEffect(() => {
    loadTwoFAStatus()
  }, [loadTwoFAStatus])

  function refreshRecoveryStatus() {
    api.get2FARecoveryStatus()
      .then((data) => {
        setRecoveryGenerated(data.generated)
        setRecoveryRemaining(data.remaining)
      })
      .catch(() => { /* ignore */ })
  }

  useEffect(() => {
    if (!twoFAEnabled) {
      setRecoveryGenerated(false)
      setRecoveryRemaining(0)
      setRecoveryCodes([])
      return
    }
    refreshRecoveryStatus()
  }, [twoFAEnabled])

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
      onSuccess: () => {
        setTwoFAEnabled(false)
        setDisableOpen(false)
        setDisablePassword('')
        setDisableCode('')
      },
    },
  )

  const twoFALoading = setupLoading || disableLoading || verifyLoading

  async function handleChangePassword(e: FormEvent) {
    e.preventDefault()
    // In field order, so the first one wrong is the one that gets focus.
    const failure = newPassword.length < 12
      ? { field: 'new-password' as const, message: t('settings.passwordMinLength') }
      : newPassword !== confirmPassword
        ? { field: 'confirm-password' as const, message: t('settings.passwordMismatch') }
        : null
    setPasswordError(failure)
    if (failure) {
      document.getElementById(failure.field)?.focus()
      return
    }
    await runChangePassword(currentPassword, newPassword)
  }

  // The backend requires the password AND a current 2FA code to disable (so a
  // session-only attacker with the password but no authenticator can't
  // downgrade the account).
  async function handleDisable2FA(e: FormEvent) {
    e.preventDefault()
    if (disableCode.length !== 6) {
      setDisableCodeError(t('settings.invalidCode'))
      document.getElementById('disable-2fa-code')?.focus()
      return
    }
    setDisableCodeError('')
    await runDisable2FA(disablePassword, disableCode)
  }

  async function handleVerify2FA(e: FormEvent) {
    e.preventDefault()
    if (!twoFACode || twoFACode.length !== 6) {
      setCodeError(t('settings.invalidCode'))
      document.getElementById('totp-code')?.focus()
      return
    }
    setCodeError('')
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

  async function handleGenerateRecoveryCodes() {
    // Regenerating invalidates any previous set — confirm when codes exist.
    if (recoveryGenerated) {
      if (!(await confirm({
        title: t('settings.recoveryCodes.regenerateConfirm'),
        description: t('settings.recoveryCodes.regenerateConfirmDesc'),
        confirmLabel: t('settings.recoveryCodes.regenerate'),
        danger: true,
      }))) return
    }
    setRecoveryLoading(true)
    try {
      const { codes } = await api.regenerate2FARecoveryCodes()
      setRecoveryCodes(codes)
      refreshRecoveryStatus()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('settings.recoveryCodes.generateFailed')
      toast.error(message)
    } finally {
      setRecoveryLoading(false)
    }
  }

  async function handleCopyRecoveryCodes() {
    if (await copyText(recoveryCodes.join('\n'))) {
      toast.success(t('settings.recoveryCodes.copied'))
    } else {
      toast.error(t('settings.recoveryCodes.copyFailed'))
    }
  }

  function handleDownloadRecoveryCodes() {
    const blob = new Blob([recoveryCodes.join('\n')], { type: 'text/plain' })
    downloadBlob(blob, 'sfpanel-recovery-codes.txt')
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
              autoComplete="current-password"
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
              autoComplete="new-password"
              aria-invalid={passwordError?.field === 'new-password' || undefined}
              aria-describedby={passwordError?.field === 'new-password' ? 'new-password-hint new-password-error' : 'new-password-hint'}
              required
              className="rounded-xl"
            />
            {/* Under the field, where it stays while typing; it was a
                placeholder that said 8 when the check is 12. */}
            <p id="new-password-hint" className="text-[12px] text-muted-foreground">{t('settings.newPasswordHint')}</p>
            {passwordError?.field === 'new-password' && <p id="new-password-error" className="text-[12px] text-destructive">{passwordError.message}</p>}
          </div>
          <div className="space-y-2">
            <Label htmlFor="confirm-password" className="text-[13px]">{t('settings.confirmNewPassword')}</Label>
            <Input
              id="confirm-password"
              type="password"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              placeholder={t('settings.confirmNewPasswordPlaceholder')}
              autoComplete="new-password"
              aria-invalid={passwordError?.field === 'confirm-password' || undefined}
              aria-describedby={passwordError?.field === 'confirm-password' ? 'confirm-password-error' : undefined}
              required
              className="rounded-xl"
            />
            {passwordError?.field === 'confirm-password' && <p id="confirm-password-error" className="text-[12px] text-destructive">{passwordError.message}</p>}
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
          {twoFAEnabled === true ? (
            <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[12px] font-medium bg-success/10 text-success">{t('settings.twoFAEnabled')}</span>
          ) : twoFAEnabled === false ? (
            <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[12px] font-medium bg-secondary text-muted-foreground">{t('settings.twoFANotConfigured')}</span>
          ) : null}
        </div>
        <p className="text-[13px] text-muted-foreground mt-1 mb-4">{t('settings.twoFADescription')}</p>
        {twoFAEnabled === null ? (
          <ListLoadState
            loading={!twoFAStatusError}
            error={twoFAStatusError}
            errorTitle={t('settings.twoFALoadFailed')}
            onRetry={loadTwoFAStatus}
            skeletonCount={1}
          />
        ) : !showTwoFASetup ? (
          <div className="flex flex-wrap gap-2">
            <Button onClick={() => runSetup2FA()} disabled={twoFALoading} className="rounded-xl">
              {getTwoFAButtonLabel()}
            </Button>
            {twoFAEnabled && (
              <Button
                variant="destructive"
                onClick={() => { setDisableCodeError(''); setDisableOpen(true) }}
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
                <Label className="text-[12px] text-muted-foreground">{t('settings.secretKey')}</Label>
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
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  aria-invalid={!!codeError || undefined}
                  aria-describedby={codeError ? 'totp-code-error' : undefined}
                  required
                  autoFocus
                  className="rounded-xl text-center text-lg tracking-[0.3em]"
                />
                {codeError && <p id="totp-code-error" className="text-[12px] text-destructive">{codeError}</p>}
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

      <Dialog open={disableOpen} onOpenChange={(open) => { if (!disableLoading) setDisableOpen(open) }}>
        <DialogContent>
          <form onSubmit={handleDisable2FA} className="grid gap-4">
            <DialogHeader>
              <DialogTitle className="text-destructive">{t('settings.disable2FA')}</DialogTitle>
              <DialogDescription>{t('settings.confirmDisable2FA')}</DialogDescription>
            </DialogHeader>
            <div className="space-y-2">
              <Label htmlFor="disable-2fa-password" className="text-[13px]">{t('settings.currentPassword')}</Label>
              <Input
                id="disable-2fa-password"
                type="password"
                value={disablePassword}
                onChange={(e) => setDisablePassword(e.target.value)}
                autoComplete="current-password"
                required
                autoFocus
                className="rounded-xl"
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="disable-2fa-code" className="text-[13px]">{t('settings.verificationCode')}</Label>
              <Input
                id="disable-2fa-code"
                type="text"
                value={disableCode}
                onChange={(e) => setDisableCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
                placeholder={t('settings.verificationPlaceholder')}
                maxLength={6}
                inputMode="numeric"
                autoComplete="one-time-code"
                aria-invalid={!!disableCodeError || undefined}
                aria-describedby={disableCodeError ? 'disable-2fa-code-error' : undefined}
                required
                className="rounded-xl"
              />
              {disableCodeError && <p id="disable-2fa-code-error" className="text-[12px] text-destructive">{disableCodeError}</p>}
            </div>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setDisableOpen(false)} disabled={disableLoading}>
                {t('common.cancel')}
              </Button>
              <Button type="submit" variant="destructive" loading={disableLoading}>
                {t('settings.disable2FA')}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      {/* Recovery Codes */}
      {twoFAEnabled && (
        <div className="bg-card rounded-2xl p-6 card-shadow">
          <h3 className="text-[15px] font-semibold">{t('settings.recoveryCodes.title')}</h3>
          <p className="text-[13px] text-muted-foreground mt-1 mb-4">{t('settings.recoveryCodes.description')}</p>

          {recoveryGenerated ? (
            recoveryRemaining > 0 ? (
              <p className="text-[13px] mb-4">{t('settings.recoveryCodes.remaining', { count: recoveryRemaining })}</p>
            ) : (
              <div className="bg-destructive/8 text-destructive text-[13px] p-3 rounded-xl mb-4 font-medium">
                {t('settings.recoveryCodes.allUsed')}
              </div>
            )
          ) : (
            <p className="text-[13px] text-muted-foreground mb-4">{t('settings.recoveryCodes.notGenerated')}</p>
          )}

          {recoveryCodes.length > 0 && (
            <div className="space-y-3 max-w-md mb-4">
              <div className="bg-warning/10 text-warning text-[13px] p-3 rounded-xl font-medium">
                {t('settings.recoveryCodes.oneTimeWarning')}
              </div>
              <div className="grid grid-cols-2 gap-2 bg-secondary/30 p-4 rounded-xl">
                {recoveryCodes.map((code) => (
                  <code key={code} className="block bg-background px-3 py-2 rounded-lg text-[13px] font-mono select-all text-center">
                    {code}
                  </code>
                ))}
              </div>
              <div className="flex gap-2">
                <Button type="button" variant="outline" onClick={handleCopyRecoveryCodes} className="rounded-xl">
                  <Copy className="h-4 w-4 mr-1.5" />
                  {t('settings.recoveryCodes.copyAll')}
                </Button>
                <Button type="button" variant="outline" onClick={handleDownloadRecoveryCodes} className="rounded-xl">
                  <Download className="h-4 w-4 mr-1.5" />
                  {t('settings.recoveryCodes.download')}
                </Button>
              </div>
            </div>
          )}

          <Button onClick={handleGenerateRecoveryCodes} disabled={recoveryLoading} className="rounded-xl">
            {recoveryLoading
              ? t('settings.recoveryCodes.generating')
              : recoveryGenerated
                ? t('settings.recoveryCodes.regenerate')
                : t('settings.recoveryCodes.generate')}
          </Button>
        </div>
      )}
    </div>
  )
}
