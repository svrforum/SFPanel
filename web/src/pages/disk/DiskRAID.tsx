import { useState, useEffect, useCallback } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { Plus, Trash2, Shield, HardDrive, PlusCircle, MinusCircle, Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
import { useApiAction } from '@/hooks/useApiAction'
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
import { TypeToConfirmDialog } from '@/components/TypeToConfirmDialog'
import { NativeSelect } from './components/NativeSelect'
import { TabLoading, CountPill, RefreshButton } from './components/TabToolbar'

import type { BlockDevice, RAIDArray } from '@/types/api'

const RAID_LEVELS = ['raid0', 'raid1', 'raid5', 'raid6', 'raid10']

function memberStateColor(state: string): string {
  switch (state.toLowerCase()) {
    case 'active':
    case 'in_sync':
      return 'bg-success/10 text-success'
    case 'spare':
    case 'rebuilding':
      return 'bg-warning/10 text-warning'
    case 'faulty':
    case 'removed':
      return 'bg-destructive/10 text-destructive'
    default:
      return 'bg-secondary text-muted-foreground'
  }
}

function arrayStateBadge(state: string) {
  const base = 'inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium'
  switch (state.toLowerCase()) {
    case 'active':
    case 'clean':
      return <span className={`${base} bg-success/10 text-success`}>{state}</span>
    case 'degraded':
    case 'rebuilding':
      return <span className={`${base} bg-warning/10 text-warning`}>{state}</span>
    case 'inactive':
    case 'failed':
      return <span className={`${base} bg-destructive/10 text-destructive`}>{state}</span>
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
  const [selectedDevices, setSelectedDevices] = useState<string[]>([])
  const [candidates, setCandidates] = useState<BlockDevice[]>([])
  const [candidatesLoading, setCandidatesLoading] = useState(false)

  // Delete dialog
  const [deleteTarget, setDeleteTarget] = useState<RAIDArray | null>(null)

  // Add disk dialog
  const [addDiskOpen, setAddDiskOpen] = useState(false)
  const [addDiskArray, setAddDiskArray] = useState<RAIDArray | null>(null)
  const [addDiskDevice, setAddDiskDevice] = useState('')

  // Remove disk dialog
  const [removeDiskOpen, setRemoveDiskOpen] = useState(false)
  const [removeDiskArray, setRemoveDiskArray] = useState<RAIDArray | null>(null)
  const [removeDiskDevice, setRemoveDiskDevice] = useState('')

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

  // Load member candidates when the create dialog opens. Unused = a whole disk
  // with no partitions and no mount point — picking from this list (VG-create
  // pattern) replaces the old free-text device input so a typo can't pull an
  // in-use disk into the array.
  useEffect(() => {
    if (!createOpen) return
    let cancelled = false
    setCandidatesLoading(true)
    api.getDiskOverview()
      .then((data) => {
        if (cancelled) return
        const unused: typeof data = []
        for (const d of data || []) {
          if ((d.type === 'disk' || !d.type) && !(d.children && d.children.length > 0) && !d.mountpoint) {
            unused.push(d)
          }
          // Unmounted partitions stay selectable too — partition RAID members
          // (e.g. sdb1) are a legitimate layout the old free-text input allowed.
          for (const c of d.children ?? []) {
            if (c.type === 'part' && !c.mountpoint && !(c.children && c.children.length > 0)) {
              unused.push(c)
            }
          }
        }
        setCandidates(unused)
      })
      .catch(() => {
        if (!cancelled) setCandidates([])
      })
      .finally(() => {
        if (!cancelled) setCandidatesLoading(false)
      })
    return () => { cancelled = true }
  }, [createOpen])

  const toggleDeviceSelection = (name: string) => {
    setSelectedDevices((prev) =>
      prev.includes(name) ? prev.filter((d) => d !== name) : [...prev, name]
    )
  }

  const resetCreateForm = () => {
    setNewName('')
    setNewLevel('raid1')
    setSelectedDevices([])
  }

  const { run: runCreate, loading: creating } = useApiAction(
    api.createRAID.bind(api),
    {
      successMsg: t('disk.raid.createSuccess'),
      errorMsg: t('disk.raid.createFailed'),
      onSuccess: () => {
        setCreateOpen(false)
        resetCreateForm()
        void fetchArrays()
      },
    },
  )

  const handleCreate = () => {
    if (!newName.trim() || selectedDevices.length === 0) return
    void runCreate({
      name: newName.trim(),
      level: newLevel,
      devices: selectedDevices,
    })
  }

  const { run: runDelete, loading: deleting } = useApiAction(
    api.deleteRAID.bind(api),
    {
      successMsg: t('disk.raid.deleted'),
      errorMsg: t('disk.raid.deleteFailed'),
      onSuccess: () => {
        setDeleteTarget(null)
        void fetchArrays()
      },
    },
  )

  const handleDelete = () => {
    if (deleteTarget) void runDelete(deleteTarget.name)
  }

  const { run: runAddDisk, loading: addingDisk } = useApiAction(
    api.addRAIDDisk.bind(api),
    {
      successMsg: t('disk.raid.addDiskSuccess'),
      errorMsg: t('disk.raid.addDiskFailed'),
      onSuccess: () => {
        setAddDiskOpen(false)
        setAddDiskArray(null)
        setAddDiskDevice('')
        void fetchArrays()
      },
    },
  )

  const handleAddDisk = () => {
    if (!addDiskArray || !addDiskDevice.trim()) return
    void runAddDisk(addDiskArray.name, addDiskDevice.trim())
  }

  const { run: runRemoveDisk, loading: removingDisk } = useApiAction(
    api.removeRAIDDisk.bind(api),
    {
      successMsg: t('disk.raid.removeDiskSuccess'),
      errorMsg: t('disk.raid.removeDiskFailed'),
      onSuccess: () => {
        setRemoveDiskOpen(false)
        setRemoveDiskArray(null)
        setRemoveDiskDevice('')
        void fetchArrays()
      },
    },
  )

  const handleRemoveDisk = () => {
    if (!removeDiskArray || !removeDiskDevice.trim()) return
    void runRemoveDisk(removeDiskArray.name, removeDiskDevice.trim())
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

  if (loading) {
    return <TabLoading />
  }

  return (
    <div className="space-y-4 mt-4">
      {/* Toolbar */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <CountPill>{t('disk.raid.arrayCount', { count: arrays.length })}</CountPill>
        <div className="flex flex-wrap items-center gap-2">
          <RefreshButton onClick={fetchArrays} loading={loading} />
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
                <div className="mt-3 grid grid-cols-2 md:grid-cols-4 gap-3">
                  <div className="bg-secondary/30 rounded-lg px-3 py-2 text-center">
                    <div className="text-[11px] text-muted-foreground">{t('disk.raid.totalDevices')}</div>
                    <div className="text-lg font-bold">{arr.total}</div>
                  </div>
                  <div className="bg-success/5 rounded-lg px-3 py-2 text-center">
                    <div className="text-[11px] text-success">{t('disk.raid.activeDevices')}</div>
                    <div className="text-lg font-bold text-success">{arr.active}</div>
                  </div>
                  <div className="bg-destructive/5 rounded-lg px-3 py-2 text-center">
                    <div className="text-[11px] text-destructive">{t('disk.raid.failedDevices')}</div>
                    <div className="text-lg font-bold text-destructive">{arr.failed}</div>
                  </div>
                  <div className="bg-warning/5 rounded-lg px-3 py-2 text-center">
                    <div className="text-[11px] text-warning">{t('disk.raid.spareDevices')}</div>
                    <div className="text-lg font-bold text-warning">{arr.spare}</div>
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
                        className="flex items-center gap-3 bg-muted/30 rounded-lg px-3 py-2 text-[13px] min-w-0"
                      >
                        <HardDrive className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
                        <span className="font-mono font-medium w-28 shrink-0 truncate" title={member.device}>{member.device}</span>
                        <span className={`inline-flex items-center px-1.5 py-0 rounded text-[10px] font-medium shrink-0 ${memberStateColor(member.state)}`}>
                          {member.state}
                        </span>
                        {member.role && (
                          <span className="text-xs text-muted-foreground truncate min-w-0">{member.role}</span>
                        )}
                        <div className="ml-auto">
                          <Button
                            variant="ghost"
                            size="icon-xs"
                            title={t('disk.raid.removeDisk')}
                            aria-label={t('disk.raid.removeDisk')}
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
              <NativeSelect
                id="raid-level"
                value={newLevel}
                onChange={(e) => setNewLevel(e.target.value)}
                className="w-full"
              >
                {RAID_LEVELS.map((level) => (
                  <option key={level} value={level}>{level.toUpperCase()}</option>
                ))}
              </NativeSelect>
            </div>
            <div className="space-y-2">
              <Label>{t('disk.raid.devices')}</Label>
              {candidatesLoading ? (
                <div className="flex items-center gap-2 text-sm text-muted-foreground py-2">
                  <Loader2 className="h-3.5 w-3.5 animate-spin" />
                  {t('common.loading')}
                </div>
              ) : candidates.length === 0 ? (
                <p className="text-sm text-muted-foreground">{t('disk.raid.noUnusedDisks')}</p>
              ) : (
                <div className="space-y-1.5">
                  {candidates.map((d) => (
                    <label
                      key={d.name}
                      className={`flex items-center gap-3 rounded-lg px-3 py-2 cursor-pointer transition-colors ${
                        selectedDevices.includes(d.name)
                          ? 'bg-primary/10 ring-1 ring-primary/30'
                          : 'bg-muted/30 hover:bg-muted/50'
                      }`}
                    >
                      <input
                        type="checkbox"
                        checked={selectedDevices.includes(d.name)}
                        onChange={() => toggleDeviceSelection(d.name)}
                        className="rounded"
                      />
                      <span className="font-mono text-sm">{d.name}</span>
                      <span className="text-xs text-muted-foreground ml-auto">
                        {d.model ? `${d.model} · ` : ''}{formatBytes(d.size)}
                      </span>
                    </label>
                  ))}
                </div>
              )}
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => { setCreateOpen(false); resetCreateForm() }}>
              {t('common.cancel')}
            </Button>
            <Button onClick={handleCreate} disabled={creating || !newName.trim() || selectedDevices.length === 0}>
              {creating ? t('common.creating') : t('common.create')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete Array Dialog */}
      <TypeToConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title={t('disk.raid.deleteTitle')}
        description={t('disk.raid.deleteConfirmDesc', { name: deleteTarget?.name ?? '' })}
        confirmPhrase={deleteTarget?.name ?? ''}
        confirmLabel={t('common.delete')}
        loading={deleting}
        onConfirm={handleDelete}
      />

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
