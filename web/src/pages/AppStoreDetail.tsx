import { useState, useEffect, useCallback, useRef, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import { Marked } from 'marked'
import {
  Eye,
  EyeOff,
  Loader2,
  RefreshCw,
  Package,
  Globe,
  Github,
  Download,
  Check,
  ChevronDown,
  ChevronUp,
  X,
  CheckCircle2,
  XCircle,
  Circle,
} from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import type { AppStoreAppDetail } from '@/types/api'

// Convert inline markdown (bold, links) to HTML
function inlineMarkdownToHtml(text: string): string {
  return text
    .replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
    .replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank" rel="noopener noreferrer" class="underline">$1</a>')
}

// Convert GitHub Alert syntax to styled HTML
function processGitHubAlerts(markdown: string): string {
  const alertIcons: Record<string, string> = {
    NOTE: 'ℹ️',
    TIP: '💡',
    IMPORTANT: '❗',
    WARNING: '⚠️',
    CAUTION: '🔴',
  }
  return markdown.replace(
    /^> \[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*\n((?:>.*\n?)*)/gm,
    (_match, type: string, body: string) => {
      const icon = alertIcons[type] || ''
      const content = inlineMarkdownToHtml(body.replace(/^> ?/gm, '').trim())
      const colors: Record<string, string> = {
        NOTE: 'border-blue-400 bg-blue-50 dark:bg-blue-950/30',
        TIP: 'border-green-400 bg-green-50 dark:bg-green-950/30',
        IMPORTANT: 'border-purple-400 bg-purple-50 dark:bg-purple-950/30',
        WARNING: 'border-yellow-400 bg-yellow-50 dark:bg-yellow-950/30',
        CAUTION: 'border-red-400 bg-red-50 dark:bg-red-950/30',
      }
      const color = colors[type] || 'border-gray-400 bg-gray-50'
      return `<div class="rounded-lg border-l-4 ${color} p-3 my-3 text-[12px]"><strong>${icon} ${type}</strong><br/>${content}</div>\n`
    }
  )
}

function generatePassword(): string {
  const bytes = new Uint8Array(16)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

function transformUrl(url: string, baseUrl?: string): string {
  if (!url) return url
  if (url.startsWith('http://') || url.startsWith('https://') || url.startsWith('data:')) return url
  if (baseUrl) {
    const cleanUrl = url.startsWith('./') ? url.slice(2) : url
    return baseUrl + cleanUrl
  }
  return url
}

function createMarked(baseUrl?: string): Marked {
  return new Marked({
    gfm: true,
    breaks: false,
    renderer: {
      image({ href, text }: { href: string; text: string }) {
        const src = transformUrl(href, baseUrl)
        const isBadge = src && (
          src.includes('shields.io') || src.includes('img.shields') ||
          src.includes('badge') || src.includes('contrib.rocks') ||
          src.includes('repobeats') || src.includes('star-history')
        )
        if (isBadge) {
          return `<img src="${src}" alt="${text}" class="inline-block h-5 my-0.5 mr-1 rounded-none" />`
        }
        const isLogo = (src && (src.endsWith('.svg') || src.includes('logo'))) ||
          (text && text.toLowerCase().includes('logo'))
        const maxH = isLogo ? 'max-h-20' : 'max-h-64'
        return `<img src="${src}" alt="${text}" class="max-w-full h-auto rounded-lg ${maxH}" />`
      },
      link({ href, text }: { href: string; text: string }) {
        const url = transformUrl(href, baseUrl)
        return `<a href="${url}" target="_blank" rel="noopener noreferrer">${text}</a>`
      },
    },
  })
}

function RenderedReadme({ markdown, baseUrl }: { markdown: string; baseUrl?: string }) {
  const html = useMemo(() => {
    const processed = processGitHubAlerts(markdown)
    const md = createMarked(baseUrl)
    return md.parse(processed) as string
  }, [markdown, baseUrl])

  return (
    <div
      className="rounded-xl bg-secondary/20 p-5 prose prose-sm dark:prose-invert max-w-none
        prose-headings:text-foreground prose-headings:font-semibold
        prose-h1:text-[16px] prose-h1:mt-0 prose-h1:mb-2
        prose-h2:text-[14px] prose-h2:mt-5 prose-h2:mb-2
        prose-h3:text-[13px] prose-h3:mt-3 prose-h3:mb-1
        prose-p:text-[12px] prose-p:text-muted-foreground prose-p:leading-relaxed
        prose-li:text-[12px] prose-li:text-muted-foreground
        prose-strong:text-foreground prose-strong:font-medium
        prose-a:text-primary prose-a:no-underline hover:prose-a:underline
        prose-code:text-[11px] prose-code:bg-secondary/50 prose-code:px-1.5 prose-code:py-0.5 prose-code:rounded-md prose-code:text-foreground
        prose-pre:bg-[#1e1e2e] prose-pre:text-[#cdd6f4] prose-pre:rounded-xl prose-pre:p-4
        [&_pre_code]:bg-transparent [&_pre_code]:p-0 [&_pre_code]:text-[11px] [&_pre_code]:text-[#cdd6f4]
        prose-table:text-[11px]
        prose-th:text-[10px] prose-th:font-semibold prose-th:text-muted-foreground prose-th:uppercase prose-th:tracking-wider
        prose-td:text-[11px]
        prose-img:rounded-lg prose-img:my-2
        [&_img]:max-w-full [&_img]:h-auto [&_img]:rounded-lg
      "
      dangerouslySetInnerHTML={{ __html: html }}
    />
  )
}

interface AppStoreDetailModalProps {
  appId: string | null
  open: boolean
  onClose: () => void
  onInstalled: () => void
}

export default function AppStoreDetailModal({ appId, open, onClose, onInstalled }: AppStoreDetailModalProps) {
  const { t, i18n } = useTranslation()
  const lang = i18n.language.startsWith('ko') ? 'ko' : 'en'

  const [detail, setDetail] = useState<AppStoreAppDetail | null>(null)
  const [loading, setLoading] = useState(false)
  const [envValues, setEnvValues] = useState<Record<string, string>>({})
  const [installing, setInstalling] = useState(false)
  const [showPasswords, setShowPasswords] = useState<Record<string, boolean>>({})
  const [showInstallForm, setShowInstallForm] = useState(false)
  const [showCompose, setShowCompose] = useState(false)
  const [showProgress, setShowProgress] = useState(false)
  const [progressLogs, setProgressLogs] = useState<Array<{ stage: string; message: string; success: boolean }>>([])
  const [progressDone, setProgressDone] = useState(false)
  const [progressSuccess, setProgressSuccess] = useState(false)
  const [currentStage, setCurrentStage] = useState('')
  const logEndRef = useRef<HTMLDivElement>(null)
  const [installMode, setInstallMode] = useState<'simple' | 'advanced'>('simple')
  const [customCompose, setCustomCompose] = useState('')
  const [customEnv, setCustomEnv] = useState('')
  const [advancedTab, setAdvancedTab] = useState<'compose' | 'env'>('compose')
  const abortRef = useRef<AbortController | null>(null)

  const getIconUrl = () => {
    if (!detail) return ''
    return detail.app.icon || `https://raw.githubusercontent.com/svrforum/SFPanel-appstore/main/apps/${detail.app.id}/icon.svg`
  }

  const loadDetail = useCallback(async () => {
    if (!appId) return
    setLoading(true)
    setDetail(null)
    setShowInstallForm(false)
    setShowCompose(false)
    setShowProgress(false)
    setProgressLogs([])
    setProgressDone(false)
    setProgressSuccess(false)
    setCurrentStage('')
    setInstalling(false)
    try {
      const data = await api.getAppStoreApp(appId)
      setDetail(data)

      // Build port conflict map from port_status
      const portSuggestions = new Map<number, number>()
      if (data.port_status) {
        for (const ps of data.port_status) {
          if (ps.in_use && ps.suggested) {
            portSuggestions.set(ps.port, ps.suggested)
          }
        }
      }

      const defaults: Record<string, string> = {}
      for (const envDef of data.app.env) {
        if (envDef.generate && envDef.type === 'password') {
          defaults[envDef.key] = generatePassword()
        } else if (envDef.default !== undefined) {
          // Auto-replace conflicting port with suggested free port
          if (envDef.type === 'port') {
            const port = parseInt(envDef.default, 10)
            const suggested = portSuggestions.get(port)
            defaults[envDef.key] = suggested ? String(suggested) : envDef.default
          } else {
            defaults[envDef.key] = envDef.default
          }
        } else {
          defaults[envDef.key] = ''
        }
      }
      setEnvValues(defaults)
      setShowPasswords({})
    } catch {
      toast.error(t('appStore.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [appId, t])

  useEffect(() => {
    if (open && appId) {
      loadDetail()
    }
  }, [open, appId, loadDetail])

  // Close on Escape
  useEffect(() => {
    if (!open) return
    const handleKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !installing) onClose()
    }
    document.addEventListener('keydown', handleKey)
    return () => document.removeEventListener('keydown', handleKey)
  }, [open, installing, onClose])

  // Prevent body scroll when modal is open; abort SSE on close
  useEffect(() => {
    if (open) {
      document.body.style.overflow = 'hidden'
    } else {
      document.body.style.overflow = ''
      // Abort any in-flight SSE stream when modal closes
      if (abortRef.current) {
        abortRef.current.abort()
        abortRef.current = null
      }
    }
    return () => {
      document.body.style.overflow = ''
      if (abortRef.current) {
        abortRef.current.abort()
        abortRef.current = null
      }
    }
  }, [open])

  // Auto-scroll logs within container only
  useEffect(() => {
    const el = logEndRef.current?.parentElement
    if (el) {
      el.scrollTop = el.scrollHeight
    }
  }, [progressLogs])

  const handleInstall = async () => {
    if (!detail) return
    setInstalling(true)
    setShowProgress(true)
    setProgressLogs([])
    setProgressDone(false)
    setProgressSuccess(false)
    setCurrentStage('')
    setShowInstallForm(false)

    try {
      const controller = new AbortController()
      abortRef.current = controller

      const token = api.getToken()
      const nodeParam = api.currentNode ? `?node=${encodeURIComponent(api.currentNode)}` : ''
      const res = await fetch(`${api.apiBase}/appstore/apps/${detail.app.id}/install${nodeParam}`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
        },
        body: JSON.stringify(
          installMode === 'advanced'
            ? { advanced: true, compose: customCompose, env_raw: customEnv }
            : { env: envValues }
        ),
        signal: controller.signal,
      })

      if (!res.ok) {
        // Pre-flight check failed (JSON error response)
        try {
          const errData = await res.json()
          const code = errData?.error?.code || ''
          const msg = errData?.error?.message || t('appStore.installFailed')
          if (code === 'PORT_CONFLICT') {
            toast.error(t('appStore.portConflict') + ': ' + msg.replace('Port conflict: ', ''))
          } else if (code === 'CONTAINER_CONFLICT') {
            toast.error(t('appStore.containerConflict') + ': ' + msg.replace('Container name conflict: ', ''))
          } else if (code === 'ALREADY_EXISTS') {
            toast.error(t('appStore.alreadyInstalled'))
          } else {
            toast.error(msg)
          }
        } catch {
          toast.error(t('appStore.installFailed'))
        }
        setShowProgress(false)
        setInstalling(false)
        setShowInstallForm(true)
        return
      }

      if (!res.body) {
        toast.error(t('appStore.installFailed'))
        setShowProgress(false)
        setInstalling(false)
        return
      }

      const reader = res.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''

        for (const line of lines) {
          if (!line.startsWith('data: ')) continue
          try {
            const event = JSON.parse(line.slice(6))
            setCurrentStage(event.stage)
            setProgressLogs(prev => [...prev, {
              stage: event.stage,
              message: event.message,
              success: event.success,
            }])
            if (event.done) {
              setProgressDone(true)
              setProgressSuccess(event.success)
              if (event.success) {
                toast.success(t('appStore.installSuccess', { name: detail.app.name }))
                onInstalled()
              } else {
                toast.error(t('appStore.installFailed'))
              }
            }
          } catch {
            // skip invalid JSON
          }
        }
      }
    } catch (err) {
      if (err instanceof DOMException && err.name === 'AbortError') {
        // User closed modal during install — no toast needed
        return
      }
      toast.error(t('appStore.installFailed'))
      setProgressDone(true)
      setProgressSuccess(false)
    } finally {
      abortRef.current = null
      setInstalling(false)
    }
  }

  if (!open) return null

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center">
      {/* Backdrop */}
      <div
        className="fixed inset-0 bg-black/50 backdrop-blur-sm animate-in fade-in duration-200"
        onClick={() => !installing && onClose()}
      />

      {/* Modal */}
      <div className="relative z-10 w-full max-w-2xl mx-4 my-8 max-h-[calc(100vh-4rem)] overflow-y-auto rounded-2xl bg-background card-shadow-lg animate-in slide-in-from-bottom-4 fade-in duration-300">
        {/* Close button */}
        <button
          onClick={() => !installing && onClose()}
          className="absolute top-4 right-4 z-20 p-1.5 rounded-full bg-secondary/80 hover:bg-secondary text-muted-foreground hover:text-foreground transition-colors"
        >
          <X className="h-4 w-4" />
        </button>

        {loading ? (
          <div className="flex items-center justify-center h-64">
            <div className="h-5 w-5 border-2 border-primary border-t-transparent rounded-full animate-spin" />
          </div>
        ) : !detail ? (
          <div className="flex flex-col items-center justify-center py-16 text-muted-foreground">
            <Package className="h-10 w-10 mb-3 opacity-40" />
            <p className="text-[13px]">{t('appStore.noApps')}</p>
          </div>
        ) : (
          <div className="p-6 space-y-6">
            {/* Hero */}
            <div className="flex flex-col sm:flex-row gap-5">
              <div className="shrink-0">
                <div className="h-20 w-20 rounded-[18px] bg-secondary/30 p-2.5 flex items-center justify-center overflow-hidden">
                  <img
                    src={getIconUrl()}
                    alt={detail.app.name}
                    className="h-full w-full object-contain"
                    onError={(e) => {
                      const target = e.currentTarget
                      target.style.display = 'none'
                      const fallback = target.nextElementSibling as HTMLElement
                      if (fallback) fallback.style.display = 'flex'
                    }}
                  />
                  <div className="hidden items-center justify-center h-full w-full text-primary">
                    <Package className="h-10 w-10" />
                  </div>
                </div>
              </div>

              <div className="flex-1 min-w-0">
                <h2 className="text-[20px] font-bold tracking-tight">{detail.app.name}</h2>
                <p className="text-[13px] text-muted-foreground mt-1 leading-relaxed">
                  {detail.app.description[lang] || detail.app.description['en'] || ''}
                </p>

                <div className="flex flex-wrap items-center gap-2 mt-3">
                  <span className="inline-flex items-center px-2.5 py-0.5 rounded-full text-[11px] font-medium bg-secondary/60 text-muted-foreground">
                    v{detail.app.version}
                  </span>
                  {detail.app.category && (
                    <span className="inline-flex items-center px-2.5 py-0.5 rounded-full text-[11px] font-medium bg-secondary/60 text-muted-foreground capitalize">
                      {detail.app.category}
                    </span>
                  )}
                  {detail.app.ports.map((port) => (
                    <span
                      key={port}
                      className="inline-flex items-center px-2.5 py-0.5 rounded-full text-[11px] font-medium bg-secondary/60 text-muted-foreground"
                    >
                      {t('appStore.port')}: {port}
                    </span>
                  ))}
                </div>

                <div className="flex items-center gap-3 mt-4">
                  {detail.installed ? (
                    <span className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl text-[13px] font-medium bg-[#00c471]/10 text-[#00c471]">
                      <Check className="h-4 w-4" />
                      {t('appStore.installed')}
                    </span>
                  ) : (
                    <Button
                      className="rounded-xl px-5"
                      size="sm"
                      onClick={() => {
                        if (!showInstallForm && detail) {
                          setCustomCompose(detail.compose || '')
                          // Build default .env from env values
                          const lines = detail.app.env.map(e => {
                            const val = envValues[e.key] ?? e.default ?? ''
                            return `${e.key}=${val}`
                          })
                          setCustomEnv(lines.join('\n'))
                          setInstallMode('simple')
                          setAdvancedTab('compose')
                        }
                        setShowInstallForm(!showInstallForm)
                      }}
                    >
                      <Download className="h-4 w-4 mr-1.5" />
                      {t('appStore.install')}
                    </Button>
                  )}
                  {detail.app.website && (
                    <a
                      href={detail.app.website}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-xl text-[12px] text-muted-foreground hover:text-foreground hover:bg-secondary/50 transition-colors"
                    >
                      <Globe className="h-3.5 w-3.5" />
                      {t('appStore.website')}
                    </a>
                  )}
                  {detail.app.source && (
                    <a
                      href={detail.app.source}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-xl text-[12px] text-muted-foreground hover:text-foreground hover:bg-secondary/50 transition-colors"
                    >
                      <Github className="h-3.5 w-3.5" />
                      {t('appStore.source')}
                    </a>
                  )}
                </div>
              </div>
            </div>

            {/* Install Form */}
            {showInstallForm && !detail.installed && (
              <div className="bg-secondary/20 rounded-xl p-5 animate-in slide-in-from-top-2 duration-200">
                <h3 className="text-[14px] font-semibold mb-4">{t('appStore.installTitle', { name: detail.app.name })}</h3>

                {/* Mode tabs */}
                <div className="flex gap-1 p-1 bg-secondary/40 rounded-xl mb-4">
                  <button
                    className={`flex-1 py-1.5 text-[12px] font-medium rounded-lg transition-colors ${
                      installMode === 'simple'
                        ? 'bg-background text-foreground shadow-sm'
                        : 'text-muted-foreground hover:text-foreground'
                    }`}
                    onClick={() => setInstallMode('simple')}
                  >
                    {t('appStore.simpleMode')}
                  </button>
                  <button
                    className={`flex-1 py-1.5 text-[12px] font-medium rounded-lg transition-colors ${
                      installMode === 'advanced'
                        ? 'bg-background text-foreground shadow-sm'
                        : 'text-muted-foreground hover:text-foreground'
                    }`}
                    onClick={() => setInstallMode('advanced')}
                  >
                    {t('appStore.advancedMode')}
                  </button>
                </div>

                {/* Simple mode: env form */}
                {installMode === 'simple' && detail.app.env.length > 0 && (
                  <div className="space-y-3 mb-5">
                    {detail.app.env.map((envDef) => (
                      <div key={envDef.key} className="space-y-1.5">
                        <label className="text-[12px] font-medium text-foreground">
                          {envDef.label[lang] || envDef.label['en'] || envDef.key}
                          {envDef.required && <span className="text-[#f04452] ml-0.5">*</span>}
                        </label>
                        {envDef.type === 'select' && envDef.options ? (
                          <select
                            className="w-full h-9 rounded-xl bg-background border border-border text-[13px] px-3"
                            value={envValues[envDef.key] || ''}
                            onChange={(e) =>
                              setEnvValues((prev) => ({ ...prev, [envDef.key]: e.target.value }))
                            }
                          >
                            {envDef.options.map((opt) => (
                              <option key={opt} value={opt}>{opt}</option>
                            ))}
                          </select>
                        ) : envDef.type === 'password' ? (
                          <div className="flex gap-2">
                            <div className="relative flex-1">
                              <Input
                                type={showPasswords[envDef.key] ? 'text' : 'password'}
                                className="h-9 rounded-xl bg-background border-border text-[13px] pr-9"
                                value={envValues[envDef.key] || ''}
                                onChange={(e) =>
                                  setEnvValues((prev) => ({ ...prev, [envDef.key]: e.target.value }))
                                }
                              />
                              <button
                                type="button"
                                className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                                onClick={() =>
                                  setShowPasswords((prev) => ({
                                    ...prev,
                                    [envDef.key]: !prev[envDef.key],
                                  }))
                                }
                              >
                                {showPasswords[envDef.key] ? (
                                  <EyeOff className="h-3.5 w-3.5" />
                                ) : (
                                  <Eye className="h-3.5 w-3.5" />
                                )}
                              </button>
                            </div>
                            {envDef.generate && (
                              <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                className="rounded-xl text-[11px] shrink-0"
                                onClick={() =>
                                  setEnvValues((prev) => ({
                                    ...prev,
                                    [envDef.key]: generatePassword(),
                                  }))
                                }
                              >
                                <RefreshCw className="h-3 w-3 mr-1" />
                                {t('appStore.generatePassword')}
                              </Button>
                            )}
                          </div>
                        ) : (
                          <div>
                            <Input
                              type={envDef.type === 'port' ? 'number' : 'text'}
                              className="h-9 rounded-xl bg-background border-border text-[13px]"
                              value={envValues[envDef.key] || ''}
                              onChange={(e) =>
                                setEnvValues((prev) => ({ ...prev, [envDef.key]: e.target.value }))
                              }
                            />
                            {envDef.type === 'port' && detail?.port_status && (() => {
                              const ps = detail.port_status?.find(p => p.port === parseInt(envDef.default || '0', 10))
                              if (ps?.in_use) {
                                return (
                                  <p className="text-[11px] text-[#f59e0b] mt-1">
                                    {t('appStore.portInUse', { port: ps.port, suggested: ps.suggested || '' })}
                                  </p>
                                )
                              }
                              return null
                            })()}
                          </div>
                        )}
                      </div>
                    ))}
                  </div>
                )}

                {/* Advanced mode: compose + env editors */}
                {installMode === 'advanced' && (
                  <div className="mb-5">
                    {/* Sub-tabs */}
                    <div className="flex gap-1 mb-3">
                      <button
                        className={`px-3 py-1 text-[11px] font-medium rounded-lg transition-colors ${
                          advancedTab === 'compose'
                            ? 'bg-primary/10 text-primary'
                            : 'text-muted-foreground hover:text-foreground hover:bg-secondary/50'
                        }`}
                        onClick={() => setAdvancedTab('compose')}
                      >
                        docker-compose.yml
                      </button>
                      <button
                        className={`px-3 py-1 text-[11px] font-medium rounded-lg transition-colors ${
                          advancedTab === 'env'
                            ? 'bg-primary/10 text-primary'
                            : 'text-muted-foreground hover:text-foreground hover:bg-secondary/50'
                        }`}
                        onClick={() => setAdvancedTab('env')}
                      >
                        .env
                      </button>
                    </div>

                    {advancedTab === 'compose' && (
                      <textarea
                        className="w-full h-64 rounded-xl bg-[#1e1e2e] text-[#cdd6f4] font-mono text-[11px] p-4 leading-relaxed resize-y border-0 focus:outline-none focus:ring-1 focus:ring-primary/30"
                        value={customCompose}
                        onChange={(e) => setCustomCompose(e.target.value)}
                        spellCheck={false}
                      />
                    )}

                    {advancedTab === 'env' && (
                      <textarea
                        className="w-full h-48 rounded-xl bg-[#1e1e2e] text-[#cdd6f4] font-mono text-[11px] p-4 leading-relaxed resize-y border-0 focus:outline-none focus:ring-1 focus:ring-primary/30"
                        value={customEnv}
                        onChange={(e) => setCustomEnv(e.target.value)}
                        placeholder="KEY=value&#10;DB_PASSWORD=secret&#10;PORT=8080"
                        spellCheck={false}
                      />
                    )}
                  </div>
                )}

                <div className="flex gap-3">
                  <Button className="rounded-xl px-5" size="sm" onClick={handleInstall} disabled={installing}>
                    {installing ? (
                      <>
                        <Loader2 className="h-4 w-4 animate-spin mr-1.5" />
                        {t('appStore.installing')}
                      </>
                    ) : (
                      <>
                        <Download className="h-4 w-4 mr-1.5" />
                        {t('appStore.confirmInstall')}
                      </>
                    )}
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    className="rounded-xl"
                    onClick={() => setShowInstallForm(false)}
                    disabled={installing}
                  >
                    {t('appStore.cancel')}
                  </Button>
                </div>
              </div>
            )}

            {/* Installation Progress */}
            {showProgress && (
              <div className="bg-secondary/20 rounded-xl p-5 animate-in slide-in-from-top-2 duration-200">
                <div className="flex items-center justify-between mb-4">
                  <h3 className="text-[14px] font-semibold">
                    {progressDone
                      ? progressSuccess
                        ? t('appStore.installComplete')
                        : t('appStore.installFailed')
                      : t('appStore.installing')}
                  </h3>
                  {progressDone && (
                    progressSuccess ? (
                      <CheckCircle2 className="h-5 w-5 text-[#00c471]" />
                    ) : (
                      <XCircle className="h-5 w-5 text-[#f04452]" />
                    )
                  )}
                  {!progressDone && (
                    <Loader2 className="h-5 w-5 animate-spin text-primary" />
                  )}
                </div>

                {/* Stage indicators */}
                <div className="flex items-center gap-2 mb-4">
                  {['fetch', 'prepare', 'pull', 'start', 'done'].map((stage) => {
                    const stageLabels: Record<string, string> = {
                      fetch: t('appStore.stageFetch'),
                      prepare: t('appStore.stagePrepare'),
                      pull: t('appStore.stagePull'),
                      start: t('appStore.stageStart'),
                      done: t('appStore.stageDone'),
                    }
                    const stageOrder = ['fetch', 'prepare', 'pull', 'start', 'done']
                    const currentIdx = stageOrder.indexOf(currentStage)
                    const thisIdx = stageOrder.indexOf(stage)
                    const isComplete = thisIdx < currentIdx || (progressDone && progressSuccess)
                    const isCurrent = stage === currentStage && !progressDone
                    const isFailed = progressDone && !progressSuccess && stage === currentStage

                    return (
                      <div key={stage} className="flex items-center gap-1">
                        {isComplete ? (
                          <CheckCircle2 className="h-3.5 w-3.5 text-[#00c471]" />
                        ) : isFailed ? (
                          <XCircle className="h-3.5 w-3.5 text-[#f04452]" />
                        ) : isCurrent ? (
                          <Loader2 className="h-3.5 w-3.5 animate-spin text-primary" />
                        ) : (
                          <Circle className="h-3.5 w-3.5 text-muted-foreground/30" />
                        )}
                        <span className={`text-[11px] ${isCurrent ? 'text-primary font-medium' : isComplete ? 'text-[#00c471]' : isFailed ? 'text-[#f04452]' : 'text-muted-foreground/50'}`}>
                          {stageLabels[stage]}
                        </span>
                        {stage !== 'done' && (
                          <span className="text-muted-foreground/20 mx-1">›</span>
                        )}
                      </div>
                    )
                  })}
                </div>

                {/* Log output */}
                <div className="rounded-xl bg-[#1e1e2e] p-4 max-h-64 overflow-y-auto font-mono text-[11px] leading-relaxed">
                  {progressLogs.map((log, idx) => (
                    <div
                      key={idx}
                      className={`${log.success ? 'text-[#cdd6f4]' : 'text-[#f04452]'}`}
                    >
                      <span className="text-[#89b4fa] select-none">[{log.stage}]</span>{' '}
                      {log.message}
                    </div>
                  ))}
                  <div ref={logEndRef} />
                </div>

                {progressDone && (
                  <div className="flex gap-3 mt-4">
                    <Button
                      size="sm"
                      className="rounded-xl"
                      onClick={() => {
                        setShowProgress(false)
                        if (progressSuccess) {
                          loadDetail()
                        }
                      }}
                    >
                      {t('common.close')}
                    </Button>
                  </div>
                )}
              </div>
            )}

            {/* Features */}
            {detail.app.features && detail.app.features.length > 0 && (
              <div>
                <h3 className="text-[14px] font-semibold mb-3">{t('appStore.features')}</h3>
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-2.5">
                  {detail.app.features.map((feature, idx) => (
                    <div key={idx} className="flex items-start gap-3 p-3 rounded-xl bg-secondary/20">
                      {feature.icon && (
                        <span className="text-lg shrink-0 mt-0.5">{feature.icon}</span>
                      )}
                      <div className="min-w-0">
                        <h4 className="text-[13px] font-semibold">
                          {feature.title[lang] || feature.title['en'] || ''}
                        </h4>
                        <p className="text-[11px] text-muted-foreground mt-0.5 leading-relaxed">
                          {feature.description[lang] || feature.description['en'] || ''}
                        </p>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* Docker Compose */}
            <div>
              <button
                onClick={() => setShowCompose(!showCompose)}
                className="flex items-center gap-2 text-[14px] font-semibold hover:text-primary transition-colors"
              >
                Docker Compose
                {showCompose ? <ChevronUp className="h-4 w-4" /> : <ChevronDown className="h-4 w-4" />}
              </button>
              {showCompose && (
                <div className="rounded-xl bg-[#1e1e2e] overflow-hidden mt-2">
                  <pre className="p-4 text-[12px] leading-relaxed overflow-x-auto font-mono text-[#cdd6f4]">
                    {detail.compose}
                  </pre>
                </div>
              )}
            </div>

            {/* README */}
            {detail.readme && (
              <div>
                <h3 className="text-[14px] font-semibold mb-3">{t('appStore.about')}</h3>
                <RenderedReadme
                  markdown={detail.readme}
                  baseUrl={detail.readme_base_url}
                />
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
