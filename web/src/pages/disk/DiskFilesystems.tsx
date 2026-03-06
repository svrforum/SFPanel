import { useState, useEffect, useCallback } from 'react'
import { Trans, useTranslation } from 'react-i18next'
import { RefreshCw, HardDrive, FolderUp, FolderDown, ArrowUpFromLine, ChevronRight, Loader2, TriangleAlert } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { formatBytes } from '@/lib/utils'
import type { Filesystem, ExpandCandidate } from '@/types/api'
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

const FORMAT_FS_TYPES = ['ext4', 'xfs', 'btrfs']

function usageBarColor(percent: number): string {
  if (percent >= 85) return '#f04452'
  if (percent >= 70) return '#f59e0b'
  return '#00c471'
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
  const [formatting, setFormatting] = useState(false)

  // Mount dialog
  const [mountOpen, setMountOpen] = useState(false)
  const [mountDevice, setMountDevice] = useState('')
  const [mountPoint, setMountPoint] = useState('')
  const [mountFsType, setMountFsType] = useState('')
  const [mountOptions, setMountOptions] = useState('')
  const [mounting, setMounting] = useState(false)

  // Unmount
  const [unmountTarget, setUnmountTarget] = useState<Filesystem | null>(null)
  const [unmounting, setUnmounting] = useState(false)

  // Expand dialog
  const [expandOpen, setExpandOpen] = useState(false)
  const [expandCandidates, setExpandCandidates] = useState<ExpandCandidate[]>([])
  const [expandChecking, setExpandChecking] = useState(false)
  const [expandTarget, setExpandTarget] = useState<ExpandCandidate | null>(null)
  const [expanding, setExpanding] = useState(false)

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

  const handleFormat = async () => {
    if (!formatDevice.trim() || !formatFsType) return
    setFormatting(true)
    try {
      await api.formatPartition({
        device: formatDevice.trim(),
        fs_type: formatFsType,
        label: formatLabel.trim() || undefined,
      })
      toast.success(t('disk.filesystems.formatSuccess'))
      setFormatOpen(false)
      resetFormatForm()
      await fetchFilesystems()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.filesystems.formatFailed')
      toast.error(message)
    } finally {
      setFormatting(false)
    }
  }

  const handleMount = async () => {
    if (!mountDevice.trim() || !mountPoint.trim()) return
    setMounting(true)
    try {
      await api.mountFilesystem({
        device: mountDevice.trim(),
        mount_point: mountPoint.trim(),
        fs_type: mountFsType.trim() || undefined,
        options: mountOptions.trim() || undefined,
      })
      toast.success(t('disk.filesystems.mountSuccess'))
      setMountOpen(false)
      resetMountForm()
      await fetchFilesystems()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.filesystems.mountFailed')
      toast.error(message)
    } finally {
      setMounting(false)
    }
  }

  const handleUnmount = async () => {
    if (!unmountTarget) return
    setUnmounting(true)
    try {
      await api.unmountFilesystem(unmountTarget.mount_point)
      toast.success(t('disk.filesystems.unmountSuccess'))
      setUnmountTarget(null)
      await fetchFilesystems()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.filesystems.unmountFailed')
      toast.error(message)
    } finally {
      setUnmounting(false)
    }
  }

  const handleExpandCheck = async () => {
    setExpandOpen(true)
    setExpandChecking(true)
    setExpandTarget(null)
    setExpandCandidates([])
    try {
      const data = await api.checkFilesystemExpand()
      setExpandCandidates(data || [])
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.filesystems.expandFailed')
      toast.error(message)
    } finally {
      setExpandChecking(false)
    }
  }

  const handleExpand = async () => {
    if (!expandTarget) return
    setExpanding(true)
    try {
      await api.expandFilesystem(expandTarget.source)
      toast.success(t('disk.filesystems.expandSuccess'))
      setExpandOpen(false)
      setExpandTarget(null)
      setExpandCandidates([])
      await fetchFilesystems()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('disk.filesystems.expandFailed')
      toast.error(message)
    } finally {
      setExpanding(false)
    }
  }

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
          {filesystems.length}
        </span>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" onClick={fetchFilesystems} disabled={loading} className="rounded-xl">
            <RefreshCw className={loading ? 'animate-spin' : ''} />
            {t('common.refresh')}
          </Button>
          <Button variant="outline" size="sm" onClick={() => setMountOpen(true)} className="rounded-xl">
            <FolderUp className="h-3.5 w-3.5" />
            {t('disk.filesystems.mount')}
          </Button>
          <Button variant="outline" size="sm" onClick={handleExpandCheck} className="rounded-xl">
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
                      fs.use_percent >= 85 ? 'text-[#f04452]' : fs.use_percent >= 70 ? 'text-[#f59e0b]' : 'text-muted-foreground'
                    }`}>
                      {fs.use_percent.toFixed(0)}%
                    </span>
                  </div>
                </TableCell>
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
                placeholder="e.g., /dev/sdb1"
                value={formatDevice}
                onChange={(e) => setFormatDevice(e.target.value)}
              />
            </div>
            <div className="space-y-2">
              <Label htmlFor="format-fs">{t('disk.filesystems.fsType')}</Label>
              <select
                id="format-fs"
                value={formatFsType}
                onChange={(e) => setFormatFsType(e.target.value)}
                className="flex h-9 w-full rounded-xl border-0 bg-secondary/50 px-3 py-1 text-[13px] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/20"
              >
                {FORMAT_FS_TYPES.map((fs) => (
                  <option key={fs} value={fs}>{fs}</option>
                ))}
              </select>
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
            <Button variant="destructive" onClick={handleFormat} disabled={formatting || !formatDevice.trim()}>
              {formatting ? t('disk.filesystems.formatting') : t('disk.filesystems.format')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

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
                placeholder="e.g., /dev/sdb1"
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
            <div className="flex gap-3 rounded-xl bg-[#f59e0b]/10 p-3">
              <TriangleAlert className="h-4 w-4 text-[#f59e0b] flex-shrink-0 mt-0.5" />
              <p className="text-[12px] text-[#f59e0b]/90 leading-relaxed">
                {t('disk.filesystems.unmountWarning')}
              </p>
            </div>

            {/* Root / system mount warning */}
            {unmountTarget && ['/', '/boot', '/home', '/var', '/tmp', '/usr', '/etc'].some(
              p => unmountTarget.mount_point === p || unmountTarget.mount_point.startsWith('/run')
            ) && (
              <div className="flex gap-3 rounded-xl bg-[#f04452]/10 p-3">
                <TriangleAlert className="h-4 w-4 text-[#f04452] flex-shrink-0 mt-0.5" />
                <p className="text-[12px] text-[#f04452]/90 leading-relaxed font-medium">
                  {t('disk.filesystems.unmountRootWarning')}
                </p>
              </div>
            )}
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
      <Dialog open={expandOpen} onOpenChange={(open) => {
        setExpandOpen(open)
        if (!open) {
          setExpandTarget(null)
          setExpandCandidates([])
        }
      }}>
        <DialogContent className="sm:max-w-[520px]">
          <DialogHeader>
            <DialogTitle>{t('disk.filesystems.expandTitle')}</DialogTitle>
            <DialogDescription>{t('disk.filesystems.expandDescription')}</DialogDescription>
          </DialogHeader>

          {expandChecking ? (
            <div className="flex items-center justify-center py-8 gap-2 text-muted-foreground">
              <Loader2 className="h-4 w-4 animate-spin" />
              <span className="text-[13px]">{t('disk.filesystems.expandChecking')}</span>
            </div>
          ) : expandTarget ? (
            /* Selected target: show steps and confirm */
            <div className="space-y-4">
              <div className="bg-muted/30 rounded-xl p-4 text-[13px]">
                <div className="grid grid-cols-2 gap-x-4 gap-y-2">
                  <span className="text-muted-foreground">{t('disk.filesystems.source')}</span>
                  <span className="font-mono font-medium truncate" title={expandTarget.source}>{expandTarget.source}</span>
                  <span className="text-muted-foreground">{t('disk.filesystems.fsType')}</span>
                  <span className="font-mono">{expandTarget.fstype}{expandTarget.is_lvm && (
                    <span className="ml-1.5 text-[10px] px-1.5 py-0.5 rounded bg-primary/10 text-primary font-medium">
                      {t('disk.filesystems.expandLVM')}
                    </span>
                  )}</span>
                  <span className="text-muted-foreground">{t('disk.filesystems.expandCurrentSize')}</span>
                  <span className="font-mono">{formatBytes(expandTarget.current_size)}</span>
                  <span className="text-muted-foreground">{t('disk.filesystems.expandFreeSpace')}</span>
                  <span className="font-mono font-semibold text-[#00c471]">+{formatBytes(expandTarget.free_space)}</span>
                </div>
              </div>

              <div>
                <p className="text-[11px] text-muted-foreground uppercase tracking-wider mb-2">
                  {t('disk.filesystems.expandSteps')}
                </p>
                <div className="space-y-1.5">
                  {expandTarget.steps.map((step, i) => (
                    <div key={i} className="flex items-start gap-2 bg-muted/20 rounded-lg px-3 py-2">
                      <span className="flex-shrink-0 w-5 h-5 rounded-full bg-primary/10 text-primary text-[11px] font-semibold flex items-center justify-center mt-0.5">
                        {i + 1}
                      </span>
                      <div className="min-w-0">
                        <p className="text-[12px] font-medium font-mono">{step.command}</p>
                        <p className="text-[11px] text-muted-foreground">{step.description}</p>
                      </div>
                    </div>
                  ))}
                </div>
              </div>

              <p className="text-[12px] text-muted-foreground">{t('disk.filesystems.expandConfirm')}</p>
            </div>
          ) : expandCandidates.length === 0 ? (
            /* No candidates */
            <div className="flex flex-col items-center justify-center py-8 text-center">
              <ArrowUpFromLine className="h-8 w-8 text-muted-foreground/40 mb-3" />
              <p className="text-[13px] font-medium text-muted-foreground">{t('disk.filesystems.expandNoTarget')}</p>
              <p className="text-[11px] text-muted-foreground/70 mt-1 max-w-[300px]">{t('disk.filesystems.expandNoTargetDesc')}</p>
            </div>
          ) : (
            /* Candidate list */
            <div className="space-y-2">
              {expandCandidates.map((c) => (
                <button
                  key={c.source}
                  type="button"
                  className="w-full text-left bg-muted/20 hover:bg-muted/40 rounded-xl p-4 transition-colors group"
                  onClick={() => setExpandTarget(c)}
                >
                  <div className="flex items-center justify-between">
                    <div className="min-w-0">
                      <p className="text-[13px] font-medium font-mono truncate" title={c.source}>{c.source}</p>
                      <div className="flex items-center gap-2 mt-1">
                        <span className="inline-flex items-center px-1.5 py-0 rounded text-[10px] font-medium border border-border">
                          {c.fstype}
                        </span>
                        {c.is_lvm && (
                          <span className="text-[10px] px-1.5 py-0.5 rounded bg-primary/10 text-primary font-medium">
                            {t('disk.filesystems.expandLVM')}
                          </span>
                        )}
                        <span className="text-[11px] text-muted-foreground">
                          {formatBytes(c.current_size)}
                        </span>
                        <span className="text-[11px] font-semibold text-[#00c471]">
                          +{formatBytes(c.free_space)}
                        </span>
                      </div>
                      <p className="text-[11px] text-muted-foreground mt-1">
                        {c.steps.length} {t('disk.filesystems.expandSteps').toLowerCase()}
                      </p>
                    </div>
                    <ChevronRight className="h-4 w-4 text-muted-foreground group-hover:text-foreground transition-colors flex-shrink-0" />
                  </div>
                </button>
              ))}
            </div>
          )}

          <DialogFooter>
            {expandTarget ? (
              <>
                <Button variant="outline" onClick={() => setExpandTarget(null)}>
                  {t('common.back')}
                </Button>
                <Button onClick={handleExpand} disabled={expanding}>
                  {expanding ? (
                    <>
                      <Loader2 className="h-3.5 w-3.5 animate-spin" />
                      {t('disk.filesystems.expanding')}
                    </>
                  ) : t('disk.filesystems.expand')}
                </Button>
              </>
            ) : (
              <Button variant="outline" onClick={() => setExpandOpen(false)}>
                {t('common.close')}
              </Button>
            )}
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
