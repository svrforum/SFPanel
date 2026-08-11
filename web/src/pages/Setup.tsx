import { useState, useEffect, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import AuthShell, { AuthErrorBanner, AUTH_INPUT_CLASS, AUTH_SUBMIT_CLASS } from '@/components/AuthShell'

export default function Setup() {
  const navigate = useNavigate()
  const { t } = useTranslation()
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [gateBlocked, setGateBlocked] = useState(false)

  useEffect(() => {
    // The server restricts first-run setup to loopback/LAN sources; surface
    // that here instead of letting the operator fail on submit.
    api.getSetupStatus()
      .then((s) => { if (s.setup_required && s.setup_allowed_from_here === false) setGateBlocked(true) })
      .catch(() => { /* status unreachable — let the form attempt and surface the error */ })
  }, [])

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setError('')

    if (password.length < 12) {
      setError(t('setup.passwordMinLength'))
      return
    }

    if (password !== confirmPassword) {
      setError(t('setup.passwordMismatch'))
      return
    }

    setLoading(true)
    try {
      const result = await api.setupAdmin(username, password)
      api.setTokenPair(result.token, result.refresh_token ?? null)
      navigate('/dashboard')
    } catch (err: unknown) {
      const code = err instanceof Error ? (err as Error & { code?: string }).code : undefined
      // Known codes map to translated text (repo convention); unknown codes
      // fall back to the server's message. WEAK_PASSWORD stays on the raw
      // message intentionally — it carries the specific requirement that failed.
      if (code === 'ALREADY_SETUP') {
        setError(t('setup.alreadySetup'))
      } else if (code === 'RATE_LIMITED') {
        setError(t('setup.rateLimited'))
      } else {
        setError(err instanceof Error ? err.message : 'Setup failed')
      }
    } finally {
      setLoading(false)
    }
  }

  return (
    <AuthShell subtitle={t('setup.subtitle')}>
      {gateBlocked ? (
        <div className="text-sm space-y-2 text-center">
          <p className="font-semibold text-foreground">{t('setup.restrictedTitle')}</p>
          <p className="text-muted-foreground">{t('setup.restrictedBody')}</p>
        </div>
      ) : (
      <form onSubmit={handleSubmit} className="space-y-5">
        <AuthErrorBanner error={error} />

        <div className="space-y-2">
          <Label htmlFor="username" className="text-xs font-medium text-muted-foreground">{t('setup.username')}</Label>
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
          <Label htmlFor="password" className="text-xs font-medium text-muted-foreground">{t('setup.password')}</Label>
          <Input
            id="password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            placeholder={t('setup.passwordPlaceholder')}
            required
            minLength={12}
            className={AUTH_INPUT_CLASS}
          />
        </div>

        <div className="space-y-2">
          <Label htmlFor="confirm-password" className="text-xs font-medium text-muted-foreground">{t('setup.confirmPassword')}</Label>
          <Input
            id="confirm-password"
            type="password"
            value={confirmPassword}
            onChange={(e) => setConfirmPassword(e.target.value)}
            placeholder={t('setup.confirmPlaceholder')}
            required
            minLength={12}
            className={AUTH_INPUT_CLASS}
          />
        </div>

        <Button
          type="submit"
          className={AUTH_SUBMIT_CLASS}
          disabled={loading}
        >
          {loading ? t('setup.creatingAccount') : t('setup.createAdmin')}
        </Button>
      </form>
      )}
    </AuthShell>
  )
}
