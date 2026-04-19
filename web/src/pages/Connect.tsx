import { useState, useRef, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Globe, Bug } from 'lucide-react'
import { LANGUAGE_KEY } from '@/i18n'

const EXAMPLES = [
  'https://192.168.1.100:8443',
  'https://myserver.example.com:8443',
  'http://10.0.0.5:8443',
]

const isTauri = '__TAURI_INTERNALS__' in window

export default function Connect() {
  const navigate = useNavigate()
  const { t, i18n } = useTranslation()
  const [url, setUrl] = useState(localStorage.getItem('sfpanel_server_url') || '')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [showDiag, setShowDiag] = useState(false)
  const [diagLog, setDiagLog] = useState<string[]>([])
  const logRef = useRef<HTMLDivElement>(null)

  const addLog = (msg: string) => {
    setDiagLog((prev) => {
      const next = [...prev, `[${new Date().toLocaleTimeString()}] ${msg}`]
      setTimeout(() => { if (logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight }, 0)
      return next
    })
  }

  const currentLang = i18n.language?.startsWith('ko') ? 'ko' : 'en'

  const switchLanguage = (lang: string) => {
    i18n.changeLanguage(lang)
    localStorage.setItem(LANGUAGE_KEY, lang)
  }

  const getServerUrl = (): string | null => {
    let serverUrl = url.trim().replace(/\/+$/, '')
    try {
      const parsed = new URL(serverUrl)
      if (!['http:', 'https:'].includes(parsed.protocol)) {
        setError(t('connect.invalidUrl'))
        return null
      }
      return parsed.origin
    } catch {
      setError(t('connect.invalidUrl'))
      return null
    }
  }

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault()
    setError('')
    const serverUrl = getServerUrl()
    if (!serverUrl) return

    setLoading(true)
    try {
      const res = await fetch(`${serverUrl}/api/v1/health`, {
        signal: AbortSignal.timeout(5000),
      })
      const json = await res.json()
      if (!json.success) throw new Error('Server returned error response')

      api.setServerUrl(serverUrl)
      navigate('/login', { replace: true })
    } catch (err) {
      const detail = err instanceof Error ? err.message : String(err)
      setError(`${t('connect.connectionFailed')}\n${detail}`)
    } finally {
      setLoading(false)
    }
  }

  const runDiagnostic = async () => {
    setShowDiag(true)
    setDiagLog([])
    const serverUrl = getServerUrl()
    if (!serverUrl) {
      addLog(t('connect.diagInvalidUrl'))
      return
    }

    addLog(t('connect.diagEnv', { env: isTauri ? 'Tauri' : 'Web' }))
    addLog(t('connect.diagTarget', { url: serverUrl }))

    addLog(t('connect.diagHealthCheck'))
    try {
      addLog(t('connect.diagRequesting', { url: `${serverUrl}/api/v1/health` }))
      const res = await fetch(`${serverUrl}/api/v1/health`, {
        signal: AbortSignal.timeout(5000),
      })
      addLog(t('connect.diagResponse', { status: res.status, statusText: res.statusText }))
      const text = await res.text()
      addLog(t('connect.diagBody', { body: text.substring(0, 200) }))
      try {
        const json = JSON.parse(text)
        if (json.success) {
          addLog(t('connect.diagSuccess'))
        } else {
          addLog(t('connect.diagServerError', { error: JSON.stringify(json) }))
        }
      } catch {
        addLog(t('connect.diagJsonError'))
      }
    } catch (err) {
      const msg = err instanceof Error ? `${err.name}: ${err.message}` : String(err)
      addLog(t('connect.diagFetchError', { error: msg }))

      if (msg.includes('Failed to fetch') || msg.includes('NetworkError')) {
        addLog(t('connect.diagHintCors'))
        addLog(t('connect.diagHintVersion'))
      } else if (msg.includes('TimeoutError') || msg.includes('timed out')) {
        addLog(t('connect.diagHintTimeout'))
        addLog(t('connect.diagHintAddress'))
      } else if (msg.includes('not allowed')) {
        addLog(t('connect.diagHintScope'))
        addLog(t('connect.diagHintUpdate'))
      }
    }

    addLog(t('connect.diagDone'))
  }

  return (
    <div className="min-h-screen flex flex-col items-center justify-center bg-background">
      <div className="w-full max-w-sm px-6">
        <div className="text-center mb-8">
          <img
            src="/favicon.png"
            alt="SFPanel"
            className="mx-auto mb-4 h-16 w-16 rounded-2xl"
          />
          <h1 className="text-2xl font-bold tracking-tight text-foreground">SFPanel</h1>
          <p className="text-sm text-muted-foreground mt-2">{t('connect.subtitle')}</p>
        </div>

        <div className="bg-card rounded-2xl card-shadow-lg p-8">
          <form onSubmit={handleSubmit} className="space-y-5">
            {error && (
              <div className="bg-destructive/8 text-destructive text-sm p-3 rounded-xl text-center font-medium whitespace-pre-line">
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
                placeholder="https://192.168.1.100:8443"
                required
                autoFocus
                className="h-11 rounded-xl bg-secondary/50 border-0 focus-visible:ring-2 focus-visible:ring-primary/30"
              />
              <div className="space-y-1 pt-1">
                <p className="text-[11px] text-muted-foreground">{t('connect.examples')}</p>
                <div className="flex flex-wrap gap-1.5">
                  {EXAMPLES.map((example) => (
                    <button
                      key={example}
                      type="button"
                      onClick={() => setUrl(example)}
                      className="text-[11px] px-2 py-0.5 rounded-full bg-secondary/80 text-muted-foreground hover:text-foreground hover:bg-secondary transition-colors"
                    >
                      {example}
                    </button>
                  ))}
                </div>
              </div>
            </div>

            <Button
              type="submit"
              className="w-full h-11 rounded-xl font-semibold text-sm transition-all duration-200 hover:brightness-110"
              disabled={loading}
            >
              {loading ? t('connect.connecting') : t('connect.connect')}
            </Button>
          </form>

          <div className="mt-3 text-center">
            <button
              type="button"
              onClick={runDiagnostic}
              className="inline-flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground transition-colors"
            >
              <Bug className="w-3 h-3" />
              {t('connect.diagnose')}
            </button>
          </div>
        </div>

        {showDiag && (
          <div className="mt-4 bg-card rounded-2xl card-shadow p-4">
            <div className="flex items-center justify-between mb-2">
              <span className="text-[13px] font-semibold">{t('connect.diagTitle')}</span>
              <button
                type="button"
                onClick={() => { setShowDiag(false); setDiagLog([]) }}
                className="text-[11px] text-muted-foreground hover:text-foreground"
              >
                {t('common.close')}
              </button>
            </div>
            <div
              ref={logRef}
              className="bg-secondary/50 rounded-xl p-3 max-h-60 overflow-y-auto font-mono text-[11px] leading-5 space-y-0.5"
            >
              {diagLog.length === 0 ? (
                <p className="text-muted-foreground">{t('connect.diagEmpty')}</p>
              ) : (
                diagLog.map((line, i) => (
                  <p key={i} className={line.includes('❌') ? 'text-[#f04452]' : line.includes('✅') ? 'text-[#00c471]' : ''}>
                    {line}
                  </p>
                ))
              )}
            </div>
          </div>
        )}
      </div>

      <div className="flex items-center gap-2 mt-6">
        <Globe className="w-3.5 h-3.5 text-muted-foreground" />
        <button
          type="button"
          onClick={() => switchLanguage('ko')}
          className={`text-[12px] px-2 py-0.5 rounded-full transition-colors ${
            currentLang === 'ko'
              ? 'bg-primary/10 text-primary font-medium'
              : 'text-muted-foreground hover:text-foreground'
          }`}
        >
          한국어
        </button>
        <span className="text-muted-foreground text-[11px]">|</span>
        <button
          type="button"
          onClick={() => switchLanguage('en')}
          className={`text-[12px] px-2 py-0.5 rounded-full transition-colors ${
            currentLang === 'en'
              ? 'bg-primary/10 text-primary font-medium'
              : 'text-muted-foreground hover:text-foreground'
          }`}
        >
          English
        </button>
      </div>
    </div>
  )
}
