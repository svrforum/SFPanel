import { useState, useEffect, useCallback } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { Plus, Trash2, Shield, HardDrive, PlusCircle, MinusCircle, Loader2 } from 'lucide-react'
import { api, type ApiError } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
import { raidTone, type RaidTone } from '@/lib/raid'
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
import { FreeDeviceSelect } from './components/FreeDeviceSelect'
import { FieldNote } from './components/FieldNote'
import { focusFirst } from './focusFirst'
import { TabLoading, CountPill, RefreshButton } from './components/TabToolbar'
import { LoadFailure } from './components/LoadFailure'
import { useFreeDevices } from './useFreeDevices'

import type { RAIDArray } from '@/types/api'

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

const ARRAY_TONE: Record<RaidTone, string> = {
  ok: 'bg-success/10 text-success',
  warn: 'bg-warning/10 text-warning',
  bad: 'bg-destructive/10 text-destructive',
  other: 'bg-secondary text-muted-foreground',
}

function arrayStateBadge(state: string) {
  const base = 'inline-flex items-center px-2 py-0.5 rounded-full text-[12px] font-medium'
  return <span className={`${base} ${ARRAY_TONE[raidTone(state)]}`}>{state}</span>
}

export default function DiskRAID() {
  const { t } = useTranslation()
  const [arrays, setArrays] = useState<RAIDArray[]>([])
  const [loading, setLoading] = useState(true)
  // A failed read is not an empty one: no "no arrays" and no Create button
  // until a read succeeds.
  const [loadError, setLoadError] = useState<ApiError | null>(null)

  // Create dialog
  const [createOpen, setCreateOpen] = useState(false)
  const [newName, setNewName] = useState('')
  const [newLevel, setNewLevel] = useState('raid1')
  const [selectedDevices, setSelectedDevices] = useState<string[]>([])
  // Set by a press of Create with something missing: from then on it says so.
  const [createTried, setCreateTried] = useState(false)
  // mdadm --create writes over every member: typed confirmation first.
  const [createConfirmOpen, setCreateConfirmOpen] = useState(false)

  // Delete dialog
  const [deleteTarget, setDeleteTarget] = useState<RAIDArray | null>(null)

  // Add disk dialog
  const [addDiskOpen, setAddDiskOpen] = useState(false)
  const [addDiskArray, setAddDiskArray] = useState<RAIDArray | null>(null)
  const [addDiskDevice, setAddDiskDevice] = useState('')
  const [addDiskTried, setAddDiskTried] = useState(false)

  // Remove disk dialog
  const [removeDiskOpen, setRemoveDiskOpen] = useState(false)
  const [removeDiskArray, setRemoveDiskArray] = useState<RAIDArray | null>(null)
  const [removeDiskDevice, setRemoveDiskDevice] = useState('')

  const fetchArrays = useCallback(async () => {
    try {
      setLoading(true)
      setLoadError(null)
      const data = await api.getRAIDArrays()
      setArrays(data || [])
    } catch (err: unknown) {
      setLoadError(err instanceof Error ? err : new Error(t('disk.raid.fetchFailed')))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchArrays()
  }, [fetchArrays])

  // Members are picked from the devices nothing holds (VG-create pattern), not
  // typed, so a typo can't pull an in-use disk into the array. Unmounted
  // partitions are among them: partition members (e.g. sdb1) are a legitimate
  // layout.
  const { free: candidates, loading: candidatesLoading } = useFreeDevices(createOpen)
  const addDiskDevices = useFreeDevices(addDiskOpen)

  const toggleDeviceSelection = (name: string) => {
    setSelectedDevices((prev) =>
      prev.includes(name) ? prev.filter((d) => d !== name) : [...prev, name]
    )
  }

  const resetCreateForm = () => {
    setNewName('')
    setNewLevel('raid1')
    setSelectedDevices([])
    setCreateTried(false)
  }

  const { run: runCreate, loading: creating } = useApiAction(
    api.createRAID.bind(api),
    {
      successMsg: t('disk.raid.createSuccess'),
      errorMsg: t('disk.raid.createFailed'),
      onSuccess: () => {
        setCreateConfirmOpen(false)
        setCreateOpen(false)
        resetCreateForm()
        void fetchArrays()
      },
    },
  )

  // The server takes no array of fewer than two members.
  const nameMissing = createTried && !newName.trim()
  const devicesShort = createTried && selectedDevices.length < 2

  const handleCreate = () => {
    if (!newName.trim() || selectedDevices.length < 2) {
      setCreateTried(true)
      focusFirst([['raid-name', !newName.trim()], ['raid-devices', selectedDevices.length < 2]])
      return
    }
    setCreateConfirmOpen(true)
  }

  const handleCreateConfirmed = () => {
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
    if (!addDiskArray) return
    if (!addDiskDevice.trim()) {
      setAddDiskTried(true)
      focusFirst([['add-disk-device', true]])
      return
    }
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
    setAddDiskTried(false)
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

  if (loadError) {
    return (
      <div className="mt-4">
        <LoadFailure
          error={loadError}
          title={t('disk.raid.fetchFailed')}
          toolTitle={t('disk.raid.toolMissing')}
          command="apt install mdadm"
          onRetry={fetchArrays}
        />
      </div>
    )
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
                <div className="flex flex-wrap items-center justify-between gap-3">
                  <div className="flex items-center gap-3 min-w-0">
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
                  <div className="flex flex-wrap items-center gap-2">
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
                    <div className="text-[12px] text-muted-foreground">{t('disk.raid.totalDevices')}</div>
                    <div className="text-lg font-bold">{arr.total}</div>
                  </div>
                  <div className="bg-success/5 rounded-lg px-3 py-2 text-center">
                    <div className="text-[12px] text-success">{t('disk.raid.activeDevices')}</div>
                    <div className="text-lg font-bold text-success">{arr.active}</div>
                  </div>
                  <div className="bg-destructive/5 rounded-lg px-3 py-2 text-center">
                    <div className="text-[12px] text-destructive">{t('disk.raid.failedDevices')}</div>
                    <div className="text-lg font-bold text-destructive">{arr.failed}</div>
                  </div>
                  <div className="bg-warning/5 rounded-lg px-3 py-2 text-center">
                    <div className="text-[12px] text-warning">{t('disk.raid.spareDevices')}</div>
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
                        <span className={`inline-flex items-center px-1.5 py-0 rounded text-[12px] font-medium shrink-0 ${memberStateColor(member.state)}`}>
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
                placeholder="md0"
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                aria-invalid={nameMissing || undefined}
                aria-describedby="raid-name-note"
              />
              <FieldNote id="raid-name-note" missing={nameMissing} />
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
              <Label id="raid-devices-label">{t('disk.raid.devices')}</Label>
              {candidatesLoading ? (
                <div className="flex items-center gap-2 text-sm text-muted-foreground py-2">
                  <Loader2 className="h-3.5 w-3.5 animate-spin" />
                  {t('common.loading')}
                </div>
              ) : candidates.length === 0 ? (
                <p className="text-sm text-muted-foreground">{t('disk.raid.noUnusedDisks')}</p>
              ) : (
                <div className="space-y-1.5" role="group" aria-labelledby="raid-devices-label" aria-describedby="raid-devices-note">
                  {candidates.map((d, i) => (
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
                        id={i === 0 ? 'raid-devices' : undefined}
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
              <FieldNote id="raid-devices-note" missing={devicesShort} message={t('disk.raid.pickTwo')} />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => { setCreateOpen(false); resetCreateForm() }}>
              {t('common.cancel')}
            </Button>
            <Button onClick={handleCreate} disabled={creating || (!candidatesLoading && candidates.length === 0)}>
              {creating ? t('common.creating') : t('common.create')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <TypeToConfirmDialog
        open={createConfirmOpen}
        onOpenChange={setCreateConfirmOpen}
        title={t('disk.raid.createConfirmTitle', { name: newName.trim() })}
        description={t('disk.raid.createConfirmDesc', { count: selectedDevices.length, devices: selectedDevices.join(', '), name: newName.trim() })}
        confirmPhrase={newName.trim()}
        confirmLabel={t('disk.raid.createArray')}
        loading={creating}
        onConfirm={handleCreateConfirmed}
      />

      {/* Delete Array Dialog */}
      <TypeToConfirmDialog
        open={!!deleteTarget}
        onOpenChange={(open) => !open && setDeleteTarget(null)}
        title={t('disk.raid.deleteTitle', { name: deleteTarget?.name ?? '' })}
        description={t('disk.raid.deleteConfirmDesc', { name: deleteTarget?.name ?? '' })}
        confirmPhrase={deleteTarget?.name ?? ''}
        confirmLabel={t('disk.raid.deleteArray')}
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
            <FreeDeviceSelect id="add-disk-device" value={addDiskDevice} onChange={setAddDiskDevice} {...addDiskDevices} invalid={addDiskTried && !addDiskDevice} describedBy="add-disk-device-note" />
            <FieldNote id="add-disk-device-note" missing={addDiskTried && !addDiskDevice} />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => { setAddDiskOpen(false); setAddDiskArray(null); setAddDiskDevice('') }}>
              {t('common.cancel')}
            </Button>
            <Button onClick={handleAddDisk} disabled={addingDisk || (!addDiskDevices.loading && addDiskDevices.free.length === 0)}>
              {addingDisk ? t('disk.raid.adding') : t('disk.raid.addDisk')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Remove Disk Dialog */}
      <Dialog open={removeDiskOpen} onOpenChange={(open) => { setRemoveDiskOpen(open); if (!open) { setRemoveDiskArray(null); setRemoveDiskDevice('') } }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('disk.raid.removeDiskTitle', { array: removeDiskArray?.name ?? '' })}</DialogTitle>
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
