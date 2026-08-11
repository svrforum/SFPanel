import { useState, useEffect, useCallback, useRef, useMemo } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import {
  Eye,
  EyeOff,
  Loader2,
  RefreshCw,
  Package,
  Globe,
  Code2,
  Download,
  Check,
  Copy,
  ChevronDown,
  ChevronUp,
  Trash2,
} from 'lucide-react'
import { toast } from 'sonner'
import { copyText } from '@/lib/utils'
import { usePrompt } from '@/components/PromptDialog'
import { useConfirm } from '@/components/ConfirmDialog'
import { api } from '@/lib/api'
import { appStoreIconUrl } from '@/lib/appstore'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog'
import ComposeEditor from '@/components/compose/ComposeEditor'
import { RenderedReadme } from '@/pages/appstore/components/RenderedReadme'
import { InstallProgressPanel, type InstallLogLine } from '@/pages/appstore/components/InstallProgressPanel'
import type { AppStoreAppDetail } from '@/types/api'

function generatePassword(): string {
  const bytes = new Uint8Array(16)
  crypto.getRandomValues(bytes)
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
}

// Shape of the install SSE events emitted by /appstore/apps/:id/install
interface InstallEvent {
  stage: string
  message: string
  success: boolean
  done?: boolean
  health?: string
}

interface AppStoreDetailModalProps {
  appId: string | null
  open: boolean
  onClose: () => void
  onInstalled: () => void
}

