import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

export default function Setup() {
  const navigate = useNavigate()
  const { t } = useTranslation()
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setError('')

    if (password.length < 8) {
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
      api.setToken(result.token)
      navigate('/dashboard')
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Setup failed')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-background">
      <div className="w-full max-w-sm px-6">
        <div className="text-center mb-8">
          <h1 className="text-2xl font-bold tracking-tight text-foreground">SFPanel</h1>
          <p className="text-sm text-muted-foreground mt-2">{t('setup.subtitle')}</p>
        </div>

        <div className="bg-card rounded-2xl card-shadow-lg p-8">
          <form onSubmit={handleSubmit} className="space-y-5">
            {error && (
              <div className="bg-destructive/8 text-destructive text-sm p-3 rounded-xl text-center font-medium">
                {error}
              </div>
            )}

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
                className="h-11 rounded-xl bg-secondary/50 border-0 focus-visible:ring-2 focus-visible:ring-primary/30"
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
                minLength={8}
                className="h-11 rounded-xl bg-secondary/50 border-0 focus-visible:ring-2 focus-visible:ring-primary/30"
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
                minLength={8}
                className="h-11 rounded-xl bg-secondary/50 border-0 focus-visible:ring-2 focus-visible:ring-primary/30"
              />
            </div>

            <Button
              type="submit"
              className="w-full h-11 rounded-xl font-semibold text-sm transition-all duration-200 hover:brightness-110"
              disabled={loading}
            >
              {loading ? t('setup.creatingAccount') : t('setup.createAdmin')}
            </Button>
          </form>
        </div>
      </div>
    </div>
  )
}
