import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import AuthShell, { AuthErrorBanner, AUTH_INPUT_CLASS, AUTH_SUBMIT_CLASS } from '@/components/AuthShell'

export default function Login() {
  const navigate = useNavigate()
  const { t } = useTranslation()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [totpCode, setTotpCode] = useState('')
  const [showTotp, setShowTotp] = useState(false)
  const [useRecovery, setUseRecovery] = useState(false)
  const [recoveryCode, setRecoveryCode] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)

    try {
      const result = showTotp && useRecovery
        ? await api.login(username, password, undefined, recoveryCode)
        : await api.login(username, password, showTotp ? totpCode : undefined)
      api.setTokenPair(result.token, result.refresh_token ?? null)
      navigate('/dashboard')
    } catch (err: unknown) {
      const code = err instanceof Error ? (err as Error & { code?: string }).code : undefined
      const message = err instanceof Error ? err.message : 'Login failed'
      // Prefer the structured err.code (repo convention: error code + frontend
      // translation). Message sniffing stays only as a fallback for servers
      // that predate the code contract.
      if (code === 'TOTP_REQUIRED' || (!code && /totp|2fa/i.test(message))) {
        setShowTotp(true)
        setError(t('login.totpRequired'))
      } else if (code === 'INVALID_TOTP') {
        setError(t(useRecovery ? 'login.invalidRecoveryCode' : 'login.invalidTotp'))
      } else if (code === 'INVALID_CREDENTIALS') {
        setError(t('login.invalidCredentials'))
      } else if (code === 'RATE_LIMITED') {
        setError(t('login.rateLimited'))
      } else {
        setError(message)
      }
    } finally {
      setLoading(false)
    }
  }

  return (
    <AuthShell subtitle={t('login.subtitle')}>
      <form onSubmit={handleSubmit} className="space-y-5">
        <AuthErrorBanner error={error} />

        <div className="space-y-2">
          <Label htmlFor="username" className="text-xs font-medium text-muted-foreground">{t('login.username')}</Label>
          <Input
            id="username"
            type="text"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            placeholder="admin"
            required
            autoFocus
            className={AUTH_INPUT_CLASS}
          />
        </div>

        <div className="space-y-2">
          <Label htmlFor="password" className="text-xs font-medium text-muted-foreground">{t('login.password')}</Label>
          <Input
            id="password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder={t('login.passwordPlaceholder')}
            required
            className={AUTH_INPUT_CLASS}
          />
        </div>

        {showTotp && (
          <div className="space-y-2">
            {useRecovery ? (
              <>
                <Label htmlFor="recovery" className="text-xs font-medium text-muted-foreground">{t('login.recoveryCode')}</Label>
                <Input
                  id="recovery"
                  type="text"
                  value={recoveryCode}
                  onChange={(e) => setRecoveryCode(e.target.value)}
                  placeholder={t('login.recoveryCodePlaceholder')}
                  autoFocus
                  className={`${AUTH_INPUT_CLASS} text-center font-mono tracking-[0.15em]`}
                />
              </>
            ) : (
              <>
                <Label htmlFor="totp" className="text-xs font-medium text-muted-foreground">{t('login.totpCode')}</Label>
                <Input
                  id="totp"
                  type="text"
                  value={totpCode}
                  onChange={(e) => setTotpCode(e.target.value)}
                  placeholder="000000"
                  maxLength={6}
                  autoFocus
                  className={`${AUTH_INPUT_CLASS} text-center text-lg tracking-[0.3em]`}
                />
              </>
            )}
            <button
              type="button"
              onClick={() => setUseRecovery((v) => !v)}
              className="text-xs font-medium text-primary hover:underline outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
            >
              {useRecovery ? t('login.useAuthenticator') : t('login.useRecoveryCode')}
            </button>
          </div>
        )}

        <Button
          type="submit"
          className={AUTH_SUBMIT_CLASS}
          disabled={loading}
        >
          {loading ? t('login.signingIn') : t('login.signIn')}
        </Button>
      </form>
    </AuthShell>
  )
}
