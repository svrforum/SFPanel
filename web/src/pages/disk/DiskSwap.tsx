import { useState, useEffect, useCallback } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { Plus, Trash2, MemoryStick, Save, Maximize2 } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
import { useApiAction } from '@/hooks/useApiAction'
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
import { SwapResizeDialog } from './components/SwapResizeDialog'
import { TabLoading, RefreshButton } from './components/TabToolbar'

import type { SwapEntry, SwapInfo } from '@/types/api'

export default function DiskSwap() {
  const { t } = useTranslation()
  const [summary, setSummary] = useState<Omit<SwapInfo, 'entries'>>({ total: 0, used: 0, free: 0, swappiness: 60 })
  const [entries, setEntries] = useState<SwapEntry[]>([])
  const [loading, setLoading] = useState(true)

  // Swappiness
  const [swappiness, setSwappiness] = useState(60)

  // Create dialog
  const [createOpen, setCreateOpen] = useState(false)
  const [createMode, setCreateMode] = useState<'file' | 'partition'>('file')
  const [createPath, setCreatePath] = useState('')
  const [createSizeMB, setCreateSizeMB] = useState('')
  const [createDevice, setCreateDevice] = useState('')

  // Remove
  const [removeTarget, setRemoveTarget] = useState<SwapEntry | null>(null)

  // Resize
  const [resizeTarget, setResizeTarget] = useState<SwapEntry | null>(null)

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

  const resetCreateForm = () => {
    setCreateMode('file')
    setCreatePath('')
    setCreateSizeMB('')
    setCreateDevice('')
  }

  const { run: runSaveSwappiness, loading: savingSwappiness } = useApiAction(
    api.setSwappiness.bind(api),
    {
      successMsg: t('disk.swap.swappinessSaved'),
      errorMsg: t('disk.swap.swappinessFailed'),
      onSuccess: () => {
        void fetchSwap()
      },
    },
  )

  const { run: runCreate, loading: creating } = useApiAction(
    api.createSwap.bind(api),
    {
      successMsg: t('disk.swap.createSuccess'),
      errorMsg: t('disk.swap.createFailed'),
      onSuccess: () => {
        setCreateOpen(false)
        resetCreateForm()
        void fetchSwap()
      },
    },
  )

  const handleCreate = () => {
    if (createMode === 'file') {
      if (!createPath.trim() || !createSizeMB.trim()) return
      void runCreate({
        type: 'file',
        path: createPath.trim(),
        size_mb: parseInt(createSizeMB, 10),
      })
    } else {
      if (!createDevice.trim()) return
      void runCreate({
        type: 'partition',
        device: createDevice.trim(),
      })
    }
  }

  const { run: runRemove, loading: removing } = useApiAction(
    api.removeSwap.bind(api),
    {
      successMsg: t('disk.swap.removed'),
      errorMsg: t('disk.swap.removeFailed'),
      onSuccess: () => {
        setRemoveTarget(null)
        void fetchSwap()
      },
    },
  )

  const handleRemove = () => {
    if (removeTarget) void runRemove(removeTarget.name)
  }

  const usedPercent = summary.total > 0 ? (summary.used / summary.total) * 100 : 0

  if (loading) {
    return <TabLoading />
  }

  return (
    <div className="space-y-4 mt-4">
      {/* Toolbar */}
      <div className="flex items-center justify-end gap-2">
        <RefreshButton onClick={fetchSwap} loading={loading} />
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
            <MemoryStick className="h-4 w-4 text-warning" />
            <span className="text-[13px] text-muted-foreground">{t('disk.swap.used')}</span>
          </div>
          <div className="text-2xl font-bold">{formatBytes(summary.used)}</div>
          {summary.total > 0 && (
            <div className="mt-2 h-1.5 bg-secondary rounded-full overflow-hidden">
              <div
                className="h-full rounded-full transition-all duration-500"
                style={{
                  width: `${Math.min(usedPercent, 100)}%`,
                  backgroundColor: usedPercent > 80 ? 'var(--destructive)' : usedPercent > 50 ? 'var(--warning)' : 'var(--primary)',
                }}
              />
            </div>
          )}
        </div>
        <div className="bg-card rounded-2xl card-shadow p-4">
          <div className="flex items-center gap-2 mb-1">
            <MemoryStick className="h-4 w-4 text-success" />
            <span className="text-[13px] text-muted-foreground">{t('disk.swap.free')}</span>
          </div>
          <div className="text-2xl font-bold text-success">{formatBytes(summary.free)}</div>
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
              onClick={() => void runSaveSwappiness(swappiness)}
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
            className="flex-1 h-2 bg-secondary rounded-full appearance-none cursor-pointer accent-primary outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
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
                        aria-label={t('disk.swap.resize')}
                        onClick={() => setResizeTarget(entry)}
                      >
                        <Maximize2 />
                      </Button>
                    )}
                    <Button
                      variant="ghost"
                      size="icon-xs"
                      title={t('disk.swap.remove')}
                      aria-label={t('disk.swap.remove')}
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
                  className={`flex-1 rounded-xl px-4 py-2.5 text-[13px] font-medium transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0 ${
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
                  className={`flex-1 rounded-xl px-4 py-2.5 text-[13px] font-medium transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0 ${
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
      <SwapResizeDialog
        target={resizeTarget}
        onOpenChange={(open) => { if (!open) setResizeTarget(null) }}
        onResized={fetchSwap}
      />
    </div>
  )
}
