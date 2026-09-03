import { useState, useEffect, useCallback } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { HardDrive, FolderUp, FolderDown, ArrowUpFromLine, TriangleAlert } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
import { useApiAction } from '@/hooks/useApiAction'
import type { Filesystem } from '@/types/api'
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
import { ExpandFilesystemDialog } from './components/ExpandFilesystemDialog'
import { NativeSelect } from './components/NativeSelect'
import { TabLoading, CountPill, RefreshButton } from './components/TabToolbar'

const FORMAT_FS_TYPES = ['ext4', 'xfs', 'btrfs']

// Mount points whose unmount would take down the running system — unmounting
// them gets an extra destructive warning in the confirm dialog.
const SYSTEM_MOUNTS = ['/', '/boot', '/home', '/var', '/tmp', '/usr', '/etc']

function isSystemMount(mountPoint: string): boolean {
  return SYSTEM_MOUNTS.includes(mountPoint) || mountPoint.startsWith('/run')
}

function usageBarColor(percent: number): string {
  if (percent >= 85) return 'var(--destructive)'
  if (percent >= 70) return 'var(--warning)'
  return 'var(--success)'
}

export default function DiskFilesystems() {
  const { t } = useTranslation()
  const [filesystems, setFilesystems] = useState<Filesystem[]>([])
  const [loading, setLoading] = useState(true)

  // Format dialog
  const [formatOpen, setFormatOpen] = useState(false)
  const [formatDevice, setFormatDevice] = useState('')
  const [formatFsType, setFormatFsType] = useState('ext4')
  const [formatLabel, setFormatLabel] = useState('')
  const [formatConfirmOpen, setFormatConfirmOpen] = useState(false)

  // Mount dialog
  const [mountOpen, setMountOpen] = useState(false)
  const [mountDevice, setMountDevice] = useState('')
  const [mountPoint, setMountPoint] = useState('')
  const [mountFsType, setMountFsType] = useState('')
  const [mountOptions, setMountOptions] = useState('')
  // Default on: someone mounting a disk from the panel almost always means it
  // to still be there tomorrow, and the surprise runs the other way — a data
  // disk that silently vanished on the next boot.
  const [mountPersist, setMountPersist] = useState(true)
  const [unmountForget, setUnmountForget] = useState(true)

  // Unmount
  const [unmountTarget, setUnmountTarget] = useState<Filesystem | null>(null)

  // Expand dialog
  const [expandOpen, setExpandOpen] = useState(false)

  const fetchFilesystems = useCallback(async () => {
    try {
      setLoading(true)
      const data = await api.getFilesystems()
      setFilesystems(data || [])
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.filesystems.fetchFailed')
      toast.error(message)
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    fetchFilesystems()
  }, [fetchFilesystems])

  const resetFormatForm = () => {
    setFormatDevice('')
    setFormatFsType('ext4')
    setFormatLabel('')
  }

  const resetMountForm = () => {
    setMountDevice('')
    setMountPoint('')
    setMountFsType('')
    setMountOptions('')
    setMountPersist(true)
  }

  const { run: runFormat, loading: formatting } = useApiAction(
    api.formatPartition.bind(api),
    {
      successMsg: t('disk.filesystems.formatSuccess'),
      errorMsg: t('disk.filesystems.formatFailed'),
      onSuccess: () => {
        setFormatConfirmOpen(false)
        setFormatOpen(false)
        resetFormatForm()
        void fetchFilesystems()
      },
    },
  )

  const handleFormat = () => {
    if (!formatDevice.trim() || !formatFsType) return
    void runFormat({
      device: formatDevice.trim(),
      fs_type: formatFsType,
      label: formatLabel.trim() || undefined,
    })
  }

  const { run: runMount, loading: mounting } = useApiAction(
    api.mountFilesystem.bind(api),
    {
      successMsg: t('disk.filesystems.mountSuccess'),
      errorMsg: t('disk.filesystems.mountFailed'),
      onSuccess: () => {
        setMountOpen(false)
        resetMountForm()
        void fetchFilesystems()
      },
    },
  )

  const handleMount = () => {
    if (!mountDevice.trim() || !mountPoint.trim()) return
    void runMount({
      device: mountDevice.trim(),
      mount_point: mountPoint.trim(),
      fs_type: mountFsType.trim() || undefined,
      options: mountOptions.trim() || undefined,
      persist: mountPersist,
    })
  }

  const { run: runUnmount, loading: unmounting } = useApiAction(
    api.unmountFilesystem.bind(api),
    {
      successMsg: t('disk.filesystems.unmountSuccess'),
      errorMsg: t('disk.filesystems.unmountFailed'),
      onSuccess: () => {
        setUnmountTarget(null)
        void fetchFilesystems()
      },
    },
  )

  const handleUnmount = () => {
    // Forget the fstab entry too, or the disk comes back at the next boot and
    // the unmount looks like it did not take.
    if (unmountTarget) void runUnmount(unmountTarget.mount_point, unmountForget)
  }

  if (loading) {
    return <TabLoading />
  }

  return (
    <div className="space-y-4 mt-4">
      {/* Toolbar */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <CountPill>{filesystems.length}</CountPill>
        <div className="flex flex-wrap items-center gap-2">
          <RefreshButton onClick={fetchFilesystems} loading={loading} />
          <Button variant="outline" size="sm" onClick={() => setMountOpen(true)} className="rounded-xl">
            <FolderUp className="h-3.5 w-3.5" />
            {t('disk.filesystems.mount')}
          </Button>
          <Button variant="outline" size="sm" onClick={() => setExpandOpen(true)} className="rounded-xl">
            <ArrowUpFromLine className="h-3.5 w-3.5" />
            {t('disk.filesystems.expand')}
          </Button>
          <Button size="sm" onClick={() => setFormatOpen(true)} className="rounded-xl">
            <HardDrive className="h-3.5 w-3.5" />
            {t('disk.filesystems.format')}
          </Button>
        </div>
      </div>

      {/* Filesystems Table */}
      <div className="bg-card rounded-2xl card-shadow overflow-hidden">
        <Table>
          <TableHeader>
            <TableRow className="border-border/50">
              <TableHead className="text-[11px]">{t('disk.filesystems.source')}</TableHead>
              <TableHead className="text-[11px]">{t('disk.filesystems.fsType')}</TableHead>
              <TableHead className="text-[11px]">{t('disk.filesystems.size')}</TableHead>
              <TableHead className="text-[11px]">{t('disk.filesystems.used')}</TableHead>
              <TableHead className="text-[11px]">{t('disk.filesystems.available')}</TableHead>
              <TableHead className="text-[11px] min-w-[160px]">{t('disk.filesystems.usage')}</TableHead>
              <TableHead className="text-[11px]">{t('disk.filesystems.mountPoint')}</TableHead>
              <TableHead className="text-right text-[11px]">{t('common.actions')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {filesystems.length === 0 && (
              <TableRow>
                <TableCell colSpan={8} className="text-center text-muted-foreground py-8">
                  {t('disk.filesystems.empty')}
                </TableCell>
              </TableRow>
            )}
            {filesystems.map((fs) => (
              <TableRow key={`${fs.source}-${fs.mount_point}`}>
                <TableCell className="font-medium font-mono text-sm max-w-[180px] truncate" title={fs.source}>
                  {fs.source}
                </TableCell>
                <TableCell>
                  <span className="inline-flex items-center px-1.5 py-0 rounded text-[10px] font-medium border border-border">
                    {fs.fstype}
                  </span>
                </TableCell>
                {fs.unresponsive ? (
                  <TableCell colSpan={4}>
                    {/* The server behind this share did not answer. Saying so
                        beats a row of zeros, which reads as an empty disk. */}
                    <span className="inline-flex items-center gap-1.5 rounded-full bg-destructive/10 px-2 py-0.5 text-[11px] font-medium text-destructive">
                      <span className="h-1.5 w-1.5 rounded-full bg-destructive" aria-hidden="true" />
                      {t('disk.filesystems.unresponsive')}
                    </span>
                  </TableCell>
                ) : (<>
                <TableCell className="text-muted-foreground text-sm">{formatBytes(fs.size)}</TableCell>
                <TableCell className="text-muted-foreground text-sm">{formatBytes(fs.used)}</TableCell>
                <TableCell className="text-muted-foreground text-sm">{formatBytes(fs.available)}</TableCell>
                <TableCell>
                  <div className="flex items-center gap-2">
                    <div className="flex-1 h-2 bg-secondary rounded-full overflow-hidden">
                      <div
                        className="h-full rounded-full transition-all duration-500"
                        style={{
                          width: `${Math.min(fs.use_percent, 100)}%`,
                          backgroundColor: usageBarColor(fs.use_percent),
                        }}
                      />
                    </div>
                    <span className={`text-xs font-medium min-w-[36px] text-right ${
                      fs.use_percent >= 85 ? 'text-destructive' : fs.use_percent >= 70 ? 'text-warning' : 'text-muted-foreground'
                    }`}>
                      {fs.use_percent.toFixed(0)}%
                    </span>
                  </div>
                </TableCell>
                </>)}
                <TableCell className="text-muted-foreground font-mono text-xs max-w-[150px] truncate" title={fs.mount_point}>
                  {fs.mount_point}
                </TableCell>
                <TableCell className="text-right">
                  <div className="flex items-center justify-end gap-1">
                    {fs.mount_point && (
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        title={t('disk.filesystems.unmount')}
                        aria-label={t('disk.filesystems.unmount')}
                        onClick={() => setUnmountTarget(fs)}
                      >
                        <FolderDown />
                      </Button>
                    )}
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      {/* Format Dialog */}
      <Dialog open={formatOpen} onOpenChange={(open) => { setFormatOpen(open); if (!open) resetFormatForm() }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('disk.filesystems.formatTitle')}</DialogTitle>
            <DialogDescription>{t('disk.filesystems.formatDescription')}</DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="format-device">{t('disk.filesystems.device')}</Label>
              <Input
                id="format-device"
                placeholder="e.g., sdb1"
                value={formatDevice}
                onChange={(e) => setFormatDevice(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="format-fs">{t('disk.filesystems.fsType')}</Label>
              <NativeSelect
                id="format-fs"
                value={formatFsType}
                onChange={(e) => setFormatFsType(e.target.value)}
                className="w-full"
              >
                {FORMAT_FS_TYPES.map((fs) => (
                  <option key={fs} value={fs}>{fs}</option>
                ))}
              </NativeSelect>
            </div>
            <div className="space-y-2">
              <Label htmlFor="format-label">{t('disk.filesystems.label')}</Label>
              <Input
                id="format-label"
                placeholder={t('disk.filesystems.labelPlaceholder')}
                value={formatLabel}
                onChange={(e) => setFormatLabel(e.target.value)}
              />
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => { setFormatOpen(false); resetFormatForm() }}>
              {t('common.cancel')}
            </Button>
            <Button variant="destructive" onClick={() => setFormatConfirmOpen(true)} disabled={formatting || !formatDevice.trim()}>
              {formatting ? t('disk.filesystems.formatting') : t('disk.filesystems.format')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Format Type-to-Confirm */}
      <TypeToConfirmDialog
        open={formatConfirmOpen}
        onOpenChange={setFormatConfirmOpen}
        title={t('disk.filesystems.formatConfirmTitle')}
        description={t('disk.filesystems.formatConfirmDesc', { device: formatDevice })}
        confirmPhrase={formatDevice}
        confirmLabel={t('disk.filesystems.format')}
        loading={formatting}
        onConfirm={handleFormat}
      />

      {/* Mount Dialog */}
      <Dialog open={mountOpen} onOpenChange={(open) => { setMountOpen(open); if (!open) resetMountForm() }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('disk.filesystems.mountTitle')}</DialogTitle>
            <DialogDescription>{t('disk.filesystems.mountDescription')}</DialogDescription>
          </DialogHeader>
          <div className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="mount-device">{t('disk.filesystems.device')}</Label>
              <Input
                id="mount-device"
                placeholder="e.g., sdb1"
                value={mountDevice}
                onChange={(e) => setMountDevice(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="mount-point">{t('disk.filesystems.mountPoint')}</Label>
              <Input
                id="mount-point"
                placeholder="e.g., /mnt/data"
                value={mountPoint}
                onChange={(e) => setMountPoint(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="mount-fstype">{t('disk.filesystems.fsType')}</Label>
              <Input
                id="mount-fstype"
                placeholder={t('disk.filesystems.autoDetect')}
                value={mountFsType}
                onChange={(e) => setMountFsType(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="mount-options">{t('disk.filesystems.options')}</Label>
              <Input
                id="mount-options"
                placeholder="e.g., defaults,noatime"
                value={mountOptions}
                onChange={(e) => setMountOptions(e.target.value)}
              />
            </div>
            <label className="flex cursor-pointer items-start gap-3">
              <input
                type="checkbox"
                checked={mountPersist}
                onChange={(e) => setMountPersist(e.target.checked)}
                className="mt-0.5 h-4 w-4 rounded border-border accent-primary"
              />
              <span>
                <span className="block text-[13px] font-medium">{t('disk.filesystems.persist')}</span>
                <span className="block text-[11px] text-muted-foreground">{t('disk.filesystems.persistDesc')}</span>
              </span>
            </label>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => { setMountOpen(false); resetMountForm() }}>
              {t('common.cancel')}
            </Button>
            <Button onClick={handleMount} disabled={mounting || !mountDevice.trim() || !mountPoint.trim()}>
              {mounting ? t('disk.filesystems.mounting') : t('disk.filesystems.mount')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Unmount Confirmation Dialog */}
      <Dialog open={!!unmountTarget} onOpenChange={(open) => !open && setUnmountTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('disk.filesystems.unmountTitle')}</DialogTitle>
            <DialogDescription>
              <Trans
                i18nKey="disk.filesystems.unmountConfirm"
                values={{ device: unmountTarget?.source ?? '', mountPoint: unmountTarget?.mount_point ?? '' }}
                components={{ strong: <span className="font-semibold font-mono" /> }}
              />
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-3">
            {/* General warning */}
            <div className="flex gap-3 rounded-xl bg-warning/10 p-3">
              <TriangleAlert className="h-4 w-4 text-warning flex-shrink-0 mt-0.5" />
              <p className="text-[12px] text-warning/90 leading-relaxed">
                {t('disk.filesystems.unmountWarning')}
              </p>
            </div>

            {/* Root / system mount warning */}
            {unmountTarget && isSystemMount(unmountTarget.mount_point) && (
              <div className="flex gap-3 rounded-xl bg-destructive/10 p-3">
                <TriangleAlert className="h-4 w-4 text-destructive flex-shrink-0 mt-0.5" />
                <p className="text-[12px] text-destructive/90 leading-relaxed font-medium">
                  {t('disk.filesystems.unmountRootWarning')}
                </p>
              </div>
            )}
            {/* The fstab side of the same action. Leaving the entry behind
                means the disk returns at the next boot, which reads as the
                unmount not having worked. Only entries this panel wrote are
                touched — a hand-written one is left alone by the server. */}
            <label className="flex cursor-pointer items-start gap-3">
              <input
                type="checkbox"
                checked={unmountForget}
                onChange={(e) => setUnmountForget(e.target.checked)}
                className="mt-0.5 h-4 w-4 rounded border-border accent-primary"
              />
              <span>
                <span className="block text-[13px] font-medium">{t('disk.filesystems.forget')}</span>
                <span className="block text-[11px] text-muted-foreground">{t('disk.filesystems.forgetDesc')}</span>
              </span>
            </label>
          </div>

          <DialogFooter>
            <Button variant="outline" onClick={() => setUnmountTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button variant="destructive" onClick={handleUnmount} disabled={unmounting}>
              {unmounting ? t('disk.filesystems.unmounting') : t('disk.filesystems.unmount')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Expand Dialog */}
      <ExpandFilesystemDialog open={expandOpen} onOpenChange={setExpandOpen} onExpanded={fetchFilesystems} />
    </div>
  )
}
