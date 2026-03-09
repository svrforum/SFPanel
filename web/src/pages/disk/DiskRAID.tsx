import { useState, useEffect, useCallback } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { Plus, Trash2, RefreshCw, Shield, HardDrive, PlusCircle, MinusCircle } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'

import type { RAIDArray } from '@/types/api'

const RAID_LEVELS = ['raid0', 'raid1', 'raid5', 'raid6', 'raid10']

function memberStateColor(state: string): string {
  switch (state.toLowerCase()) {
    case 'active':
    case 'in_sync':
      return 'bg-[#00c471]/10 text-[#00c471]'
    case 'spare':
    case 'rebuilding':
      return 'bg-[#f59e0b]/10 text-[#f59e0b]'
    case 'faulty':
    case 'removed':
      return 'bg-[#f04452]/10 text-[#f04452]'
    default:
      return 'bg-secondary text-muted-foreground'
  }
}

function arrayStateBadge(state: string) {
  const base = 'inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium'
  switch (state.toLowerCase()) {
    case 'active':
    case 'clean':
      return <span className={`${base} bg-[#00c471]/10 text-[#00c471]`}>{state}</span>
    case 'degraded':
    case 'rebuilding':
      return <span className={`${base} bg-[#f59e0b]/10 text-[#f59e0b]`}>{state}</span>
    case 'inactive':
    case 'failed':
      return <span className={`${base} bg-[#f04452]/10 text-[#f04452]`}>{state}</span>
    default:
      return <span className={`${base} bg-secondary text-muted-foreground`}>{state}</span>
  }
}

