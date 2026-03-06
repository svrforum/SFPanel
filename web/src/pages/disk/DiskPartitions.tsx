import { useState, useEffect, useCallback } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { Plus, Trash2, RefreshCw, HardDrive } from 'lucide-react'
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

import type { BlockDevice } from '@/types/api'

type PhysicalDisk = BlockDevice
type DiskPartitionChild = NonNullable<BlockDevice['children']>[number]

const FS_TYPES = ['ext4', 'xfs', 'btrfs', 'swap']

export default function DiskPartitions() {
  const { t } = useTranslation()
  const [disks, setDisks] = useState<PhysicalDisk[]>([])
  const [loading, setLoading] = useState(true)
  const [selectedDisk, setSelectedDisk] = useState<string>('')
  const [createOpen, setCreateOpen] = useState(false)
  const [creating, setCreating] = useState(false)
  const [deleteTarget, setDeleteTarget] = useState<DiskPartitionChild | null>(null)
  const [actionLoading, setActionLoading] = useState(false)

  // Create form state
  const [newStart, setNewStart] = useState('')
  const [newEnd, setNewEnd] = useState('')
  const [newFsType, setNewFsType] = useState('ext4')

  const fetchDisks = useCallback(async () => {
    try {
      setLoading(true)
      const data = await api.getDiskOverview()
      const diskDevices = (data || []).filter((d: PhysicalDisk) => d.type === 'disk' || !d.type)
      setDisks(diskDevices)
      if (diskDevices.length > 0 && !selectedDisk) {
        setSelectedDisk(diskDevices[0].name)
      }
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.partitions.fetchFailed')
      toast.error(message)
    } finally {
      setLoading(false)
    }
  }, [t, selectedDisk])

  useEffect(() => {
    fetchDisks()
  }, [fetchDisks])

  const currentDisk = disks.find((d) => d.name === selectedDisk)
  const partitions = currentDisk?.children || []

  const handleCreate = async () => {
    if (!selectedDisk || !newStart.trim() || !newEnd.trim()) return
    setCreating(true)
    try {
      await api.createPartition(selectedDisk, {
        start: newStart.trim(),
        end: newEnd.trim(),
        fs_type: newFsType,
      })
      toast.success(t('disk.partitions.createSuccess'))
      setCreateOpen(false)
      resetCreateForm()
      await fetchDisks()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.partitions.createFailed')
      toast.error(message)
    } finally {
      setCreating(false)
    }
  }

  const handleDelete = async () => {
    if (!deleteTarget) return
    setActionLoading(true)
    try {
      await api.deletePartition(selectedDisk, deleteTarget.name)
      toast.success(t('disk.partitions.deleted'))
      setDeleteTarget(null)
      await fetchDisks()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.partitions.deleteFailed')
      toast.error(message)
    } finally {
      setActionLoading(false)
    }
  }

  const resetCreateForm = () => {
    setNewStart('')
    setNewEnd('')
    setNewFsType('ext4')
  }

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
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-2">
            <HardDrive className="h-4 w-4 text-muted-foreground" />
            <Label className="text-[13px]">{t('disk.partitions.selectDisk')}</Label>
          </div>
          <select
            value={selectedDisk}
            onChange={(e) => setSelectedDisk(e.target.value)}
            className="flex h-9 rounded-xl border-0 bg-secondary/50 px-3 py-1 text-[13px] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/20"
          >
            {disks.map((d) => (
              <option key={d.name} value={d.name}>
                {d.name} — {d.model || t('disk.overview.unknownModel')} ({formatBytes(d.size)})
              </option>
            ))}
          </select>
        </div>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={fetchDisks} disabled={loading} className="rounded-xl">
            <RefreshCw className={loading ? 'animate-spin' : ''} />
            {t('common.refresh')}
          </Button>
          <Button size="sm" onClick={() => setCreateOpen(true)} disabled={!selectedDisk} className="rounded-xl">
            <Plus />
            {t('disk.partitions.createPartition')}
          </Button>
        </div>
      </div>

      {/* Partitions Table */}
      <div className="bg-card rounded-2xl card-shadow overflow-hidden">
        <Table>
          <TableHeader>
            <TableRow className="border-border/50">
              <TableHead className="text-[11px]">{t('common.name')}</TableHead>
              <TableHead className="text-[11px]">{t('disk.partitions.size')}</TableHead>
              <TableHead className="text-[11px]">{t('disk.partitions.type')}</TableHead>
              <TableHead className="text-[11px]">{t('disk.partitions.fsType')}</TableHead>
              <TableHead className="text-[11px]">{t('disk.partitions.mountPoint')}</TableHead>
              <TableHead className="text-right text-[11px]">{t('common.actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {partitions.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="text-center text-muted-foreground py-8">
                  {t('disk.partitions.empty')}
                </TableCell>
              </TableRow>
            )}
            {partitions.map((p) => (
              <TableRow key={p.name}>
                <TableCell className="font-medium font-mono text-sm">{p.name}</TableCell>
                <TableCell className="text-muted-foreground">{formatBytes(p.size)}</TableCell>
                <TableCell className="text-muted-foreground">{p.type || '-'}</TableCell>
                <TableCell>
                  {p.fstype ? (
                    <span className="inline-flex items-center px-1.5 py-0 rounded text-[10px] font-medium border border-border">
                      {p.fstype}
                    </span>
                  ) : (
                    <span className="text-muted-foreground">-</span>
                  )}
                </TableCell>
                <TableCell className="text-muted-foreground font-mono text-xs">
                  {p.mountpoint || '-'}
                </TableCell>
                <TableCell className="text-right">
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    title={t('common.delete')}
                    onClick={() => setDeleteTarget(p)}
                    disabled={!!p.mountpoint}
                  >
                    <Trash2 />
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      {/* Create Partition Dialog */}
      <Dialog open={createOpen} onOpenChange={setCreateOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('disk.partitions.createPartition')}</DialogTitle>
            <DialogDescription>
              {t('disk.partitions.createDescription', { disk: selectedDisk })}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="part-start">{t('disk.partitions.start')}</Label>
              <Input
                id="part-start"
                placeholder="e.g., 0%"
                value={newStart}
                onChange={(e) => setNewStart(e.target.value)}
              />
              <p className="text-[11px] text-muted-foreground">{t('disk.partitions.startHint')}</p>
            </div>
            <div className="space-y-2">
              <Label htmlFor="part-end">{t('disk.partitions.end')}</Label>
              <Input
                id="part-end"
                placeholder="e.g., 100%"
                value={newEnd}
                onChange={(e) => setNewEnd(e.target.value)}
              />
              <p className="text-[11px] text-muted-foreground">{t('disk.partitions.endHint')}</p>
            </div>
            <div className="space-y-2">
              <Label htmlFor="part-fs">{t('disk.partitions.fsType')}</Label>
              <select
                id="part-fs"
                value={newFsType}
                onChange={(e) => setNewFsType(e.target.value)}
                className="flex h-9 w-full rounded-xl border-0 bg-secondary/50 px-3 py-1 text-[13px] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/20"
              >
                {FS_TYPES.map((fs) => (
                  <option key={fs} value={fs}>{fs}</option>
                ))}
              </select>
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => { setCreateOpen(false); resetCreateForm() }}>
              {t('common.cancel')}
            </Button>
            <Button onClick={handleCreate} disabled={creating || !newStart.trim() || !newEnd.trim()}>
              {creating ? t('common.creating') : t('common.create')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete Confirmation Dialog */}
      <Dialog open={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('disk.partitions.deleteTitle')}</DialogTitle>
            <DialogDescription>
              <Trans
                i18nKey="disk.partitions.deleteConfirm"
                values={{ name: deleteTarget?.name ?? '' }}
                components={{ strong: <span className="font-semibold font-mono" /> }}
              />
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button variant="destructive" onClick={handleDelete} disabled={actionLoading}>
              {t('common.delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
