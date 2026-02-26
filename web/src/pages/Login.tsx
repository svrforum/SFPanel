import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

export default function Login() {
  const navigate = useNavigate()
  const { t } = useTranslation()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [totpCode, setTotpCode] = useState('')
  const [showTotp, setShowTotp] = useState(false)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setError('')
    setLoading(true)

    try {
      const result = await api.login(username, password, showTotp ? totpCode : undefined)
      api.setToken(result.token)
      navigate('/dashboard')
    } catch (err: any) {
      const message = err.message || 'Login failed'
      if (message.toLowerCase().includes('totp') || message.toLowerCase().includes('2fa')) {
        setShowTotp(true)
        setError(t('login.totpRequired'))
      } else {
        setError(message)
      }
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-background">
      <div className="w-full max-w-sm px-6">
        <div className="text-center mb-8">
          <h1 className="text-2xl font-bold tracking-tight text-foreground">SFPanel</h1>
          <p className="text-sm text-muted-foreground mt-2">{t('login.subtitle')}</p>
        </div>

        <div className="bg-card rounded-2xl card-shadow-lg p-8">
          <form onSubmit={handleSubmit} className="space-y-5">
            {error && (
              <div className="bg-destructive/8 text-destructive text-sm p-3 rounded-xl text-center font-medium">
                {error}
              </div>
            )}

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
                className="h-11 rounded-xl bg-secondary/50 border-0 focus-visible:ring-2 focus-visible:ring-primary/30"
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
                className="h-11 rounded-xl bg-secondary/50 border-0 focus-visible:ring-2 focus-visible:ring-primary/30"
              />
            </div>

            {showTotp && (
              <div className="space-y-2">
                <Label htmlFor="totp" className="text-xs font-medium text-muted-foreground">{t('login.totpCode')}</Label>
                <Input
                  id="totp"
                  type="text"
                  value={totpCode}
                  onChange={(e) => setTotpCode(e.target.value)}
                  placeholder="000000"
                  maxLength={6}
                  autoFocus
                  className="h-11 rounded-xl bg-secondary/50 border-0 text-center text-lg tracking-[0.3em] focus-visible:ring-2 focus-visible:ring-primary/30"
                />
              </div>
            )}

            <Button
              type="submit"
              className="w-full h-11 rounded-xl font-semibold text-sm transition-all duration-200 hover:brightness-110"
              disabled={loading}
            >
              {loading ? t('login.signingIn') : t('login.signIn')}
            </Button>
          </form>
        </div>
      </div>
    </div>
  )
}