export default function AppStoreDetailModal({ appId, open, onClose, onInstalled }: AppStoreDetailModalProps) {
  const { t, i18n } = useTranslation()
  const prompt = usePrompt()
  const confirm = useConfirm()
  const navigate = useNavigate()
  const lang = i18n.language.startsWith('ko') ? 'ko' : 'en'

  const [detail, setDetail] = useState<AppStoreAppDetail | null>(null)
  const [loading, setLoading] = useState(false)
  const [envValues, setEnvValues] = useState<Record<string, string>>({})
  const [installing, setInstalling] = useState(false)
  const [showPasswords, setShowPasswords] = useState<Record<string, boolean>>({})
  const [showInstallForm, setShowInstallForm] = useState(false)
  const [showCompose, setShowCompose] = useState(false)
  const [showProgress, setShowProgress] = useState(false)
  const [progressLogs, setProgressLogs] = useState<InstallLogLine[]>([])
  const [progressDone, setProgressDone] = useState(false)
  const [progressSuccess, setProgressSuccess] = useState(false)
  const [installHealth, setInstallHealth] = useState<string>('')
  const [currentStage, setCurrentStage] = useState('')
  const [installMode, setInstallMode] = useState<'simple' | 'advanced'>('simple')
  const [customCompose, setCustomCompose] = useState('')
  const [customEnv, setCustomEnv] = useState('')
  const [advancedTab, setAdvancedTab] = useState<'compose' | 'env'>('compose')
  const [copiedKey, setCopiedKey] = useState<string | null>(null)
  const [uninstalling, setUninstalling] = useState(false)
  const [keepData, setKeepData] = useState(false)
  const abortRef = useRef<AbortController | null>(null)

  const getIconUrl = () => {
    if (!detail) return ''
    return appStoreIconUrl(detail.app.id, detail.app.icon)
  }

  const copyToClipboard = async (text: string, key: string) => {
    if (!(await copyText(text))) {
      toast.error('Failed to copy to clipboard')
      return
    }
    setCopiedKey(key)
    toast.success(t('appStore.copied'))
    setTimeout(() => setCopiedKey((cur) => (cur === key ? null : cur)), 2000)
  }

  // Resolve the external access port the user is installing on: prefer the
  // first `port`-typed env field value, fall back to the app's first declared
  // port. Used for the access-URL preview and the post-install "Open app" link.
  const installPort = useMemo(() => {
    if (!detail) return ''
    const portDef = detail.app.env.find((e) => e.type === 'port')
    if (portDef) {
      const v = (envValues[portDef.key] ?? '').trim()
      if (v) return v
    }
    if (detail.app.ports.length > 0) return String(detail.app.ports[0])
    return ''
  }, [detail, envValues])

  const accessUrl = installPort
    ? `${window.location.protocol}//${window.location.hostname}:${installPort}`
    : ''

  const handleUninstall = async () => {
    if (!detail) return
    const ok = await confirm({
      title: t('appStore.uninstallTitle', { name: detail.app.name }),
      description: keepData
        ? t('appStore.uninstallConfirmKeep', { name: detail.app.name })
        : t('appStore.uninstallConfirm', { name: detail.app.name }),
      confirmLabel: t('appStore.uninstall'),
      danger: true,
    })
    if (!ok) return
    setUninstalling(true)
    try {
      await api.uninstallApp(detail.app.id, keepData)
      toast.success(t('appStore.uninstallSuccess', { name: detail.app.name }))
      setKeepData(false)
      onInstalled()
      loadDetail()
    } catch {
      toast.error(t('appStore.uninstallFailed'))
    } finally {
      setUninstalling(false)
    }
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
    setInstallHealth('')
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

  // Abort any in-flight install SSE stream when the modal closes/unmounts.
  // (Escape handling, backdrop, focus trap and scroll lock come from Radix.)
  useEffect(() => {
    if (!open && abortRef.current) {
      abortRef.current.abort()
      abortRef.current = null
    }
    return () => {
      if (abortRef.current) {
        abortRef.current.abort()
        abortRef.current = null
      }
    }
  }, [open])

  const handleInstall = async () => {
    if (!detail) return

    // Advanced mode requires step-up re-auth: the same JWT that loaded the
    // page is not sufficient to push arbitrary compose YAML through to
    // docker compose up. validateAdvancedCompose blocks obvious host-escape
    // shapes (privileged, pid: host, etc.), but a fresh password confirms
    // it is the operator at the keyboard, not a borrowed session.
    let advancedPassword = ''
    if (installMode === 'advanced') {
      const entered = await prompt({ title: t('appStore.advancedReAuthPrompt'), password: true })
      if (entered === null || entered === '') {
        return
      }
      advancedPassword = entered
    }

    setInstalling(true)
    setShowProgress(true)
    setProgressLogs([])
    setProgressDone(false)
    setProgressSuccess(false)
    setInstallHealth('')
    setCurrentStage('')
    setShowInstallForm(false)

    const controller = new AbortController()
    abortRef.current = controller
    // Flips once the SSE stream delivers events — a throw before that is a
    // pre-flight rejection and should restore the install form.
    let sawEvent = false

    try {
      await api.installAppStream<InstallEvent>(
        detail.app.id,
        installMode === 'advanced'
          ? { advanced: true, compose: customCompose, env_raw: customEnv, password: advancedPassword }
          : { env: envValues },
        (event) => {
          sawEvent = true
          setCurrentStage(event.stage)
          setProgressLogs(prev => [...prev, {
            stage: event.stage,
            message: event.message,
            success: event.success,
          }])
          if (event.done) {
            setProgressDone(true)
            setProgressSuccess(event.success)
            if (event.health) setInstallHealth(event.health)
            if (event.success) {
              toast.success(t('appStore.installSuccess', { name: detail.app.name }))
              onInstalled()
            } else {
              toast.error(t('appStore.installFailed'))
            }
          }
        },
        controller.signal,
      )
    } catch (err) {
      if (err instanceof DOMException && err.name === 'AbortError') {
        // User closed modal during install — no toast needed
        return
      }
      if (!sawEvent) {
        // Pre-flight check failed (JSON error before any SSE) — keep the
        // code-specific toasts, then restore the form.
        const e = err as Error & { code?: string }
        const msg = e.message || t('appStore.installFailed')
        if (e.code === 'PORT_CONFLICT') {
          toast.error(t('appStore.portConflict') + ': ' + msg.replace('Port conflict: ', ''))
        } else if (e.code === 'CONTAINER_CONFLICT') {
          toast.error(t('appStore.containerConflict') + ': ' + msg.replace('Container name conflict: ', ''))
        } else if (e.code === 'ALREADY_EXISTS') {
          toast.error(t('appStore.alreadyInstalled'))
        } else {
          toast.error(msg)
        }
        setShowProgress(false)
        setShowInstallForm(true)
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

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o && !installing) onClose() }}>
      <DialogContent className="sm:max-w-2xl rounded-2xl p-0 gap-0" aria-describedby={undefined}>
        <DialogTitle className="sr-only">{detail?.app.name ?? t('appStore.title')}</DialogTitle>

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
                  {detail.installed && detail.installed_at ? (
                    <span className="inline-flex items-center px-2.5 py-0.5 rounded-full text-[11px] font-medium bg-secondary/60 text-muted-foreground">
                      {t('appStore.installedOn', { date: new Date(detail.installed_at).toLocaleDateString() })}
                    </span>
                  ) : null}
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

                <div className="flex flex-wrap items-center gap-3 mt-4">
                  {detail.installed ? (
                    <>
                      <span className="inline-flex items-center gap-1.5 px-4 py-2 rounded-xl text-[13px] font-medium bg-success/10 text-success">
                        <Check className="h-4 w-4" />
                        {t('appStore.installed')}
                      </span>
                      <label className="flex items-center gap-2 text-[13px] text-muted-foreground mb-2 cursor-pointer">
                        <input type="checkbox" checked={keepData} onChange={(e) => setKeepData(e.target.checked)} />
                        {t('appStore.keepData')}
                      </label>
                      <Button
                        variant="ghost"
                        size="sm"
                        className="rounded-xl text-destructive hover:text-destructive hover:bg-destructive/10"
                        onClick={handleUninstall}
                        disabled={uninstalling}
                      >
                        {uninstalling ? (
                          <Loader2 className="h-4 w-4 mr-1.5 animate-spin" />
                        ) : (
                          <Trash2 className="h-4 w-4 mr-1.5" />
                        )}
                        {t('appStore.uninstall')}
                      </Button>
                    </>
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
                      className="inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-xl text-[12px] text-muted-foreground hover:text-foreground hover:bg-secondary/50 transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
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
                      className="inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-xl text-[12px] text-muted-foreground hover:text-foreground hover:bg-secondary/50 transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
                    >
                      <Code2 className="h-3.5 w-3.5" />
                      {t('appStore.source')}
                    </a>
                  )}
                </div>
              </div>
            </div>

            {/* Screenshots */}
            {detail.app.screenshots && detail.app.screenshots.length > 0 && (
              <div>
                <h3 className="text-[14px] font-semibold mb-3">{t('appStore.screenshots')}</h3>
                <div className="flex gap-3 overflow-x-auto pb-2 -mx-1 px-1">
                  {detail.app.screenshots.map((src, idx) => (
                    <a
                      key={idx}
                      href={src}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="shrink-0 rounded-xl outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
                    >
                      <img
                        src={src}
                        alt={`${detail.app.name} screenshot ${idx + 1}`}
                        loading="lazy"
                        className="h-40 w-auto rounded-xl border border-border object-cover hover:opacity-90 transition-opacity"
                        onError={(e) => {
                          const target = e.currentTarget
                          const wrapper = target.parentElement
                          if (wrapper) wrapper.style.display = 'none'
                        }}
                      />
                    </a>
                  ))}
                </div>
              </div>
            )}

            {/* Install Form */}
            {showInstallForm && !detail.installed && (
              <div className="bg-secondary/20 rounded-xl p-5 animate-in slide-in-from-top-2 duration-200">
                <h3 className="text-[14px] font-semibold mb-4">{t('appStore.installTitle', { name: detail.app.name })}</h3>

                {/* Mode tabs */}
                <div className="flex gap-1 p-1 bg-secondary/40 rounded-xl mb-4">
                  <button
                    className={`flex-1 py-1.5 text-[12px] font-medium rounded-lg transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0 ${
                      installMode === 'simple'
                        ? 'bg-background text-foreground shadow-sm'
                        : 'text-muted-foreground hover:text-foreground'
                    }`}
                    onClick={() => setInstallMode('simple')}
                  >
                    {t('appStore.simpleMode')}
                  </button>
                  <button
                    className={`flex-1 py-1.5 text-[12px] font-medium rounded-lg transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0 ${
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
                          {envDef.required && <span className="text-destructive ml-0.5">*</span>}
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
                          <>
                          <div className="flex flex-wrap gap-2">
                            <div className="relative flex-1 min-w-[180px]">
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
                                className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
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
                              <>
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
                                <Button
                                  type="button"
                                  variant="outline"
                                  size="sm"
                                  className="rounded-xl text-[11px] shrink-0"
                                  onClick={() => copyToClipboard(envValues[envDef.key] || '', `env-${envDef.key}`)}
                                >
                                  {copiedKey === `env-${envDef.key}` ? (
                                    <Check className="h-3 w-3 mr-1 text-success" />
                                  ) : (
                                    <Copy className="h-3 w-3 mr-1" />
                                  )}
                                  {t('appStore.copy')}
                                </Button>
                              </>
                            )}
                          </div>
                          {envDef.generate && (
                            <p className="text-[11px] text-muted-foreground">
                              {t('appStore.passwordStoredNote')}
                            </p>
                          )}
                          </>
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
                                  <p className="text-[11px] text-warning mt-1">
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
                    {/* Safety reminder — advanced mode hands arbitrary YAML
                        to `docker compose up -d` running as root. The server
                        rejects the most dangerous patterns (privileged, host
                        namespaces, bind mounts into system paths, docker.sock)
                        but a broken file can still brick the stack. */}
                    <div className="mb-3 rounded-lg border border-yellow-500/40 bg-yellow-500/10 p-3 text-[11px] text-yellow-900 dark:text-yellow-100">
                      <Trans
                        i18nKey="appStore.advancedWarning"
                        defaults="⚠️ Advanced mode runs arbitrary Docker Compose YAML on the host with root privileges. Dangerous patterns (privileged containers, host namespaces, bind mounts into <code>/etc</code>, <code>/root</code>, <code>/var/lib/sfpanel</code>, the Docker socket, etc.) are rejected server-side. Review your file before installing."
                        components={{ code: <code /> }}
                      />
                    </div>
                    {/* Sub-tabs */}
                    <div className="flex gap-1 mb-3">
                      <button
                        className={`px-3 py-1 text-[11px] font-medium rounded-lg transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0 ${
                          advancedTab === 'compose'
                            ? 'bg-primary/10 text-primary'
                            : 'text-muted-foreground hover:text-foreground hover:bg-secondary/50'
                        }`}
                        onClick={() => setAdvancedTab('compose')}
                      >
                        docker-compose.yml
                      </button>
                      <button
                        className={`px-3 py-1 text-[11px] font-medium rounded-lg transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0 ${
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
                      <div className="rounded-xl overflow-hidden border">
                        <ComposeEditor value={customCompose} onChange={setCustomCompose} height="256px" />
                      </div>
                    )}

                    {advancedTab === 'env' && (
                      <div className="rounded-xl overflow-hidden border">
                        <ComposeEditor value={customEnv} onChange={setCustomEnv} language="ini" height="192px" />
                      </div>
                    )}
                  </div>
                )}

                {installMode === 'simple' && accessUrl && (
                  <p className="text-[11px] text-muted-foreground mb-3 break-all">
                    {t('appStore.accessAfterInstall')}{' '}
                    <span className="font-mono text-foreground">{accessUrl}</span>
                  </p>
                )}

                <div className="flex flex-wrap gap-3">
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
              <InstallProgressPanel
                logs={progressLogs}
                done={progressDone}
                success={progressSuccess}
                health={installHealth}
                currentStage={currentStage}
                installPort={installPort}
                onManage={() => {
                  onClose()
                  navigate(`/docker/stacks/${detail.app.id}`)
                }}
                onClose={() => {
                  setShowProgress(false)
                  if (progressSuccess) {
                    loadDetail()
                  }
                }}
              />
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
                className="flex items-center gap-2 text-[14px] font-semibold hover:text-primary transition-colors rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
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
      </DialogContent>
    </Dialog>
  )
}
