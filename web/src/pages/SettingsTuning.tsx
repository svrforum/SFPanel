import { useState, useEffect, useRef, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
import type { TuningStatus, TuningCategory } from '@/types/api'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { ChevronDown, ChevronRight, Check, Minus, RotateCcw, Zap, Shield, HardDrive, Cpu, MemoryStick, Network, Info, AlertTriangle, Timer } from 'lucide-react'

const CATEGORY_META: Record<string, { icon: typeof Network; color: string }> = {
  network:    { icon: Network,    color: '#3182f6' },
  memory:     { icon: MemoryStick, color: '#00c471' },
  filesystem: { icon: HardDrive,  color: '#f59e0b' },
  security:   { icon: Shield,     color: '#f04452' },
}

export default function SettingsTuning() {
  const { t } = useTranslation()
  const [status, setStatus] = useState<TuningStatus | null>(null)
  const [loading, setLoading] = useState(true)
  const [applying, setApplying] = useState<string | null>(null) // category name or 'all'
  const [resetting, setResetting] = useState(false)
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})
  const [countdown, setCountdown] = useState(0) // rollback countdown seconds
  const [confirming, setConfirming] = useState(false)
  const countdownRef = useRef<ReturnType<typeof setInterval> | null>(null)

  const startCountdown = useCallback((seconds: number) => {
    if (countdownRef.current) clearInterval(countdownRef.current)
    setCountdown(seconds)
    countdownRef.current = setInterval(() => {
      setCountdown(prev => {
        if (prev <= 1) {
          if (countdownRef.current) clearInterval(countdownRef.current)
          countdownRef.current = null
          // Rollback happened on server — reload status
          loadStatus()
          toast.error(t('settings.tuning.rollbackTriggered'))
          return 0
        }
        return prev - 1
      })
    }, 1000)
  }, [])

  useEffect(() => {
    loadStatus()
    return () => {
      if (countdownRef.current) clearInterval(countdownRef.current)
    }
  }, [])

  async function loadStatus() {
    setLoading(true)
    try {
      const data = await api.getTuningStatus()
      setStatus(data)
      // Restore countdown if there's a pending rollback on the server
      if (data.pending_rollback && data.rollback_remaining > 0 && countdown === 0) {
        startCountdown(data.rollback_remaining)
      }
    } catch {
      toast.error(t('settings.tuning.loadFailed'))
    } finally {
      setLoading(false)
    }
  }

  async function handleApply(categories?: string[]) {
    const key = categories?.[0] || 'all'
    setApplying(key)
    try {
      const result = await api.applyTuning(categories)
      toast.success(t('settings.tuning.applySuccess'))
      // Start countdown for auto-rollback
      if (result.timeout) {
        startCountdown(result.timeout)
      }
      await loadStatus()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('settings.tuning.applyFailed')
      toast.error(message)
    } finally {
      setApplying(null)
    }
  }

  async function handleConfirm() {
    setConfirming(true)
    try {
      await api.confirmTuning()
      if (countdownRef.current) clearInterval(countdownRef.current)
      countdownRef.current = null
      setCountdown(0)
      toast.success(t('settings.tuning.confirmSuccess'))
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('settings.tuning.confirmFailed')
      toast.error(message)
    } finally {
      setConfirming(false)
    }
  }

  async function handleReset() {
    if (!window.confirm(t('settings.tuning.resetConfirm'))) return
    setResetting(true)
    try {
      await api.resetTuning()
      toast.success(t('settings.tuning.resetSuccess'))
      await loadStatus()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('settings.tuning.resetFailed')
      toast.error(message)
    } finally {
      setResetting(false)
    }
  }

  function toggleExpand(name: string) {
    setExpanded(prev => ({ ...prev, [name]: !prev[name] }))
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center py-12">
        <div className="h-6 w-6 border-2 border-primary border-t-transparent rounded-full animate-spin" />
      </div>
    )
  }

  if (!status) return null

  const overallPercent = status.total_params > 0
    ? Math.round((status.applied / status.total_params) * 100)
    : 0

  return (
    <div className="space-y-6">
      {/* System Specs */}
      <div className="bg-card rounded-2xl p-5 card-shadow">
        <div className="flex items-center gap-2 mb-4">
          <Cpu className="h-4 w-4 text-muted-foreground" />
          <h3 className="text-[15px] font-semibold">{t('settings.tuning.systemSpecs')}</h3>
        </div>
        <div className="grid grid-cols-3 gap-4">
          <div className="space-y-1">
            <p className="text-[11px] text-muted-foreground uppercase tracking-wider">CPU</p>
            <p className="text-[13px] font-medium">{status.system_info.cpu_cores} {t('settings.tuning.cores')}</p>
          </div>
          <div className="space-y-1">
            <p className="text-[11px] text-muted-foreground uppercase tracking-wider">RAM</p>
            <p className="text-[13px] font-medium">{formatBytes(status.system_info.total_ram)}</p>
          </div>
          <div className="space-y-1">
            <p className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('settings.tuning.kernel')}</p>
            <p className="text-[13px] font-medium">{status.system_info.kernel}</p>
          </div>
        </div>
      </div>

      {/* Overall Progress */}
      <div className="bg-card rounded-2xl p-5 card-shadow">
        <div className="flex items-center justify-between mb-3">
          <div className="flex items-center gap-2">
            <Zap className="h-4 w-4 text-[#3182f6]" />
            <h3 className="text-[15px] font-semibold">{t('settings.tuning.optimizationStatus')}</h3>
          </div>
          <span className="text-[13px] font-semibold text-[#3182f6]">
            {status.applied} / {status.total_params}
          </span>
        </div>
        <div className="h-2 rounded-full bg-secondary overflow-hidden mb-4">
          <div
            className="h-full rounded-full transition-all duration-500"
            style={{
              width: `${overallPercent}%`,
              backgroundColor: overallPercent === 100 ? '#00c471' : overallPercent > 50 ? '#3182f6' : '#f59e0b',
            }}
          />
        </div>
        <div className="flex gap-2">
          <Button
            onClick={() => handleApply()}
            disabled={applying !== null || countdown > 0 || status.applied === status.total_params}
            className="rounded-xl"
          >
            <Zap className="h-4 w-4 mr-2" />
            {applying === 'all' ? t('settings.tuning.applying') : t('settings.tuning.applyAll')}
          </Button>
          <Button
            variant="outline"
            onClick={handleReset}
            disabled={resetting || countdown > 0}
            className="rounded-xl"
          >
            <RotateCcw className="h-4 w-4 mr-2" />
            {resetting ? t('settings.tuning.resettingDefaults') : t('settings.tuning.resetDefaults')}
          </Button>
        </div>

        {/* Rollback Countdown Banner */}
        {countdown > 0 && (
          <div className="mt-4 bg-[#f59e0b]/10 border border-[#f59e0b]/30 rounded-xl p-4">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-3">
                <Timer className="h-5 w-5 text-[#f59e0b] animate-pulse" />
                <div>
                  <p className="text-[13px] font-semibold">{t('settings.tuning.rollbackCountdown', { seconds: countdown })}</p>
                  <p className="text-[11px] text-muted-foreground">{t('settings.tuning.rollbackHint')}</p>
                </div>
              </div>
              <Button
                onClick={handleConfirm}
                disabled={confirming}
                className="rounded-xl bg-[#00c471] hover:bg-[#00c471]/90"
              >
                <Check className="h-4 w-4 mr-2" />
                {confirming ? t('settings.tuning.confirming') : t('settings.tuning.keepChanges')}
              </Button>
            </div>
            <div className="mt-3 h-1.5 rounded-full bg-secondary overflow-hidden">
              <div
                className="h-full rounded-full bg-[#f59e0b] transition-all duration-1000"
                style={{ width: `${(countdown / 60) * 100}%` }}
              />
            </div>
          </div>
        )}
      </div>

      {/* Category Cards */}
      {status.categories.map((cat: TuningCategory) => {
        const meta = CATEGORY_META[cat.name] || { icon: Zap, color: '#3182f6' }
        const Icon = meta.icon
        const isExpanded = expanded[cat.name] || false
        const allApplied = cat.applied === cat.total

        return (
          <div key={cat.name} className="bg-card rounded-2xl card-shadow overflow-hidden">
            {/* Category Header */}
            <div
              className="flex items-center justify-between p-5 cursor-pointer hover:bg-secondary/30 transition-colors"
              onClick={() => toggleExpand(cat.name)}
            >
              <div className="flex items-center gap-3">
                <div
                  className="h-8 w-8 rounded-lg flex items-center justify-center"
                  style={{ backgroundColor: `${meta.color}15` }}
                >
                  <Icon className="h-4 w-4" style={{ color: meta.color }} />
                </div>
                <div>
                  <h3 className="text-[15px] font-semibold">{t(`settings.tuning.cat.${cat.name}`)}</h3>
                  <p className="text-[11px] text-muted-foreground">
                    {t(`settings.tuning.catDesc.${cat.name}`)}
                  </p>
                </div>
              </div>

              <div className="flex items-center gap-3">
                {allApplied ? (
                  <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#00c471]/10 text-[#00c471]">
                    <Check className="h-3 w-3 mr-1" />
                    {t('settings.tuning.optimized')}
                  </span>
                ) : (
                  <>
                    <span className="text-[12px] text-muted-foreground">
                      {cat.applied}/{cat.total}
                    </span>
                    <Button
                      size="sm"
                      variant="outline"
                      className="rounded-lg h-7 text-[12px]"
                      disabled={applying !== null}
                      onClick={(e) => {
                        e.stopPropagation()
                        handleApply([cat.name])
                      }}
                    >
                      {applying === cat.name ? t('settings.tuning.applying') : t('settings.tuning.apply')}
                    </Button>
                  </>
                )}
                {isExpanded
                  ? <ChevronDown className="h-4 w-4 text-muted-foreground" />
                  : <ChevronRight className="h-4 w-4 text-muted-foreground" />
                }
              </div>
            </div>

            {/* Parameter List */}
            {isExpanded && (
              <div className="border-t border-border">
                {/* Benefit & Caution */}
                <div className="px-5 py-3 bg-secondary/20 space-y-2">
                  <div className="flex items-start gap-2">
                    <Info className="h-3.5 w-3.5 text-[#3182f6] mt-0.5 shrink-0" />
                    <p className="text-[12px] text-foreground/80">{t(`settings.tuning.${cat.benefit}`)}</p>
                  </div>
                  <div className="flex items-start gap-2">
                    <AlertTriangle className="h-3.5 w-3.5 text-[#f59e0b] mt-0.5 shrink-0" />
                    <p className="text-[12px] text-foreground/80">{t(`settings.tuning.${cat.caution}`)}</p>
                  </div>
                </div>
                <div className="divide-y divide-border">
                  {cat.params.map(param => (
                    <div key={param.key} className="px-5 py-3 flex items-center justify-between gap-4">
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2">
                          <code className="text-[12px] font-mono text-foreground/80">{param.key}</code>
                          {param.applied ? (
                            <Check className="h-3.5 w-3.5 text-[#00c471] shrink-0" />
                          ) : (
                            <Minus className="h-3.5 w-3.5 text-[#f59e0b] shrink-0" />
                          )}
                        </div>
                        <p className="text-[11px] text-muted-foreground mt-0.5">{param.description}</p>
                      </div>
                      <div className="text-right shrink-0">
                        <div className="flex items-center gap-2 text-[12px]">
                          <span className="text-muted-foreground">{param.current || '-'}</span>
                          <span className="text-muted-foreground/50">→</span>
                          <span className={param.applied ? 'text-[#00c471] font-medium' : 'text-[#3182f6] font-medium'}>
                            {param.recommended}
                          </span>
                        </div>
                      </div>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}
