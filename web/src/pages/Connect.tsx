import { useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Monitor } from 'lucide-react'

export default function Connect() {
  const navigate = useNavigate()
  const { t } = useTranslation()
  const [url, setUrl] = useState(localStorage.getItem('sfpanel_server_url') || '')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setError('')

    // Normalize URL: remove trailing slash
    let serverUrl = url.trim().replace(/\/+$/, '')

    // Validate URL format
    try {
      const parsed = new URL(serverUrl)
      if (!['http:', 'https:'].includes(parsed.protocol)) {
        setError(t('connect.invalidUrl'))
        return
      }
      serverUrl = parsed.origin
    } catch {
      setError(t('connect.invalidUrl'))
      return
    }

    setLoading(true)

    try {
      // Test connection by hitting the health endpoint
      const res = await fetch(`${serverUrl}/api/v1/health`, {
        signal: AbortSignal.timeout(5000),
      })
      const json = await res.json()
      if (!json.success) throw new Error()

      // Connection successful — save and proceed
      api.setServerUrl(serverUrl)
      navigate('/login', { replace: true })
    } catch {
      setError(t('connect.connectionFailed'))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-background">
      <div className="w-full max-w-sm px-6">
        <div className="text-center mb-8">
          <div className="inline-flex items-center justify-center w-12 h-12 rounded-2xl bg-primary/10 mb-4">
            <Monitor className="w-6 h-6 text-primary" />
          </div>
          <h1 className="text-2xl font-bold tracking-tight text-foreground">SFPanel</h1>
          <p className="text-sm text-muted-foreground mt-2">{t('connect.subtitle')}</p>
        </div>

        <div className="bg-card rounded-2xl card-shadow-lg p-8">
          <form onSubmit={handleSubmit} className="space-y-5">
            {error && (
              <div className="bg-destructive/8 text-destructive text-sm p-3 rounded-xl text-center font-medium">
                {error}
              </div>
            )}

            <div className="space-y-2">
              <Label htmlFor="server-url" className="text-xs font-medium text-muted-foreground">
                {t('connect.serverUrl')}
              </Label>
              <Input
                id="server-url"
                type="url"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                placeholder={t('connect.placeholder')}
                required
                autoFocus
                className="h-11 rounded-xl bg-secondary/50 border-0 focus-visible:ring-2 focus-visible:ring-primary/30"
              />
            </div>

            <Button
              type="submit"
              className="w-full h-11 rounded-xl font-semibold text-sm transition-all duration-200 hover:brightness-110"
              disabled={loading}
            >
              {loading ? t('connect.connecting') : t('connect.connect')}
            </Button>
          </form>
        </div>
      </div>
    </div>
  )
}