export default function DiskRAID() {
  const { t } = useTranslation()
  const [arrays, setArrays] = useState<RAIDArray[]>([])
  const [loading, setLoading] = useState(true)

  // Create dialog
  const [createOpen, setCreateOpen] = useState(false)
  const [newName, setNewName] = useState('')
  const [newLevel, setNewLevel] = useState('raid1')
  const [newDevices, setNewDevices] = useState('')
  const [creating, setCreating] = useState(false)

  // Delete dialog
  const [deleteTarget, setDeleteTarget] = useState<RAIDArray | null>(null)
  const [deleting, setDeleting] = useState(false)

  // Add disk dialog
  const [addDiskOpen, setAddDiskOpen] = useState(false)
  const [addDiskArray, setAddDiskArray] = useState<RAIDArray | null>(null)
  const [addDiskDevice, setAddDiskDevice] = useState('')
  const [addingDisk, setAddingDisk] = useState(false)

  // Remove disk dialog
  const [removeDiskOpen, setRemoveDiskOpen] = useState(false)
  const [removeDiskArray, setRemoveDiskArray] = useState<RAIDArray | null>(null)
  const [removeDiskDevice, setRemoveDiskDevice] = useState('')
  const [removingDisk, setRemovingDisk] = useState(false)

  const fetchArrays = useCallback(async () => {
    try {
      setLoading(true)
      const data = await api.getRAIDArrays()
      setArrays(data || [])
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.raid.fetchFailed')
      toast.error(message)
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchArrays()
  }, [fetchArrays])

  const handleCreate = async () => {
    if (!newName.trim() || !newDevices.trim()) return
    setCreating(true)
    try {
      const devices = newDevices.split(',').map((d) => d.trim()).filter(Boolean)
      await api.createRAID({
        name: newName.trim(),
        level: newLevel,
        devices,
      })
      toast.success(t('disk.raid.createSuccess'))
      setCreateOpen(false)
      resetCreateForm()
      await fetchArrays()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.raid.createFailed')
      toast.error(message)
    } finally {
      setCreating(false)
    }
  }

  const handleDelete = async () => {
    if (!deleteTarget) return
    setDeleting(true)
    try {
      await api.deleteRAID(deleteTarget.name)
      toast.success(t('disk.raid.deleted'))
      setDeleteTarget(null)
      await fetchArrays()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.raid.deleteFailed')
      toast.error(message)
    } finally {
      setDeleting(false)
    }
  }

  const handleAddDisk = async () => {
    if (!addDiskArray || !addDiskDevice.trim()) return
    setAddingDisk(true)
    try {
      await api.addRAIDDisk(addDiskArray.name, addDiskDevice.trim())
      toast.success(t('disk.raid.addDiskSuccess'))
      setAddDiskOpen(false)
      setAddDiskArray(null)
      setAddDiskDevice('')
      await fetchArrays()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.raid.addDiskFailed')
      toast.error(message)
    } finally {
      setAddingDisk(false)
    }
  }

  const handleRemoveDisk = async () => {
    if (!removeDiskArray || !removeDiskDevice.trim()) return
    setRemovingDisk(true)
    try {
      await api.removeRAIDDisk(removeDiskArray.name, removeDiskDevice.trim())
      toast.success(t('disk.raid.removeDiskSuccess'))
      setRemoveDiskOpen(false)
      setRemoveDiskArray(null)
      setRemoveDiskDevice('')
      await fetchArrays()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.raid.removeDiskFailed')
      toast.error(message)
    } finally {
      setRemovingDisk(false)
    }
  }

  const openAddDisk = (arr: RAIDArray) => {
    setAddDiskArray(arr)
    setAddDiskDevice('')
    setAddDiskOpen(true)
  }

  const openRemoveDisk = (arr: RAIDArray, device: string) => {
    setRemoveDiskArray(arr)
    setRemoveDiskDevice(device)
    setRemoveDiskOpen(true)
  }

  const resetCreateForm = () => {
    setNewName('')
    setNewLevel('raid1')
    setNewDevices('')
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
        <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
          {t('disk.raid.arrayCount', { count: arrays.length })}
        </span>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={fetchArrays} disabled={loading} className="rounded-xl">
            <RefreshCw className={loading ? 'animate-spin' : ''} />
            {t('common.refresh')}
          </Button>
          <Button size="sm" onClick={() => setCreateOpen(true)} className="rounded-xl">
            <Plus />
            {t('disk.raid.createArray')}
          </Button>
        </div>
      </div>

      {/* RAID Array Cards */}
      {arrays.length === 0 ? (
        <div className="bg-card rounded-2xl card-shadow p-8 text-center text-muted-foreground">
          {t('disk.raid.empty')}
        </div>
      ) : (
        <div className="space-y-4">
          {arrays.map((arr) => (
            <div key={arr.name} className="bg-card rounded-2xl card-shadow overflow-hidden">
              {/* Array Header */}
              <div className="p-5 border-b border-border/50">
                <div className="flex items-center justify-between">
                  <div className="flex items-center gap-3">
                    <div className="p-2 rounded-xl bg-primary/10">
                      <Shield className="h-5 w-5 text-primary" />
                    </div>
                    <div>
                      <div className="flex items-center gap-2">
                        <span className="font-semibold text-[15px]">{arr.name}</span>
                        {arrayStateBadge(arr.state)}
                      </div>
                      <div className="text-[13px] text-muted-foreground mt-0.5">
                        {arr.level.toUpperCase()} &middot; {formatBytes(arr.size)}
                      </div>
                    </div>
                  </div>
                  <div className="flex items-center gap-2">
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => openAddDisk(arr)}
                      className="rounded-xl"
                    >
                      <PlusCircle className="h-3.5 w-3.5" />
                      {t('disk.raid.addDisk')}
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => setDeleteTarget(arr)}
                      className="rounded-xl text-destructive hover:text-destructive"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                      {t('common.delete')}
                    </Button>
                  </div>
                </div>

                {/* Device Summary */}
                <div className="mt-3 grid grid-cols-4 gap-3">
                  <div className="bg-secondary/30 rounded-lg px-3 py-2 text-center">
                    <div className="text-[11px] text-muted-foreground">{t('disk.raid.totalDevices')}</div>
                    <div className="text-lg font-bold">{arr.total}</div>
                  </div>
                  <div className="bg-[#00c471]/5 rounded-lg px-3 py-2 text-center">
                    <div className="text-[11px] text-[#00c471]">{t('disk.raid.activeDevices')}</div>
                    <div className="text-lg font-bold text-[#00c471]">{arr.active}</div>
                  </div>
                  <div className="bg-[#f04452]/5 rounded-lg px-3 py-2 text-center">
                    <div className="text-[11px] text-[#f04452]">{t('disk.raid.failedDevices')}</div>
                    <div className="text-lg font-bold text-[#f04452]">{arr.failed}</div>
                  </div>
                  <div className="bg-[#f59e0b]/5 rounded-lg px-3 py-2 text-center">
                    <div className="text-[11px] text-[#f59e0b]">{t('disk.raid.spareDevices')}</div>
                    <div className="text-lg font-bold text-[#f59e0b]">{arr.spare}</div>
                  </div>
                </div>
              </div>

              {/* Member Disks */}
              {arr.devices && arr.devices.length > 0 && (
                <div className="px-5 py-3">
                  <div className="text-[12px] font-medium text-muted-foreground mb-2 flex items-center gap-1.5">
                    <HardDrive className="h-3 w-3" />
                    {t('disk.raid.memberDisks')} ({arr.devices.length})
                  </div>
                  <div className="space-y-1">
                    {arr.devices.map((member) => (
                      <div
                        key={member.device}
                        className="flex items-center gap-3 bg-muted/30 rounded-lg px-3 py-2 text-[13px]"
                      >
                        <HardDrive className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                        <span className="font-mono font-medium w-28 shrink-0">{member.device}</span>
                        <span className={`inline-flex items-center px-1.5 py-0 rounded text-[10px] font-medium ${memberStateColor(member.state)}`}>
                          {member.state}
                        </span>
                        {member.role && (
                          <span className="text-xs text-muted-foreground">{member.role}</span>
                        )}
                        <div className="ml-auto">
                          <Button
                            variant="ghost"
                            size="icon-xs"
                            title={t('disk.raid.removeDisk')}
                            onClick={() => openRemoveDisk(arr, member.device)}
                          >
                            <MinusCircle className="h-3.5 w-3.5" />
                          </Button>
                        </div>
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      {/* Create Array Dialog */}
      <Dialog open={createOpen} onOpenChange={(open) => { setCreateOpen(open); if (!open) resetCreateForm() }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('disk.raid.createArray')}</DialogTitle>
            <DialogDescription>{t('disk.raid.createDescription')}</DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="raid-name">{t('disk.raid.arrayName')}</Label>
              <Input
                id="raid-name"
                placeholder="e.g., md0"
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="raid-level">{t('disk.raid.level')}</Label>
              <select
                id="raid-level"
                value={newLevel}
                onChange={(e) => setNewLevel(e.target.value)}
                className="flex h-9 w-full rounded-xl border-0 bg-secondary/50 px-3 py-1 text-[13px] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/20"
              >
                {RAID_LEVELS.map((level) => (
                  <option key={level} value={level}>{level.toUpperCase()}</option>
                ))}
              </select>
            </div>
            <div className="space-y-2">
              <Label htmlFor="raid-devices">{t('disk.raid.devices')}</Label>
              <Input
                id="raid-devices"
                placeholder="e.g., sdb,sdc,sdd"
                value={newDevices}
                onChange={(e) => setNewDevices(e.target.value)}
              />
              <p className="text-[11px] text-muted-foreground">{t('disk.raid.devicesHint')}</p>
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => { setCreateOpen(false); resetCreateForm() }}>
              {t('common.cancel')}
            </Button>
            <Button onClick={handleCreate} disabled={creating || !newName.trim() || !newDevices.trim()}>
              {creating ? t('common.creating') : t('common.create')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete Array Dialog */}
      <Dialog open={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('disk.raid.deleteTitle')}</DialogTitle>
            <DialogDescription>
              <Trans
                i18nKey="disk.raid.deleteConfirm"
                values={{ name: deleteTarget?.name ?? '' }}
                components={{ strong: <span className="font-semibold font-mono" /> }}
              />
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button variant="destructive" onClick={handleDelete} disabled={deleting}>
              {t('common.delete')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Add Disk Dialog */}
      <Dialog open={addDiskOpen} onOpenChange={(open) => { setAddDiskOpen(open); if (!open) { setAddDiskArray(null); setAddDiskDevice('') } }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('disk.raid.addDiskTitle')}</DialogTitle>
            <DialogDescription>
              {t('disk.raid.addDiskDescription', { array: addDiskArray?.name ?? '' })}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="add-disk-device">{t('disk.raid.devicePath')}</Label>
            <Input
              id="add-disk-device"
              placeholder="e.g., sdd"
              value={addDiskDevice}
              onChange={(e) => setAddDiskDevice(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && handleAddDisk()}
            />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => { setAddDiskOpen(false); setAddDiskArray(null); setAddDiskDevice('') }}>
              {t('common.cancel')}
            </Button>
            <Button onClick={handleAddDisk} disabled={addingDisk || !addDiskDevice.trim()}>
              {addingDisk ? t('disk.raid.adding') : t('disk.raid.addDisk')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Remove Disk Dialog */}
      <Dialog open={removeDiskOpen} onOpenChange={(open) => { setRemoveDiskOpen(open); if (!open) { setRemoveDiskArray(null); setRemoveDiskDevice('') } }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('disk.raid.removeDiskTitle')}</DialogTitle>
            <DialogDescription>
              <Trans
                i18nKey="disk.raid.removeDiskConfirm"
                values={{ device: removeDiskDevice, array: removeDiskArray?.name ?? '' }}
                components={{ strong: <span className="font-semibold font-mono" /> }}
              />
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => { setRemoveDiskOpen(false); setRemoveDiskArray(null); setRemoveDiskDevice('') }}>
              {t('common.cancel')}
            </Button>
            <Button variant="destructive" onClick={handleRemoveDisk} disabled={removingDisk}>
              {removingDisk ? t('disk.raid.removing') : t('disk.raid.removeDisk')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
