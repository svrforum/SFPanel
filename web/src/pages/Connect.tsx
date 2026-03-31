import { useState, useEffect, useRef, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Monitor, Globe, Bug } from 'lucide-react'
import { LANGUAGE_KEY } from '@/i18n'

const EXAMPLES = [
  'https://192.168.1.100:8443',
  'https://myserver.example.com:8443',
  'http://10.0.0.5:8443',
]

// Tauri HTTP 플러그인 로딩 (CORS 우회용)
const isTauri = '__TAURI_INTERNALS__' in window
let pluginFetchPromise: Promise<typeof globalThis.fetch> | null = null
if (isTauri) {
  pluginFetchPromise = import('@tauri-apps/plugin-http')
    .then((mod) => mod.fetch as typeof globalThis.fetch)
    .catch(() => globalThis.fetch)
}

async function safeFetch(input: string, init?: RequestInit): Promise<Response> {
  if (pluginFetchPromise) {
    const fn = await pluginFetchPromise
    return fn(input, init)
  }
  return globalThis.fetch(input, init)
}

export default function Connect() {
  const navigate = useNavigate()
  const { t, i18n } = useTranslation()
  const [url, setUrl] = useState(localStorage.getItem('sfpanel_server_url') || '')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)
  const [showDiag, setShowDiag] = useState(false)
  const [diagLog, setDiagLog] = useState<string[]>([])
  const [pluginReady, setPluginReady] = useState(!isTauri)
  const logRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (pluginFetchPromise) {
      pluginFetchPromise.then(() => setPluginReady(true))
    }
  }, [])

  useEffect(() => {
    if (logRef.current) {
      logRef.current.scrollTop = logRef.current.scrollHeight
    }
  }, [diagLog])

  const addLog = (msg: string) => {
    setDiagLog((prev) => [...prev, `[${new Date().toLocaleTimeString()}] ${msg}`])
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
      const res = await safeFetch(`${serverUrl}/api/v1/health`, {
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
      addLog('❌ URL 형식이 올바르지 않습니다')
      return
    }

    addLog(`🔍 환경: ${isTauri ? 'Tauri 데스크톱' : '웹 브라우저'}`)
    addLog(`🔍 HTTP 플러그인: ${pluginReady ? '로드됨' : '미로드'}`)
    addLog(`🔍 대상: ${serverUrl}`)

    // Step 1: 플러그인 fetch 테스트
    addLog('--- Step 1: HTTP 요청 테스트 ---')
    try {
      addLog(`📡 ${serverUrl}/api/v1/health 요청 중...`)
      const res = await safeFetch(`${serverUrl}/api/v1/health`, {
        signal: AbortSignal.timeout(5000),
      })
      addLog(`✅ 응답 수신: HTTP ${res.status} ${res.statusText}`)
      const text = await res.text()
      addLog(`📄 응답 본문: ${text.substring(0, 200)}`)
      try {
        const json = JSON.parse(text)
        if (json.success) {
          addLog('✅ Health check 성공!')
        } else {
          addLog(`❌ 서버가 실패 응답 반환: ${JSON.stringify(json)}`)
        }
      } catch {
        addLog('❌ JSON 파싱 실패 — 서버 응답이 JSON이 아닙니다')
      }
    } catch (err) {
      const msg = err instanceof Error ? `${err.name}: ${err.message}` : String(err)
      addLog(`❌ 요청 실패: ${msg}`)

      if (msg.includes('Failed to fetch') || msg.includes('NetworkError')) {
        addLog('💡 원인: CORS 차단 또는 네트워크 연결 불가')
        addLog('💡 서버가 최신 버전(v0.6.0+)인지 확인하세요')
      } else if (msg.includes('TimeoutError') || msg.includes('timed out')) {
        addLog('💡 원인: 서버 응답 없음 (타임아웃 5초)')
        addLog('💡 서버 주소와 포트를 확인하세요')
      }
    }

    // Step 2: globalThis.fetch 직접 테스트
    addLog('--- Step 2: 브라우저 기본 fetch 테스트 ---')
    try {
      const res = await globalThis.fetch(`${serverUrl}/api/v1/health`, {
        signal: AbortSignal.timeout(5000),
      })
      addLog(`✅ 기본 fetch 성공: HTTP ${res.status}`)
    } catch (err) {
      const msg = err instanceof Error ? `${err.name}: ${err.message}` : String(err)
      addLog(`❌ 기본 fetch 실패: ${msg}`)
    }

    addLog('--- 진단 완료 ---')
  }

  return (
    <div className="min-h-screen flex flex-col items-center justify-center bg-background">
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
              disabled={loading || !pluginReady}
            >
              {!pluginReady ? '플러그인 로딩 중...' : loading ? t('connect.connecting') : t('connect.connect')}
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
