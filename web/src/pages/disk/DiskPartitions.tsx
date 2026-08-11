import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { Plus, Trash2, HardDrive } from 'lucide-react'
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
import { TypeToConfirmDialog } from '@/components/TypeToConfirmDialog'
import { NativeSelect } from './components/NativeSelect'
import { TabLoading, RefreshButton } from './components/TabToolbar'

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
  const [deleteTarget, setDeleteTarget] = useState<DiskPartitionChild | null>(null)

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
      if (diskDevices.length > 0) {
        // Functional update keeps selectedDisk out of the deps, so changing the
        // dropdown selection stays a pure local operation (no lsblk refetch).
        setSelectedDisk((cur) => cur || diskDevices[0].name)
      }
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.partitions.fetchFailed')
      toast.error(message)
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchDisks()
  }, [fetchDisks])

  const currentDisk = disks.find((d) => d.name === selectedDisk)
  const partitions = currentDisk?.children || []

  const resetCreateForm = () => {
    setNewStart('')
    setNewEnd('')
    setNewFsType('ext4')
  }

  const { run: runCreate, loading: creating } = useApiAction(
    api.createPartition.bind(api),
    {
      successMsg: t('disk.partitions.createSuccess'),
      errorMsg: t('disk.partitions.createFailed'),
      onSuccess: () => {
        setCreateOpen(false)
        resetCreateForm()
        void fetchDisks()
      },
    },
  )

  const handleCreate = () => {
    if (!selectedDisk || !newStart.trim() || !newEnd.trim()) return
    void runCreate(selectedDisk, {
      start: newStart.trim(),
      end: newEnd.trim(),
      fs_type: newFsType,
    })
  }

  const { run: runDelete, loading: deleting } = useApiAction(
    api.deletePartition.bind(api),
    {
      successMsg: t('disk.partitions.deleted'),
      errorMsg: t('disk.partitions.deleteFailed'),
      onSuccess: () => {
        setDeleteTarget(null)
        void fetchDisks()
      },
    },
  )

  const handleDelete = () => {
    if (deleteTarget) void runDelete(selectedDisk, deleteTarget.name)
  }

  if (loading) {
    return <TabLoading />
  }

  return (
    <div className="space-y-4 mt-4">
      {/* Toolbar */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-2">
            <HardDrive className="h-4 w-4 text-muted-foreground" />
            <Label className="text-[13px]">{t('disk.partitions.selectDisk')}</Label>
          </div>
          <NativeSelect
            value={selectedDisk}
            onChange={(e) => setSelectedDisk(e.target.value)}
          >
            {disks.map((d) => (
              <option key={d.name} value={d.name}>
                {d.name} — {d.model || t('disk.overview.unknownModel')} ({formatBytes(d.size)})
              </option>
            ))}
          </NativeSelect>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <RefreshButton onClick={fetchDisks} loading={loading} />
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
                    aria-label={t('common.delete')}
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
              <NativeSelect
                id="part-fs"
                value={newFsType}
                onChange={(e) => setNewFsType(e.target.value)}
                className="w-full"
              >
                {FS_TYPES.map((fs) => (
                  <option key={fs} value={fs}>{fs}</option>
                ))}
              </NativeSelect>
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
      <TypeToConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title={t('disk.partitions.deleteTitle')}
        description={t('disk.partitions.deleteConfirmDesc', { name: deleteTarget?.name ?? '' })}
        confirmPhrase={deleteTarget?.name ?? ''}
        confirmLabel={t('common.delete')}
        loading={deleting}
        onConfirm={handleDelete}
      />
    </div>
  )
}
