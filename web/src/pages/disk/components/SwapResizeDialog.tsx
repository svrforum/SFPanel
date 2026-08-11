import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { MemoryStick, Maximize2, ArrowRight, CheckCircle2, XCircle, Loader2, AlertTriangle, HardDrive, Cpu } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

import type { SwapEntry } from '@/types/api'

interface SwapResizeCheck {
  current_size_mb: number
  disk_free_mb: number
  max_size_mb: number
  swap_used_mb: number
  ram_free_mb: number
  swapoff_safe: boolean
}

interface ResizeStep {
  name: string
  status: string
  output: string
}

/**
 * Swap-file resize wizard: config phase (constraint check, slider + presets +
 * manual input) then progress phase (per-step success/failure log). The
 * dialog can't be dismissed while the resize is running.
 */
export function SwapResizeDialog({ target, onOpenChange, onResized }: {
  /** Swap file to resize; null keeps the dialog closed. */
  target: SwapEntry | null
  onOpenChange: (open: boolean) => void
  /** Called after a successful resize so the parent can refresh. */
  onResized: () => void
}) {
  const { t } = useTranslation()
  const [sizeMB, setSizeMB] = useState('')
  const [phase, setPhase] = useState<'config' | 'progress'>('config')
  const [steps, setSteps] = useState<ResizeStep[]>([])
  const [resizing, setResizing] = useState(false)
  const [check, setCheck] = useState<SwapResizeCheck | null>(null)
  const [checkLoading, setCheckLoading] = useState(false)

  // Reset before paint when a new target opens (adjust-state-during-render,
  // same pattern as TypeToConfirmDialog).
  const [prevTarget, setPrevTarget] = useState<SwapEntry | null>(target)
  if (prevTarget !== target) {
    setPrevTarget(target)
    if (target) {
      setSizeMB(Math.round(target.size / 1024 / 1024).toString())
      setPhase('config')
      setSteps([])
      setCheck(null)
      setCheckLoading(true)
    }
  }

  // Fetch disk/RAM/usage constraints for the opened swap file.
  useEffect(() => {
    if (!target) return
    let cancelled = false
    setCheckLoading(true)
    api.checkSwapResize(target.name)
      .then((c) => {
        if (!cancelled) setCheck(c)
      })
      .catch(() => {
        // ignore, constraints just won't show
      })
      .finally(() => {
        if (!cancelled) setCheckLoading(false)
      })
    return () => { cancelled = true }
  }, [target])

  const close = () => {
    if (!resizing) onOpenChange(false)
  }

  const handleResize = async () => {
    if (!target || !sizeMB.trim()) return
    setResizing(true)
    setPhase('progress')
    setSteps([])
    try {
      const result = await api.resizeSwap({
        path: target.name,
        new_size_mb: parseInt(sizeMB, 10),
      })
      setSteps(result.steps || [])
      if (result.success) {
        toast.success(t('disk.swap.resizeSuccess'))
        onResized()
      }
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.swap.resizeFailed')
      toast.error(message)
    } finally {
      setResizing(false)
    }
  }

  // Derived sizes for the config phase — plain constants instead of an in-JSX IIFE.
  const currentMB = target ? Math.round(target.size / 1024 / 1024) : 0
  const newMB = parseInt(sizeMB, 10) || 0
  const diffMB = newMB - currentMB
  const maxSlider = check ? Math.min(check.max_size_mb, Math.max(currentMB * 4, 16384)) : Math.max(currentMB * 4, 16384)
  const exceedsDisk = check ? newMB > check.max_size_mb : false
  const swapoffUnsafe = check ? !check.swapoff_safe : false

  return (
    <Dialog open={!!target} onOpenChange={(open) => { if (!open) close() }}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t('disk.swap.resizeTitle')}</DialogTitle>
          <DialogDescription>
            <span className="font-mono">{target?.name}</span>
          </DialogDescription>
        </DialogHeader>

        {phase === 'config' ? (
          <div className="space-y-4">
            {/* System constraints */}
            {checkLoading ? (
              <div className="flex items-center gap-2 text-[13px] text-muted-foreground py-2">
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                {t('disk.swap.checkingConstraints')}
              </div>
            ) : check && (
              <div className="grid grid-cols-2 gap-2">
                <div className="bg-secondary/30 rounded-xl py-2.5 px-3">
                  <div className="flex items-center gap-1.5 mb-0.5">
                    <HardDrive className="h-3 w-3 text-muted-foreground" />
                    <span className="text-[11px] text-muted-foreground">{t('disk.swap.diskFree')}</span>
                  </div>
                  <span className="text-[14px] font-bold font-mono">{formatBytes(check.disk_free_mb * 1024 * 1024)}</span>
                </div>
                <div className="bg-secondary/30 rounded-xl py-2.5 px-3">
                  <div className="flex items-center gap-1.5 mb-0.5">
                    <Cpu className="h-3 w-3 text-muted-foreground" />
                    <span className="text-[11px] text-muted-foreground">{t('disk.swap.ramFree')}</span>
                  </div>
                  <span className="text-[14px] font-bold font-mono">{formatBytes(check.ram_free_mb * 1024 * 1024)}</span>
                </div>
                <div className="bg-secondary/30 rounded-xl py-2.5 px-3">
                  <div className="flex items-center gap-1.5 mb-0.5">
                    <MemoryStick className="h-3 w-3 text-muted-foreground" />
                    <span className="text-[11px] text-muted-foreground">{t('disk.swap.swapUsed')}</span>
                  </div>
                  <span className="text-[14px] font-bold font-mono">{formatBytes(check.swap_used_mb * 1024 * 1024)}</span>
                </div>
                <div className="bg-secondary/30 rounded-xl py-2.5 px-3">
                  <div className="flex items-center gap-1.5 mb-0.5">
                    <Maximize2 className="h-3 w-3 text-muted-foreground" />
                    <span className="text-[11px] text-muted-foreground">{t('disk.swap.maxSize')}</span>
                  </div>
                  <span className="text-[14px] font-bold font-mono">{formatBytes(check.max_size_mb * 1024 * 1024)}</span>
                </div>
              </div>
            )}

            {/* Warnings */}
            {swapoffUnsafe && (
              <div className="flex items-start gap-2 bg-destructive/10 border border-destructive/30 rounded-xl px-3 py-2.5">
                <AlertTriangle className="h-4 w-4 text-destructive shrink-0 mt-0.5" />
                <p className="text-[12px] text-destructive">{t('disk.swap.swapoffWarning')}</p>
              </div>
            )}
            {exceedsDisk && (
              <div className="flex items-start gap-2 bg-destructive/10 border border-destructive/30 rounded-xl px-3 py-2.5">
                <AlertTriangle className="h-4 w-4 text-destructive shrink-0 mt-0.5" />
                <p className="text-[12px] text-destructive">{t('disk.swap.exceedsDisk')}</p>
              </div>
            )}

            {/* Visual size comparison */}
            <div className="flex items-center justify-center gap-3">
              <div className="text-center">
                <div className="text-[11px] text-muted-foreground mb-1">{t('disk.swap.currentSize')}</div>
                <div className="text-xl font-bold font-mono">{formatBytes(target?.size ?? 0)}</div>
              </div>
              <ArrowRight className="h-5 w-5 text-muted-foreground shrink-0" />
              <div className="text-center">
                <div className="text-[11px] text-muted-foreground mb-1">{t('disk.swap.newSizeMB')}</div>
                <div className={`text-xl font-bold font-mono ${
                  diffMB > 0 ? 'text-success' : diffMB < 0 ? 'text-warning' : ''
                }`}>
                  {newMB > 0 ? formatBytes(newMB * 1024 * 1024) : '—'}
                </div>
              </div>
              {newMB > 0 && diffMB !== 0 && (
                <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium ${
                  diffMB > 0 ? 'bg-success/10 text-success' : 'bg-warning/10 text-warning'
                }`}>
                  {diffMB > 0 ? '+' : ''}{formatBytes(Math.abs(diffMB) * 1024 * 1024)}
                </span>
              )}
            </div>

            {/* Visual bar */}
            <div className="space-y-1.5">
              <div className="h-3 bg-secondary rounded-full overflow-hidden relative">
                <div
                  className="absolute inset-y-0 left-0 bg-primary/30 rounded-full transition-all duration-300"
                  style={{ width: `${Math.min((currentMB / maxSlider) * 100, 100)}%` }}
                />
                {newMB > 0 && (
                  <div
                    className={`absolute inset-y-0 left-0 rounded-full transition-all duration-300 ${
                      exceedsDisk ? 'bg-destructive' : diffMB >= 0 ? 'bg-primary' : 'bg-warning'
                    }`}
                    style={{ width: `${Math.min((newMB / maxSlider) * 100, 100)}%` }}
                  />
                )}
              </div>
              <div className="flex justify-between text-[10px] text-muted-foreground font-mono">
                <span>0</span>
                <span>{formatBytes(maxSlider * 1024 * 1024)}</span>
              </div>
            </div>

            {/* Slider */}
            <input
              type="range"
              min={64}
              max={maxSlider}
              step={64}
              value={newMB || currentMB}
              onChange={(e) => setSizeMB(e.target.value)}
              className="w-full h-2 bg-secondary rounded-full appearance-none cursor-pointer accent-primary outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
            />

            {/* Preset buttons */}
            <div className="flex flex-wrap gap-1.5">
              {[512, 1024, 2048, 4096, 8192, 16384].map((mb) => (
                <button
                  key={mb}
                  type="button"
                  onClick={() => setSizeMB(String(mb))}
                  className={`px-3 py-1.5 rounded-lg text-[12px] font-medium transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0 ${
                    newMB === mb
                      ? 'bg-primary/10 text-primary ring-1 ring-primary/30'
                      : 'bg-secondary/50 text-muted-foreground hover:bg-secondary'
                  }`}
                >
                  {formatBytes(mb * 1024 * 1024)}
                </button>
              ))}
            </div>

            {/* Manual input */}
            <div className="flex items-center gap-2">
              <Input
                type="number"
                min={64}
                placeholder="MB"
                value={sizeMB}
                onChange={(e) => setSizeMB(e.target.value)}
                className="font-mono"
              />
              <span className="text-[13px] text-muted-foreground shrink-0">MB</span>
            </div>
          </div>
        ) : (
          /* Progress phase */
          <div className="space-y-3">
            {resizing && steps.length === 0 && (
              <div className="flex items-center gap-2 text-[13px] text-muted-foreground py-4 justify-center">
                <Loader2 className="h-4 w-4 animate-spin" />
                {t('disk.swap.resizing')}
              </div>
            )}
            {steps.map((step, i) => (
              <div key={i} className="flex items-start gap-3 bg-secondary/20 rounded-xl px-4 py-3">
                <div className="mt-0.5">
                  {step.status === 'success' ? (
                    <CheckCircle2 className="h-4 w-4 text-success" />
                  ) : (
                    <XCircle className="h-4 w-4 text-destructive" />
                  )}
                </div>
                <div className="flex-1 min-w-0">
                  <div className="flex items-center gap-2">
                    <span className="text-[13px] font-semibold font-mono">{step.name}</span>
                    <span className={`inline-flex items-center px-1.5 py-0 rounded text-[10px] font-medium ${
                      step.status === 'success'
                        ? 'bg-success/10 text-success'
                        : 'bg-destructive/10 text-destructive'
                    }`}>
                      {step.status}
                    </span>
                  </div>
                  {step.output && (
                    <pre className="text-[11px] text-muted-foreground mt-1 whitespace-pre-wrap break-all font-mono leading-relaxed max-h-[80px] overflow-y-auto">
                      {step.output}
                    </pre>
                  )}
                </div>
              </div>
            ))}
            {!resizing && steps.length > 0 && (
              <div className={`flex items-center gap-2 justify-center py-2 text-[13px] font-medium ${
                steps.every(s => s.status === 'success') ? 'text-success' : 'text-destructive'
              }`}>
                {steps.every(s => s.status === 'success') ? (
                  <><CheckCircle2 className="h-4 w-4" />{t('disk.swap.resizeSuccess')}</>
                ) : (
                  <><XCircle className="h-4 w-4" />{t('disk.swap.resizeFailed')}</>
                )}
              </div>
            )}
          </div>
        )}

        <DialogFooter>
          {phase === 'config' ? (
            <>
              <Button variant="outline" onClick={close}>
                {t('common.cancel')}
              </Button>
              <Button
                onClick={handleResize}
                disabled={
                  resizing ||
                  !sizeMB.trim() ||
                  parseInt(sizeMB, 10) <= 0 ||
                  (check ? parseInt(sizeMB, 10) > check.max_size_mb : false)
                }
              >
                {t('disk.swap.resize')}
              </Button>
            </>
          ) : (
            <Button variant="outline" onClick={close} disabled={resizing}>
              {t('common.close')}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
