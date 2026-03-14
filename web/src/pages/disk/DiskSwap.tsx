import { useState, useEffect, useCallback } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { Plus, Trash2, RefreshCw, MemoryStick, Save, Maximize2, ArrowRight, CheckCircle2, XCircle, Loader2, AlertTriangle, HardDrive, Cpu } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

interface SwapSummary {
  total: number
  used: number
  free: number
  swappiness: number
}

interface SwapEntry {
  name: string
  type: string
  size: number
  used: number
  priority: number
}

export default function DiskSwap() {
  const { t } = useTranslation()
  const [summary, setSummary] = useState<SwapSummary>({ total: 0, used: 0, free: 0, swappiness: 60 })
  const [entries, setEntries] = useState<SwapEntry[]>([])
  const [loading, setLoading] = useState(true)

  // Swappiness
  const [swappiness, setSwappiness] = useState(60)
  const [savingSwappiness, setSavingSwappiness] = useState(false)

  // Create dialog
  const [createOpen, setCreateOpen] = useState(false)
  const [createMode, setCreateMode] = useState<'file' | 'partition'>('file')
  const [createPath, setCreatePath] = useState('')
  const [createSizeMB, setCreateSizeMB] = useState('')
  const [createDevice, setCreateDevice] = useState('')
  const [creating, setCreating] = useState(false)

  // Remove
  const [removeTarget, setRemoveTarget] = useState<SwapEntry | null>(null)
  const [removing, setRemoving] = useState(false)

  // Resize
  const [resizeTarget, setResizeTarget] = useState<SwapEntry | null>(null)
  const [resizeSizeMB, setResizeSizeMB] = useState('')
  const [resizing, setResizing] = useState(false)
  const [resizeCheck, setResizeCheck] = useState<{
    current_size_mb: number
    disk_free_mb: number
    max_size_mb: number
    swap_used_mb: number
    ram_free_mb: number
    swapoff_safe: boolean
  } | null>(null)
  const [resizeCheckLoading, setResizeCheckLoading] = useState(false)
  const [resizeSteps, setResizeSteps] = useState<Array<{ name: string; status: string; output: string }>>([])
  const [resizePhase, setResizePhase] = useState<'config' | 'progress'>('config')

  const fetchSwap = useCallback(async () => {
    try {
      setLoading(true)
      const data = await api.getSwapInfo()
      setSummary({
        total: data.total ?? 0,
        used: data.used ?? 0,
        free: data.free ?? 0,
        swappiness: data.swappiness ?? 60,
      })
      setSwappiness(data.swappiness ?? 60)
      setEntries(data.entries || [])
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.swap.fetchFailed')
      toast.error(message)
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchSwap()
  }, [fetchSwap])

  const handleSaveSwappiness = async () => {
    setSavingSwappiness(true)
    try {
      await api.setSwappiness(swappiness)
      toast.success(t('disk.swap.swappinessSaved'))
      await fetchSwap()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.swap.swappinessFailed')
      toast.error(message)
    } finally {
      setSavingSwappiness(false)
    }
  }

  const handleCreate = async () => {
    setCreating(true)
    try {
      if (createMode === 'file') {
        if (!createPath.trim() || !createSizeMB.trim()) return
        await api.createSwap({
          type: 'file',
          path: createPath.trim(),
          size_mb: parseInt(createSizeMB, 10),
        })
      } else {
        if (!createDevice.trim()) return
        await api.createSwap({
          type: 'partition',
          device: createDevice.trim(),
        })
      }
      toast.success(t('disk.swap.createSuccess'))
      setCreateOpen(false)
      resetCreateForm()
      await fetchSwap()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.swap.createFailed')
      toast.error(message)
    } finally {
      setCreating(false)
    }
  }

  const handleRemove = async () => {
    if (!removeTarget) return
    setRemoving(true)
    try {
      await api.removeSwap(removeTarget.name)
      toast.success(t('disk.swap.removed'))
      setRemoveTarget(null)
      await fetchSwap()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.swap.removeFailed')
      toast.error(message)
    } finally {
      setRemoving(false)
    }
  }

  const handleResize = async () => {
    if (!resizeTarget || !resizeSizeMB.trim()) return
    setResizing(true)
    setResizePhase('progress')
    setResizeSteps([])
    try {
      const result = await api.resizeSwap({
        path: resizeTarget.name,
        new_size_mb: parseInt(resizeSizeMB, 10),
      })
      setResizeSteps(result.steps || [])
      if (result.success) {
        toast.success(t('disk.swap.resizeSuccess'))
        await fetchSwap()
      }
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.swap.resizeFailed')
      toast.error(message)
    } finally {
      setResizing(false)
    }
  }

  const openResizeDialog = async (entry: SwapEntry) => {
    setResizeTarget(entry)
    setResizeSizeMB(Math.round(entry.size / 1024 / 1024).toString())
    setResizePhase('config')
    setResizeSteps([])
    setResizeCheck(null)
    setResizeCheckLoading(true)
    try {
      const check = await api.checkSwapResize(entry.name)
      setResizeCheck(check)
    } catch {
      // ignore, constraints just won't show
    } finally {
      setResizeCheckLoading(false)
    }
  }

  const closeResizeDialog = () => {
    if (!resizing) {
      setResizeTarget(null)
      setResizeSizeMB('')
      setResizePhase('config')
      setResizeSteps([])
      setResizeCheck(null)
    }
  }

  const resetCreateForm = () => {
    setCreateMode('file')
    setCreatePath('')
    setCreateSizeMB('')
    setCreateDevice('')
  }

  const usedPercent = summary.total > 0 ? (summary.used / summary.total) * 100 : 0

  if (loading) {
    return (
      <div className="flex items-center justify-center py-12 text-muted-foreground">
        {t('common.loading')}
      </div>
    )
  }

  return (
    <div className="space-y-4 mt-4">
      {/* Toolbar */}
      <div className="flex items-center justify-end gap-2">
        <Button variant="outline" size="sm" onClick={fetchSwap} disabled={loading} className="rounded-xl">
          <RefreshCw className={loading ? 'animate-spin' : ''} />
          {t('common.refresh')}
        </Button>
        <Button size="sm" onClick={() => setCreateOpen(true)} className="rounded-xl">
          <Plus />
          {t('disk.swap.createSwap')}
        </Button>
      </div>

      {/* Summary Cards */}
      <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
        <div className="bg-card rounded-2xl card-shadow p-4">
          <div className="flex items-center gap-2 mb-1">
            <MemoryStick className="h-4 w-4 text-primary" />
            <span className="text-[13px] text-muted-foreground">{t('disk.swap.total')}</span>
          </div>
          <div className="text-2xl font-bold">{formatBytes(summary.total)}</div>
        </div>
        <div className="bg-card rounded-2xl card-shadow p-4">
          <div className="flex items-center gap-2 mb-1">
            <MemoryStick className="h-4 w-4 text-[#f59e0b]" />
            <span className="text-[13px] text-muted-foreground">{t('disk.swap.used')}</span>
          </div>
          <div className="text-2xl font-bold">{formatBytes(summary.used)}</div>
          {summary.total > 0 && (
            <div className="mt-2 h-1.5 bg-secondary rounded-full overflow-hidden">
              <div
                className="h-full rounded-full transition-all duration-500"
                style={{
                  width: `${Math.min(usedPercent, 100)}%`,
                  backgroundColor: usedPercent > 80 ? '#f04452' : usedPercent > 50 ? '#f59e0b' : '#3182f6',
                }}
              />
            </div>
          )}
        </div>
        <div className="bg-card rounded-2xl card-shadow p-4">
          <div className="flex items-center gap-2 mb-1">
            <MemoryStick className="h-4 w-4 text-[#00c471]" />
            <span className="text-[13px] text-muted-foreground">{t('disk.swap.free')}</span>
          </div>
          <div className="text-2xl font-bold text-[#00c471]">{formatBytes(summary.free)}</div>
        </div>
      </div>

      {/* Swappiness Control */}
      <div className="bg-card rounded-2xl card-shadow p-5">
        <div className="flex items-center justify-between mb-3">
          <div>
            <h3 className="text-[14px] font-semibold">{t('disk.swap.swappiness')}</h3>
            <p className="text-[12px] text-muted-foreground mt-0.5">{t('disk.swap.swappinessDescription')}</p>
          </div>
          <div className="flex items-center gap-3">
            <span className="text-lg font-bold min-w-[40px] text-right">{swappiness}</span>
            <Button
              variant="outline"
              size="sm"
              onClick={handleSaveSwappiness}
              disabled={savingSwappiness || swappiness === summary.swappiness}
              className="rounded-xl"
            >
              <Save className="h-3.5 w-3.5" />
              {t('common.save')}
            </Button>
          </div>
        </div>
        <div className="flex items-center gap-3">
          <span className="text-xs text-muted-foreground w-4">0</span>
          <input
            type="range"
            min={0}
            max={100}
            value={swappiness}
            onChange={(e) => setSwappiness(parseInt(e.target.value, 10))}
            className="flex-1 h-2 bg-secondary rounded-full appearance-none cursor-pointer accent-primary"
          />
          <span className="text-xs text-muted-foreground w-7">100</span>
        </div>
        <div className="flex items-center justify-between mt-1 text-[11px] text-muted-foreground">
          <span>{t('disk.swap.preferRAM')}</span>
          <span>{t('disk.swap.preferSwap')}</span>
        </div>
      </div>

      {/* Swap Entries Table */}
      <div className="bg-card rounded-2xl card-shadow overflow-hidden">
        <Table>
          <TableHeader>
            <TableRow className="border-border/50">
              <TableHead className="text-[11px]">{t('common.name')}</TableHead>
              <TableHead className="text-[11px]">{t('disk.swap.type')}</TableHead>
              <TableHead className="text-[11px]">{t('disk.swap.size')}</TableHead>
              <TableHead className="text-[11px]">{t('disk.swap.usedCol')}</TableHead>
              <TableHead className="text-[11px]">{t('disk.swap.priority')}</TableHead>
              <TableHead className="text-right text-[11px]">{t('common.actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {entries.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="text-center text-muted-foreground py-8">
                  {t('disk.swap.empty')}
                </TableCell>
              </TableRow>
            )}
            {entries.map((entry) => (
              <TableRow key={entry.name}>
                <TableCell className="font-medium font-mono text-sm max-w-[200px] truncate" title={entry.name}>
                  {entry.name}
                </TableCell>
                <TableCell>
                  <span className="inline-flex items-center px-1.5 py-0 rounded text-[10px] font-medium border border-border">
                    {entry.type}
                  </span>
                </TableCell>
                <TableCell className="text-muted-foreground">{formatBytes(entry.size)}</TableCell>
                <TableCell className="text-muted-foreground">{formatBytes(entry.used)}</TableCell>
                <TableCell className="text-muted-foreground font-mono text-xs">{entry.priority}</TableCell>
                <TableCell className="text-right">
                  <div className="flex items-center justify-end gap-1">
                    {entry.type === 'file' && (
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        title={t('disk.swap.resize')}
                        onClick={() => openResizeDialog(entry)}
                      >
                        <Maximize2 />
                      </Button>
                    )}
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      title={t('disk.swap.remove')}
                      onClick={() => setRemoveTarget(entry)}
                    >
                      <Trash2 />
                    </Button>
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      {/* Create Swap Dialog */}
      <Dialog open={createOpen} onOpenChange={(open) => { setCreateOpen(open); if (!open) resetCreateForm() }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('disk.swap.createSwap')}</DialogTitle>
            <DialogDescription>{t('disk.swap.createDescription')}</DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            {/* Mode Toggle */}
            <div className="space-y-2">
              <Label>{t('disk.swap.createMode')}</Label>
              <div className="flex gap-2">
                <button
                  type="button"
                  onClick={() => setCreateMode('file')}
                  className={`flex-1 rounded-xl px-4 py-2.5 text-[13px] font-medium transition-colors ${
                    createMode === 'file'
                      ? 'bg-primary/10 text-primary ring-1 ring-primary/30'
                      : 'bg-secondary/50 text-muted-foreground hover:bg-secondary'
                  }`}
                >
                  {t('disk.swap.fileBased')}
                </button>
                <button
                  type="button"
                  onClick={() => setCreateMode('partition')}
                  className={`flex-1 rounded-xl px-4 py-2.5 text-[13px] font-medium transition-colors ${
                    createMode === 'partition'
                      ? 'bg-primary/10 text-primary ring-1 ring-primary/30'
                      : 'bg-secondary/50 text-muted-foreground hover:bg-secondary'
                  }`}
                >
                  {t('disk.swap.partitionBased')}
                </button>
              </div>
            </div>

            {createMode === 'file' ? (
              <>
                <div className="space-y-2">
                  <Label htmlFor="swap-path">{t('disk.swap.filePath')}</Label>
                  <Input
                    id="swap-path"
                    placeholder="e.g., /swapfile"
                    value={createPath}
                    onChange={(e) => setCreatePath(e.target.value)}
                  />
                </div>
                <div className="space-y-2">
                  <Label htmlFor="swap-size">{t('disk.swap.sizeMB')}</Label>
                  <Input
                    id="swap-size"
                    type="number"
                    placeholder="e.g., 2048"
                    value={createSizeMB}
                    onChange={(e) => setCreateSizeMB(e.target.value)}
                  />
                  <p className="text-[11px] text-muted-foreground">{t('disk.swap.sizeHint')}</p>
                </div>
              </>
            ) : (
              <div className="space-y-2">
                <Label htmlFor="swap-device">{t('disk.swap.device')}</Label>
                <Input
                  id="swap-device"
                  placeholder="e.g., /dev/sdb2"
                  value={createDevice}
                  onChange={(e) => setCreateDevice(e.target.value)}
                />
              </div>
            )}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => { setCreateOpen(false); resetCreateForm() }}>
              {t('common.cancel')}
            </Button>
            <Button
              onClick={handleCreate}
              disabled={
                creating ||
                (createMode === 'file' && (!createPath.trim() || !createSizeMB.trim())) ||
                (createMode === 'partition' && !createDevice.trim())
              }
            >
              {creating ? t('common.creating') : t('common.create')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Remove Swap Dialog */}
      <Dialog open={!!removeTarget} onOpenChange={(open) => !open && setRemoveTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('disk.swap.removeTitle')}</DialogTitle>
            <DialogDescription>
              <Trans
                i18nKey="disk.swap.removeConfirm"
                values={{ name: removeTarget?.name ?? '' }}
                components={{ strong: <span className="font-semibold font-mono" /> }}
              />
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setRemoveTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button variant="destructive" onClick={handleRemove} disabled={removing}>
              {removing ? t('disk.swap.removing') : t('disk.swap.remove')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Resize Swap Dialog */}
      <Dialog open={!!resizeTarget} onOpenChange={(open) => { if (!open) closeResizeDialog() }}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t('disk.swap.resizeTitle')}</DialogTitle>
            <DialogDescription>
              <span className="font-mono">{resizeTarget?.name}</span>
            </DialogDescription>
          </DialogHeader>

          {resizePhase === 'config' ? (() => {
            const currentMB = resizeTarget ? Math.round(resizeTarget.size / 1024 / 1024) : 0
            const newMB = parseInt(resizeSizeMB, 10) || 0
            const diffMB = newMB - currentMB
            const maxSlider = resizeCheck ? Math.min(resizeCheck.max_size_mb, Math.max(currentMB * 4, 16384)) : Math.max(currentMB * 4, 16384)
            const exceedsDisk = resizeCheck ? newMB > resizeCheck.max_size_mb : false
            const swapoffUnsafe = resizeCheck ? !resizeCheck.swapoff_safe : false
            return (
              <div className="space-y-4">
                {/* System constraints */}
                {resizeCheckLoading ? (
                  <div className="flex items-center gap-2 text-[13px] text-muted-foreground py-2">
                    <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    {t('disk.swap.checkingConstraints')}
                  </div>
                ) : resizeCheck && (
                  <div className="grid grid-cols-2 gap-2">
                    <div className="bg-secondary/30 rounded-xl py-2.5 px-3">
                      <div className="flex items-center gap-1.5 mb-0.5">
                        <HardDrive className="h-3 w-3 text-muted-foreground" />
                        <span className="text-[11px] text-muted-foreground">{t('disk.swap.diskFree')}</span>
                      </div>
                      <span className="text-[14px] font-bold font-mono">{formatBytes(resizeCheck.disk_free_mb * 1024 * 1024)}</span>
                    </div>
                    <div className="bg-secondary/30 rounded-xl py-2.5 px-3">
                      <div className="flex items-center gap-1.5 mb-0.5">
                        <Cpu className="h-3 w-3 text-muted-foreground" />
                        <span className="text-[11px] text-muted-foreground">{t('disk.swap.ramFree')}</span>
                      </div>
                      <span className="text-[14px] font-bold font-mono">{formatBytes(resizeCheck.ram_free_mb * 1024 * 1024)}</span>
                    </div>
                    <div className="bg-secondary/30 rounded-xl py-2.5 px-3">
                      <div className="flex items-center gap-1.5 mb-0.5">
                        <MemoryStick className="h-3 w-3 text-muted-foreground" />
                        <span className="text-[11px] text-muted-foreground">{t('disk.swap.swapUsed')}</span>
                      </div>
                      <span className="text-[14px] font-bold font-mono">{formatBytes(resizeCheck.swap_used_mb * 1024 * 1024)}</span>
                    </div>
                    <div className="bg-secondary/30 rounded-xl py-2.5 px-3">
                      <div className="flex items-center gap-1.5 mb-0.5">
                        <Maximize2 className="h-3 w-3 text-muted-foreground" />
                        <span className="text-[11px] text-muted-foreground">{t('disk.swap.maxSize')}</span>
                      </div>
                      <span className="text-[14px] font-bold font-mono">{formatBytes(resizeCheck.max_size_mb * 1024 * 1024)}</span>
                    </div>
                  </div>
                )}

                {/* Warnings */}
                {swapoffUnsafe && (
                  <div className="flex items-start gap-2 bg-[#f04452]/10 border border-[#f04452]/30 rounded-xl px-3 py-2.5">
                    <AlertTriangle className="h-4 w-4 text-[#f04452] shrink-0 mt-0.5" />
                    <p className="text-[12px] text-[#f04452]">{t('disk.swap.swapoffWarning')}</p>
                  </div>
                )}
                {exceedsDisk && (
                  <div className="flex items-start gap-2 bg-[#f04452]/10 border border-[#f04452]/30 rounded-xl px-3 py-2.5">
                    <AlertTriangle className="h-4 w-4 text-[#f04452] shrink-0 mt-0.5" />
                    <p className="text-[12px] text-[#f04452]">{t('disk.swap.exceedsDisk')}</p>
                  </div>
                )}

                {/* Visual size comparison */}
                <div className="flex items-center justify-center gap-3">
                  <div className="text-center">
                    <div className="text-[11px] text-muted-foreground mb-1">{t('disk.swap.currentSize')}</div>
                    <div className="text-xl font-bold font-mono">{formatBytes(resizeTarget?.size ?? 0)}</div>
                  </div>
                  <ArrowRight className="h-5 w-5 text-muted-foreground shrink-0" />
                  <div className="text-center">
                    <div className="text-[11px] text-muted-foreground mb-1">{t('disk.swap.newSizeMB')}</div>
                    <div className={`text-xl font-bold font-mono ${
                      diffMB > 0 ? 'text-[#00c471]' : diffMB < 0 ? 'text-[#f59e0b]' : ''
                    }`}>
                      {newMB > 0 ? formatBytes(newMB * 1024 * 1024) : '—'}
                    </div>
                  </div>
                  {newMB > 0 && diffMB !== 0 && (
                    <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium ${
                      diffMB > 0 ? 'bg-[#00c471]/10 text-[#00c471]' : 'bg-[#f59e0b]/10 text-[#f59e0b]'
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
                          exceedsDisk ? 'bg-[#f04452]' : diffMB >= 0 ? 'bg-[#3182f6]' : 'bg-[#f59e0b]'
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
                  onChange={(e) => setResizeSizeMB(e.target.value)}
                  className="w-full h-2 bg-secondary rounded-full appearance-none cursor-pointer accent-primary"
                />

                {/* Preset buttons */}
                <div className="flex flex-wrap gap-1.5">
                  {[512, 1024, 2048, 4096, 8192, 16384].map((mb) => (
                    <button
                      key={mb}
                      type="button"
                      onClick={() => setResizeSizeMB(String(mb))}
                      className={`px-3 py-1.5 rounded-lg text-[12px] font-medium transition-colors ${
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
                    value={resizeSizeMB}
                    onChange={(e) => setResizeSizeMB(e.target.value)}
                    className="font-mono"
                  />
                  <span className="text-[13px] text-muted-foreground shrink-0">MB</span>
                </div>
              </div>
            )
          })() : (
            /* Progress phase */
            <div className="space-y-3">
              {resizing && resizeSteps.length === 0 && (
                <div className="flex items-center gap-2 text-[13px] text-muted-foreground py-4 justify-center">
                  <Loader2 className="h-4 w-4 animate-spin" />
                  {t('disk.swap.resizing')}
                </div>
              )}
              {resizeSteps.map((step, i) => (
                <div key={i} className="flex items-start gap-3 bg-secondary/20 rounded-xl px-4 py-3">
                  <div className="mt-0.5">
                    {step.status === 'success' ? (
                      <CheckCircle2 className="h-4 w-4 text-[#00c471]" />
                    ) : (
                      <XCircle className="h-4 w-4 text-[#f04452]" />
                    )}
                  </div>
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="text-[13px] font-semibold font-mono">{step.name}</span>
                      <span className={`inline-flex items-center px-1.5 py-0 rounded text-[10px] font-medium ${
                        step.status === 'success'
                          ? 'bg-[#00c471]/10 text-[#00c471]'
                          : 'bg-[#f04452]/10 text-[#f04452]'
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
              {!resizing && resizeSteps.length > 0 && (
                <div className={`flex items-center gap-2 justify-center py-2 text-[13px] font-medium ${
                  resizeSteps.every(s => s.status === 'success') ? 'text-[#00c471]' : 'text-[#f04452]'
                }`}>
                  {resizeSteps.every(s => s.status === 'success') ? (
                    <><CheckCircle2 className="h-4 w-4" />{t('disk.swap.resizeSuccess')}</>
                  ) : (
                    <><XCircle className="h-4 w-4" />{t('disk.swap.resizeFailed')}</>
                  )}
                </div>
              )}
            </div>
          )}

          <DialogFooter>
            {resizePhase === 'config' ? (
              <>
                <Button variant="outline" onClick={closeResizeDialog}>
                  {t('common.cancel')}
                </Button>
                <Button
                  onClick={handleResize}
                  disabled={
                    resizing ||
                    !resizeSizeMB.trim() ||
                    parseInt(resizeSizeMB, 10) <= 0 ||
                    (resizeCheck ? parseInt(resizeSizeMB, 10) > resizeCheck.max_size_mb : false)
                  }
                >
                  {t('disk.swap.resize')}
                </Button>
              </>
            ) : (
              <Button variant="outline" onClick={closeResizeDialog} disabled={resizing}>
                {t('common.close')}
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
